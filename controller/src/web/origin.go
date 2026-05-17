package web

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	cfg "IpacPanel/controller/src/config"
)

type effectiveOrigin struct {
	Scheme   string
	Host     string
	Hostname string
	Port     string
}

// Only trust forwarded host headers from an explicitly trusted reverse proxy.
// This keeps direct access working while still supporting mixed proxy topologies.
func trustedForwardedHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	if !cfg.IsTrustedProxyIP(remoteIP(r)) {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for _, part := range parts {
			segments := strings.Split(part, ";")
			for _, segment := range segments {
				kv := strings.SplitN(strings.TrimSpace(segment), "=", 2)
				if len(kv) != 2 {
					continue
				}
				if !strings.EqualFold(strings.TrimSpace(kv[0]), "host") {
					continue
				}
				host := strings.Trim(strings.TrimSpace(kv[1]), `"`)
				if host != "" {
					return host
				}
			}
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		return ""
	}
	if idx := strings.Index(host, ","); idx >= 0 {
		host = host[:idx]
	}
	return strings.TrimSpace(host)
}

// requestExternalScheme resolves the user-facing scheme.
// In reverse-proxy deployments we must follow trusted forwarded headers instead of backend TLS.
func requestExternalScheme(r *http.Request) string {
	if proto := trustedForwardedProto(r); proto != "" {
		return proto
	}
	if r != nil && r.TLS != nil {
		return "https"
	}
	return "http"
}

// IsExternalHTTPS reports whether the browser-facing request scheme is HTTPS.
// It follows trusted proxy headers so redirect and cookie decisions stay aligned.
func IsExternalHTTPS(r *http.Request) bool {
	return requestExternalScheme(r) == "https"
}

// requestExternalHost resolves the user-facing host, including a forwarded port when present.
func requestExternalHost(r *http.Request) string {
	if host := trustedForwardedHost(r); host != "" {
		return host
	}
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Host)
}

func normalizeOriginPort(scheme string, port string) (string, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	port = strings.TrimSpace(port)
	if port == "" {
		switch scheme {
		case "http":
			return "80", nil
		case "https":
			return "443", nil
		default:
			return "", errors.New(msg.OriginSchemeInvalid)
		}
	}
	value, err := strconv.Atoi(port)
	if err != nil || value <= 0 || value > 65535 {
		return "", errors.New(msg.OriginPortInvalid)
	}
	return strconv.Itoa(value), nil
}

func splitHostPort(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("host 为空")
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", "", err
		}
		raw = strings.TrimSpace(parsed.Host)
		if raw == "" {
			return "", "", fmt.Errorf("host 为空")
		}
	}
	if strings.HasPrefix(raw, "[") && strings.Contains(raw, "]") {
		host, port, err := net.SplitHostPort(raw)
		if err == nil {
			return strings.ToLower(strings.TrimSpace(host)), strings.TrimSpace(port), nil
		}
	}
	host, port, err := net.SplitHostPort(raw)
	if err == nil {
		return strings.ToLower(strings.TrimSpace(host)), strings.TrimSpace(port), nil
	}
	addrErr, ok := err.(*net.AddrError)
	if ok && strings.Contains(strings.ToLower(addrErr.Err), "missing port") {
		return strings.ToLower(strings.TrimSpace(raw)), "", nil
	}
	if strings.Count(raw, ":") >= 2 && !strings.HasPrefix(raw, "[") {
		return strings.ToLower(strings.TrimSpace(raw)), "", nil
	}
	return "", "", err
}

func buildEffectiveOrigin(scheme string, host string) (*effectiveOrigin, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return nil, errors.New(msg.OriginSchemeInvalid)
	}
	hostname, port, err := splitHostPort(host)
	if err != nil {
		return nil, err
	}
	if hostname == "" {
		return nil, fmt.Errorf("host 为空")
	}
	normalizedPort, err := normalizeOriginPort(scheme, port)
	if err != nil {
		return nil, err
	}
	normalizedHost := hostname
	if strings.Contains(normalizedHost, ":") && !strings.HasPrefix(normalizedHost, "[") {
		normalizedHost = "[" + normalizedHost + "]"
	}
	return &effectiveOrigin{
		Scheme:   scheme,
		Host:     normalizedHost + ":" + normalizedPort,
		Hostname: hostname,
		Port:     normalizedPort,
	}, nil
}

// requestEffectiveOrigin builds the canonical external origin used by both same-origin checks
// and cookie security decisions. The comparison target is scheme + hostname + effective port.
func requestEffectiveOrigin(r *http.Request) (*effectiveOrigin, error) {
	return buildEffectiveOrigin(requestExternalScheme(r), requestExternalHost(r))
}

func sameOriginURL(origin *url.URL, requestOrigin *effectiveOrigin) bool {
	if origin == nil || requestOrigin == nil {
		return false
	}
	originEffective, err := buildEffectiveOrigin(origin.Scheme, origin.Host)
	if err != nil {
		return false
	}
	return originEffective.Scheme == requestOrigin.Scheme && originEffective.Hostname == requestOrigin.Hostname && originEffective.Port == requestOrigin.Port
}

func IsSameOriginRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if originURL.Scheme == "" || originURL.Host == "" {
		return false
	}
	requestOrigin, err := requestEffectiveOrigin(r)
	if err != nil {
		return false
	}
	return sameOriginURL(originURL, requestOrigin)
}

func RequireSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if IsSameOriginRequest(r) {
		return true
	}
	WriteAPIError(w, http.StatusForbidden, "同源校验失败", nil)
	return false
}
