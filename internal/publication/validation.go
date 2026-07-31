package publication

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxOpaqueIdentifierBytes = 512
	maxResourceTypes         = 4096
)

var (
	ErrInvalidDatasetRef       = errors.New("invalid dataset reference")
	ErrInvalidSchemaIdentity   = errors.New("invalid schema identity")
	ErrInvalidManifest         = errors.New("invalid dataset manifest")
	ErrInvalidTransition       = errors.New("invalid dataset manifest transition")
	ErrGenerationNotReady      = errors.New("dataset generation is not ready")
	ErrInvalidActiveGeneration = errors.New("invalid active dataset generation")
	ErrNoActiveGeneration      = errors.New("no active dataset generation")
)

func validateOpaqueIdentifier(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if len(value) > maxOpaqueIdentifierBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxOpaqueIdentifierBytes)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}
