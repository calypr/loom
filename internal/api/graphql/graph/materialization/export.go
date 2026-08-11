package materializationapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

// ExportDataframe streams a principal-scoped published dataframe through the
// same federated reader contract used by interactive rows.
func (s *Service) ExportDataframe(ctx context.Context, request dfmaterialization.ExportRequest, out io.Writer) error {
	if s.reader == nil {
		return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	if out == nil {
		return dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return err
	}
	if request.MaterializationID != "" {
		value, err := s.reader.DatasetByPublishedID(ctx, request.MaterializationID)
		if err != nil {
			return mapReaderError(err)
		}
		if err := s.authorizePublished(ctx, value); err != nil {
			return err
		}
		format := request.Format.Normalize()
		if format != dfmaterialization.ExportCSV && format != dfmaterialization.ExportTSV && format != dfmaterialization.ExportJSON && format != dfmaterialization.ExportJSONL {
			return dataframeerrors.NewError(dataframeerrors.CodeUnsupportedExportFormat, "")
		}
		columns := append([]string(nil), request.Columns...)
		if len(columns) == 0 {
			for _, column := range value.Columns {
				if column.Name != "auth_resource_path" && column.Name != "__loom_row_id" {
					columns = append(columns, column.Name)
				}
			}
		}
		writer := &exportCountingWriter{out: out, maxRows: s.maxExportRows, maxBytes: s.maxExportBytes}
		state := &exportWriter{format: format, columns: columns, out: writer}
		if err := state.begin(); err != nil {
			return err
		}
		if err := s.reader.StreamPublishedID(ctx, value.ID, dfmaterialization.FederatedStreamRequest{Columns: columns, Filters: request.Filters, Sort: request.Sort}, state.visit); err != nil {
			return err
		}
		return state.end()
	}
	selector, err := s.resolvePublishedSelector(request.Selector, request.DataType)
	if err != nil {
		return err
	}
	dataset, access, err := s.authorizedFederation(ctx, principal, selector, request.Filters)
	if err != nil {
		return err
	}
	if len(dataset.Sources) == 0 {
		return dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	format := request.Format.Normalize()
	if format != dfmaterialization.ExportCSV && format != dfmaterialization.ExportTSV && format != dfmaterialization.ExportJSON && format != dfmaterialization.ExportJSONL {
		return dataframeerrors.NewError(dataframeerrors.CodeUnsupportedExportFormat, "")
	}
	columns := append([]string(nil), request.Columns...)
	if len(columns) == 0 {
		columns = make([]string, 0, len(dataset.Columns))
		for _, column := range dataset.Columns {
			if column.Name == "auth_resource_path" || column.Name == "__loom_row_id" {
				continue
			}
			columns = append(columns, column.Name)
		}
	}
	writer := &exportCountingWriter{out: out, maxRows: s.maxExportRows, maxBytes: s.maxExportBytes}
	state := &exportWriter{format: format, columns: columns, out: writer}
	if err := state.begin(); err != nil {
		return err
	}
	_, err = s.reader.StreamFederatedDataset(ctx, dataset, dfmaterialization.FederatedStreamRequest{
		Columns: columns, Filters: request.Filters, Sort: request.Sort,
		AccessByProject: access,
	}, state.visit)
	if err != nil {
		return err
	}
	if err := state.end(); err != nil {
		return err
	}
	return nil
}

type exportCountingWriter struct {
	out      io.Writer
	rows     int64
	bytes    int64
	maxRows  int64
	maxBytes int64
}

func (w *exportCountingWriter) Write(value []byte) (int, error) {
	if w.maxBytes > 0 && w.bytes+int64(len(value)) > w.maxBytes {
		return 0, dataframeerrors.NewError(dataframeerrors.CodeExportLimitExceeded, "")
	}
	n, err := w.out.Write(value)
	w.bytes += int64(n)
	return n, err
}

type exportWriter struct {
	format  dfmaterialization.ExportFormat
	columns []string
	out     *exportCountingWriter
	csv     *csv.Writer
	started bool
	first   bool
}

func (w *exportWriter) begin() error {
	w.first = true
	switch w.format {
	case dfmaterialization.ExportCSV, dfmaterialization.ExportTSV:
		w.csv = csv.NewWriter(w.out)
		if w.format == dfmaterialization.ExportTSV {
			w.csv.Comma = '\t'
		}
		if err := w.csv.Write(w.columns); err != nil {
			return err
		}
		w.csv.Flush()
		return w.csv.Error()
	case dfmaterialization.ExportJSON:
		_, err := io.WriteString(w.out, "[")
		return err
	default:
		return nil
	}
}

func (w *exportWriter) visit(row map[string]any) error {
	if w.out.maxRows > 0 && w.out.rows >= w.out.maxRows {
		return dataframeerrors.NewError(dataframeerrors.CodeExportLimitExceeded, "")
	}
	w.out.rows++
	switch w.format {
	case dfmaterialization.ExportCSV, dfmaterialization.ExportTSV:
		values := make([]string, len(w.columns))
		for i, column := range w.columns {
			value, err := exportScalar(row[column])
			if err != nil {
				return err
			}
			values[i] = value
		}
		w.csv.Write(values)
		w.csv.Flush()
		return w.csv.Error()
	case dfmaterialization.ExportJSON:
		if !w.first {
			if _, err := io.WriteString(w.out, ","); err != nil {
				return err
			}
		}
		w.first = false
		return json.NewEncoder(w.out).Encode(rowWithoutInternal(row))
	case dfmaterialization.ExportJSONL:
		return json.NewEncoder(w.out).Encode(rowWithoutInternal(row))
	default:
		return dataframeerrors.NewError(dataframeerrors.CodeUnsupportedExportFormat, "")
	}
}

func (w *exportWriter) end() error {
	if w.csv != nil {
		w.csv.Flush()
		if err := w.csv.Error(); err != nil {
			return err
		}
	}
	if w.format == dfmaterialization.ExportJSON {
		_, err := io.WriteString(w.out, "]")
		return err
	}
	return nil
}

func rowWithoutInternal(row map[string]any) map[string]any {
	result := make(map[string]any, len(row))
	for key, value := range row {
		if strings.HasPrefix(key, "__loom_") {
			continue
		}
		result[key] = value
	}
	return result
}

func exportScalar(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	if bytes, err := json.Marshal(value); err == nil {
		if len(bytes) > 0 && bytes[0] == '"' {
			var text string
			if json.Unmarshal(bytes, &text) == nil {
				return text, nil
			}
		}
		return string(bytes), nil
	}
	return "", dataframeerrors.NewError(dataframeerrors.CodeOutputEncodingFailed, "")
}
