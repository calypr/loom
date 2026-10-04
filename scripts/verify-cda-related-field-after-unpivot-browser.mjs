import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { classifyNullableSourceScalar, expectedNullableRelatedAll } from './lib/nullable-related-all.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp, ApiBuildFreezeError } from './lib/api-build-freeze.mjs';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const generation = process.env.LOOM_CDA_GENERATION;
const afterUnpivotCase = process.env.LOOM_RELATED_AFTER_UNPIVOT_CASE ?? 'gender-all';
assert(['gender-all', 'gender-null-all', 'resource-type-all', 'id-count'].includes(afterUnpivotCase), `Unsupported LOOM_RELATED_AFTER_UNPIVOT_CASE: ${afterUnpivotCase}`);
const nullableGenderCase = afterUnpivotCase === 'gender-null-all';
const knownGenderWitnessReport = process.env.LOOM_CDA_GENDER_NULL_WITNESS_REPORT ?? '/tmp/loom-related-resource-type-after-unpivot-native-complete/report.json';
const afterUnpivotFieldPath = afterUnpivotCase === 'id-count'
  ? 'id'
  : afterUnpivotCase === 'resource-type-all' ? 'resourceType' : 'gender';
const afterUnpivotForm = afterUnpivotCase === 'id-count' ? 'COUNT' : 'ALL';
const requireFieldWitness = afterUnpivotCase === 'gender-all' || afterUnpivotCase === 'resource-type-all';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `related-field-after-unpivot-${randomUUID()}`;
assert.notEqual(explorer, protectedExplorer);
const evidence = process.argv[2] ?? `/tmp/loom-related-bindings-reshape-verifier/evidence-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
assert(apiOrigin && uiOrigin, 'Set LOOM_CDA_API_ORIGIN and LOOM_CDA_UI_ORIGIN to the isolated CDA stack.');
assert.equal(generation, 'cda-fhir-v1', 'Set LOOM_CDA_GENERATION to the loaded CDA FHIR generation.');
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
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
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(sourceRoot);
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };
let frozenApiBuild;

let browser;
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
report.ownedTarget = ownedTarget;
let requestCapture;
const page = () => browser.page;
const inspect = callback => page().evaluate(callback);
const waitUI = (condition, timeout = 30000) => page().waitForFunction(condition, undefined, { timeout });
const navigateUI = url => page().goto(url, { waitUntil: 'domcontentloaded', timeout: 30000 });
const clickUI = (selector, options = {}) => {
  let locator = page().locator(selector);
  if (options.name) locator = locator.and(page().getByRole('button', { name: options.name, exact: true }));
  if (options.includes) locator = locator.and(page().getByRole('button', { name: new RegExp(options.includes.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') }));
  return performAction(report, options.name ?? options.includes ?? selector, locator, target => target.click({ timeout: 5000 }));
};
const selectUI = (selector, value) => {
  const locator = page().locator(selector);
  return performAction(report, `Select ${value}`, locator, (target, { timeout }) => target.selectOption(value, { timeout }));
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
  requestCapture = captureCDARequests(page(), {
    apiOrigin,
    browserRequestOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: base,
    report,
  });
  page().on('request', request => {
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
  await waitUI(`Boolean(document.querySelector('[data-testid="construction-proposal-panel"]') || document.querySelector('[data-testid="construction-choice-proposal-panel"]')) && ['ready','error','needs-repair'].includes((document.querySelector('[data-testid="construction-proposal-panel"]') ?? document.querySelector('[data-testid="construction-choice-proposal-panel"]'))?.dataset.proposalStatus)`,
    5000);
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
  await waitUI(`
    (() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const dataRows = [...(table?.querySelectorAll('[role="row"]') ?? [])]
        .filter((row) => row.querySelector('[role="cell"]'));
      return table?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, rowCount) + 1))}
        && table?.getAttribute('aria-colcount') === ${JSON.stringify(String(columnCount))}
        && dataRows.length === ${JSON.stringify(rowCount)}
        && !document.body.innerText.includes('Loading your table…')
        && !document.body.innerText.includes('Preview failed:');
    })()
  `, 5000);
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
  await waitUI(`Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`,
    5000);
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
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]') && !document.querySelector('[data-testid="construction-choice-proposal-panel"]')`,
    5000);
  const command = await waitNative('/commands', startedAt);
  assert.equal(command.status, 200, `${name}: Apply command failed`);
  builder = await api(base + '/builder');
  await page().waitForFunction(({ outputId, draftDigest, draftVersion }) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready'
      && preview.dataset.previewOutputId === outputId
      && preview.dataset.currentDraftDigest === draftDigest
      && preview.dataset.currentDraftVersion === draftVersion;
  }, { outputId, draftDigest: builder.draftDigest, draftVersion: String(builder.draftVersion) }, { timeout: 5000 });
  const receiptId = await page().locator('[data-testid="construction-preview"]').getAttribute('data-preview-receipt-id');
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
  await waitUI(`!document.querySelector('[data-testid="construction-proposal-panel"]') && !document.querySelector('[data-testid="construction-choice-proposal-panel"]')`,
    5000);
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
  await waitUI(`Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 5000);
  if (!await page().locator('[aria-label="Related resources"] summary').evaluate(summary => summary.parentElement.open)) {
    await clickUI('[aria-label="Related resources"] summary');
  }
  const sources = await page().locator('[data-testid="construction-add-columns-source-option"]').evaluateAll(options =>
    options
      .map((option) => ({ label: option.getAttribute('aria-label') ?? '', disabled: option.disabled }))
      .filter((option) => option.label.includes('Patient') && option.label.includes('Related resource')));
  assert.equal(sources.length, 1, `Expected one retained Patient related source, got ${JSON.stringify(sources)}`);
  assert.equal(sources[0].disabled, false, `Retained Patient source is disabled: ${sources[0].label}`);
  await clickUI(`[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(sources[0].label)}]`);
  if (!await page().getByTestId('feature-catalog-raw-fields').locator('summary').evaluate(summary => summary.parentElement.open)) {
    await clickUI('[data-testid="feature-catalog-raw-fields"] summary');
  }
  const candidateSelector = `input[aria-label=${JSON.stringify(`Select Patient.${fieldPath}`)}]`;
  await waitUI(`Boolean(document.querySelector(${JSON.stringify(candidateSelector + ':not(:disabled)')}))`, 5000);
  const startedAt = Date.now();
  await clickUI(candidateSelector);
  await clickUI('[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitUI(`Boolean(document.querySelector('[role="dialog"]'))`, 5000);
  const controls = await page().getByRole('dialog').evaluate(dialog => {
    const radios = [...(dialog?.querySelectorAll('input[type="radio"]') ?? [])]
      .map((input) => ({ aria: input.getAttribute('aria-label') ?? '', checked: input.checked }));
    return { radios, groupedPolicy: dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value };
  });
  const route = controls.radios.find((radio) =>
    radio.aria.includes('Specimen -[subject]-> Patient'));
  const formLabel = desiredForm === 'COUNT' ? 'Count matching records' : 'Keep all matching values';
  assert(route, `No exact Specimen.subject -> Patient choice: ${JSON.stringify(controls.radios)}`);
  await clickUI(`[role="dialog"] input[aria-label=${JSON.stringify(route.aria)}]`);
  await waitUI(`Boolean([...document.querySelectorAll('[role="dialog"] input[type="radio"]')].find(input=>(input.getAttribute('aria-label')??'').includes(${JSON.stringify(formLabel)})))`, 5000);
  const formChoices = await page().getByRole('dialog').locator('input[type="radio"]').evaluateAll(radios => radios.map(input=>input.getAttribute('aria-label')??''));
  const formChoice = formChoices.find(label=>label.includes(formLabel));
  assert(formChoice, `No ${desiredForm} choice for Patient.${fieldPath}: ${JSON.stringify(formChoices)}`);
  await clickUI(`[role="dialog"] input[aria-label=${JSON.stringify(formChoice)}]`);
  if (desiredForm === 'ALL' && controls.groupedPolicy !== undefined) {
    await selectUI('[role="dialog"] select[aria-label="Values per grouped row"]', 'ALL');
  }
  await clickUI('[role="dialog"] button', { name: 'Add 1 column' });
  return startedAt;
};

const beginUnpivot = async (sourceLabel) => {
  await clickUI('[data-testid="construction-rows-settings-trigger"]');
  await waitUI(`Boolean(document.querySelector('[data-testid="construction-action-unpivot-rows"]:not(:disabled)'))`,
    5000);
  await clickUI('[data-testid="construction-action-unpivot-rows"]');
  const selector = `input[aria-label=${JSON.stringify(`Unpivot ${sourceLabel}`)}]`;
  await waitUI(`Boolean(document.querySelector(${JSON.stringify(selector + ':not(:disabled)')}))`, 5000);
  const startedAt = Date.now();
  await clickUI(selector);
  return startedAt;
};

const editRelatedLabel = async (step, nextLabel) => {
  await clickUI(`[data-testid="construction-history-step-${step.id}"]`);
  await clickUI(`[data-testid="construction-edit-step-${step.id}"]`);
  const selector = '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]';
  await waitUI(`Boolean(document.querySelector(${JSON.stringify(selector + ':not(:disabled)')}))`, 5000);
  const locator = page().locator(selector);
  const startedAt = Date.now();
  await performAction(report, `Edit related field label to ${nextLabel}`, locator, (target, { timeout }) => target.fill(nextLabel, { timeout }), { editable: true });
  return startedAt;
};

const removeStep = async (step, name, expectedRows, expectedPreviousConstruction) => {
  await clickUI(`[data-testid="construction-history-step-${step.id}"]`);
  const startedAt = Date.now();
  await clickUI(`[data-testid="construction-remove-step-${step.id}"]`);
  const preview = await proposal(name, startedAt, expectedRows);
  const removalSteps = await page().locator('[data-testid^="construction-removal-step-"]').evaluateAll(elements => elements.map(element => element.dataset.testid));
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
  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: true });
  beginNativeCapture();

  let previewRows = [{ 'Specimen ID': source.specimen.id }];
  let baseline = await reloadTable(previewRows, 'load-exact-CDA-Specimen');
  assert.equal(baseline.rowCount, 1);

  const initialWorkspace = structuredClone(builder.workspace);
  let start = await openRelatedField('id');
  let patientIDLabel;
  const addedID = await proposal('add-related-Patient-ID-before-Unpivot-preview', start, (response) => {
    const step = response.candidateConstruction.steps.findLast((candidate) =>
      candidate.operation.kind === 'RELATED_SOURCE' &&
      (candidate.operation.relatedSource?.source?.path === 'id' || candidate.operation.relatedSource?.source?.path?.endsWith('.id')));
    assert(step, 'The related Patient.id proposal did not produce a RELATED_SOURCE step.');
    patientIDLabel = outputForStep(step, step.operation.relatedSource.outputColumnId).label;
    return [{ 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] }];
  });
  const initialRelatedStep = addedID.request.response.candidateConstruction.steps.findLast((step) =>
    step.operation.kind === 'RELATED_SOURCE' &&
    (step.operation.relatedSource?.source?.path === 'id' || step.operation.relatedSource?.source?.path?.endsWith('.id')));
  assert(initialRelatedStep, 'The related Patient.id proposal did not produce a RELATED_SOURCE step.');
  assertDirectPatientRoute(initialRelatedStep, 'id');
  await cancelProposal(initialWorkspace, 'cancel-related-Patient-ID-preview-restores-source-workspace');

  start = await openRelatedField('id');
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
  await cancelProposal(relatedBaseline.workspace, 'cancel-Unpivot-preview-preserves-related-source-binding');

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

  const reshapedRetainedPatientRow = structuredClone(previewRows[0]);
  const postUnpivotValue = nullableGenderCase
    ? expectedNullableRelatedAll({ relatedCount: source.patientCount, sourceState: sourceGenderState.state })
    : afterUnpivotCase === 'id-count' ? source.patientCount : [source.patient[afterUnpivotFieldPath]];
  const postUnpivotValueForRow = (label) => ({ ...reshapedRetainedPatientRow, [label]: postUnpivotValue });
  const beforePostUnpivotField = structuredClone(builder);
  start = await openRelatedField(afterUnpivotFieldPath, afterUnpivotForm);
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
  await cancelProposal(beforePostUnpivotField.workspace, `cancel-Patient-${afterUnpivotFieldPath}-${afterUnpivotForm}-after-Unpivot-preserves-existing-binding`);

  start = await openRelatedField(afterUnpivotFieldPath, afterUnpivotForm);
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

  const beforeEdit = structuredClone(builder);
  const editedLabel = `Patient ${afterUnpivotFieldPath} ${afterUnpivotForm.toLowerCase()} after Unpivot`;
  assert(!Object.hasOwn(reshapedRetainedPatientRow, editedLabel));
  start = await editRelatedLabel(savedPostUnpivotStep, editedLabel);
  await proposal('edit-related-source-label-after-Unpivot-preview', start, [postUnpivotValueForRow(editedLabel)]);
  await cancelProposal(beforeEdit.workspace, 'cancel-related-source-edit-preserves-route-and-output');

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

  await removeStep(savedPostUnpivotStep, 'remove-post-Unpivot-related-source-preview', previewRows.map(({ [editedLabel]: _removed, ...row }) => row),
    unpivotBaseline.workspace.documents[0].construction);
  await cancelProposal(postUnpivotBaseline.workspace, 'cancel-post-Unpivot-related-source-removal-preserves-binding');

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

  const currentUnpivot = lastStep(builder, 'UNPIVOT');
  await removeStep(currentUnpivot, 'remove-Unpivot-restores-preexisting-related-source-preview', [
    { 'Specimen ID': source.specimen.id, [patientIDLabel]: [source.patient.id] },
  ], relatedBaseline.workspace.documents[0].construction);
  await cancelProposal(builder.workspace, 'cancel-Unpivot-removal-preserves-related-source-binding');

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

  await requestCapture.flush();
  for (const failure of browser.diagnostics.networkFailures) report.errors.push({ kind: 'browser-network', ...failure });
  for (const failure of browser.diagnostics.httpFailures) report.errors.push({ kind: 'browser-http', ...failure });
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
  if (browser) await browser.captureFailure(error, {
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
  if (!error.rawOracleFailure || error.unverifiedKind === 'raw-source-command' || error.unverifiedKind === 'exact-record-reread-command') {
    process.exitCode = 1;
  }
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
      process.exitCode = 1;
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
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({
  status: report.status,
  evidence,
  explorer,
  route: report.oracle?.route,
  cases: report.cases.map(({ name, durationMs }) => ({ name, durationMs })),
  error: report.error,
}));
