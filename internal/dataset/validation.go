package dataset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxOpaqueIdentifierBytes = 512
	maxResourceTypes         = 4096
	maxScopePaths            = 4096
)

var (
	// ErrInvalidDatasetRef reports a malformed project/generation pair.
	ErrInvalidDatasetRef = errors.New("invalid dataset reference")
	// ErrInvalidSchemaIdentity reports a malformed schema snapshot.
	ErrInvalidSchemaIdentity = errors.New("invalid schema identity")
	// ErrInvalidAnalysisVersion reports a malformed analysis-version value.
	ErrInvalidAnalysisVersion = errors.New("invalid analysis version")
	// ErrInvalidAuthScopeFingerprint reports a malformed scope fingerprint.
	ErrInvalidAuthScopeFingerprint = errors.New("invalid authorization scope fingerprint")
	// ErrInvalidManifest reports a manifest whose immutable values or state are
	// not valid for this lifecycle contract.
	ErrInvalidManifest = errors.New("invalid dataset manifest")
	// ErrInvalidTransition reports a lifecycle transition that is not allowed.
	ErrInvalidTransition = errors.New("invalid dataset manifest transition")
	// ErrGenerationNotReady reports an attempt to select a generation that is
	// not a finalized READY manifest.
	ErrGenerationNotReady = errors.New("dataset generation is not ready")
	// ErrInvalidActiveGeneration reports an invalid active-generation reference
	// or an active-generation lookup that cannot resolve exactly one READY
	// manifest.
	ErrInvalidActiveGeneration = errors.New("invalid active dataset generation")
)

func validateOpaqueIdentifier(field, value string, allowEmpty bool) error {
	if value == "" {
		if allowEmpty {
			return nil
		}
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

// decodeStrictJSON rejects unknown fields and trailing values. The concrete
// value type still performs all semantic validation after decoding.
func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func isJSONNull(data []byte) bool {
	return bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}
