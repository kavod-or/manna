package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"manna/internal/web"
)

func TestAdminAPIWithRootedEventContent(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	handler, err := web.NewDynamicWithAdmin(
		admin.registry.Current,
		os.DirFS("."),
		fstest.MapFS{},
		admin.registry.content,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		web.AdminOptions{Password: "secret", Content: admin},
	)
	if err != nil {
		t.Fatal(err)
	}

	readRequest := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/api/events/alpha", nil)
	readRequest.SetBasicAuth("admin", "secret")
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status = %d: %s", readResponse.Code, readResponse.Body.String())
	}
	var current struct {
		Revision string `json:"revision"`
	}
	if err := json.NewDecoder(readResponse.Body).Decode(&current); err != nil {
		t.Fatal(err)
	}

	updated := testMenu("Published through HTTP") + "# preserved comment\n"
	body, err := json.Marshal(map[string]string{"yaml": updated, "revision": current.Revision})
	if err != nil {
		t.Fatal(err)
	}
	publishRequest := httptest.NewRequest(http.MethodPut, "https://manna.example/admin/api/events/alpha", bytes.NewReader(body))
	publishRequest.SetBasicAuth("admin", "secret")
	publishRequest.Header.Set("Content-Type", "application/json")
	publishRequest.Header.Set("X-Manna-Admin", "1")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publishRequest)
	if publishResponse.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", publishResponse.Code, publishResponse.Body.String())
	}

	written, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != updated {
		t.Fatal("HTTP publish did not atomically store the exact YAML")
	}
}
