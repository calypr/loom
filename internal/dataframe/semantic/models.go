// Package semantic turns generated FHIR shape metadata into stable,
// researcher-facing concepts.  It deliberately returns descriptions of
// executable selections rather than backend-specific recipe or AQL nodes.
package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	RuleProfile            = "profile"
	RuleResourceSemantic   = "resource_semantic"
	RuleComplexDatatype    = "complex_datatype"
	RulePrimitiveRepeated  = "primitive_repeated"
	OutputScalar           = "scalar"
	OutputCodedValue       = "coded_value"
	OutputMeasurement      = "measurement"
	OutputDynamicFamily    = "dynamic_family"
	OutputReference        = "reference"
	OutputMetadata         = "raw_metadata"
	CardinalityOptionalOne = "optional_one"
	CardinalityRepeated    = "repeated"
	CardinalityPivoted     = "pivoted"
	SeverityInfo           = "info"
	SeverityWarning        = "warning"
	SeverityError          = "error"
)

// SchemaDescriptor is the stable input to a Registry. Fields are normally
// produced by DescribeResource and may be augmented with profile bindings.
type SchemaDescriptor struct {
	ResourceType string
	Fields       []SourceDescriptor
	Profiles     []ProfileBinding
}

// ProfileBinding allows a deployment/profile to override a generated rule.
// RuleID is intentionally a string so profiles can add rules without changing
// a public FHIR enum.
type ProfileBinding struct {
	Path        string
	RuleID      string
	Label       string
	Description string
}

// SourceDescriptor is a backend-neutral description of one discovered or
// generated source. Reference and shape values are metadata strings, not a
// closed list of FHIR structures.
type SourceDescriptor struct {
	Canonical         string
	ResourceType      string
	Path              string
	Profile           string
	SourcePath        string
	ValuePath         string
	KeySelector       string
	KeySystem         string
	KeyCode           string
	KeyDisplay        string
	RuleVersion       string
	Shape             string
	Primitive         string
	Repeated          bool
	Reference         string
	ItemReference     string
	PopulationCount   int64
	DistinctTruncated bool
	Examples          []Example
}

// Example is safe only when Safe is true. Rules never copy untrusted examples
// into a concept response.
type Example struct {
	Value string
	Safe  bool
}

// SafeExample is the explicit opt-in constructor for non-sensitive examples.
func SafeExample(value string) Example { return Example{Value: value, Safe: true} }

type ExamplePolicy struct {
	Values  []string
	Limited bool
}

type Selection struct {
	Mode             string
	SourcePath       string
	KeySelector      string
	ValueSelector    string
	ValueFallbacks   []string
	ItemSource       string
	ItemResourceType string
	Transforms       []string
	Key              string
}

type OutputDescriptor struct {
	Mode        string
	ValueType   string
	Cardinality string
	Selection   Selection
	Generic     bool
}

type TraceDescriptor struct {
	ResourceType   string
	RawPath        string
	RawKey         string
	RawValue       string
	RawCardinality string
	SourcePath     string
	ValuePath      string
	Reference      string
	RuleID         string
	RuleVersion    string
	Precedence     int
	Fallback       bool
}

type Concept struct {
	ID          string
	Label       string
	Group       string
	Description string
	RuleID      string
	RuleVersion string
	Precedence  int
	Source      SourceDescriptor
	Output      OutputDescriptor
	Examples    ExamplePolicy
	Trace       TraceDescriptor
}

type Family struct {
	ID         string
	Label      string
	RuleID     string
	Precedence int
	Source     SourceDescriptor
	Concepts   []Concept
	Trace      TraceDescriptor
}

type Diagnostic struct {
	Severity string
	Code     string
	RuleID   string
	Path     string
	Message  string
}

type Result struct {
	ResourceType string
	Families     []Family
	Concepts     []Concept
	Diagnostics  []Diagnostic
}

func (s SourceDescriptor) canonical() SourceDescriptor {
	s.Canonical = strings.TrimSpace(s.Canonical)
	s.ResourceType = strings.TrimSpace(s.ResourceType)
	s.Path = strings.TrimSpace(s.Path)
	s.Profile = strings.TrimSpace(s.Profile)
	s.SourcePath = strings.TrimSpace(s.SourcePath)
	s.ValuePath = strings.TrimSpace(s.ValuePath)
	s.KeySelector = strings.TrimSpace(s.KeySelector)
	s.KeySystem = strings.TrimSpace(s.KeySystem)
	s.KeyCode = strings.TrimSpace(s.KeyCode)
	s.KeyDisplay = strings.TrimSpace(s.KeyDisplay)
	s.RuleVersion = strings.TrimSpace(s.RuleVersion)
	s.Shape = strings.TrimSpace(s.Shape)
	s.Primitive = strings.TrimSpace(s.Primitive)
	s.Reference = strings.TrimSpace(s.Reference)
	s.ItemReference = strings.TrimSpace(s.ItemReference)
	s.Examples = append([]Example(nil), s.Examples...)
	return s
}

func (s SourceDescriptor) safeExamples(max int) ExamplePolicy {
	if max <= 0 {
		max = 5
	}
	seen := map[string]struct{}{}
	values := make([]string, 0, max)
	limited := false
	for _, example := range s.Examples {
		value := strings.TrimSpace(example.Value)
		if !example.Safe || value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		if len(values) == max {
			limited = true
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values)
	return ExamplePolicy{Values: values, Limited: limited}
}

func stableID(prefix string, value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return prefix + "_" + hex.EncodeToString(digest[:])
}

func conceptID(c Concept) string {
	return stableID("concept", struct {
		ResourceType string
		RuleID       string
		Path         string
		Selection    Selection
		Output       OutputDescriptor
	}{c.Source.ResourceType, c.RuleID, c.Source.Path, c.Output.Selection, c.Output})
}

func familyID(f Family) string {
	return stableID("family", struct {
		ResourceType string
		RuleID       string
		Path         string
	}{f.Source.ResourceType, f.RuleID, f.Source.Path})
}

func humanize(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "[]", "")
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func labelFor(source SourceDescriptor, suffix string) string {
	base := humanize(source.Path)
	if base == "" {
		base = source.ResourceType
	}
	if suffix == "" {
		return base
	}
	return fmt.Sprintf("%s %s", base, suffix)
}
