# ARANGODB_PROTO

Minimal graph database prototype for the `gen3_tracker.meta.dataframer` path.

The primary implementation target is ArangoDB. The repository also contains an
experimental SurrealDB backend for ingest and dataframe benchmarking. See
[`ARANGO_VS_SURREAL_FHIR_POSTMORTEM.md`](ARANGO_VS_SURREAL_FHIR_POSTMORTEM.md)
for the technical comparison and recommendation.

This prototype intentionally ignores auth and only targets one hardcoded traversal:

- `flattened_document_reference`
- `gdc_file_sample_matrix`
- `gdc_case_assay_matrix`

It mirrors the current Python/SQLite logic in
`/Users/peterkor/Desktop/gen3_util/gen3_tracker/meta/dataframer.py`:

1. load FHIR NDJSON
2. store raw resource payloads in Arango collections
3. key every resource by `project::id` so multiple projects can coexist in one DB
4. create reference edges in AQL for:
   - `subject`
   - `focus`
5. run an AQL traversal that follows:
   - `DocumentReference.subject`
   - `subject-of-subject`
   - `Observation.focus -> DocumentReference`
6. either:
   - return flattened rows directly as NDJSON
   - materialize the flattened rows into a collection
   - export either path as Elasticsearch bulk NDJSON

The more useful GDC traversal is `gdc_file_sample_matrix`. It emits one row per
`DocumentReference` file and sample-group member:

- `DocumentReference -> Group.member[] -> Specimen -> Patient`
- patient diagnosis from `Condition`
- study metadata from `ResearchSubject/ResearchStudy`
- treatment categories/counts from `MedicationAdministration`
- file category/type/format/platform/workflow/access from GDC codings

The patient-first GDC traversal is `gdc_case_assay_matrix`. It emits one row per
`Patient`/case and summarizes what downstream assay evidence exists:

- `Patient -> Specimen -> Group.member[] -> DocumentReference`
- patient diagnosis from `Condition`
- study metadata from `ResearchSubject/ResearchStudy`
- treatment categories/counts from `MedicationAdministration`
- assay availability flags/counts for SNV, WXS, WGS, RNA-Seq, expression, fusion,
  CNV, methylation, slides, aligned reads, and clinical files
- representative files per assay class for drill-down

This is the useful metadata-level starting point for questions like "find cases
with tumor samples, annotated somatic mutation files, expression files, and
slides." A literal "find everyone with TP53 mutation" query needs a second
prototype phase that indexes controlled MAF/VCF/variant payload contents and then
joins those variant findings back to this case/sample/file matrix.

## Scope

This is a prototype to answer:

- can the current hardcoded dataframer traversal be expressed in AQL?
- how fast is Arango at loading and materializing this shape?

It is not intended to be production-ready.

## Collections

The prototype discovers resource collections from whatever exists in `META/*.ndjson`.

It also creates:

- `subject_ref` (edge)
- `focus_ref` (edge)
- `member_ref` (edge)
- `document_reference_flat` (optional materialized rows)

## Quick start

Start the local benchmark stack:

```bash
cd /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO
docker compose up -d
```

This brings up:

- ArangoDB on `http://127.0.0.1:8529`
- SurrealDB on `http://127.0.0.1:8001`

The bundled SurrealDB service uses `surrealkv:/data/fhir_proto.db` for
file-backed local storage.

Experimental Surreal load benchmark:

```bash
./arango-fhir-proto load \
  --backend surreal \
  --url http://127.0.0.1:8001 \
  --namespace fhir_proto \
  --database fhir_proto \
  --username root \
  --password root \
  --meta-dir /tmp/META_10PCT \
  --project ARANGODB_PROTO \
  --batch-size 1000 \
  --progress-every 50000 \
  --auth-resource-path EllrottLab-GDC_Data
```

Bootstrap collections and indexes:

```bash
python3 prototype.py bootstrap \
  --meta-dir /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/META
```

Load the local test dataset as raw payload documents:

```bash
python3 prototype.py load \
  --meta-dir /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/META \
  --project ARANGODB_PROTO \
  --batch-size 2000 \
  --progress-every 50000
```

For large runs, `load` now streams file-by-file and emits JSON progress events
instead of holding the whole dataset in memory before writing.

Build project-scoped graph edges from the raw payload using AQL:

