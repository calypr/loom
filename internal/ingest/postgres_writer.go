package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/backends/postgres/control"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/source"
)

var ErrInvalidPGResourceWrite = errors.New("ingest: invalid postgres resource write")

type PGResourceWriteScopeError struct {
	BatchID string
	Field   string
	Reason  string
}

func (e *PGResourceWriteScopeError) Error() string {
	return fmt.Sprintf("postgres resource write refused: batch %q has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Field, e.Reason)
}

func (e *PGResourceWriteScopeError) Unwrap() error { return ErrInvalidPGResourceWrite }

type PGResourceRowIdentityError struct {
	BatchID string
	Row     int
	Column  string
	Reason  string
}

func (e *PGResourceRowIdentityError) Error() string {
	return fmt.Sprintf("postgres resource write refused: batch %q row %d has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Row, e.Column, e.Reason)
}

func (e *PGResourceRowIdentityError) Unwrap() error { return ErrInvalidPGResourceWrite }

type PGResourceBatchScope struct {
	BatchID           string
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
}

type PGResourceWriteResult struct {
	BatchID          string
	Digest           string
	Offered          int
	Committed        int64
	SkippedIdentical int64
	Replay           bool
}

type resourceBatchCommitter interface {
	WriteResourceBatch(ctx context.Context, batch control.ResourceBatch) (control.ResourceBatchResult, error)
}

type PostgresResourceWriter struct {
	committer resourceBatchCommitter
}

func NewPostgresResourceWriter(src control.ConnSource, schema string) (*PostgresResourceWriter, error) {
	writer, err := control.NewResourceWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &PostgresResourceWriter{committer: writer}, nil
}

func (w *PostgresResourceWriter) WriteResourceBatch(
	ctx context.Context,
	scope PGResourceBatchScope,
	rows []rowBuildResult,
) (PGResourceWriteResult, error) {
	if strings.TrimSpace(scope.BatchID) == "" {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id", Reason: "it is blank",
		}
	}
	if len(scope.BatchID) > control.MaxBatchIDLength {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id",
			Reason: fmt.Sprintf("it is %d bytes, want at most %d", len(scope.BatchID), control.MaxBatchIDLength),
		}
	}
	if strings.TrimSpace(scope.Project) == "" {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "project", Reason: "it is blank",
		}
	}
	if len(rows) == 0 {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "rows", Reason: "the batch carries no rows",
		}
	}
	if len(rows) > control.MaxBatchResources {
		return PGResourceWriteResult{}, &PGResourceWriteScopeError{
			BatchID: scope.BatchID, Field: "rows",
			Reason: fmt.Sprintf("the batch offers %d rows, want at most %d",
				len(rows), control.MaxBatchResources),
		}
	}

	decoded := make([]source.Resource, 0, len(rows))
	for index, built := range rows {
		record, err := decodeBuiltResourceVertex(built.vertex, scope, index)
		if err != nil {
			return PGResourceWriteResult{}, err
		}
		decoded = append(decoded, record)
	}

	result, err := w.committer.WriteResourceBatch(ctx, control.ResourceBatch{
		ID:                scope.BatchID,
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		Rows:              decoded,
	})
	if err != nil {
		return PGResourceWriteResult{}, err
	}
	return PGResourceWriteResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Offered:          len(rows),
		Committed:        result.Committed,
		SkippedIdentical: result.SkippedIdentical,
		Replay:           result.Replay,
	}, nil
}

