package web

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"example.invalid/hackplan/internal/config"
)

type secureRequestKey struct{}

type networkBoundary struct {
	allowedHosts map[string]struct{}
	allowAnyHost bool
	trusted      []*net.IPNet
}

func newNetworkBoundary(cfg config.Config) (*networkBoundary, error) {
	allowedHosts := cfg.HTTP.AllowedHosts
	if len(allowedHosts) == 0 {
		if base, err := url.Parse(cfg.BaseURL); err == nil && base.Hostname() != "" {
			allowedHosts = []string{base.Hostname()}
		} else {
			allowedHosts = []string{"example.com"}
		}
	}
	boundary := &networkBoundary{allowedHosts: make(map[string]struct{}, len(allowedHosts))}
	for _, host := range allowedHosts {
		normalized := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
		if normalized == "*" {
			boundary.allowAnyHost = true
			continue
		}
		boundary.allowedHosts[normalized] = struct{}{}
	}
	for _, value := range cfg.HTTP.TrustedProxyCIDRs {
		_, cidr, err := net.ParseCIDR(value)
		if err != nil {
			return nil, err
		}
		boundary.trusted = append(boundary.trusted, cidr)
	}
	return boundary, nil
}

func (boundary *networkBoundary) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		host := request.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
		if _, ok := boundary.allowedHosts[host]; !boundary.allowAnyHost && !ok {
			http.Error(response, "Ungültiger Host.", http.StatusBadRequest)
			return
		}
		source := remoteIP(request.RemoteAddr)
		secure := request.TLS != nil
		client := source
		if source != nil && boundary.isTrusted(source) {
			if proto := firstForwardedValue(request.Header.Get("X-Forwarded-Proto")); proto == "https" {
				secure = true
			}
			client = boundary.forwardedClient(request.Header.Get("X-Forwarded-For"), source)
		}
		if client != nil {
			request.RemoteAddr = client.String()
		}
		ctx := context.WithValue(request.Context(), secureRequestKey{}, secure)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func (boundary *networkBoundary) forwardedClient(value string, source net.IP) net.IP {
	chain := make([]net.IP, 0, 8)
	for _, item := range strings.Split(value, ",") {
		if len(chain) == 8 {
			break
		}
		if ip := net.ParseIP(strings.TrimSpace(item)); ip != nil {
			chain = append(chain, ip)
		}
	}
	chain = append(chain, source)
	for index := len(chain) - 1; index >= 0; index-- {
		if !boundary.isTrusted(chain[index]) {
			return chain[index]
		}
	}
	return source
}

func (boundary *networkBoundary) isTrusted(ip net.IP) bool {
	for _, cidr := range boundary.trusted {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(value string) net.IP {
	if host, _, err := net.SplitHostPort(strings.TrimSpace(value)); err == nil {
		value = host
	}
	return net.ParseIP(strings.Trim(strings.TrimSpace(value), "[]"))
}

func firstForwardedValue(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ",", 2)[0]))
}

func requestIsSecure(request *http.Request) bool {
	secure, _ := request.Context().Value(secureRequestKey{}).(bool)
	return secure
}

func requestLimits(maxBody int64, rate int) func(http.Handler) http.Handler {
	if maxBody <= 0 {
		maxBody = 16 << 20
	}
	if rate <= 0 {
		rate = 600
	}
	limiter := newConfirmationRateLimiter(rate, time.Now)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if !limiter.Allow(request.RemoteAddr) {
				response.Header().Set("Retry-After", "60")
				http.Error(response, "Zu viele Anfragen. Bitte kurz warten.", http.StatusTooManyRequests)
				return
			}
			if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions && request.Body != nil {
				request.Body = http.MaxBytesReader(response, request.Body, maxBody)
				if request.ContentLength != 0 {
					contentType := strings.ToLower(request.Header.Get("Content-Type"))
					if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") && !strings.HasPrefix(contentType, "multipart/form-data") && !strings.HasPrefix(contentType, "application/json") {
						http.Error(response, "Nicht unterstützter Inhaltstyp.", http.StatusUnsupportedMediaType)
						return
					}
				}
			}
			next.ServeHTTP(response, request)
		})
	}
}

func maintenanceMode(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			path := request.URL.Path
			if strings.HasPrefix(path, "/health/") || strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/admin/") || path == "/login" || path == "/logout" || path == "/password" {
				next.ServeHTTP(response, request)
				return
			}
			response.Header().Set("Retry-After", "300")
			response.Header().Set("Cache-Control", "no-store")
			http.Error(response, "HackWerk wird gerade gewartet. Bitte versuchen Sie es später erneut.", http.StatusServiceUnavailable)
		})
	}
}
