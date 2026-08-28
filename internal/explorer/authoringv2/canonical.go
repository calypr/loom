package authoringv2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

func (d Document) CanonicalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	n := d
	n.RouteSteps = append([]RouteStep(nil), d.RouteSteps...)
	n.Selections = append([]Selection(nil), d.Selections...)
	sort.SliceStable(n.Selections, func(i, j int) bool {
		left := n.Selections[i].CandidateID + "\x00" + n.Selections[i].OccurrenceID
		right := n.Selections[j].CandidateID + "\x00" + n.Selections[j].OccurrenceID
		return left < right
	})
	if n.RouteSteps == nil {
		n.RouteSteps = []RouteStep{}
	}
	if n.Selections == nil {
		n.Selections = []Selection{}
	}
	if n.Presentation == nil {
		n.Presentation = map[string]Presentation{}
	}
	return json.Marshal(n)
}

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
		n.Documents[i].Selections = append([]Selection(nil), n.Documents[i].Selections...)
		sort.SliceStable(n.Documents[i].Selections, func(a, b int) bool {
			left := n.Documents[i].Selections[a]
			right := n.Documents[i].Selections[b]
			return left.CandidateID+"\x00"+left.OccurrenceID+"\x00"+left.ProjectionMode < right.CandidateID+"\x00"+right.OccurrenceID+"\x00"+right.ProjectionMode
		})
		if n.Documents[i].RouteSteps == nil {
			n.Documents[i].RouteSteps = []RouteStep{}
		}
		if n.Documents[i].Selections == nil {
			n.Documents[i].Selections = []Selection{}
		}
		if n.Documents[i].Presentation == nil {
			n.Documents[i].Presentation = map[string]Presentation{}
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

func (d Document) Digest() (string, error) {
	raw, err := d.CanonicalJSON()
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
	if err := strictDecode(raw, &out); err != nil {
		return out, err
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
