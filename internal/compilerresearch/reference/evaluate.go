package reference

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type Document map[string]any

const sourceDocumentKey = "\x00reference_source_document"

type UnsupportedError struct{ Feature string }

func (e UnsupportedError) Error() string {
	return "reference interpreter does not support " + e.Feature
}

func Evaluate(output recipe.Output, documents []Document) ([]Row, error) {
	if err := validateSupportedOutput(output); err != nil {
		return nil, err
	}
	rows, columns, err := project(output, documents)
	if err != nil {
		return nil, err
	}
	if output.Construction == nil {
		return publicProjectionRows(output, rows), nil
	}
	for _, step := range output.Construction.Steps {
		rows, columns, err = evaluateStep(step, rows, columns)
		if err != nil {
			return nil, fmt.Errorf("step %q (%s): %w", step.ID, step.Operation.Kind, err)
		}
	}
	return exposeRows(rows, columns), nil
}

func validateSupportedOutput(output recipe.Output) error {
	if output.RootColumnNaming != "" && output.RootColumnNaming != recipe.RootColumnNamingExact {
		return UnsupportedError{Feature: "PROJECT root column naming " + string(output.RootColumnNaming)}
	}
	if output.ArchiveRetiredReshape != nil || len(output.DerivedColumns) != 0 || output.GroupRows != nil || output.Identity != nil || output.Population != nil || output.Expand != nil ||
		len(output.Pivots) != 0 || len(output.OwnerRecords) != 0 || len(output.Aggregates) != 0 || len(output.Slices) != 0 || len(output.DynamicColumns) != 0 || len(output.ExtensionColumns) != 0 || len(output.CatalogProjections) != 0 || len(output.ColumnTransformations) != 0 {
		return UnsupportedError{Feature: "PROJECT output modifiers outside direct Fields and Filters"}
	}
	for _, field := range output.Fields {
		if strings.Contains(field.Expr.Select, "[]") || field.ValueMode == recipe.ValueModeAll || field.ValueMode == recipe.ValueModeDistinct {
			return UnsupportedError{Feature: "repeated or aggregated PROJECT field " + field.Name}
		}
		for _, fallback := range field.Fallbacks {
			if strings.Contains(fallback.Select, "[]") {
				return UnsupportedError{Feature: "repeated PROJECT fallback " + field.Name}
			}
		}
	}
	for _, filter := range output.Filters {
		if filter.Quantifier != "" || strings.Contains(filter.Select, "[]") {
			return UnsupportedError{Feature: "repeated PROJECT filter " + filter.Select}
		}
	}
	if output.Construction == nil {
		return nil
	}
	expectedInput := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
	for _, step := range output.Construction.Steps {
		if len(step.Inputs) != 1 || step.Inputs[0].Kind != expectedInput.Kind || step.Inputs[0].StepID != expectedInput.StepID {
			return UnsupportedError{Feature: "nonlinear construction inputs"}
		}
		expectedInput = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: step.ID}
		if len(step.RowValues) != 0 {
			return UnsupportedError{Feature: "construction rowValues"}
		}
		switch step.Operation.Kind {
		case recipe.ConstructionPivotOp:
			if step.Operation.Pivot == nil || step.Operation.Pivot.Discovery != nil {
				return UnsupportedError{Feature: "dynamic PIVOT discovery"}
			}
		case recipe.ConstructionDeriveOp, recipe.ConstructionFilterOp, recipe.ConstructionUnpivotOp, recipe.ConstructionExpandOp:
		case recipe.ConstructionGroupOp:
			if step.Operation.Group == nil || len(step.Operation.Group.Keys) == 0 {
				return UnsupportedError{Feature: "keyless GROUP semantics"}
			}
			for _, key := range step.Operation.Group.Keys {
				if key.FieldSource != nil || key.SemanticCategory != nil {
					return UnsupportedError{Feature: "resolved GROUP field source"}
				}
			}
		case recipe.ConstructionCodedGroupOp:
			if step.Operation.CodedGroup == nil || len(step.Operation.CodedGroup.Source.Route) != 0 {
				return UnsupportedError{Feature: "routed CODED_GROUP"}
			}
		case recipe.ConstructionCodedPivotOp:
			if step.Operation.CodedPivot == nil || len(step.Operation.CodedPivot.Source.Route) != 0 || len(step.Operation.CodedPivot.Source.ValueFallbacks) != 0 || step.Operation.CodedPivot.Source.FallbackOnBlankString || len(step.Operation.CodedPivot.Source.ChoiceArms) != 0 {
				return UnsupportedError{Feature: "routed or fallback CODED_PIVOT"}
			}
		case recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp, recipe.ConstructionRelatedEligibilityOp, recipe.ConstructionRelatedFieldOp, recipe.ConstructionCombineOp:
			return UnsupportedError{Feature: string(step.Operation.Kind)}
		default:
			return UnsupportedError{Feature: "construction kind " + string(step.Operation.Kind)}
		}
	}
	return nil
}

