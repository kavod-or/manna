package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"strings"

	admincontent "manna/internal/admin"
	"manna/internal/menu"

	"go.yaml.in/yaml/v3"
)

const maxAdminRequestSize = 3 << 20

const (
	maxAdminLogoSize        = 2 << 20
	maxAdminLogoRequestSize = maxAdminLogoSize + (1 << 20)
	maxAdminLogoDimension   = 8192
	maxAdminLogoPixels      = 16 << 20
)

type adminYAMLRequest struct {
	YAML       string       `json:"yaml"`
	SourceYAML string       `json:"source_yaml,omitempty"`
	Config     *menu.Config `json:"config,omitempty"`
	Revision   string       `json:"revision,omitempty"`
}

type adminEventResponse struct {
	Path             string       `json:"path"`
	Archived         bool         `json:"archived"`
	PublicURL        string       `json:"public_url,omitempty"`
	CanIncludeLogo   bool         `json:"can_include_logo"`
	YAML             string       `json:"yaml"`
	Config           *menu.Config `json:"config,omitempty"`
	Revision         string       `json:"revision"`
	ManifestRevision string       `json:"manifest_revision,omitempty"`
}

type adminCreateEventRequest struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type adminDuplicateEventRequest struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type adminDeleteEventRequest struct {
	Revision     string `json:"revision"`
	Confirmation string `json:"confirmation"`
}

type adminArchiveEventRequest struct {
	Archived         *bool  `json:"archived"`
	ManifestRevision string `json:"manifest_revision"`
	EventRevision    string `json:"event_revision"`
}

type adminRestoreEventRequest struct {
	Revision string `json:"revision"`
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
	mux.HandleFunc("POST /admin/api/events", s.adminCreateEvent)
	mux.HandleFunc("GET /admin/api/events/{slug}", s.adminReadEvent)
	mux.HandleFunc("DELETE /admin/api/events/{slug}", s.adminDeleteEvent)
	mux.HandleFunc("POST /admin/api/events/{slug}/duplicate", s.adminDuplicateEvent)
	mux.HandleFunc("PUT /admin/api/events/{slug}/archive", s.adminSetEventArchived)
	mux.HandleFunc("GET /admin/api/events/{slug}/history", s.adminEventHistory)
	mux.HandleFunc("POST /admin/api/events/{slug}/history/{historyID}/restore", s.adminRestoreEvent)
	mux.HandleFunc("POST /admin/api/events/{slug}/validate", s.adminValidateEvent)
	mux.HandleFunc("POST /admin/api/events/{slug}/logo", s.adminUploadLogo)
	mux.HandleFunc("GET /admin/api/events/{slug}/qr.svg", s.adminEventQR)
	mux.HandleFunc("PUT /admin/api/events/{slug}", s.adminPublishEvent)
	return mux
}

func (s *server) adminEventHistory(writer http.ResponseWriter, request *http.Request) {
	history, ok := s.admin.(admincontent.HistoryContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "revision history is not available for this content store")
		return
	}
	revisions, err := history.EventHistory("/" + request.PathValue("slug"))
	if err != nil {
		s.adminContentError(writer, "list event history", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Revisions []admincontent.Revision `json:"revisions"`
	}{Revisions: revisions})
}

func (s *server) adminRestoreEvent(writer http.ResponseWriter, request *http.Request) {
	history, ok := s.admin.(admincontent.HistoryContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "revision history is not available for this content store")
		return
	}
	var payload adminRestoreEventRequest
	if !decodeAdminJSON(writer, request, &payload) {
		return
	}
	if payload.Revision == "" {
		writeAdminError(writer, http.StatusBadRequest, "revision is required")
		return
	}
	eventPath := "/" + request.PathValue("slug")
	content, revision, err := history.RestoreEvent(eventPath, request.PathValue("historyID"), payload.Revision)
	if err != nil {
		s.adminContentError(writer, "restore event history", err)
		return
	}
	config, err := decodeAdminConfig(content)
	if err != nil {
		s.adminContentError(writer, "decode restored event", err)
		return
	}
	event, err := s.adminEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "read restored event state", err)
		return
	}
	publicURL, err := s.adminPublicEventURL(request, event)
	if err != nil {
		s.adminInternalError(writer, "build restored event URL", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, adminEventResponse{
		Path: eventPath, Archived: event.Archived, PublicURL: publicURL, CanIncludeLogo: config.Conference.Logo != "", YAML: string(content), Config: &config, Revision: revision,
	})
}

