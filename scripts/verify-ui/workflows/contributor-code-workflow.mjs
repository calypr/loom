import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { createNativeCdaWorkflowTools, validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';

export async function contributorCodeWorkflow({ page, cda, caseOptions = {} }) {
  const { click, fill, selectOption, navigate, inspect, clickControl, fillControl, selectControl,
    nativeClick, nativeFill, nativeSelect, navigatePage, inspectDOM, browserEval, inspectPage,
    waitForDOM, waitForBrowser, captureCDARequests, captureRequests, waitForCapturedResponse,
    performAction, requireUnique } = createNativeCdaWorkflowTools({ page, cda });
  const target = cda.target;
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(target, caseOptions.arangoContainer);
  assert(project && generation && apiOrigin && uiOrigin, 'The CDA fixture must bind project, generation, API origin, and UI origin explicitly.');
  assert.equal(generation, 'cda-fhir-v1', 'This verifier requires the loaded CDA FHIR generation.');
const explorer = `contributor-code-browser-${Date.now()}`;
const quantityWitnessReportPath = caseOptions.quantityWitnessReportPath ?? cda.env?.LOOM_CDA_QUANTITY_WITNESS_REPORT
  ?? '/tmp/loom-pivot-native-advanced-open/report.json';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`;
const candidatePatientLimit = 2000;
const relatedObservationLimit = 100;
const observationEdgeLimit = relatedObservationLimit + 1;
const report = {
  explorer,
  controls: {
    covered: ['Related-record field search and selection: Observation.valueQuantity.code',
      'Contributor predicate: EQUALS with a returned catalog suggestion present on some but not all related CDA records',
      'No-match policies: PRESERVE_PARENT and EXCLUDE',
      'Preview, Cancel, Apply, reload, policy edit, remove Cancel, remove Apply, exact source restoration'],
    uncovered: ['Other Contributor fields and suggestion families',
      'Repeated code paths such as Observation.category[].coding[].code and Observation.code.coding[].code',
      'Alternative related paths and starting anchors', 'EXISTS and ERROR repair (covered by sibling verifier)'],
  },
  cases: [], requests: [], nativeRequests: [], errors: [], started: new Date().toISOString(),
};

let builder;
let outputId;
let requestCapture;

const contributorSearchRequests = [];

const api = async (path, body) => {
  const startedAt = Date.now();
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': `native-cda-${randomUUID()}` };
  const url = apiOrigin + path;
  const response = body === undefined
    ? await cda.request.get(url, { headers, timeout: 30000 })
    : await cda.request.post(url, { headers, data: body, timeout: 30000 });
  const value = await response.json();
  const apiEvidence = { path, status: response.status(), body, response: value, startedAt, completedAt: Date.now() };
  report.apiCalls?.push(apiEvidence);
  report.requests?.push(apiEvidence);
  assert(response.ok(), `${path}: ${JSON.stringify(value)}`);
  return value;
};

const rawQuery = (query) => {
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string',
    `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `Arango did not return JSON: ${result.stdout.slice(-500)}`);
  return JSON.parse(result.stdout.slice(jsonStart));
};

