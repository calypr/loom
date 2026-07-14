package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// detectDuplicateKeys walks the JSON token stream before typed decoding. The
// standard library's struct decoder accepts duplicate object keys, which
// would make recipe digests dependent on decoder behavior.
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
	if err := dec.Decode(&trailing); err == nil {
		return validationError("parse_error", "$", "multiple JSON values")
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
