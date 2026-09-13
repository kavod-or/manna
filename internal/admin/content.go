// Package admin defines the content operations needed by Mana's admin UI.
package admin

import "errors"

var (
	ErrEventNotFound   = errors.New("admin event not found")
	ErrRevisionStale   = errors.New("admin event revision is stale")
	ErrContentReadOnly = errors.New("admin content is read-only")
	ErrInvalidMenu     = errors.New("invalid admin menu")
)

// Content is the narrow boundary between the web admin and menu storage.
type Content interface {
	Events() ([]string, error)
	ReadEvent(eventPath string) (content []byte, revision string, err error)
	ValidateMenu(content []byte) error
	PublishEvent(eventPath, expectedRevision string, content []byte) (newRevision string, err error)
}