const sourceWitnesses = async () => {
  const witnessReport = JSON.parse(await readFile(quantityWitnessReportPath, 'utf8'));
  assert.equal(witnessReport.status, 'passed', `Quantity witness report did not pass: ${quantityWitnessReportPath}`);
  assert.equal(witnessReport.oracle?.source?.generation, generation,
    'Quantity witness report belongs to a different CDA generation');
  const reportedDomain = witnessReport.oracle?.domains?.['Observation.valueQuantity.code'];
  assert(Array.isArray(reportedDomain) && reportedDomain.some((value) => value.kind === 'NULL'),
    'Quantity witness report must establish missing Observation.valueQuantity.code values');
  const concreteDomainValues = reportedDomain.filter((value) => ['STRING', 'CODE'].includes(value.kind));
  assert.equal(concreteDomainValues.length, 1, 'Quantity witness report must have exactly one concrete code value');
  const targetDomainValue = concreteDomainValues[0];
  const targetValue = targetDomainValue?.kind === 'CODE' ? targetDomainValue.code?.code : targetDomainValue?.string;
  assert(targetValue, 'Quantity witness report has no concrete Observation.valueQuantity.code value');
  const reportObservations = witnessReport.oracle.observations;
  assert(Array.isArray(reportObservations) && reportObservations.length >= 2,
    'Quantity witness report has no bounded Observation witness rows');
  assert.equal(witnessReport.oracle.observationCount, reportObservations.length,
    'Quantity witness report row count disagrees with its declared Observation count');
  const reportPatientIds = [...new Set(reportObservations.map((item) => item.patientId).filter(Boolean))];
  assert.equal(reportPatientIds.length, 1, 'Quantity witness report must identify one Patient for its Observation rows');
  assert(reportObservations.length <= relatedObservationLimit,
    `Quantity witness report exceeds the ${relatedObservationLimit}-Observation cap`);
  assert(witnessReport.oracle.source?._id, 'Quantity witness report has no raw Specimen document identity');

  const searchQuery = `LET catalogFields = (
      FOR field IN fhir_field_catalog
        FILTER field.project == ${JSON.stringify(project)}
          AND field.dataset_generation == ${JSON.stringify(generation)}
          AND field.resource_type == "Observation" AND field.path == "valueQuantity.code"
        SORT field.auth_resource_path
        LIMIT 100
        RETURN field.distinct_values
    )
    LET catalogValues = UNIQUE((
      FOR values IN catalogFields
        FILTER IS_ARRAY(values)
        FOR value IN values
          FILTER IS_STRING(value) AND value != ""
          RETURN value
    ))
    LET candidates = (
      FOR patient IN Patient
        FILTER patient.project == ${JSON.stringify(project)}
          AND patient.dataset_generation == ${JSON.stringify(generation)}
        SORT patient.id
        LIMIT ${candidatePatientLimit}
        LET edgeKeys = (
          FOR edge IN fhir_edge
            FILTER edge._to == patient._id AND edge.label == "subject_Patient"
              AND edge.project == ${JSON.stringify(project)}
              AND edge.dataset_generation == ${JSON.stringify(generation)}
              AND STARTS_WITH(edge._from, "Observation/")
            SORT edge._from
            LIMIT ${observationEdgeLimit}
            RETURN edge._from
        )
        LET observationKeys = UNIQUE(edgeKeys)
        LET observations = (
          FOR observationKey IN observationKeys
            LET observation = DOCUMENT(observationKey)
            FILTER observation.project == ${JSON.stringify(project)}
              AND observation.dataset_generation == ${JSON.stringify(generation)}
            RETURN observation
        )
        RETURN {
          patient: { id: patient.id, _id: patient._id },
          edgeCount: LENGTH(edgeKeys), exactEdgeCount: LENGTH(edgeKeys) < ${observationEdgeLimit},
          observationCount: LENGTH(observations)
        }
    )
    LET zero = FIRST(FOR candidate IN candidates
      FILTER candidate.exactEdgeCount AND candidate.observationCount == 0
      SORT candidate.patient.id
      RETURN candidate)
    LET one = FIRST(FOR candidate IN candidates
      FILTER candidate.exactEdgeCount AND candidate.observationCount == 1
      SORT candidate.patient.id
      RETURN candidate)
    RETURN { candidateCount: LENGTH(candidates), catalogSuggestionCount: LENGTH(catalogValues),
      targetInCatalog: ${JSON.stringify(targetValue)} IN catalogValues, zero, one }`;
  const [search] = rawQuery(searchQuery);
  assert(search?.zero?.patient?.id, `No zero-related CDA Patient found in the bounded ${candidatePatientLimit}-patient scan`);
  assert(search?.one?.patient?.id, `No one-related CDA Patient found in the bounded ${candidatePatientLimit}-patient scan`);
  assert(search.catalogSuggestionCount > 0 && search.targetInCatalog,
    'Report-seeded quantity value is absent from the raw Observation.valueQuantity.code catalog');

  const [seedProof] = rawQuery(`
    LET specimen = DOCUMENT(${JSON.stringify(witnessReport.oracle.source._id)})
    LET patient = FIRST(
      FOR edge IN fhir_edge
        FILTER edge._from == specimen._id AND edge.label == "subject_Patient"
          AND edge.project == ${JSON.stringify(project)}
          AND edge.dataset_generation == ${JSON.stringify(generation)}
        LET relatedPatient = DOCUMENT(edge._to)
        FILTER relatedPatient.id == ${JSON.stringify(reportPatientIds[0])}
          AND relatedPatient.project == ${JSON.stringify(project)}
          AND relatedPatient.dataset_generation == ${JSON.stringify(generation)}
        RETURN relatedPatient
    )
    LET observationKeys = (
      FOR edge IN fhir_edge
        FILTER edge._to == patient._id AND edge.label == "subject_Patient"
          AND edge.project == ${JSON.stringify(project)}
          AND edge.dataset_generation == ${JSON.stringify(generation)}
          AND STARTS_WITH(edge._from, "Observation/")
        SORT edge._from
        LIMIT ${observationEdgeLimit}
        RETURN edge._from
    )
    LET observations = (
      FOR observationKey IN UNIQUE(observationKeys)
        LET observation = DOCUMENT(observationKey)
        FILTER observation.project == ${JSON.stringify(project)}
          AND observation.dataset_generation == ${JSON.stringify(generation)}
        RETURN { id: observation.id, _id: observation._id,
          valueQuantityCode: observation.payload.valueQuantity.code }
    )
    LET valueQuantityCodeCounts = (
      FOR observation IN observations
        FILTER IS_STRING(observation.valueQuantityCode) AND observation.valueQuantityCode != ""
        COLLECT value = observation.valueQuantityCode WITH COUNT INTO valueCount
        SORT value
        RETURN { value, count: valueCount }
    )
    LET targetCount = LENGTH(FOR observation IN observations
      FILTER observation.valueQuantityCode == ${JSON.stringify(targetValue)}
      RETURN observation._id)
    RETURN {
      specimen: { id: specimen.id, _id: specimen._id },
      patient: { id: patient.id, _id: patient._id },
      edgeCount: LENGTH(observationKeys), exactEdgeCount: LENGTH(observationKeys) < ${observationEdgeLimit},
      observationCount: LENGTH(observations), valueQuantityCodeCounts, targetCount, observations
    }`);
  assert.equal(seedProof?.specimen?._id, witnessReport.oracle.source._id,
    'Report-seeded Specimen could not be independently resolved in raw CDA documents');
  assert.equal(seedProof?.patient?.id, reportPatientIds[0],
    'Report-seeded Patient was not independently found through the Specimen subject_Patient edge');
  assert(seedProof.exactEdgeCount && seedProof.observationCount === reportObservations.length,
    `Report-seeded Patient does not have an exact bounded Observation enumeration: ${JSON.stringify(seedProof)}`);
  assert(seedProof.observationCount >= 2 && seedProof.observationCount <= relatedObservationLimit,
    `Report-seeded Patient has an unsupported Observation count: ${seedProof.observationCount}`);
  const reportRows = reportObservations.map((item) => [item._id, item.quantityCode ?? null]).sort(([left], [right]) => left.localeCompare(right));
  const rawRows = seedProof.observations.map((item) => [item._id, item.valueQuantityCode ?? null])
    .sort(([left], [right]) => left.localeCompare(right));
  assert.deepEqual(rawRows, reportRows,
    'Raw report-seeded Observation identities or quantity codes differ from the successful witness report');
  const seedCodeCounts = Object.fromEntries(seedProof.valueQuantityCodeCounts.map(({ value, count }) => [value, count]));
  const targetCount = seedCodeCounts[targetValue] ?? 0;
  assert.equal(seedProof.targetCount, targetCount, 'Raw quantity code count disagrees with the independent code-count query');
  assert(targetCount > 0 && targetCount < seedProof.observationCount,
    `Raw report-seeded Patient must have the catalog value on some but not all Observations: ${JSON.stringify(seedProof)}`);
  const selected = [
    { bucket: 'zero', patient: search.zero.patient },
    { bucket: 'one', patient: search.one.patient },
    { bucket: 'many', patient: seedProof.patient, edgeCount: seedProof.edgeCount,
      target: { value: targetValue, count: targetCount } },
  ];
  assert.equal(new Set(selected.map((item) => item.patient._id)).size, 3, 'CDA zero/one/many witnesses must be distinct');
  const serializedSources = JSON.stringify(selected.map(({ bucket, patient, edgeCount, target }) =>
    ({ bucket, ...patient, edgeCount, target })));
  const detailsQuery = `FOR source IN ${serializedSources}
    LET patient = DOCUMENT(source._id)
    LET observations = (
      FOR e IN fhir_edge
        FILTER e._to == patient._id AND e.label == "subject_Patient"
          AND e.project == ${JSON.stringify(project)} AND e.dataset_generation == ${JSON.stringify(generation)}
          AND STARTS_WITH(e._from, "Observation/")
        SORT e._from
        LIMIT ${observationEdgeLimit}
        COLLECT observationKey = e._from
          LET observation = DOCUMENT(observationKey)
        FILTER observation.project == ${JSON.stringify(project)}
          AND observation.dataset_generation == ${JSON.stringify(generation)}
        SORT observation.id
        RETURN { id: observation.id, _id: observation._id, valueQuantityCode: observation.payload.valueQuantity.code }
    )
    RETURN { bucket: source.bucket, patient: { id: patient.id, _id: patient._id },
      edgeCount: source.edgeCount, target: source.target, observations }`;
  const witnesses = rawQuery(detailsQuery);
  const byBucket = Object.fromEntries(witnesses.map((item) => [item.bucket, item]));
  assert.deepEqual(Object.keys(byBucket).sort(), ['many', 'one', 'zero']);
  assert.equal(byBucket.zero.observations.length, 0);
  assert.equal(byBucket.one.observations.length, 1);
  assert(byBucket.many.observations.length >= 2 && byBucket.many.observations.length <= relatedObservationLimit);
  assert(byBucket.many.edgeCount < observationEdgeLimit, 'Many witness must have a complete bounded edge enumeration');
  const valueQuantityCodeCounts = Object.fromEntries([...new Set(byBucket.many.observations
    .map((observation) => observation.valueQuantityCode).filter((value) => typeof value === 'string' && value.length > 0))]
    .map((value) => [value, byBucket.many.observations.filter((observation) => observation.valueQuantityCode === value).length]));
  const verifiedTargetCount = valueQuantityCodeCounts[targetValue] ?? 0;
  assert.equal(verifiedTargetCount, targetCount, 'Detailed raw Observation query changed the report-seeded target count');
  assert(verifiedTargetCount > 0 && verifiedTargetCount < byBucket.many.observations.length,
    `Catalog suggestion must match some but not all many-witness Observations; missing codes count as nonmatches: ${JSON.stringify({ targetValue, valueQuantityCodeCounts, observationCount: byBucket.many.observations.length })}`);
  return { witnesses, targetValue, valueQuantityCodeCounts,
    candidateCount: search.candidateCount, catalogSuggestionCount: search.catalogSuggestionCount };
};

