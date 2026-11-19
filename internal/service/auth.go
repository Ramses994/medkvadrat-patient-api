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
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
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

	pilot := s.Config.Auth.Mode == "pilot"
	if pilot && !inList(s.Config.Auth.PilotWhitelist, phone) {
		return handler.OTPRequestResult{}, apperr.New(http.StatusForbidden, "PILOT_ONLY", "Личный кабинет пока недоступен")
	}
	if pilot && !config.ValidPilotCode(s.Config.Auth.PilotCode) {
		s.Logger.Error("otp pilot code is not configured", "hint", "set AUTH_PILOT_CODE")
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}

	// Fetch candidates from MSSQL (once) and store in otp_requests row.
	pats, err := s.Repos.Patient.OTPPatientsByPhone(ctx, phone)
	if err != nil {
		s.Logger.Error("otp patients query failed", "err", err)
		return handler.OTPRequestResult{}, apperr.New(http.StatusBadGateway, "INTERNAL", "Не получилось связаться с клиникой, попробуйте ещё раз через минуту")
	}
	pats = repo.FilterAdultOTPPatients(pats, now)
	groups := groupOTPProfiles(pats, pilot)
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
	if len(groups) == 0 && s.Config.Auth.Mode == "dev" {
		s.Logger.Warn(
			"otp request has no address: patient not found or email missing",
			"hint", "set PATIENTS.EMAIL on dev MSSQL",
		)
	}
	if pilot && len(groups) != 1 {
		reason := "several_addresses"
		if len(pats) == 0 {
			reason = "no_card"
		}
		s.Logger.Warn("otp pilot request has no address", "reason", reason)
		groups = nil
	}

	secret := s.Config.OTP.HMACSecret
	if secret == "" {
		secret = "dev"
	}
	addresses := make([]otp.AddressGroup, 0, len(groups))
	codes := make([]string, 0, len(groups))
	for _, g := range groups {
		code := s.Config.Auth.PilotCode
		if !pilot {
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
		Whitelisted:    pilot,
		Format:         otp.FormatAddresses,
		IssuedMode:     s.Config.Auth.Mode,
	}
	if err := otpRepo.Create(ctx, row); err != nil {
		s.Logger.Error("otp request insert failed", "err", err)
		return handler.OTPRequestResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}

	if !pilot {
		for i, g := range groups {
			if g.email == "" {
				continue
			}
			s.enqueueOTPMail(otpMailJob{requestID: reqID, email: g.email, code: codes[i], expiresAt: expiresAt})
		}
	}

	res := handler.OTPRequestResult{
		RequestID: reqID,
		TTL:       ttl,
		Message:   otpSentMessage,
		Delivery:  otpDelivery(s.Config.Auth.Mode),
	}
	if pilot {
		res.Message = otpPilotMessage
	}
	if s.Config.Auth.Mode == "dev" && len(codes) == 1 {
		res.DevCode = codes[0]
	}
	return res, nil
}

func (s *Services) OTPVerify(ctx context.Context, requestID, code string) (handler.OTPVerifyResult, error) {
	return s.otpVerify(ctx, auth.ClientMobile, requestID, code, "")
}

// WebOTPVerify is OTPVerify for the web cabinet: the refresh token gets the web lifetime and binding.
// previousRefresh is the current lk_refresh cookie, if any; a live prior web session is revoked on login.
func (s *Services) WebOTPVerify(ctx context.Context, requestID, code, previousRefresh string) (handler.OTPVerifyResult, error) {
	return s.otpVerify(ctx, auth.ClientWeb, requestID, code, previousRefresh)
}