func decodeBuiltResourceVertex(
	vertex json.RawMessage,
	scope PGResourceBatchScope,
	index int,
) (source.Resource, error) {
	refuse := func(column string, format string, args ...any) (source.Resource, error) {
		return source.Resource{}, &PGResourceRowIdentityError{
			BatchID: scope.BatchID, Row: index, Column: column,
			Reason: fmt.Sprintf(format, args...),
		}
	}
	invalid := func(column string, err error) (source.Resource, error) {
		return refuse(column, "%v", err)
	}

	document, err := decodeTopLevelDocument(vertex)
	if err != nil {
		return refuse("vertex", "the built document is undecodable: %v", err)
	}

	record := source.Resource{
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		AuthResourcePath:  scope.AuthResourcePath,
	}

	project, err := requiredDocumentString(document, "project")
	if err != nil {
		return invalid("project", err)
	}
	if project != scope.Project {
		return refuse("project", "vertex project %q does not match the batch project %q", project, scope.Project)
	}
	projectID, err := requiredDocumentString(document, "project_id")
	if err != nil {
		return invalid("project_id", err)
	}
	if projectID != scope.Project {
		return refuse("project_id", "vertex project_id %q does not match the batch project %q", projectID, scope.Project)
	}

	generation, stamped, err := optionalDocumentString(document, generationIdentityField)
	if err != nil {
		return invalid(generationIdentityField, err)
	}
	if scope.DatasetGeneration != "" {
		if !stamped {
			return refuse(generationIdentityField,
				"the row carries no dataset_generation stamp but the batch commits into generation %q",
				scope.DatasetGeneration)
		}
		if generation != scope.DatasetGeneration {
			return refuse(generationIdentityField, "vertex generation %q does not match the batch generation %q",
				generation, scope.DatasetGeneration)
		}
	} else if stamped {
		return refuse(generationIdentityField,
			"the row is stamped for generation %q but the batch is the legacy (unversioned) namespace", generation)
	}

	resourceType, err := requiredDocumentString(document, "resourceType")
	if err != nil {
		return invalid("resourceType", err)
	}
	record.ResourceType = resourceType

	logicalID, err := requiredDocumentString(document, "id")
	if err != nil {
		return invalid("id", err)
	}
	record.LogicalID = logicalID

	authPath, authPresent, err := optionalDocumentString(document, "auth_resource_path")
	if err != nil {
		return invalid("auth_resource_path", err)
	}
	if !authPresent {
		if scope.AuthResourcePath != "" {
			return refuse("auth_resource_path",
				"the row carries no auth_resource_path but the batch is scoped to %q", scope.AuthResourcePath)
		}
	} else if authPath != scope.AuthResourcePath {
		return refuse("auth_resource_path", "vertex auth_resource_path %q does not match the batch auth path %q",
			authPath, scope.AuthResourcePath)
	}

	if rawPayload, present := document["payload"]; present {
		canonical, err := canonicalPayload(rawPayload)
		if err != nil {
			return invalid("payload", fmt.Errorf("the built payload document is undecodable: %w", err))
		}
		record.Payload = canonical
	}
	return record, nil
}

func canonicalPayload(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected content after the JSON document: %v", err)
	}
	return json.Marshal(document)
}

func optionalDocumentString(document map[string]json.RawMessage, field string) (string, bool, error) {
	raw, present := document[field]
	if !present {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, fmt.Errorf("%s must be a string", field)
	}
	return value, true, nil
}

var ErrInvalidPGEdgeWrite = errors.New("ingest: invalid postgres edge write")

type PGEdgeWriteScopeError struct {
	BatchID string
	Field   string
	Reason  string
}