const command = async (commands) => {
  await api(base + '/commands', {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(base + '/builder');
};

const documentForOutput = (state = builder) => state.workspace.documents.find((item) => item.output.id === outputId);
const withoutStageIdentity = (columns) => columns.map(({ columnId: _stageId, ...column }) => column);

const assertStableSourceProjection = (state, baseline, canonicalSourceColumnId, phase) => {
  const sourceColumnName = baseline.columns[0]?.column;
  const restored = documentForOutput(state).columns.find((column) => column.column === sourceColumnName);
  assert(restored, `${phase}: authored source column ${sourceColumnName} is missing`);
  assert.equal(restored.columnId, canonicalSourceColumnId,
    `${phase}: compiler-owned source column identity changed`);
  assert.deepEqual(withoutStageIdentity(documentForOutput(state).columns), withoutStageIdentity(baseline.columns),
    `${phase}: authored source column binding, label, physical name, or table presentation changed`);
};

const codeRule = (step) => step.operation.relatedExpand;
const typedEqualsValue = (choice, value) => choice.source.logicalType === 'code'
  ? { kind: 'CODE', code: { code: value } }
  : { kind: 'STRING', string: value };
const assertPersistedEqualsRule = (related, choice, value, emptyPolicy, phase) => {
  assert.equal(related.emptyPolicy, emptyPolicy, `${phase}: no-match policy changed`);
  assert.deepEqual(related.contributorRule, {
    policy: 'ALL_MATCHES',
    predicate: { candidateId: choice.source.candidateId, operator: 'EQUALS', value: typedEqualsValue(choice, value) },
  }, `${phase}: typed Contributor EQUALS predicate changed`);
  assert.deepEqual(related.contributorSource, choice.source, `${phase}: signed Contributor source changed`);
  assert.equal(related.contributorChoiceId, choice.choiceId, `${phase}: selected Contributor choice identity changed`);
  assert(related.contributorSource.path.endsWith('valueQuantity.code'), `${phase}: wrong Contributor field path`);
  assert.equal(related.contributorSource.resourceType, 'Observation', `${phase}: wrong Contributor resource type`);
  assert.equal(related.contributorSource.logicalType, choice.source.logicalType, `${phase}: returned Contributor logical type changed`);
  assert.equal(related.contributorRule.predicate.candidateId, related.contributorSource.candidateId,
    `${phase}: predicate and signed source candidate identities diverged`);
};

const rowsForValueQuantityCode = (witnesses, value, preserveParent) => witnesses.flatMap((item) => {
  const matching = item.observations.filter((observation) => observation.valueQuantityCode === value)
    .map((observation) => [item.patient.id, observation.id]);
  return matching.length || !preserveParent ? matching : [[item.patient.id, '—']];
});

const displayedRows = async () => inspectDOM(page, async args => { return [...document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]')].slice(1)
    .map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())).filter(row=>row.length); });

