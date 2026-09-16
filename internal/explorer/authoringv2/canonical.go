package authoringv2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

func (w Workspace) CanonicalJSON() ([]byte, error) {
	if err := w.Validate(); err != nil {
		return nil, err
	}
	n := w.NormalizePresentationOrders()
	n.Documents = append([]Document(nil), n.Documents...)
	for i := range n.Documents {
		n.Documents[i].APIVersion = ""
		if n.Documents[i].Columns == nil {
			n.Documents[i].Columns = []Column{}
		}
		for j := range n.Documents[i].Columns {
			n.Documents[i].Columns[j].Source = n.Documents[i].Columns[j].Source.Normalized()
		}
	}
	n.Tabs = append([]Tab(nil), w.Tabs...)
	if n.Documents == nil {
		n.Documents = []Document{}
	}
	if n.Tabs == nil {
		n.Tabs = []Tab{}
	}
	return json.Marshal(n)
}

// NormalizePresentationOrders gives every table column one unambiguous,
// contiguous presentation position. Authored order is the primary key and the
// stable public column identity breaks ties. The normalized order also becomes
// the recipe projection order, so presentation and execution cannot disagree.
//
// Duplicate presentation positions are valid mutable Builder input. Freezing
// them here keeps equivalent requests from depending on frontend collection or
// map iteration order and makes the normalized workspace safe to persist and
// return to the Builder.
func (w Workspace) NormalizePresentationOrders() Workspace {
	n := w
	n.Documents = append([]Document(nil), w.Documents...)
	for documentIndex := range n.Documents {
		document := &n.Documents[documentIndex]
		columns := append([]Column(nil), document.Columns...)
		if columns == nil {
			columns = []Column{}
		}
		for columnIndex := range columns {
			column := &columns[columnIndex]
			if column.Table != nil {
				table := *column.Table
				column.Table = &table
			}
			if column.Filter != nil {
				filter := *column.Filter
				column.Filter = &filter
			}
			if column.Chart != nil {
				chart := *column.Chart
				column.Chart = &chart
			}
		}
		sort.SliceStable(columns, func(i, j int) bool {
			left, right := columns[i], columns[j]
			leftClass, leftOrder := presentationOrder(left)
			rightClass, rightOrder := presentationOrder(right)
			if leftClass != rightClass {
				return leftClass < rightClass
			}
			if leftOrder != rightOrder {
				return leftOrder < rightOrder
			}
			return left.Column < right.Column
		})
		tableOrder := 0
		for columnIndex := range columns {
			if columns[columnIndex].Table == nil {
				continue
			}
			value := tableOrder
			columns[columnIndex].Table.Order = &value
			tableOrder++
		}
		normalizeFilterOrders(columns)
		normalizeChartOrders(columns)
		document.Columns = columns
	}
	return n
}

func normalizeFilterOrders(columns []Column) {
	indexes := make([]int, 0, len(columns))
	for index := range columns {
		if columns[index].Filter != nil {
			indexes = append(indexes, index)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		left, right := columns[indexes[i]], columns[indexes[j]]
		return auxiliaryPresentationLess(left.Filter.Order, left.Column, right.Filter.Order, right.Column)
	})
	for order, index := range indexes {
		value := order
		columns[index].Filter.Order = &value
	}
}

func normalizeChartOrders(columns []Column) {
	indexes := make([]int, 0, len(columns))
	for index := range columns {
		if columns[index].Chart != nil {
			indexes = append(indexes, index)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		left, right := columns[indexes[i]], columns[indexes[j]]
		return auxiliaryPresentationLess(left.Chart.Order, left.Column, right.Chart.Order, right.Column)
	})
	for order, index := range indexes {
		value := order
		columns[index].Chart.Order = &value
	}
}

func auxiliaryPresentationLess(leftOrder *int, leftColumn string, rightOrder *int, rightColumn string) bool {
	if (leftOrder == nil) != (rightOrder == nil) {
		return leftOrder != nil
	}
	if leftOrder != nil && *leftOrder != *rightOrder {
		return *leftOrder < *rightOrder
	}
	return leftColumn < rightColumn
}

func presentationOrder(column Column) (class, order int) {
	if column.Table == nil {
		return 2, 0
	}
	if column.Table.Order == nil {
		return 1, 0
	}
	return 0, *column.Table.Order
}

