package recipe

import (
	_ "embed"
)

// DefaultACEDJSON is the checked-in recipe data used as the initial translation
// bundle. It is data, not a Go implementation of any output behavior.
//
//go:embed default_aced.json
var DefaultACEDJSON []byte

func DefaultACEDBundle() (Bundle, error) { return Parse(DefaultACEDJSON) }