const revealControl = async (selector, includes) => {
  let locator = page.locator(selector);
  if (includes !== undefined) locator = locator.filter({ hasText: includes });
  const label = `Reveal ${includes ?? selector}`;
  await performAction(page, label, locator, (target, options) => target.scrollIntoViewIfNeeded(options));
  const box = await locator.boundingBox();
  assert(box, `${label}: control has no visible bounding box after Playwright scrolling`);
  const viewport = page.viewportSize();
  assert(box.y >= 0 && box.y + box.height <= viewport.height,
    `Control remains outside the viewport after Playwright scrolling: ${JSON.stringify({ box, viewport })}`);
  return { top: box.y, bottom: box.y + box.height, viewportHeight: viewport.height };
};
const assertRows = (actual, expected, name) => {
  assert(expected.length <= 25, `${name}: oracle has ${expected.length} rows; this verifier requires a fully visible fixture`);
  const sortRows = (rows) => rows.map((row) => JSON.stringify(row)).sort();
  assert.deepEqual(sortRows(actual), sortRows(expected), `${name}: exact rows differ from the independent CDA oracle`);
};

const recordAction = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, ...details });
  return durationMs;
};

const rendered = async (expectedRows, name) => {
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === args.__template0 && !document.body.innerText.includes('Loading your table…')), { __template0: (String(Math.min(25, expectedRows.length) + 1)) });
  const rows = await displayedRows();
  assertRows(rows, expectedRows, name);
  return rows;
};

const proposal = async (name, startedAt, expectedRows) => {
  await waitForDOM(page, args => Boolean(['ready','error','needs-repair'].includes(document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus)));
  const result = await inspectDOM(page, async args => { const panel=document.querySelector('[data-testid="construction-proposal-panel"]');return {
      status:panel?.dataset.proposalStatus,text:panel?.innerText,
      rows:[...document.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
        .map(row=>[...row.querySelectorAll('td')].map(cell=>cell.innerText.trim()))}; });
  assert.equal(result.status, 'ready', `${name}: ${result.text}`);
  assertRows(result.rows, expectedRows, name);
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, rowCount: expectedRows.length, preview: result.rows });
  return result;
};

const open = async (expectedRows, name) => {
  const startedAt = Date.now();
  await navigatePage(page, pageURL);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-table-'+args.__template0+'"]')), { __template0: (outputId) });
  await clickControl(page, `[data-testid="construction-table-${outputId}"]`);
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false));
  const rows = await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: rows.length });
};

