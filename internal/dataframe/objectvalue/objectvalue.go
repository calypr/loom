// Package objectvalue encodes logical dataframe object values as deterministic
// JSON documents at the storage boundary.
package objectvalue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
)

// Shape identifies the permitted root shape of an object-valued column.
type Shape uint8

const (
	ScalarObject Shape = iota + 1
	RepeatedObjects
)

// Encode validates and normalizes one logical object value. The returned JSON
// is deterministic for equivalent object maps because encoding/json sorts map
// keys. A nil value is encoded as the JSON document "null".
func Encode(shape Shape, value any) (string, error) {
	if err := validateShape(shape); err != nil {
		return "", err
	}
	if err := validateJSONValue(reflect.ValueOf(value), make(map[visit]struct{})); err != nil {
		return "", fmt.Errorf("encode object value: %w", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode object value: %w", err)
	}
	decoded, err := Decode(shape, string(encoded))
	if err != nil {
		return "", fmt.Errorf("encode object value: %w", err)
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("normalize object value: %w", err)
	}
	return string(normalized), nil
}

// Decode validates and decodes one normalized logical object document. JSON
// numbers remain json.Number so consumers do not silently lose precision.
func Decode(shape Shape, text string) (any, error) {
	if err := validateShape(shape); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode object value: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode object value: trailing JSON")
		}
		return nil, fmt.Errorf("decode object value: trailing JSON: %w", err)
	}
	if err := validateDecodedShape(shape, value); err != nil {
		return nil, fmt.Errorf("decode object value: %w", err)
	}
	return value, nil
}

func validateShape(shape Shape) error {
	switch shape {
	case ScalarObject, RepeatedObjects:
		return nil
	default:
		return fmt.Errorf("unsupported object value shape %d", shape)
	}
}

func validateDecodedShape(shape Shape, value any) error {
	if value == nil {
		return nil
	}
	switch shape {
	case ScalarObject:
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("scalar object root must be an object or null, got %T", value)
		}
	case RepeatedObjects:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("repeated object root must be an array or null, got %T", value)
		}
		for index, item := range items {
			if item == nil {
				continue
			}
			if _, ok := item.(map[string]any); !ok {
				return fmt.Errorf("repeated object item %d must be an object or null, got %T", index, item)
			}
		}
	}
	return nil
}

type visit struct {
	typ  reflect.Type
	addr uintptr
}

func validateJSONValue(value reflect.Value, active map[visit]struct{}) error {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return nil
		}
		if value.Kind() == reflect.Pointer {
			key := visit{typ: value.Type(), addr: value.Pointer()}
			if _, seen := active[key]; seen {
				return fmt.Errorf("cyclic value")
			}
			active[key] = struct{}{}
			defer delete(active, key)
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Bool, reflect.String:
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return nil
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(value.Float()) || math.IsInf(value.Float(), 0) {
			return fmt.Errorf("non-finite number")
		}
		return nil
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return nil
		}
		key := visit{typ: value.Type()}
		if value.Kind() == reflect.Slice {
			key.addr = value.Pointer()
			if key.addr != 0 {
				if _, seen := active[key]; seen {
					return fmt.Errorf("cyclic value")
				}
				active[key] = struct{}{}
				defer delete(active, key)
			}
		}
		for index := 0; index < value.Len(); index++ {
			if err := validateJSONValue(value.Index(index), active); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("object map keys must be strings, got %s", value.Type().Key())
		}
		key := visit{typ: value.Type(), addr: value.Pointer()}
		if key.addr != 0 {
			if _, seen := active[key]; seen {
				return fmt.Errorf("cyclic value")
			}
			active[key] = struct{}{}
			defer delete(active, key)
		}
		iter := value.MapRange()
		for iter.Next() {
			if err := validateJSONValue(iter.Value(), active); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath != "" || strings.Split(field.Tag.Get("json"), ",")[0] == "-" {
				continue
			}
			if err := validateJSONValue(value.Field(index), active); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return fmt.Errorf("unsupported value: %w", err)
		}
		return validateJSONValue(reflect.ValueOf(json.RawMessage(encoded)), active)
	default:
		return fmt.Errorf("unsupported value type %s", value.Type())
	}
}
