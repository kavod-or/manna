package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	admincontent "manna/internal/admin"
	"manna/internal/menu"

	"go.yaml.in/yaml/v3"
)

type eventAdmin struct {
	registry  *eventRegistry
	publishMu sync.Mutex
}

var _ admincontent.Content = (*eventAdmin)(nil)
var _ admincontent.LogoContent = (*eventAdmin)(nil)
var _ admincontent.EventContent = (*eventAdmin)(nil)
var _ admincontent.HistoryContent = (*eventAdmin)(nil)

const (
	eventHistoryRoot  = ".manna-history"
	eventHistoryLimit = 10
	historyTimeLayout = "20060102T150405.000000000Z"
)

const blankEventMenu = `conference:
  languages: [de, en]
  timezone: Europe/Berlin
  name:
    de: Neue Veranstaltung
    en: New event
  location:
    de: Wird noch bekannt gegeben
    en: To be announced
permanent: {}
days:
  - date: 2030-01-01
    services:
      - id: first_service
        title:
          de: Erstes Angebot
          en: First service
        subtitle:
          de: Bitte bearbeiten
          en: Please edit
        from: "12:00"
        until: "13:00"
`

func newEventAdmin(registry *eventRegistry) *eventAdmin {
	return &eventAdmin{registry: registry}
}

func (admin *eventAdmin) Events() ([]string, error) {
	routes, err := admin.routes()
	if err != nil {
		return nil, err
	}
	events := make([]string, 0, len(routes))
	for eventPath := range routes {
		events = append(events, eventPath)
	}
	sort.Strings(events)
	return events, nil
}

func (admin *eventAdmin) ManifestRevision() (string, error) {
	_, content, err := admin.manifest()
	if err != nil {
		return "", err
	}
	return contentRevision(content), nil
}

func (admin *eventAdmin) CreateEvent(eventPath, expectedManifestRevision string) (string, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	if err := validateEventPath(eventPath); err != nil {
		return "", fmt.Errorf("%w: %v", admincontent.ErrInvalidEvent, err)
	}
	entries, manifest, err := admin.manifest()
	if err != nil {
		return "", err
	}
	if contentRevision(manifest) != expectedManifestRevision {
		return "", admincontent.ErrManifestStale
	}
	menuPath := strings.TrimPrefix(eventPath, "/") + ".yaml"
	for _, entry := range entries {
		if entry.Path == eventPath || entry.Menu == menuPath {
			return "", admincontent.ErrEventExists
		}
	}
	if err := admin.ValidateMenu([]byte(blankEventMenu)); err != nil {
		return "", err
	}
	writer, ok := admin.registry.content.(interface {
		writeNewFile(string, []byte) error
		writeFileAtomic(string, string, []byte) error
		removeFileRevision(string, string) error
	})
	if !ok {
		return "", admincontent.ErrContentReadOnly
	}
	if err := writer.writeNewFile(menuPath, []byte(blankEventMenu)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", admincontent.ErrEventExists
		}
		return "", fmt.Errorf("create admin event menu: %w", err)
	}
	entries = append(entries, eventEntry{Path: eventPath, Menu: menuPath})
	updated, err := encodeEventManifest(entries)
	if err != nil {
		_ = writer.removeFileRevision(menuPath, contentRevision([]byte(blankEventMenu)))
		return "", err
	}
	if err := writer.writeFileAtomic(eventManifestFile, expectedManifestRevision, updated); err != nil {
		// Only remove the file if it is still the exact template created above.
		// A concurrent host edit is never destroyed during rollback.
		_ = writer.removeFileRevision(menuPath, contentRevision([]byte(blankEventMenu)))
		return "", fmt.Errorf("update event manifest: %w", err)
	}
	admin.invalidateRegistry()
	return contentRevision(updated), nil
}