const startRelatedExpand = async () => {
  const startedAt = Date.now();
  await clickControl(page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForDOM(page, args => Boolean(document.querySelector('[data-testid="construction-action-related-rows"]')?.disabled === false));
  await clickControl(page, '[data-testid="construction-action-related-rows"]');
  const panel = '[data-testid="construction-related-expand-editor"]';
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' select[aria-label="Related record type"]')?.disabled === false), { __template0: (panel) });
  await selectControl(page, `${panel} select[aria-label="Related record type"]`, 'Observation');
  const route = 'Patient <-[subject]- Observation';
  await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)), { __template0: (`${panel} input[aria-label="${route}"]`) });
  await clickControl(page, `${panel} input[aria-label="${route}"]`);
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' [data-testid="construction-related-expand-contributor-options"]')), { __template0: (panel) });
  recordAction('open-related-expand-editor', startedAt);
  return panel;
};

const waitForContributorSearch = async (startedAt) => {
  const entry = await requestCapture.waitFor(item => item.startedAt >= startedAt && item.path === `${base}/related-expand-contributors` && item.body?.query === 'valueQuantity.code' && item.response, { timeoutMs: Math.max(1, startedAt + 5000 - Date.now()) });
  return entry.response;
};
const chooseContributorValueQuantityCodeEquals = async (panel, oracle, actionName) => {
  const startedAt = Date.now();
  const options = `${panel} [data-testid="construction-related-expand-contributors"]`;
  await selectControl(page, `${panel} select[aria-label="If a current row has no matches"]`, 'PRESERVE_PARENT');
  const disclosure = `${panel} [data-testid="construction-related-expand-contributor-options"]`;
  await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)), { __template0: (disclosure+' summary') });
  const disclosureState = await inspectDOM(page, async args => { return document.querySelector(args.__template0)?.open ?? false; }, { __template0: (disclosure) });
  if (!disclosureState) {
    await revealControl(`${disclosure} summary`);
    await clickControl(page, `${disclosure} summary`);
    await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)?.open === true), { __template0: (disclosure) });
  }
  const onlyRecords = `${options} label`;
  await revealControl(onlyRecords, 'Only records meeting a condition');
  await clickControl(page, onlyRecords, { includes: 'Only records meeting a condition' });
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' input[placeholder="Search field name or path"]')), { __template0: (options) });
  const search = `${options} input[placeholder="Search field name or path"]`;
  await revealControl(search);
  await clickControl(page, search);
  const searchStartedAt = Date.now();
  await fillControl(page, search, 'valueQuantity.code');
  await waitForDOM(page, args => Boolean([...document.querySelectorAll(''+args.__template0+' [role="group"][aria-label="Fields for related-record condition"] button')]
      .some(button=>button.innerText.replace(/\\s+/g,' ').includes('valueQuantity.code'))), { __template0: (options) });
  const choiceResponse = await waitForContributorSearch(searchStartedAt);
  const visibleChoices = await inspectDOM(page, async args => { return [...document.querySelectorAll(''+args.__template0+' [role="group"][aria-label="Fields for related-record condition"] button')]
      .map(button=>({text:button.innerText.trim(),pressed:button.getAttribute('aria-pressed')})); }, { __template0: (options) });
  const manyWitness = oracle.witnesses.find((item) => item.bucket === 'many');
  const choice = choiceResponse.choices.find((candidate) => candidate.source.path.endsWith('valueQuantity.code')
    && candidate.source.resourceType === 'Observation'
    && ['string', 'code'].includes(candidate.source.logicalType)
    && candidate.operators.includes('EQUALS')
    && candidate.suggestedValues.includes(oracle.targetValue));
  assert(choice, `Native Observation.valueQuantity.code choice did not return the bounded oracle's catalog suggestion: ${JSON.stringify({
    choices: choiceResponse.choices.map((candidate) => ({ path: candidate.source.path, logicalType: candidate.source.logicalType,
      resourceType: candidate.source.resourceType, operators: candidate.operators, suggestedValueCount: candidate.suggestedValues.length })),
    targetValue: oracle.targetValue,
    valueQuantityCodeCounts: oracle.valueQuantityCodeCounts,
  })}`);
  assert(choice.choiceId && choice.source.candidateId && choice.source.nodeId,
    'Native Contributor catalog choice omitted its signed choice or exact candidate identity');
  assert.equal(choice.source.kind, 'FIELD', 'Contributor valueQuantity.code must be a native scalar field choice');
  assert(choice.source.logicalType === 'string' || choice.source.logicalType === 'code',
    `Record the exact supported source logical type without treating string as code: ${choice.source.logicalType}`);
  assert.equal(choice.suggestionsSource, 'catalog', 'Contributor value suggestions must come from the native source catalog');
  const targetValue = oracle.targetValue;
  const matchedCount = oracle.valueQuantityCodeCounts[targetValue] ?? 0;
  assert(matchedCount > 0 && matchedCount < manyWitness.observations.length,
    'Returned native suggestion must match some but not all many-witness Observations');
  const fieldSummary = { path: choice.source.path, resourceType: choice.source.resourceType,
    logicalType: choice.source.logicalType, operators: choice.operators,
    suggestionsComplete: choice.suggestionsComplete, suggestionsSource: choice.suggestionsSource,
    suggestedValueCount: choice.suggestedValues.length };
  assert(visibleChoices.some((candidate) => candidate.text.split('\n')[0].trim() === choice.label
    && candidate.text.split('\n')[1]?.includes('valueQuantity.code')
    && candidate.text.split('\n')[1]?.includes(choice.source.logicalType)),
  `The native valueQuantity.code choice was not offered as an actionable field: ${JSON.stringify(visibleChoices)}`);
  report.contributorChoice = fieldSummary;
  const contributorFieldButtons = `${options} [role="group"][aria-label="Fields for related-record condition"] button`;
  const fieldIdentity = `${choice.source.path} · ${choice.source.logicalType}`;
  await revealControl(contributorFieldButtons, fieldIdentity);
  await clickControl(page, contributorFieldButtons, { includes: fieldIdentity });
  await waitForDOM(page, args => Boolean(document.querySelector(''+args.__template0+' select')?.value === 'EXISTS'), { __template0: (options) });
  await revealControl(`${options} select`);
  await selectControl(page, `${options} select`, 'EQUALS');
  const valueLabel = choice.source.logicalType === 'code' ? 'Code' : 'Exact value';
  await waitForDOM(page, args => Boolean(Boolean([...document.querySelectorAll(''+args.__template0+' label')].find(label=>label.innerText.trim().startsWith(args.__template1))?.querySelector('input'))), { __template0: (options), __template1: (valueLabel) });
  await revealControl(`${options} label`, valueLabel);
  const suggestions = `${options} [aria-label="Catalog value suggestions"] button`;
  await waitForDOM(page, args => Boolean(Boolean([...document.querySelectorAll(args.__template0)]
    .find(button=>button.innerText.trim()===args.__template1))), { __template0: (suggestions), __template1: (targetValue) });
  await revealControl(suggestions, targetValue);
  await clickControl(page, suggestions, { name: targetValue });
  await waitForDOM(page, args => Boolean(([...document.querySelectorAll(''+args.__template0+' label')].find(label=>label.innerText.trim().startsWith(args.__template1))?.querySelector('input')?.value === args.__template2)), { __template0: (options), __template1: (valueLabel), __template2: (targetValue) });
  const selected = await inspectDOM(page, async args => { return {condition:document.querySelector(''+args.__template0+' select')?.value,
      field:document.querySelector(''+args.__template1+' [aria-pressed="true"]')?.innerText.trim(),
      value:[...document.querySelectorAll(''+args.__template2+' label')].find(label=>label.innerText.trim().startsWith(args.__template3))?.querySelector('input')?.value}; }, { __template0: (options), __template1: (options), __template2: (options), __template3: (valueLabel) });
  assert.equal(selected.condition, 'EQUALS');
  assert(selected.field?.includes('valueQuantity.code') && selected.field?.includes(choice.source.logicalType), JSON.stringify(selected));
  assert.equal(selected.value, targetValue);
  report.contributorRule = { condition: selected.condition, source: fieldSummary,
    selectedCatalogValue: selected.value, valueKind: typedEqualsValue(choice, selected.value).kind,
    noMatchPolicy: 'PRESERVE_PARENT' };
  recordAction(actionName, startedAt, { condition: selected.condition, sourceField: 'valueQuantity.code',
    logicalType: choice.source.logicalType,
    selectedFromCatalog: true, noMatchPolicy: 'PRESERVE_PARENT' });
  return { condition: selected.condition, value: selected.value, choice, targetValue };
};

