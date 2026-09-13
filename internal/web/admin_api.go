package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	admincontent "mana/internal/admin"
)

const maxAdminRequestSize = 1 << 20

type adminYAMLRequest struct {
	YAML     string `json:"yaml"`
	Revision string `json:"revision,omitempty"`
}

type adminEventResponse struct {
	Path     string `json:"path"`
	YAML     string `json:"yaml"`
	Revision string `json:"revision"`
}

type adminEventSummary struct {
	Path string `json:"path"`
}

func (s *server) adminHandler() http.Handler {
	if s.admin == nil {
		return http.HandlerFunc(http.NotFound)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin", s.adminRedirect)
	mux.HandleFunc("GET /admin/", s.adminPage)
	mux.HandleFunc("GET /admin/static/{asset}", s.adminAsset)
	mux.HandleFunc("GET /admin/api/events", s.adminEvents)
	mux.HandleFunc("GET /admin/api/events/{slug}", s.adminReadEvent)
	mux.HandleFunc("POST /admin/api/events/{slug}/validate", s.adminValidateEvent)
	mux.HandleFunc("PUT /admin/api/events/{slug}", s.adminPublishEvent)
	return mux
}

func (s *server) adminEvents(writer http.ResponseWriter, _ *http.Request) {
	paths, err := s.admin.Events()
	if err != nil {
		s.adminInternalError(writer, "list events", err)
		return
	}
	events := make([]adminEventSummary, 0, len(paths))
	for _, eventPath := range paths {
		events = append(events, adminEventSummary{Path: eventPath})
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Events []adminEventSummary `json:"events"`
	}{Events: events})
}

func (s *server) adminReadEvent(writer http.ResponseWriter, request *http.Request) {
	eventPath := "/" + request.PathValue("slug")
	content, revision, err := s.admin.ReadEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "read event", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, adminEventResponse{
		Path:     eventPath,
		YAML:     string(content),
		Revision: revision,
	})
}

func (s *server) adminValidateEvent(writer http.ResponseWriter, request *http.Request) {
	eventPath := "/" + request.PathValue("slug")
	if _, _, err := s.admin.ReadEvent(eventPath); err != nil {
		s.adminContentError(writer, "resolve event for validation", err)
		return
	}
	payload, ok := decodeAdminYAMLRequest(writer, request)
	if !ok {
		return
	}
	if err := s.admin.ValidateMenu([]byte(payload.YAML)); err != nil {
		writeAdminError(writer, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Valid bool `json:"valid"`
	}{Valid: true})
}

func (s *server) adminPublishEvent(writer http.ResponseWriter, request *http.Request) {
	payload, ok := decodeAdminYAMLRequest(writer, request)
	if !ok {
		return
	}
	if payload.Revision == "" {
		writeAdminError(writer, http.StatusBadRequest, "revision is required")
		return
	}
	eventPath := "/" + request.PathValue("slug")
	revision, err := s.admin.PublishEvent(eventPath, payload.Revision, []byte(payload.YAML))
	if err != nil {
		s.adminContentError(writer, "publish event", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Path     string `json:"path"`
		Revision string `json:"revision"`
	}{Path: eventPath, Revision: revision})
}

func decodeAdminYAMLRequest(writer http.ResponseWriter, request *http.Request) (adminYAMLRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAdminError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return adminYAMLRequest{}, false
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxAdminRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload adminYAMLRequest
	if err := decoder.Decode(&payload); err != nil {
		writeAdminDecodeError(writer, err)
		return adminYAMLRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			writeAdminError(writer, http.StatusBadRequest, "request body must contain one JSON object")
		} else {
			writeAdminDecodeError(writer, err)
		}
		return adminYAMLRequest{}, false
	}
	return payload, true
}

func writeAdminDecodeError(writer http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeAdminError(writer, http.StatusBadRequest, fmt.Sprintf("request body must not exceed %d bytes", maxAdminRequestSize))
		return
	}
	writeAdminError(writer, http.StatusBadRequest, "request body must be one valid JSON object")
}

func (s *server) adminContentError(writer http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, admincontent.ErrEventNotFound):
		writeAdminError(writer, http.StatusNotFound, "event not found")
	case errors.Is(err, admincontent.ErrRevisionStale):
		writeAdminError(writer, http.StatusConflict, "the event changed after it was loaded")
	case errors.Is(err, admincontent.ErrInvalidMenu):
		writeAdminError(writer, http.StatusUnprocessableEntity, validationMessage(err))
	default:
		s.adminInternalError(writer, operation, err)
	}
}

func (s *server) adminInternalError(writer http.ResponseWriter, operation string, err error) {
	// Detailed storage errors can contain host filesystem paths. Keep them out of
	// logs that may be shipped to third-party observability systems.
	s.logger.Error("admin content operation failed", "operation", operation)
	writeAdminError(writer, http.StatusInternalServerError, "admin content operation failed")
}

func validationMessage(err error) string {
	message := err.Error()
	prefix := admincontent.ErrInvalidMenu.Error() + ": "
	return strings.TrimPrefix(message, prefix)
}

func writeAdminError(writer http.ResponseWriter, status int, message string) {
	writeAdminJSON(writer, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func writeAdminJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
