package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adminUsername          = "admin"
	adminRequestHeader     = "X-Mana-Admin"
	adminLoginFailureLimit = 5
	adminLoginWindow       = time.Minute
	adminLoginClientLimit  = 4096
)

type adminLoginFailures struct {
	mu      sync.Mutex
	entries map[string]adminLoginFailure
	now     func() time.Time
}

type adminLoginFailure struct {
	started time.Time
	count   int
}

func adminSecurity(password string, trustProxyHTTPS bool, trustedProxyCIDRs []netip.Prefix, next http.Handler) http.Handler {
	return newAdminSecurity(password, trustProxyHTTPS, trustedProxyCIDRs, time.Now, next)
}

func newAdminSecurity(password string, trustProxyHTTPS bool, trustedProxyCIDRs []netip.Prefix, now func() time.Time, next http.Handler) http.Handler {
	wantedUsername := sha256.Sum256([]byte(adminUsername))
	wantedPassword := sha256.Sum256([]byte(password))
	failures := &adminLoginFailures{entries: make(map[string]adminLoginFailure), now: now}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")

		trustedProxy := trustProxyHTTPS && requestFromTrustedProxy(request, trustedProxyCIDRs)
		secure := request.TLS != nil || (trustedProxy && forwardedHTTPS(request))
		if secure {
			writer.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if !secure && !loopbackRequest(request) {
			writer.Header().Set("Upgrade", "TLS/1.2, HTTP/1.1")
			http.Error(writer, "HTTPS Required", http.StatusUpgradeRequired)
			return
		}

		client := requestClient(request, trustedProxy)
		if retryAfter, blocked := failures.blocked(client); blocked {
			setRetryAfter(writer, retryAfter)
			http.Error(writer, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		username, suppliedPassword, ok := request.BasicAuth()
		usernameHash := sha256.Sum256([]byte(username))
		passwordHash := sha256.Sum256([]byte(suppliedPassword))
		usernameMatches := subtle.ConstantTimeCompare(usernameHash[:], wantedUsername[:])
		passwordMatches := subtle.ConstantTimeCompare(passwordHash[:], wantedPassword[:])
		if !ok || usernameMatches&passwordMatches != 1 {
			if retryAfter, blocked := failures.record(client); blocked {
				setRetryAfter(writer, retryAfter)
				http.Error(writer, "Too Many Requests", http.StatusTooManyRequests)
				return
			}
			writer.Header().Set("WWW-Authenticate", `Basic realm="Mana Admin", charset="UTF-8"`)
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		failures.clear(client)

		if modifiesState(request.Method) && !validAdminWriteRequest(request) {
			http.Error(writer, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func setRetryAfter(writer http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	writer.Header().Set("Retry-After", strconv.Itoa(seconds))
}

func (failures *adminLoginFailures) blocked(client string) (time.Duration, bool) {
	failures.mu.Lock()
	defer failures.mu.Unlock()
	now := failures.now()
	entry, ok := failures.entries[client]
	if !ok {
		return 0, false
	}
	remaining := adminLoginWindow - now.Sub(entry.started)
	if remaining <= 0 {
		delete(failures.entries, client)
		return 0, false
	}
	return remaining, entry.count >= adminLoginFailureLimit
}

func (failures *adminLoginFailures) record(client string) (time.Duration, bool) {
	failures.mu.Lock()
	defer failures.mu.Unlock()
	now := failures.now()
	entry, ok := failures.entries[client]
	if !ok || now.Sub(entry.started) >= adminLoginWindow {
		if !ok && len(failures.entries) >= adminLoginClientLimit {
			failures.evictOldest()
		}
		entry = adminLoginFailure{started: now}
	}
	entry.count++
	failures.entries[client] = entry
	return adminLoginWindow - now.Sub(entry.started), entry.count >= adminLoginFailureLimit
}

func (failures *adminLoginFailures) evictOldest() {
	var oldestClient string
	var oldest time.Time
	for client, entry := range failures.entries {
		if oldestClient == "" || entry.started.Before(oldest) {
			oldestClient = client
			oldest = entry.started
		}
	}
	delete(failures.entries, oldestClient)
}

func (failures *adminLoginFailures) clear(client string) {
	failures.mu.Lock()
	defer failures.mu.Unlock()
	delete(failures.entries, client)
}

func forwardedHTTPS(request *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https")
}

func requestFromTrustedProxy(request *http.Request, trustedProxyCIDRs []netip.Prefix) bool {
	peer, ok := requestPeer(request)
	if !ok {
		return false
	}
	for _, prefix := range trustedProxyCIDRs {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

func loopbackRequest(request *http.Request) bool {
	requestHost := request.Host
	if parsedHost, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = parsedHost
	}
	requestHost = strings.Trim(requestHost, "[]")
	hostIP := net.ParseIP(requestHost)
	localHost := strings.EqualFold(requestHost, "localhost") || hostIP != nil && hostIP.IsLoopback()

	clientHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		clientHost = request.RemoteAddr
	}
	clientIP := net.ParseIP(strings.Trim(clientHost, "[]"))
	return localHost && clientIP != nil && clientIP.IsLoopback()
}

func requestClient(request *http.Request, trustedProxy bool) string {
	if trustedProxy {
		forwarded := strings.TrimSpace(request.Header.Get("X-Forwarded-For"))
		if !strings.Contains(forwarded, ",") {
			if address, err := netip.ParseAddr(forwarded); err == nil {
				return address.Unmap().String()
			}
		}
	}
	if peer, ok := requestPeer(request); ok {
		return peer.String()
	}
	return "unknown"
}

func requestPeer(request *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func modifiesState(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func validAdminWriteRequest(request *http.Request) bool {
	if request.Header.Get(adminRequestHeader) != "1" {
		return false
	}
	if fetchSite := request.Header.Get("Sec-Fetch-Site"); fetchSite != "" && fetchSite != "same-origin" {
		return false
	}

	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}
