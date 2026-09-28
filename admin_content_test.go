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

	admincontent "manna/internal/admin"
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

func TestEventAdminStoresUploadedLogoBesideMenu(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	logo := []byte("validated png bytes")
	logoPath, err := admin.UploadLogo("/alpha", ".png", logo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(logoPath, "alpha-logo-") || !strings.HasSuffix(logoPath, ".png") {
		t.Fatalf("logo path = %q", logoPath)
	}
	written, err := os.ReadFile(filepath.Join(contentDir, logoPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(logo) {
		t.Fatal("uploaded logo contents changed")
	}
	if _, err := admin.UploadLogo("/alpha", ".gif", logo); !errors.Is(err, admincontent.ErrInvalidLogo) {
		t.Fatalf("invalid extension error = %v", err)
	}
}

func TestEventAdminCreatesBlankEventAndUpdatesManifest(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	newRevision, err := admin.CreateEvent("/team-day", revision)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(contentDir, eventManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if newRevision != contentRevision(manifest) || !strings.Contains(string(manifest), "path: /team-day") || !strings.Contains(string(manifest), "menu: team-day.yaml") {
		t.Fatalf("created manifest = %s", manifest)
	}
	created, err := os.ReadFile(filepath.Join(contentDir, "team-day.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.ValidateMenu(created); err != nil {
		t.Fatalf("blank event menu is invalid: %v", err)
	}
	if _, _, err := admin.ReadEvent("/team-day"); err != nil {
		t.Fatalf("created event was not immediately available: %v", err)
	}
}

func TestEventAdminCreateRejectsUnsafePathsCollisionsAndStaleManifest(t *testing.T) {
	for _, eventPath := range []string{"team-day", "/../team-day", "/admin", "/Team-Day", "/team--day"} {
		t.Run(eventPath, func(t *testing.T) {
			admin, _ := newTestEventAdmin(t)
			revision, err := admin.ManifestRevision()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := admin.CreateEvent(eventPath, revision); !errors.Is(err, admincontent.ErrInvalidEvent) {
				t.Fatalf("CreateEvent(%q) error = %v", eventPath, err)
			}
		})
	}
	admin, contentDir := newTestEventAdmin(t)
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateEvent("/alpha", revision); !errors.Is(err, admincontent.ErrEventExists) {
		t.Fatalf("duplicate event error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "orphan.yaml"), []byte(blankEventMenu), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateEvent("/orphan", revision); !errors.Is(err, admincontent.ErrEventExists) {
		t.Fatalf("orphan collision error = %v", err)
	}
	if _, err := admin.CreateEvent("/new-event", "sha256:stale"); !errors.Is(err, admincontent.ErrManifestStale) {
		t.Fatalf("stale manifest error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, "new-event.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale create wrote a menu: %v", err)
	}
}

func TestEventAdminDeleteRequiresExactConfirmationAndDeletesOwnedFiles(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	logoPath := "alpha-logo-0123456789abcdef.png"
	unpublishedLogoPath := "alpha-logo-fedcba9876543210.jpg"
	unrelatedPath := "other-logo-0123456789abcdef.png"
	alpha := strings.Replace(testMenu("Alpha"), "conference:\n", "conference:\n  logo: "+logoPath+"\n", 1)
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), []byte(alpha), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{logoPath, unpublishedLogoPath, unrelatedPath} {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte("logo"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DeleteEvent("/alpha", "alpha", revision); !errors.Is(err, admincontent.ErrInvalidEvent) {
		t.Fatalf("confirmation error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, "alpha.yaml")); err != nil {
		t.Fatal("failed confirmation changed event files")
	}
	newRevision, err := admin.DeleteEvent("/alpha", "/alpha", revision)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(contentDir, eventManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if newRevision != contentRevision(manifest) || strings.Contains(string(manifest), "/alpha") {
		t.Fatalf("removed event remains in manifest: %s", manifest)
	}
	for _, name := range []string{"alpha.yaml", logoPath, unpublishedLogoPath} {
		if _, err := os.Stat(filepath.Join(contentDir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("removed file %q still exists: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(contentDir, unrelatedPath)); err != nil {
		t.Fatal("unrelated logo was deleted")
	}
	if _, _, err := admin.ReadEvent("/alpha"); !errors.Is(err, admincontent.ErrEventNotFound) {
		t.Fatalf("removed route read error = %v", err)
	}
}

func TestEventAdminDeletePreservesCustomBrandingAndRejectsStaleManifest(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	alpha := strings.Replace(testMenu("Alpha"), "conference:\n", "conference:\n  logo: shared-brand.png\n", 1)
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), []byte(alpha), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "shared-brand.png"), []byte("logo"), 0o600); err != nil {
		t.Fatal(err)
	}
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DeleteEvent("/alpha", "/alpha", "sha256:stale"); !errors.Is(err, admincontent.ErrManifestStale) {
		t.Fatalf("stale delete error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, "alpha.yaml")); err != nil {
		t.Fatal("stale delete removed the menu")
	}
	if _, err := admin.DeleteEvent("/alpha", "/alpha", revision); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, "shared-brand.png")); err != nil {
		t.Fatal("custom branding was deleted")
	}
}

func TestEventAdminDeleteRefusesSharedMenu(t *testing.T) {
	contentDir := t.TempDir()
	files := map[string]string{
		"events.yaml": "events:\n  - path: /alpha\n    menu: shared.yaml\n  - path: /beta\n    menu: shared.yaml\n",
		"shared.yaml": testMenu("Shared"),
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
	registry, err := newEventRegistryWithInterval(&rootedFS{root: root}, 0)
	if err != nil {
		t.Fatal(err)
	}
	admin := newEventAdmin(registry)
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DeleteEvent("/alpha", "/alpha", revision); !errors.Is(err, admincontent.ErrInvalidEvent) {
		t.Fatalf("shared menu delete error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, "shared.yaml")); err != nil {
		t.Fatal("shared menu was deleted")
	}
	manifest, err := os.ReadFile(filepath.Join(contentDir, eventManifestFile))
	if err != nil || !strings.Contains(string(manifest), "/alpha") {
		t.Fatalf("shared event was removed from manifest: %v\n%s", err, manifest)
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

func TestRootedContentWriteCheckRequiresManifest(t *testing.T) {
	contentDir := t.TempDir()
	rootHandle, err := os.OpenRoot(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rootHandle.Close() })
	root := &rootedFS{root: rootHandle}
	if err := root.verifyWritable(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing manifest error = %v", err)
	}
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("write probe remained after failed check: %#v", entries)
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
