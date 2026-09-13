package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"manna/internal/menu"
)

func TestAdminSecurityRequiresCorrectBasicAuthentication(t *testing.T) {
	handler := adminSecurity("correct horse battery staple", false, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name     string
		username string
		password string
		want     int
	}{
		{name: "missing credentials", want: http.StatusUnauthorized},
		{name: "wrong username", username: "operator", password: "correct horse battery staple", want: http.StatusUnauthorized},
		{name: "wrong password", username: "admin", password: "wrong", want: http.StatusUnauthorized},
		{name: "correct credentials", username: "admin", password: "correct horse battery staple", want: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/", nil)
			if test.username != "" || test.password != "" {
				request.SetBasicAuth(test.username, test.password)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("admin response can be cached")
			}
			challenge := response.Header().Get("WWW-Authenticate")
			if test.want == http.StatusUnauthorized && challenge != `Basic realm="Manna Admin", charset="UTF-8"` {
				t.Fatalf("WWW-Authenticate = %q", challenge)
			}
			if test.password != "" && strings.Contains(response.Body.String(), test.password) {
				t.Fatal("response contains the supplied password")
			}
		})
	}
}

func TestAdminSecurityProtectsModifyingRequests(t *testing.T) {
	handler := adminSecurity("secret", false, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name      string
		method    string
		header    string
		origin    string
		fetchSite string
		want      int
	}{
		{name: "read needs no integrity header", method: http.MethodGet, want: http.StatusNoContent},
		{name: "write with custom header", method: http.MethodPut, header: "1", want: http.StatusNoContent},
		{name: "same origin browser write", method: http.MethodPost, header: "1", origin: "https://manna.example", fetchSite: "same-origin", want: http.StatusNoContent},
		{name: "missing custom header", method: http.MethodPut, want: http.StatusForbidden},
		{name: "wrong custom header", method: http.MethodPut, header: "true", want: http.StatusForbidden},
		{name: "cross origin", method: http.MethodPost, header: "1", origin: "https://attacker.example", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "same site is not same origin", method: http.MethodPost, header: "1", origin: "https://other.manna.example", fetchSite: "same-site", want: http.StatusForbidden},
		{name: "malformed origin", method: http.MethodDelete, header: "1", origin: "://bad", want: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "https://manna.example/admin/api/events/test", nil)
			request.SetBasicAuth("admin", "secret")
			if test.header != "" {
				request.Header.Set(adminRequestHeader, test.header)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("admin response can be cached")
			}
		})
	}
}

func TestAdminSecurityRequiresHTTPSOutsideLocalhost(t *testing.T) {
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name       string
		target     string
		trustProxy bool
		proxyCIDRs []netip.Prefix
		forwarded  string
		want       int
	}{
		{name: "remote HTTP", target: "http://manna.example/admin/", want: http.StatusUpgradeRequired},
		{name: "spoofed proxy header", target: "http://manna.example/admin/", forwarded: "https", want: http.StatusUpgradeRequired},
		{name: "trusted HTTPS proxy", target: "http://manna.example/admin/", trustProxy: true, proxyCIDRs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, forwarded: "https", want: http.StatusNoContent},
		{name: "untrusted HTTPS proxy", target: "http://manna.example/admin/", trustProxy: true, proxyCIDRs: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, forwarded: "https", want: http.StatusUpgradeRequired},
		{name: "direct HTTPS", target: "https://manna.example/admin/", want: http.StatusNoContent},
		{name: "localhost development", target: "http://localhost:8080/admin/", want: http.StatusNoContent},
		{name: "loopback development", target: "http://127.0.0.1:8080/admin/", want: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := adminSecurity("correct horse battery staple", test.trustProxy, test.proxyCIDRs, next)
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if strings.Contains(test.name, "development") {
				request.RemoteAddr = "127.0.0.1:1234"
			}
			request.SetBasicAuth("admin", "correct horse battery staple")
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwarded)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
			if test.want == http.StatusUpgradeRequired && response.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("insecure response exposed an authentication challenge")
			}
			if (strings.HasPrefix(test.target, "https://") || test.want == http.StatusNoContent && test.trustProxy && test.forwarded == "https") && response.Header().Get("Strict-Transport-Security") == "" {
				t.Fatal("secure response has no HSTS header")
			}
		})
	}
}

func TestAdminSecurityDoesNotTrustSpoofedLocalhostHost(t *testing.T) {
	handler := adminSecurity("correct horse battery staple", false, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://localhost:8080/admin/", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	request.SetBasicAuth("admin", "correct horse battery staple")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("spoofed localhost status = %d", response.Code)
	}
}

