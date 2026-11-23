package explorer

import (
	"bytes"
	"encoding/json"
	"fmt"

	explorerconfig "github.com/calypr/loom/internal/explorer/config"
)

func canonicalJSONBytes(raw []byte) ([]byte, error) {
	return explorerconfig.CanonicalJSONBytes(raw)
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(string) error
	walk = func(path string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key := keyToken.(string)
				if seen[key] {
					return fmt.Errorf("duplicate JSON object key %q at %s", key, path)
				}
				seen[key] = true
				if err := walk(path + "." + key); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for index := 0; decoder.More(); index++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	return walk("$")
}