func (admin *eventAdmin) DuplicateEvent(sourcePath, targetPath, expectedManifestRevision string) (string, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	if err := validateEventPath(sourcePath); err != nil {
		return "", fmt.Errorf("%w: invalid source path: %v", admincontent.ErrInvalidEvent, err)
	}
	if err := validateEventPath(targetPath); err != nil {
		return "", fmt.Errorf("%w: invalid target path: %v", admincontent.ErrInvalidEvent, err)
	}
	if sourcePath == targetPath {
		return "", fmt.Errorf("%w: source and target paths must differ", admincontent.ErrInvalidEvent)
	}
	entries, manifest, err := admin.manifest()
	if err != nil {
		return "", err
	}
	if contentRevision(manifest) != expectedManifestRevision {
		return "", admincontent.ErrManifestStale
	}

	var source eventEntry
	targetMenuPath := strings.TrimPrefix(targetPath, "/") + ".yaml"
	for _, entry := range entries {
		if entry.Path == sourcePath {
			source = entry
		}
		if entry.Path == targetPath || entry.Menu == targetMenuPath {
			return "", admincontent.ErrEventExists
		}
	}
	if source.Path == "" {
		return "", admincontent.ErrEventNotFound
	}

	menuContent, err := fs.ReadFile(admin.registry.content, source.Menu)
	if err != nil {
		return "", fmt.Errorf("read source event menu: %w", err)
	}
	config, err := menu.Decode(bytes.NewReader(menuContent))
	if err != nil {
		return "", fmt.Errorf("%w: %v", admincontent.ErrInvalidMenu, err)
	}

	writer, ok := admin.registry.content.(interface {
		writeNewFile(string, []byte) error
		writeFileAtomic(string, string, []byte) error
		removeFileRevision(string, string) error
	})
	if !ok {
		return "", admincontent.ErrContentReadOnly
	}

	var logoPath string
	var logoContent []byte
	if config.Conference.Logo != "" && isUploadedEventLogoPath(source.Menu, config.Conference.Logo) {
		logoContent, err = fs.ReadFile(admin.registry.content, config.Conference.Logo)
		if err != nil {
			return "", fmt.Errorf("read source event logo: %w", err)
		}
		digest := sha256.Sum256(logoContent)
		base := strings.TrimSuffix(path.Base(targetMenuPath), path.Ext(targetMenuPath))
		logoPath = path.Join(path.Dir(targetMenuPath), base+"-logo-"+hex.EncodeToString(digest[:8])+strings.ToLower(path.Ext(config.Conference.Logo)))
		menuContent, err = replaceMenuLogo(menuContent, logoPath)
		if err != nil {
			return "", err
		}
	}
	if err := admin.ValidateMenu(menuContent); err != nil {
		return "", err
	}

	if logoPath != "" {
		if err := writer.writeNewFile(logoPath, logoContent); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return "", admincontent.ErrEventExists
			}
			return "", fmt.Errorf("create duplicated event logo: %w", err)
		}
	}
	rollbackLogo := func() {
		if logoPath != "" {
			_ = writer.removeFileRevision(logoPath, contentRevision(logoContent))
		}
	}
	if err := writer.writeNewFile(targetMenuPath, menuContent); err != nil {
		rollbackLogo()
		if errors.Is(err, fs.ErrExist) {
			return "", admincontent.ErrEventExists
		}
		return "", fmt.Errorf("create duplicated event menu: %w", err)
	}
	rollbackFiles := func() {
		_ = writer.removeFileRevision(targetMenuPath, contentRevision(menuContent))
		rollbackLogo()
	}

	entries = append(entries, eventEntry{Path: targetPath, Menu: targetMenuPath})
	updated, err := encodeEventManifest(entries)
	if err != nil {
		rollbackFiles()
		return "", err
	}
	if err := writer.writeFileAtomic(eventManifestFile, expectedManifestRevision, updated); err != nil {
		rollbackFiles()
		return "", fmt.Errorf("update event manifest: %w", err)
	}
	admin.invalidateRegistry()
	return contentRevision(updated), nil
}

func replaceMenuLogo(content []byte, logoPath string) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("%w: decode menu for logo duplication: %v", admincontent.ErrInvalidMenu, err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: menu root must be a mapping", admincontent.ErrInvalidMenu)
	}
	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value != "conference" || root.Content[index+1].Kind != yaml.MappingNode {
			continue
		}
		conference := root.Content[index+1]
		for field := 0; field+1 < len(conference.Content); field += 2 {
			if conference.Content[field].Value == "logo" {
				conference.Content[field+1].Value = logoPath
				conference.Content[field+1].Tag = "!!str"
				var output bytes.Buffer
				encoder := yaml.NewEncoder(&output)
				encoder.SetIndent(2)
				if err := encoder.Encode(&document); err != nil {
					return nil, fmt.Errorf("%w: encode duplicated menu: %v", admincontent.ErrInvalidMenu, err)
				}
				if err := encoder.Close(); err != nil {
					return nil, fmt.Errorf("%w: encode duplicated menu: %v", admincontent.ErrInvalidMenu, err)
				}
				return output.Bytes(), nil
			}
		}
	}
	return nil, fmt.Errorf("%w: source event logo field is missing", admincontent.ErrInvalidMenu)
}

