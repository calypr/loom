import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { classifyNullableSourceScalar, expectedNullableRelatedAll } from '../helpers/nullable-related-all.mjs';
import { captureSourceFreeze } from '../helpers/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp, ApiBuildFreezeError } from '../helpers/api-build-freeze.mjs';

import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from '../helpers/owned-cda-target.mjs';

export const createRelatedFieldAfterUnpivotChecks = (cda, caseName) => {
  const recordLifecycleCheck = (name, passed, evidence = {}) =>
    cda.check('correctness', name, Boolean(passed), evidence);
  const recordGenderAllLifecycleCheck = (name, passed, evidence = {}) => {
    if (caseName === 'gender-all') return recordLifecycleCheck(name, passed, evidence);
  };
  const recordNoUnexpectedNativeErrorsCheck = report =>
    recordLifecycleCheck(
      'No unexpected native HTTP or network errors occurred',
      report.errors.length === 0,
      { domainErrors: report.errors });
  return { recordLifecycleCheck, recordGenderAllLifecycleCheck, recordNoUnexpectedNativeErrorsCheck };
};

export async function runRelatedFieldAfterUnpivotBrowserWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const project = cda.project;
const generation = (cda.generation ?? cda.env?.LOOM_CDA_GENERATION);
const afterUnpivotCase = originalArgs.caseName ?? cda.caseName ?? (cda.env?.LOOM_RELATED_AFTER_UNPIVOT_CASE ?? process.env.LOOM_RELATED_AFTER_UNPIVOT_CASE) ?? 'gender-all';
assert(['gender-all', 'gender-null-all', 'resource-type-all', 'id-count'].includes(afterUnpivotCase), `Unsupported LOOM_RELATED_AFTER_UNPIVOT_CASE: ${afterUnpivotCase}`);
const nullableGenderCase = afterUnpivotCase === 'gender-null-all';
const knownGenderWitnessReport = (cda.env?.LOOM_CDA_GENDER_NULL_WITNESS_REPORT ?? process.env.LOOM_CDA_GENDER_NULL_WITNESS_REPORT) ?? '/tmp/loom-related-resource-type-after-unpivot-native-complete/report.json';
const afterUnpivotFieldPath = afterUnpivotCase === 'id-count'
  ? 'id'
  : afterUnpivotCase === 'resource-type-all' ? 'resourceType' : 'gender';
const afterUnpivotForm = afterUnpivotCase === 'id-count' ? 'COUNT' : 'ALL';
const requireFieldWitness = afterUnpivotCase === 'gender-all' || afterUnpivotCase === 'resource-type-all';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = cda.explorer;
assert.notEqual(explorer, protectedExplorer);
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const apiContainer = (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER);
const composeProject = (cda.target.composeProject ?? cda.env?.LOOM_CDA_COMPOSE_PROJECT);
const arangoContainer = (cda.target.arangoContainer ?? cda.env?.LOOM_ARANGO_CONTAINER);
assert(apiOrigin && uiOrigin, 'Set LOOM_CDA_API_ORIGIN and LOOM_CDA_UI_ORIGIN to the isolated CDA stack.');
assert.equal(generation, 'cda-fhir-v1', 'Set LOOM_CDA_GENERATION to the loaded CDA FHIR generation.');
const sourceRoot = fileURLToPath(new URL('../../..', import.meta.url));
const ownedTarget = await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
const apiRoot = `/api/v1/projects/${project}/explorers`;
const base = `${apiRoot}/${explorer}/authoring/v2`;
const report = {
  status: 'running',
  explorer,
  project,
  generation,
  scenario: afterUnpivotCase === 'id-count'
    ? 'Patient.id RELATED_SOURCE ALL before UNPIVOT; unpivot Specimen ID while retaining the Patient.id array; add Patient.id COUNT after UNPIVOT on the same exact Specimen.subject->Patient route.'
    : `Patient.id RELATED_SOURCE ALL before UNPIVOT; unpivot Specimen ID while retaining the Patient.id array; add Patient.${afterUnpivotFieldPath} ALL after UNPIVOT on the same exact Specimen.subject->Patient route${nullableGenderCase ? ' using the pinned scoped witness with a missing or explicit-null gender' : ''}.`,
  coverageLimitations: nullableGenderCase
    ? ['This separate mode uses the exact scoped Specimen/Patient identity from the passed resourceType-after-Unpivot raw-oracle report, then rereads that source and records whether gender is absent or explicitly null. It does not require or claim a populated gender witness.', 'One linked Patient with a missing or explicit-null gender must project as ALL [null]; an empty related population has ALL []. The pinned CDA witness has one direct Patient, so the empty-population policy is checked only by the offline helper test.', 'Basic-fixture Builder behavior, populated Patient.gender, restricted-auth, and Pivot remain untested.']
    : afterUnpivotCase === 'id-count'
      ? ['This case proves retained Patient.id ALL values across UNPIVOT and same-route Patient.id COUNT after UNPIVOT. It does not prove non-id Patient field-value access after UNPIVOT.', 'The bounded first-2,000-Specimen inventory found project_id and resourceType as populated Patient payload scalars besides id; this is not a project-wide absence claim.']
      : [`The ${afterUnpivotFieldPath} case requires a populated Patient.${afterUnpivotFieldPath} witness in the bounded candidate scan. A missing witness does not establish project-wide absence.`, 'This verifier does not claim restricted-auth coverage.'],
  authorizationClaim: 'Exact selected resource membership is checked. The local project-scoped CDA oracle does not claim restricted-auth coverage.',
  started: new Date().toISOString(),
  protectedExplorerUntouched: true,
  cases: [],
  nativeRequests: [],
  errors: [],
};
const fixtureRequestCapture = cda.captureRequests(base, { apiOrigin: uiOrigin });
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(sourceRoot);
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };
let frozenApiBuild;

let builder;
let outputId;

const api = async (path, body) => {
  assert(!path.includes(protectedExplorer), `Refusing to address protected Explorer ${protectedExplorer}.`);
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `related-field-after-unpivot-${randomUUID()}` },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert(response.ok, `${path}: ${JSON.stringify(value)}`);
  return value;
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
  return builder;
};

const doc = (state = builder) => state.workspace.documents.find((document) => document.output.id === outputId);
const steps = (state = builder) => doc(state).construction.steps;
const record = (name, startedAt, details = {}) => {
  const durationMs = Date.now() - startedAt;
  assert(durationMs <= 5000, `${name} took ${durationMs}ms`);
  report.cases.push({ name, durationMs, ...details });
};
const { recordLifecycleCheck, recordGenderAllLifecycleCheck, recordNoUnexpectedNativeErrorsCheck } =
  createRelatedFieldAfterUnpivotChecks(cda, afterUnpivotCase);