const beginEdit = async (stepId) => {
  const startedAt = Date.now();
  await clickControl(page, `[data-testid="construction-history-step-${stepId}"]`);
  await clickControl(page, `[data-testid="construction-edit-step-${stepId}"]`);
  const policy = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
  await waitForDOM(page, args => Boolean(document.querySelector(args.__template0)), { __template0: (policy+':not(:disabled)') });
  await revealControl(policy);
  recordAction('edit-saved-step-to-policy-control', startedAt);
};

const applyProposal = async (expectedRows, name) => {
  const startedAt = Date.now();
  await clickControl(page, '[data-testid="construction-apply-proposal"]');
  await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')));
  await rendered(expectedRows, name);
  recordAction(name, startedAt, { rowCount: expectedRows.length });
  builder = await api(base + '/builder');
};


report.target ??= { ...cda.report.target };
report.nativeRequests = cda.report.nativeRequests;
report.errors = cda.report.errors;

  try {

const oracle = await sourceWitnesses();
const witnesses = oracle.witnesses;
report.oracle = {
  project,
  generation,
  relationship: 'Observation --subject_Patient--> Patient',
  candidatePatientCount: oracle.candidateCount,
  candidatePatientLimit,
  relatedObservationLimit,
  observationEdgeLimit,
  catalogSuggestionCount: oracle.catalogSuggestionCount,
  targetCatalogSuggestion: oracle.targetValue,
  manyValueQuantityCodeCounts: oracle.valueQuantityCodeCounts,
  witnesses,
  countByBucket: Object.fromEntries(witnesses.map((item) => [item.bucket, item.observations.length])),
};
const baselineRows = witnesses.map((item) => [item.patient.id]);

await api(root, { name: explorer, title: 'Contributor code lifecycle QA' });
builder = await api(base + '/builder');
assert.equal(builder.catalog.generation, generation);
const patientNode = builder.catalog.nodes.find((node) => node.resourceType === 'Patient');
assert(patientNode, 'Patient source type is absent from the current catalog');
await command([{ type: 'CREATE_TABLE', title: 'Contributor code QA', rootNodeId: patientNode.nodeId }]);
outputId = builder.workspace.documents[0].output.id;
const patientIDField = builder.catalog.candidates.find((candidate) => candidate.nodeId === patientNode.nodeId && candidate.fieldPath === 'id');
assert(patientIDField, 'Patient ID is absent from the current catalog');
await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: patientIDField.candidateId,
  projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Patient ID' }]);
const selection = await api(base.replace('/authoring/v2', '/selections'), {
  snapshotToken: builder.catalog.snapshotToken,
  idempotencyKey: explorer,
  source: { kind: 'resources', resources: { refs: witnesses.map(({ patient }) => ({
    project, generation, resourceType: 'Patient', id: patient.id,
  })) } },
});
assert.equal(selection.memberCount, 3, 'Selection must contain the three independently witnessed source rows');
const routes = await api(base + '/population-routes', {
  snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 50,
});
const direct = routes.choices.find((choice) => choice.route.length === 0);
assert(direct, 'Selected Patient resources have no direct population route');
await command([{ type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selection.id, routeChoiceId: direct.routeChoiceId }]);
const original = structuredClone(documentForOutput(builder));
assert.equal(original.population.selectionRevisionId, selection.id);

page.on('request', request => {
  const url = new URL(request.url());
  if (url.pathname.includes('cda-builder-full-qa-1790440983382')) report.errors.push({ kind: 'protected-explorer-request', path: url.pathname });
});
requestCapture = cda.captureRequests(`${root}/${explorer}`, { responsePaths: /related-expand-contributors|commands|construction-proposals|preview/, });
page.on('request', request => {
  if (new URL(request.url()).pathname === `${base}/related-expand-contributors`) {
    const entry = requestCapture.byRequest.get(request);
    if (entry) contributorSearchRequests.push(entry);
  }
});

await open(baselineRows, 'source-selection-zero-one-many');
let panel = await startRelatedExpand();
const cancelledChoice = await chooseContributorValueQuantityCodeEquals(panel, oracle, 'configure-valuequantity-code-contributor-controls');
const cancelledPreserveRows = rowsForValueQuantityCode(witnesses, cancelledChoice.targetValue, true);
const cancelledExcludedRows = rowsForValueQuantityCode(witnesses, cancelledChoice.targetValue, false);
assert(cancelledPreserveRows.length > cancelledExcludedRows.length,
  'PRESERVE_PARENT must retain at least one independently witnessed patient without the selected valueQuantity.code');
let startedAt = Date.now();
await proposal('contributor-valuequantity-code-equals-preview', startedAt, cancelledPreserveRows);
const beforeCancel = await api(base + '/builder');
startedAt = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), {}, 5000);
await rendered(baselineRows, 'cancel-contributor-preview');
recordAction('cancel-contributor-preview', startedAt, { rowCount: baselineRows.length });
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeCancel.workspace, 'Cancel must leave the saved workspace unchanged');
assert.deepEqual(documentForOutput(builder), original, 'Cancel must preserve the exact source table');
report.cases.push({ name: 'contributor-rule-cancel-preserves-source', workspaceUnchanged: true });