func (admin *eventAdmin) DeleteEvent(eventPath, confirmation, expectedManifestRevision string) (string, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	if confirmation != eventPath {
		return "", fmt.Errorf("%w: confirmation must exactly match the event path", admincontent.ErrInvalidEvent)
	}
	entries, manifest, err := admin.manifest()
	if err != nil {
		return "", err
	}
	if contentRevision(manifest) != expectedManifestRevision {
		return "", admincontent.ErrManifestStale
	}
	var removed eventEntry
	remaining := make([]eventEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Path == eventPath {
			removed = entry
			continue
		}
		remaining = append(remaining, entry)
	}
	if removed.Path == "" {
		return "", admincontent.ErrEventNotFound
	}
	for _, entry := range remaining {
		if entry.Menu == removed.Menu {
			return "", fmt.Errorf("%w: menu file is shared by another event", admincontent.ErrInvalidEvent)
		}
	}
	menuContent, err := fs.ReadFile(admin.registry.content, removed.Menu)
	if err != nil {
		return "", fmt.Errorf("read event before removal: %w", err)
	}
	logoRevisions, err := admin.uploadedEventLogos(removed.Menu)
	if err != nil {
		return "", err
	}
	for logoPath := range logoRevisions {
		if admin.logoIsShared(logoPath, remaining) {
			delete(logoRevisions, logoPath)
		}
	}
	updated, err := encodeEventManifest(remaining)
	if err != nil {
		return "", err
	}
	writer, ok := admin.registry.content.(interface {
		writeFileAtomic(string, string, []byte) error
		removeFileRevision(string, string) error
	})
	if !ok {
		return "", admincontent.ErrContentReadOnly
	}
	if err := writer.writeFileAtomic(eventManifestFile, expectedManifestRevision, updated); err != nil {
		return "", fmt.Errorf("update event manifest: %w", err)
	}
	admin.invalidateRegistry()
	if err := writer.removeFileRevision(removed.Menu, contentRevision(menuContent)); err != nil {
		return "", fmt.Errorf("remove event menu after route removal: %w", err)
	}
	logoPaths := make([]string, 0, len(logoRevisions))
	for logoPath := range logoRevisions {
		logoPaths = append(logoPaths, logoPath)
	}
	sort.Strings(logoPaths)
	for _, logoPath := range logoPaths {
		if err := writer.removeFileRevision(logoPath, logoRevisions[logoPath]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("remove event logo after route removal: %w", err)
		}
	}
	if historyWriter, ok := admin.registry.content.(interface{ removeDirectory(string) error }); ok {
		if err := historyWriter.removeDirectory(eventHistoryDirectory(removed.Menu)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("remove event history after route removal: %w", err)
		}
	}
	return contentRevision(updated), nil
}

func (admin *eventAdmin) manifest() ([]eventEntry, []byte, error) {
	content, err := fs.ReadFile(admin.registry.content, eventManifestFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read writable event manifest: %w", err)
	}
	entries, err := decodeEventEntries(bytes.NewReader(content), eventManifestFile)
	if err != nil {
		return nil, nil, err
	}
	return entries, content, nil
}

func encodeEventManifest(entries []eventEntry) ([]byte, error) {
	content, err := yaml.Marshal(struct {
		Events []eventEntry `yaml:"events"`
	}{Events: entries})
	if err != nil {
		return nil, fmt.Errorf("encode event manifest: %w", err)
	}
	return content, nil
}

func (admin *eventAdmin) invalidateRegistry() {
	admin.registry.mu.Lock()
	admin.registry.nextReloadAfter = time.Time{}
	admin.registry.mu.Unlock()
}

func (admin *eventAdmin) uploadedEventLogos(menuPath string) (map[string]string, error) {
	directory := path.Dir(menuPath)
	entries, err := fs.ReadDir(admin.registry.content, directory)
	if err != nil {
		return nil, fmt.Errorf("list event logos before removal: %w", err)
	}
	logos := make(map[string]string)
	for _, entry := range entries {
		logoPath := path.Join(directory, entry.Name())
		if !isUploadedEventLogoPath(menuPath, logoPath) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect event logo before removal: %w", err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		content, err := fs.ReadFile(admin.registry.content, logoPath)
		if err != nil {
			return nil, fmt.Errorf("read event logo before removal: %w", err)
		}
		logos[logoPath] = contentRevision(content)
	}
	return logos, nil
}