func TestAdminSecurityThrottlesFailedLoginsByClient(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	handler := newAdminSecurity("correct horse battery staple", false, nil, func() time.Time { return now }, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	for attempt := 1; attempt <= adminLoginFailureLimit; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		request.SetBasicAuth("admin", "wrong password")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == adminLoginFailureLimit {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.Code, want)
		}
		if want == http.StatusTooManyRequests && response.Header().Get("Retry-After") == "" {
			t.Fatal("throttled response has no Retry-After header")
		}
	}

	blocked := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/", nil)
	blocked.RemoteAddr = "203.0.113.10:5678"
	blocked.SetBasicAuth("admin", "correct horse battery staple")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, blocked)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("correct login during throttle status = %d", response.Code)
	}

	otherClient := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/", nil)
	otherClient.RemoteAddr = "203.0.113.11:1234"
	otherClient.SetBasicAuth("admin", "correct horse battery staple")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, otherClient)
	if response.Code != http.StatusNoContent {
		t.Fatalf("other client status = %d", response.Code)
	}

	now = now.Add(adminLoginWindow + time.Second)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, blocked)
	if response.Code != http.StatusNoContent {
		t.Fatalf("login after throttle window status = %d", response.Code)
	}
}

func TestAdminSecurityUsesSanitizedClientFromTrustedProxy(t *testing.T) {
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	proxyCIDRs := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	handler := newAdminSecurity("correct horse battery staple", true, proxyCIDRs, func() time.Time { return now }, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	for attempt := 1; attempt <= adminLoginFailureLimit; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "http://manna.example/admin/", nil)
		request.RemoteAddr = "192.0.2.10:443"
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("X-Forwarded-For", "203.0.113.10")
		request.SetBasicAuth("admin", "wrong")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}

	otherClient := httptest.NewRequest(http.MethodGet, "http://manna.example/admin/", nil)
	otherClient.RemoteAddr = "192.0.2.10:443"
	otherClient.Header.Set("X-Forwarded-Proto", "https")
	otherClient.Header.Set("X-Forwarded-For", "203.0.113.11")
	otherClient.SetBasicAuth("admin", "correct horse battery staple")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, otherClient)
	if response.Code != http.StatusNoContent {
		t.Fatalf("independent forwarded client status = %d", response.Code)
	}

	spoofedChain := httptest.NewRequest(http.MethodGet, "http://manna.example/admin/", nil)
	spoofedChain.RemoteAddr = "192.0.2.10:443"
	spoofedChain.Header.Set("X-Forwarded-Proto", "https")
	spoofedChain.Header.Set("X-Forwarded-For", "203.0.113.12, 203.0.113.13")
	if got := requestClient(spoofedChain, true); got != "192.0.2.10" {
		t.Fatalf("forwarded chain client = %q; want trusted peer fallback", got)
	}
}

func TestAdminSubtreeIsRegisteredOnlyWhenConfigured(t *testing.T) {
	templates := fstest.MapFS{"web/templates/index.html": {Data: []byte(`{{define "index.html"}}menu{{end}}`)}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	events := func() (map[string]menu.Loader, error) {
		return map[string]menu.Loader{"/test": func() (menu.Config, error) { return menu.Config{}, nil }}, nil
	}

	withoutAdmin, err := NewDynamic(events, templates, fstest.MapFS{}, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	withoutAdmin.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if response.Code != http.StatusNotFound || response.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("disabled admin response = %d, challenge %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}

	withAdmin, err := NewDynamicWithAdmin(events, templates, fstest.MapFS{}, nil, logger, AdminOptions{Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin", "/admin/", "/admin/api/events"} {
		response = httptest.NewRecorder()
		withAdmin.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://manna.example"+path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s status = %d", path, response.Code)
		}
		if strings.Contains(response.Body.String(), "/test") || strings.Contains(response.Body.String(), "yaml") {
			t.Errorf("unauthenticated %s exposed admin content: %s", path, response.Body.String())
		}

		request := httptest.NewRequest(http.MethodGet, "https://manna.example"+path, nil)
		request.SetBasicAuth("admin", "secret")
		response = httptest.NewRecorder()
		withAdmin.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("authenticated placeholder %s status = %d", path, response.Code)
		}
	}

	publicRequest := httptest.NewRequest(http.MethodGet, "/test", nil)
	response = httptest.NewRecorder()
	withAdmin.ServeHTTP(response, publicRequest)
	if response.Code != http.StatusOK {
		t.Fatalf("public menu status with admin enabled = %d", response.Code)
	}
}
