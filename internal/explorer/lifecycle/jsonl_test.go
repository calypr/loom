package lifecycle

import (
	"bytes"
	"encoding/json"
	"os"
)

func writeLifecycleJSONL[Header any, Row any](path string, header Header, rows []Row) error {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	if err := encoder.Encode(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content.Bytes(), 0o600)
}
