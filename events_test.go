package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestEmbeddedEvents(t *testing.T) {
	content, err := fs.Sub(assets, "content")
	if err != nil {
		t.Fatal(err)
	}
	events, err := loadEventFS(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("fresh checkout contains demo events: %#v", events)
	}
}

func TestMissingEventManifestIsAnEmptyList(t *testing.T) {
	content := fstest.MapFS{
		"runtime.yaml": {Data: []byte(testMenu("Runtime"))},
	}
	events, err := loadEventFS(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("missing manifest loaded events: %#v", events)
	}
}

func TestRejectInvalidEventPaths(t *testing.T) {
	for _, path := range []string{"/", "/healthz", "/static", "/branding", "/admin", "/../secret", "missing-slash"} {
		_, err := loadEventFS(fstest.MapFS{"events.yaml": {Data: []byte("events:\n  - path: " + path + "\n    menu: menu.yaml\n")}})
		if err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}

func TestRejectInvalidMenuPaths(t *testing.T) {
	for _, menuPath := range []string{
		"../secret.yaml",
		"/etc/passwd",
		`..\secret.yaml`,
		"./menu.yaml",
		"menu.yml",
		"menu.json",
		"events.yaml",
	} {
		manifest := "events:\n  - path: /test\n    menu: '" + menuPath + "'\n"
		_, err := loadEventFS(fstest.MapFS{"events.yaml": {Data: []byte(manifest)}})
		if err == nil {
			t.Fatalf("accepted menu path %q", menuPath)
		}
	}
}

func TestAcceptNestedYAMLMenuPath(t *testing.T) {
	manifest := "events:\n  - path: /test\n    menu: conferences/menu.yaml\n"
	content := fstest.MapFS{
		"events.yaml":           {Data: []byte(manifest)},
		"conferences/menu.yaml": {Data: []byte(testMenu("Nested"))},
	}
	if _, err := loadEventFS(content); err != nil {
		t.Fatalf("rejected a safe nested YAML menu path: %v", err)
	}
}

func TestContentRootRejectsSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	contentDir := filepath.Join(parent, "content")
	if err := os.Mkdir(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "events.yaml"), []byte("events:\n  - path: /test\n    menu: escape.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.yaml"), []byte("secret: server-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../secret.yaml", filepath.Join(contentDir, "escape.yaml")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONTENT_DIR", contentDir)
	content, err := loadContentFS()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := content.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	if _, err := fs.ReadFile(content, "escape.yaml"); err == nil {
		t.Fatal("content filesystem followed a symlink outside its root")
	}
	if _, err := loadEventFS(content); err == nil {
		t.Fatal("event loader accepted a menu symlink outside the content root")
	}
}

func TestEventRegistryReloadsManifestAndKeepsLastValidVersion(t *testing.T) {
	content := fstest.MapFS{
		"events.yaml": {Data: []byte("events:\n  - path: /alpha\n    menu: alpha.yaml\n")},
		"alpha.yaml":  {Data: []byte(testMenu("Alpha"))},
		"beta.yaml":   {Data: []byte(testMenu("Beta"))},
	}
	registry, err := newEventRegistryWithInterval(content, 0)
	if err != nil {
		t.Fatal(err)
	}

	content["events.yaml"] = &fstest.MapFile{Data: []byte("events:\n  - path: /beta\n    menu: beta.yaml\n")}
	events, err := registry.Current()
	if err != nil {
		t.Fatal(err)
	}
	if _, alphaExists := events["/alpha"]; alphaExists || events["/beta"] == nil {
		t.Fatalf("manifest change was not applied: %#v", events)
	}
	beta, err := events["/beta"]()
	if err != nil || beta.Conference.Name.EN != "Beta" {
		t.Fatalf("new event menu = %#v, %v", beta, err)
	}

	content["events.yaml"] = &fstest.MapFile{Data: []byte("events: [")}
	events, err = registry.Current()
	if err == nil {
		t.Fatal("invalid manifest did not report an error")
	}
	if events["/beta"] == nil {
		t.Fatal("invalid manifest replaced the last valid routes")
	}

	content["events.yaml"] = &fstest.MapFile{Data: []byte("events: []\n")}
	events, err = registry.Current()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("removed event remained available: %#v", events)
	}
}

func testMenu(name string) string {
	return "conference:\n" +
		"  name: {de: " + name + ", en: " + name + "}\n" +
		"  location: {de: Foyer, en: Foyer}\n" +
		"days:\n" +
		"  - date: '2026-10-12'\n" +
		"    services:\n" +
		"      - id: lunch\n" +
		"        title: {de: Mittagessen, en: Lunch}\n" +
		"        subtitle: {de: Frisch, en: Fresh}\n" +
		"        from: '12:00'\n" +
		"        until: '13:00'\n" +
		"        items: []\n"
}
