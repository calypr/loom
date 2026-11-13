package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func (b Bundle) CanonicalJSONWithWorkspaceOutputs(allowed []string) ([]byte, error) {
	if err := b.ValidateWithWorkspaceOutputs(allowed); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

func (b Bundle) DigestWithWorkspaceOutputs(allowed []string) (string, error) {
	canonical, err := b.CanonicalJSONWithWorkspaceOutputs(allowed)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (b Bundle) CanonicalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

func (b Bundle) CanonicalBundle() (Bundle, error) {
	if b.Fragments != nil {
		expanded, err := b.ExpandFragments()
		if err != nil {
			return Bundle{}, err
		}
		b = expanded
	}
	canonical, err := b.CanonicalJSON()
	if err != nil {
		return Bundle{}, err
	}
	var normalized Bundle
	if err := json.Unmarshal(canonical, &normalized); err != nil {
		return Bundle{}, err
	}
	return normalized, nil
}

func (b Bundle) Digest() (string, error) {
	canonical, err := b.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

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
		for _, d := range out.ExtensionColumns {
			e.ExtensionColumns = append(e.ExtensionColumns, d.Name)
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