panel = await startRelatedExpand();
const selectedChoice = await chooseContributorValueQuantityCodeEquals(panel, oracle, 'reconfigure-valuequantity-code-after-cancel');
assert.equal(selectedChoice.targetValue, cancelledChoice.targetValue,
  'The same native catalog suggestion must be selected after Cancel');
assert.equal(selectedChoice.choice.choiceId, cancelledChoice.choice.choiceId,
  'The same signed field choice must be selected after Cancel');
assert.deepEqual(selectedChoice.choice.source, cancelledChoice.choice.source,
  'The same signed source field must be selected after Cancel');
const preserveRows = rowsForValueQuantityCode(witnesses, selectedChoice.targetValue, true);
const excludedRows = rowsForValueQuantityCode(witnesses, selectedChoice.targetValue, false);
report.oracle.selectedCatalogValue = selectedChoice.targetValue;
report.oracle.matchedValueQuantityCodeCountInManyWitness = oracle.valueQuantityCodeCounts[selectedChoice.targetValue];
assert(preserveRows.length > excludedRows.length,
  'PRESERVE_PARENT must retain the zero-match witness after applying the catalog-selected value');
startedAt = Date.now();
await proposal('confirmed-contributor-valuequantity-code-equals-preview', startedAt, preserveRows);
await applyProposal(preserveRows, 'apply-contributor-valuequantity-code-rule-to-render');
let relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assert(relatedStep, 'Applied contributor rule is missing from saved construction');
const sourceProjection = relatedStep.outputs.find((column) => column.name === original.columns[0].column);
assert(sourceProjection?.id, 'Applied related-expand step omitted the compiler-owned source projection identity');
const canonicalSourceColumnId = sourceProjection.id;
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'initial contributor Apply');
report.sourceColumnIdentity = {
  sourceColumnName: sourceProjection.name,
  compilerCanonicalSourceColumnId: canonicalSourceColumnId,
};
assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'PRESERVE_PARENT',
  'initial Contributor valueQuantity.code Apply');
