package querysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"arangodb-proto/internal/dbio"
	"arangodb-proto/internal/store"
)

type PrepareCaseAssayOptions struct {
	dbio.ConnectionOptions
	Project          string
	AuthResourcePath string
	BatchSize        int
	ProgressEvery    int
	Truncate         bool
}

type PrepareCaseAssaySummary struct {
	Project          string  `json:"project"`
	AuthResourcePath string  `json:"auth_resource_path,omitempty"`
	RowsPrepared     int     `json:"rows_prepared"`
	Seconds          float64 `json:"seconds"`
}

type patientFileRollupDocument struct {
	Key                 string   `json:"_key"`
	Project             string   `json:"project"`
	PatientKey          string   `json:"patient_key"`
	AuthResourcePath    string   `json:"auth_resource_path,omitempty"`
	SpecimenCount       int      `json:"specimen_count"`
	GroupCount          int      `json:"group_count"`
	FileCount           int      `json:"file_count"`
	SpecimenTypes       []string `json:"specimen_types,omitempty"`
	PreservationMethods []string `json:"preservation_methods,omitempty"`
	FileKeys            []string `json:"file_keys,omitempty"`
}

type specimenMeta struct {
	types               []string
	preservationMethods []string
}

func PrepareGDCCaseAssayMatrix(ctx context.Context, opts PrepareCaseAssayOptions) (PrepareCaseAssaySummary, error) {
	if dbio.BackendName(opts.Backend) != dbio.BackendSurreal && dbio.BackendName(opts.Backend) != dbio.BackendPostgres {
		return PrepareCaseAssaySummary{}, fmt.Errorf("prepare-gdc-case-assay-matrix currently supports only the surreal and postgres backends")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.ProgressEvery <= 0 {
		opts.ProgressEvery = 5000
	}

	client, err := openBackend(ctx, opts.ConnectionOptions)
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	defer client.Close(ctx)

	spec := helperBootstrapSpec([]store.CollectionSpec{
		{
			Name:    patientFileRollupCollection,
			Indexes: [][]string{{"project", "patient_key"}, {"project", "auth_resource_path", "patient_key"}},
		},
	}, opts.Truncate)

	start := time.Now()
	emit("go_prepare_start", map[string]any{
		"project":            opts.Project,
		"auth_resource_path": opts.AuthResourcePath,
		"collection":         patientFileRollupCollection,
		"truncate":           opts.Truncate,
	})
	if err := client.Bootstrap(ctx, spec); err != nil {
		return PrepareCaseAssaySummary{}, err
	}

	backend := dbio.BackendName(opts.Backend)
	patientAuth, patientOrder, err := queryPatientsForPrepare(ctx, client, backend, opts.Project, opts.AuthResourcePath)
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	specimenMetadata, err := querySpecimenMetadata(ctx, client, backend, opts.Project)
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	patientSpecimens, err := queryEdgeMap(ctx, client, backend, opts.Project, "subject_Patient", "Specimen", "Patient")
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	patientFileKeys, err := queryEdgeMap(ctx, client, backend, opts.Project, "subject_Patient", "DocumentReference", "Patient")
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	specimenGroups, err := queryEdgeMap(ctx, client, backend, opts.Project, "member_entity_Specimen", "Group", "Specimen")
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	specimenFiles, err := queryEdgeMap(ctx, client, backend, opts.Project, "subject_Specimen", "DocumentReference", "Specimen")
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}
	groupFiles, err := queryEdgeMap(ctx, client, backend, opts.Project, "subject_Group", "DocumentReference", "Group")
	if err != nil {
		return PrepareCaseAssaySummary{}, err
	}

	batch := make([]json.RawMessage, 0, opts.BatchSize)
	rowsPrepared := 0
	overwrite := !opts.Truncate

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := client.InsertBatchRaw(ctx, patientFileRollupCollection, batch, overwrite, "import"); err != nil {
			return err
		}
		batch = make([]json.RawMessage, 0, opts.BatchSize)
		return nil
	}

	for _, patientKey := range patientOrder {
		specimenKeys := sortedStringKeys(patientSpecimens[patientKey])
		groupSet := make(map[string]struct{})
		fileSet := make(map[string]struct{})
		specimenTypesSet := make(map[string]struct{})
		preservationSet := make(map[string]struct{})

		for _, specimenKey := range specimenKeys {
			for key := range specimenGroups[specimenKey] {
				groupSet[key] = struct{}{}
			}
			for key := range specimenFiles[specimenKey] {
				fileSet[key] = struct{}{}
			}
			meta := specimenMetadata[specimenKey]
			for _, value := range meta.types {
				specimenTypesSet[value] = struct{}{}
			}
			for _, value := range meta.preservationMethods {
				preservationSet[value] = struct{}{}
			}
		}
		for groupKey := range groupSet {
			for fileKey := range groupFiles[groupKey] {
				fileSet[fileKey] = struct{}{}
			}
		}
		for fileKey := range patientFileKeys[patientKey] {
			fileSet[fileKey] = struct{}{}
		}

		doc := patientFileRollupDocument{
			Key:                 rollupKey(opts.Project, patientKey),
			Project:             opts.Project,
			PatientKey:          patientKey,
			AuthResourcePath:    patientAuth[patientKey],
			SpecimenCount:       len(specimenKeys),
			GroupCount:          len(groupSet),
			FileCount:           len(fileSet),
			SpecimenTypes:       sortedStringKeys(specimenTypesSet),
			PreservationMethods: sortedStringKeys(preservationSet),
			FileKeys:            sortedStringKeys(fileSet),
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return PrepareCaseAssaySummary{}, err
		}
		batch = append(batch, raw)
		rowsPrepared++
		if rowsPrepared%opts.ProgressEvery == 0 {
			emit("go_prepare_progress", map[string]any{
				"project":       opts.Project,
				"rows_prepared": rowsPrepared,
				"seconds":       secondsSince(start),
			})
		}
		if len(batch) >= opts.BatchSize {
			if err := flush(); err != nil {
				return PrepareCaseAssaySummary{}, err
			}
		}
	}
	if err := flush(); err != nil {
		return PrepareCaseAssaySummary{}, err
	}

	summary := PrepareCaseAssaySummary{
		Project:          opts.Project,
		AuthResourcePath: opts.AuthResourcePath,
		RowsPrepared:     rowsPrepared,
		Seconds:          secondsSince(start),
	}
	emit("go_prepare_complete", map[string]any{
		"project":       opts.Project,
		"rows_prepared": rowsPrepared,
		"seconds":       summary.Seconds,
	})
	return summary, nil
}

