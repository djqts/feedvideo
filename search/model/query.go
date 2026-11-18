package model

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrInvalidQuery = errors.New("invalid search query")

// QueryValidationError identifies which input violates the search contract.
type QueryValidationError struct {
	Parameter string
	Reason    string
}

func (e *QueryValidationError) Error() string { return e.Parameter + ": " + e.Reason }
func (e *QueryValidationError) Unwrap() error { return ErrInvalidQuery }

// ValidateSearchRequest checks direct service and HTTP inputs without clamping.
// Empty queries are valid; callers return their requested empty categories.
func ValidateSearchRequest(q, typ string, page, size int64) error {
	switch SearchType(typ) {
	case SearchTypeAll, SearchTypeVideo, SearchTypeUser, SearchTypeTag:
	default:
		return &QueryValidationError{Parameter: "type", Reason: "must be all, video, user, or tag"}
	}
	if page < 1 || page > 100 {
		return &QueryValidationError{Parameter: "page", Reason: "must be between 1 and 100"}
	}
	if size < 1 || size > 50 {
		return &QueryValidationError{Parameter: "size", Reason: "must be between 1 and 50"}
	}
	if utf8.RuneCountInString(strings.TrimSpace(q)) > 50 {
		return &QueryValidationError{Parameter: "q", Reason: "must contain at most 50 Unicode characters"}
	}
	return nil
}