func (s *server) adminDuplicateEvent(writer http.ResponseWriter, request *http.Request) {
	manager, ok := s.admin.(admincontent.EventContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "event management is not available for this content store")
		return
	}
	var payload adminDuplicateEventRequest
	if !decodeAdminJSON(writer, request, &payload) {
		return
	}
	if payload.Path == "" || payload.Revision == "" {
		writeAdminError(writer, http.StatusBadRequest, "path and revision are required")
		return
	}
	sourcePath := "/" + request.PathValue("slug")
	manifestRevision, err := manager.DuplicateEvent(sourcePath, payload.Path, payload.Revision)
	if err != nil {
		s.adminContentError(writer, "duplicate event", err)
		return
	}
	content, revision, err := s.admin.ReadEvent(payload.Path)
	if err != nil {
		s.adminContentError(writer, "read duplicated event", err)
		return
	}
	config, err := decodeAdminConfig(content)
	if err != nil {
		s.adminContentError(writer, "decode duplicated event", err)
		return
	}
	event, err := s.adminEvent(payload.Path)
	if err != nil {
		s.adminContentError(writer, "read duplicated event state", err)
		return
	}
	publicURL, err := s.adminPublicEventURL(request, event)
	if err != nil {
		s.adminInternalError(writer, "build duplicated event URL", err)
		return
	}
	writeAdminJSON(writer, http.StatusCreated, adminEventResponse{
		Path: payload.Path, Archived: event.Archived, PublicURL: publicURL, CanIncludeLogo: config.Conference.Logo != "", YAML: string(content), Config: &config, Revision: revision, ManifestRevision: manifestRevision,
	})
}

func (s *server) adminUploadLogo(writer http.ResponseWriter, request *http.Request) {
	uploader, ok := s.admin.(admincontent.LogoContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "logo uploads are not available for this content store")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxAdminLogoRequestSize)
	file, _, err := request.FormFile("logo")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAdminError(writer, http.StatusRequestEntityTooLarge, "logo must not exceed 2 MB")
			return
		}
		writeAdminError(writer, http.StatusBadRequest, "upload one PNG or JPEG file in the logo field")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxAdminLogoSize+1))
	if err != nil {
		writeAdminError(writer, http.StatusBadRequest, "could not read the uploaded logo")
		return
	}
	if len(content) > maxAdminLogoSize {
		writeAdminError(writer, http.StatusRequestEntityTooLarge, "logo must not exceed 2 MB")
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || (format != "png" && format != "jpeg") {
		writeAdminError(writer, http.StatusUnprocessableEntity, "logo must be a valid PNG or JPEG image")
		return
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxAdminLogoDimension || config.Height > maxAdminLogoDimension || int64(config.Width)*int64(config.Height) > maxAdminLogoPixels {
		writeAdminError(writer, http.StatusUnprocessableEntity, "logo dimensions are too large")
		return
	}
	if _, decodedFormat, err := image.Decode(bytes.NewReader(content)); err != nil || decodedFormat != format {
		writeAdminError(writer, http.StatusUnprocessableEntity, "logo must be a valid PNG or JPEG image")
		return
	}
	extension := ".png"
	if format == "jpeg" {
		extension = ".jpg"
	}
	eventPath := "/" + request.PathValue("slug")
	logoPath, err := uploader.UploadLogo(eventPath, extension, content)
	if err != nil {
		s.adminContentError(writer, "upload event logo", err)
		return
	}
	writeAdminJSON(writer, http.StatusCreated, struct {
		Logo string `json:"logo"`
	}{Logo: logoPath})
}

func (s *server) adminEvents(writer http.ResponseWriter, _ *http.Request) {
	events, err := s.admin.Events()
	if err != nil {
		s.adminInternalError(writer, "list events", err)
		return
	}
	manager, manageable := s.admin.(admincontent.EventContent)
	_, canUseHistory := s.admin.(admincontent.HistoryContent)
	var revision string
	if manageable {
		revision, err = manager.ManifestRevision()
		if err != nil {
			s.adminInternalError(writer, "read event manifest revision", err)
			return
		}
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Events     []admincontent.Event `json:"events"`
		Revision   string               `json:"revision,omitempty"`
		CanManage  bool                 `json:"can_manage"`
		CanHistory bool                 `json:"can_history"`
	}{Events: events, Revision: revision, CanManage: manageable, CanHistory: canUseHistory})
}