func (s *Services) otpVerify(ctx context.Context, client auth.Client, requestID, code, previousRefresh string) (handler.OTPVerifyResult, error) {
	now := time.Now()
	otpRepo := otp.NewRepo(s.SQLite)
	row, err := otpRepo.Get(ctx, requestID)
	if err != nil {
		if err == sql.ErrNoRows {
			return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
		}
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if s.staleOTPRow(row) {
		return handler.OTPVerifyResult{}, errOTPStale()
	}
	if now.After(row.ExpiresAt) {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_EXPIRED", "Код истёк, запросите новый")
	}
	if row.UsedAt.Valid {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	spent, err := otpRepo.SpendAttempt(ctx, requestID)
	if err != nil {
		s.Logger.Error("otp attempt update failed", "err", err)
		return handler.OTPVerifyResult{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	if !spent {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_ATTEMPTS_EXCEEDED", "Слишком много попыток, запросите новый код")
	}
	secret := s.Config.OTP.HMACSecret
	if secret == "" {
		secret = "dev"
	}
	groups, err := otp.DecodeAddresses(row)
	if err != nil {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	matched, n := matchAddress(groups, otp.HashCodeHMAC(code, secret))
	if n != 1 {
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
	return s.finishVerified(ctx, client, requestID, row.Phone, matched.Candidates, previousRefresh)
}

func (s *Services) OTPSelectPatient(ctx context.Context, requestID string, patientID int64) (handler.Tokens, error) {
	return s.otpSelectPatient(ctx, auth.ClientMobile, requestID, patientID, "")
}

// WebOTPSelectPatient is OTPSelectPatient for the web cabinet.
func (s *Services) WebOTPSelectPatient(ctx context.Context, requestID string, patientID int64, previousRefresh string) (handler.Tokens, error) {
	return s.otpSelectPatient(ctx, auth.ClientWeb, requestID, patientID, previousRefresh)
}

func (s *Services) otpSelectPatient(ctx context.Context, client auth.Client, requestID string, patientID int64, previousRefresh string) (handler.Tokens, error) {
	now := time.Now()
	otpRepo := otp.NewRepo(s.SQLite)
	row, err := otpRepo.Get(ctx, requestID)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
	}
	if s.staleOTPRow(row) {
		return handler.Tokens{}, errOTPStale()
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
	tok, err := s.consumeAndIssue(ctx, client, requestID, patientID, row.Phone, previousRefresh)
	if err != nil {
		if errors.Is(err, otp.ErrConsumed) {
			return handler.Tokens{}, apperr.New(http.StatusUnauthorized, "OTP_INVALID", "Неверный код")
		}
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	return tok, nil
}

func (s *Services) Refresh(ctx context.Context, refresh string) (handler.Tokens, error) {
	row, claims, err := s.liveRefresh(ctx, auth.ClientMobile, refresh)
	if err != nil {
		return handler.Tokens{}, err
	}
	_ = auth.NewRefreshStore(s.SQLite).Revoke(ctx, claims.JTI, time.Now())
	tok, err := s.issueTokens(ctx, auth.ClientMobile, row.PatientID, "")
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	return tok, nil
}

const refreshRaceWindow = 10 * time.Second

// WebRefresh rotates a web cabinet refresh token. Absolute session TTL is not extended; idle window resets.
// expectedSession, when set, must match the cookie session or the call returns 409 SESSION_CHANGED.
// A token rotated less than refreshRaceWindow ago yields 409 REFRESH_RACE without killing the sid.
func (s *Services) WebRefresh(ctx context.Context, refresh, expectedSession string) (handler.Tokens, error) {
	row, claims, err := s.liveRefresh(ctx, auth.ClientWeb, refresh)
	if err != nil {
		return handler.Tokens{}, err
	}
	if expectedSession != "" && expectedSession != row.Sid {
		return handler.Tokens{}, apperr.ErrSessionChanged
	}
	now := time.Now()
	tx, err := s.SQLite.BeginTx(ctx, nil)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	defer tx.Rollback()
	revoked, err := auth.RevokeOnce(ctx, tx, claims.JTI, now)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	if !revoked {
		if s.refreshRace(ctx, tx, claims.JTI, row.Sid, now) {
			_ = tx.Rollback()
			return handler.Tokens{}, apperr.ErrRefreshRace
		}
		_ = auth.RevokeSid(ctx, tx, row.Sid, now)
		_ = tx.Commit()
		return handler.Tokens{}, apperr.ErrUnauthorized
	}
	tok, err := s.issueTokensExec(ctx, tx, auth.ClientWeb, row.PatientID, "", row.Sid, row.SessionExpiresAt)
	if err != nil {
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	if err := tx.Commit(); err != nil {
		return handler.Tokens{}, apperr.New(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
	return tok, nil
}

// liveRefresh checks a refresh token: signature, stored row, not revoked or expired, same AUTH_MODE and client.
// For web, a token rotated within refreshRaceWindow yields REFRESH_RACE; an older replay kills the sid.
func (s *Services) liveRefresh(ctx context.Context, client auth.Client, refresh string) (auth.RefreshRow, auth.RefreshClaims, error) {
	now := time.Now()
	secret := []byte(s.Config.JWT.Secret)
	if len(secret) == 0 {
		secret = []byte("dev")
	}
	claims, err := auth.ParseRefresh(secret, refresh)
	if err != nil {
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	store := auth.NewRefreshStore(s.SQLite)
	row, err := store.Get(ctx, claims.JTI)
	if err != nil {
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	if row.TokenHash != auth.HashToken(refresh) {
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	if row.Mode != s.Config.Auth.Mode || row.Client != client {
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	if row.RevokedAt.Valid {
		if client == auth.ClientWeb && s.refreshRace(ctx, s.SQLite, claims.JTI, row.Sid, now) {
			return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrRefreshRace
		}
		if client == auth.ClientWeb && row.Sid != "" {
			_ = auth.RevokeSid(ctx, s.SQLite, row.Sid, now)
		}
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	if now.After(row.ExpiresAt) {
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	if client == auth.ClientWeb && !row.SessionExpiresAt.IsZero() && !now.Before(row.SessionExpiresAt) {
		_ = auth.RevokeSid(ctx, s.SQLite, row.Sid, now)
		return auth.RefreshRow{}, auth.RefreshClaims{}, apperr.ErrUnauthorized
	}
	return row, claims, nil
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

// WebLogout revokes every refresh token for the web session (sid), including tokens issued after this one.
// When expectedSession is set and does not match the cookie session, nothing is revoked and clearCookie is false.
func (s *Services) WebLogout(ctx context.Context, refresh, expectedSession string) (clearCookie bool, err error) {
	if refresh == "" {
		return expectedSession == "", nil
	}
	secret := []byte(s.Config.JWT.Secret)
	if len(secret) == 0 {
		secret = []byte("dev")
	}
	claims, err := auth.ParseRefresh(secret, refresh)
	if err != nil {
		return expectedSession == "", nil
	}
	store := auth.NewRefreshStore(s.SQLite)
	row, err := store.Get(ctx, claims.JTI)
	if err != nil {
		return expectedSession == "", nil
	}
	if row.TokenHash != auth.HashToken(refresh) {
		return expectedSession == "", nil
	}
	if expectedSession != "" && expectedSession != row.Sid {
		return false, nil
	}
	now := time.Now()
	if row.Sid != "" {
		return true, auth.RevokeSid(ctx, s.SQLite, row.Sid, now)
	}
	return true, store.Revoke(ctx, claims.JTI, now)
}

func (s *Services) issueTokens(ctx context.Context, client auth.Client, patientID int64, phone string) (handler.Tokens, error) {
	return s.issueTokensExec(ctx, s.SQLite, client, patientID, phone, "", time.Time{})
}

func (s *Services) issueTokensExec(ctx context.Context, exec auth.Execer, client auth.Client, patientID int64, phone, sid string, sessionExp time.Time) (handler.Tokens, error) {
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
	if client == auth.ClientWeb {
		abs := s.Config.Auth.WebRefreshTTL
		if abs <= 0 {
			abs = 12 * time.Hour
		}
		idle := s.Config.Auth.WebIdleMin
		if idle <= 0 {
			idle = 20 * time.Minute
		}
		if sid == "" {
			var err error
			sid, err = auth.NewJTI()
			if err != nil {
				return handler.Tokens{}, err
			}
			sessionExp = now.Add(abs)
		}
		if sessionExp.IsZero() {
			sessionExp = now.Add(abs)
		}
		remaining := sessionExp.Sub(now)
		if remaining <= 0 {
			return handler.Tokens{}, apperr.ErrUnauthorized
		}
		// Ц1: web access must not outlive the absolute session.
		if accessTTL > remaining {
			accessTTL = remaining
		}
		idleExp := now.Add(idle)
		if idleExp.After(sessionExp) {
			idleExp = sessionExp
		}
		refreshTTL = idleExp.Sub(now)
		if refreshTTL <= 0 {
			return handler.Tokens{}, apperr.ErrUnauthorized
		}
	}

	mode := s.Config.Auth.Mode
	// Web cabinet returns the access token in the response body; never embed the phone claim.
	if client == auth.ClientWeb {
		phone = ""
	}
	access, err := auth.IssueAccess(secret, issuer, patientID, phone, mode, accessTTL, now)
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
	in := auth.RefreshInsert{
		JTI: jti, PatientID: patientID, TokenHash: auth.HashToken(refresh), Mode: mode, Client: client,
		ExpiresAt: now.Add(refreshTTL),
	}
	if client == auth.ClientWeb {
		in.Sid = sid
		in.SessionExpiresAt = sessionExp
	}
	if err := auth.InsertRefresh(ctx, exec, in); err != nil {
		return handler.Tokens{}, err
	}
	tok := handler.Tokens{
		Access:           access,
		Refresh:          refresh,
		AccessExpiresIn:  int(accessTTL.Seconds()),
		RefreshExpiresIn: int(refreshTTL.Seconds()),
	}
	if client == auth.ClientWeb {
		tok.Session = sid
	}
	return tok, nil
}

type otpProfileGroup struct {
	email      string
	candidates []otp.CandidatesItem
}

func groupOTPProfiles(pats []repo.OTPPatient, whitelisted bool) []*otpProfileGroup {
	var groups []*otpProfileGroup
	byEmail := map[string]*otpProfileGroup{}
	add := func(key, email string, item otp.CandidatesItem) {
		g, ok := byEmail[key]
		if !ok {
			g = &otpProfileGroup{email: email}
			byEmail[key] = g
			groups = append(groups, g)
		}
		g.candidates = append(g.candidates, item)
	}
	for _, p := range pats {
		email := strings.TrimSpace(p.Email)
		if email == "" {
			if whitelisted {
				add("", "", otp.CandidatesItem{PatientID: p.PatientID, FullName: p.FullName})
			}
			continue
		}
		add(otpAddressKey(email), email, otp.CandidatesItem{
			PatientID:   p.PatientID,
			FullName:    p.FullName,
			MaskedEmail: otp.MaskEmail(email),
		})
	}
	if whitelisted && len(groups) == 0 {
		for _, p := range pats {
			add("", "", otp.CandidatesItem{PatientID: p.PatientID, FullName: p.FullName})
		}
	}
	return groups
}

func (s *Services) finishVerified(ctx context.Context, client auth.Client, requestID, phone string, cands []otp.CandidatesItem, previousRefresh string) (handler.OTPVerifyResult, error) {
	if len(cands) == 0 {
		return handler.OTPVerifyResult{}, apperr.New(http.StatusNotFound, "PATIENT_NOT_FOUND", "Пациент не найден")
	}
	if len(cands) == 1 {
		tok, err := s.consumeAndIssue(ctx, client, requestID, cands[0].PatientID, phone, previousRefresh)
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
			Session:          tok.Session,
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

func (s *Services) consumeAndIssue(ctx context.Context, client auth.Client, requestID string, patientID int64, phone, previousRefresh string) (handler.Tokens, error) {
	now := time.Now()
	tx, err := s.SQLite.BeginTx(ctx, nil)
	if err != nil {
		return handler.Tokens{}, err
	}
	defer tx.Rollback()
	if err := otp.NewRepo(s.SQLite).Consume(ctx, tx, requestID, patientID, now); err != nil {
		return handler.Tokens{}, err
	}
	if client == auth.ClientWeb {
		if err := s.revokeLiveWebCookie(ctx, tx, previousRefresh, now); err != nil {
			return handler.Tokens{}, err
		}
	}
	tok, err := s.issueTokensExec(ctx, tx, client, patientID, phone, "", time.Time{})
	if err != nil {
		return handler.Tokens{}, err
	}
	if err := tx.Commit(); err != nil {
		return handler.Tokens{}, err
	}
	return tok, nil
}

// refreshRace is true when a revoked token is replayed while another live token still exists for the sid
// and the revocation is younger than refreshRaceWindow (concurrent refresh, not logout).
func (s *Services) refreshRace(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jti, sid string, now time.Time) bool {
	row, err := auth.GetRefresh(ctx, q, jti)
	if err != nil || !row.RevokedAt.Valid {
		return false
	}
	if now.Sub(row.RevokedAt.Time) >= refreshRaceWindow {
		return false
	}
	live, err := auth.SidHasLive(ctx, q, sid)
	return err == nil && live
}

// revokeLiveWebCookie revokes the whole prior web session carried by previousRefresh.
// The sid is taken from the token row even when that token was already rotated (Щ1).
func (s *Services) revokeLiveWebCookie(ctx context.Context, exec auth.Execer, previousRefresh string, now time.Time) error {
	if previousRefresh == "" {
		return nil
	}
	secret := []byte(s.Config.JWT.Secret)
	if len(secret) == 0 {
		secret = []byte("dev")
	}
	claims, err := auth.ParseRefresh(secret, previousRefresh)
	if err != nil {
		return nil
	}
	q, ok := exec.(interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	})
	if !ok {
		return nil
	}
	row, err := auth.GetRefresh(ctx, q, claims.JTI)
	if err != nil || row.TokenHash != auth.HashToken(previousRefresh) {
		return nil
	}
	if row.Client != auth.ClientWeb || row.Sid == "" {
		return nil
	}
	return auth.RevokeSid(ctx, exec, row.Sid, now)
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

const (
	otpSentMessage  = "Если номер зарегистрирован в клинике, код отправлен на e-mail из карточки"
	otpPilotMessage = "Введите код доступа пилота"
)

func otpDelivery(mode string) string {
	if mode == "pilot" {
		return "pilot"
	}
	return "email"
}

// staleOTPRow is true for a row written before per-address codes or in another AUTH_MODE.
func (s *Services) staleOTPRow(row otp.RequestRow) bool {
	return row.Format != otp.FormatAddresses || row.IssuedMode != s.Config.Auth.Mode
}

func errOTPStale() error {
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
