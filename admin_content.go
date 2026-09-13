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

	admincontent "manna/internal/admin"
	"manna/internal/menu"
)

type eventAdmin struct {
	registry  *eventRegistry
	publishMu sync.Mutex
}

var _ admincontent.Content = (*eventAdmin)(nil)

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

func (admin *eventAdmin) PublishEvent(eventPath, expectedRevision string, content []byte) (string, error) {
	admin.publishMu.Lock()
	defer admin.publishMu.Unlock()

	route, err := admin.route(eventPath)
	if err != nil {
		return "", err
	}
	if err := admin.ValidateMenu(content); err != nil {
		return "", err
	}
	current, err := fs.ReadFile(admin.registry.content, route.menuPath)
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
	})
	if !ok {
		return "", admincontent.ErrContentReadOnly
	}
	if err := writer.writeFileAtomic(route.menuPath, expectedRevision, content); err != nil {
		return "", fmt.Errorf("publish admin event: %w", err)
	}
	return newRevision, nil
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
	return root.root.Remove(temporaryName)
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
