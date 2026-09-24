#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import process from 'node:process';
import { performance } from 'node:perf_hooks';

const defaults = {
  api: 'http://127.0.0.1:8182',
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  explorer: 'loom-dev-bootstrap',
  snapshot: 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7',
  rowRoot: 'MedicationAdministration',
  resourceType: 'MedicationAdministration',
  samples: 5,
  arangoContainer: 'loom-dev-6d7df93d6a37-arangodb-1',
  arangoDatabase: 'loom_dev',
};

const optionNames = new Map([
  ['--api', 'api'],
  ['--project', 'project'],
  ['--generation', 'generation'],
  ['--explorer', 'explorer'],
  ['--snapshot', 'snapshot'],
  ['--row-root', 'rowRoot'],
  ['--resource-type', 'resourceType'],
  ['--samples', 'samples'],
  ['--arango-container', 'arangoContainer'],
  ['--arango-database', 'arangoDatabase'],
]);

const parseArgs = (argv) => {
  const options = { ...defaults };
  for (let index = 0; index < argv.length; index += 2) {
    const key = optionNames.get(argv[index]);
    assert.ok(key && argv[index + 1], `unknown or incomplete option ${argv[index] ?? ''}`);
    options[key] = key === 'samples' ? Number.parseInt(argv[index + 1], 10) : argv[index + 1];
  }
  assert.ok(Number.isInteger(options.samples) && options.samples > 0, '--samples must be a positive integer');
  return options;
};

const percentile = (values, fraction) => {
  const sorted = [...values].sort((left, right) => left - right);
  return sorted[Math.floor((sorted.length - 1) * fraction)];
};

const requestPage = async (url, body) => {
  const started = performance.now();
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const value = await response.json();
  assert.equal(response.status, 200, `feature catalog returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 500)}`);
  assert.ok(Array.isArray(value.entries), 'feature catalog response omitted entries');
  assert.ok(value.entries.length <= body.limit, `feature catalog returned ${value.entries.length} entries for limit ${body.limit}`);
  return { elapsedMs: performance.now() - started, value };
};

const collectSection = async (url, baseBody, section, query = '') => {
  const identities = new Set();
  let addableCount = 0;
  let disabledCount = 0;
  let cursor = '';
  let pageCount = 0;
  do {
    const body = { ...baseBody, section };
    if (query) body.query = query;
    if (cursor) body.cursor = cursor;
    const { value } = await requestPage(url, body);
    assert.equal(value.section, section);
    for (const entry of value.entries) {
      const addable = entry.readiness?.status === 'READY' || entry.readiness?.status === 'READY_WITH_WARNING';
      if (section === 'NEEDS_REVIEW') {
        assert.ok(!addable && !entry.constructionChoice, `NEEDS_REVIEW returned addable ${entry.featureId ?? 'entry'}`);
      }
      if (addable) addableCount += 1;
      else disabledCount += 1;
      const identity = `${entry.source?.bindingId ?? ''}\u0000${entry.source?.conceptId ?? ''}`;
      assert.ok(!identities.has(identity), `${section} repeated ${identity}`);
      identities.add(identity);
    }
    const next = value.nextCursor ?? '';
    assert.notEqual(next, cursor || undefined, `${section} pagination stalled`);
    cursor = next;
    pageCount += 1;
    assert.ok(pageCount < 100_000, `${section} pagination exceeded safety bound`);
  } while (cursor);
  return { identities, pageCount, addableCount, disabledCount };
};