report.persistedValueQuantityCodeCondition = {
  operator: 'EQUALS', valueKind: typedEqualsValue(selectedChoice.choice, selectedChoice.targetValue).kind,
  sourceLogicalType: selectedChoice.choice.source.logicalType, selectedCatalogValue: selectedChoice.targetValue,
  source: report.contributorChoice, signedSourcePersisted: true, selectedChoicePersisted: true,
  noMatchPolicy: 'PRESERVE_PARENT',
};
await open(preserveRows, 'reload-contributor-valuequantity-code-equals');

const beforeEdit = await api(base + '/builder');
assertStableSourceProjection(beforeEdit, original, canonicalSourceColumnId, 'reload after initial contributor Apply');
relatedStep = documentForOutput(beforeEdit).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'PRESERVE_PARENT',
  'reload after initial Contributor valueQuantity.code Apply');
await beginEdit(relatedStep.id);
const policySelector = '[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]';
assert.equal(await cda.inspect(async args => { return document.querySelector(args.__template0)?.value; }, { __template0: (policySelector) }), 'PRESERVE_PARENT');
startedAt = Date.now();
await nativeSelect(page, policySelector, 'EXCLUDE');
await proposal('edit-policy-exclude-preview', startedAt, excludedRows);
await applyProposal(excludedRows, 'apply-exclude-policy-to-render');
relatedStep = documentForOutput(builder).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'edited EXCLUDE policy Apply');
assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'EXCLUDE',
  'edited EXCLUDE policy Apply');
report.persistedValueQuantityCodeCondition.policyEditKeptCondition = true;
report.persistedValueQuantityCodeCondition.noMatchPolicyAfterEdit = 'EXCLUDE';
await open(excludedRows, 'reload-edited-exclude-policy');

const beforeRemoval = await api(base + '/builder');
assertStableSourceProjection(beforeRemoval, original, canonicalSourceColumnId, 'reload after edited EXCLUDE policy');
relatedStep = documentForOutput(beforeRemoval).construction.steps.find((step) => step.operation.kind === 'RELATED_EXPAND');
assertPersistedEqualsRule(codeRule(relatedStep), selectedChoice.choice, selectedChoice.targetValue, 'EXCLUDE',
  'reload after edited EXCLUDE policy');
const remove = async () => {
  await nativeClick(page, `[data-testid="construction-history-step-${relatedStep.id}"]`, {});
  startedAt = Date.now();
  await nativeClick(page, `[data-testid="construction-remove-step-${relatedStep.id}"]`, {});
  await proposal('remove-contributor-rule-preview', startedAt, baselineRows);
};
await remove();
startedAt = Date.now();
await nativeClick(page, '[data-testid="construction-cancel-proposal"]', {});
await waitForDOM(page, args => Boolean(!document.querySelector('[data-testid="construction-proposal-panel"]')), {}, 5000);
await rendered(excludedRows, 'cancel-remove-to-render');
recordAction('cancel-remove-to-render', startedAt, { rowCount: excludedRows.length });
builder = await api(base + '/builder');
assert.deepEqual(builder.workspace, beforeRemoval.workspace, 'Cancel removal must preserve the authored Contributor rule');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'cancel remove');
report.cases.push({ name: 'remove-cancel-preserves-contributor-rule', workspaceUnchanged: true });
await remove();
await applyProposal(baselineRows, 'apply-remove-to-render');
const restored = documentForOutput(builder);
assert.deepEqual(restored.construction?.steps ?? [], original.construction?.steps ?? [],
  'Removing Contributor rules must restore the exact authored source construction');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'remove Apply');
assert.deepEqual(restored.rows, original.rows, 'Removing Contributor rules must restore the exact row definition');
assert.deepEqual(restored.population, original.population, 'Removing Contributor rules must restore the exact selected population');
await open(baselineRows, 'reload-restored-source-table');
builder = await api(base + '/builder');
assertStableSourceProjection(builder, original, canonicalSourceColumnId, 'reload after Contributor removal');
await requestCapture.flush();
assert.deepEqual(report.errors, [], 'Unexpected browser errors were reported');
assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected page errors were reported');
assert.deepEqual(cda.diagnostics.console, [], 'Unexpected console errors were reported');
assert.deepEqual(cda.diagnostics.networkFailures, [], 'Unexpected network failures were reported');
assert.deepEqual(cda.diagnostics.httpFailures, [], 'Unexpected HTTP failures were reported');
report.status = 'passed';
  } finally {
    try { await requestCapture?.flush(); } catch (error) { report.requestFlushError = String(error); }
    cda.includeBrowserDiagnostics();
    report.finished = new Date().toISOString();
    await cda.attachReport(`standalone-${cda.caseName}-domain.json`, {
      ...cda.report,
      domain: report,
    });
  }
}