func (e *PGEdgeWriteScopeError) Error() string {
	return fmt.Sprintf("postgres edge write refused: batch %q has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Field, e.Reason)
}

func (e *PGEdgeWriteScopeError) Unwrap() error { return ErrInvalidPGEdgeWrite }

type PGEdgeRowIdentityError struct {
	BatchID  string
	Row      int
	Category rowErrorType
	Column   string
	Reason   string
}

func (e *PGEdgeRowIdentityError) Error() string {
	return fmt.Sprintf("postgres edge write refused: batch %q row %d (%s failure) has an invalid %s: %s; "+
		"nothing was written", e.BatchID, e.Row, e.Category, e.Column, e.Reason)
}

func (e *PGEdgeRowIdentityError) Unwrap() error { return ErrInvalidPGEdgeWrite }

type PGEdgeBatchScope struct {
	BatchID           string
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
}

type PGEdgeWriteResult struct {
	BatchID          string
	Digest           string
	Rows             int
	Offered          int
	Committed        int64
	SkippedIdentical int64
	Replay           bool
}

type edgeBatchCommitter interface {
	WriteEdgeBatch(ctx context.Context, batch control.EdgeBatch) (control.EdgeBatchResult, error)
}

type PostgresEdgeWriter struct {
	committer edgeBatchCommitter
}

func NewPostgresEdgeWriter(src control.ConnSource, schema string) (*PostgresEdgeWriter, error) {
	writer, err := control.NewEdgeWriter(src, schema)
	if err != nil {
		return nil, err
	}
	return &PostgresEdgeWriter{committer: writer}, nil
}

func (w *PostgresEdgeWriter) WriteEdgeBatch(
	ctx context.Context,
	scope PGEdgeBatchScope,
	rows []rowBuildResult,
) (PGEdgeWriteResult, error) {
	if strings.TrimSpace(scope.BatchID) == "" {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id", Reason: "it is blank",
		}
	}
	if len(scope.BatchID) > control.MaxBatchIDLength {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "batch id",
			Reason: fmt.Sprintf("it is %d bytes, want at most %d", len(scope.BatchID), control.MaxBatchIDLength),
		}
	}
	if strings.TrimSpace(scope.Project) == "" {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "project", Reason: "it is blank",
		}
	}
	if len(rows) == 0 {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "rows", Reason: "the batch carries no rows",
		}
	}

	offered := make([]control.EdgeOccurrenceRow, 0, len(rows))
	for index, built := range rows {
		converted, err := convertBuiltEdgeOccurrences(built, scope, index)
		if err != nil {
			return PGEdgeWriteResult{}, err
		}
		offered = append(offered, converted...)
	}
	if len(offered) == 0 {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "rows",
			Reason: "the batch's rows carry no built edge occurrences",
		}
	}
	if len(offered) > control.MaxBatchEdges {
		return PGEdgeWriteResult{}, &PGEdgeWriteScopeError{
			BatchID: scope.BatchID, Field: "rows",
			Reason: fmt.Sprintf("the batch offers %d edge occurrences, want at most %d",
				len(offered), control.MaxBatchEdges),
		}
	}

	result, err := w.committer.WriteEdgeBatch(ctx, control.EdgeBatch{
		ID:                scope.BatchID,
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		Rows:              offered,
	})
	if err != nil {
		return PGEdgeWriteResult{}, err
	}
	return PGEdgeWriteResult{
		BatchID:          result.BatchID,
		Digest:           result.Digest,
		Rows:             len(rows),
		Offered:          len(offered),
		Committed:        result.Committed,
		SkippedIdentical: result.SkippedIdentical,
		Replay:           result.Replay,
	}, nil
}

type pgEdgeDocument struct {
	fromType, fromID  string
	toType, toID      string
	label             string
	project           string
	authResourcePath  string
	authPresent       bool
	datasetGeneration string
	stamped           bool
	key               string
	logicalEdgeKey    *string
}