func (s *server) adminSetEventArchived(writer http.ResponseWriter, request *http.Request) {
	manager, ok := s.admin.(admincontent.EventContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "event management is not available for this content store")
		return
	}
	var payload adminArchiveEventRequest
	if !decodeAdminJSON(writer, request, &payload) {
		return
	}
	if payload.Archived == nil || payload.ManifestRevision == "" || payload.EventRevision == "" {
		writeAdminError(writer, http.StatusBadRequest, "archived, manifest_revision, and event_revision are required")
		return
	}
	eventPath := "/" + request.PathValue("slug")
	revision, err := manager.SetEventArchived(eventPath, *payload.Archived, payload.ManifestRevision, payload.EventRevision)
	if err != nil {
		s.adminContentError(writer, "change event archive state", err)
		return
	}
	publicURL := ""
	if !*payload.Archived {
		publicURL, err = s.publicEventURL(request, eventPath)
		if err != nil {
			s.adminInternalError(writer, "build unarchived event URL", err)
			return
		}
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Path             string `json:"path"`
		Archived         bool   `json:"archived"`
		ManifestRevision string `json:"manifest_revision"`
		PublicURL        string `json:"public_url,omitempty"`
	}{Path: eventPath, Archived: *payload.Archived, ManifestRevision: revision, PublicURL: publicURL})
}

func (s *server) adminCreateEvent(writer http.ResponseWriter, request *http.Request) {
	manager, ok := s.admin.(admincontent.EventContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "event management is not available for this content store")
		return
	}
	var payload adminCreateEventRequest
	if !decodeAdminJSON(writer, request, &payload) {
		return
	}
	if payload.Path == "" || payload.Revision == "" {
		writeAdminError(writer, http.StatusBadRequest, "path and revision are required")
		return
	}
	manifestRevision, err := manager.CreateEvent(payload.Path, payload.Revision)
	if err != nil {
		s.adminContentError(writer, "create event", err)
		return
	}
	content, revision, err := s.admin.ReadEvent(payload.Path)
	if err != nil {
		s.adminContentError(writer, "read created event", err)
		return
	}
	config, err := decodeAdminConfig(content)
	if err != nil {
		s.adminContentError(writer, "decode created event", err)
		return
	}
	writeAdminJSON(writer, http.StatusCreated, adminEventResponse{
		Path: payload.Path, YAML: string(content), Config: &config, Revision: revision, ManifestRevision: manifestRevision,
	})
}

func (s *server) adminDeleteEvent(writer http.ResponseWriter, request *http.Request) {
	manager, ok := s.admin.(admincontent.EventContent)
	if !ok {
		writeAdminError(writer, http.StatusNotImplemented, "event management is not available for this content store")
		return
	}
	var payload adminDeleteEventRequest
	if !decodeAdminJSON(writer, request, &payload) {
		return
	}
	if payload.Revision == "" || payload.Confirmation == "" {
		writeAdminError(writer, http.StatusBadRequest, "revision and confirmation are required")
		return
	}
	eventPath := "/" + request.PathValue("slug")
	revision, err := manager.DeleteEvent(eventPath, payload.Confirmation, payload.Revision)
	if err != nil {
		s.adminContentError(writer, "delete event", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Deleted  string `json:"deleted"`
		Revision string `json:"revision"`
	}{Deleted: eventPath, Revision: revision})
}

