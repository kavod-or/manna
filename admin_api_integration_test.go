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
	original, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
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

	historyRequest := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/api/events/alpha/history", nil)
	historyRequest.SetBasicAuth("admin", "secret")
	historyResponse := httptest.NewRecorder()
	handler.ServeHTTP(historyResponse, historyRequest)
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history status = %d: %s", historyResponse.Code, historyResponse.Body.String())
	}
	var history struct {
		Revisions []struct {
			ID string `json:"id"`
		} `json:"revisions"`
	}
	if err := json.NewDecoder(historyResponse.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Revisions) != 1 {
		t.Fatalf("history after HTTP publish = %#v", history.Revisions)
	}
	restoreBody, err := json.Marshal(map[string]string{"revision": contentRevision([]byte(updated))})
	if err != nil {
		t.Fatal(err)
	}
	restoreRequest := httptest.NewRequest(http.MethodPost, "https://manna.example/admin/api/events/alpha/history/"+history.Revisions[0].ID+"/restore", bytes.NewReader(restoreBody))
	restoreRequest.SetBasicAuth("admin", "secret")
	restoreRequest.Header.Set("Content-Type", "application/json")
	restoreRequest.Header.Set("X-Manna-Admin", "1")
	restoreResponse := httptest.NewRecorder()
	handler.ServeHTTP(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore status = %d: %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	restored, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatal("HTTP restore did not recover the exact previous YAML")
	}
}
