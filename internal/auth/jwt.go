package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	Sub   int64  `json:"sub"`
	Phone string `json:"phone"`
	Typ   string `json:"typ"`
	jwt.RegisteredClaims
}

type RefreshClaims struct {
	Sub int64  `json:"sub"`
	Typ string `json:"typ"`
	JTI string `json:"jti"`
	jwt.RegisteredClaims
}

func NewJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func IssueAccess(secret []byte, issuer string, patientID int64, phone string, ttl time.Duration, now time.Time) (string, error) {
	claims := AccessClaims{
		Sub:   patientID,
		Phone: phone,
		Typ:   "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(secret)
}

func IssueRefresh(secret []byte, issuer string, patientID int64, jti string, ttl time.Duration, now time.Time) (string, error) {
	if jti == "" {
		return "", fmt.Errorf("empty jti")
	}
	claims := RefreshClaims{
		Sub: patientID,
		Typ: "refresh",
		JTI: jti,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(secret)
}

func ParseRefresh(secret []byte, tokenStr string) (RefreshClaims, error) {
	var claims RefreshClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected alg")
		}
		return secret, nil
	})
	if err != nil {
		return RefreshClaims{}, err
	}
	if claims.Typ != "refresh" || claims.JTI == "" || claims.Sub == 0 {
		return RefreshClaims{}, fmt.Errorf("invalid refresh claims")
	}
	return claims, nil
}

func ParseAccess(secret []byte, tokenStr string) (AccessClaims, error) {
	var claims AccessClaims
	_, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected alg")
		}
		return secret, nil
	})
	if err != nil {
		return AccessClaims{}, err
	}
	if claims.Typ != "access" || claims.Sub == 0 {
		return AccessClaims{}, fmt.Errorf("invalid access claims")
	}
	return claims, nil
}
