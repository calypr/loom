package compilerprobe

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func taggedOperation(raw []byte) (recipe.ConstructionOperation, error) {
	var operation recipe.ConstructionOperation
	if err := json.Unmarshal(raw, &operation); err != nil {
		return operation, err
	}
	fields := reflect.ValueOf(operation)
	kind := operationKeys[string(operation.Kind)]
	matched, count := false, 0
	for i := 1; i < fields.NumField(); i++ {
		if !fields.Field(i).IsNil() {
			count++
			tag := fields.Type().Field(i).Tag.Get("json")
			matched = matched || tag == kind+",omitempty"
		}
	}
	if count != 1 || !matched {
		return operation, fmt.Errorf("incorrect tagged payload")
	}
	return operation, nil
}

func dispatchedOperation(raw []byte) (recipe.ConstructionOperation, error) {
	var operation recipe.ConstructionOperation
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return operation, err
	}
	if err := json.Unmarshal(fields["kind"], &operation.Kind); err != nil {
		return operation, err
	}
	key := operationKeys[string(operation.Kind)]
	if len(fields) != 2 || len(fields[key]) == 0 || string(fields[key]) == "null" {
		return operation, fmt.Errorf("incorrect dispatched payload")
	}
	switch key {
	case "pivot":
		return operation, json.Unmarshal(fields[key], &operation.Pivot)
	case "derive":
		return operation, json.Unmarshal(fields[key], &operation.Derive)
	case "filter":
		return operation, json.Unmarshal(fields[key], &operation.Filter)
	case "unpivot":
		return operation, json.Unmarshal(fields[key], &operation.Unpivot)
	case "group":
		return operation, json.Unmarshal(fields[key], &operation.Group)
	case "codedGroup":
		return operation, json.Unmarshal(fields[key], &operation.CodedGroup)
	case "codedPivot":
		return operation, json.Unmarshal(fields[key], &operation.CodedPivot)
	case "expand":
		return operation, json.Unmarshal(fields[key], &operation.Expand)
	case "combine":
		return operation, json.Unmarshal(fields[key], &operation.Combine)
	case "relatedSource":
		return operation, json.Unmarshal(fields[key], &operation.RelatedSource)
	case "relatedExpand":
		return operation, json.Unmarshal(fields[key], &operation.RelatedExpand)
	case "relatedEligibility":
		return operation, json.Unmarshal(fields[key], &operation.RelatedEligibility)
	case "relatedField":
		return operation, json.Unmarshal(fields[key], &operation.RelatedField)
	default:
		return operation, fmt.Errorf("unsupported operation %q", operation.Kind)
	}
}

func BenchmarkOperationCodecs(b *testing.B) {
	var corpus [][]byte
	for _, raw := range researchCandidates(b) {
		bundle, err := decodeCandidate(raw)
		if err != nil {
			continue
		}
		for _, output := range bundle.Outputs {
			if output.Construction == nil {
				continue
			}
			for _, step := range output.Construction.Steps {
				raw, err := json.Marshal(step.Operation)
				if err != nil {
					b.Fatal(err)
				}
				corpus = append(corpus, raw)
			}
		}
	}
	if len(corpus) == 0 {
		b.Skip("capture corpus contains no construction operations")
	}
	for _, raw := range corpus {
		tagged, err := taggedOperation(raw)
		if err != nil {
			b.Fatal(err)
		}
		dispatched, err := dispatchedOperation(raw)
		if err != nil || !reflect.DeepEqual(tagged, dispatched) {
			b.Fatalf("operation decode disagreed: %v", err)
		}
	}
	b.Logf("decode comparison uses %d construction operations", len(corpus))
	for _, codec := range []struct {
		name   string
		decode func([]byte) (recipe.ConstructionOperation, error)
	}{
		{"tagged", taggedOperation},
		{"dispatch", dispatchedOperation},
	} {
		b.Run(codec.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := codec.decode(corpus[i%len(corpus)]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