func (s *server) adminReadEvent(writer http.ResponseWriter, request *http.Request) {
	eventPath := "/" + request.PathValue("slug")
	event, err := s.adminEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "read event state", err)
		return
	}
	content, revision, err := s.admin.ReadEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "read event", err)
		return
	}
	config, decodeErr := decodeAdminConfig(content)
	var editableConfig *menu.Config
	if decodeErr == nil {
		editableConfig = &config
	}
	publicURL, err := s.adminPublicEventURL(request, event)
	if err != nil {
		s.adminInternalError(writer, "build public event URL", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, adminEventResponse{
		Path:           eventPath,
		Archived:       event.Archived,
		PublicURL:      publicURL,
		CanIncludeLogo: editableConfig != nil && config.Conference.Logo != "",
		YAML:           string(content),
		Config:         editableConfig,
		Revision:       revision,
	})
}

func (s *server) adminEvent(eventPath string) (admincontent.Event, error) {
	events, err := s.admin.Events()
	if err != nil {
		return admincontent.Event{}, err
	}
	for _, event := range events {
		if event.Path == eventPath {
			return event, nil
		}
	}
	return admincontent.Event{}, admincontent.ErrEventNotFound
}

func (s *server) adminPublicEventURL(request *http.Request, event admincontent.Event) (string, error) {
	if event.Archived {
		return "", nil
	}
	return s.publicEventURL(request, event.Path)
}

func (s *server) adminValidateEvent(writer http.ResponseWriter, request *http.Request) {
	eventPath := "/" + request.PathValue("slug")
	current, _, err := s.admin.ReadEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "resolve event for validation", err)
		return
	}
	payload, ok := decodeAdminYAMLRequest(writer, request)
	if !ok {
		return
	}
	content, err := payload.menuYAML(current)
	if err != nil {
		writeAdminError(writer, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	if err := s.admin.ValidateMenu(content); err != nil {
		writeAdminError(writer, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	config, decodeErr := decodeAdminConfig(content)
	var editableConfig *menu.Config
	if decodeErr == nil {
		editableConfig = &config
	}
	writeAdminJSON(writer, http.StatusOK, struct {
		Valid  bool         `json:"valid"`
		YAML   string       `json:"yaml"`
		Config *menu.Config `json:"config,omitempty"`
	}{Valid: true, YAML: string(content), Config: editableConfig})
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
	current, _, err := s.admin.ReadEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "resolve event for publishing", err)
		return
	}
	content, err := payload.menuYAML(current)
	if err != nil {
		writeAdminError(writer, http.StatusUnprocessableEntity, validationMessage(err))
		return
	}
	revision, err := s.admin.PublishEvent(eventPath, payload.Revision, content)
	if err != nil {
		s.adminContentError(writer, "publish event", err)
		return
	}
	config, decodeErr := decodeAdminConfig(content)
	var editableConfig *menu.Config
	if decodeErr == nil {
		editableConfig = &config
	}
	event, err := s.adminEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "read published event state", err)
		return
	}
	publicURL, err := s.adminPublicEventURL(request, event)
	if err != nil {
		s.adminInternalError(writer, "build published event URL", err)
		return
	}
	writeAdminJSON(writer, http.StatusOK, adminEventResponse{
		Path:           eventPath,
		Archived:       event.Archived,
		PublicURL:      publicURL,
		CanIncludeLogo: editableConfig != nil && config.Conference.Logo != "",
		YAML:           string(content),
		Config:         editableConfig,
		Revision:       revision,
	})
}

func decodeAdminConfig(content []byte) (menu.Config, error) {
	config, err := menu.Decode(strings.NewReader(string(content)))
	if err != nil {
		return menu.Config{}, fmt.Errorf("%w: %v", admincontent.ErrInvalidMenu, err)
	}
	return config, nil
}

func (payload adminYAMLRequest) menuYAML(current []byte) ([]byte, error) {
	if payload.Config == nil {
		return []byte(payload.YAML), nil
	}
	if payload.YAML != "" {
		return nil, fmt.Errorf("%w: provide config or yaml, not both", admincontent.ErrInvalidMenu)
	}
	if err := payload.Config.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", admincontent.ErrInvalidMenu, err)
	}
	content, err := yaml.Marshal(payload.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: encode menu YAML: %v", admincontent.ErrInvalidMenu, err)
	}
	commentSource := current
	if payload.SourceYAML != "" {
		commentSource = []byte(payload.SourceYAML)
	}
	return preserveYAMLComments(commentSource, content), nil
}

