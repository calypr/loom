package ingest

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmeg/jsonschemagraph/graph"
)

const defaultPreflightSampleRows = 10

// IngestionMode describes the loader selected for a resource type after
// checking the active graph schema. Generated is a fast path; Generic remains
// the supported schema-backed path for resources outside that optimized
// switch.
type IngestionMode string

const (
	IngestionModeGenerated   IngestionMode = "generated"
	IngestionModeGeneric     IngestionMode = "generic"
	IngestionModeUnsupported IngestionMode = "unsupported"
)

// PreflightResource is a deterministic per-resource report. Resource types
// come from the staged file names because that is the load contract; sampled
// payload resourceType values are checked below before any database mutation.
type PreflightResource struct {
	ResourceType             string        `json:"resourceType"`
	Files                    []string      `json:"files"`
	SampledRows              int           `json:"sampledRows"`
	GraphSchemaSupported     bool          `json:"graphSchemaSupported"`
	GeneratedLoaderSupported bool          `json:"generatedLoaderSupported"`
	Mode                     IngestionMode `json:"mode"`
}

// PreflightIssue is intentionally structured so an HTTP or CLI caller can
// present every actionable issue rather than leaving a partially loaded
// project behind after the first invalid file.
type PreflightIssue struct {
	Code         string `json:"code"`
	File         string `json:"file"`
	ResourceType string `json:"resourceType"`
	Row          int    `json:"row,omitempty"`
	Message      string `json:"message"`
}

type PreflightReport struct {
	Resources []PreflightResource `json:"resources"`
	Issues    []PreflightIssue    `json:"issues"`
}

func (r PreflightReport) Valid() bool {
	return len(r.Issues) == 0
}

// PreflightError preserves the complete report for callers while retaining a
// compact Error implementation for the existing synchronous Load API.
type PreflightError struct {
	Report PreflightReport
}

func (e *PreflightError) Error() string {
	if len(e.Report.Issues) == 0 {
		return "ingestion preflight failed"
	}
	issue := e.Report.Issues[0]
	return fmt.Sprintf("ingestion preflight failed: %s", issue.Message)
}

// PreflightFiles validates the staged filename-to-resource contract and
// chooses the generated or generic loader for every active graph-schema class.
// It samples a bounded number of records per file; full validation remains in
// the streaming loader so large imports are not read twice.
func PreflightFiles(files []string, schema *graph.GraphSchema, sampleRows int) (PreflightReport, error) {
	if schema == nil {
		return PreflightReport{}, fmt.Errorf("graph schema is required")
	}
	if sampleRows <= 0 {
		sampleRows = defaultPreflightSampleRows
	}

	grouped := make(map[string][]string, len(files))
	for _, file := range files {
		resourceType := ResourceTypeFromPath(file)
		grouped[resourceType] = append(grouped[resourceType], file)
	}
	resourceTypes := make([]string, 0, len(grouped))
	for resourceType := range grouped {
		resourceTypes = append(resourceTypes, resourceType)
	}
	sort.Strings(resourceTypes)

	report := PreflightReport{
		Resources: make([]PreflightResource, 0, len(resourceTypes)),
		Issues:    []PreflightIssue{},
	}
	for _, resourceType := range resourceTypes {
		resourceFiles := append([]string(nil), grouped[resourceType]...)
		sort.Strings(resourceFiles)
		classSupported := schema.GetClass(resourceType) != nil
		generatedSupported := supportsGeneratedLoad(resourceType)
		mode := IngestionModeGeneric
		if !classSupported {
			mode = IngestionModeUnsupported
		} else if generatedSupported {
			mode = IngestionModeGenerated
		}
		resource := PreflightResource{
			ResourceType:             resourceType,
			Files:                    resourceFiles,
			GraphSchemaSupported:     classSupported,
			GeneratedLoaderSupported: generatedSupported,
			Mode:                     mode,
		}
		if !classSupported {
			report.Issues = append(report.Issues, PreflightIssue{
				Code:         "unsupported_graph_schema_resource",
				File:         filepath.Base(resourceFiles[0]),
				ResourceType: resourceType,
				Message:      fmt.Sprintf("resource type %q is not represented by the active graph schema", resourceType),
			})
		}

		for _, file := range resourceFiles {
			sampled, issues, err := sampleFileResourceTypes(file, resourceType, sampleRows)
			if err != nil {
				return PreflightReport{}, err
			}
			resource.SampledRows += sampled
			report.Issues = append(report.Issues, issues...)
		}
		report.Resources = append(report.Resources, resource)
	}
	if !report.Valid() {
		return report, &PreflightError{Report: report}
	}
	return report, nil
}

func sampleFileResourceTypes(file string, expectedResourceType string, sampleRows int) (int, []PreflightIssue, error) {
	scanner, closeFn, err := OpenLineScanner(file)
	if err != nil {
		return 0, nil, err
	}
	defer closeFn()

	issues := []PreflightIssue{}
	sampled := 0
	row := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		row++
		sampled++
		var envelope struct {
			ResourceType string `json:"resourceType"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			issues = append(issues, PreflightIssue{
				Code:         "invalid_json",
				File:         filepath.Base(file),
				ResourceType: expectedResourceType,
				Row:          row,
				Message:      fmt.Sprintf("%s row %d is not valid JSON: %v", filepath.Base(file), row, err),
			})
		} else if envelope.ResourceType == "" {
			issues = append(issues, PreflightIssue{
				Code:         "missing_resource_type",
				File:         filepath.Base(file),
				ResourceType: expectedResourceType,
				Row:          row,
				Message:      fmt.Sprintf("%s row %d does not declare resourceType", filepath.Base(file), row),
			})
		} else if envelope.ResourceType != expectedResourceType {
			issues = append(issues, PreflightIssue{
				Code:         "resource_type_mismatch",
				File:         filepath.Base(file),
				ResourceType: expectedResourceType,
				Row:          row,
				Message:      fmt.Sprintf("%s row %d declares resourceType %q, expected %q from the staged filename", filepath.Base(file), row, envelope.ResourceType, expectedResourceType),
			})
		}
		if sampled >= sampleRows {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, err
	}
	return sampled, issues, nil
}
