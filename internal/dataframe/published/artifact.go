package published

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"reflect"
	"strings"
	"time"
)

const (
	artifactManifestVersion = 1
	defaultNullEncoding     = `\N`
	defaultArrayEncoding    = "json"
)

var (
	// ErrArtifactRowLimit means the row stream exceeded the caller's bound.
	ErrArtifactRowLimit = errors.New("artifact row limit exceeded")
	// ErrArtifactByteLimit means the archive exceeded the caller's byte bound.
	ErrArtifactByteLimit = errors.New("artifact byte limit exceeded")
)

// ArtifactIdentity is the immutable publication identity copied into the
// artifact manifest. All fields are required; a selector or current pointer
// is not a substitute for an execution identity.
type ArtifactIdentity struct {
	Project           string `json:"project"`
	DatasetGeneration string `json:"datasetGeneration"`
	ReceiptID         string `json:"receiptId"`
	ExecutionID       string `json:"executionId"`
	OutputID          string `json:"outputId"`
	RevisionID        string `json:"revisionId"`
}

// ArtifactColumn supplies the stable exported feature order and the schema
// description for data.csv. The encoder does not infer a second schema from
// row values.
type ArtifactColumn struct {
	Name        string `json:"name"`
	LogicalType string `json:"logicalType,omitempty"`
	Nullable    bool   `json:"nullable,omitempty"`
	Repeated    bool   `json:"repeated,omitempty"`
}

// ArtifactRequest contains the data and metadata members supplied by the
// lifecycle owner. Raw metadata must be valid JSON; it is compacted into a
// deterministic representation before writing.
type ArtifactRequest struct {
	Identity               ArtifactIdentity
	Columns                []ArtifactColumn
	SelectionMetadata      json.RawMessage
	InterpretationMetadata json.RawMessage
	Provenance             json.RawMessage
	Quality                json.RawMessage
	README                 string
	NullEncoding           string
	ArrayEncoding          string
	MaxRows                int64
	MaxBytes               int64
}

// ArtifactRowVisitor consumes one row without requiring the encoder to
// retain the dataset.
type ArtifactRowVisitor func(map[string]any) error

// ArtifactRowStream supplies rows to the artifact encoder. It should invoke
// the visitor once for each row and stop when the visitor returns an error.
type ArtifactRowStream func(ArtifactRowVisitor) error

// ArtifactMember records an uncompressed member's digest and byte size. The
// manifest contains every member except manifest.json itself.
type ArtifactMember struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// ArtifactResult is complete only when every member and the ZIP central
// directory were written successfully. On any error WriteArtifact returns a
// zero result, even though an io.Writer may contain an unusable prefix.
type ArtifactResult struct {
	Complete      bool
	ArchiveSHA256 string
	ArchiveBytes  int64
	Rows          int64
	Members       []ArtifactMember
	Identity      ArtifactIdentity
}

type artifactManifest struct {
	Version                int              `json:"version"`
	Identity               ArtifactIdentity `json:"identity"`
	SelectionMetadata      json.RawMessage  `json:"selection"`
	InterpretationMetadata json.RawMessage  `json:"interpretations"`
	Rows                   int64            `json:"rows"`
	Features               int              `json:"features"`
	NullEncoding           string           `json:"nullEncoding"`
	ArrayEncoding          string           `json:"arrayEncoding"`
	Members                []ArtifactMember `json:"members"`
}

type artifactSchema struct {
	Columns       []ArtifactColumn `json:"columns"`
	NullEncoding  string           `json:"nullEncoding"`
	ArrayEncoding string           `json:"arrayEncoding"`
}

