// Package admin defines the content operations needed by Manna's admin UI.
package admin

import (
	"errors"
	"time"
)

var (
	ErrEventNotFound   = errors.New("admin event not found")
	ErrRevisionStale   = errors.New("admin event revision is stale")
	ErrContentReadOnly = errors.New("admin content is read-only")
	ErrInvalidMenu     = errors.New("invalid admin menu")
	ErrInvalidLogo     = errors.New("invalid admin logo")
	ErrEventExists     = errors.New("admin event already exists")
	ErrManifestStale   = errors.New("admin event manifest revision is stale")
	ErrInvalidEvent    = errors.New("invalid admin event")
	ErrHistoryNotFound = errors.New("admin event history entry not found")
	ErrInvalidHistory  = errors.New("invalid admin event history entry")
)

// Content is the narrow boundary between the web admin and menu storage.
type Content interface {
	Events() ([]string, error)
	ReadEvent(eventPath string) (content []byte, revision string, err error)
	ValidateMenu(content []byte) error
	PublishEvent(eventPath, expectedRevision string, content []byte) (newRevision string, err error)
}

// LogoContent is implemented by writable content stores that support event
// branding uploads. Logo paths returned here are relative to the content root.
type LogoContent interface {
	UploadLogo(eventPath, extension string, content []byte) (logoPath string, err error)
}

// EventContent is the elevated boundary for changing the event manifest.
// Implementations must derive content filenames from validated event paths and
// use optimistic concurrency for every manifest mutation.
type EventContent interface {
	ManifestRevision() (revision string, err error)
	CreateEvent(eventPath, expectedManifestRevision string) (newManifestRevision string, err error)
	DuplicateEvent(sourcePath, targetPath, expectedManifestRevision string) (newManifestRevision string, err error)
	DeleteEvent(eventPath, confirmation, expectedManifestRevision string) (newManifestRevision string, err error)
}

// Revision describes an exact previous published menu stored by HistoryContent.
type Revision struct {
	ID         string    `json:"id"`
	Revision   string    `json:"revision"`
	ArchivedAt time.Time `json:"archived_at"`
}

// HistoryContent is implemented by writable content stores that retain and
// restore previous published event menus.
type HistoryContent interface {
	EventHistory(eventPath string) ([]Revision, error)
	RestoreEvent(eventPath, historyID, expectedCurrentRevision string) (content []byte, newRevision string, err error)
}