func project(output recipe.Output, documents []Document) ([]Row, []recipe.StageColumn, error) {
	if len(output.Traversals) != 0 || output.Expand != nil {
		return nil, nil, UnsupportedError{Feature: "nested PROJECT traversals or EXPAND"}
	}
	columns := append([]recipe.StageColumn(nil), constructionSourceColumns(output)...)
	if len(columns) == 0 {
		for _, field := range output.Fields {
			id := field.ColumnID
			if id == "" {
				id = field.Name
			}
			columns = append(columns, recipe.StageColumn{ID: id, Name: field.Name})
		}
	}
	rows := make([]Row, 0, len(documents))
	for _, document := range documents {
		row := make(Row, len(columns))
		row[sourceDocumentKey] = Value{Data: map[string]any(document), Present: true}
		for _, field := range output.Fields {
			value, err := expressionValue(map[string]any(document), field.Expr)
			if err != nil {
				return nil, nil, fmt.Errorf("PROJECT field %q: %w", field.Name, err)
			}
			if (!value.Present || value.Data == nil) && len(field.Fallbacks) != 0 {
				for _, fallback := range field.Fallbacks {
					value, err = expressionValue(map[string]any(document), fallback)
					if err != nil {
						return nil, nil, err
					}
					if value.Present && value.Data != nil {
						break
					}
				}
			}
			id := field.ColumnID
			if id == "" {
				id = field.Name
			}
			row[id] = value
		}
		keep := true
		for _, filter := range output.Filters {
			value, err := expressionValue(map[string]any(document), recipe.Expression{Select: filter.Select})
			if err != nil {
				return nil, nil, err
			}
			match, err := applyFilter(value, recipe.ConstructionFilter{Operator: filter.Operator, Values: filter.Values})
			if err != nil {
				return nil, nil, err
			}
			if !match {
				keep = false
				break
			}
		}
		if keep {
			rows = append(rows, row)
		}
	}
	return rows, columns, nil
}

func constructionSourceColumns(o recipe.Output) []recipe.StageColumn {
	if o.Construction == nil {
		return nil
	}
	return append([]recipe.StageColumn(nil), o.Construction.SourceColumns...)
}

func publicProjectionRows(output recipe.Output, rows []Row) []Row {
	result := make([]Row, 0, len(rows))
	for _, row := range rows {
		public := make(Row)
		for _, field := range output.Fields {
			id := field.ColumnID
			if id == "" {
				id = field.Name
			}
			public[field.Name] = row[id]
		}
		result = append(result, public)
	}
	return result
}