func queryPatientsForPrepare(ctx context.Context, client store.Backend, backend, project, authResourcePath string) (map[string]string, []string, error) {
	query := `
SELECT _key, auth_resource_path
FROM Patient
WHERE project = $project
  AND ($auth_resource_path = "" OR auth_resource_path = $auth_resource_path)
ORDER BY _key ASC;
`
	bindVars := map[string]any{
		"project":            project,
		"auth_resource_path": authResourcePath,
	}
	if backend == dbio.BackendPostgres {
		query = `
SELECT
  split_part(resource_key, '/', 2) AS _key,
  auth_resource_path
FROM fhir_resource
WHERE project = @project
  AND resource_type = 'Patient'
  AND (NULLIF(@auth_resource_path, '') IS NULL OR auth_resource_path = @auth_resource_path)
ORDER BY resource_key ASC;
`
	}
	authByPatient := make(map[string]string)
	order := make([]string, 0, 1024)
	err := client.QueryRows(ctx, query, 1000, bindVars, func(row map[string]any) error {
		key := stringValue(row["_key"])
		if key == "" {
			return nil
		}
		authByPatient[key] = stringValue(row["auth_resource_path"])
		order = append(order, key)
		return nil
	})
	return authByPatient, order, err
}

func querySpecimenMetadata(ctx context.Context, client store.Backend, backend, project string) (map[string]specimenMeta, error) {
	query := `
SELECT _key, payload
FROM Specimen
WHERE project = $project;
`
	if backend == dbio.BackendPostgres {
		query = `
SELECT
  split_part(resource_key, '/', 2) AS _key,
  body AS payload
FROM fhir_resource
WHERE project = @project
  AND resource_type = 'Specimen';
`
	}
	out := make(map[string]specimenMeta)
	err := client.QueryRows(ctx, query, 1000, map[string]any{"project": project}, func(row map[string]any) error {
		key := stringValue(row["_key"])
		payload, _ := row["payload"].(map[string]any)
		out[key] = specimenMeta{
			types:               specimenTypesFromPayload(payload),
			preservationMethods: preservationMethodsFromPayload(payload),
		}
		return nil
	})
	return out, err
}

