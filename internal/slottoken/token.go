package slottoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	formatVersion = 1
	ttl           = 5 * time.Minute
	minSecretLen  = 32
)

var (
	ErrSecret  = errors.New("slot token secret not configured")
	ErrToken   = errors.New("slot token rejected")
	ErrExpired = errors.New("slot token expired")
	ErrPatient = errors.New("slot token patient")
	ErrPair    = errors.New("slot token pair")
)

// Payload is the signed slot claim. Start is Moscow wall time.
type Payload struct {
	V           int    `json:"v"`
	PatientID   int64  `json:"patient_id"`
	PlSubjID    int64  `json:"pl_subj_id"`
	PlExamID    int64  `json:"pl_exam_id"`
	Start       string `json:"start"`
	DurationMin int    `json:"duration_min"`
	BranchID    int    `json:"branch_id"`
	Exp         int64  `json:"exp"`
}

func SecretOK(secret string) bool {
	return len([]byte(secret)) >= minSecretLen
}

func Issue(secret string, p Payload, now time.Time) (string, error) {
	if !SecretOK(secret) {
		return "", ErrSecret
	}
	p.V = formatVersion
	p.Exp = now.Add(ttl).Unix()
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := sign(secret, body)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// Verify checks the signature, expiry, patient and the line/exam the caller asked for.
func Verify(secret, token string, now time.Time, patientID, plSubjID, plExamID int64) (Payload, error) {
	p, err := Parse(secret, token, now, patientID, true)
	if err != nil {
		return Payload{}, err
	}
	if p.PlSubjID != plSubjID || p.PlExamID != plExamID {
		return Payload{}, ErrPair
	}
	return p, nil
}

// Parse checks the signature and patient. Expiry is enforced only when checkExp is set,
// so an idempotent replay can read the claim after the token has lapsed.
func Parse(secret, token string, now time.Time, patientID int64, checkExp bool) (Payload, error) {
	if !SecretOK(secret) {
		return Payload{}, ErrSecret
	}
	body, sig, ok := strings.Cut(token, ".")
	if !ok || body == "" || sig == "" {
		return Payload{}, ErrToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return Payload{}, ErrToken
	}
	want := sign(secret, body)
	if !hmac.Equal(got, want) {
		return Payload{}, ErrToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return Payload{}, ErrToken
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil || p.V != formatVersion {
		return Payload{}, ErrToken
	}
	if checkExp && now.Unix() > p.Exp {
		return Payload{}, ErrExpired
	}
	if p.PatientID != patientID {
		return Payload{}, ErrPatient
	}
	return p, nil
}

func sign(secret, body string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(body))
	return m.Sum(nil)
}