const queryExpected = (options) => {
  const project = JSON.stringify(options.project);
  const generation = JSON.stringify(options.generation);
  const resourceType = JSON.stringify(options.resourceType);
  const query = String.raw`
  LET build = FIRST(
    FOR d IN fhir_semantic_inventory_builds
      FILTER d.project == ${project}
      FILTER d.dataset_generation == ${generation}
      FILTER d.state == "complete"
      SORT d.entry_index_version DESC, d.rule_version DESC, d.build_id DESC
      LIMIT 1
      RETURN d
  )
  LET rows = (
    FOR d IN fhir_semantic_inventory_entries
      FILTER d.project == ${project}
      FILTER d.dataset_generation == ${generation}
      FILTER d.build_id == build.build_id
      COLLECT section = d.catalog_section, resourceType = d.resource_type,
        bindingID = d.binding_id, conceptID = d.concept_id
        AGGREGATE searchText = MIN(d.search_text)
      SORT bindingID, conceptID
      RETURN {section, resourceType, bindingID, conceptID, searchText}
  )
  LET rawGroups = (
    FOR d IN fhir_semantic_inventory
      FILTER d.project == ${project}
      FILTER d.dataset_generation == ${generation}
      FILTER d.build_id == build.build_id
      FILTER NOT_NULL(d.source_kind, "file") == NOT_NULL(build.source_kind, "file")
      COLLECT authPath = NOT_NULL(d.auth_resource_path, ""), resourceType = d.resource_type,
        bindingID = d.binding_id, conceptID = d.concept_id, conceptSlotID = d.concept_slot_id,
        ruleHint = d.observation.rule_hint
      RETURN {authPath, resourceType, bindingID, conceptID, conceptSlotID, ruleHint}
  )
  LET gaps = (
    FOR raw IN rawGroups
      LET matches = (
        FOR entry IN fhir_semantic_inventory_entries
          FILTER entry.project == ${project}
          FILTER entry.dataset_generation == ${generation}
          FILTER entry.build_id == build.build_id
          FILTER NOT_NULL(entry.source_kind, "file") == NOT_NULL(build.source_kind, "file")
          FILTER NOT_NULL(entry.auth_resource_path, "") == raw.authPath
          FILTER entry.resource_type == raw.resourceType
          FILTER entry.binding_id == raw.bindingID
          FILTER entry.concept_id == raw.conceptID
          RETURN entry.catalog_section
      )
      FILTER LENGTH(matches) != 1 OR matches[0] NOT IN ["CONCEPTS", "NEEDS_REVIEW"]
      LIMIT 10
      RETURN MERGE(raw, {matches})
  )
  LET materializedGroups = LENGTH(
    FOR entry IN fhir_semantic_inventory_entries
      FILTER entry.project == ${project}
      FILTER entry.dataset_generation == ${generation}
      FILTER entry.build_id == build.build_id
      FILTER NOT_NULL(entry.source_kind, "file") == NOT_NULL(build.source_kind, "file")
      COLLECT authPath = NOT_NULL(entry.auth_resource_path, ""), resourceType = entry.resource_type,
        bindingID = entry.binding_id, conceptID = entry.concept_id
      RETURN 1
  )
  RETURN {
    buildID: build.build_id,
    ruleVersion: build.rule_version,
    entryIndexVersion: build.entry_index_version,
    resourceType: ${resourceType},
    rows,
    coverage: {rawGroups: LENGTH(rawGroups), materializedGroups, gaps}
  }`;
  const program = String.raw`
const result = db._query(${JSON.stringify(query)}).toArray()[0];
if (!result || !result.buildID) throw new Error("complete semantic inventory build not found");
print(JSON.stringify(result));`;
  const output = execFileSync('docker', [
    'exec', options.arangoContainer,
    'arangosh', '--server.endpoint', 'tcp://127.0.0.1:8529',
    '--server.database', options.arangoDatabase,
    '--server.username', 'root', '--server.password', '',
    '--javascript.execute-string', program,
  ], { encoding: 'utf8' });
  return JSON.parse(output.trim());
};