func evaluateStep(step recipe.ConstructionStep, input []Row, inputColumns []recipe.StageColumn) ([]Row, []recipe.StageColumn, error) {
	op := step.Operation
	var rows []Row
	switch op.Kind {
	case recipe.ConstructionFilterOp:
		if op.Filter == nil {
			return nil, nil, fmt.Errorf("FILTER payload is nil")
		}
		for _, row := range input {
			keep, err := applyFilter(row[op.Filter.ColumnID], *op.Filter)
			if err != nil {
				return nil, nil, err
			}
			if keep {
				rows = append(rows, cloneRow(row))
			}
		}
	case recipe.ConstructionDeriveOp:
		if op.Derive == nil {
			return nil, nil, fmt.Errorf("DERIVE payload is nil")
		}
		for _, row := range input {
			left, err := deriveOperand(row, op.Derive.Left)
			if err != nil {
				return nil, nil, err
			}
			right, err := deriveOperand(row, op.Derive.Right)
			if err != nil {
				return nil, nil, err
			}
			value, err := derive(left, right, op.Derive.Operation, op.Derive.MissingInputPolicy, op.Derive.DivisionByZeroPolicy)
			if err != nil {
				return nil, nil, err
			}
			out := cloneRow(row)
			out[op.Derive.OutputColumnID] = value
			rows = append(rows, out)
		}
	case recipe.ConstructionPivotOp:
		if op.Pivot == nil {
			return nil, nil, fmt.Errorf("PIVOT payload is nil")
		}
		var err error
		rows, err = pivotRows(input, *op.Pivot)
		if err != nil {
			return nil, nil, err
		}
	case recipe.ConstructionUnpivotOp:
		if op.Unpivot == nil {
			return nil, nil, fmt.Errorf("UNPIVOT payload is nil")
		}
		rows = unpivotRows(input, *op.Unpivot)
	case recipe.ConstructionExpandOp:
		if op.Expand == nil {
			return nil, nil, fmt.Errorf("EXPAND payload is nil")
		}
		var err error
		rows, err = expandRows(input, *op.Expand)
		if err != nil {
			return nil, nil, err
		}
	case recipe.ConstructionGroupOp:
		if op.Group == nil {
			return nil, nil, fmt.Errorf("GROUP payload is nil")
		}
		var err error
		rows, err = groupRows(input, *op.Group)
		if err != nil {
			return nil, nil, err
		}
	case recipe.ConstructionCodedGroupOp:
		if op.CodedGroup == nil {
			return nil, nil, fmt.Errorf("CODED_GROUP payload is nil")
		}
		var err error
		rows, err = codedGroupRows(input, *op.CodedGroup)
		if err != nil {
			return nil, nil, err
		}
	case recipe.ConstructionCodedPivotOp:
		if op.CodedPivot == nil {
			return nil, nil, fmt.Errorf("CODED_PIVOT payload is nil")
		}
		var err error
		rows, err = codedPivotRows(input, *op.CodedPivot)
		if err != nil {
			return nil, nil, err
		}
	case recipe.ConstructionRelatedSourceOp, recipe.ConstructionRelatedExpandOp, recipe.ConstructionRelatedEligibilityOp, recipe.ConstructionRelatedFieldOp:
		return nil, nil, UnsupportedError{Feature: string(op.Kind) + " until the interpreter has an FHIR reference graph"}
	case recipe.ConstructionCombineOp:
		return nil, nil, UnsupportedError{Feature: "COMBINE workspace input semantics"}
	default:
		return nil, nil, UnsupportedError{Feature: fmt.Sprintf("construction kind %q", op.Kind)}
	}
	return conformRows(rows, step.Outputs), step.Outputs, nil
}

func deriveOperand(row Row, operand recipe.ConstructionOperand) (Value, error) {
	if operand.Kind == recipe.DerivedColumnOperand {
		return row[operand.ColumnID], nil
	}
	if operand.Kind == recipe.DerivedLiteralOperand && operand.Literal != nil {
		return Value{Data: literalValue(*operand.Literal), Present: true}, nil
	}
	return Value{}, fmt.Errorf("invalid DERIVE operand %q", operand.Kind)
}

func pivotRows(input []Row, pivot recipe.ConstructionPivot) ([]Row, error) {
	type group struct {
		keys   []Value
		values map[string][]Value
	}
	groups := map[string]*group{}
	for _, row := range input {
		keys := make([]Value, len(pivot.GroupKeyIDs))
		for i, id := range pivot.GroupKeyIDs {
			keys[i] = row[id]
		}
		key, err := canonicalKey(keys)
		if err != nil {
			return nil, err
		}
		g := groups[key]
		if g == nil {
			g = &group{keys: keys, values: map[string][]Value{}}
			groups[key] = g
		}
		category := row[pivot.CategoryColumnID]
		if !category.Present || category.Data == nil {
			continue
		}
		matched := ""
		for _, item := range pivot.Categories {
			if equal(category.Data, scalarValue(item.Key)) {
				matched = item.OutputColumnID
				break
			}
		}
		if matched == "" {
			if pivot.UnlistedCategoryPolicy == recipe.PivotUnlistedCategoryError {
				return nil, fmt.Errorf("unlisted Pivot category %v", category.Data)
			}
			continue
		}
		g.values[matched] = append(g.values[matched], row[pivot.ValueColumnID])
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]Row, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		row := make(Row)
		for i, id := range pivot.GroupKeyIDs {
			row[id] = g.keys[i]
		}
		for _, category := range pivot.Categories {
			values := g.values[category.OutputColumnID]
			value, err := aggregatePivot(values, pivot.DuplicatePolicy, pivot.MissingCellPolicy)
			if err != nil {
				return nil, err
			}
			row[category.OutputColumnID] = value
		}
		result = append(result, row)
	}
	return result, nil
}

