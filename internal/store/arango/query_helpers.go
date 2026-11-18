package arango

import (
	"context"
	"encoding/json"
)

func QueryHasRow(ctx context.Context, queryer RowQueryer, query string, batchSize int, bindVars map[string]interface{}) (bool, error) {
	found := false
	err := queryer.QueryRows(ctx, query, batchSize, bindVars, func(map[string]any) error {
		found = true
		return nil
	})
	return found, err
}

func QueryLast[T any](ctx context.Context, queryer RowQueryer, query string, batchSize int, bindVars map[string]interface{}) (T, bool, error) {
	var last T
	found := false
	err := queryer.QueryRows(ctx, query, batchSize, bindVars, func(row map[string]any) error {
		value, err := DecodeValue[T](row)
		if err != nil {
			return err
		}
		last = value
		found = true
		return nil
	})
	return last, found, err
}

func DecodeValue[T any](value any) (T, error) {
	var decoded T
	encoded, err := json.Marshal(value)
	if err != nil {
		return decoded, err
	}
	err = json.Unmarshal(encoded, &decoded)
	return decoded, err
}
