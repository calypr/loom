//go:build ignore

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/fhir/schema"
	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

type featureAudit struct {
	Resource    string                                 `json:"resource"`
	Observation catalog.SemanticObservation            `json:"observation"`
	Matches     int64                                  `json:"matches"`
	Records     int64                                  `json:"records"`
	Readiness   authoringv2.SemanticSelectionReadiness `json:"readiness"`
}

type fieldAudit struct {
	Resource   string           `json:"resource"`
	Path       string           `json:"path"`
	Treatments map[string]int64 `json:"treatments"`
}

type fileAudit struct {
	File       string `json:"file"`
	Records    int64  `json:"records"`
	Complete   bool   `json:"complete"`
	ReadSHA256 string `json:"readSHA256"`
	Bytes      int64  `json:"bytes"`
	Unchanged  bool   `json:"unchanged"`
}

func main() {
	input := flag.String("input", "", "FHIR NDJSON directory")
	limit := flag.Int64("limit-per-file", 0, "sample bound; zero scans every record")
	flag.Parse()
	if err := audit(*input, *limit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func audit(input string, limit int64) error {
	if input == "" || limit < 0 {
		return fmt.Errorf("input directory and nonnegative limit are required")
	}
	paths, err := filepath.Glob(filepath.Join(input, "*.ndjson"))
	if err != nil || len(paths) == 0 {
		return fmt.Errorf("no NDJSON files in %s", input)
	}
	index, err := schema.GeneratedIndex()
	if err != nil {
		return err
	}
	features := map[string]*featureAudit{}
	fields := map[string]*fieldAudit{}
	classifications := map[string]fhirsemantic.DirectFieldClassification{}
	files := []fileAudit{}
	emitter := catalog.NewSemanticInventoryEmitter("semantic-audit", "audit")
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		before, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		hash := sha256.New()
		scanner := bufio.NewScanner(io.TeeReader(file, hash))
		scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
		entry := fileAudit{File: filepath.Base(path), Bytes: before.Size(), Complete: true}
		for scanner.Scan() {
			if limit > 0 && entry.Records == limit {
				entry.Complete = false
				break
			}
			var payload map[string]any
			decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			decoder.UseNumber()
			if err := decoder.Decode(&payload); err != nil {
				file.Close()
				return fmt.Errorf("%s record %d: %w", path, entry.Records+1, err)
			}
			resource, _ := payload["resourceType"].(string)
			entry.Records++
			if entry.Records%10000 == 0 {
				fmt.Fprintf(os.Stderr, "%s: %d records scanned\n", entry.File, entry.Records)
			}
			observations := []catalog.SemanticObservation{}
			seen := map[string]bool{}
			err := emitter.ObservePayload(payload, resource, "", fmt.Sprintf("%s#%d", entry.File, entry.Records), func(contribution catalog.SemanticInventoryContribution) {
				observation := contribution.Observation
				observations = append(observations, observation)
				key := contribution.BindingID + "/" + contribution.ConceptID
				feature := features[key]
				if feature == nil {
					feature = &featureAudit{Resource: resource, Observation: observation, Readiness: authoringv2.ResolveSemanticSelectionPlan(observation).Readiness}
					features[key] = feature
				}
				feature.Matches++
				if !seen[key] {
					feature.Records++
					seen[key] = true
				}
			})
			if err != nil {
				file.Close()
				return fmt.Errorf("%s record %d: %w", path, entry.Records, err)
			}
			leaves(payload, "", func(path string) {
				key := resource + "." + path
				classification, found := classifications[key]
				if !found {
					var classificationErr error
					classification, classificationErr = fhirsemantic.ClassifyDirectField(index, schema.DefinitionName(resource), path)
					if classificationErr != nil {
						classification.Reason = "SCHEMA_UNKNOWN"
					}
					classifications[key] = classification
				}
				treatment := string(classification.Reason)
				switch {
				case classification.Eligible:
					treatment = "DIRECT_FIELD"
				case classification.Disposition == fhirsemantic.DispositionNavigationOnly:
					treatment = "NAVIGATION"
				case treatment != "SCHEMA_UNKNOWN":
					treatment = coverageFor(path, observations)
				}
				field := fields[key]
				if field == nil {
					field = &fieldAudit{Resource: resource, Path: path, Treatments: map[string]int64{}}
					fields[key] = field
				}
				field.Treatments[treatment]++
			})
		}
		scanErr := scanner.Err()
		after, statErr := file.Stat()
		closeErr := file.Close()
		if scanErr != nil {
			return scanErr
		}
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		entry.Unchanged = os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
		if !entry.Unchanged {
			return fmt.Errorf("source changed during audit: %s", path)
		}
		entry.ReadSHA256 = hex.EncodeToString(hash.Sum(nil))
		files = append(files, entry)
		fmt.Fprintf(os.Stderr, "%s: %d records, complete=%v\n", entry.File, entry.Records, entry.Complete)
	}
	featureRows := make([]*featureAudit, 0, len(features))
	for _, key := range sortedKeys(features) {
		featureRows = append(featureRows, features[key])
	}
	fieldRows := make([]*fieldAudit, 0, len(fields))
	for _, key := range sortedKeys(fields) {
		fieldRows = append(fieldRows, fields[key])
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Files    []fileAudit     `json:"files"`
		Features []*featureAudit `json:"features"`
		Fields   []*fieldAudit   `json:"fields"`
	}{files, featureRows, fieldRows})
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func leaves(value any, path string, visit func(string)) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			next := key
			if path != "" {
				next = path + "." + key
			}
			leaves(child, next, visit)
		}
	case []any:
		for _, child := range value {
			leaves(child, path+"[]", visit)
		}
	default:
		visit(path)
	}
}

func coverageFor(path string, observations []catalog.SemanticObservation) string {
	metadata := false
	for _, observation := range observations {
		value := observation.Value.Selector
		if observation.OwningScope != "" && value != observation.OwningScope && !strings.HasPrefix(value, observation.OwningScope+".") {
			value = observation.OwningScope + "." + value
		}
		if path == value || strings.HasPrefix(path, value+".") {
			if observation.Completeness == catalog.SemanticComplete && observation.Status == "SUPPORTED" {
				return "SEMANTIC_VALUE"
			}
			return "NEEDS_REVIEW"
		}
		if observation.Value.Presentation == schema.ValuePresentationDisplayOrCode {
			codingPath := strings.TrimSuffix(value, ".code")
			if path == codingPath || strings.HasPrefix(path, codingPath+".") {
				metadata = true
			}
		}
		if observation.ChoiceArm != "" {
			arm := observation.ChoiceArm
			if observation.OwningScope != "" {
				arm = observation.OwningScope + "." + arm
			}
			if path != value && strings.HasPrefix(path, arm+".") && strings.HasPrefix(value, arm+".") {
				metadata = true
			}
		}
		if observation.Source.Path != "" && (path == observation.Source.Path || strings.HasPrefix(path, observation.Source.Path+".")) {
			metadata = true
		}
	}
	if metadata {
		return "SOURCE_METADATA"
	}
	return "UNCOVERED"
}
