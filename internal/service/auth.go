package service

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/auth"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/ratelimit"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func (s *Services) OTPRequest(ctx context.Context, phoneRaw string, ipRaw string) (handler.OTPRequestResult, error) {
	now := time.Now()
	phone := normalizePhone(phoneRaw)
	if phone == "" {
		return handler.OTPRequestResult{}, apperr.New(http.StatusBadRequest, "VALIDATION", "Введите телефон в формате +7 999 123-45-67")
	}

	ip := normalizeIP(ipRaw)
	// Rate-limit (disabled in dev)
	if s.Config.Auth.Mode != "dev" {
		rl := ratelimit.NewStore(s.SQLite)
		window := time.Duration(s.Config.OTP.RateWindowSeconds) * time.Second
		if window <= 0 {
			window = 15 * time.Minute
		}
		phoneLimit := s.Config.OTP.RatePhoneLimit
		if phoneLimit <= 0 {
			phoneLimit = 3
		}
		ipLimit := s.Config.OTP.RateIPLimit
		if ipLimit <= 0 {
			ipLimit = 10
		}

		ok, err := rl.Allow(ctx, "otp_phone", phone, window, phoneLimit, now)
		if err != nil {
			s.Logger.Error("rate limit phone failed", "err", err)
		}
		if !ok {
			return handler.OTPRequestResult{}, apperr.New(http.StatusTooManyRequests, "RATE_LIMITED", "Слишком много запросов, попробуйте позже")
		}
		ok, err = rl.Allow(ctx, "otp_ip", ip, window, ipLimit, now)
		if err != nil {
			s.Logger.Error("rate limit ip failed", "err", err)
		}
		if !ok {
			return handler.OTPRequestResult{}, apperr.New(http.StatusTooManyRequests, "RATE_LIMITED", "Слишком много запросов, попробуйте позже")
		}
	}

	whitelisted := s.Config.Auth.Mode == "pilot"
	if whitelisted && !inList(s.Config.Auth.PilotWhitelist, phone) {
		return handler.OTPRequestResult{}, apperr.New(http.StatusForbidden, "PILOT_ONLY", "Личный кабинет пока недоступен")
	}

	// Fetch candidates from MSSQL (once) and store in otp_requests row.
	pats, err := s.Repos.Patient.OTPPatientsByPhone(ctx, phone)
	if err != nil {
		s.Logger.Error("otp patients query failed", "err", err)
		return handler.OTPRequestResult{}, apperr.New(http.StatusBadGateway, "INTERNAL", "Не получилось связаться с клиникой, попробуйте ещё раз через минуту")
	}
	if len(pats) == 0 {
		return handler.OTPRequestResult{}, apperr.New(http.StatusNotFound, "PATIENT_NOT_FOUND", "Пациент не найден")
	}

	groups := groupOTPProfiles(pats, whitelisted)
	for _, p := range pats {
		if strings.TrimSpace(p.Email) == "" {
			continue
		}
		s.Logger.Info("otp.send.audit",
			"patient_id", p.PatientID,
			"channel", "email",
			"not_send_mailing", p.NotSendMailingSMS,
			"send_auto_email", p.SendAutoEmail,
		)
	}
	if len(groups) == 0 {
		if s.Config.Auth.Mode == "dev" {
			s.Logger.Warn(
				"otp request blocked: patient email missing",
				"hint", "set PATIENTS.EMAIL on dev MSSQL or use AUTH_MODE=pilot with AUTH_PILOT_WHITELIST for happy-path testing",
			)
		}
		return handler.OTPRequestResult{}, apperr.New(http.StatusBadRequest, "EMAIL_NOT_SET", "Для входа в личный кабинет обратитесь в регистратуру, чтобы добавить email в карточку пациента. Телефон: +7 (499) 288-88-14")
	}
	if whitelisted && len(groups) != 1 {
		return handler.OTPRequestResult{}, apperr.New(http.StatusBadRequest, "PILOT_SINGLE_ADDRESS", "В пилоте вход только для номеров с одним адресом")
	}

	secret := s.Config.OTP.HMACSecret
	if secret == "" {
		secret = "dev"
	}
	addresses := make([]otp.AddressGroup, 0, len(groups))
	codes := make([]string, 0, len(groups))
	for _, g := range groups {
		code := "000000"
		if !whitelisted {
			c, err := uniqueOTPCode(codes)
			if err != nil {
				return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
			}
			code = c
		}
		codes = append(codes, code)
		addresses = append(addresses, otp.AddressGroup{
			CodeHash:   otp.HashCodeHMAC(code, secret),
			Candidates: g.candidates,
		})
	}

	reqID, err := auth.NewJTI()
	if err != nil {
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	ttl := s.Config.OTP.TTLSeconds
	if ttl <= 0 {
		ttl = 300
	}
	expiresAt := now.Add(time.Duration(ttl) * time.Second)

	candJSON, err := json.Marshal(addresses)
	if err != nil {
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	hashJSON, err := json.Marshal(codeHashes(addresses))
	if err != nil {
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	otpRepo := otp.NewRepo(s.SQLite)
	row := otp.RequestRow{
		RequestID:      reqID,
		Phone:          phone,
		CodeHash:       string(hashJSON),
		CandidatesJSON: string(candJSON),
		Attempts:       0,
		ExpiresAt:      expiresAt,
		Whitelisted:    whitelisted,
		Format:         otp.FormatAddresses,
	}
	if err := otpRepo.Create(ctx, row); err != nil {
		s.Logger.Error("otp request insert failed", "err", err)
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}

	channelName := "email"
	if s.Config.Auth.Mode == "dev" {
		channelName = "dev"
	} else if whitelisted {
		channelName = "pilot"
	}

	if !whitelisted {
		for i, g := range groups {
			if g.email == "" {
				continue
			}
			if err := sendOTPMail(ctx, s, g.email, codes[i]); err != nil {
				_ = otpRepo.Delete(ctx, reqID)
				s.Logger.Error("otp email send failed", "err", err)
				return handler.OTPRequestResult{}, apperr.New(http.StatusBadGateway, "EMAIL_SEND_FAILED", "Не удалось отправить письмо, попробуйте ещё раз")
			}
		}
	}

	maskedDest := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.masked != "" {
			maskedDest = append(maskedDest, g.masked)
		}
	}
	var masked interface{} = maskedDest
	if len(maskedDest) == 1 {
		masked = maskedDest[0]
	}
	if whitelisted {
		masked = "pilot-whitelist"
	}

	res := handler.OTPRequestResult{
		RequestID:         reqID,
		TTL:               ttl,
		Channel:           channelName,
		MaskedDestination: masked,
	}
	if s.Config.Auth.Mode == "dev" && len(codes) == 1 {
		res.DevCode = codes[0]
	}
	return res, nil
}

func (s *Services) OTPVerify(ctx context.Context, requestID, code string) (handler.OTPVerifyResult, error) {
	now := time.Now()
	otpRepo := otp.NewRepo(s.SQLite)
	row, err := otpRepo.Get(ctx, requestID)
	if err != nil {
		if err == sql.ErrNoRows {
			return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
		}
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if row.Format != otp.FormatAddresses {
		return handler.OTPVerifyResult{}, errOTPOldFormat()
	}
	if now.After(row.ExpiresAt) {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_EXPIRED", "Код истёк, запросите новый")
	}
	if row.Attempts >= 3 {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_ATTEMPTS_EXCEEDED", "Слишком много попыток, запросите новый код")
	}

	if row.UsedAt.Valid {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	secret := s.Config.OTP.HMACSecret
	if secret == "" {
		secret = "dev"
	}
	groups, err := otp.DecodeAddresses(row)
	if err != nil || len(groups) == 0 {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusNotFound, "PATIENT_NOT_FOUND", "Пациент не найден")
	}
	matched, n := matchAddress(groups, otp.HashCodeHMAC(code, secret))
	if n != 1 {
		_, _ = otpRepo.IncrementAttempts(ctx, requestID)
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if !row.VerifiedAt.Valid {
		locked, err := otpRepo.LockAddress(ctx, requestID, matched, now)
		if err != nil {
			return handler.OTPVerifyResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		if !locked {
			row, err = otpRepo.Get(ctx, requestID)
			if err != nil || row.UsedAt.Valid {
				return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
			}
			groups, err = otp.DecodeAddresses(row)
			if err != nil {
				return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
			}
			matched, n = matchAddress(groups, otp.HashCodeHMAC(code, secret))
			if n != 1 {
				return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
			}
		}
	}
	return s.finishVerified(ctx, requestID, row.Phone, matched.Candidates)
}

func (s *Services) OTPSelectPatient(ctx context.Context, requestID string, patientID int64) (handler.Tokens, error) {
	now := time.Now()
	otpRepo := otp.NewRepo(s.SQLite)
	row, err := otpRepo.Get(ctx, requestID)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if row.Format != otp.FormatAddresses {
		return handler.Tokens{}, errOTPOldFormat()
	}
	if !row.VerifiedAt.Valid {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if row.UsedAt.Valid {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if now.After(row.ExpiresAt) {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_EXPIRED", "Код истёк, запросите новый")
	}
	groups, err := otp.DecodeAddresses(row)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if !addressHasPatient(groups, patientID) {
		return handler.Tokens{}, apperr.New(http.StatusForbidden, "FORBIDDEN", "Forbidden")
	}
	tok, err := s.consumeAndIssue(ctx, requestID, patientID, row.Phone)
	if err != nil {
		if errors.Is(err, otp.ErrConsumed) {
			return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
		}
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	return tok, nil
}

func (s *Services) Refresh(ctx context.Context, refresh string) (handler.Tokens, error) {
	now := time.Now()
	secret := []byte(s.Config.JWT.Secret)
	if len(secret) == 0 {
		secret = []byte("dev")
	}
	claims, err := auth.ParseRefresh(secret, refresh)
	if err != nil {
		return handler.Tokens{}, apperr.ErrUnauthorized
	}
	store := auth.NewRefreshStore(s.SQLite)
	pid, tokenHash, exp, revokedAt, err := store.Get(ctx, claims.JTI)
	if err != nil {
		return handler.Tokens{}, apperr.ErrUnauthorized
	}
	if revokedAt.Valid || now.After(exp) {
		return handler.Tokens{}, apperr.ErrUnauthorized
	}
	if tokenHash != auth.HashToken(refresh) {
		return handler.Tokens{}, apperr.ErrUnauthorized
	}
	_ = store.Revoke(ctx, claims.JTI, now)
	tok, err := s.issueTokens(ctx, pid, "")
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	return tok, nil
}

func (s *Services) Logout(ctx context.Context, refresh string) error {
	secret := []byte(s.Config.JWT.Secret)
	if len(secret) == 0 {
		secret = []byte("dev")
	}
	claims, err := auth.ParseRefresh(secret, refresh)
	if err != nil {
		return nil
	}
	return auth.NewRefreshStore(s.SQLite).Revoke(ctx, claims.JTI, time.Now())
}

func (s *Services) issueTokens(ctx context.Context, patientID int64, phone string) (handler.Tokens, error) {
	return s.issueTokensExec(ctx, s.SQLite, patientID, phone)
}

func (s *Services) issueTokensExec(ctx context.Context, exec auth.Execer, patientID int64, phone string) (handler.Tokens, error) {
	now := time.Now()
	secret := []byte(s.Config.JWT.Secret)
	if len(secret) == 0 {
		secret = []byte("dev")
	}
	issuer := s.Config.JWT.Issuer
	accessTTL := time.Duration(s.Config.JWT.AccessTTLMin) * time.Minute
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	refreshTTL := time.Duration(s.Config.JWT.RefreshTTLDays) * 24 * time.Hour
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}

	access, err := auth.IssueAccess(secret, issuer, patientID, phone, accessTTL, now)
	if err != nil {
		return handler.Tokens{}, err
	}
	jti, err := auth.NewJTI()
	if err != nil {
		return handler.Tokens{}, err
	}
	refresh, err := auth.IssueRefresh(secret, issuer, patientID, jti, refreshTTL, now)
	if err != nil {
		return handler.Tokens{}, err
	}
	if err := auth.InsertRefresh(ctx, exec, jti, patientID, auth.HashToken(refresh), now.Add(refreshTTL)); err != nil {
		return handler.Tokens{}, err
	}
	return handler.Tokens{
		Access:           access,
		Refresh:          refresh,
		AccessExpiresIn:  int(accessTTL.Seconds()),
		RefreshExpiresIn: int(refreshTTL.Seconds()),
	}, nil
}

type otpProfileGroup struct {
	email      string
	masked     string
	candidates []otp.CandidatesItem
}

func groupOTPProfiles(pats []repo.OTPPatient, whitelisted bool) []*otpProfileGroup {
	var groups []*otpProfileGroup
	byEmail := map[string]*otpProfileGroup{}
	add := func(key, email, masked string, item otp.CandidatesItem) {
		g, ok := byEmail[key]
		if !ok {
			g = &otpProfileGroup{email: email, masked: masked}
			byEmail[key] = g
			groups = append(groups, g)
		}
		g.candidates = append(g.candidates, item)
	}
	for _, p := range pats {
		email := strings.TrimSpace(p.Email)
		if email == "" {
			if whitelisted {
				add("", "", "", otp.CandidatesItem{PatientID: p.PatientID, FullName: p.FullName})
			}
			continue
		}
		add(otpAddressKey(email), email, otp.MaskEmail(email), otp.CandidatesItem{
			PatientID:   p.PatientID,
			FullName:    p.FullName,
			MaskedEmail: otp.MaskEmail(email),
		})
	}
	if whitelisted && len(groups) == 0 {
		for _, p := range pats {
			add("", "", "", otp.CandidatesItem{PatientID: p.PatientID, FullName: p.FullName})
		}
	}
	return groups
}

func (s *Services) finishVerified(ctx context.Context, requestID, phone string, cands []otp.CandidatesItem) (handler.OTPVerifyResult, error) {
	if len(cands) == 0 {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusNotFound, "PATIENT_NOT_FOUND", "Пациент не найден")
	}
	if len(cands) == 1 {
		tok, err := s.consumeAndIssue(ctx, requestID, cands[0].PatientID, phone)
		if err != nil {
			if errors.Is(err, otp.ErrConsumed) {
				return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
			}
			return handler.OTPVerifyResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return handler.OTPVerifyResult{
			Access:           tok.Access,
			Refresh:          tok.Refresh,
			AccessExpiresIn:  tok.AccessExpiresIn,
			RefreshExpiresIn: tok.RefreshExpiresIn,
		}, nil
	}
	out := make([]handler.Candidate, 0, len(cands))
	for _, c := range cands {
		out = append(out, handler.Candidate{
			PatientID:   c.PatientID,
			FullName:    c.FullName,
			MaskedEmail: c.MaskedEmail,
		})
	}
	return handler.OTPVerifyResult{Candidates: out}, nil
}

func (s *Services) consumeAndIssue(ctx context.Context, requestID string, patientID int64, phone string) (handler.Tokens, error) {
	tx, err := s.SQLite.BeginTx(ctx, nil)
	if err != nil {
		return handler.Tokens{}, err
	}
	defer tx.Rollback()
	if err := otp.NewRepo(s.SQLite).Consume(ctx, tx, requestID, patientID, time.Now()); err != nil {
		return handler.Tokens{}, err
	}
	tok, err := s.issueTokensExec(ctx, tx, patientID, phone)
	if err != nil {
		return handler.Tokens{}, err
	}
	if err := tx.Commit(); err != nil {
		return handler.Tokens{}, err
	}
	return tok, nil
}

// otpAddressKey keeps the mailbox name as typed: only the domain is case-insensitive.
func otpAddressKey(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return email
	}
	return email[:i+1] + strings.ToLower(email[i+1:])
}

var newOTPCode = otp.RandomCode6

func uniqueOTPCode(taken []string) (string, error) {
	for i := 0; i < 100; i++ {
		c, err := newOTPCode()
		if err != nil {
			return "", err
		}
		if !slices.Contains(taken, c) {
			return c, nil
		}
	}
	return "", errors.New("otp code repeats")
}

func errOTPOldFormat() error {
	return apperr.New(http.StatusUnauthorized, "OTP_EXPIRED", "Код устарел, запросите новый")
}

// matchAddress returns the mailbox whose code matched and how many matched; only one match may be used.
func matchAddress(groups []otp.AddressGroup, hash string) (otp.AddressGroup, int) {
	var found otp.AddressGroup
	n := 0
	for _, g := range groups {
		if len(g.CodeHash) == len(hash) && subtle.ConstantTimeCompare([]byte(g.CodeHash), []byte(hash)) == 1 {
			found = g
			n++
		}
	}
	return found, n
}

func addressHasPatient(groups []otp.AddressGroup, patientID int64) bool {
	for _, g := range groups {
		for _, c := range g.Candidates {
			if c.PatientID == patientID {
				return true
			}
		}
	}
	return false
}

func codeHashes(groups []otp.AddressGroup) []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = g.CodeHash
	}
	return out
}

var sendOTPMail = func(ctx context.Context, s *Services, email, code string) error {
	if s.Config.Auth.Mode == "dev" {
		return otp.DevChannel{Logger: s.Logger}.Send(ctx, otp.Patient{MaskedEmail: otp.MaskEmail(email)}, code)
	}
	return otp.NewEmailChannel(s.Config.SMTP).Send(ctx, otp.Patient{Email: email, MaskedEmail: otp.MaskEmail(email)}, code)
}

func inList(list []string, phone string) bool {
	for _, v := range list {
		if strings.TrimSpace(v) == phone {
			return true
		}
	}
	return false
}

func normalizePhone(phone string) string {
	return repo.NormalizePhone(phone)
}

func normalizeIP(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return "unknown"
	}
	// X-Forwarded-For can have list
	if strings.Contains(remoteAddr, ",") {
		remoteAddr = strings.TrimSpace(strings.Split(remoteAddr, ",")[0])
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	return remoteAddr
}
