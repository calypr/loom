package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Explanation struct {
	RecipeSchemaVersion int                 `json:"recipeSchemaVersion"`
	Name                string              `json:"name"`
	TranslationVersion  string              `json:"translationVersion"`
	Digest              string              `json:"digest"`
	Outputs             []OutputExplanation `json:"outputs"`
}

type OutputExplanation struct {
	Name               string   `json:"name"`
	RootResourceType   string   `json:"rootResourceType"`
	RowGrain           string   `json:"rowGrain"`
	FieldNames         []string `json:"fieldNames,omitempty"`
	TraversalNames     []string `json:"traversalNames,omitempty"`
	Expanded           bool     `json:"expanded,omitempty"`
	DynamicColumns     []string `json:"dynamicColumns,omitempty"`
	ExtensionColumns   []string `json:"extensionColumns,omitempty"`
	CatalogProjections []string `json:"catalogProjections,omitempty"`
}

type DocumentRef struct {
	Context string `json:"context,omitempty"`
}

type Expression struct {
	Select   string
	Literal  json.RawMessage
	Call     string
	Args     []Expression
	Document *DocumentRef
}

func (e Expression) MarshalJSON() ([]byte, error) {
	switch {
	case e.Select != "" && e.Call == "" && e.Literal == nil && e.Document == nil:
		return json.Marshal(struct {
			Select string `json:"select"`
		}{e.Select})
	case e.Call != "" && e.Select == "" && e.Literal == nil && e.Document == nil:
		if len(e.Args) == 0 {
			return json.Marshal(struct {
				Call string `json:"call"`
			}{e.Call})
		}
		return json.Marshal(struct {
			Call string       `json:"call"`
			Args []Expression `json:"args"`
		}{e.Call, e.Args})
	case e.Literal != nil && e.Select == "" && e.Call == "" && e.Document == nil:
		if !json.Valid(e.Literal) {
			return nil, fmt.Errorf("literal is not valid JSON")
		}
		return append([]byte(`{"literal":`), append(e.Literal, '}')...), nil
	case e.Document != nil && e.Select == "" && e.Call == "" && e.Literal == nil:
		document := *e.Document
		if strings.TrimSpace(document.Context) == "" {
			document.Context = "root"
		}
		return json.Marshal(struct {
			Document *DocumentRef `json:"document"`
		}{&document})
	default:
		return nil, fmt.Errorf("expression must contain exactly one of select, literal, or call")
	}
}

func (e *Expression) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&object); err != nil {
		return fmt.Errorf("expression must be an object: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("expression contains trailing JSON")
		}
		return fmt.Errorf("expression contains invalid trailing JSON: %w", err)
	}
	if len(object) == 0 {
		return fmt.Errorf("expression must contain an operator")
	}
	for key := range object {
		if key != "select" && key != "literal" && key != "call" && key != "args" && key != "document" {
			return fmt.Errorf("unknown expression field %q", key)
		}
	}
	_, hasSelect := object["select"]
	_, hasLiteral := object["literal"]
	_, hasCall := object["call"]
	_, hasArgs := object["args"]
	_, hasDocument := object["document"]
	operatorCount := 0
	if hasSelect {
		operatorCount++
	}
	if hasLiteral {
		operatorCount++
	}
	if hasCall {
		operatorCount++
	}
	if hasDocument {
		operatorCount++
	}
	if operatorCount != 1 {
		return fmt.Errorf("expression must contain exactly one operator")
	}
	if hasArgs && !hasCall {
		return fmt.Errorf("args is only valid with call")
	}
	if hasDocument {
		if hasArgs {
			return fmt.Errorf("args is not valid with document")
		}
		*e = Expression{}
		var document DocumentRef
		dec := json.NewDecoder(bytes.NewReader(object["document"]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&document); err != nil {
			return fmt.Errorf("document must be an object: %w", err)
		}
		if strings.TrimSpace(document.Context) == "" {
			document.Context = "root"
		}
		e.Document = &document
		return nil
	}
	*e = Expression{}
	if hasSelect {
		if err := json.Unmarshal(object["select"], &e.Select); err != nil || strings.TrimSpace(e.Select) == "" {
			return fmt.Errorf("select must be a non-empty string")
		}
		return nil
	}
	if hasLiteral {
		if !json.Valid(object["literal"]) {
			return fmt.Errorf("literal is not valid JSON")
		}
		e.Literal = append(json.RawMessage(nil), object["literal"]...)
		return nil
	}
	if err := json.Unmarshal(object["call"], &e.Call); err != nil || strings.TrimSpace(e.Call) == "" {
		return fmt.Errorf("call must be a non-empty string")
	}
	if hasArgs {
		if err := json.Unmarshal(object["args"], &e.Args); err != nil {
			return fmt.Errorf("args must be an array: %w", err)
		}
	}
	return nil
}

func Parse(data []byte) (Bundle, error) {
	b, err := decodeBundleJSON(data)
	if err != nil {
		return Bundle{}, err
	}
	if err := b.Validate(); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

func decodeBundleJSON(data []byte) (Bundle, error) {
	if err := detectDuplicateKeys(data); err != nil {
		return Bundle{}, err
	}
	var b Bundle
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, validationError("parse_error", "$", err.Error())
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Bundle{}, validationError("parse_error", "$", "multiple JSON values")
		}
		return Bundle{}, validationError("parse_error", "$", "invalid trailing JSON: "+err.Error())
	}
	return b, nil
}

func detectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var walk func(string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return validationError("parse_error", path, err.Error())
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for dec.More() {
					keyToken, err := dec.Token()
					if err != nil {
						return validationError("parse_error", path, err.Error())
					}
					key, ok := keyToken.(string)
					if !ok {
						return validationError("parse_error", path, "object key is not a string")
					}
					keyPath := path + "." + key
					if seen[key] {
						return validationError("duplicate_field", keyPath, "duplicate JSON object key")
					}
					seen[key] = true
					if forbiddenStorageKey(key) {
						return validationError("forbidden_storage_binding", keyPath, "storage implementation fields are not allowed in recipes")
					}
					if err := walk(keyPath); err != nil {
						return err
					}
				}
				end, err := dec.Token()
				if err != nil {
					return validationError("parse_error", path, err.Error())
				}
				if end != json.Delim('}') {
					return validationError("parse_error", path, "invalid object")
				}
			case '[':
				index := 0
				for dec.More() {
					if err := walk(fmt.Sprintf("%s[%d]", path, index)); err != nil {
						return err
					}
					index++
				}
				end, err := dec.Token()
				if err != nil {
					return validationError("parse_error", path, err.Error())
				}
				if end != json.Delim(']') {
					return validationError("parse_error", path, "invalid array")
				}
			}
		}
		return nil
	}
	if err := walk("$"); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return validationError("parse_error", "$", "multiple JSON values")
		}
		return validationError("parse_error", "$", "invalid trailing JSON: "+err.Error())
	}
	return nil
}

func forbiddenStorageKey(key string) bool {
	switch strings.ToLower(key) {
	case "aql", "sql", "collection", "table", "database", "arangocollection", "clickhousetable":
		return true
	default:
		return false
	}
}
