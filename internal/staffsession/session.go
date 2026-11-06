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

// Sign returns userID|version|expUnix|hex(HMAC-SHA256(secret, userID|version|expUnix)).
func Sign(secret []byte, userID int64, version int, exp time.Time) (string, error) {
	if WeakSecret(secret) {
		return "", ErrWeakSecret
	}
	if userID <= 0 {
		return "", errors.New("staff user id")
	}
	if version < 1 {
		return "", errors.New("session version")
	}
	payload := strconv.FormatInt(userID, 10) + "|" + strconv.Itoa(version) + "|" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	return payload + "|" + hex.EncodeToString(mac.Sum(nil)), nil
}

// Open checks the signature and expiry and returns the staff user id and session version.
// A missing or short secret is an error, never a valid session.
func Open(secret []byte, value string, now time.Time) (int64, int, bool, error) {
	if WeakSecret(secret) {
		return 0, 0, false, ErrWeakSecret
	}
	parts := strings.Split(value, "|")
	if len(parts) != 4 {
		return 0, 0, false, nil
	}
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || userID <= 0 {
		return 0, 0, false, nil
	}
	version, err := strconv.Atoi(parts[1])
	if err != nil || version < 1 {
		return 0, 0, false, nil
	}
	expUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, 0, false, nil
	}
	if now.Unix() > expUnix {
		return 0, 0, false, nil
	}
	payload := parts[0] + "|" + parts[1] + "|" + parts[2]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(parts[3])
	if err != nil || len(got) != len(want) {
		return 0, 0, false, nil
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return 0, 0, false, nil
	}
	return userID, version, true, nil
}

// Valid reports whether value is a well-formed, unexpired, correctly signed session.
func Valid(secret []byte, value string, now time.Time) (bool, error) {
	_, _, ok, err := Open(secret, value, now)
	return ok, err
}

// PasswordMatch compares candidate to expected using constant-time digest compare.
func PasswordMatch(expected, candidate string) bool {
	a := sha256.Sum256([]byte(expected))
	b := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// ClientIP returns host from RemoteAddr (no X-Forwarded-For — trust only direct peer).
// UpdatedBy is a non-PII staff marker: "staff" plus 8 hex characters of the cookie MAC.
func UpdatedBy(cookieValue string) string {
	parts := strings.Split(cookieValue, "|")
	if len(parts) != 2 || len(parts[1]) < 8 {
		return "staff"
	}
	return "staff" + parts[1][:8]
}

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