func preserveYAMLComments(current, replacement []byte) []byte {
	var oldDocument, newDocument yaml.Node
	if yaml.Unmarshal(current, &oldDocument) != nil || yaml.Unmarshal(replacement, &newDocument) != nil {
		return replacement
	}
	copyYAMLComments(&newDocument, &oldDocument)
	var output strings.Builder
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&newDocument); err != nil {
		return replacement
	}
	_ = encoder.Close()
	return []byte(output.String())
}

func copyYAMLComments(destination, source *yaml.Node) {
	if destination == nil || source == nil {
		return
	}
	destination.HeadComment = source.HeadComment
	destination.LineComment = source.LineComment
	destination.FootComment = source.FootComment
	if destination.Kind != source.Kind {
		return
	}
	switch destination.Kind {
	case yaml.DocumentNode:
		if len(destination.Content) > 0 && len(source.Content) > 0 {
			copyYAMLComments(destination.Content[0], source.Content[0])
		}
	case yaml.MappingNode:
		oldValues := make(map[string][2]*yaml.Node, len(source.Content)/2)
		for index := 0; index+1 < len(source.Content); index += 2 {
			oldValues[source.Content[index].Value] = [2]*yaml.Node{source.Content[index], source.Content[index+1]}
		}
		for index := 0; index+1 < len(destination.Content); index += 2 {
			if old, ok := oldValues[destination.Content[index].Value]; ok {
				copyYAMLComments(destination.Content[index], old[0])
				copyYAMLComments(destination.Content[index+1], old[1])
			}
		}
	case yaml.SequenceNode:
		used := make(map[int]bool, len(source.Content))
		for index, child := range destination.Content {
			match := matchingYAMLSequenceNode(child, source.Content, used, index)
			if match >= 0 {
				used[match] = true
				copyYAMLComments(child, source.Content[match])
			}
		}
	}
}

func matchingYAMLSequenceNode(wanted *yaml.Node, candidates []*yaml.Node, used map[int]bool, fallback int) int {
	identity := yamlSequenceIdentity(wanted)
	if identity != "" {
		for index, candidate := range candidates {
			if !used[index] && yamlSequenceIdentity(candidate) == identity {
				return index
			}
		}
	}
	if fallback < len(candidates) && !used[fallback] {
		return fallback
	}
	return -1
}

func yamlSequenceIdentity(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for _, name := range []string{"id", "date"} {
		for index := 0; index+1 < len(node.Content); index += 2 {
			if node.Content[index].Value == name {
				return name + ":" + node.Content[index+1].Value
			}
		}
	}
	return ""
}

func decodeAdminYAMLRequest(writer http.ResponseWriter, request *http.Request) (adminYAMLRequest, bool) {
	var payload adminYAMLRequest
	ok := decodeAdminJSON(writer, request, &payload)
	return payload, ok
}

func decodeAdminJSON(writer http.ResponseWriter, request *http.Request, payload any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAdminError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}

	request.Body = http.MaxBytesReader(writer, request.Body, maxAdminRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(payload); err != nil {
		writeAdminDecodeError(writer, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			writeAdminError(writer, http.StatusBadRequest, "request body must contain one JSON object")
		} else {
			writeAdminDecodeError(writer, err)
		}
		return false
	}
	return true
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
	case errors.Is(err, admincontent.ErrManifestStale):
		writeAdminError(writer, http.StatusConflict, "the event list changed after it was loaded")
	case errors.Is(err, admincontent.ErrEventExists):
		writeAdminError(writer, http.StatusConflict, "the event path or generated menu file already exists")
	case errors.Is(err, admincontent.ErrInvalidEvent):
		writeAdminError(writer, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), admincontent.ErrInvalidEvent.Error()+": "))
	case errors.Is(err, admincontent.ErrHistoryNotFound):
		writeAdminError(writer, http.StatusNotFound, "revision history entry not found")
	case errors.Is(err, admincontent.ErrInvalidHistory):
		writeAdminError(writer, http.StatusBadRequest, "invalid revision history entry")
	case errors.Is(err, admincontent.ErrInvalidMenu):
		writeAdminError(writer, http.StatusUnprocessableEntity, validationMessage(err))
	case errors.Is(err, admincontent.ErrInvalidLogo):
		writeAdminError(writer, http.StatusUnprocessableEntity, "logo must be a valid PNG or JPEG image")
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