```bash
python3 prototype.py build-edges \
  --meta-dir /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/META \
  --project ARANGODB_PROTO \
  --cursor-batch-size 10000 \
  --progress-every 50000
```

`build-edges` builds `subject_ref`, `focus_ref`, and `member_ref`. Rerun it after
upgrading the prototype because the case-first matrix depends on `Group.member[]`
edges.

Query the hardcoded dataframer traversal directly and emit flattened NDJSON:

```bash
python3 prototype.py query-document-references \
  --project ARANGODB_PROTO \
  --cursor-batch-size 10000 \
  --progress-every 50000 \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/document_reference_flat.ndjson
```

If you want a materialized Arango collection for repeated export/testing:

```bash
python3 prototype.py materialize-document-references \
  --project ARANGODB_PROTO \
  --batch-size 2000 \
  --cursor-batch-size 10000 \
  --progress-every 50000
```

Export Elasticsearch bulk NDJSON directly from the AQL query:

```bash
python3 prototype.py export-elastic \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/document_reference_flat.bulk.ndjson \
  --index fhir_document_reference_flat \
  --source query \
  --project ARANGODB_PROTO \
  --cursor-batch-size 10000 \
  --progress-every 50000
```

Build the more useful GDC file/sample/case dataframe as NDJSON:

```bash
python3 prototype.py query-gdc-file-sample-matrix \
  --project ARANGODB_PROTO \
  --cursor-batch-size 10000 \
  --progress-every 50000 \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/gdc_file_sample_matrix.ndjson
```

For quick iteration, cap the output:

```bash
python3 prototype.py query-gdc-file-sample-matrix \
  --project ARANGODB_PROTO \
  --cursor-batch-size 500 \
  --progress-every 500 \
  --max-rows 1000 \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/gdc_file_sample_matrix.sample.ndjson
```

Export that dataframe directly as Elasticsearch bulk NDJSON:

```bash
python3 prototype.py export-gdc-file-sample-matrix \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/gdc_file_sample_matrix.bulk.ndjson \
  --index gdc_file_sample_matrix \
  --project ARANGODB_PROTO \
  --cursor-batch-size 10000 \
  --progress-every 50000
```

Build the patient-first GDC case/assay dataframe as NDJSON:

```bash
python3 prototype.py query-gdc-case-assay-matrix \
  --project ARANGODB_PROTO \
  --cursor-batch-size 1000 \
  --progress-every 1000 \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/gdc_case_assay_matrix.ndjson
```

For quick iteration, cap the case-first output:

```bash
python3 prototype.py query-gdc-case-assay-matrix \
  --project ARANGODB_PROTO \
  --cursor-batch-size 500 \
  --progress-every 500 \
  --max-rows 1000 \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/gdc_case_assay_matrix.sample.ndjson
```

Export that patient-first dataframe directly as Elasticsearch bulk NDJSON:

```bash
python3 prototype.py export-gdc-case-assay-matrix \
  --output /Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/gdc_case_assay_matrix.bulk.ndjson \
  --index gdc_case_assay_matrix \
  --project ARANGODB_PROTO \
  --cursor-batch-size 1000 \
  --progress-every 1000
```

## What is preserved

Each resource document stores:

- `payload`: canonical source JSON
- `id`: logical FHIR id
- `project`
- `resourceType`
- `_key = project::id`

This version is more realistic than the first pass because:

- ingest does not precompute flattened fields
- AQL builds graph edges from raw payload
- AQL derives flattened rows from raw payload + edges
- export can stream those flattened rows straight into Elasticsearch bulk format
- `gdc_file_sample_matrix` shows a real GDC file-to-case/sample/study traversal
- `gdc_case_assay_matrix` shows a real case-to-sample/file/assay traversal

It is still intentionally narrow, but it now has one real file-centric GDC
dataframe, one case-centric cohort discovery dataframe, and the older
`flattened_document_reference` compatibility path.

## Benchmarking

Each command prints timing information for:

- collection bootstrap
- resource ingest
- edge ingest
- query materialization
- export

That should be enough to evaluate whether the current hardcoded dataframer can be abstracted into AQL later.

## Portability Guide

For the implementation-neutral workload definition used to port and benchmark
this prototype against other databases, see
[DATAFRAME_BUILDER_PORTABILITY.md](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/DATAFRAME_BUILDER_PORTABILITY.md).
