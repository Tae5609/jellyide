package metadata

import (
	"fmt"
)

// API error types. The api package maps them to status codes:
// ValidationError -> 400, NotFoundError -> 404, ConflictError -> 409.
type (
	// ValidationError is a request problem (bad field name, type or range).
	ValidationError struct {
		Message string
		Field   string
		TrackID string
	}

	// NotFoundError is a missing album, track or change set.
	NotFoundError struct {
		Message string
		TrackID string
	}

	// ConflictError is a revert of a change set whose track was deleted.
	ConflictError struct {
		Message string
		TrackID string
	}
)

func (e *ValidationError) Error() string { return e.Message }
func (e *NotFoundError) Error() string   { return e.Message }
func (e *ConflictError) Error() string   { return e.Message }

// textFields and intFields are the fields a bulk edit may touch.
var (
	textFields = map[string]bool{
		"title":        true,
		"album":        true,
		"artist":       true,
		"album_artist": true,
		"genre":        true,
		"composer":     true,
		"comment":      true,
	}
	intFields = map[string]bool{
		"track_number": true,
		"disc_number":  true,
		"year":         true,
	}
)

func isKnownField(name string) bool {
	return textFields[name] || intFields[name]
}

func fieldRangeError(name string, value int) string {
	switch name {
	case "year":
		if value > 9999 {
			return fmt.Sprintf("year must be between 0 and 9999, got %d", value)
		}
	case "track_number", "disc_number":
		if value > 999 {
			return fmt.Sprintf("%s must be between 0 and 999, got %d", name, value)
		}
	}
	return ""
}
