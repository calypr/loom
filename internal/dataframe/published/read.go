package published

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
)

type ClickHouseQueryer interface {
	QueryRowsArgs(context.Context, string, []string, ...any) ([]map[string]any, error)
	QueryRowsArgsVisit(context.Context, string, []string, func(map[string]any) error, ...any) error
}

type Reader struct {
	ClickHouse             ClickHouseQueryer
	Catalog                bundlepublication.BundleCatalog
	Logger                 *slog.Logger
	MaxPage                int
	ActiveManifestResolver publication.ActiveResolver
}

type Filter struct {
	Column string
	Op     string
	Value  any
}

type Sort struct {
	Column string
	Desc   bool
}

type Page struct {
	Materialization Materialization
	Columns         []string
	Rows            []map[string]any
	TotalCount      int64
	HasNext         bool
	NextCursor      string
}

type AggregateResult struct {
	Materialization Materialization
	Columns         []string
	Rows            []map[string]any
}

func numericCount(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("ClickHouse count returned unsupported value %T", value)
	}
}

func buildWhere(filters []Filter, allowed map[string]struct{}) ([]string, []any, error) {
	where := make([]string, 0, len(filters))
	args := make([]any, 0, len(filters))
	for _, filter := range filters {
		if _, ok := allowed[filter.Column]; !ok {
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidFilter, "")
		}
		switch strings.ToUpper(filter.Op) {
		case "EQ":
			where = append(where, fmt.Sprintf("`%s` = ?", filter.Column))
			args = append(args, filter.Value)
		case "NEQ":
			where = append(where, fmt.Sprintf("`%s` != ?", filter.Column))
			args = append(args, filter.Value)
		case "IN", "NOT_IN":
			if emptyFilterCollection(filter.Value) {
				if strings.EqualFold(filter.Op, "IN") {
					where = append(where, "0")
				} else {
					where = append(where, "1")
				}
				continue
			}
			op := "IN"
			if strings.EqualFold(filter.Op, "NOT_IN") {
				op = "NOT IN"
			}
			where = append(where, fmt.Sprintf("`%s` %s ?", filter.Column, op))
			args = append(args, filter.Value)
		case "LT", "LTE", "GT", "GTE":
			op := map[string]string{"LT": "<", "LTE": "<=", "GT": ">", "GTE": ">="}[strings.ToUpper(filter.Op)]
			where = append(where, fmt.Sprintf("`%s` %s ?", filter.Column, op))
			args = append(args, filter.Value)
		case "CONTAINS":
			where = append(where, fmt.Sprintf("positionCaseInsensitive(toString(`%s`), ?) > 0", filter.Column))
			args = append(args, filter.Value)
		case "STARTS_WITH":
			where = append(where, fmt.Sprintf("startsWith(toString(`%s`), ?)", filter.Column))
			args = append(args, filter.Value)
		case "EXISTS":
			where = append(where, fmt.Sprintf("isNotNull(`%s`)", filter.Column))
		case "IS_NULL":
			where = append(where, fmt.Sprintf("isNull(`%s`)", filter.Column))
		case "ARRAY_CONTAINS":
			where = append(where, fmt.Sprintf("has(`%s`, ?)", filter.Column))
			args = append(args, filter.Value)
		case "ARRAY_OVERLAPS":
			where = append(where, fmt.Sprintf("hasAny(`%s`, ?)", filter.Column))
			args = append(args, filter.Value)
		default:
			return nil, nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidFilter, "")
		}
	}
	return where, args, nil
}

func emptyFilterCollection(value any) bool {
	switch typed := value.(type) {
	case []string:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	case nil:
		return true
	default:
		return false
	}
}

type pageCursor struct {
	Version   int    `json:"version,omitempty"`
	Binding   string `json:"binding,omitempty"`
	RowID     string `json:"rowId"`
	SortValue any    `json:"sortValue,omitempty"`
}

func encodeCursor(rowID string, sortValue any) string {
	return encodeBoundCursor(rowID, sortValue, "")
}

func encodeBoundCursor(rowID string, sortValue any, binding string) string {
	data, _ := json.Marshal(pageCursor{Version: 1, Binding: binding, RowID: rowID, SortValue: sortValue})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(cursor string) (*pageCursor, error) {
	if cursor == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidCursor, "")
	}
	var value pageCursor
	if err := json.Unmarshal(data, &value); err != nil || value.RowID == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
	}
	return &value, nil
}

type cursorFilter struct {
	Column string          `json:"column"`
	Op     string          `json:"op"`
	Value  json.RawMessage `json:"value"`
}

type cursorSort struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

type cursorBinding struct {
	Version           int            `json:"version"`
	Project           string         `json:"project"`
	DatasetGeneration string         `json:"datasetGeneration"`
	Revision          string         `json:"revision"`
	Selector          string         `json:"selector"`
	PhysicalTable     string         `json:"physicalTable"`
	Columns           []string       `json:"columns"`
	Sort              *cursorSort    `json:"sort,omitempty"`
	Filters           []cursorFilter `json:"filters"`
}

func cursorFingerprint(materialization Materialization, req PageRequest) (string, error) {
	revision := materialization.Revision
	if revision == "" {
		revision = materialization.ID
	}
	selector := ""
	if materialization.Selector.Valid() {
		selector = materialization.Selector.Key()
	}
	filters := make([]cursorFilter, 0, len(req.Filters))
	for _, filter := range req.Filters {
		value, err := json.Marshal(filter.Value)
		if err != nil {
			return "", invalidCursor()
		}
		filters = append(filters, cursorFilter{Column: strings.TrimSpace(filter.Column), Op: strings.ToUpper(strings.TrimSpace(filter.Op)), Value: value})
	}
	sort.SliceStable(filters, func(i, j int) bool {
		if filters[i].Column != filters[j].Column {
			return filters[i].Column < filters[j].Column
		}
		if filters[i].Op != filters[j].Op {
			return filters[i].Op < filters[j].Op
		}
		return string(filters[i].Value) < string(filters[j].Value)
	})
	var sortBy *cursorSort
	if req.Sort != nil {
		sortBy = &cursorSort{Column: strings.TrimSpace(req.Sort.Column), Desc: req.Sort.Desc}
	}
	payload, err := json.Marshal(cursorBinding{
		Version: 1, Project: materialization.Project, DatasetGeneration: materialization.DatasetGeneration,
		Revision: revision, Selector: selector, PhysicalTable: materialization.PhysicalTable,
		Columns: append([]string(nil), req.Columns...), Sort: sortBy, Filters: filters,
	})
	if err != nil {
		return "", invalidCursor()
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func validateReaderColumns(columns []string, allowed map[string]struct{}) error {
	for _, column := range columns {
		if column == "__loom_row_id" || column == "__loom_total" {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
		if _, ok := allowed[column]; !ok {
			return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
		}
	}
	return nil
}

func quotedColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = fmt.Sprintf("`%s`", column)
	}
	return strings.Join(quoted, ", ")
}
