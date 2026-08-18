package published

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
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
	ClickHouse                 ClickHouseQueryer
	Catalog                    bundlepublication.BundleCatalog
	Logger                     *slog.Logger
	MaxPage                    int
	ActiveManifestResolver     publication.ActiveResolver
	ProjectStatusResolver      ProjectStatusResolver
	ReleaseExecutionResolver   ReleaseExecutionResolver
	FederationSnapshotResolver FederationSnapshotResolver
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
	RowID     string `json:"rowId"`
	SortValue any    `json:"sortValue,omitempty"`
}

func encodeCursor(rowID string, sortValue any) string {
	data, _ := json.Marshal(pageCursor{RowID: rowID, SortValue: sortValue})
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

func cursorPredicate(cursor *pageCursor, sort *Sort) (string, []any, error) {
	row := "toString(`__loom_row_id`) > ?"
	if sort == nil {
		return row, []any{cursor.RowID}, nil
	}
	if cursor.SortValue == nil {
		return "", nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidCursor, "")
	}
	operator := ">"
	if sort.Desc {
		operator = "<"
	}
	return fmt.Sprintf("(`%s` %s ? OR (`%s` = ? AND %s))", sort.Column, operator, sort.Column, row), []any{cursor.SortValue, cursor.SortValue, cursor.RowID}, nil
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