func isUploadedEventLogoPath(menuPath, logoPath string) bool {
	base := strings.TrimSuffix(path.Base(menuPath), path.Ext(menuPath)) + "-logo-"
	logoBase := path.Base(logoPath)
	extension := strings.ToLower(path.Ext(logoBase))
	stem := strings.TrimSuffix(logoBase, extension)
	if path.Dir(logoPath) != path.Dir(menuPath) || (extension != ".png" && extension != ".jpg") || !strings.HasPrefix(stem, base) {
		return false
	}
	digest := strings.TrimPrefix(stem, base)
	if len(digest) != 16 {
		return false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return false
	}
	return true
}

func (admin *eventAdmin) logoIsShared(logoPath string, entries []eventEntry) bool {
	for _, entry := range entries {
		content, err := fs.ReadFile(admin.registry.content, entry.Menu)
		if err != nil {
			return true
		}
		config, err := menu.Decode(bytes.NewReader(content))
		if err != nil || config.Conference.Logo == logoPath {
			return true
		}
	}
	return false
}

func (admin *eventAdmin) ReadEvent(eventPath string) ([]byte, string, error) {
	route, err := admin.route(eventPath)
	if err != nil {
		return nil, "", err
	}
	content, err := fs.ReadFile(admin.registry.content, route.menuPath)
	if err != nil {
		return nil, "", fmt.Errorf("read admin event: %w", err)
	}
	return content, contentRevision(content), nil
}

func (admin *eventAdmin) ValidateMenu(content []byte) error {
	if _, err := menu.Decode(bytes.NewReader(content)); err != nil {
		return fmt.Errorf("%w: %v", admincontent.ErrInvalidMenu, err)
	}
	return nil
}

func (admin *eventAdmin) UploadLogo(eventPath, extension string, content []byte) (string, error) {
	if extension != ".png" && extension != ".jpg" {
		return "", admincontent.ErrInvalidLogo
	}
	route, err := admin.route(eventPath)
	if err != nil {
		return "", err
	}
	writer, ok := admin.registry.content.(interface {
		writeAssetAtomic(string, []byte) error
	})
	if !ok {
		return "", admincontent.ErrContentReadOnly
	}
	base := strings.TrimSuffix(path.Base(route.menuPath), path.Ext(route.menuPath))
	digest := sha256.Sum256(content)
	logoPath := path.Join(path.Dir(route.menuPath), base+"-logo-"+hex.EncodeToString(digest[:8])+extension)
	if err := writer.writeAssetAtomic(logoPath, content); err != nil {
		return "", fmt.Errorf("write admin logo: %w", err)
	}
	return logoPath, nil
}

func (admin *eventAdmin) PublishEvent(eventPath, expectedRevision string, content []byte) (string, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	route, err := admin.route(eventPath)
	if err != nil {
		return "", err
	}
	return admin.publishEventLocked(route.menuPath, expectedRevision, content)
}

func (admin *eventAdmin) EventHistory(eventPath string) ([]admincontent.Revision, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	route, err := admin.route(eventPath)
	if err != nil {
		return nil, err
	}
	if err := admin.pruneEventHistory(route.menuPath); err != nil {
		return nil, err
	}
	return admin.eventHistory(route.menuPath)
}

func (admin *eventAdmin) RestoreEvent(eventPath, historyID, expectedCurrentRevision string) ([]byte, string, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	route, err := admin.route(eventPath)
	if err != nil {
		return nil, "", err
	}
	_, historicalDigest, err := parseHistoryID(historyID)
	if err != nil {
		return nil, "", err
	}
	historyPath := path.Join(eventHistoryDirectory(route.menuPath), historyID+".yaml")
	content, err := fs.ReadFile(admin.registry.content, historyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", admincontent.ErrHistoryNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("read event history: %w", err)
	}
	if !strings.HasPrefix(strings.TrimPrefix(contentRevision(content), "sha256:"), historicalDigest) {
		return nil, "", admincontent.ErrInvalidHistory
	}
	revision, err := admin.publishEventLocked(route.menuPath, expectedCurrentRevision, content)
	if err != nil {
		return nil, "", err
	}
	return content, revision, nil
}

