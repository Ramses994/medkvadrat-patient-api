package middleware

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

// AllowCIDRs rejects requests whose RemoteAddr is outside the allowlist.
type AllowCIDRs struct {
	Nets []*net.IPNet
}

func ParseCIDRList(csv []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(csv))
	for _, raw := range csv {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			ip := net.ParseIP(raw)
			if ip == nil {
				return nil, &net.ParseError{Type: "IP address", Text: raw}
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			raw = ip.String() + "/" + strconv.Itoa(bits)
		}
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func (a AllowCIDRs) Allowed(ipStr string) bool {
	if len(a.Nets) == 0 {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range a.Nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (a AllowCIDRs) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := staffsession.ClientIP(r.RemoteAddr)
		if !a.Allowed(ip) {
			response.Error(w, http.StatusForbidden, "FORBIDDEN", "Доступ запрещён")
			return
		}
		next.ServeHTTP(w, r)
	})
}
