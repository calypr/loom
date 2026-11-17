package execution

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func computeNamedUUID(operation string, args []any) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s requires a namespace and at least one name", operation)
	}
	for _, value := range args {
		if value == nil {
			return nil, nil
		}
	}
	namespace, err := namedUUIDNamespace(args[0])
	if err != nil {
		return nil, err
	}
	var name strings.Builder
	for _, value := range args[1:] {
		name.WriteString(fmt.Sprint(value))
	}
	switch strings.ToLower(operation) {
	case "uuid3":
		return uuid.NewMD5(namespace, []byte(name.String())).String(), nil
	case "uuid5":
		return uuid.NewSHA1(namespace, []byte(name.String())).String(), nil
	default:
		return nil, fmt.Errorf("unsupported named UUID operation %q", operation)
	}
}

func namedUUIDNamespace(value any) (uuid.UUID, error) {
	raw := fmt.Sprint(value)
	if parsed, err := uuid.Parse(raw); err == nil {
		return parsed, nil
	}
	return uuid.NewMD5(uuid.NameSpaceDNS, []byte(raw)), nil
}