func (admin *eventAdmin) publishEventLocked(menuPath, expectedRevision string, content []byte) (string, error) {
	if err := admin.ValidateMenu(content); err != nil {
		return "", err
	}
	current, err := fs.ReadFile(admin.registry.content, menuPath)
	if err != nil {
		return "", fmt.Errorf("read current admin event: %w", err)
	}
	if contentRevision(current) != expectedRevision {
		return "", admincontent.ErrRevisionStale
	}
	newRevision := contentRevision(content)
	if bytes.Equal(current, content) {
		return newRevision, nil
	}
	writer, ok := admin.registry.content.(interface {
		writeFileAtomic(string, string, []byte) error
		writeHistoryFile(string, []byte) error
		removeFileRevision(string, string) error
	})
	if !ok {
		return "", admincontent.ErrContentReadOnly
	}
	historyPath := path.Join(eventHistoryDirectory(menuPath), newHistoryID(time.Now().UTC(), current)+".yaml")
	if err := writer.writeHistoryFile(historyPath, current); err != nil {
		return "", fmt.Errorf("archive current event menu: %w", err)
	}
	if err := writer.writeFileAtomic(menuPath, expectedRevision, content); err != nil {
		_ = writer.removeFileRevision(historyPath, contentRevision(current))
		return "", fmt.Errorf("publish admin event: %w", err)
	}
	// Publication is already complete. Retention cleanup is best-effort and is
	// retried whenever history is listed or another version is published.
	_ = admin.pruneEventHistory(menuPath)
	return newRevision, nil
}

func eventHistoryDirectory(menuPath string) string {
	base := strings.TrimSuffix(path.Base(menuPath), path.Ext(menuPath))
	digest := sha256.Sum256([]byte(menuPath))
	return path.Join(eventHistoryRoot, base+"-"+hex.EncodeToString(digest[:8]))
}

func newHistoryID(archivedAt time.Time, content []byte) string {
	digest := sha256.Sum256(content)
	return archivedAt.UTC().Format(historyTimeLayout) + "-" + hex.EncodeToString(digest[:])
}

func parseHistoryID(id string) (time.Time, string, error) {
	separator := strings.LastIndexByte(id, '-')
	if separator <= 0 {
		return time.Time{}, "", admincontent.ErrInvalidHistory
	}
	archivedAt, err := time.Parse(historyTimeLayout, id[:separator])
	if err != nil {
		return time.Time{}, "", admincontent.ErrInvalidHistory
	}
	digest := id[separator+1:]
	if len(digest) != sha256.Size*2 {
		return time.Time{}, "", admincontent.ErrInvalidHistory
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return time.Time{}, "", admincontent.ErrInvalidHistory
	}
	return archivedAt, digest, nil
}

func (admin *eventAdmin) eventHistory(menuPath string) ([]admincontent.Revision, error) {
	directory := eventHistoryDirectory(menuPath)
	entries, err := fs.ReadDir(admin.registry.content, directory)
	if errors.Is(err, fs.ErrNotExist) {
		return []admincontent.Revision{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list event history: %w", err)
	}
	revisions := make([]admincontent.Revision, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".yaml" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".yaml")
		archivedAt, digest, err := parseHistoryID(id)
		if err != nil {
			continue
		}
		revisions = append(revisions, admincontent.Revision{ID: id, Revision: "sha256:" + digest, ArchivedAt: archivedAt})
	}
	sort.Slice(revisions, func(left, right int) bool { return revisions[left].ArchivedAt.After(revisions[right].ArchivedAt) })
	return revisions, nil
}

func (admin *eventAdmin) pruneEventHistory(menuPath string) error {
	revisions, err := admin.eventHistory(menuPath)
	if err != nil || len(revisions) <= eventHistoryLimit {
		return err
	}
	writer, ok := admin.registry.content.(interface{ removeFileRevision(string, string) error })
	if !ok {
		return admincontent.ErrContentReadOnly
	}
	directory := eventHistoryDirectory(menuPath)
	for _, revision := range revisions[eventHistoryLimit:] {
		if err := writer.removeFileRevision(path.Join(directory, revision.ID+".yaml"), revision.Revision); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("prune event history: %w", err)
		}
	}
	return nil
}

func (admin *eventAdmin) route(eventPath string) (eventRoute, error) {
	routes, err := admin.routes()
	if err != nil {
		return eventRoute{}, err
	}
	route, ok := routes[eventPath]
	if !ok {
		return eventRoute{}, admincontent.ErrEventNotFound
	}
	return route, nil
}

func (admin *eventAdmin) routes() (map[string]eventRoute, error) {
	if _, err := admin.registry.Current(); err != nil {
		return nil, err
	}
	admin.registry.mu.Lock()
	defer admin.registry.mu.Unlock()
	routes := make(map[string]eventRoute, len(admin.registry.current))
	for eventPath, route := range admin.registry.current {
		routes[eventPath] = route
	}
	return routes, nil
}