report.ownedTarget = ownedTarget;
let requestCapture;
const inspect = callback => page.evaluate(callback);
const waitUI = (condition, argument, timeout = 5000) => page.waitForFunction(condition, argument, { timeout });
const navigateUI = url => {
  const targetLocator = page.locator('body');
  report.lastAction = { label: 'Navigate to Builder', locator: targetLocator.toString(), targetLocator, startedAt: Date.now() };
  return page.goto(url, { waitUntil: 'commit', timeout: 5000 });
};
const clickUI = (selector, options = {}) => {
  let locator = page.locator(selector);
  if (options.name) locator = locator.and(page.getByRole('button', { name: options.name, exact: true }));
  if (options.includes) locator = locator.and(page.getByRole('button', { name: new RegExp(options.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') }));
  const label = options.name ?? options.includes ?? selector;
  report.lastAction = { label, locator: locator.toString(), targetLocator: locator, startedAt: Date.now() };
  return cda.action(label, locator, target => target.click({ timeout: 5000 }), { timeout: 5000, budget: 5000 });
};
const selectUI = (selector, value) => {
  const locator = page.locator(selector);
  report.lastAction = { label: `Select ${value}`, locator: locator.toString(), targetLocator: locator, startedAt: Date.now() };
  return cda.action(`Select ${value}`, locator, target => target.selectOption(value, { timeout: 5000 }), { timeout: 5000, budget: 5000 });
};
const requestBody = entry => requestCapture.rawRequestBody(entry) ?? entry.body;
const responseBody = entry => requestCapture.rawResponseBody(entry) ?? entry.response;
const endpointOf = entry => entry.path?.slice(base.length) ?? '';

const rawOracleFailure = (message, result, phase) => {
  const error = new Error(message);
  error.name = 'RawOracleUnavailableError';
  error.rawOracleFailure = true;
  error.unverifiedKind = 'raw-source-command';
  error.productFailure = false;
  error.diagnostics = {
    phase,
    status: result.status,
    signal: result.signal ?? undefined,
    spawnError: result.error ? {
      name: result.error.name,
      code: result.error.code,
      message: result.error.message,
    } : undefined,
    stderrTail: result.stderr?.slice(-1000) || undefined,
    stdoutTail: result.status !== 0 ? (result.stdout?.slice(-1000) || undefined) : undefined,
    stdoutBytes: Buffer.byteLength(result.stdout ?? ''),
  };
  return error;
};

const rawQuery = (query, bindVariables = {}) => {
  const container = arangoContainer;
  const remotePath = `/tmp/loom-cda-raw-oracle-${randomUUID()}.js`;
  let localDirectory;
  let rows;
  let failure;
  try {
    localDirectory = mkdtempSync(join(tmpdir(), 'loom-cda-raw-oracle-'));
    const localPath = join(localDirectory, 'query.js');
    const program = `const rows = db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVariables)}).toArray();\nprint(JSON.stringify(rows));\n`;
    writeFileSync(localPath, program);

    const copy = spawnSync('docker', ['cp', localPath, `${container}:${remotePath}`], {
      encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024,
    });
    if (copy.status !== 0 || copy.error) throw rawOracleFailure('Could not copy the bounded raw-source program into the Arango container.', copy, 'docker-copy');

    const result = spawnSync('docker', [
      'exec', container, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute', remotePath,
    ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
    if (result.status !== 0 || result.error) throw rawOracleFailure('Arango raw-source program did not complete.', result, 'arangosh-execute-file');
    const jsonStart = result.stdout.indexOf('[');
    if (jsonStart < 0) throw rawOracleFailure('Arango raw-source program returned no JSON array.', result, 'arangosh-execute-file');
    try {
      rows = JSON.parse(result.stdout.slice(jsonStart));
      if (!Array.isArray(rows)) throw new Error('Raw-source query result is not an array.');
    } catch (cause) {
      const error = rawOracleFailure('Arango raw-source program returned invalid JSON.', result, 'arangosh-execute-file');
      error.cause = String(cause);
      throw error;
    }
  } catch (error) {
    failure = error.rawOracleFailure ? error : rawOracleFailure('Could not prepare the bounded raw-source program.', {
      status: null,
      error: { name: error.name, code: error.code, message: error.message },
      stdout: '', stderr: '',
    }, 'local-script');
  } finally {
    let localCleanupFailure;
    try {
      if (localDirectory) rmSync(localDirectory, { recursive: true, force: true });
    } catch (error) {
      localCleanupFailure = rawOracleFailure('Could not remove the local temporary raw-source program.', {
        status: null, error: { name: error.name, code: error.code, message: error.message }, stdout: '', stderr: '',
      }, 'local-cleanup');
    } finally {
      let cleanup;
      try {
        cleanup = spawnSync('docker', ['exec', container, 'rm', '-f', remotePath], {
          encoding: 'utf8', timeout: 10000, maxBuffer: 1024 * 1024,
        });
      } catch (error) {
        cleanup = { status: null, error: { name: error.name, code: error.code, message: error.message }, stdout: '', stderr: '' };
      }
      if (cleanup.status !== 0 || cleanup.error) {
        const cleanupFailure = rawOracleFailure('Could not remove the owned temporary raw-source program from the Arango container.', cleanup, 'container-cleanup');
        if (failure) failure.diagnostics.cleanup = cleanupFailure.diagnostics;
        else failure = cleanupFailure;
      }
    }
    if (localCleanupFailure) {
      if (failure) failure.diagnostics.localCleanup = localCleanupFailure.diagnostics;
      else failure = localCleanupFailure;
    }
  }
  if (failure) throw failure;
  return rows;
};

const scopedSpecimenCandidatesQuery = `FOR s IN Specimen
  FILTER s.project == @project AND s.dataset_generation == @generation
  SORT s._key
  LIMIT 2000
  RETURN { id: s.id, _id: s._id, project: s.project, generation: s.dataset_generation }`;

const pinnedSpecimenCandidateQuery = `FOR s IN Specimen
  FILTER s.id == @specimenID
    AND s.project == @project
    AND s.dataset_generation == @generation
  SORT s._key
  LIMIT 2
  RETURN { id: s.id, _id: s._id, project: s.project, generation: s.dataset_generation }`;

const linkedPatientWitnessQuery = `FOR specimenKey IN @specimenKeys
  LET specimen = DOCUMENT(specimenKey)
  FILTER specimen != null
    AND specimen.project == @project
    AND specimen.dataset_generation == @generation
  LET patients = (
    FOR edge IN fhir_edge
      FILTER edge._from == specimen._id
        AND edge.label == "subject_Patient"
        AND edge.project == @project
        AND edge.dataset_generation == @generation
        AND STARTS_WITH(edge._to, "Patient/")
      LET patient = DOCUMENT(edge._to)
      FILTER patient != null
        AND patient.project == @project
        AND patient.dataset_generation == @generation
      RETURN DISTINCT { id: patient.payload.id, _id: patient._id, gender: patient.payload.gender, genderOwnProperty: HAS(patient.payload, "gender"), genderIsNull: IS_NULL(patient.payload.gender), genderValue: (HAS(patient.payload, "gender") ? patient.payload.gender : null), resourceType: patient.payload.resourceType }
  )
  FILTER LENGTH(patients) == 1
  FILTER IS_STRING(patients[0].id) AND LENGTH(patients[0].id) > 0
  FILTER @requiredField == "" OR (IS_STRING(patients[0][@requiredField]) AND LENGTH(patients[0][@requiredField]) > 0)
  FILTER @nullableGenderOnly == false OR (patients[0].genderOwnProperty == false OR patients[0].genderIsNull == true)
  SORT specimen._key
  LIMIT 1
  LET patient = patients[0]
  RETURN {
    specimen: { id: specimen.id, _id: specimen._id },
    patient: { id: patient.id, _id: patient._id, gender: patient.gender, genderOwnProperty: patient.genderOwnProperty, genderIsNull: patient.genderIsNull, genderValue: patient.genderValue, resourceType: patient.resourceType },
    patientCount: LENGTH(patients),
    route: "Specimen -[subject]-> Patient",
    project: specimen.project,
    generation: specimen.dataset_generation
  }`;

const exactLinkedPatientQuery = `LET specimen = DOCUMENT(@specimenKey)
  FILTER specimen != null
    AND specimen.id == @specimenID
    AND specimen.project == @project
    AND specimen.dataset_generation == @generation
  LET patients = (
    FOR edge IN fhir_edge
      FILTER edge._from == specimen._id
        AND edge.label == "subject_Patient"
        AND edge.project == @project
        AND edge.dataset_generation == @generation
        AND STARTS_WITH(edge._to, "Patient/")
      LET patient = DOCUMENT(edge._to)
      FILTER patient != null
        AND patient.project == @project
        AND patient.dataset_generation == @generation
      RETURN DISTINCT { id: patient.payload.id, _id: patient._id, gender: patient.payload.gender, genderOwnProperty: HAS(patient.payload, "gender"), genderIsNull: IS_NULL(patient.payload.gender), genderValue: (HAS(patient.payload, "gender") ? patient.payload.gender : null), resourceType: patient.payload.resourceType }
  )
  FILTER LENGTH(patients) == 1
  FILTER IS_STRING(patients[0].id) AND LENGTH(patients[0].id) > 0
  FILTER @requiredField == "" OR (IS_STRING(patients[0][@requiredField]) AND LENGTH(patients[0][@requiredField]) > 0)
  FILTER @nullableGenderOnly == false OR (patients[0].genderOwnProperty == false OR patients[0].genderIsNull == true)
  LET patient = patients[0]
  RETURN {
    specimen: { id: specimen.id, _id: specimen._id },
    patient: { id: patient.id, _id: patient._id, gender: patient.gender, genderOwnProperty: patient.genderOwnProperty, genderIsNull: patient.genderIsNull, genderValue: patient.genderValue, resourceType: patient.resourceType },
    patientCount: LENGTH(patients),
    route: "Specimen -[subject]-> Patient",
    project: specimen.project,
    generation: specimen.dataset_generation
  }`;

const beginNativeCapture = () => {
  requestCapture = captureCDARequests(page, {
    apiOrigin,
    browserRequestOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base,
    report,
  });
  page.on('request', request => {
    if (new URL(request.url()).pathname.includes(`/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
  });
};

const nativeView = entry => ({
  ...entry,
  endpoint: endpointOf(entry),
  request: requestBody(entry),
  response: responseBody(entry),
  complete: Boolean(entry.completedAt),
  outputId: requestBody(entry)?.outputId ?? new URL(entry.origin + entry.path).searchParams.get('outputId') ?? undefined,
});

const waitNative = async (endpoint, startedAt, predicate = () => true, timeoutMs = 5000) => {
  const entry = await requestCapture.waitFor(candidate => candidate.path === base + endpoint
    && candidate.startedAt >= startedAt
    && predicate(nativeView(candidate)), { timeout: timeoutMs });
  return nativeView(entry);
};

const panelInfo = async () => inspect(() => {
  const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
  const choice = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
  const panel = proposal ?? choice;
  return panel ? {
    selector: proposal ? 'construction-proposal-panel' : 'construction-choice-proposal-panel',
    status: panel.dataset.proposalStatus,
    proposalId: panel.dataset.proposalId,
    text: panel.innerText,
  } : null;
});

const previewValueFor = (preview, label, rowIndex) => {
  const column = preview.columns?.find((candidate) => candidate.label === label);
  assert(column, `Preview omitted column label ${JSON.stringify(label)}: ${JSON.stringify(preview.columns)}`);
  assert(preview.rows?.[rowIndex], `Preview omitted row ${rowIndex}`);
  return preview.rows[rowIndex][column.column];
};

// Matches PreviewTable's formatPreviewCell for this verifier's string and integer values.
const formatPreviewCell = (value) => {
  if (value === undefined || value === null) return '—';
  if (typeof value === 'string') return value.trim() || '—';
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value);
  if (Array.isArray(value)) {
    const items = value.map(formatPreviewCell).filter((item) => item !== '—');
    return items.length > 0 ? items.join('; ') : '—';
  }
  throw new TypeError(`Unexpected preview value type in focused verifier: ${Object.prototype.toString.call(value)}`);
};

const assertPreviewRows = (response, expectedRows, name) => {
  assert.equal(response.status, 200, `${name}: proposal HTTP status`);
  assert.equal(response.response?.previewStatus, 'READY', `${name}: ${JSON.stringify(response.response)}`);
  const preview = response.response.preview;
  assert(preview?.receiptId, `${name}: preview receipt missing`);
  assert.equal(preview.receiptId, response.response.proposalId, `${name}: preview must belong to the current proposal`);
  assert.equal(preview.rowCount, expectedRows.length, `${name}: ${JSON.stringify(preview)}`);
  assert.equal(preview.rows.length, expectedRows.length, `${name}: bounded fixture must include every row`);
  const labels = preview.columns.map((column) => column.label);
  assert.equal(new Set(labels).size, labels.length, `${name}: preview labels must be unique for oracle and DOM matching: ${JSON.stringify(labels)}`);
  const expectedLabels = [...new Set(expectedRows.flatMap((row) => Object.keys(row)))];
  assert.deepEqual([...labels].sort(), [...expectedLabels].sort(), `${name}: oracle columns must exactly match the declared preview columns`);
  const projected = preview.rows.map((row, rowIndex) => Object.fromEntries(labels.map((label) => [
    label, previewValueFor(preview, label, rowIndex),
  ])));
  assert.deepEqual(
    projected.map((row) => JSON.stringify(row)).sort(),
    expectedRows.map((row) => JSON.stringify(row)).sort(),
    `${name}: native protocol values differ from the scoped raw CDA oracle`,
  );
  return preview;
};

const proposal = async (name, startedAt, expectedRowsOrFactory) => {
  await waitUI(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]')
      ?? document.querySelector('[data-testid="construction-choice-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  });
  const panel = await panelInfo();
  assert(panel, `${name}: no proposal panel`);
  assert.equal(panel.status, 'ready', `${name}: ${panel.text}`);
  const request = await waitNative('/construction-proposals', startedAt,
    entry => entry.response?.proposalId === panel.proposalId,
    Math.max(1, startedAt + 5000 - Date.now()));
  const expectedRows = typeof expectedRowsOrFactory === 'function'
    ? expectedRowsOrFactory(request.response)
    : expectedRowsOrFactory;
  const preview = assertPreviewRows(request, expectedRows, name);
  record(name, startedAt, { proposalId: request.response.proposalId, rowCount: preview.rowCount });
  return { panel, request, preview, expectedRows };
};

const visibleTable = async (expectedRows, preview, name, startedAt) => {
  const labels = preview.columns.map((column) => column.label);
  const rowCount = expectedRows.length;
  const columnCount = labels.length;
  await waitUI(({ rowCount, columnCount }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const dataRows = [...(table?.querySelectorAll('[role="row"]') ?? [])]
        .filter((row) => row.querySelector('[role="cell"]'));
      return table?.getAttribute('aria-rowcount') === String(Math.min(25, rowCount) + 1)
        && table?.getAttribute('aria-colcount') === String(columnCount)
        && dataRows.length === rowCount
        && !document.body.innerText.includes('Loading your table…')
        && !document.body.innerText.includes('Preview failed:');
  }, { rowCount, columnCount });
  const rendered = await inspect(() => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return {
      headers: [...(table?.querySelectorAll('[role="columnheader"]') ?? [])].map((cell) => cell.textContent.trim()),
      rows: [...(table?.querySelectorAll('[role="row"]') ?? [])]
        .filter((row) => row.querySelector('[role="cell"]'))
        .map((row) => [...row.querySelectorAll('[role="cell"]')].map((cell) => cell.textContent.trim())),
    };
  });
  assert.deepEqual(rendered.headers, labels, `${name}: visible Builder headers must match declared preview labels`);
  assert.equal(rendered.rows.length, rowCount, `${name}: visible Builder row count`);
  assert(rendered.rows.every((row) => row.length === columnCount), `${name}: visible Builder rows must include every declared preview column`);
  const renderedByLabel = rendered.rows.map((cells) => Object.fromEntries(rendered.headers.map((label, index) => [label, cells[index]])));
  const expectedByLabel = expectedRows.map((row) => Object.fromEntries(labels.map((label) => [label, formatPreviewCell(row[label])])));
  assert.deepEqual(renderedByLabel, expectedByLabel, `${name}: visible Builder cells must display the independently expected strings, ID arrays, and counts`);
  record(name, startedAt, { rowCount, columnCount });
};

const reloadTable = async (expectedRows, name) => {
  const startedAt = Date.now();
  await navigateUI(`${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitUI(id => Boolean(document.querySelector(`[data-testid="construction-table-${id}"]`)), outputId);
  await clickUI(`[data-testid="construction-table-${outputId}"]`);
  const state = await api(base + '/builder');
  builder = state;
  const preview = await waitNative('/preview', startedAt, (entry) => entry.outputId === outputId);
  assert.equal(preview.status, 200, `${name}: ${JSON.stringify(preview.response)}`);
  assert.equal(preview.response?.rowCount, expectedRows.length);
  assertPreviewRows({ status: preview.status, response: { proposalId: preview.response?.receiptId, previewStatus: 'READY', preview: preview.response } }, expectedRows, name);
  await visibleTable(expectedRows, preview.response, name + '-render', startedAt);
  return preview.response;
};

const applyProposal = async (expectedRows, name) => {
  const panel = await panelInfo();
  assert(panel, `${name}: no active proposal`);
  const startedAt = Date.now();
  if (panel.selector === 'construction-choice-proposal-panel') {
    await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  } else {
    await clickUI('[data-testid="construction-apply-proposal"]');
  }
  await waitUI(() => !document.querySelector('[data-testid="construction-proposal-panel"]')
    && !document.querySelector('[data-testid="construction-choice-proposal-panel"]'));
  const command = await waitNative('/commands', startedAt);
  assert.equal(command.status, 200, `${name}: Apply command failed`);
  builder = await api(base + '/builder');
  await page.waitForFunction(({ outputId, draftDigest, draftVersion }) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready'
      && preview.dataset.previewOutputId === outputId
      && preview.dataset.currentDraftDigest === draftDigest
      && preview.dataset.currentDraftVersion === draftVersion;
  }, { outputId, draftDigest: builder.draftDigest, draftVersion: String(builder.draftVersion) }, { timeout: 5000 });
  const receiptId = await page.locator('[data-testid="construction-preview"]').getAttribute('data-preview-receipt-id');
  assert(receiptId, `${name}: visible saved preview receipt missing`);
  const preview = await api(base + '/preview', { receiptId, outputId, limit: 100 });
  assert.equal(preview.receiptId, receiptId);
  assert.equal(preview.outputId, outputId);
  assertPreviewRows({ status: 200, response: { proposalId: receiptId, previewStatus: 'READY', preview } }, expectedRows, name);
  await visibleTable(expectedRows, preview, name, startedAt);
  return preview;
};

const cancelProposal = async (expectedWorkspace, name) => {
  const startedAt = Date.now();
  const panel = await panelInfo();
  assert(panel, `${name}: no proposal to cancel`);
  if (panel.selector === 'construction-choice-proposal-panel') {
    await clickUI('[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
  } else {
    await clickUI('[data-testid="construction-cancel-proposal"]');
  }
  await waitUI(() => !document.querySelector('[data-testid="construction-proposal-panel"]')
    && !document.querySelector('[data-testid="construction-choice-proposal-panel"]'));
  builder = await api(base + '/builder');
  assert.deepEqual(builder.workspace, expectedWorkspace, `${name}: Cancel changed the saved workspace`);
  record(name, startedAt);
};

const lastStep = (state = builder, kind, predicate = () => true) =>
  steps(state).findLast((step) => step.operation.kind === kind && predicate(step));

const relatedSourceStepFor = (state, path) => lastStep(
  state,
  'RELATED_SOURCE',
  (step) => {
    const sourcePath = step.operation.relatedSource?.source?.path ?? '';
    return sourcePath === path || sourcePath.endsWith('.' + path);
  },
);

const outputForStep = (step, columnId) => {
  const output = step.outputs.find((candidate) => candidate.id === columnId);
  assert(output, `Step ${step.id} omitted output ${columnId}`);
  return output;
};

const assertDirectPatientRoute = (step, path, expectedForm = 'ALL') => {
  const related = step.operation.relatedSource;
  assert(related, `Step ${step.id} lost its RELATED_SOURCE payload`);
  assert.equal(related.source.resourceType, 'Patient');
  assert(related.source.path === path || related.source.path.endsWith('.' + path), `Unexpected source path: ${related.source.path}`);
  assert.equal(related.form, expectedForm);
  assert.equal(related.route.length, 1, JSON.stringify(related.route));
  const [hop] = related.route;
  assert.equal(hop.fromResourceType, 'Specimen');
  assert.equal(hop.toResourceType, 'Patient');
  assert.equal(hop.relationship, 'subject_Patient');
  assert.equal(hop.storageDirection, 'OUTBOUND');
};

const openRelatedField = async (fieldPath, desiredForm = 'ALL') => {
  await clickUI('[data-testid="construction-action-add-columns"]');
  await clickUI('[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitUI(() => Boolean(document.querySelector('[data-testid="construction-add-columns-source"]')));
  if (!await page.locator('[aria-label="Related resources"] summary').evaluate(summary => summary.parentElement.open)) {
    await clickUI('[aria-label="Related resources"] summary');
  }
  const sources = await page.locator('[data-testid="construction-add-columns-source-option"]').evaluateAll(options =>
    options
      .map((option) => ({ label: option.getAttribute('aria-label') ?? '', disabled: option.disabled }))
      .filter((option) => option.label.includes('Patient') && option.label.includes('Related resource')));
  assert.equal(sources.length, 1, `Expected one retained Patient related source, got ${JSON.stringify(sources)}`);
  assert.equal(sources[0].disabled, false, `Retained Patient source is disabled: ${sources[0].label}`);
  await clickUI(`[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(sources[0].label)}]`);
  if (!await page.getByTestId('feature-catalog-raw-fields').locator('summary').evaluate(summary => summary.parentElement.open)) {
    await clickUI('[data-testid="feature-catalog-raw-fields"] summary');
  }
  const candidateSelector = `input[aria-label=${JSON.stringify(`Select Patient.${fieldPath}`)}]`;
  await waitUI(selector => Boolean(document.querySelector(selector + ':not(:disabled)')), candidateSelector);
  const startedAt = Date.now();
  await clickUI(candidateSelector);
  await clickUI('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitUI(() => Boolean(document.querySelector('[role="dialog"]')));
  const controls = await page.getByRole('dialog').evaluate(dialog => {
    const radios = [...(dialog?.querySelectorAll('input[type="radio"]') ?? [])]
      .map((input) => ({ aria: input.getAttribute('aria-label') ?? '', checked: input.checked }));
    return { radios, groupedPolicy: dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value };
  });
  const route = controls.radios.find((radio) =>
    radio.aria.includes('Specimen -[subject]-> Patient'));
  const formLabel = desiredForm === 'COUNT' ? 'Count matching records' : 'Keep all matching values';
  assert(route, `No exact Specimen.subject -> Patient choice: ${JSON.stringify(controls.radios)}`);
  await clickUI(`[role="dialog"] input[aria-label=${JSON.stringify(route.aria)}]`);
  await waitUI(label => [...document.querySelectorAll('[role="dialog"] input[type="radio"]')]
    .some(input => (input.getAttribute('aria-label') ?? '').includes(label)), formLabel);
  const formChoices = await page.getByRole('dialog').locator('input[type="radio"]').evaluateAll(radios => radios.map(input=>input.getAttribute('aria-label')??''));
  const formChoice = formChoices.find(label=>label.includes(formLabel));
  assert(formChoice, `No ${desiredForm} choice for Patient.${fieldPath}: ${JSON.stringify(formChoices)}`);
  await clickUI(`[role="dialog"] input[aria-label=${JSON.stringify(formChoice)}]`);
  if (desiredForm === 'ALL' && controls.groupedPolicy !== undefined) {
    await selectUI('[role="dialog"] select[aria-label="Values per grouped row"]', 'ALL');
  }
  await clickUI('[role="dialog"] button', { name: 'Add 1 column' });
  return { startedAt, selectedRoute: route.aria, selectedForm: formChoice };
};

const beginUnpivot = async (sourceLabel) => {
  await clickUI('[data-testid="construction-rows-settings-trigger"]');
  await waitUI(() => Boolean(document.querySelector('[data-testid="construction-action-unpivot-rows"]:not(:disabled)')));
  await clickUI('[data-testid="construction-action-unpivot-rows"]');
  const selector = `input[aria-label=${JSON.stringify(`Unpivot ${sourceLabel}`)}]`;
  await waitUI(target => Boolean(document.querySelector(target + ':not(:disabled)')), selector);
  const startedAt = Date.now();
  await clickUI(selector);
  return startedAt;
};

const editRelatedLabel = async (step, nextLabel) => {
  await clickUI(`[data-testid="construction-history-step-${step.id}"]`);
  await clickUI(`[data-testid="construction-edit-step-${step.id}"]`);
  const selector = '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]';
  await waitUI(target => Boolean(document.querySelector(target + ':not(:disabled)')), selector);
  const locator = page.locator(selector);
  const startedAt = Date.now();
  await cda.action(`Edit related field label to ${nextLabel}`, locator, target => target.fill(nextLabel, { timeout: 5000 }), { timeout: 5000, budget: 5000, editable: true });
  return startedAt;
};

const removeStep = async (step, name, expectedRows, expectedPreviousConstruction) => {
  await clickUI(`[data-testid="construction-history-step-${step.id}"]`);
  const startedAt = Date.now();
  await clickUI(`[data-testid="construction-remove-step-${step.id}"]`);
  const preview = await proposal(name, startedAt, expectedRows);
  const removalSteps = await page.locator('[data-testid^="construction-removal-step-"]').evaluateAll(elements => elements.map(element => element.dataset.testid));
  assert(removalSteps.includes('construction-removal-step-' + step.id), `${name}: removal preview omitted selected step ${step.id}`);
  if (expectedPreviousConstruction) {
    assert(removalSteps.length === 1, `${name}: expected only the selected step to be removed, got ${JSON.stringify(removalSteps)}`);
    assert.deepEqual(preview.request.response.candidateConstruction, expectedPreviousConstruction,
      `${name}: removal proposal did not restore the exact preceding construction.`);
  }
  return preview;
};

try {
  report.apiBuildFreeze = { target: apiContainer, startedAt: new Date().toISOString(), productFailure: false };
  frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiContainer));
  report.apiBuildFreeze.initial = frozenApiBuild.initial;
  const oracleScope = { project, generation };
  const oracleCandidateLimit = nullableGenderCase ? 2 : 2000;
  let pinnedGenderWitness;
  if (nullableGenderCase) {
    let pinnedReport;
    try {
      pinnedReport = JSON.parse(await readFile(knownGenderWitnessReport, 'utf8'));
    } catch (error) {
      const unavailable = new Error(`Could not read the existing scoped Patient witness report: ${knownGenderWitnessReport}`);
      unavailable.name = 'RawOracleUnavailableError';
      unavailable.rawOracleFailure = true;
      unavailable.unverifiedKind = 'pinned-witness-report-unavailable';
      unavailable.productFailure = false;
      unavailable.diagnostics = { reportPath: knownGenderWitnessReport, cause: String(error) };
      throw unavailable;
    }
    pinnedGenderWitness = pinnedReport.oracle?.source;
    if (pinnedReport.status !== 'passed' || pinnedReport.oracle?.project !== project ||
        pinnedReport.oracle?.generation !== generation || pinnedReport.oracle?.route !== 'Specimen -[subject]-> Patient' ||
        pinnedGenderWitness?.project !== project || pinnedGenderWitness?.generation !== generation ||
        pinnedGenderWitness?.directPatientCount !== 1 || !pinnedGenderWitness?.specimenID || !pinnedGenderWitness?.patientID) {
      const unavailable = new Error('The pinned witness report is not a passed, exact-project/generation, one-Patient Specimen source.');
      unavailable.name = 'RawOracleUnavailableError';
      unavailable.rawOracleFailure = true;
      unavailable.unverifiedKind = 'pinned-witness-report-invalid';
      unavailable.productFailure = false;
      unavailable.diagnostics = { reportPath: knownGenderWitnessReport, status: pinnedReport.status, project: pinnedReport.oracle?.project, generation: pinnedReport.oracle?.generation, source: pinnedGenderWitness };
      throw unavailable;
    }
  }
  const candidateQuery = nullableGenderCase ? pinnedSpecimenCandidateQuery : scopedSpecimenCandidatesQuery;
  const candidates = rawQuery(candidateQuery, {
    ...oracleScope,
    ...(nullableGenderCase ? { specimenID: pinnedGenderWitness.specimenID } : {}),
  });
  assert(candidates.length <= oracleCandidateLimit, `Raw-source candidate query exceeded its bounded limit of ${oracleCandidateLimit}`);
  if (nullableGenderCase && candidates.length !== 1) {
    const unavailable = new Error(`The pinned Specimen resource ID resolved to ${candidates.length} scoped source documents; exactly one is required.`);
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'pinned-specimen-resolution-mismatch';
    unavailable.productFailure = false;
    unavailable.diagnostics = { reportPath: knownGenderWitnessReport, candidateLimit: oracleCandidateLimit, candidateCount: candidates.length };
    throw unavailable;
  }
  assert(candidates.every(candidate => candidate.project === project && candidate.generation === generation && candidate._id?.startsWith('Specimen/')),
    'Scoped raw-source candidate scan returned a resource outside the requested CDA slice');
  if (candidates.length === 0) {
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: oracleCandidateLimit, candidateCount: 0,
      result: 'no-candidates-within-bounded-scan',
    };
    const unavailable = new Error(nullableGenderCase
      ? 'The exact pinned Specimen resource ID no longer resolves inside the scoped CDA generation; no Builder behavior was tested.'
      : 'The bounded scoped Specimen candidate query returned no resources; no Builder behavior was tested.');
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'bounded-candidate-scan-empty';
    unavailable.productFailure = false;
    unavailable.diagnostics = { candidateLimit: oracleCandidateLimit, candidateCount: 0 };
    throw unavailable;
  }

  let witnesses;
  try {
    witnesses = rawQuery(linkedPatientWitnessQuery, {
      ...oracleScope,
      specimenKeys: candidates.map(candidate => candidate._id),
      requiredField: requireFieldWitness ? afterUnpivotFieldPath : '',
      nullableGenderOnly: nullableGenderCase,
    });
  } catch (error) {
    if (!error.rawOracleFailure) throw error;
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: oracleCandidateLimit, candidateCount: candidates.length,
    };
    throw error;
  }
  const [candidateWitness] = witnesses;
  if (!candidateWitness) {
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: oracleCandidateLimit, candidateCount: candidates.length,
      linkedPatientWitnesses: 0,
      result: 'no-witness-within-bounded-candidate-scan',
    };
    const unavailable = new Error(nullableGenderCase
      ? 'The exact pinned CDA Specimen no longer has exactly one linked Patient with absent or explicit-null gender.'
      : 'No direct Patient witness matching the selected mode was found among the first 2000 scoped Specimens by _key; this does not establish absence from the full project generation.');
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'bounded-linked-patient-witness-not-found';
    unavailable.productFailure = false;
    unavailable.diagnostics = { candidateLimit: oracleCandidateLimit, candidateCount: candidates.length, linkedPatientWitnesses: 0 };
    throw unavailable;
  }

  let exactRows;
  try {
    exactRows = rawQuery(exactLinkedPatientQuery, {
      ...oracleScope,
      specimenKey: candidateWitness.specimen._id,
      specimenID: candidateWitness.specimen.id,
      requiredField: requireFieldWitness ? afterUnpivotFieldPath : '',
      nullableGenderOnly: nullableGenderCase,
    });
  } catch (error) {
    if (!error.rawOracleFailure) throw error;
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: oracleCandidateLimit, candidateCount: candidates.length,
      candidateWitness: candidateWitness.specimen.id,
    };
    error.unverifiedKind = 'exact-record-reread-command';
    throw error;
  }
  const exactRereadMatches = exactRows.length === 1 &&
    exactRows[0].specimen.id === candidateWitness.specimen.id &&
    exactRows[0].specimen._id === candidateWitness.specimen._id &&
    exactRows[0].patient.id === candidateWitness.patient.id &&
    exactRows[0].patient._id === candidateWitness.patient._id &&
    exactRows[0].patient.gender === candidateWitness.patient.gender &&
    exactRows[0].patient.genderOwnProperty === candidateWitness.patient.genderOwnProperty &&
    exactRows[0].patient.genderIsNull === candidateWitness.patient.genderIsNull &&
    exactRows[0].patient.genderValue === candidateWitness.patient.genderValue &&
    exactRows[0].patient.resourceType === candidateWitness.patient.resourceType &&
    exactRows[0].patientCount === candidateWitness.patientCount &&
    exactRows[0].project === candidateWitness.project &&
    exactRows[0].generation === candidateWitness.generation;
  if (!exactRereadMatches) {
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: oracleCandidateLimit, candidateCount: candidates.length,
      candidateWitness: candidateWitness.specimen.id,
      exactRereadCount: exactRows.length,
      result: 'exact-record-reread-did-not-confirm-witness',
    };
    const unavailable = new Error('The independently queried exact selected Specimen no longer matches the pinned/bounded source witness; no Builder behavior was tested.');
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'exact-record-reread-mismatch';
    unavailable.productFailure = false;
    unavailable.diagnostics = { candidateLimit: oracleCandidateLimit, candidateWitness: candidateWitness.specimen.id, exactRereadCount: exactRows.length };
    throw unavailable;
  }
  const source = exactRows[0];
  let sourceGenderState;
  if (nullableGenderCase) {
    const identityMismatch = source?.specimen?.id !== pinnedGenderWitness.specimenID || source?.patient?.id !== pinnedGenderWitness.patientID;
    if (identityMismatch) {
      const unavailable = new Error('The exact raw-source reread no longer matches the previously selected Specimen and Patient IDs.');
      unavailable.name = 'RawOracleUnavailableError';
      unavailable.rawOracleFailure = true;
      unavailable.unverifiedKind = 'pinned-witness-identity-changed';
      unavailable.productFailure = false;
      unavailable.diagnostics = { expected: { specimenID: pinnedGenderWitness.specimenID, patientID: pinnedGenderWitness.patientID }, actual: { specimenID: source?.specimen?.id, patientID: source?.patient?.id } };
      throw unavailable;
    }
    try {
      sourceGenderState = classifyNullableSourceScalar({
        ownProperty: source.patient.genderOwnProperty,
        isNull: source.patient.genderIsNull,
        value: source.patient.genderValue,
      });
    } catch (cause) {
      const unavailable = new Error('The exact raw-source reread did not preserve consistent gender own-property/null evidence.');
      unavailable.name = 'RawOracleUnavailableError';
      unavailable.rawOracleFailure = true;
      unavailable.unverifiedKind = 'raw-gender-state-evidence-invalid';
      unavailable.productFailure = false;
      unavailable.diagnostics = { specimenID: source.specimen.id, patientID: source.patient.id, patient: source.patient, cause: String(cause) };
      throw unavailable;
    }
    if (sourceGenderState.state !== 'missing' && sourceGenderState.state !== 'explicit-null') {
      const unavailable = new Error('The fresh raw source reread no longer has a missing or explicit-null Patient.gender witness.');
      unavailable.name = 'RawOracleUnavailableError';
      unavailable.rawOracleFailure = true;
      unavailable.unverifiedKind = 'pinned-gender-state-changed';
      unavailable.productFailure = false;
      unavailable.diagnostics = { specimenID: source.specimen.id, patientID: source.patient.id, patientGenderSource: sourceGenderState };
      throw unavailable;
    }
  }
  assert(source?.specimen?.id && source?.patient?.id && source?.patientCount === 1,
    'The scoped CDA oracle needs one Specimen with exactly one direct linked Patient and populated Patient.id.');
  if (requireFieldWitness) assert(typeof source.patient[afterUnpivotFieldPath] === 'string' && source.patient[afterUnpivotFieldPath].length > 0,
    `The selected mode requires a populated Patient.${afterUnpivotFieldPath} witness.`);
  assert.equal(source.project, project);
  assert.equal(source.generation, generation);
  report.oracle = {
    oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
    witnessBasis: nullableGenderCase ? `Pinned exact witness from ${knownGenderWitnessReport}; the raw source is reread by the bounded resource-ID query.` : undefined,
    candidateQuery,
    witnessQuery: linkedPatientWitnessQuery,
    exactRereadQuery: exactLinkedPatientQuery,
    route: source.route,
    project, generation, candidateLimit: oracleCandidateLimit, candidateCount: candidates.length,
    selectedCandidateWitnessCount: 1,
    exactRereadCount: exactRows.length,
    source: {
      specimenID: source.specimen.id,
      patientID: source.patient.id,
      ...(nullableGenderCase ? { patientGenderSource: { state: sourceGenderState.state, ownProperty: sourceGenderState.ownProperty, isNull: sourceGenderState.isNull, value: source.patient.genderValue } } : source.patient.gender == null ? {} : { patientGender: source.patient.gender }),
      ...(source.patient.resourceType == null ? {} : { patientResourceType: source.patient.resourceType }),
      ...(nullableGenderCase ? { postUnpivotFieldPath: 'gender', postUnpivotExpectedAll: expectedNullableRelatedAll({ relatedCount: source.patientCount, sourceState: sourceGenderState.state }) } : {}),
      ...(requireFieldWitness ? { postUnpivotFieldPath: afterUnpivotFieldPath, postUnpivotFieldWitness: source.patient[afterUnpivotFieldPath] } : {}),
      directPatientCount: source.patientCount,
      project: source.project,
      generation: source.generation,
    },
    patientMatches: 1,
  };
  recordLifecycleCheck(
    'bounded raw CDA oracle rereads one exact scoped Specimen with one linked Patient',
    report.oracle.project === project && report.oracle.generation === generation &&
      report.oracle.exactRereadCount === 1 && source.patientCount === 1 &&
      Boolean(source.specimen.id) && Boolean(source.patient.id),
    { project, generation, route: source.route, specimenID: source.specimen.id, patientID: source.patient.id,
      directPatientCount: source.patientCount, candidateLimit: oracleCandidateLimit,
      candidateCount: candidates.length, exactRereadCount: exactRows.length });
  recordGenderAllLifecycleCheck(
    'bounded raw CDA oracle selects the exact Specimen and one linked Patient with populated gender',
    report.oracle.source.postUnpivotFieldPath === 'gender' &&
      source.patient.gender === report.oracle.source.postUnpivotFieldWitness &&
      typeof source.patient.gender === 'string' && source.patient.gender.length > 0,
    { project, generation, specimenID: source.specimen.id, patientID: source.patient.id,
      directPatientCount: source.patientCount, patientGender: source.patient.gender,
      candidateLimit: oracleCandidateLimit, candidateCount: candidates.length, exactRereadCount: exactRows.length });

  await api(apiRoot, { name: explorer, title: afterUnpivotCase === 'id-count' ? 'Related ID count after Unpivot QA' : nullableGenderCase ? 'Related nullable Patient gender after Unpivot QA' : 'Related field after Unpivot QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, generation);
  const rootNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen');
  assert(rootNode, 'Scoped CDA catalog omitted Specimen root');
  await command([{ type: 'CREATE_TABLE', title: 'Related binding reshape QA', rootNodeId: rootNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const specimenIDCandidate = builder.catalog.candidates.find((candidate) =>
    candidate.nodeId === rootNode.nodeId && candidate.fieldPath === 'id');
  assert(specimenIDCandidate, 'Scoped CDA catalog omitted Specimen.id');
  await command([{
    type: 'ADD_COLUMN',
    outputId,
    occurrenceId: 'base',
    candidateId: specimenIDCandidate.candidateId,
    projectionMode: 'VALUE',
    initialPresentation: 'TABLE',
    title: 'Specimen ID',
  }]);
  const authoredSpecimenID = doc().columns.find((column) => column.label === 'Specimen ID');
  assert(authoredSpecimenID, 'The authored root Specimen.id column is missing.');
  const selection = await api(`${apiRoot}/${explorer}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: explorer,
    source: { kind: 'resources', resources: { refs: [{
      project, generation, resourceType: 'Specimen', id: source.specimen.id,
    }] } },
  });
  assert.equal(selection.memberCount, 1, 'The native population must contain exactly the raw-oracle Specimen.');
  const routes = await api(base + '/population-routes', {
    snapshotToken: builder.catalog.snapshotToken,
    outputId,
    selectionRevisionId: selection.id,
    limit: 50,
  });
  const direct = routes.choices.find((choice) => choice.route.length === 0);
  assert(direct, 'The exact selected Specimen has no direct population route.');
  await command([{
    type: 'SET_TABLE_POPULATION',
    outputId,
    selectionRevisionId: selection.id,
    routeChoiceId: direct.routeChoiceId,
  }]);
  report.ownedSelection = {
    selectionRevisionId: selection.id,
    exactMemberCount: selection.memberCount,
    selectedSpecimenID: source.specimen.id,
  };
  beginNativeCapture();

  let previewRows = [{ 'Specimen ID': source.specimen.id }];
  let baseline = await reloadTable(previewRows, 'load-exact-CDA-Specimen');
  assert.equal(baseline.rowCount, 1);

  const initialWorkspace = structuredClone(builder.workspace);
  const patientIDChoice = await openRelatedField('id');
  let start = patientIDChoice.startedAt;
  recordLifecycleCheck(
    'Patient.id ALL choice uses the exact Specimen subject-to-Patient route',
    patientIDChoice.selectedRoute.includes('Specimen -[subject]-> Patient') &&
      patientIDChoice.selectedForm.includes('Keep all matching values'),
    { route: patientIDChoice.selectedRoute, form: patientIDChoice.selectedForm });
  let patientIDLabel;
  const addedID = await proposal('add-related-Patient-ID-before-Unpivot-preview', start, (response) => {
    const step = response.candidateConstruction.steps.findLast((candidate) =>
      candidate.operation.kind === 'RELATED_SOURCE' &&
      (candidate.operation.relatedSource?.source?.path === 'id' || candidate.operation.relatedSource?.source?.path?.endsWith('.id')));
    assert(step, 'The related Patient.id proposal did not produce a RELATED_SOURCE step.');
    patientIDLabel = outputForStep(step, step.operation.relatedSource.outputColumnId).label;
    return [{ 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] }];
  });
  recordLifecycleCheck(
    'Patient.id ALL proposal previews the exact raw linked Patient ID',
    addedID.expectedRows[0]['Specimen ID'] === source.specimen.id &&
      addedID.expectedRows[0][patientIDLabel]?.[0] === source.patient.id && addedID.preview.rowCount === 1,
    { specimenID: source.specimen.id, patientID: source.patient.id, previewRows: addedID.preview.rows });
  const initialRelatedStep = addedID.request.response.candidateConstruction.steps.findLast((step) =>
    step.operation.kind === 'RELATED_SOURCE' &&
    (step.operation.relatedSource?.source?.path === 'id' || step.operation.relatedSource?.source?.path?.endsWith('.id')));
  assert(initialRelatedStep, 'The related Patient.id proposal did not produce a RELATED_SOURCE step.');
  assertDirectPatientRoute(initialRelatedStep, 'id');
  await cancelProposal(initialWorkspace, 'cancel-related-Patient-ID-preview-restores-source-workspace');
  recordLifecycleCheck(
    'Cancel preserves the exact Specimen workspace before applying Patient.id ALL',
    JSON.stringify(builder.workspace) === JSON.stringify(initialWorkspace),
    { specimenID: source.specimen.id, workspacePreserved: JSON.stringify(builder.workspace) === JSON.stringify(initialWorkspace) });

  const confirmedPatientIDChoice = await openRelatedField('id');
  start = confirmedPatientIDChoice.startedAt;
  await proposal('confirm-related-Patient-ID-before-Unpivot-preview', start, [
    { 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] },
  ]);
  await applyProposal([
    { 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] },
  ], 'apply-related-Patient-ID-before-Unpivot');
  let relatedBaseline = structuredClone(builder);
  const patientIDStep = relatedSourceStepFor(relatedBaseline, 'id');
  assert(patientIDStep, 'Applied Patient.id binding is missing.');
  assertDirectPatientRoute(patientIDStep, 'id');
  const savedPatientIDLabel = outputForStep(patientIDStep, patientIDStep.operation.relatedSource.outputColumnId).label;
  assert.equal(savedPatientIDLabel, patientIDLabel, 'Apply changed the proposed Patient.id output label.');
  assert(/patient/i.test(patientIDLabel) && /\bids?\b/i.test(patientIDLabel),
    `Unexpected Patient ID output label: ${patientIDLabel}`);
  previewRows = [{ 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] }];
  await reloadTable(previewRows, 'reload-related-Patient-ID-before-Unpivot');
  recordLifecycleCheck(
    'Apply and reload preserve the exact Patient.id ALL source binding and raw value',
    savedPatientID.operation.relatedSource.form === 'ALL' &&
      (savedPatientID.operation.relatedSource.source.path === 'id' ||
      savedPatientID.operation.relatedSource.source.path.endsWith('.id')) &&
      previewRows[0][patientIDLabel]?.[0] === source.patient.id,
    { stepID: savedPatientID.id, route: savedPatientID.operation.relatedSource.route,
      patientID: source.patient.id, savedRows: previewRows });

  start = await beginUnpivot('Specimen ID');
  const unpivotPreview = await proposal('Unpivot-with-retained-related-Patient-binding-preview', start, (response) => {
    const step = response.candidateConstruction.steps.findLast((candidate) => candidate.operation.kind === 'UNPIVOT');
    assert(step, 'Unpivot proposal omitted its native UNPIVOT step.');
    const key = outputForStep(step, step.operation.unpivot.keyOutputColumnId);
    const value = outputForStep(step, step.operation.unpivot.valueOutputColumnId);
    return [{ [patientIDLabel]: [source.patient.id], [key.label]: 'Specimen ID', [value.label]: source.specimen.id }];
  });
  const candidateUnpivot = unpivotPreview.request.response.candidateConstruction.steps.findLast((step) =>
    step.operation.kind === 'UNPIVOT');
  assert(candidateUnpivot, 'Unpivot proposal omitted its native UNPIVOT step.');
  const unpivot = candidateUnpivot.operation.unpivot;
  const keyOutput = outputForStep(candidateUnpivot, unpivot.keyOutputColumnId);
  const valueOutput = outputForStep(candidateUnpivot, unpivot.valueOutputColumnId);
  const keyLabel = keyOutput.label;
  const valueLabel = valueOutput.label;
  recordLifecycleCheck(
    'Unpivot proposal renders the exact Specimen ID pair and retains Patient.id ALL',
    unpivotPreview.expectedRows.length === 1 &&
      unpivotPreview.expectedRows[0][patientIDLabel]?.[0] === source.patient.id &&
      unpivotPreview.expectedRows[0][keyLabel] === 'Specimen ID' &&
      unpivotPreview.expectedRows[0][valueLabel] === source.specimen.id && unpivotPreview.preview.rowCount === 1,
    { rawSpecimenID: source.specimen.id, rawPatientID: source.patient.id, keyLabel, valueLabel,
      previewRows: unpivotPreview.preview.rows });
  await cancelProposal(relatedBaseline.workspace, 'cancel-Unpivot-preview-preserves-related-source-binding');
  recordLifecycleCheck(
    'Cancel preserves the exact Patient.id Related state before Unpivot Apply',
    JSON.stringify(builder.workspace) === JSON.stringify(relatedBaseline.workspace) &&
      builder.draftVersion === relatedBaseline.draftVersion && builder.draftDigest === relatedBaseline.draftDigest,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, relatedStepIDs: steps().map(step => step.id) });

  start = await beginUnpivot('Specimen ID');
  const confirmedUnpivot = await proposal('confirm-Unpivot-with-retained-related-Patient-binding-preview', start, [
    { [patientIDLabel]: [source.patient.id], [keyLabel]: 'Specimen ID', [valueLabel]: source.specimen.id },
  ]);
  const confirmedUnpivotStep = confirmedUnpivot.request.response.candidateConstruction.steps.findLast((step) =>
    step.operation.kind === 'UNPIVOT');
  assert(confirmedUnpivotStep);
  await applyProposal([
    { [patientIDLabel]: [source.patient.id], [keyLabel]: 'Specimen ID', [valueLabel]: source.specimen.id },
  ], 'apply-Unpivot-with-retained-related-Patient-binding');
  let unpivotBaseline = structuredClone(builder);
  const savedUnpivot = lastStep(unpivotBaseline, 'UNPIVOT');
  const savedPatientID = relatedSourceStepFor(unpivotBaseline, 'id');
  assert(savedUnpivot && savedPatientID);
  assertDirectPatientRoute(savedPatientID, 'id');
  assert.deepEqual(savedPatientID.operation.relatedSource.route, patientIDStep.operation.relatedSource.route,
    'UNPIVOT changed the existing exact Patient source route.');
  assert.deepEqual(savedPatientID.operation.relatedSource.source, patientIDStep.operation.relatedSource.source,
    'UNPIVOT changed the existing Patient.id field binding.');
  previewRows = [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
  }];
  await reloadTable(previewRows, 'reload-Unpivot-retained-related-Patient-binding');

  assert.deepEqual(savedUnpivot.inputs, [{ kind: 'STEP_OUTPUT', stepId: savedPatientID.id }],
    'UNPIVOT must consume the compiler stage represented by the retained related-source step.');
  const unpivotInputStage = await api(base + '/construction-capabilities', {
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    outputId,
    stageId: savedPatientID.id,
  });
  assert.equal(unpivotInputStage.selectedStage.id, savedPatientID.id,
    'The compiler stage used by the Unpivot editor must be the applied related-source stage.');
  const compilerSpecimenIDColumns = unpivotInputStage.selectedStage.columns.filter((column) =>
    column.label === authoredSpecimenID.label);
  assert.equal(compilerSpecimenIDColumns.length, 1,
    `Expected one Specimen ID column in the compiler stage used by Unpivot, got ${JSON.stringify(compilerSpecimenIDColumns)}`);
  const specimenIDColumn = compilerSpecimenIDColumns[0];
  assert(specimenIDColumn.id && specimenIDColumn.name,
    `The Unpivot compiler stage omitted stable identity for Specimen ID: ${JSON.stringify(specimenIDColumn)}`);
  const specimenIDColumnId = specimenIDColumn.id;
  report.unpivotInputStage = {
    stageId: unpivotInputStage.selectedStage.id,
    rootColumn: { id: specimenIDColumn.id, name: specimenIDColumn.name, label: specimenIDColumn.label },
    patientIDColumnId: savedPatientID.operation.relatedSource.outputColumnId,
  };

  const savedUnpivotOutputs = new Set(savedUnpivot.outputs.map((output) => output.id));
  const retainedPatientIDOutputId = patientIDStep.operation.relatedSource.outputColumnId;
  assert.deepEqual(savedUnpivot.operation.unpivot.inputs.map((input) => input.columnId), [specimenIDColumnId],
    'UNPIVOT must consume only the exact Specimen ID source column.');
  assert(!savedUnpivotOutputs.has(specimenIDColumnId),
    'The consumed Specimen ID source must not remain in the UNPIVOT output schema.');
  assert(savedUnpivotOutputs.has(retainedPatientIDOutputId),
    'The original Patient.id ALL binding output must remain in the UNPIVOT output schema.');
  assert(savedUnpivotOutputs.has(savedUnpivot.operation.unpivot.keyOutputColumnId));
  assert(savedUnpivotOutputs.has(savedUnpivot.operation.unpivot.valueOutputColumnId));
  assert.equal(savedPatientID.operation.relatedSource.form, 'ALL');
  assertDirectPatientRoute(savedPatientID, 'id', 'ALL');
  const savedPatientIDOutput = outputForStep(savedPatientID, retainedPatientIDOutputId);
  assert.equal(savedPatientIDOutput.label, patientIDLabel);
  recordLifecycleCheck(
    'Unpivot Apply and reload preserve the exact retained Patient.id binding and transformed rows',
    savedPatientID.operation.relatedSource.form === 'ALL' &&
      (savedPatientID.operation.relatedSource.source.path === 'id' ||
      savedPatientID.operation.relatedSource.source.path.endsWith('.id')) &&
      savedUnpivot.operation.unpivot.inputs.length === 1 &&
      savedUnpivot.operation.unpivot.inputs[0].columnId === specimenIDColumnId &&
      previewRows[0][patientIDLabel]?.[0] === source.patient.id,
    { unpivotStepID: savedUnpivot.id, retainedPatientStepID: savedPatientID.id,
      consumedSourceColumnID: specimenIDColumnId, retainedPatientID: source.patient.id, rows: previewRows });

  const reshapedRetainedPatientRow = structuredClone(previewRows[0]);
  const postUnpivotValue = nullableGenderCase
    ? expectedNullableRelatedAll({ relatedCount: source.patientCount, sourceState: sourceGenderState.state })
    : afterUnpivotCase === 'id-count' ? source.patientCount : [source.patient[afterUnpivotFieldPath]];
  const postUnpivotValueForRow = (label) => ({ ...reshapedRetainedPatientRow, [label]: postUnpivotValue });
  const beforePostUnpivotField = structuredClone(builder);
  const postUnpivotChoice = await openRelatedField(afterUnpivotFieldPath, afterUnpivotForm);
  start = postUnpivotChoice.startedAt;
  recordGenderAllLifecycleCheck(
    'Post-Unpivot Patient.gender ALL choice remains available on the exact raw Patient route',
    afterUnpivotCase === 'gender-all' && postUnpivotChoice.selectedRoute.includes('Specimen -[subject]-> Patient') &&
      postUnpivotChoice.selectedForm.includes('Keep all matching values') &&
      source.patient.gender === report.oracle.source.postUnpivotFieldWitness,
    { route: postUnpivotChoice.selectedRoute, form: postUnpivotChoice.selectedForm,
      rawPatientID: source.patient.id, rawGender: source.patient.gender });
  let postUnpivotLabel;
  const postUnpivotLabelProposal = await proposal(`add-Patient-${afterUnpivotFieldPath}-${afterUnpivotForm}-after-Unpivot-preview`, start, (response) => {
    const step = response.candidateConstruction.steps.findLast((candidate) =>
      candidate.operation.kind === 'RELATED_SOURCE' &&
      candidate.operation.relatedSource?.source?.resourceType === 'Patient' &&
      (candidate.operation.relatedSource?.source?.path === afterUnpivotFieldPath ||
        candidate.operation.relatedSource?.source?.path?.endsWith('.' + afterUnpivotFieldPath)));
    assert(step, `Post-Unpivot Patient.${afterUnpivotFieldPath} did not produce a related source operation.`);
    assert.equal(step.operation.relatedSource.form, afterUnpivotForm, 'The post-Unpivot related source form changed.');
    const candidateOutput = outputForStep(step, step.operation.relatedSource.outputColumnId);
    assert.notEqual(candidateOutput.id, retainedPatientIDOutputId);
    if (afterUnpivotForm === 'COUNT') assert.equal(candidateOutput.type, 'integer');
    postUnpivotLabel = candidateOutput.label;
    assert(!Object.hasOwn(reshapedRetainedPatientRow, postUnpivotLabel),
      'The new related output label collides with a retained or Unpivot output label.');
    return [postUnpivotValueForRow(postUnpivotLabel)];
  });
  const postUnpivotStepProposal = postUnpivotLabelProposal.request.response.candidateConstruction.steps.findLast((step) =>
    step.operation.kind === 'RELATED_SOURCE' &&
    step.operation.relatedSource?.source?.resourceType === 'Patient' &&
    (step.operation.relatedSource?.source?.path === afterUnpivotFieldPath ||
      step.operation.relatedSource?.source?.path?.endsWith('.' + afterUnpivotFieldPath)));
  assert(postUnpivotStepProposal, `Post-Unpivot Patient.${afterUnpivotFieldPath} proposal is missing.`);
  assert.equal(postUnpivotStepProposal.operation.relatedSource.form, afterUnpivotForm);
  assertDirectPatientRoute(postUnpivotStepProposal, afterUnpivotFieldPath, afterUnpivotForm);
  assert.equal(outputForStep(postUnpivotStepProposal, postUnpivotStepProposal.operation.relatedSource.outputColumnId).label, postUnpivotLabel);
  assert.notEqual(postUnpivotStepProposal.operation.relatedSource.outputColumnId, retainedPatientIDOutputId,
    'The new source operation must have its own output identity.');
  recordGenderAllLifecycleCheck(
    'Post-Unpivot Patient.gender ALL proposal matches the exact populated raw Patient.gender value',
    afterUnpivotCase === 'gender-all' && postUnpivotLabelProposal.expectedRows.length === 1 &&
      postUnpivotLabelProposal.expectedRows[0][postUnpivotLabel]?.[0] === source.patient.gender &&
      postUnpivotLabelProposal.preview.rowCount === 1,
    { patientID: source.patient.id, rawGender: source.patient.gender,
      expectedRows: postUnpivotLabelProposal.expectedRows, previewRows: postUnpivotLabelProposal.preview.rows,
      proposedRoute: postUnpivotStepProposal.operation.relatedSource.route });
  await cancelProposal(beforePostUnpivotField.workspace, `cancel-Patient-${afterUnpivotFieldPath}-${afterUnpivotForm}-after-Unpivot-preserves-existing-binding`);
  recordGenderAllLifecycleCheck(
    'Cancel preserves exact saved Unpivot rows before applying Patient.gender ALL',
    JSON.stringify(builder.workspace) === JSON.stringify(beforePostUnpivotField.workspace) &&
      builder.draftVersion === beforePostUnpivotField.draftVersion && builder.draftDigest === beforePostUnpivotField.draftDigest,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, rows: previewRows });

  const confirmedPostUnpivotChoice = await openRelatedField(afterUnpivotFieldPath, afterUnpivotForm);
  start = confirmedPostUnpivotChoice.startedAt;
  await proposal(`confirm-Patient-${afterUnpivotFieldPath}-${afterUnpivotForm}-after-Unpivot-preview`, start,
    [postUnpivotValueForRow(postUnpivotLabel)]);
  await applyProposal([postUnpivotValueForRow(postUnpivotLabel)], `apply-Patient-${afterUnpivotFieldPath}-${afterUnpivotForm}-after-Unpivot`);
  let postUnpivotBaseline = structuredClone(builder);
  let savedPostUnpivotStep = relatedSourceStepFor(postUnpivotBaseline, afterUnpivotFieldPath);
  assert(savedPostUnpivotStep, `Applied post-Unpivot Patient.${afterUnpivotFieldPath} binding is missing.`);
  assert.notEqual(savedPostUnpivotStep.id, patientIDStep.id, 'The post-Unpivot binding must be a new source operation.');
  assert.equal(savedPostUnpivotStep.operation.relatedSource.form, afterUnpivotForm);
  assertDirectPatientRoute(savedPostUnpivotStep, afterUnpivotFieldPath, afterUnpivotForm);
  const savedPostUnpivotOutput = outputForStep(savedPostUnpivotStep, savedPostUnpivotStep.operation.relatedSource.outputColumnId);
  assert.equal(savedPostUnpivotOutput.label, postUnpivotLabel);
  assert.notEqual(savedPostUnpivotOutput.id, retainedPatientIDOutputId);
  assert.notEqual(savedPostUnpivotOutput.name, savedPatientIDOutput.name);
  if (afterUnpivotForm === 'COUNT') assert.equal(savedPostUnpivotOutput.type, 'integer');
  assert(!Object.hasOwn(reshapedRetainedPatientRow, savedPostUnpivotOutput.label),
    'The COUNT/ALL output label must not collide with retained output labels.');
  assert.deepEqual(savedPostUnpivotStep.operation.relatedSource.route, savedPatientID.operation.relatedSource.route,
    'Post-Unpivot field selection changed the exact Patient source route.');
  assert.deepEqual(savedPostUnpivotStep.operation.relatedSource.source, postUnpivotStepProposal.operation.relatedSource.source,
    'Applying the post-Unpivot field changed the exact proposed Patient source candidate.');
  assert.deepEqual(steps(postUnpivotBaseline).find((step) => step.id === savedUnpivot.id), savedUnpivot,
    'Adding the related source changed the saved Unpivot operation.');
  previewRows = [postUnpivotValueForRow(postUnpivotLabel)];
  await reloadTable(previewRows, `reload-related-Patient-${afterUnpivotFieldPath}-${afterUnpivotForm}-after-Unpivot`);
  const reloadedPostUnpivotStep = relatedSourceStepFor(builder, afterUnpivotFieldPath);
  assert(reloadedPostUnpivotStep, 'Reload must retain the post-Unpivot related Patient field.');
  assertDirectPatientRoute(reloadedPostUnpivotStep, afterUnpivotFieldPath, afterUnpivotForm);
  recordGenderAllLifecycleCheck(
    'Apply and reload preserve exact Patient.gender ALL output and raw value',
    afterUnpivotCase === 'gender-all' && reloadedPostUnpivotStep?.operation.relatedSource.form === 'ALL' &&
      reloadedPostUnpivotStep.operation.relatedSource.route.length === 1 &&
      (reloadedPostUnpivotStep.operation.relatedSource.source.path === afterUnpivotFieldPath ||
        reloadedPostUnpivotStep.operation.relatedSource.source.path.endsWith('.' + afterUnpivotFieldPath)) &&
      previewRows[0][postUnpivotLabel]?.[0] === source.patient.gender &&
      steps(builder).some(step => step.id === savedUnpivot.id),
    { patientID: source.patient.id, rawGender: source.patient.gender,
      savedStepID: reloadedPostUnpivotStep?.id, route: reloadedPostUnpivotStep?.operation.relatedSource.route, rows: previewRows });

  const beforeEdit = structuredClone(builder);
  const editedLabel = `Patient ${afterUnpivotFieldPath} ${afterUnpivotForm.toLowerCase()} after Unpivot`;
  assert(!Object.hasOwn(reshapedRetainedPatientRow, editedLabel));
  start = await editRelatedLabel(savedPostUnpivotStep, editedLabel);
  await proposal('edit-related-source-label-after-Unpivot-preview', start, [postUnpivotValueForRow(editedLabel)]);
  await cancelProposal(beforeEdit.workspace, 'cancel-related-source-edit-preserves-route-and-output');
  recordGenderAllLifecycleCheck(
    'Cancel preserves saved Patient.gender ALL binding after label-edit proposal',
    JSON.stringify(builder.workspace) === JSON.stringify(beforeEdit.workspace) &&
      builder.draftVersion === beforeEdit.draftVersion && builder.draftDigest === beforeEdit.draftDigest,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, patientID: source.patient.id });

  savedPostUnpivotStep = relatedSourceStepFor(builder, afterUnpivotFieldPath);
  start = await editRelatedLabel(savedPostUnpivotStep, editedLabel);
  await proposal('confirm-related-source-label-edit-after-Unpivot-preview', start, [postUnpivotValueForRow(editedLabel)]);
  await applyProposal([postUnpivotValueForRow(editedLabel)], 'apply-related-source-label-edit-after-Unpivot');
  postUnpivotBaseline = structuredClone(builder);
  savedPostUnpivotStep = relatedSourceStepFor(postUnpivotBaseline, afterUnpivotFieldPath);
  assert(savedPostUnpivotStep);
  assert.equal(outputForStep(savedPostUnpivotStep, savedPostUnpivotStep.operation.relatedSource.outputColumnId).label, editedLabel);
  assert.equal(savedPostUnpivotStep.operation.relatedSource.form, afterUnpivotForm);
  assertDirectPatientRoute(savedPostUnpivotStep, afterUnpivotFieldPath, afterUnpivotForm);
  assert.deepEqual(savedPostUnpivotStep.operation.relatedSource.source, postUnpivotStepProposal.operation.relatedSource.source);
  assert.deepEqual(savedPostUnpivotStep.operation.relatedSource.route, postUnpivotStepProposal.operation.relatedSource.route);
  assert.deepEqual(steps(postUnpivotBaseline).find((step) => step.id === savedUnpivot.id), savedUnpivot,
    'Editing the related source changed the saved Unpivot operation.');
  previewRows = [postUnpivotValueForRow(editedLabel)];
  await reloadTable(previewRows, 'reload-edited-related-source-after-Unpivot');
  const reloadedEditedPostUnpivotStep = relatedSourceStepFor(builder, afterUnpivotFieldPath);
  recordGenderAllLifecycleCheck(
    'Edit Apply and reload preserve Patient.gender ALL route and edited label',
    reloadedEditedPostUnpivotStep?.operation.relatedSource.form === 'ALL' &&
      outputForStep(reloadedEditedPostUnpivotStep, reloadedEditedPostUnpivotStep.operation.relatedSource.outputColumnId).label === editedLabel &&
      previewRows[0][editedLabel]?.[0] === source.patient.gender,
    { patientID: source.patient.id, rawGender: source.patient.gender,
      stepID: reloadedEditedPostUnpivotStep?.id, label: editedLabel, rows: previewRows });

  await removeStep(savedPostUnpivotStep, 'remove-post-Unpivot-related-source-preview', previewRows.map(({ [editedLabel]: _removed, ...row }) => row),
    unpivotBaseline.workspace.documents[0].construction);
  await cancelProposal(postUnpivotBaseline.workspace, 'cancel-post-Unpivot-related-source-removal-preserves-binding');
  recordGenderAllLifecycleCheck(
    'Cancel preserves saved Patient.gender ALL after removal preview',
    JSON.stringify(builder.workspace) === JSON.stringify(postUnpivotBaseline.workspace) &&
      builder.draftVersion === postUnpivotBaseline.draftVersion && builder.draftDigest === postUnpivotBaseline.draftDigest,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, patientID: source.patient.id });

  savedPostUnpivotStep = relatedSourceStepFor(builder, afterUnpivotFieldPath);
  const reshapedRowsWithoutPostField = previewRows.map(({ [editedLabel]: _removed, ...row }) => row);
  await removeStep(savedPostUnpivotStep, 'confirm-remove-post-Unpivot-related-source-preview', reshapedRowsWithoutPostField,
    unpivotBaseline.workspace.documents[0].construction);
  await applyProposal(reshapedRowsWithoutPostField, 'remove-post-Unpivot-related-source');
  assert.deepEqual(doc().construction, doc(unpivotBaseline).construction,
    'Removing the post-Unpivot field must restore the exact saved Unpivot construction.');
  assert.deepEqual(doc().population, doc(unpivotBaseline).population);
  previewRows = reshapedRowsWithoutPostField;
  await reloadTable(previewRows, 'reload-after-removing-post-Unpivot-field');
  recordGenderAllLifecycleCheck(
    'Removing Patient.gender ALL and reloading restores exact Unpivot-only construction and rows',
    JSON.stringify(doc().construction) === JSON.stringify(doc(unpivotBaseline).construction) &&
      JSON.stringify(doc().population) === JSON.stringify(doc(unpivotBaseline).population) &&
      !relatedSourceStepFor(builder, afterUnpivotFieldPath) &&
      JSON.stringify(previewRows[0][patientIDLabel]) === JSON.stringify([source.patient.id]),
    { restoredConstruction: doc().construction, rows: previewRows, patientID: source.patient.id });

  const currentUnpivot = lastStep(builder, 'UNPIVOT');
  const beforeUnpivotRemoval = structuredClone(builder);
  await removeStep(currentUnpivot, 'remove-Unpivot-restores-preexisting-related-source-preview', [
    { 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] },
  ], relatedBaseline.workspace.documents[0].construction);
  await cancelProposal(beforeUnpivotRemoval.workspace, 'cancel-Unpivot-removal-preserves-related-source-binding');
  recordLifecycleCheck(
    'Cancel preserves saved Unpivot state after removal preview',
    JSON.stringify(builder.workspace) === JSON.stringify(beforeUnpivotRemoval.workspace) &&
      builder.draftVersion === beforeUnpivotRemoval.draftVersion && builder.draftDigest === beforeUnpivotRemoval.draftDigest,
    { draftVersion: builder.draftVersion, draftDigest: builder.draftDigest, rows: previewRows });

  await removeStep(currentUnpivot, 'confirm-remove-Unpivot-restores-preexisting-related-source-preview', [
    { 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] },
  ], relatedBaseline.workspace.documents[0].construction);
  await applyProposal([{
    'Specimen ID': source.specimen.id,
    [patientIDLabel]: [source.patient.id],
  }], 'remove-Unpivot-and-restore-related-source-binding');
  assert.deepEqual(doc().construction, doc(relatedBaseline).construction,
    'Removing Unpivot must restore the exact earlier related-field construction.');
  assert.deepEqual(doc().columns, doc(relatedBaseline).columns,
    'Removing Unpivot must restore the exact earlier related source column binding.');
  previewRows = [{ 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] }];
  await reloadTable(previewRows, 'reload-restored-pre-Unpivot-related-source-binding');
  recordLifecycleCheck(
    'Removing Unpivot and reloading restores exact Patient.id Related construction and rows',
    JSON.stringify(doc().construction) === JSON.stringify(doc(relatedBaseline).construction) &&
      JSON.stringify(doc().columns) === JSON.stringify(doc(relatedBaseline).columns) &&
      JSON.stringify(doc().population) === JSON.stringify(doc(relatedBaseline).population) &&
      previewRows[0][patientIDLabel]?.[0] === source.patient.id,
    { restoredConstruction: doc().construction, restoredColumns: doc().columns, rows: previewRows, patientID: source.patient.id });

  await requestCapture.flush();
  for (const failure of cda.diagnostics.networkFailures) report.errors.push({ kind: 'browser-network', ...failure });
  for (const failure of cda.diagnostics.httpFailures) report.errors.push({ kind: 'browser-http', ...failure });
  recordLifecycleCheck(
    'All native action and action-to-render checkpoints complete within five seconds',
    report.cases.every(item => item.durationMs <= 5000) &&
      cda.report.actions.every(item => item.status === 'passed' && item.elapsedMs <= 5000),
    { workflowCheckpoints: report.cases.map(({ name, durationMs }) => ({ name, durationMs })),
      maximumNativeActionMs: Math.max(0, ...cda.report.actions.map(item => item.elapsedMs)) });
  recordNoUnexpectedNativeErrorsCheck(report);
  assert.deepEqual(report.errors, [], 'No unexpected native HTTP, runtime, console, or module errors are allowed.');
  assert(report.protectedExplorerUntouched, `A browser request targeted protected Explorer ${protectedExplorer}.`);
  report.status = 'passed';
} catch (error) {
  report.status = error.rawOracleFailure ? 'unverified' : error instanceof ApiBuildFreezeError ? 'invalidated' : 'failed';
  if (error instanceof ApiBuildFreezeError) {
    report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: error.before, ...(error.after?.checked ? { after: error.after } : {}), unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
    report.productFailure = false;
    report.invalidations = [{ kind: 'apiBuildFreeze', reason: error.reason }];
  }
  if (error.rawOracleFailure) {
    report.productFailure = false;
    report.unverifiedReason = error.message;
    report.unverified = { kind: error.unverifiedKind ?? 'raw-source-command', message: error.message, diagnostics: error.diagnostics };
  }
  report.error = String(error.stack ?? error);
  const failedAction = report.activeAction;
if (page) await captureFailure(error, {
    phase: 'related-field-after-unpivot',
    elapsedMs: Date.now() - new Date(report.started).getTime(),
    action: failedAction ? { label: failedAction.label, locator: failedAction.locator, targetLocator: failedAction.targetLocator } : undefined,
  });
  report.failedAction = failedAction ? { label: failedAction.label, locator: failedAction.locator } : undefined;
  if (builder) {
    const state = await api(base + '/builder').catch((readError) => ({ error: String(readError) }));
    const savedDocument = state.workspace?.documents?.find((document) => document.output.id === outputId);
    report.savedStateSummary = state.workspace ? {
      draftVersion: state.draftVersion,
      stepKinds: savedDocument?.construction?.steps?.map((step) => step.operation.kind),
      columnLabels: savedDocument?.columns?.map((column) => column.label),
    } : state;
  }
  report.__nativeFailure = true;
} finally {
  await requestCapture?.flush().catch(() => undefined);
  report.nativeRequests = report.nativeRequests.map(entry => {
    const view = nativeView(entry);
    return {
      requestId: entry.requestId,
      endpoint: view.endpoint,
      method: entry.method,
      durationMs: entry.completedAt === undefined ? undefined : entry.completedAt - entry.startedAt,
      status: entry.status,
      outputId: view.outputId,
      proposalId: view.response?.proposalId,
      previewRowCount: view.response?.preview?.rowCount ?? view.response?.rowCount,
      expectedCancellation: entry.expectedCancellation,
    };
  });
  if (frozenApiBuild) {
    try {
      report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()), finishedAt: new Date().toISOString() };
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.productFailure = false;
      report.apiBuildFreeze = { ...report.apiBuildFreeze, initial: error.before, after: error.after, unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason, finishedAt: new Date().toISOString() };
      report.invalidations = [...(report.invalidations ?? []), { kind: 'apiBuildFreeze', reason: error.reason }];
      report.__nativeFailure = true;
    }
  }
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      error: String(error),
      finishedAt: sourceFreezeFinishedAt,
    };
    report.__nativeFailure = true;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-related-field-after-unpivot-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-related-field-after-unpivot-browser.mjs', report);
  return report;
}