func aggregatePivot(values []Value, duplicate recipe.PivotDuplicatePolicy, missing recipe.PivotMissingCellPolicy) (Value, error) {
	nonNull := make([]float64, 0, len(values))
	for _, value := range values {
		if value.Present && value.Data != nil {
			n, ok := numeric(value.Data)
			if !ok {
				return Value{}, fmt.Errorf("Pivot value %v is not numeric", value.Data)
			}
			nonNull = append(nonNull, n)
		}
	}
	if len(nonNull) == 0 {
		if missing == recipe.PivotMissingCellError {
			return Value{}, fmt.Errorf("Pivot cell is missing")
		}
		return Value{Present: true}, nil
	}
	if len(nonNull) == 1 {
		return Value{Data: nonNull[0], Present: true}, nil
	}
	switch duplicate {
	case recipe.PivotDuplicateError:
		return Value{}, fmt.Errorf("duplicate Pivot cell has %d values", len(nonNull))
	case recipe.PivotDuplicateSum:
		total := 0.0
		for _, v := range nonNull {
			total += v
		}
		return Value{Data: total, Present: true}, nil
	case recipe.PivotDuplicateMin:
		n := nonNull[0]
		for _, v := range nonNull[1:] {
			if v < n {
				n = v
			}
		}
		return Value{Data: n, Present: true}, nil
	case recipe.PivotDuplicateMax:
		n := nonNull[0]
		for _, v := range nonNull[1:] {
			if v > n {
				n = v
			}
		}
		return Value{Data: n, Present: true}, nil
	default:
		return Value{}, fmt.Errorf("unsupported duplicate policy %q", duplicate)
	}
}

func unpivotRows(input []Row, operation recipe.ConstructionUnpivot) []Row {
	var result []Row
	for _, row := range input {
		for _, source := range operation.Inputs {
			value := row[source.ColumnID]
			if (!value.Present || value.Data == nil) && operation.NullRowPolicy == recipe.UnpivotNullDrop {
				continue
			}
			out := cloneRow(row)
			out[operation.KeyOutputColumnID] = Value{Data: scalarValue(source.Key), Present: true}
			out[operation.ValueOutputColumnID] = value
			result = append(result, out)
		}
	}
	return result
}

func expandRows(input []Row, operation recipe.ConstructionExpand) ([]Row, error) {
	var result []Row
	for _, row := range input {
		value := row[operation.InputColumnID]
		array, ok := value.Data.([]any)
		if !ok || len(array) == 0 {
			policy := operation.EmptyPolicy.Normalized()
			switch policy {
			case recipe.ExpansionExclude:
				continue
			case recipe.ExpansionPreserveParent:
				out := cloneRow(row)
				out[operation.OutputColumnID] = Value{Present: true}
				if operation.OrdinalColumnID != "" {
					out[operation.OrdinalColumnID] = Value{Present: true}
				}
				result = append(result, out)
				continue
			default:
				return nil, fmt.Errorf("EXPAND input %s is empty or not an array", operation.InputColumnID)
			}
		}
		for index, item := range array {
			out := cloneRow(row)
			out[operation.OutputColumnID] = Value{Data: item, Present: true}
			if operation.OrdinalColumnID != "" {
				out[operation.OrdinalColumnID] = Value{Data: index, Present: true}
			}
			result = append(result, out)
		}
	}
	return result, nil
}