func contentRevision(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (root *rootedFS) writeFileAtomic(name, expectedRevision string, content []byte) error {
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid content path %q", name)
	}
	info, err := root.root.Stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("content path %q is not a regular file", name)
	}

	temporaryName, file, err := root.createTemporaryFile(name, info.Mode().Perm())
	if err != nil {
		return err
	}
	keepTemporary := true
	fileOpen := true
	defer func() {
		if fileOpen {
			_ = file.Close()
		}
		if keepTemporary {
			_ = root.root.Remove(temporaryName)
		}
	}()

	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	fileOpen = false

	// Recheck immediately before replacing the target. This catches an edit made
	// by another writer while the replacement file was being prepared.
	current, err := root.root.ReadFile(name)
	if err != nil {
		return err
	}
	if contentRevision(current) != expectedRevision {
		return admincontent.ErrRevisionStale
	}
	if err := root.root.Rename(temporaryName, name); err != nil {
		return err
	}
	keepTemporary = false
	return nil
}

func (root *rootedFS) writeAssetAtomic(name string, content []byte) error {
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid content path %q", name)
	}
	temporaryName, file, err := root.createTemporaryFile(name, 0o644)
	if err != nil {
		return err
	}
	keepTemporary := true
	fileOpen := true
	defer func() {
		if fileOpen {
			_ = file.Close()
		}
		if keepTemporary {
			_ = root.root.Remove(temporaryName)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	fileOpen = false
	if err := root.root.Rename(temporaryName, name); err != nil {
		return err
	}
	keepTemporary = false
	return nil
}

func (root *rootedFS) writeNewFile(name string, content []byte) error {
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid content path %q", name)
	}
	file, err := root.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = root.root.Remove(name)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}

func (root *rootedFS) writeHistoryFile(name string, content []byte) error {
	if !fs.ValidPath(name) || !strings.HasPrefix(name, eventHistoryRoot+"/") || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid history path %q", name)
	}
	if err := root.root.MkdirAll(path.Dir(name), 0o700); err != nil {
		return err
	}
	return root.writeNewFile(name, content)
}

func (root *rootedFS) removeDirectory(name string) error {
	if !fs.ValidPath(name) || !strings.HasPrefix(name, eventHistoryRoot+"/") || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid history directory %q", name)
	}
	return root.root.RemoveAll(name)
}

func (root *rootedFS) removeFileRevision(name, expectedRevision string) error {
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid content path %q", name)
	}
	info, err := root.root.Stat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("content path %q is not a regular file", name)
	}
	content, err := root.root.ReadFile(name)
	if err != nil {
		return err
	}
	if contentRevision(content) != expectedRevision {
		return admincontent.ErrRevisionStale
	}
	return root.root.Remove(name)
}

func (root *rootedFS) verifyWritable() error {
	temporaryName, file, err := root.createTemporaryFile("events.yaml", 0o600)
	if err != nil {
		return err
	}
	fileOpen := true
	defer func() {
		if fileOpen {
			_ = file.Close()
		}
		_ = root.root.Remove(temporaryName)
	}()
	if err := file.Close(); err != nil {
		return err
	}
	fileOpen = false
	if err := root.root.Remove(temporaryName); err != nil {
		return err
	}
	manifest, err := root.root.ReadFile(eventManifestFile)
	if errors.Is(err, fs.ErrNotExist) {
		if createErr := root.writeNewFile(eventManifestFile, []byte("events: []\n")); createErr != nil && !errors.Is(createErr, fs.ErrExist) {
			return createErr
		}
		manifest, err = root.root.ReadFile(eventManifestFile)
	}
	if err != nil {
		return err
	}
	if _, err := decodeEventEntries(bytes.NewReader(manifest), eventManifestFile); err != nil {
		return err
	}
	// Replacing the manifest with identical bytes verifies that it supports the
	// same atomic rename used for real route mutations. A separate file bind
	// mount, for example, fails here instead of failing during an admin action.
	return root.writeFileAtomic(eventManifestFile, contentRevision(manifest), manifest)
}

func (root *rootedFS) createTemporaryFile(target string, mode fs.FileMode) (string, *os.File, error) {
	directory := path.Dir(target)
	base := path.Base(target)
	for range 10 {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := path.Join(directory, "."+base+".manna-"+hex.EncodeToString(random[:]))
		file, err := root.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("could not create a unique temporary file for %q", target)
}
