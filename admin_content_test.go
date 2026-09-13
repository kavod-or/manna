package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	admincontent "mana/internal/admin"
)

func TestEventAdminListsAndReadsExistingEvents(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)

	events, err := admin.Events()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"/alpha", "/beta"}) {
		t.Fatalf("events = %#v", events)
	}

	content, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(want) {
		t.Fatal("admin read did not preserve the original YAML bytes")
	}
	if revision != contentRevision(content) || !strings.HasPrefix(revision, "sha256:") {
		t.Fatalf("revision = %q", revision)
	}
}

func TestEventAdminRejectsUnknownEvent(t *testing.T) {
	admin, _ := newTestEventAdmin(t)
	for _, eventPath := range []string{"/missing", "/../alpha", "alpha", "/admin"} {
		if _, _, err := admin.ReadEvent(eventPath); !errors.Is(err, admincontent.ErrEventNotFound) {
			t.Errorf("ReadEvent(%q) error = %v", eventPath, err)
		}
	}
}

func TestEventAdminUsesCurrentManifest(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	manifest := []byte("events:\n  - path: /beta\n    menu: beta.yaml\n")
	if err := os.WriteFile(filepath.Join(contentDir, "events.yaml"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}

	events, err := admin.Events()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"/beta"}) {
		t.Fatalf("events after manifest update = %#v", events)
	}
	if _, _, err := admin.ReadEvent("/alpha"); !errors.Is(err, admincontent.ErrEventNotFound) {
		t.Fatalf("removed event read error = %v", err)
	}
}

func TestEventAdminValidatesWithoutWriting(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	before, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if err := admin.ValidateMenu([]byte("conference: [")); !errors.Is(err, admincontent.ErrInvalidMenu) {
		t.Fatalf("validation error = %v, want invalid menu", err)
	}
	after, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("validation changed the live file")
	}
}

func TestEventAdminPublishesValidMenuAtomically(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	_, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	updated := []byte(testMenu("Updated Alpha") + "# editor comment\n")

	newRevision, err := admin.PublishEvent("/alpha", revision, updated)
	if err != nil {
		t.Fatal(err)
	}
	if newRevision != contentRevision(updated) {
		t.Fatalf("new revision = %q", newRevision)
	}
	written, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(updated) {
		t.Fatal("published file does not contain the exact proposed YAML")
	}
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("temporary file remained after publish: %#v", entries)
	}
}

func TestEventAdminInvalidPublishLeavesCurrentFile(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	before, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := admin.PublishEvent("/alpha", revision, []byte("days: [")); !errors.Is(err, admincontent.ErrInvalidMenu) {
		t.Fatalf("publish error = %v, want invalid menu", err)
	}
	after, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid publish changed the live file")
	}
}

func TestEventAdminDetectsStaleRevision(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	_, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	hostEdit := []byte(testMenu("Host Edit"))
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), hostEdit, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.PublishEvent("/alpha", revision, []byte(testMenu("Admin Edit"))); !errors.Is(err, admincontent.ErrRevisionStale) {
		t.Fatalf("publish error = %v, want stale revision", err)
	}
	after, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(hostEdit) {
		t.Fatal("stale publish overwrote the host edit")
	}
}

func TestRootedAtomicWriteRechecksRevisionBeforeRename(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	root := admin.registry.content.(*rootedFS)
	hostEdit := []byte(testMenu("Host Edit"))
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), hostEdit, 0o600); err != nil {
		t.Fatal(err)
	}

	err := root.writeFileAtomic("alpha.yaml", contentRevision([]byte(testMenu("Alpha"))), []byte(testMenu("Admin Edit")))
	if !errors.Is(err, admincontent.ErrRevisionStale) {
		t.Fatalf("atomic write error = %v, want stale revision", err)
	}
	written, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(hostEdit) {
		t.Fatal("atomic write overwrote an edit made before its final revision check")
	}
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("temporary file remained after stale write: %#v", entries)
	}
}

func TestRootedContentWriteCheckCleansUpProbe(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	root := admin.registry.content.(*rootedFS)
	if err := root.verifyWritable(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("write probe remained in content directory: %#v", entries)
	}
}

func TestEventAdminCannotPublishToReadOnlyContent(t *testing.T) {
	content := fstest.MapFS{
		"events.yaml": {Data: []byte("events:\n  - path: /alpha\n    menu: alpha.yaml\n")},
		"alpha.yaml":  {Data: []byte(testMenu("Alpha"))},
	}
	registry, err := newEventRegistryWithInterval(content, 0)
	if err != nil {
		t.Fatal(err)
	}
	admin := newEventAdmin(registry)
	_, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}

	_, err = admin.PublishEvent("/alpha", revision, []byte(testMenu("Updated")))
	if !errors.Is(err, admincontent.ErrContentReadOnly) {
		t.Fatalf("publish error = %v, want read-only content", err)
	}
}

func newTestEventAdmin(t *testing.T) (*eventAdmin, string) {
	t.Helper()
	contentDir := t.TempDir()
	files := map[string]string{
		"events.yaml": "events:\n  - path: /beta\n    menu: beta.yaml\n  - path: /alpha\n    menu: alpha.yaml\n",
		"alpha.yaml":  testMenu("Alpha"),
		"beta.yaml":   testMenu("Beta"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	root, err := os.OpenRoot(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	content := &rootedFS{root: root}
	var _ fs.FS = content
	registry, err := newEventRegistryWithInterval(content, 0)
	if err != nil {
		t.Fatal(err)
	}
	return newEventAdmin(registry), contentDir
}
