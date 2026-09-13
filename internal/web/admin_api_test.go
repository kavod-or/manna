package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	admincontent "manna/internal/admin"
	"manna/internal/menu"
)

func TestAdminAPIListsAndReadsEvents(t *testing.T) {
	content := newStubAdminContent()
	handler := newAdminAPITestServer(t, content)

	response := serveAdminAPI(handler, http.MethodGet, "/admin/api/events", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
	}
	var list struct {
		Events []adminEventSummary `json:"events"`
	}
	decodeTestJSON(t, response, &list)
	if len(list.Events) != 2 || list.Events[0].Path != "/alpha" || list.Events[1].Path != "/beta" {
		t.Fatalf("events = %#v", list.Events)
	}

	response = serveAdminAPI(handler, http.MethodGet, "/admin/api/events/alpha", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read status = %d: %s", response.Code, response.Body.String())
	}
	var event adminEventResponse
	decodeTestJSON(t, response, &event)
	if event.Path != "/alpha" || event.YAML != content.files["/alpha"] || event.Revision != content.revisions["/alpha"] {
		t.Fatalf("event = %#v", event)
	}

	response = serveAdminAPI(handler, http.MethodGet, "/admin/api/events/missing", "")
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "event not found") {
		t.Fatalf("missing event response = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminAPIValidatesAndPublishes(t *testing.T) {
	content := newStubAdminContent()
	handler := newAdminAPITestServer(t, content)

	validBody := `{"yaml":"conference: valid"}`
	response := serveAdminAPI(handler, http.MethodPost, "/admin/api/events/alpha/validate", validBody)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"valid":true`) {
		t.Fatalf("valid response = %d %s", response.Code, response.Body.String())
	}

	before := content.files["/alpha"]
	response = serveAdminAPI(handler, http.MethodPost, "/admin/api/events/alpha/validate", `{"yaml":"invalid menu"}`)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "menu validation failed") {
		t.Fatalf("invalid response = %d %s", response.Code, response.Body.String())
	}
	if content.files["/alpha"] != before {
		t.Fatal("validation changed the stored menu")
	}

	publishBody := `{"yaml":"conference: updated","revision":"rev-alpha"}`
	response = serveAdminAPI(handler, http.MethodPut, "/admin/api/events/alpha", publishBody)
	if response.Code != http.StatusOK {
		t.Fatalf("publish response = %d %s", response.Code, response.Body.String())
	}
	var published struct {
		Path     string `json:"path"`
		Revision string `json:"revision"`
	}
	decodeTestJSON(t, response, &published)
	if published.Path != "/alpha" || published.Revision != "rev-published" || content.files["/alpha"] != "conference: updated" {
		t.Fatalf("published = %#v, content = %q", published, content.files["/alpha"])
	}

	response = serveAdminAPI(handler, http.MethodPut, "/admin/api/events/alpha", publishBody)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "changed after it was loaded") {
		t.Fatalf("conflict response = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminAPIRejectsInvalidRequests(t *testing.T) {
	content := newStubAdminContent()
	handler := newAdminAPITestServer(t, content)

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		contentType string
		want        int
	}{
		{name: "malformed JSON", method: http.MethodPost, path: "/admin/api/events/alpha/validate", body: `{`, want: http.StatusBadRequest},
		{name: "unknown JSON field", method: http.MethodPost, path: "/admin/api/events/alpha/validate", body: `{"yaml":"ok","extra":true}`, want: http.StatusBadRequest},
		{name: "multiple JSON values", method: http.MethodPost, path: "/admin/api/events/alpha/validate", body: `{"yaml":"ok"} {}`, want: http.StatusBadRequest},
		{name: "missing revision", method: http.MethodPut, path: "/admin/api/events/alpha", body: `{"yaml":"ok"}`, want: http.StatusBadRequest},
		{name: "oversized body", method: http.MethodPost, path: "/admin/api/events/alpha/validate", body: `{"yaml":"` + strings.Repeat("x", maxAdminRequestSize) + `"}`, want: http.StatusBadRequest},
		{name: "missing content type", method: http.MethodPost, path: "/admin/api/events/alpha/validate", body: `{"yaml":"ok"}`, contentType: "none", want: http.StatusUnsupportedMediaType},
		{name: "wrong content type", method: http.MethodPut, path: "/admin/api/events/alpha", body: `{"yaml":"ok","revision":"rev-alpha"}`, contentType: "text/plain", want: http.StatusUnsupportedMediaType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := newAdminAPIRequest(test.method, test.path, test.body)
			switch test.contentType {
			case "none":
				request.Header.Del("Content-Type")
			case "":
			default:
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
		})
	}
}

func TestAdminAPICannotAddressFilesOrEscapeEventRoutes(t *testing.T) {
	handler := newAdminAPITestServer(t, newStubAdminContent())
	for _, requestPath := range []string{
		"/admin/api/events/events.yaml",
		"/admin/api/events/..",
		"/admin/api/events/%2e%2e",
		"/admin/api/events/%2falpha",
		"/admin/api/events/alpha%2f..%2fbeta",
		"/admin/api/events/alpha/../../events.yaml",
	} {
		request := newAdminAPIRequest(http.MethodGet, requestPath, "")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "conference: alpha") || strings.Contains(response.Body.String(), "conference: beta") {
			t.Errorf("unsafe path %q returned %d: %s", requestPath, response.Code, response.Body.String())
		}
	}
}

func TestAdminAPIDoesNotExposeInternalErrors(t *testing.T) {
	content := newStubAdminContent()
	content.listErr = errors.New("open /private/secret/events.yaml: permission denied")
	var logs bytes.Buffer
	handler := newAdminAPITestServerWithLogger(t, content, slog.New(slog.NewTextHandler(&logs, nil)))

	response := serveAdminAPI(handler, http.MethodGet, "/admin/api/events", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "/private/secret") {
		t.Fatalf("response exposed an internal path: %s", response.Body.String())
	}
	if strings.Contains(logs.String(), "/private/secret") {
		t.Fatalf("admin logs exposed an internal path: %s", logs.String())
	}
}

type stubAdminContent struct {
	files     map[string]string
	revisions map[string]string
	listErr   error
}

func newStubAdminContent() *stubAdminContent {
	return &stubAdminContent{
		files:     map[string]string{"/alpha": "conference: alpha", "/beta": "conference: beta"},
		revisions: map[string]string{"/alpha": "rev-alpha", "/beta": "rev-beta"},
	}
}

func (content *stubAdminContent) Events() ([]string, error) {
	if content.listErr != nil {
		return nil, content.listErr
	}
	return []string{"/alpha", "/beta"}, nil
}

func (content *stubAdminContent) ReadEvent(eventPath string) ([]byte, string, error) {
	yaml, ok := content.files[eventPath]
	if !ok {
		return nil, "", admincontent.ErrEventNotFound
	}
	return []byte(yaml), content.revisions[eventPath], nil
}

func (content *stubAdminContent) ValidateMenu(yaml []byte) error {
	if strings.Contains(string(yaml), "invalid") {
		return fmt.Errorf("%w: menu validation failed at line 1", admincontent.ErrInvalidMenu)
	}
	return nil
}

func (content *stubAdminContent) PublishEvent(eventPath, expectedRevision string, yaml []byte) (string, error) {
	if _, ok := content.files[eventPath]; !ok {
		return "", admincontent.ErrEventNotFound
	}
	if err := content.ValidateMenu(yaml); err != nil {
		return "", err
	}
	if content.revisions[eventPath] != expectedRevision {
		return "", admincontent.ErrRevisionStale
	}
	content.files[eventPath] = string(yaml)
	content.revisions[eventPath] = "rev-published"
	return "rev-published", nil
}

func newAdminAPITestServer(t *testing.T, content admincontent.Content) http.Handler {
	return newAdminAPITestServerWithLogger(t, content, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newAdminAPITestServerWithLogger(t *testing.T, content admincontent.Content, logger *slog.Logger) http.Handler {
	t.Helper()
	templates := fstest.MapFS{
		"web/templates/index.html": {Data: []byte(`{{define "index.html"}}menu{{end}}`)},
		"web/templates/admin.html": {Data: []byte(`{{define "admin.html"}}admin editor{{end}}`)},
	}
	events := func() (map[string]menu.Loader, error) { return map[string]menu.Loader{}, nil }
	handler, err := NewDynamicWithAdmin(events, templates, fstest.MapFS{}, nil, logger, AdminOptions{Password: "secret", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func serveAdminAPI(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := newAdminAPIRequest(method, path, body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newAdminAPIRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, "https://manna.example"+path, bytes.NewBufferString(body))
	request.SetBasicAuth("admin", "secret")
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set(adminRequestHeader, "1")
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func decodeTestJSON(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
