package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"manna/internal/menu"
)

type eventEntry struct {
	Path string `yaml:"path"`
	Menu string `yaml:"menu"`
}

type eventRoute struct {
	menuPath string
	loader   menu.Loader
}

type eventRegistry struct {
	mu              sync.Mutex
	reloading       bool
	content         fs.FS
	current         map[string]eventRoute
	reloadInterval  time.Duration
	nextReloadAfter time.Time
}

var eventPathPattern = regexp.MustCompile(`^/[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	eventManifestFile         = "events.yaml"
	eventManifestTemplateFile = "events.example.yaml"
)

type rootedFS struct {
	root *os.Root
}

func (root *rootedFS) Open(name string) (fs.File, error) {
	return root.root.Open(name)
}

func (root *rootedFS) Stat(name string) (fs.FileInfo, error) {
	return root.root.Stat(name)
}

func (root *rootedFS) Close() error {
	return root.root.Close()
}

func loadContentFS() (fs.FS, error) {
	if dir := os.Getenv("CONTENT_DIR"); dir != "" {
		// os.Root prevents relative paths and symlinks from escaping CONTENT_DIR.
		root, err := os.OpenRoot(dir)
		if err != nil {
			return nil, err
		}
		return &rootedFS{root: root}, nil
	}
	return fs.Sub(assets, "content")
}

func loadEventFS(content fs.FS) (map[string]menu.Loader, error) {
	routes, err := loadEventRoutes(content, nil)
	if err != nil {
		return nil, err
	}
	return eventLoaders(routes), nil
}

func newEventRegistry(content fs.FS) (*eventRegistry, error) {
	return newEventRegistryWithInterval(content, 500*time.Millisecond)
}

func newEventRegistryWithInterval(content fs.FS, reloadInterval time.Duration) (*eventRegistry, error) {
	routes, err := loadEventRoutes(content, nil)
	if err != nil {
		return nil, err
	}
	return &eventRegistry{
		content:         content,
		current:         routes,
		reloadInterval:  reloadInterval,
		nextReloadAfter: time.Now().Add(reloadInterval),
	}, nil
}

func (registry *eventRegistry) Current() (map[string]menu.Loader, error) {
	registry.mu.Lock()
	if registry.reloading || time.Now().Before(registry.nextReloadAfter) {
		events := eventLoaders(registry.current)
		registry.mu.Unlock()
		return events, nil
	}
	registry.reloading = true
	current := registry.current
	registry.mu.Unlock()

	routes, err := loadEventRoutes(registry.content, current)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.reloading = false
	registry.nextReloadAfter = time.Now().Add(registry.reloadInterval)
	if err == nil {
		registry.current = routes
	}
	return eventLoaders(registry.current), err
}

func eventLoaders(routes map[string]eventRoute) map[string]menu.Loader {
	events := make(map[string]menu.Loader, len(routes))
	for eventPath, route := range routes {
		events[eventPath] = route.loader
	}
	return events
}

func loadEventRoutes(content fs.FS, current map[string]eventRoute) (map[string]eventRoute, error) {
	entries, err := loadEventEntries(content)
	if err != nil {
		return nil, err
	}
	routes := make(map[string]eventRoute, len(entries))
	for _, entry := range entries {
		if route, ok := current[entry.Path]; ok && route.menuPath == entry.Menu {
			routes[entry.Path] = route
			continue
		}
		menuPath := entry.Menu
		store, err := menu.NewStore(func() (menu.Config, error) {
			file, err := content.Open(menuPath)
			if err != nil {
				return menu.Config{}, err
			}
			defer file.Close()
			return menu.Decode(file)
		})
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", entry.Path, err)
		}
		routes[entry.Path] = eventRoute{menuPath: menuPath, loader: store.Current}
	}
	return routes, nil
}

func loadEventEntries(content fs.FS) ([]eventEntry, error) {
	manifestPath := eventManifestFile
	file, err := content.Open(manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		manifestPath = eventManifestTemplateFile
		file, err = content.Open(manifestPath)
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var manifest struct {
		Events []eventEntry `yaml:"events"`
	}
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode %s: %w", manifestPath, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%s must contain exactly one YAML document", manifestPath)
	}
	seen := make(map[string]bool, len(manifest.Events))
	for _, entry := range manifest.Events {
		if !eventPathPattern.MatchString(entry.Path) || entry.Path == "/healthz" || entry.Path == "/static" || entry.Path == "/branding" || entry.Path == "/admin" {
			return nil, fmt.Errorf("invalid or reserved event path %q", entry.Path)
		}
		if seen[entry.Path] {
			return nil, fmt.Errorf("duplicate event path %q", entry.Path)
		}
		seen[entry.Path] = true
		if !fs.ValidPath(entry.Menu) || strings.Contains(entry.Menu, `\`) {
			return nil, fmt.Errorf("invalid menu path %q", entry.Menu)
		}
	}
	return manifest.Events, nil
}
