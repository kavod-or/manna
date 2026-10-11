package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	admincontent "manna/internal/admin"
)

func TestEventAdminListsAndReadsExistingEvents(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)

	events, err := admin.Events()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []admincontent.Event{{Path: "/alpha"}, {Path: "/beta"}}) {
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
	if !slices.Equal(events, []admincontent.Event{{Path: "/beta"}}) {
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
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".manna-") && entry.Name() != eventHistoryRoot {
			t.Fatalf("temporary file remained after publish: %#v", entries)
		}
	}
}

func TestEventAdminArchivesAndRestoresPublishedMenus(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	original, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	updated := []byte(testMenu("Updated Alpha") + "# published comment\n")
	updatedRevision, err := admin.PublishEvent("/alpha", revision, updated)
	if err != nil {
		t.Fatal(err)
	}
	history, err := admin.EventHistory("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Revision != contentRevision(original) {
		t.Fatalf("history after publish = %#v", history)
	}
	archived, err := os.ReadFile(filepath.Join(contentDir, eventHistoryDirectory("alpha.yaml"), history[0].ID+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archived, original) {
		t.Fatal("history did not preserve the exact previous YAML")
	}

	restored, restoredRevision, err := admin.RestoreEvent("/alpha", history[0].ID, updatedRevision)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) || restoredRevision != contentRevision(original) {
		t.Fatalf("restored revision = %q, content changed = %t", restoredRevision, !bytes.Equal(restored, original))
	}
	live, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil || !bytes.Equal(live, original) {
		t.Fatalf("restored live menu = %q, %v", live, err)
	}
	history, err = admin.EventHistory("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("restore did not archive the outgoing version: %#v", history)
	}
	foundUpdated := false
	for _, item := range history {
		foundUpdated = foundUpdated || item.Revision == contentRevision(updated)
	}
	if !foundUpdated {
		t.Fatal("outgoing version was not available for reversing the restore")
	}
}

