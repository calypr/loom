package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ArchivedMetadata describes stored output identity without exposing an executable recipe.
type ArchivedMetadata struct {
	RecipeSchemaVersion int
	Name                string
	TranslationVersion  string
	Outputs             []ArchivedOutputDescriptor
}

type ArchivedOutputDescriptor struct {
	Name             string
	RootResourceType string
	RootOccurrenceID string
	RowGrain         string
}

func (b Bundle) HasArchivedReshape() bool {
	for _, output := range b.Outputs {
		if output.ArchiveRetiredReshape != nil {
			return true
		}
	}
	return false
}

// CurrentBundle validates and detaches executable input. Archive verification
// must never be used as a substitute for this boundary.
func (b Bundle) CurrentBundle() (Bundle, error) {
	if err := b.Validate(); err != nil {
		return Bundle{}, err
	}
	return b.CanonicalBundle()
}

// ValidateArchive retains historical wire validation for immutable reads only.
// It does not make an archive eligible for compilation or registration.
func (b Bundle) ValidateArchive() error { return b.validate(nil, archiveRecipeValidation) }

// ArchiveCanonicalJSON preserves historical typed encoding without expanding
// fragments, resolving schema, normalizing data, or advancing recipe semantics.
func (b Bundle) ArchiveCanonicalJSON() ([]byte, error) {
	if err := b.ValidateArchive(); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

func (b Bundle) ArchiveDigest() (string, error) {
	raw, err := b.ArchiveCanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ParseArchivedMetadata strictly validates stored recipe JSON and returns only
// output identity. It has no conversion to a current executable Bundle.
func ParseArchivedMetadata(raw []byte) (ArchivedMetadata, error) {
	b, err := decodeBundleJSON(raw)
	if err != nil {
		return ArchivedMetadata{}, err
	}
	if err := b.ValidateArchive(); err != nil {
		return ArchivedMetadata{}, err
	}
	metadata := ArchivedMetadata{RecipeSchemaVersion: b.RecipeSchemaVersion, Name: b.Name, TranslationVersion: b.TranslationVersion, Outputs: make([]ArchivedOutputDescriptor, len(b.Outputs))}
	for i, o := range b.Outputs {
		metadata.Outputs[i] = ArchivedOutputDescriptor{Name: o.Name, RootResourceType: o.RootResourceType, RootOccurrenceID: o.RootOccurrenceID, RowGrain: o.RowGrain}
	}
	return metadata, nil
}