// WriteArtifact writes the fixed, safe artifact members to out in a stable
// order. It uses ZIP Store entries so member bytes and the resulting archive
// digest are deterministic for the same input. The manifest is emitted last,
// after all other member checksums are final.
func WriteArtifact(ctx context.Context, out io.Writer, request ArtifactRequest, stream ArtifactRowStream) (ArtifactResult, error) {
	if ctx == nil {
		return ArtifactResult{}, fmt.Errorf("artifact context is required")
	}
	if out == nil {
		return ArtifactResult{}, fmt.Errorf("artifact writer is required")
	}
	if stream == nil {
		return ArtifactResult{}, fmt.Errorf("artifact row stream is required")
	}
	if err := validateArtifactRequest(&request); err != nil {
		return ArtifactResult{}, err
	}
	selection, err := canonicalArtifactJSON(request.SelectionMetadata)
	if err != nil {
		return ArtifactResult{}, fmt.Errorf("selection metadata: %w", err)
	}
	interpretations, err := canonicalArtifactJSON(request.InterpretationMetadata)
	if err != nil {
		return ArtifactResult{}, fmt.Errorf("interpretation metadata: %w", err)
	}
	provenance, err := canonicalArtifactJSON(request.Provenance)
	if err != nil {
		return ArtifactResult{}, fmt.Errorf("provenance metadata: %w", err)
	}
	quality, err := canonicalArtifactJSON(request.Quality)
	if err != nil {
		return ArtifactResult{}, fmt.Errorf("quality metadata: %w", err)
	}

	archiveOut := &artifactArchiveWriter{ctx: ctx, out: out, maxBytes: request.MaxBytes, digest: sha256.New()}
	archive := zip.NewWriter(archiveOut)
	closeArchive := func() error {
		if err := archive.Close(); err != nil {
			return err
		}
		return nil
	}
	members := make([]ArtifactMember, 0, 5)
	dataColumns := append([]ArtifactColumn(nil), request.Columns...)
	var rowCount int64
	writeMember := func(name string, write func(io.Writer) error) error {
		member, err := writeArtifactMember(ctx, archive, name, write)
		if err != nil {
			return err
		}
		members = append(members, member)
		return nil
	}

	if err := writeMember("data.csv", func(writer io.Writer) error {
		if err := writeArtifactCSVLine(writer, request.NullEncoding, stringArtifactValues(columnNames(dataColumns)), false); err != nil {
			return err
		}
		return stream(func(row map[string]any) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if request.MaxRows > 0 && rowCount >= request.MaxRows {
				return ErrArtifactRowLimit
			}
			values, err := artifactRowValues(row, dataColumns)
			if err != nil {
				return err
			}
			if err := writeArtifactCSVLine(writer, request.NullEncoding, values, true); err != nil {
				return err
			}
			rowCount++
			return nil
		})
	}); err != nil {
		return ArtifactResult{}, err
	}

	schemaBytes, err := json.Marshal(artifactSchema{Columns: dataColumns, NullEncoding: request.NullEncoding, ArrayEncoding: request.ArrayEncoding})
	if err != nil {
		return ArtifactResult{}, err
	}
	if err := writeMember("schema.json", writeBytes(schemaBytes)); err != nil {
		return ArtifactResult{}, err
	}
	if err := writeMember("provenance.json", writeBytes(provenance)); err != nil {
		return ArtifactResult{}, err
	}
	if err := writeMember("quality.json", writeBytes(quality)); err != nil {
		return ArtifactResult{}, err
	}
	readme := request.README
	if readme == "" {
		readme = defaultArtifactREADME(request)
	}
	if err := writeMember("README.md", writeBytes([]byte(readme))); err != nil {
		return ArtifactResult{}, err
	}

	manifestBytes, err := json.Marshal(artifactManifest{
		Version:                artifactManifestVersion,
		Identity:               request.Identity,
		SelectionMetadata:      selection,
		InterpretationMetadata: interpretations,
		Rows:                   rowCount,
		Features:               len(dataColumns),
		NullEncoding:           request.NullEncoding,
		ArrayEncoding:          request.ArrayEncoding,
		Members:                members,
	})
	if err != nil {
		return ArtifactResult{}, err
	}
	if err := writeArtifactMemberBytes(ctx, archive, "manifest.json", manifestBytes); err != nil {
		return ArtifactResult{}, err
	}
	if err := closeArchive(); err != nil {
		return ArtifactResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ArtifactResult{}, err
	}
	return ArtifactResult{
		Complete:      true,
		ArchiveSHA256: hex.EncodeToString(archiveOut.digest.Sum(nil)),
		ArchiveBytes:  archiveOut.bytes,
		Rows:          rowCount,
		Members:       append([]ArtifactMember(nil), members...),
		Identity:      request.Identity,
	}, nil
}