func TestEventAdminHistoryIsBoundedAndSkipsNoOpPublishes(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	current, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.PublishEvent("/alpha", revision, current); err != nil {
		t.Fatal(err)
	}
	history, err := admin.EventHistory("/alpha")
	if err != nil || len(history) != 0 {
		t.Fatalf("no-op publish history = %#v, %v", history, err)
	}
	for index := 0; index < eventHistoryLimit+3; index++ {
		content := []byte(testMenu(fmt.Sprintf("Version %02d", index)))
		revision, err = admin.PublishEvent("/alpha", revision, content)
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err = admin.EventHistory("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != eventHistoryLimit {
		t.Fatalf("history length = %d, want %d", len(history), eventHistoryLimit)
	}
	entries, err := os.ReadDir(filepath.Join(contentDir, eventHistoryDirectory("alpha.yaml")))
	if err != nil || len(entries) != eventHistoryLimit {
		t.Fatalf("stored history entries = %d, %v", len(entries), err)
	}
}

func TestEventAdminRestoreRejectsInvalidMissingAndStaleHistory(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	_, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	updatedRevision, err := admin.PublishEvent("/alpha", revision, []byte(testMenu("Updated")))
	if err != nil {
		t.Fatal(err)
	}
	history, err := admin.EventHistory("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := admin.RestoreEvent("/alpha", "../alpha", updatedRevision); !errors.Is(err, admincontent.ErrInvalidHistory) {
		t.Fatalf("invalid history ID error = %v", err)
	}
	missingID := newHistoryID(time.Now().UTC().Add(time.Second), []byte("missing"))
	if _, _, err := admin.RestoreEvent("/alpha", missingID, updatedRevision); !errors.Is(err, admincontent.ErrHistoryNotFound) {
		t.Fatalf("missing history error = %v", err)
	}
	if _, _, err := admin.RestoreEvent("/alpha", history[0].ID, "sha256:stale"); !errors.Is(err, admincontent.ErrRevisionStale) {
		t.Fatalf("stale restore error = %v", err)
	}
	historyPath := filepath.Join(contentDir, eventHistoryDirectory("alpha.yaml"), history[0].ID+".yaml")
	if err := os.WriteFile(historyPath, []byte(testMenu("Tampered history")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := admin.RestoreEvent("/alpha", history[0].ID, updatedRevision); !errors.Is(err, admincontent.ErrInvalidHistory) {
		t.Fatalf("tampered history error = %v", err)
	}
	invalidID := newHistoryID(time.Now().UTC().Add(2*time.Second), []byte("days: ["))
	historyDir := filepath.Join(contentDir, eventHistoryDirectory("alpha.yaml"))
	if err := os.WriteFile(filepath.Join(historyDir, invalidID+".yaml"), []byte("days: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := admin.RestoreEvent("/alpha", invalidID, updatedRevision); !errors.Is(err, admincontent.ErrInvalidMenu) {
		t.Fatalf("invalid stored menu error = %v", err)
	}
	live, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil || contentRevision(live) != updatedRevision {
		t.Fatal("rejected restore changed the live menu")
	}
}

func TestEventAdminDeleteRemovesOnlyItsHistory(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	for _, eventPath := range []string{"/alpha", "/beta"} {
		_, revision, err := admin.ReadEvent(eventPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.PublishEvent(eventPath, revision, []byte(testMenu("Updated "+eventPath))); err != nil {
			t.Fatal(err)
		}
	}
	manifestRevision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DeleteEvent("/alpha", "/alpha", manifestRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, eventHistoryDirectory("alpha.yaml"))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted event history remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, eventHistoryDirectory("beta.yaml"))); err != nil {
		t.Fatalf("other event history was removed: %v", err)
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

func TestEventAdminDuplicatesPublishedMenu(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	source := []byte(testMenu("Alpha") + "# keep this editor comment\n")
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	newRevision, err := admin.DuplicateEvent("/alpha", "/alpha-copy", revision)
	if err != nil {
		t.Fatal(err)
	}
	duplicated, err := os.ReadFile(filepath.Join(contentDir, "alpha-copy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(duplicated, source) {
		t.Fatal("duplication without an owned logo did not preserve the exact published YAML")
	}
	manifest, err := os.ReadFile(filepath.Join(contentDir, eventManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if newRevision != contentRevision(manifest) || !strings.Contains(string(manifest), "path: /alpha-copy") || !strings.Contains(string(manifest), "menu: alpha-copy.yaml") {
		t.Fatalf("duplicated manifest = %s", manifest)
	}
	if _, _, err := admin.ReadEvent("/alpha-copy"); err != nil {
		t.Fatalf("duplicated event was not immediately available: %v", err)
	}
}

func TestEventAdminDuplicatesOwnedLogoIndependently(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	sourceLogo := "alpha-logo-0123456789abcdef.png"
	source := strings.Replace(testMenu("Alpha"), "conference:\n", "# conference comment\nconference:\n  logo: "+sourceLogo+" # logo comment\n", 1)
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	logo := []byte("published logo bytes")
	if err := os.WriteFile(filepath.Join(contentDir, sourceLogo), logo, 0o600); err != nil {
		t.Fatal(err)
	}
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DuplicateEvent("/alpha", "/alpha-copy", revision); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(logo)
	targetLogo := "alpha-copy-logo-" + hex.EncodeToString(digest[:8]) + ".png"
	duplicated, err := os.ReadFile(filepath.Join(contentDir, "alpha-copy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(duplicated), "logo: "+targetLogo) || !strings.Contains(string(duplicated), "# conference comment") || !strings.Contains(string(duplicated), "# logo comment") {
		t.Fatalf("duplicated menu did not update its logo while preserving comments:\n%s", duplicated)
	}
	writtenLogo, err := os.ReadFile(filepath.Join(contentDir, targetLogo))
	if err != nil || !bytes.Equal(writtenLogo, logo) {
		t.Fatalf("duplicated logo = %q, %v", writtenLogo, err)
	}

	revision, err = admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DeleteEvent("/alpha", "/alpha", revision); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(contentDir, targetLogo)); err != nil {
		t.Fatal("deleting the source removed the duplicate's logo")
	}
}

func TestEventAdminDuplicateRejectsMissingCollisionAndStaleManifest(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	revision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, source, target, revision string
		want                           error
	}{
		{name: "missing source", source: "/missing", target: "/copy", revision: revision, want: admincontent.ErrEventNotFound},
		{name: "existing target", source: "/alpha", target: "/beta", revision: revision, want: admincontent.ErrEventExists},
		{name: "same target", source: "/alpha", target: "/alpha", revision: revision, want: admincontent.ErrInvalidEvent},
		{name: "unsafe target", source: "/alpha", target: "/../copy", revision: revision, want: admincontent.ErrInvalidEvent},
		{name: "stale manifest", source: "/alpha", target: "/copy", revision: "sha256:stale", want: admincontent.ErrManifestStale},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := admin.DuplicateEvent(test.source, test.target, test.revision); !errors.Is(err, test.want) {
				t.Fatalf("DuplicateEvent() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(contentDir, "copy.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("rejected duplication wrote a menu: %v", err)
	}
}

func TestEventAdminArchivesAndRestoresPublicRouteWithoutChangingContent(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	menuBefore, eventRevision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	manifestRevision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}

	archivedRevision, err := admin.SetEventArchived("/alpha", true, manifestRevision, eventRevision)
	if err != nil {
		t.Fatal(err)
	}
	events, err := admin.registry.Current()
	if err != nil {
		t.Fatal(err)
	}
	if events["/alpha"] != nil || events["/beta"] == nil {
		t.Fatalf("public events after archive = %#v", events)
	}
	adminEvents, err := admin.Events()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(adminEvents, admincontent.Event{Path: "/alpha", Archived: true}) {
		t.Fatalf("admin events after archive = %#v", adminEvents)
	}
	menuAfter, currentRevision, err := admin.ReadEvent("/alpha")
	if err != nil || !bytes.Equal(menuAfter, menuBefore) || currentRevision != eventRevision {
		t.Fatalf("archived menu changed: revision %q, error %v", currentRevision, err)
	}
	manifest, err := os.ReadFile(filepath.Join(contentDir, eventManifestFile))
	if err != nil || !strings.Contains(string(manifest), "archived: true") {
		t.Fatalf("archived manifest = %q, %v", manifest, err)
	}
	idempotentRevision, err := admin.SetEventArchived("/alpha", true, archivedRevision, "sha256:stale-is-irrelevant")
	if err != nil || idempotentRevision != archivedRevision {
		t.Fatalf("idempotent archive = %q, %v", idempotentRevision, err)
	}
	if _, err := admin.SetEventArchived("/alpha", false, manifestRevision, eventRevision); !errors.Is(err, admincontent.ErrManifestStale) {
		t.Fatalf("stale manifest unarchive error = %v", err)
	}

	publicRevision, err := admin.SetEventArchived("/alpha", false, archivedRevision, eventRevision)
	if err != nil {
		t.Fatal(err)
	}
	if publicRevision == archivedRevision {
		t.Fatal("unarchive did not change the manifest revision")
	}
	events, err = admin.registry.Current()
	if err != nil || events["/alpha"] == nil {
		t.Fatalf("unarchived event was not public: %#v, %v", events, err)
	}
}

func TestEventAdminUnarchiveRejectsStaleOrInvalidMenu(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	_, eventRevision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	manifestRevision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	archivedRevision, err := admin.SetEventArchived("/alpha", true, manifestRevision, eventRevision)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []byte("conference: [")
	if err := os.WriteFile(filepath.Join(contentDir, "alpha.yaml"), invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.SetEventArchived("/alpha", false, archivedRevision, eventRevision); !errors.Is(err, admincontent.ErrRevisionStale) {
		t.Fatalf("stale unarchive error = %v", err)
	}
	if _, err := admin.SetEventArchived("/alpha", false, archivedRevision, contentRevision(invalid)); !errors.Is(err, admincontent.ErrInvalidMenu) {
		t.Fatalf("invalid unarchive error = %v", err)
	}
	stored, revision, err := admin.ReadEvent("/alpha")
	if err != nil || !bytes.Equal(stored, invalid) || revision != contentRevision(invalid) {
		t.Fatalf("archived invalid menu was not editable: %q, %q, %v", stored, revision, err)
	}
	events, err := admin.Events()
	if err != nil || !slices.Contains(events, admincontent.Event{Path: "/alpha", Archived: true}) {
		t.Fatalf("failed unarchive changed state: %#v, %v", events, err)
	}
}

func TestEventAdminArchivedDuplicateStaysArchived(t *testing.T) {
	admin, _ := newTestEventAdmin(t)
	_, eventRevision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	manifestRevision, err := admin.ManifestRevision()
	if err != nil {
		t.Fatal(err)
	}
	manifestRevision, err = admin.SetEventArchived("/alpha", true, manifestRevision, eventRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.DuplicateEvent("/alpha", "/alpha-copy", manifestRevision); err != nil {
		t.Fatal(err)
	}
	events, err := admin.Events()
	if err != nil || !slices.Contains(events, admincontent.Event{Path: "/alpha-copy", Archived: true}) {
		t.Fatalf("duplicated archived event = %#v, %v", events, err)
	}
	public, err := admin.registry.Current()
	if err != nil || public["/alpha-copy"] != nil {
		t.Fatalf("archived duplicate became public: %#v, %v", public, err)
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
	history, err := admin.EventHistory("/alpha")
	if err != nil || len(history) != 0 {
		t.Fatalf("stale publish created history = %#v, %v", history, err)
	}
}

type failingPublishFS struct {
	*rootedFS
}

func (content *failingPublishFS) writeFileAtomic(name, expectedRevision string, data []byte) error {
	if name == "alpha.yaml" {
		return errors.New("forced publish failure")
	}
	return content.rootedFS.writeFileAtomic(name, expectedRevision, data)
}

func TestEventAdminFailedPublishRollsBackHistory(t *testing.T) {
	admin, contentDir := newTestEventAdmin(t)
	root := admin.registry.content.(*rootedFS)
	admin.registry.content = &failingPublishFS{rootedFS: root}
	before, revision, err := admin.ReadEvent("/alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.PublishEvent("/alpha", revision, []byte(testMenu("Rejected update"))); err == nil {
		t.Fatal("forced publish failure unexpectedly succeeded")
	}
	after, err := os.ReadFile(filepath.Join(contentDir, "alpha.yaml"))
	if err != nil || !bytes.Equal(after, before) {
		t.Fatal("failed publish changed the live menu")
	}
	history, err := admin.EventHistory("/alpha")
	if err != nil || len(history) != 0 {
		t.Fatalf("failed publish left history = %#v, %v", history, err)
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

func TestRootedContentWriteCheckCreatesIgnoredRuntimeManifest(t *testing.T) {
	contentDir := t.TempDir()
	rootHandle, err := os.OpenRoot(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rootHandle.Close() })
	root := &rootedFS{root: rootHandle}
	if err := root.verifyWritable(); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(contentDir, eventManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != "events: []\n" {
		t.Fatalf("runtime manifest = %q", manifest)
	}
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("write probe remained after initialization: %#v", entries)
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