func queryEdgeMap(ctx context.Context, client store.Backend, backend, project, label, fromType, toType string) (map[string]map[string]struct{}, error) {
	query := `
SELECT _from, _to
FROM fhir_edge
WHERE project = $project
  AND label = $label
  AND from_type = $from_type
  AND to_type = $to_type;
`
	rowFrom := "_from"
	rowTo := "_to"
	bindVars := map[string]any{
		"project":   project,
		"label":     label,
		"from_type": fromType,
		"to_type":   toType,
	}
	if backend == dbio.BackendPostgres {
		query = `
SELECT
  src_key AS _from,
  dst_key AS _to
FROM fhir_edge
WHERE project = @project
  AND edge_type = @label
  AND src_type = @from_type
  AND dst_type = @to_type;
`
		rowFrom = "_from"
		rowTo = "_to"
	}
	out := make(map[string]map[string]struct{})
	err := client.QueryRows(ctx, query, 2000, bindVars, func(row map[string]any) error {
		fromKey := refKey(stringValue(row[rowFrom]))
		toKey := refKey(stringValue(row[rowTo]))
		if fromKey == "" || toKey == "" {
			return nil
		}
		set := out[toKey]
		if set == nil {
			set = make(map[string]struct{})
			out[toKey] = set
		}
		set[fromKey] = struct{}{}
		return nil
	})
	return out, err
}

func specimenTypesFromPayload(payload map[string]any) []string {
	typeField, _ := payload["type"].(map[string]any)
	codings, _ := typeField["coding"].([]any)
	set := make(map[string]struct{})
	for _, codingValue := range codings {
		coding, _ := codingValue.(map[string]any)
		value := strings.TrimSpace(stringValue(coding["display"]))
		if value == "" {
			value = strings.TrimSpace(stringValue(coding["code"]))
		}
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedStringKeys(set)
}

func preservationMethodsFromPayload(payload map[string]any) []string {
	processings, _ := payload["processing"].([]any)
	set := make(map[string]struct{})
	for _, processingValue := range processings {
		processing, _ := processingValue.(map[string]any)
		method, _ := processing["method"].(map[string]any)
		codings, _ := method["coding"].([]any)
		for _, codingValue := range codings {
			coding, _ := codingValue.(map[string]any)
			system := stringValue(coding["system"])
			if !strings.Contains(system, "preservation_method") {
				continue
			}
			value := strings.TrimSpace(stringValue(coding["display"]))
			if value == "" {
				value = strings.TrimSpace(stringValue(coding["code"]))
			}
			if value != "" {
				set[value] = struct{}{}
			}
		}
	}
	return sortedStringKeys(set)
}

func refKey(ref string) string {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func rollupKey(project, patientKey string) string {
	return strings.ReplaceAll(project, "/", "_") + "::" + patientKey
}

func sortedStringKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
