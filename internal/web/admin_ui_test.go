package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"mana/internal/menu"
)

func TestAdminEditorPageAndAssets(t *testing.T) {
	handler, err := NewDynamicWithAdmin(
		func() (map[string]menu.Loader, error) { return map[string]menu.Loader{}, nil },
		os.DirFS("../.."),
		os.DirFS("../../web/static"),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminOptions{Password: "secret", Content: newStubAdminContent()},
	)
	if err != nil {
		t.Fatal(err)
	}

	redirect := httptest.NewRequest(http.MethodGet, "https://mana.example/admin", nil)
	redirect.SetBasicAuth("admin", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, redirect)
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "/admin/" {
		t.Fatalf("admin redirect = %d %q", response.Code, response.Header().Get("Location"))
	}

	page := httptest.NewRequest(http.MethodGet, "https://mana.example/admin/", nil)
	page.SetBasicAuth("admin", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, page)
	if response.Code != http.StatusOK {
		t.Fatalf("admin page status = %d: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{"Menu editor", `id="event-select"`, `id="yaml-editor"`, `id="validate-button"`, `id="publish-button"`, "/admin/static/admin.css", "/admin/static/admin.js"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("admin page missing %q", expected)
		}
	}
	if strings.Contains(response.Body.String(), "conference: alpha") {
		t.Fatal("admin page embedded menu YAML before the authenticated API request")
	}

	for _, asset := range []string{"admin.css", "admin.js"} {
		request := httptest.NewRequest(http.MethodGet, "https://mana.example/admin/static/"+asset, nil)
		request.SetBasicAuth("admin", "secret")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Errorf("admin asset %s response = %d, %d bytes", asset, response.Code, response.Body.Len())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("admin asset %s can be cached", asset)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "https://mana.example/admin/static/admin.js", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin asset status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "https://mana.example/admin/static/secret.yaml", nil)
	request.SetBasicAuth("admin", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown admin asset status = %d", response.Code)
	}
}
