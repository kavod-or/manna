package web

import (
	"image"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/boombuler/barcode/qr"
	"go.yaml.in/yaml/v3"

	"manna/internal/menu"
)

func TestAdminEventQRDownloadsSVG(t *testing.T) {
	handler := newAdminAPITestServer(t, newStubAdminContent())
	response := serveAdminAPI(handler, http.MethodGet, "/admin/api/events/alpha/qr.svg", "")

	if response.Code != http.StatusOK {
		t.Fatalf("QR response = %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "image/svg+xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); got != `attachment; filename="alpha-qr.svg"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("QR response can be cached")
	}
	for _, expected := range []string{`<svg xmlns="http://www.w3.org/2000/svg"`, `shape-rendering="crispEdges"`, `<path d="`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("QR SVG missing %q", expected)
		}
	}
	expectedCode, err := qr.Encode("https://manna.example/alpha", qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	expectedSVG, err := renderQRSVG(expectedCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Body.String() != expectedSVG {
		t.Fatal("downloaded QR code does not encode the public event URL")
	}
}

func TestAdminEventQRCanEmbedPublishedLogo(t *testing.T) {
	config := validQRTestConfig("alpha.png")
	encoded, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	content := newStubAdminContent()
	content.files["/alpha"] = string(encoded)
	logo := []byte("\x89PNG\r\n\x1a\n")
	storage := fstest.MapFS{"alpha.png": {Data: logo}}
	handler := newAdminAPITestServerWithStorageAndLogger(t, content, storage, slog.New(slog.NewTextHandler(io.Discard, nil)))

	response := serveAdminAPI(handler, http.MethodGet, "/admin/api/events/alpha/qr.svg?logo=1", "")
	if response.Code != http.StatusOK {
		t.Fatalf("logo QR response = %d: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`href="data:image/png;base64,iVBORw0KGgo="`, `<rect x="`, `preserveAspectRatio="xMidYMid meet"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("logo QR SVG missing %q", expected)
		}
	}

	expectedCode, err := qr.Encode("https://manna.example/alpha", qr.H, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	expectedSVG, err := renderQRSVG(expectedCode, &embeddedQRLogo{content: logo, contentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Body.String() != expectedSVG {
		t.Fatal("logo QR did not use high error correction or the published logo")
	}
}

func TestAdminEventQRRejectsUnknownEventsAndRequiresAuthentication(t *testing.T) {
	handler := newAdminAPITestServer(t, newStubAdminContent())

	missing := serveAdminAPI(handler, http.MethodGet, "/admin/api/events/missing/qr.svg", "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "event not found") {
		t.Fatalf("missing QR response = %d: %s", missing.Code, missing.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/api/events/alpha/qr.svg", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated QR response = %d", response.Code)
	}
}

func TestAdminEventQRRejectsInvalidLogoOptions(t *testing.T) {
	content := newStubAdminContent()
	encoded, err := yaml.Marshal(validQRTestConfig(""))
	if err != nil {
		t.Fatal(err)
	}
	content.files["/alpha"] = string(encoded)
	handler := newAdminAPITestServer(t, content)

	invalid := serveAdminAPI(handler, http.MethodGet, "/admin/api/events/alpha/qr.svg?logo=yes", "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "logo must be 1") {
		t.Fatalf("invalid logo option response = %d: %s", invalid.Code, invalid.Body.String())
	}

	missing := serveAdminAPI(handler, http.MethodGet, "/admin/api/events/alpha/qr.svg?logo=1", "")
	if missing.Code != http.StatusUnprocessableEntity || !strings.Contains(missing.Body.String(), "no published logo") {
		t.Fatalf("missing logo response = %d: %s", missing.Code, missing.Body.String())
	}
}

func validQRTestConfig(logo string) menu.Config {
	return menu.Config{
		Conference: menu.Conference{
			Languages: []string{"en"},
			TimeZone:  "Europe/Berlin",
			Name:      menu.Localized{EN: "Alpha"},
			Location:  menu.Localized{EN: "Hall"},
			Logo:      logo,
		},
		Days: []menu.Day{{
			Date: "2030-01-01",
			Services: []menu.Service{{
				ID: "lunch", Title: menu.Localized{EN: "Lunch"}, Subtitle: menu.Localized{EN: "Food"}, From: "12:00", Until: "13:00",
			}},
		}},
	}
}

func TestPublicEventURLUsesOnlyTrustedHTTPSForwarding(t *testing.T) {
	trusted := netip.MustParsePrefix("192.0.2.0/24")
	server := &server{adminTrustProxyHTTPS: true, adminTrustedProxyCIDRs: []netip.Prefix{trusted}}

	tests := []struct {
		name       string
		target     string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{name: "direct HTTPS", target: "https://menu.example/admin/", want: "https://menu.example/team-day"},
		{name: "trusted proxy", target: "http://menu.example/admin/", remoteAddr: "192.0.2.10:1234", forwarded: "https", want: "https://menu.example/team-day"},
		{name: "untrusted proxy", target: "http://menu.example/admin/", remoteAddr: "198.51.100.10:1234", forwarded: "https", want: "http://menu.example/team-day"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.remoteAddr != "" {
				request.RemoteAddr = test.remoteAddr
			}
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwarded)
			}
			got, err := server.publicEventURL(request, "/team-day")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("public URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRenderQRSVGAddsQuietZoneAndRejectsInvalidMatrices(t *testing.T) {
	matrix := image.NewGray(image.Rect(0, 0, 2, 2))
	matrix.SetGray(0, 0, color.Gray{Y: 0})
	matrix.SetGray(1, 0, color.Gray{Y: 255})
	matrix.SetGray(0, 1, color.Gray{Y: 255})
	matrix.SetGray(1, 1, color.Gray{Y: 0})

	svg, err := renderQRSVG(matrix, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(svg, `viewBox="0 0 10 10"`) || !strings.Contains(svg, "M4 4h1v1H4z") || !strings.Contains(svg, "M5 5h1v1H5z") {
		t.Fatalf("unexpected SVG:\n%s", svg)
	}
	withLogo, err := renderQRSVG(matrix, &embeddedQRLogo{content: []byte{1, 2, 3}, contentType: "image/png"})
	if err != nil || !strings.Contains(withLogo, `href="data:image/png;base64,AQID"`) {
		t.Fatalf("embedded logo missing from SVG: %v\n%s", err, withLogo)
	}
	if _, err := renderQRSVG(image.NewGray(image.Rect(0, 0, 2, 1)), nil); err == nil {
		t.Fatal("accepted a non-square QR matrix")
	}
}
