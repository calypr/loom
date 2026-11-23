package arango

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"strings"
)

func migrationDocument(value any, key string) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	out["_key"] = key
	return out, nil
}

func migrationJSONQuery(query string, enabled bool) string {
	if !enabled {
		return query
	}
	for _, name := range []string{"d", "NEW"} {
		if strings.HasSuffix(query, "RETURN "+name) {
			return strings.TrimSuffix(query, "RETURN "+name) + "RETURN {migrationJSON:JSON_STRINGIFY(" + name + ")}"
		}
	}
	panic("migration JSON query requires a terminal document return")
}
func decodeMigrationJSON[T any](value any) (T, error) {
	var out T
	raw, ok := value.(string)
	if !ok {
		return out, fmt.Errorf("migration JSON envelope is not a string")
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) == nil && fields != nil {
		var numeric float64
		if timestampRaw, present := fields["updatedAt"]; present && json.Unmarshal(timestampRaw, &numeric) == nil && string(timestampRaw) != "null" {
			if timestamp, ok := db.NumericTimestamp(numeric); ok {
				fields["updatedAt"], _ = json.Marshal(timestamp)
				normalized, err := json.Marshal(fields)
				if err != nil {
					return out, err
				}
				raw = string(normalized)
			}
		}
	}
	err := json.Unmarshal([]byte(raw), &out)
	return out, err
}
func decodeDraftRow[T any](row map[string]any, migration bool) (T, error) {
	if migration {
		return decodeMigrationJSON[T](row["migrationJSON"])
	}
	return db.Decode[T](row)
}
