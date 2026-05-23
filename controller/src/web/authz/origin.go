package authz

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

type EffectiveOrigin struct {
	Scheme   string
	Host     string
	Hostname string
	Port     string
}

type OriginResolver struct{}

func NewOriginResolver() *OriginResolver {
	return &OriginResolver{}
}

func (o *OriginResolver) IsExternalHTTPS(r *http.Request) bool {
	return o.RequestExternalScheme(r) == "https"
}

func (o *OriginResolver) IsSameOriginRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" {
		return false
	}
	requestOrigin, err := o.RequestEffectiveOrigin(r)
	if err != nil {
		return false
	}
	return sameOriginURL(originURL, requestOrigin)
}

func (o *OriginResolver) RequestEffectiveOrigin(r *http.Request) (*EffectiveOrigin, error) {
	return BuildEffectiveOrigin(o.RequestExternalScheme(r), o.RequestExternalHost(r))
}

func (o *OriginResolver) RequestExternalScheme(r *http.Request) string {
	if proto := trustedForwardedProto(r); proto != "" {
		return proto
	}
	if r != nil && r.TLS != nil {
		return "https"
	}
	return "http"
}

func (o *OriginResolver) RequestExternalHost(r *http.Request) string {
	if host := trustedForwardedHost(r); host != "" {
		return host
	}
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Host)
}

func trustedForwardedHost(r *http.Request) string {
	if r == nil || !cfg.IsTrustedProxyIP(remoteIP(r)) {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for _, part := range parts {
			segments := strings.Split(part, ";")
			for _, segment := range segments {
				kv := strings.SplitN(strings.TrimSpace(segment), "=", 2)
				if len(kv) != 2 || !strings.EqualFold(strings.TrimSpace(kv[0]), "host") {
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

func trustedForwardedProto(r *http.Request) string {
	if r == nil || !cfg.IsTrustedProxyIP(remoteIP(r)) {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for _, part := range parts {
			segments := strings.Split(part, ";")
			for _, segment := range segments {
				kv := strings.SplitN(strings.TrimSpace(segment), "=", 2)
				if len(kv) != 2 || !strings.EqualFold(strings.TrimSpace(kv[0]), "proto") {
					continue
				}
				if proto := normalizeForwardedProto(strings.Trim(strings.TrimSpace(kv[1]), `"`)); proto != "" {
					return proto
				}
			}
		}
	}
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		return ""
	}
	if idx := strings.Index(proto, ","); idx >= 0 {
		proto = proto[:idx]
	}
	return normalizeForwardedProto(proto)
}

func normalizeForwardedProto(proto string) string {
	proto = strings.TrimSpace(proto)
	if strings.EqualFold(proto, "https") {
		return "https"
	}
	if strings.EqualFold(proto, "http") {
		return "http"
	}
	return ""
}

func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return remote
}

func NormalizeOriginPort(scheme string, port string) (string, error) {
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

func SplitHostPort(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf(msg.HostEmpty)
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", "", err
		}
		raw = strings.TrimSpace(parsed.Host)
		if raw == "" {
			return "", "", fmt.Errorf(msg.HostEmpty)
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

func BuildEffectiveOrigin(scheme string, host string) (*EffectiveOrigin, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return nil, errors.New(msg.OriginSchemeInvalid)
	}
	hostname, port, err := SplitHostPort(host)
	if err != nil {
		return nil, err
	}
	if hostname == "" {
		return nil, fmt.Errorf(msg.HostEmpty)
	}
	normalizedPort, err := NormalizeOriginPort(scheme, port)
	if err != nil {
		return nil, err
	}
	normalizedHost := hostname
	if strings.Contains(normalizedHost, ":") && !strings.HasPrefix(normalizedHost, "[") {
		normalizedHost = "[" + normalizedHost + "]"
	}
	return &EffectiveOrigin{Scheme: scheme, Host: normalizedHost + ":" + normalizedPort, Hostname: hostname, Port: normalizedPort}, nil
}

func sameOriginURL(origin *url.URL, requestOrigin *EffectiveOrigin) bool {
	if origin == nil || requestOrigin == nil {
		return false
	}
	originEffective, err := BuildEffectiveOrigin(origin.Scheme, origin.Host)
	if err != nil {
		return false
	}
	return originEffective.Scheme == requestOrigin.Scheme && originEffective.Hostname == requestOrigin.Hostname && originEffective.Port == requestOrigin.Port
}