func validateArtifactRequest(request *ArtifactRequest) error {
	if request == nil {
		return fmt.Errorf("artifact request is required")
	}
	identity := request.Identity
	for name, value := range map[string]string{
		"project": identity.Project, "dataset generation": identity.DatasetGeneration,
		"receipt": identity.ReceiptID, "execution": identity.ExecutionID,
		"output": identity.OutputID, "revision": identity.RevisionID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("artifact %s identity is required", name)
		}
	}
	if len(request.Columns) == 0 {
		return fmt.Errorf("artifact schema requires at least one column")
	}
	seen := make(map[string]struct{}, len(request.Columns))
	for _, column := range request.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" {
			return fmt.Errorf("artifact column name is required")
		}
		if name == authResourcePathColumn || strings.HasPrefix(name, "__loom_") {
			return fmt.Errorf("artifact column %q is internal", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("artifact column %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	request.NullEncoding = strings.TrimSpace(request.NullEncoding)
	if request.NullEncoding == "" {
		request.NullEncoding = defaultNullEncoding
	}
	if strings.ContainsAny(request.NullEncoding, ",\"\r\n") {
		return fmt.Errorf("artifact null encoding must be one unquoted CSV field")
	}
	request.ArrayEncoding = strings.ToLower(strings.TrimSpace(request.ArrayEncoding))
	if request.ArrayEncoding == "" {
		request.ArrayEncoding = defaultArrayEncoding
	}
	if request.ArrayEncoding != defaultArrayEncoding {
		return fmt.Errorf("unsupported artifact array encoding %q", request.ArrayEncoding)
	}
	if request.MaxRows < 0 || request.MaxBytes < 0 {
		return fmt.Errorf("artifact limits must not be negative")
	}
	return nil
}

func canonicalArtifactJSON(raw json.RawMessage) ([]byte, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return []byte(`{}`), nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func columnNames(columns []ArtifactColumn) []string {
	result := make([]string, len(columns))
	for i, column := range columns {
		result[i] = column.Name
	}
	return result
}

type artifactCSVValue struct {
	Value string
	Null  bool
}

func artifactRowValues(row map[string]any, columns []ArtifactColumn) ([]artifactCSVValue, error) {
	result := make([]artifactCSVValue, len(columns))
	for i, column := range columns {
		value, ok := row[column.Name]
		if !ok || isNilArtifactValue(value) {
			result[i].Null = true
			continue
		}
		encoded, err := artifactValueString(value)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", column.Name, err)
		}
		result[i].Value = encoded
	}
	return result, nil
}

func isNilArtifactValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func artifactValueString(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	if bytes, err := json.Marshal(value); err != nil {
		return "", err
	} else {
		if len(bytes) > 0 && bytes[0] == '"' {
			var text string
			if err := json.Unmarshal(bytes, &text); err == nil {
				return text, nil
			}
		}
		return string(bytes), nil
	}
}

func stringArtifactValues(values []string) []artifactCSVValue {
	result := make([]artifactCSVValue, len(values))
	for i, value := range values {
		result[i].Value = value
	}
	return result
}

func writeArtifactCSVLine(writer io.Writer, nullEncoding string, values []artifactCSVValue, row bool) error {
	for i, value := range values {
		if i > 0 {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		if value.Null {
			if _, err := io.WriteString(writer, nullEncoding); err != nil {
				return err
			}
			continue
		}
		forceQuote := row && (value.Value == "" || value.Value == nullEncoding)
		if _, err := io.WriteString(writer, artifactCSVCell(value.Value, forceQuote)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "\n")
	return err
}

func artifactCSVCell(value string, forceQuote bool) string {
	if !forceQuote && !strings.ContainsAny(value, ",\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func writeBytes(value []byte) func(io.Writer) error {
	return func(writer io.Writer) error {
		_, err := writer.Write(value)
		return err
	}
}

func defaultArtifactREADME(request ArtifactRequest) string {
	return fmt.Sprintf("# Loom dataset artifact\n\nExecution `%s`, output `%s`, revision `%s`.\n\nNull values are encoded as the unquoted `%s` field. Empty strings are quoted as `\"\"`. Arrays use deterministic JSON encoding.\n", request.Identity.ExecutionID, request.Identity.OutputID, request.Identity.RevisionID, request.NullEncoding)
}

func writeArtifactMember(ctx context.Context, archive *zip.Writer, name string, write func(io.Writer) error) (ArtifactMember, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetModTime(time.Unix(0, 0).UTC())
	header.SetMode(0644)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return ArtifactMember{}, err
	}
	digest := sha256.New()
	member := &artifactMemberWriter{out: entry, digest: digest}
	if err := write(&artifactContextWriter{ctx: ctx, out: member}); err != nil {
		return ArtifactMember{}, err
	}
	return ArtifactMember{Name: name, SHA256: hex.EncodeToString(digest.Sum(nil)), Bytes: member.bytes}, nil
}

func writeArtifactMemberBytes(ctx context.Context, archive *zip.Writer, name string, value []byte) error {
	_, err := writeArtifactMember(ctx, archive, name, writeBytes(value))
	return err
}

type artifactMemberWriter struct {
	out    io.Writer
	digest hash.Hash
	bytes  int64
}

func (w *artifactMemberWriter) Write(value []byte) (int, error) {
	n, err := w.out.Write(value)
	if n > 0 {
		_, _ = w.digest.Write(value[:n])
		w.bytes += int64(n)
	}
	return n, err
}

type artifactContextWriter struct {
	ctx context.Context
	out io.Writer
}

func (w *artifactContextWriter) Write(value []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.Write(value)
}

type artifactArchiveWriter struct {
	ctx      context.Context
	out      io.Writer
	maxBytes int64
	digest   hash.Hash
	bytes    int64
}

func (w *artifactArchiveWriter) Write(value []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.maxBytes > 0 && w.bytes+int64(len(value)) > w.maxBytes {
		return 0, ErrArtifactByteLimit
	}
	n, err := w.out.Write(value)
	if n > 0 {
		_, _ = w.digest.Write(value[:n])
		w.bytes += int64(n)
	}
	if err == nil && n != len(value) {
		return n, io.ErrShortWrite
	}
	return n, err
}