func (w Workspace) Digest() (string, error) {
	raw, err := w.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c CatalogSnapshot) CanonicalJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	n := c
	n.Nodes = append([]CatalogNode(nil), c.Nodes...)
	n.Edges = append([]CatalogEdge(nil), c.Edges...)
	n.Candidates = append([]CatalogCandidate(nil), c.Candidates...)
	n.Diagnostics = append([]CatalogDiagnostic(nil), c.Diagnostics...)
	for i := range n.Candidates {
		n.Candidates[i].ProjectionModes = append([]string(nil), n.Candidates[i].ProjectionModes...)
		n.Candidates[i].FilterOperators = append([]string(nil), n.Candidates[i].FilterOperators...)
		n.Candidates[i].ChartOperations = append([]string(nil), n.Candidates[i].ChartOperations...)
		n.Candidates[i].ConceptCandidates = cloneConceptCandidates(n.Candidates[i].ConceptCandidates)
		sort.Strings(n.Candidates[i].ProjectionModes)
		sort.Strings(n.Candidates[i].FilterOperators)
		sort.Strings(n.Candidates[i].ChartOperations)
	}
	sort.Slice(n.Nodes, func(i, j int) bool { return n.Nodes[i].ID < n.Nodes[j].ID })
	sort.Slice(n.Edges, func(i, j int) bool { return n.Edges[i].ID < n.Edges[j].ID })
	sort.Slice(n.Candidates, func(i, j int) bool { return n.Candidates[i].ID < n.Candidates[j].ID })
	sort.Slice(n.Diagnostics, func(i, j int) bool {
		if n.Diagnostics[i].Code != n.Diagnostics[j].Code {
			return n.Diagnostics[i].Code < n.Diagnostics[j].Code
		}
		return n.Diagnostics[i].Message < n.Diagnostics[j].Message
	})
	return json.Marshal(n)
}

func cloneConceptCandidates(in []ConceptCandidate) []ConceptCandidate {
	out := make([]ConceptCandidate, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].ExtensionURLPath = append([]string(nil), in[i].ExtensionURLPath...)
		out[i].ObservedUnits = append([]string(nil), in[i].ObservedUnits...)
		out[i].Examples = append([]string(nil), in[i].Examples...)
	}
	sort.Slice(out, func(i, j int) bool {
		left := strings.Join([]string{out[i].SourceResourceType, out[i].SourceCanonical, out[i].SourceProfile, out[i].SourcePath, out[i].OwningScope, strings.Join(out[i].ExtensionURLPath, "\x1f"), out[i].KeySelector, out[i].System, out[i].Code, out[i].ValueSelector, out[i].ChoiceArm}, "\x00")
		right := strings.Join([]string{out[j].SourceResourceType, out[j].SourceCanonical, out[j].SourceProfile, out[j].SourcePath, out[j].OwningScope, strings.Join(out[j].ExtensionURLPath, "\x1f"), out[j].KeySelector, out[j].System, out[j].Code, out[j].ValueSelector, out[j].ChoiceArm}, "\x00")
		return left < right
	})
	return out
}

func (c CatalogSnapshot) Digest() (string, error) {
	raw, err := c.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s BuilderState) CanonicalJSON() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var workspace []byte
	var err error
	if s.Workspace == nil {
		workspace = []byte("null")
	} else {
		workspace, err = s.Workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
	}
	catalog, err := s.Catalog.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Workspace  json.RawMessage `json:"workspace"`
		Catalog    json.RawMessage `json:"catalog"`
	}{s.APIVersion, s.Kind, workspace, catalog})
}

