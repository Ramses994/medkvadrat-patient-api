package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ParseWebOrigins reads LK_WEB_ORIGINS: comma-separated scheme://host[:port], no path.
// Empty input yields an empty list (web cabinet requests then get 403 until configured).
func ParseWebOrigins(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		canon, err := NormalizeWebOrigin(p)
		if err != nil {
			return nil, fmt.Errorf("LK_WEB_ORIGINS: %w", err)
		}
		if _, ok := seen[canon]; ok {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	return out, nil
}

// NormalizeWebOrigin validates and returns a canonical origin (lowercase scheme+host, explicit default port omitted).
func NormalizeWebOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid origin %q: scheme must be http or https", raw)
	}
	if u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid origin %q: host required", raw)
	}
	if u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid origin %q: must be scheme://host[:port] only", raw)
	}
	path := u.EscapedPath()
	if path != "" && path != "/" {
		return "", fmt.Errorf("invalid origin %q: path not allowed", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid origin %q: host required", raw)
	}
	port := u.Port()
	scheme := strings.ToLower(u.Scheme)
	if port == "" || isDefaultPort(scheme, port) {
		return scheme + "://" + host, nil
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

func isDefaultPort(scheme, port string) bool {
	return (scheme == "http" && port == "80") || (scheme == "https" && port == "443")
}

// OriginAllowed reports whether Origin exactly matches one of the allowed canonical origins.
func OriginAllowed(origin string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	canon, err := NormalizeWebOrigin(origin)
	if err != nil {
		return false
	}
	for _, a := range allowed {
		if canon == a {
			return true
		}
	}
	return false
}