func groupRows(input []Row, operation recipe.ConstructionGroup) ([]Row, error) {
	type bucket struct {
		keys []Value
		rows []Row
	}
	buckets := map[string]*bucket{}
	for _, row := range input {
		keys := make([]Value, len(operation.Keys))
		missingKey := false
		for i, key := range operation.Keys {
			keys[i] = row[key.InputColumnID]
			missingKey = missingKey || !keys[i].Present || keys[i].Data == nil
		}
		if missingKey {
			switch operation.MissingKeyPolicy.Normalized() {
			case recipe.ConstructionGroupMissingKeyExclude:
				continue
			case recipe.ConstructionGroupMissingKeyError:
				return nil, fmt.Errorf("GROUP key is missing")
			}
		}
		k, err := canonicalKey(keys)
		if err != nil {
			return nil, err
		}
		b := buckets[k]
		if b == nil {
			b = &bucket{keys: keys}
			buckets[k] = b
		}
		b.rows = append(b.rows, row)
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var result []Row
	for _, key := range keys {
		b := buckets[key]
		out := make(Row)
		for i, k := range operation.Keys {
			out[k.OutputColumnID] = b.keys[i]
		}
		for _, agg := range operation.Aggregates {
			values := make([]any, 0, len(b.rows))
			for _, r := range b.rows {
				v := r[agg.InputColumnID]
				if v.Present && v.Data != nil {
					values = append(values, v.Data)
				}
			}
			out[agg.OutputColumnID], _ = groupAggregate(agg.Operation, b.rows, values)
		}
		result = append(result, out)
	}
	return result, nil
}

func groupAggregate(operation recipe.ConstructionGroupAggregateOp, rows []Row, values []any) (Value, error) {
	switch operation {
	case recipe.ConstructionGroupCountRows:
		return Value{Data: float64(len(rows)), Present: true}, nil
	case recipe.ConstructionGroupCountNonNull:
		return Value{Data: float64(len(values)), Present: true}, nil
	case recipe.ConstructionGroupSum, recipe.ConstructionGroupMean:
		total := 0.0
		for _, v := range values {
			n, ok := numeric(v)
			if !ok {
				return Value{}, fmt.Errorf("GROUP numeric aggregate received %T", v)
			}
			total += n
		}
		if operation == recipe.ConstructionGroupMean && len(values) > 0 {
			total /= float64(len(values))
		}
		return Value{Data: total, Present: true}, nil
	case recipe.ConstructionGroupMin, recipe.ConstructionGroupMax:
		if len(values) == 0 {
			return Value{Present: true}, nil
		}
		n, ok := numeric(values[0])
		if !ok {
			return Value{}, fmt.Errorf("GROUP numeric aggregate received %T", values[0])
		}
		for _, v := range values[1:] {
			m, ok := numeric(v)
			if !ok {
				return Value{}, fmt.Errorf("GROUP numeric aggregate received %T", v)
			}
			if operation == recipe.ConstructionGroupMin && m < n || operation == recipe.ConstructionGroupMax && m > n {
				n = m
			}
		}
		return Value{Data: n, Present: true}, nil
	case recipe.ConstructionGroupCountDistinct:
		set := map[string]bool{}
		for _, v := range values {
			b, _ := json.Marshal(v)
			set[string(b)] = true
		}
		return Value{Data: float64(len(set)), Present: true}, nil
	default:
		return Value{}, UnsupportedError{Feature: "GROUP aggregate " + string(operation)}
	}
}

func canonicalKey(values []Value) (string, error) {
	raw := make([]any, len(values))
	for i, v := range values {
		if !v.Present {
			raw[i] = map[string]any{"missing": true}
		} else {
			raw[i] = v.Data
		}
	}
	encoded, err := json.Marshal(raw)
	return string(encoded), err
}

func cloneRow(row Row) Row {
	out := make(Row, len(row))
	for key, value := range row {
		out[key] = value
	}
	return out
}

func conformRows(rows []Row, columns []recipe.StageColumn) []Row {
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		projected := make(Row, len(columns))
		for _, column := range columns {
			projected[column.ID] = row[column.ID]
		}
		if document, ok := row[sourceDocumentKey]; ok {
			projected[sourceDocumentKey] = document
		}
		out = append(out, projected)
	}
	return out
}

func exposeRows(rows []Row, columns []recipe.StageColumn) []Row {
	result := make([]Row, 0, len(rows))
	for _, row := range rows {
		public := make(Row, len(columns))
		for _, column := range columns {
			public[column.Name] = row[column.ID]
		}
		result = append(result, public)
	}
	return result
}

func pathMany(value any, selector string) []any {
	selector = strings.TrimPrefix(selector, "root.")
	parts := strings.Split(strings.Trim(selector, "."), ".")
	return selectPath(value, parts)
}