func (s BuilderState) Digest() (string, error) {
	raw, err := s.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func DecodeWorkspace(raw []byte) (Workspace, error) {
	var out Workspace
	if err := strictDecode(raw, &out); err == nil {
		if err := out.Validate(); err != nil {
			return out, err
		}
		for i := range out.Documents {
			if out.Documents[i].Columns == nil {
				out.Documents[i].Columns = []Column{}
			}
		}
		return out, nil
	} else {
		// A legacy flat source is accepted only while decoding a persisted
		// pre-v3 workspace. New mutation requests never use this path.
		var version struct {
			SemanticsVersion int `json:"semanticsVersion"`
		}
		if json.Unmarshal(raw, &version) != nil || version.SemanticsVersion >= CurrentSemanticsVersion {
			return out, err
		}
		migrated, migrationErr := decodePersistedLegacyWorkspace(raw)
		if migrationErr != nil {
			return out, err
		}
		out = migrated
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	for i := range out.Documents {
		if out.Documents[i].Columns == nil {
			out.Documents[i].Columns = []Column{}
		}
	}
	return out, nil
}

type persistedWorkspaceWire struct {
	APIVersion         string                           `json:"apiVersion"`
	Kind               string                           `json:"kind"`
	SemanticsVersion   int                              `json:"semanticsVersion,omitempty"`
	MigrationDecisions []string                         `json:"migrationDecisions,omitempty"`
	Explorer           ExplorerMetadata                 `json:"explorer"`
	Documents          []persistedDocumentWire          `json:"documents"`
	Tabs               []Tab                            `json:"tabs"`
	SharedFilters      map[string][]SharedFilterBinding `json:"sharedFilters,omitempty"`
	FileActions        *FileActions                     `json:"fileActions,omitempty"`
}

type persistedDocumentWire struct {
	Kind             string                `json:"kind"`
	Output           Output                `json:"output"`
	RootResourceType string                `json:"rootResourceType,omitempty"`
	Route            RouteNode             `json:"route,omitempty"`
	Columns          []persistedColumnWire `json:"columns"`
	FixedFilters     []FixedFilter         `json:"fixedFilters,omitempty"`
	Actions          []Action              `json:"actions,omitempty"`
}

type persistedColumnWire struct {
	Column       string              `json:"column"`
	Label        string              `json:"label"`
	LogicalType  string              `json:"logicalType,omitempty"`
	OccurrenceID string              `json:"occurrenceId"`
	Source       json.RawMessage     `json:"source"`
	Table        *TablePresentation  `json:"table,omitempty"`
	Filter       *FilterPresentation `json:"filter,omitempty"`
	Chart        *ChartPresentation  `json:"chart,omitempty"`
}

type legacyColumnSource struct {
	Kind           string   `json:"kind"`
	FieldPath      string   `json:"fieldPath,omitempty"`
	Match          string   `json:"match,omitempty"`
	ProjectionMode string   `json:"projectionMode,omitempty"`
	Operation      string   `json:"operation,omitempty"`
	WherePath      string   `json:"wherePath,omitempty"`
	WhereEquals    string   `json:"whereEquals,omitempty"`
	RequiredValues []string `json:"requiredValues,omitempty"`
}

func decodePersistedLegacyWorkspace(raw []byte) (Workspace, error) {
	var wire persistedWorkspaceWire
	if err := strictDecode(raw, &wire); err != nil {
		return Workspace{}, err
	}
	out := Workspace{
		APIVersion: wire.APIVersion, Kind: wire.Kind, SemanticsVersion: wire.SemanticsVersion,
		MigrationDecisions: append([]string(nil), wire.MigrationDecisions...), Explorer: wire.Explorer,
		Tabs: append([]Tab(nil), wire.Tabs...), SharedFilters: wire.SharedFilters, FileActions: wire.FileActions,
		Documents: make([]Document, 0, len(wire.Documents)),
	}
	for documentIndex, document := range wire.Documents {
		converted := Document{Kind: document.Kind, Output: document.Output, RootResourceType: document.RootResourceType, Route: document.Route, FixedFilters: document.FixedFilters, Actions: document.Actions, Columns: make([]Column, 0, len(document.Columns))}
		for columnIndex, column := range document.Columns {
			source, err := decodePersistedSource(column.Source)
			if err != nil {
				return Workspace{}, fmt.Errorf("documents[%d].columns[%d].source: %w", documentIndex, columnIndex, err)
			}
			converted.Columns = append(converted.Columns, Column{Column: column.Column, Label: column.Label, LogicalType: column.LogicalType, OccurrenceID: column.OccurrenceID, Source: source, Table: column.Table, Filter: column.Filter, Chart: column.Chart})
		}
		out.Documents = append(out.Documents, converted)
	}
	return out, nil
}

func decodePersistedSource(raw json.RawMessage) (ColumnSource, error) {
	var current ColumnSource
	if err := strictDecode(raw, &current); err == nil {
		return current, nil
	}
	var legacy legacyColumnSource
	if err := strictDecode(raw, &legacy); err != nil {
		return ColumnSource{}, err
	}
	mode := strings.ToUpper(strings.TrimSpace(legacy.ProjectionMode))
	if mode == "" {
		mode = "FIRST"
	}
	switch legacy.Kind {
	case SourceField:
		return ColumnSource{Kind: SourceField, Field: &FieldSource{Path: legacy.FieldPath, ProjectionMode: mode}}, nil
	case SourceIdentifierBySystem, SourceExtensionByURL, SourceCodingBySystem, SourceObservationComponentByCode:
		return ColumnSource{Kind: legacy.Kind, Lookup: &LookupSource{Match: legacy.Match, Path: legacy.FieldPath, ProjectionMode: mode}}, nil
	case SourceAggregate:
		aggregate := &AggregateSource{Operation: strings.ToUpper(strings.TrimSpace(legacy.Operation)), Path: legacy.FieldPath, RequiredValues: append([]string(nil), legacy.RequiredValues...)}
		if strings.TrimSpace(legacy.WherePath) != "" || strings.TrimSpace(legacy.WhereEquals) != "" {
			aggregate.Where = &SourceWhere{Path: legacy.WherePath, Equals: legacy.WhereEquals}
		}
		return ColumnSource{Kind: SourceAggregate, Aggregate: aggregate}, nil
	case SourceProjectID:
		return ColumnSource{Kind: SourceProjectID}, nil
	default:
		return ColumnSource{}, fmt.Errorf("unsupported source kind %q", legacy.Kind)
	}
}

func strictDecode(raw []byte, target any) error {
	if err := rejectDuplicateKeys(raw); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("strict decode: trailing JSON value")
		}
		return fmt.Errorf("strict decode: %w", err)
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if seen[name] {
					return fmt.Errorf("duplicate JSON key %q", name)
				}
				seen[name] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	return nil
}