const main = async () => {
  const options = parseArgs(process.argv.slice(2));
  const url = `${options.api}/api/v1/projects/${encodeURIComponent(options.project)}/explorers/${encodeURIComponent(options.explorer)}/authoring/v2/feature-catalog`;
  const scopedBody = {
    snapshotToken: options.snapshot,
    rowRoot: options.rowRoot,
    resourceType: options.resourceType,
    section: 'CONCEPTS',
    limit: 50,
  };
  const unscopedBody = { ...scopedBody };
  delete unscopedBody.resourceType;

  const coldScoped = await requestPage(url, scopedBody);
  const coldUnscoped = await requestPage(url, unscopedBody);
  const warmScoped = [];
  const warmUnscoped = [];
  for (let index = 0; index < options.samples; index += 1) {
    warmScoped.push((await requestPage(url, scopedBody)).elapsedMs);
    warmUnscoped.push((await requestPage(url, unscopedBody)).elapsedMs);
  }

  const [concepts, review] = await Promise.all([
    collectSection(url, scopedBody, 'CONCEPTS'),
    collectSection(url, scopedBody, 'NEEDS_REVIEW'),
  ]);
  const [allConcepts, allReview] = await Promise.all([
    collectSection(url, unscopedBody, 'CONCEPTS'),
    collectSection(url, unscopedBody, 'NEEDS_REVIEW'),
  ]);
  const overlap = [...concepts.identities].filter((identity) => review.identities.has(identity));
  assert.deepEqual(overlap, [], `catalog identities appeared in both sections: ${overlap.slice(0, 5).join(', ')}`);
  const allOverlap = [...allConcepts.identities].filter((identity) => allReview.identities.has(identity));
  assert.deepEqual(allOverlap, [], `unscoped catalog identities appeared in both sections: ${allOverlap.slice(0, 5).join(', ')}`);

  const expected = queryExpected(options);
  const scopedRows = expected.rows.filter((row) => row.resourceType === options.resourceType);
  const expectedConcepts = new Set(scopedRows.filter((row) => row.section === 'CONCEPTS').map((row) => `${row.bindingID}\u0000${row.conceptID}`));
  const expectedReview = new Set(scopedRows.filter((row) => row.section === 'NEEDS_REVIEW').map((row) => `${row.bindingID}\u0000${row.conceptID}`));
  assert.deepEqual(concepts.identities, expectedConcepts, 'CONCEPTS endpoint identities differ from indexed identities');
  assert.deepEqual(review.identities, expectedReview, 'NEEDS_REVIEW endpoint identities differ from indexed identities');
  const expectedAllConcepts = new Set(expected.rows.filter((row) => row.section === 'CONCEPTS').map((row) => `${row.bindingID}\u0000${row.conceptID}`));
  const expectedAllReview = new Set(expected.rows.filter((row) => row.section === 'NEEDS_REVIEW').map((row) => `${row.bindingID}\u0000${row.conceptID}`));
  assert.deepEqual(allConcepts.identities, expectedAllConcepts, 'unscoped CONCEPTS endpoint identities differ from indexed identities');
  assert.deepEqual(allReview.identities, expectedAllReview, 'unscoped NEEDS_REVIEW endpoint identities differ from indexed identities');
  assert.deepEqual(expected.coverage.gaps, [], 'raw semantic groups are missing or multiply represented in materialized entries');
  assert.equal(expected.coverage.rawGroups, expected.coverage.materializedGroups, 'raw and materialized semantic group counts differ');

  const searchChecks = [];
  for (const section of ['CONCEPTS', 'NEEDS_REVIEW']) {
    const sectionRows = expected.rows.filter((row) => row.section === section);
    if (sectionRows.length <= unscopedBody.limit) continue;
    const target = sectionRows[unscopedBody.limit];
    const query = String(target.searchText ?? '').slice(0, 256);
    assert.ok(query, `${section} has no searchable text beyond its first page`);
    const searched = await collectSection(url, unscopedBody, section, query);
    const indexedMatches = new Set(sectionRows
      .filter((row) => String(row.searchText ?? '').toLowerCase().includes(query.toLowerCase()))
      .map((row) => `${row.bindingID}\u0000${row.conceptID}`));
    assert.deepEqual(searched.identities, indexedMatches, `${section} search differs from the complete indexed search result`);
    searchChecks.push({ section, query, matches: searched.identities.size, pages: searched.pageCount });
  }

  const result = {
    workload: { project: options.project, rowRoot: options.rowRoot, resourceType: options.resourceType, limit: 50 },
    buildID: expected.buildID,
    entryIndexVersion: expected.entryIndexVersion,
    scoped: {
      coldMs: coldScoped.elapsedMs,
      warmMedianMs: percentile(warmScoped, 0.5),
      warmP95Ms: percentile(warmScoped, 0.95),
      warmValuesMs: warmScoped,
    },
    unscoped: {
      coldMs: coldUnscoped.elapsedMs,
      warmMedianMs: percentile(warmUnscoped, 0.5),
      warmP95Ms: percentile(warmUnscoped, 0.95),
      warmValuesMs: warmUnscoped,
    },
    completeness: {
      concepts: concepts.identities.size,
      conceptsAddable: concepts.addableCount,
      conceptsDisabled: concepts.disabledCount,
      conceptsPages: concepts.pageCount,
      needsReview: review.identities.size,
      needsReviewPages: review.pageCount,
      indexedScopedUnion: scopedRows.length,
      indexedUnscopedUnion: expected.rows.length,
      rawSemanticGroups: expected.coverage.rawGroups,
      materializedGroups: expected.coverage.materializedGroups,
      searchChecks,
    },
  };
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
};

await main();