func selectPath(value any, parts []string) []any {
	if len(parts) == 0 {
		return []any{value}
	}
	part := parts[0]
	if strings.HasSuffix(part, "[]") {
		key := strings.TrimSuffix(part, "[]")
		obj, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		array, ok := obj[key].([]any)
		if !ok {
			return nil
		}
		var out []any
		for _, item := range array {
			out = append(out, selectPath(item, parts[1:])...)
		}
		return out
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	child, ok := obj[part]
	if !ok {
		return nil
	}
	return selectPath(child, parts[1:])
}

func sourceDocument(row Row) (map[string]any, error) {
	value := row[sourceDocumentKey]
	document, ok := value.Data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("source document is missing")
	}
	return document, nil
}

func codedGroupRows(input []Row, operation recipe.ConstructionCodedGroup) ([]Row, error) {
	type group struct {
		coding  map[string]any
		sources map[string]bool
	}
	groups := map[string]*group{}
	missing := 0
	for _, row := range input {
		document, err := sourceDocument(row)
		if err != nil {
			return nil, err
		}
		codings := pathMany(document, operation.Source.CodingPath)
		if len(codings) == 0 {
			switch operation.MissingKeyPolicy.Normalized() {
			case recipe.ConstructionGroupMissingKeyExclude:
				continue
			case recipe.ConstructionGroupMissingKeyError:
				return nil, fmt.Errorf("CODED_GROUP source has no coding key")
			default:
				missing++
			}
			continue
		}
		for _, raw := range codings {
			coding, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			system, _ := coding["system"].(string)
			version, _ := coding["version"].(string)
			code, _ := coding["code"].(string)
			key := fmt.Sprintf("%q/%q/%q", system, version, code)
			g := groups[key]
			if g == nil {
				g = &group{coding: coding, sources: map[string]bool{}}
				groups[key] = g
			}
			sourceID, _ := document["id"].(string)
			if sourceID == "" {
				sourceID = fmt.Sprintf("source-%p", document)
			}
			g.sources[sourceID] = true
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]Row, 0, len(keys)+1)
	for _, key := range keys {
		g := groups[key]
		rows = append(rows, Row{operation.SystemOutputColumnID: optionalString(g.coding["system"]), operation.VersionOutputColumnID: optionalString(g.coding["version"]), operation.CodeOutputColumnID: optionalString(g.coding["code"]), operation.DistinctSourceCountOutputColumnID: Value{Data: float64(len(g.sources)), Present: true}})
	}
	if missing > 0 && operation.MissingKeyPolicy.Normalized() == recipe.ConstructionGroupMissingKeyGroup {
		rows = append(rows, Row{operation.SystemOutputColumnID: Value{Present: true}, operation.VersionOutputColumnID: Value{Present: true}, operation.CodeOutputColumnID: Value{Present: true}, operation.DistinctSourceCountOutputColumnID: Value{Data: float64(missing), Present: true}})
	}
	return rows, nil
}

func codedPivotRows(input []Row, operation recipe.ConstructionCodedPivot) ([]Row, error) {
	values := make([][]Value, len(operation.Categories))
	for _, row := range input {
		document, err := sourceDocument(row)
		if err != nil {
			return nil, err
		}
		owners := pathMany(document, operation.Source.OwningScope)
		if len(owners) == 0 {
			owners = []any{document}
		}
		for _, owner := range owners {
			codingPath := strings.TrimPrefix(operation.Source.KeyPath, strings.TrimSuffix(operation.Source.OwningScope, ".")+".")
			codings := pathMany(owner, codingPath)
			for _, raw := range codings {
				coding, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				system, _ := coding["system"].(string)
				code, _ := coding["code"].(string)
				for i, category := range operation.Categories {
					if category.System != system || category.Code != code {
						continue
					}
					valuePath := operation.Source.ValuePath
					selected := path(owner, valuePath)
					if !selected.Present || selected.Data == nil {
						continue
					}
					values[i] = append(values[i], selected)
				}
			}
		}
	}
	row := make(Row)
	for i, category := range operation.Categories {
		value, err := aggregatePivot(values[i], operation.DuplicatePolicy, operation.MissingCellPolicy)
		if err != nil {
			return nil, err
		}
		row[category.OutputColumnID] = value
	}
	return []Row{row}, nil
}

func optionalString(value any) Value {
	if value == nil {
		return Value{Present: true}
	}
	return Value{Data: value, Present: true}
}
