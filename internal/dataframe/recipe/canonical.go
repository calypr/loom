// Package recipe defines the persistence-neutral recipe document used by the
// dataframe compiler. A recipe describes semantic row shaping only; it never
// carries database collection, table, AQL, or SQL details.
package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func (b Bundle) CanonicalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

// Digest returns the SHA-256 digest of CanonicalJSON encoded as lowercase hex.
func (b Bundle) Digest() (string, error) {
	canonical, err := b.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Explain returns the validated semantic shape and canonical digest.
func (b Bundle) Explain() (Explanation, error) {
	digest, err := b.Digest()
	if err != nil {
		return Explanation{}, err
	}
	exp := Explanation{RecipeSchemaVersion: b.RecipeSchemaVersion, Name: b.Name, TranslationVersion: b.TranslationVersion, Digest: digest, Outputs: make([]OutputExplanation, len(b.Outputs))}
	for i, out := range b.Outputs {
		e := OutputExplanation{Name: out.Name, RootResourceType: out.RootResourceType, RowGrain: out.RowGrain, Expanded: out.Expand != nil}
		for _, f := range out.Fields {
			e.FieldNames = append(e.FieldNames, f.Name)
		}
		for _, t := range out.Traversals {
			e.TraversalNames = append(e.TraversalNames, t.Name)
		}
		for _, d := range out.DynamicColumns {
			e.DynamicColumns = append(e.DynamicColumns, d.Name)
		}
		for _, projection := range out.CatalogProjections {
			e.CatalogProjections = append(e.CatalogProjections, projection.Name)
		}
		exp.Outputs[i] = e
	}
	return exp, nil
}

type ValidationError struct {
	Code    string
	Path    string
	Message string
}

func (e *ValidationError) Error() string { return e.Code + " at " + e.Path + ": " + e.Message }
func validationError(code, path, message string) error {
	return &ValidationError{Code: code, Path: path, Message: message}
}