func convertBuiltEdgeOccurrences(
	built rowBuildResult,
	scope PGEdgeBatchScope,
	index int,
) ([]control.EdgeOccurrenceRow, error) {
	refuse := func(category rowErrorType, column string, format string, args ...any) ([]control.EdgeOccurrenceRow, error) {
		return nil, &PGEdgeRowIdentityError{
			BatchID: scope.BatchID, Row: index, Category: category, Column: column,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	if _, err := decodeBuiltResourceVertex(built.vertex, PGResourceBatchScope{
		BatchID:           scope.BatchID,
		Project:           scope.Project,
		DatasetGeneration: scope.DatasetGeneration,
		AuthResourcePath:  scope.AuthResourcePath,
	}, index); err != nil {
		var rowErr *PGResourceRowIdentityError
		if errors.As(err, &rowErr) {
			category := rowErrorGeneration
			return refuse(category, rowErr.Column, "%s", rowErr.Reason)
		}
		return refuse(rowErrorGeneration, "vertex", "%v", err)
	}

	document, err := decodeTopLevelDocument(built.vertex)
	if err != nil {
		return refuse(rowErrorGeneration, "vertex", "the built document is undecodable: %v", err)
	}
	resourceType, err := requiredDocumentString(document, "resourceType")
	if err != nil {
		return refuse(rowErrorGeneration, "resourceType", "%v", err)
	}
	logicalID, err := requiredDocumentString(document, "id")
	if err != nil {
		return refuse(rowErrorGeneration, "id", "%v", err)
	}
	vertexKey, err := requiredDocumentString(document, "_key")
	if err != nil {
		return refuse(rowErrorGeneration, "_key", "%v", err)
	}
	if len(built.edges) == 0 {
		return nil, nil
	}

	var positions []pgReferencePosition
	if rawPayload, present := document["payload"]; present {
		canonical, err := canonicalPayload(rawPayload)
		if err != nil {
			return refuse(rowErrorGeneration, "payload", "the built payload document is undecodable: %v", err)
		}
		positions, err = scanPayloadReferencePositions(canonical)
		if err != nil {
			return refuse(rowErrorGeneration, "payload", "%v", err)
		}
	}

	ownEndpoint := SanitizeKey(resourceType) + "/" + vertexKey

	groupOrder := make([]pgEdgeIdentity, 0, len(built.edges))
	groups := make(map[pgEdgeIdentity][]*pgEdgeDocument, len(built.edges))
	for edgeIndex, rawEdge := range built.edges {
		doc, err := decodeBuiltEdgeDocument(rawEdge, scope, index, edgeIndex)
		if err != nil {
			var rowErr *PGEdgeRowIdentityError
			if errors.As(err, &rowErr) {
				return nil, rowErr
			}
			return refuse(rowErrorEdge, fmt.Sprintf("edge %d", edgeIndex), "%v", err)
		}
		identity, err := classifyEdgeOwnership(doc, ownEndpoint, index, edgeIndex, scope)
		if err != nil {
			return nil, err
		}
		if _, seen := groups[identity]; !seen {
			groupOrder = append(groupOrder, identity)
		}
		groups[identity] = append(groups[identity], doc)
	}

	out := make([]control.EdgeOccurrenceRow, 0, len(built.edges))
	for _, identity := range groupOrder {
		members := groups[identity]
		peerType, peerKey := identity.toType, identity.toID
		ownIsSource := !identity.directionBackref
		direction := "forward"
		if !ownIsSource {
			peerType, peerKey = identity.fromType, identity.fromID
			direction = "back-reference"
		}
		qualify := sanitizeKeyPart
		if scope.DatasetGeneration != "" {
			project, generation := scope.Project, scope.DatasetGeneration
			qualify = func(logicalID string) string {
				return generationDocumentKey(project, generation, peerType, SanitizeKey(logicalID))
			}
		}
		candidates := selectReferencePositions(positions, peerType, peerKey, identity.label, qualify, identity.directionBackref)
		if len(candidates) == 0 {
			column := fmt.Sprintf("edge _to (first of group %s)", identity.key)
			if !ownIsSource {
				column = fmt.Sprintf("edge _from (first of group %s)", identity.key)
			}
			return refuse(rowErrorEdge, column,
				"the %s edge document %s/%s -%s-> %s/%s matches no reference position in the vertex payload",
				direction, identity.fromType, identity.fromID, identity.label, identity.toType, identity.toID)
		}
		if len(members) > len(candidates) {
			return refuse(rowErrorEdge, fmt.Sprintf("edge _key (group %s)", identity.key),
				"the row offers %d identical %s edge documents for %s/%s -%s-> %s/%s but the payload holds only %d matching reference positions",
				len(members), direction, identity.fromType, identity.fromID, identity.label,
				identity.toType, identity.toID, len(candidates))
		}
		ordinals := make(map[string]int64, len(candidates))
		rowType := SanitizeKey(resourceType)
		for i := range members {
			position := candidates[i]
			ordinal := ordinals[position.path]
			ordinals[position.path] = ordinal + 1
			sourceType, sourceID, targetType, targetID := identity.fromType, identity.fromID, identity.toType, identity.toID
			if ownIsSource {
				sourceID = logicalID
				targetID = position.logicalID
				if position.refType != "" {
					canonical, ok := fhirschema.ConcreteResourceType(position.refType)
					if !ok {
						return refuse(rowErrorEdge, fmt.Sprintf("edge _to (first of group %s)", identity.key),
							"the matched payload reference %q at %q does not resolve to a concrete FHIR resource type",
							position.refType, position.path)
					}
					if canonical != identity.toType {
						return refuse(rowErrorEdge, fmt.Sprintf("edge _to (first of group %s)", identity.key),
							"the forward edge document declares target type %q, but the matched payload reference %s/%s at %q carries type %q",
							identity.toType, canonical, position.logicalID, position.path, position.refType)
					}
				}
			} else {
				sourceID = position.logicalID
				targetID = logicalID
				if position.refType != "" {
					canonical, ok := fhirschema.ConcreteResourceType(position.refType)
					if !ok {
						return refuse(rowErrorEdge, fmt.Sprintf("edge _from (first of group %s)", identity.key),
							"the matched payload reference %q at %q does not resolve to a concrete FHIR resource type",
							position.refType, position.path)
					}
					if canonical != identity.fromType {
						if identity.fromType == rowType {
							sourceType = canonical
						} else {
							return refuse(rowErrorEdge, fmt.Sprintf("edge _from (first of group %s)", identity.key),
								"the back-reference edge document declares source type %q, but the matched payload reference %s/%s at %q carries type %q and the declared type is not the row's own type %q",
								identity.fromType, canonical, position.logicalID, position.path, position.refType, rowType)
						}
					} else {
						sourceType = canonical
					}
				}
			}
			out = append(out, control.EdgeOccurrenceRow{
				Project:            scope.Project,
				DatasetGeneration:  scope.DatasetGeneration,
				AuthResourcePath:   scope.AuthResourcePath,
				Label:              identity.label,
				SourceResourceType: sourceType,
				SourceLogicalID:    sourceID,
				TargetResourceType: targetType,
				TargetLogicalID:    targetID,
				SourcePath:         position.path,
				SourceOrdinal:      ordinal,
				LogicalEdgeKey:     members[i].logicalEdgeKey,
			})
		}
	}
	return out, nil
}

func sanitizeKeyPart(logicalID string) string {
	return SanitizeKey(logicalID)
}

func decodeBuiltEdgeDocument(
	raw json.RawMessage,
	scope PGEdgeBatchScope,
	rowIndex int,
	edgeIndex int,
) (*pgEdgeDocument, error) {
	column := func(name string) string { return fmt.Sprintf("edge %d %s", edgeIndex, name) }
	refuse := func(column string, format string, args ...any) (*pgEdgeDocument, error) {
		return nil, &PGEdgeRowIdentityError{
			BatchID: scope.BatchID, Row: rowIndex, Category: rowErrorEdge, Column: column,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	document, err := decodeTopLevelDocument(raw)
	if err != nil {
		return refuse(column("document"), "the built edge document is undecodable: %v", err)
	}
	if _, advertised := document["edge_seq"]; advertised {
		return refuse(column("edge_seq"),
			"the storage identity is server-assigned; an edge document must not advertise one")
	}

	doc := &pgEdgeDocument{}
	if doc.project, err = requiredDocumentString(document, "project"); err != nil {
		return refuse(column("project"), "%v", err)
	}
	if doc.project != scope.Project {
		return refuse(column("project"), "edge project %q does not match the batch project %q", doc.project, scope.Project)
	}
	if doc.label, err = requiredDocumentString(document, "label"); err != nil {
		return refuse(column("label"), "%v", err)
	}
	if doc.key, err = requiredDocumentString(document, "_key"); err != nil {
		return refuse(column("_key"), "%v", err)
	}
	if logicalKey, present := document[logicalKeyField]; present {
		var value string
		if err := json.Unmarshal(logicalKey, &value); err != nil {
			return refuse(column(logicalKeyField), "%s must be a string", logicalKeyField)
		}
		doc.logicalEdgeKey = &value
	} else {
		key := doc.key
		doc.logicalEdgeKey = &key
	}
	from, err := requiredDocumentString(document, "_from")
	if err != nil {
		return refuse(column("_from"), "%v", err)
	}
	to, err := requiredDocumentString(document, "_to")
	if err != nil {
		return refuse(column("_to"), "%v", err)
	}
	declaredFromType, err := requiredDocumentString(document, "from_type")
	if err != nil {
		return refuse(column("from_type"), "%v", err)
	}
	declaredToType, err := requiredDocumentString(document, "to_type")
	if err != nil {
		return refuse(column("to_type"), "%v", err)
	}
	fromType, fromID, parseErr := parseEdgeEndpoint(column("from_type"), declaredFromType, from)
	if parseErr != nil {
		parseErr.BatchID = scope.BatchID
		parseErr.Row = rowIndex
		return nil, parseErr
	}
	toType, toID, parseErr := parseEdgeEndpoint(column("to_type"), declaredToType, to)
	if parseErr != nil {
		parseErr.BatchID = scope.BatchID
		parseErr.Row = rowIndex
		return nil, parseErr
	}
	doc.fromType, doc.fromID = fromType, fromID
	doc.toType, doc.toID = toType, toID

	if doc.authResourcePath, doc.authPresent, err = optionalDocumentString(document, "auth_resource_path"); err != nil {
		return refuse(column("auth_resource_path"), "%v", err)
	}
	if !doc.authPresent {
		if scope.AuthResourcePath != "" {
			return refuse(column("auth_resource_path"),
				"the edge carries no auth_resource_path but the batch is scoped to %q", scope.AuthResourcePath)
		}
	} else if doc.authResourcePath != scope.AuthResourcePath {
		return refuse(column("auth_resource_path"), "edge auth_resource_path %q does not match the batch auth path %q",
			doc.authResourcePath, scope.AuthResourcePath)
	}

	if doc.datasetGeneration, doc.stamped, err = optionalDocumentString(document, generationIdentityField); err != nil {
		return refuse(column(generationIdentityField), "%v", err)
	}
	if scope.DatasetGeneration != "" {
		if !doc.stamped {
			return refuse(column(generationIdentityField),
				"the edge carries no dataset_generation stamp but the batch commits into generation %q",
				scope.DatasetGeneration)
		}
		if doc.datasetGeneration != scope.DatasetGeneration {
			return refuse(column(generationIdentityField),
				"edge generation %q does not match the batch generation %q", doc.datasetGeneration, scope.DatasetGeneration)
		}
	} else if doc.stamped {
		return refuse(column(generationIdentityField),
			"the edge is stamped for generation %q but the batch is the legacy (unversioned) namespace", doc.datasetGeneration)
	}
	return doc, nil
}

func parseEdgeEndpoint(column, declaredType, endpoint string) (string, string, *PGEdgeRowIdentityError) {
	resourceType, logicalID, ok := SplitCollectionID(endpoint)
	if !ok {
		return "", "", &PGEdgeRowIdentityError{
			Category: rowErrorEdge, Column: column,
			Reason: fmt.Sprintf("endpoint %q is not a well-formed Type/id collection identity", endpoint),
		}
	}
	if resourceType != declaredType {
		return "", "", &PGEdgeRowIdentityError{
			Category: rowErrorEdge, Column: column,
			Reason: fmt.Sprintf("endpoint %q has collection type %q but the document declares %q",
				endpoint, resourceType, declaredType),
		}
	}
	return resourceType, logicalID, nil
}

type pgEdgeIdentity struct {
	fromType         string
	fromID           string
	label            string
	toType           string
	toID             string
	directionBackref bool
	key              string
}

func classifyEdgeOwnership(
	doc *pgEdgeDocument,
	ownEndpoint string,
	rowIndex int,
	edgeIndex int,
	scope PGEdgeBatchScope,
) (pgEdgeIdentity, error) {
	identity := pgEdgeIdentity{
		fromType: doc.fromType,
		fromID:   doc.fromID,
		label:    doc.label,
		toType:   doc.toType,
		toID:     doc.toID,
		key:      doc.key,
	}
	fromEndpoint := SanitizeKey(doc.fromType) + "/" + doc.fromID
	toEndpoint := SanitizeKey(doc.toType) + "/" + doc.toID
	switch ownEndpoint {
	case fromEndpoint:
		return identity, nil
	case toEndpoint:
		identity.directionBackref = true
		return identity, nil
	default:
		return pgEdgeIdentity{}, &PGEdgeRowIdentityError{
			BatchID: scope.BatchID, Row: rowIndex, Category: rowErrorEdge,
			Column: fmt.Sprintf("edge %d _from", edgeIndex),
			Reason: fmt.Sprintf("neither _from %q nor _to %q is the owning row's endpoint %q; "+
				"an edge document must be owned by (or back-reference) its row",
				fromEndpoint, toEndpoint, ownEndpoint),
		}
	}
}

type pgReferencePosition struct {
	path      string
	refType   string
	logicalID string
}

func scanPayloadReferencePositions(canonical []byte) ([]pgReferencePosition, error) {
	trimmed := strings.TrimSpace(string(canonical))
	if trimmed == "null" || trimmed == "" {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &object); err != nil {
		return nil, fmt.Errorf("the built payload document must be a JSON object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("the built payload document must be a JSON object")
	}
	var out []pgReferencePosition
	collectReferencePositions(object, "", &out)
	return out, nil
}

func collectReferencePositions(object map[string]json.RawMessage, prefix string, out *[]pgReferencePosition) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		walkReferenceValue(object[key], joinPath(prefix, key), out)
	}
}

func walkReferenceValue(raw json.RawMessage, path string, out *[]pgReferencePosition) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return
		}
		collectReferencePositions(object, path, out)
	case strings.HasPrefix(trimmed, "["):
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return
		}
		for _, item := range items {
			walkReferenceValue(item, path, out)
		}
	case path == "reference" || strings.HasSuffix(path, ".reference"):
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return
		}
		if refType, logicalID, ok := parseFHIRReferenceString(value); ok {
			*out = append(*out, pgReferencePosition{
				path:      strings.TrimSuffix(strings.TrimSuffix(path, "reference"), "."),
				refType:   refType,
				logicalID: logicalID,
			})
		}
	}
}

