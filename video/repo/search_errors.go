package repo

import "errors"

// ErrSearchUnavailable distinguishes an unavailable backend from no matches.
var ErrSearchUnavailable = errors.New("search is temporarily unavailable")
