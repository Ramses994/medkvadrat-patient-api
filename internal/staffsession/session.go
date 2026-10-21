package staffsession

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	CookieName     = "mk_staff"
	MinSecretBytes = 32
)

var ErrWeakSecret = errors.New("dashboard secret too short")

func WeakSecret(secret []byte) bool {
	return len(secret) < MinSecretBytes
}

// Sign returns a cookie value: expUnix|hex(HMAC-SHA256(secret, expUnix)).
func Sign(secret []byte, exp time.Time) (string, error) {
	if WeakSecret(secret) {
		return "", ErrWeakSecret
	}
	payload := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil)), nil
}

// Valid reports whether value is a well-formed, unexpired, correctly signed session.
// A missing or short secret is an error, never true.
func Valid(secret []byte, value string, now time.Time) (bool, error) {
	if WeakSecret(secret) {
		return false, ErrWeakSecret
	}
	parts := strings.Split(value, "|")
	if len(parts) != 2 {
		return false, nil
	}
	payload, sigHex := parts[0], parts[1]
	expUnix, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false, nil
	}
	if now.Unix() > expUnix {
		return false, nil
	}

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(sigHex)
	if err != nil || len(got) != len(want) {
		return false, nil
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// PasswordMatch compares candidate to expected using constant-time digest compare.
func PasswordMatch(expected, candidate string) bool {
	a := sha256.Sum256([]byte(expected))
	b := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// ClientIP returns host from RemoteAddr (no X-Forwarded-For — trust only direct peer).
func ClientIP(remoteAddr string) string {
	host := remoteAddr
	if i := strings.LastIndex(remoteAddr, ":"); i >= 0 {
		h := remoteAddr[:i]
		h = strings.Trim(h, "[]")
		if h != "" {
			host = h
		}
	}
	return host
}