func parseFHIRReferenceString(value string) (refType, logicalID string, ok bool) {
	first := strings.IndexByte(value, '/')
	if first <= 0 || first == len(value)-1 {
		return "", value, true
	}
	refType = value[:first]
	logicalID = value[first+1:]
	if second := strings.IndexByte(logicalID, '/'); second >= 0 {
		logicalID = logicalID[:second]
	}
	if logicalID == "" {
		return "", "", false
	}
	return refType, logicalID, true
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func selectReferencePositions(
	positions []pgReferencePosition,
	peerType, peerKey, label string,
	qualify func(logicalID string) string,
	backref bool,
) []pgReferencePosition {
	var idMatches []pgReferencePosition
	for _, position := range positions {
		if qualify(position.logicalID) == peerKey {
			idMatches = append(idMatches, position)
		}
	}
	if len(idMatches) == 0 {
		return nil
	}
	pick := func(predicate func(pgReferencePosition) bool) ([]pgReferencePosition, bool) {
		var kept []pgReferencePosition
		for _, position := range idMatches {
			if predicate(position) {
				kept = append(kept, position)
			}
		}
		return kept, len(kept) > 0
	}
	if backref {
		if kept, ok := pick(func(p pgReferencePosition) bool { return labelNamesPathToken(label, p.path) }); ok {
			idMatches = kept
		}
		if kept, ok := pick(func(p pgReferencePosition) bool { return positionTypeMatchesPeer(p.refType, peerType) }); ok {
			idMatches = kept
		} else if kept, ok := pick(func(p pgReferencePosition) bool { return p.refType == "" }); ok {
			idMatches = kept
		}
		return idMatches
	}
	if kept, ok := pick(func(p pgReferencePosition) bool { return positionTypeMatchesPeer(p.refType, peerType) }); ok {
		idMatches = kept
	} else if kept, ok := pick(func(p pgReferencePosition) bool { return p.refType == "" }); ok {
		idMatches = kept
	}
	if kept, ok := pick(func(p pgReferencePosition) bool { return labelNamesPathToken(label, p.path) }); ok {
		idMatches = kept
	}
	return idMatches
}

func positionTypeMatchesPeer(positionType, peerType string) bool {
	if positionType == peerType {
		return true
	}
	if positionType == "" || peerType == "" {
		return false
	}
	canonical, ok := fhirschema.ConcreteResourceType(positionType)
	return ok && canonical == peerType
}

func labelNamesPathToken(label, path string) bool {
	for _, pathSegment := range strings.Split(path, ".") {
		if pathSegment == "" {
			continue
		}
		for _, labelToken := range strings.FieldsFunc(label, func(r rune) bool {
			return r == '_' || r == '.'
		}) {
			if labelToken == pathSegment {
				return true
			}
		}
	}
	return false
}
