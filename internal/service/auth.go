package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/auth"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/ratelimit"
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

	// Fetch candidates from MSSQL (once) and store in otp_requests row.
	pats, err := s.Repos.Patient.OTPPatientsByPhone(ctx, phone[len(phone)-10:])
	if err != nil {
		s.Logger.Error("otp patients query failed", "err", err)
		return handler.OTPRequestResult{}, apperr.New(http.StatusBadGateway, "INTERNAL", "Не получилось связаться с клиникой, попробуйте ещё раз через минуту")
	}

	// Filter to recipients with EMAIL
	var candidates []otp.CandidatesItem
	emailSet := map[string]struct{}{}
	var maskedDest []string

	for _, p := range pats {
		email := strings.TrimSpace(p.Email)
		if email == "" {
			// In dev mode we still allow OTP to proceed without email to unblock local testing.
			// In pilot whitelist we also allow to proceed without email delivery.
			if s.Config.Auth.Mode == "dev" || (s.Config.Auth.Mode == "pilot" && inList(s.Config.Auth.PilotWhitelist, phone)) {
				candidates = append(candidates, otp.CandidatesItem{
					PatientID:   p.PatientID,
					FullName:    p.FullName,
					MaskedEmail: "no-email",
				})
			}
			continue
		}
		masked := otp.MaskEmail(email)
		candidates = append(candidates, otp.CandidatesItem{
			PatientID:   p.PatientID,
			FullName:    p.FullName,
			MaskedEmail: masked,
		})
		if _, ok := emailSet[email]; !ok {
			emailSet[email] = struct{}{}
			maskedDest = append(maskedDest, masked)
		}
		// audit log (flags are orthogonal to OTP)
		s.Logger.Info("otp.send.audit",
			"patient_id", p.PatientID,
			"channel", "email",
			"not_send_mailing", p.NotSendMailingSMS,
			"send_auto_email", p.SendAutoEmail,
		)
	}

	whitelisted := s.Config.Auth.Mode == "pilot" && inList(s.Config.Auth.PilotWhitelist, phone)

	if len(maskedDest) == 0 {
		if s.Config.Auth.Mode == "dev" && len(candidates) > 0 {
			s.Logger.Warn("otp request in dev without patient email", "phone", phone)
			maskedDest = []string{"dev-no-email"}
		} else if whitelisted && len(pats) > 0 {
			maskedDest = []string{"pilot-whitelist"}
		} else {
			return handler.OTPRequestResult{}, apperr.New(http.StatusBadRequest, "EMAIL_NOT_SET", "Для входа в личный кабинет обратитесь в регистратуру, чтобы добавить email в карточку пациента. Телефон: +7 (499) 288-88-14")
		}
	}
	code := ""
	if whitelisted {
		code = "000000"
	} else {
		c, err := otp.RandomCode6()
		if err != nil {
			return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		code = c
	}

	secret := s.Config.OTP.HMACSecret
	if secret == "" {
		// dev default, but still hashed for uniformity
		secret = "dev"
	}
	codeHash := otp.HashCodeHMAC(code, secret)

	reqID, err := auth.NewJTI()
	if err != nil {
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	ttl := s.Config.OTP.TTLSeconds
	if ttl <= 0 {
		ttl = 300
	}
	expiresAt := now.Add(time.Duration(ttl) * time.Second)

	candJSON, _ := json.Marshal(candidates)
	otpRepo := otp.NewRepo(s.SQLite)
	row := otp.RequestRow{
		RequestID:      reqID,
		Phone:          phone,
		CodeHash:       codeHash,
		CandidatesJSON: string(candJSON),
		Attempts:       0,
		ExpiresAt:      expiresAt,
		Whitelisted:    whitelisted,
	}
	if err := otpRepo.Create(ctx, row); err != nil {
		s.Logger.Error("otp request insert failed", "err", err)
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}

	// Send OTP (dev: do not send email; pilot whitelist: do not send)
	channelName := "email"
	if s.Config.Auth.Mode == "dev" {
		channelName = "dev"
	} else if whitelisted {
		channelName = "pilot"
	}

	if s.Config.Auth.Mode == "dev" {
		_ = otp.DevChannel{Logger: s.Logger}.Send(ctx, otp.Patient{MaskedEmail: maskedDest[0]}, code)
	} else if !whitelisted {
		ch := otp.NewEmailChannel(s.Config.SMTP)
		for email := range emailSet {
			err := ch.Send(ctx, otp.Patient{Email: email, MaskedEmail: otp.MaskEmail(email)}, code)
			if err != nil {
				_ = otpRepo.Delete(ctx, reqID)
				s.Logger.Error("otp email send failed", "err", err)
				return handler.OTPRequestResult{}, apperr.New(http.StatusBadGateway, "EMAIL_SEND_FAILED", "Не удалось отправить письмо, попробуйте ещё раз")
			}
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
	if s.Config.Auth.Mode != "prod" {
		res.DevCode = code
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
	if now.After(row.ExpiresAt) {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_EXPIRED", "Код истёк, запросите новый")
	}
	if row.Attempts >= 3 {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_ATTEMPTS_EXCEEDED", "Слишком много попыток, запросите новый код")
	}

	secret := s.Config.OTP.HMACSecret
	if secret == "" {
		secret = "dev"
	}
	if otp.HashCodeHMAC(code, secret) != row.CodeHash {
		_, _ = otpRepo.IncrementAttempts(ctx, requestID)
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	_ = otpRepo.MarkVerified(ctx, requestID, now)

	cands, err := otpRepo.DecodeCandidates(row)
	if err != nil || len(cands) == 0 {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusNotFound, "PATIENT_NOT_FOUND", "Пациент не найден")
	}

	if len(cands) == 1 {
		tok, err := s.issueTokens(ctx, cands[0].PatientID, row.Phone)
		if err != nil {
			return handler.OTPVerifyResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return handler.OTPVerifyResult{Tokens: &tok}, nil
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

func (s *Services) OTPSelectPatient(ctx context.Context, requestID string, patientID int64) (handler.Tokens, error) {
	now := time.Now()
	otpRepo := otp.NewRepo(s.SQLite)
	row, err := otpRepo.Get(ctx, requestID)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if !row.VerifiedAt.Valid {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if now.After(row.ExpiresAt) {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_EXPIRED", "Код истёк, запросите новый")
	}
	cands, err := otpRepo.DecodeCandidates(row)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	found := false
	for _, c := range cands {
		if c.PatientID == patientID {
			found = true
			break
		}
	}
	if !found {
		return handler.Tokens{}, apperr.New(http.StatusForbidden, "FORBIDDEN", "Forbidden")
	}
	_ = otpRepo.MarkSelectedPatient(ctx, requestID, patientID)
	tok, err := s.issueTokens(ctx, patientID, row.Phone)
	if err != nil {
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
	store := auth.NewRefreshStore(s.SQLite)
	if err := store.Put(ctx, jti, patientID, auth.HashToken(refresh), now.Add(refreshTTL)); err != nil {
		return handler.Tokens{}, err
	}
	return handler.Tokens{Access: access, Refresh: refresh}, nil
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
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(d) == 11 && strings.HasPrefix(d, "8") {
		d = "7" + d[1:]
	}
	if len(d) == 10 {
		d = "7" + d
	}
	if len(d) != 11 || !strings.HasPrefix(d, "7") {
		return ""
	}
	return d
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
