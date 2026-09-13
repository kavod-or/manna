package main

import (
	"io/fs"
	"mana/internal/menu"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestEmbeddedEvents(t *testing.T) {
	content, err := loadContentFS()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := content.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	events, err := loadEventFS(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("event count %d", len(events))
	}
	first, err := events["/example-conference"]()
	if err != nil {
		t.Fatal(err)
	}
	second, err := events["/community-day"]()
	if err != nil {
		t.Fatal(err)
	}
	if first.Conference.Name.EN == second.Conference.Name.EN {
		t.Fatal("events share menu")
	}
	if first.Conference.EffectiveCurrency() != menu.Dollar {
		t.Fatalf("example conference currency = %q, want dollar", first.Conference.EffectiveCurrency())
	}
	if second.Conference.EffectiveCurrency() != menu.Schekel {
		t.Fatalf("community day currency = %q, want schekel", second.Conference.EffectiveCurrency())
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
	for _, menuPath := range []string{"../secret.yaml", "/etc/passwd", `..\secret.yaml`, "./menu.yaml"} {
		manifest := "events:\n  - path: /test\n    menu: '" + menuPath + "'\n"
		_, err := loadEventFS(fstest.MapFS{"events.yaml": {Data: []byte(manifest)}})
		if err == nil {
			t.Fatalf("accepted menu path %q", menuPath)
		}
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
