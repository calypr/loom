package resolver

import (
	"encoding/json"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func graphqlRows(in []map[string]any) (json.RawMessage, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeOutputEncodingFailed, "")
	}
	return json.RawMessage(encoded), nil
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}
