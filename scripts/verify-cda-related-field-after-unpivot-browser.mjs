import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = `related-field-after-unpivot-${randomUUID()}`;
assert.notEqual(explorer, protectedExplorer);
const evidence = process.argv[2] ?? `/tmp/loom-related-bindings-reshape-verifier/evidence-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const apiRoot = `/api/v1/projects/${project}/explorers`;
const base = `${apiRoot}/${explorer}/authoring/v2`;
const report = {
  status: 'running',
  explorer,
  project,
  generation,
  scenario: 'Patient.id RELATED_SOURCE before UNPIVOT; add Patient.gender after UNPIVOT on the same exact Specimen.subject->Patient route.',
  authorizationClaim: 'Exact selected resource membership is checked. The local project-scoped CDA oracle does not claim restricted-auth coverage.',
  started: new Date().toISOString(),
  protectedExplorerUntouched: true,
  cases: [],
  nativeRequests: [],
  errors: [],
};
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
report.sourceFreeze = { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount };

let browser;
let builder;
let outputId;
const nativeRequests = [];
const nativeById = new Map();
const pendingReads = new Set();

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
const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

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
  const container = process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
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
      RETURN DISTINCT { id: patient.id, _id: patient._id, gender: patient.payload.gender }
  )
  FILTER LENGTH(patients) == 1
  FILTER IS_STRING(patients[0].gender) AND LENGTH(patients[0].gender) > 0
  SORT specimen._key
  LIMIT 1
  LET patient = patients[0]
  RETURN {
    specimen: { id: specimen.id, _id: specimen._id },
    patient: { id: patient.id, _id: patient._id, gender: patient.gender },
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
      RETURN DISTINCT { id: patient.id, _id: patient._id, gender: patient.payload.gender }
  )
  FILTER LENGTH(patients) == 1
  FILTER IS_STRING(patients[0].gender) AND LENGTH(patients[0].gender) > 0
  LET patient = patients[0]
  RETURN {
    specimen: { id: specimen.id, _id: specimen._id },
    patient: { id: patient.id, _id: patient._id, gender: patient.gender },
    route: "Specimen -[subject]-> Patient",
    project: specimen.project,
    generation: specimen.dataset_generation
  }`;

const beginNativeCapture = () => {
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request }) => {
    const url = new URL(request.url);
    if (url.pathname.includes(`/${protectedExplorer}/`)) report.protectedExplorerUntouched = false;
    if (!url.pathname.startsWith(base + '/')) return;
    const endpoint = url.pathname.slice(base.length);
    if (!['/commands', '/construction-proposals', '/construction-choice-proposals', '/preview'].includes(endpoint)) return;
    let body;
    try { body = request.postData ? JSON.parse(request.postData) : undefined; } catch { body = undefined; }
    const entry = {
      requestId,
      endpoint,
      startedAt: Date.now(),
      outputId: body?.outputId ?? url.searchParams.get('outputId') ?? undefined,
      request: body,
      status: undefined,
      response: undefined,
      complete: false,
    };
    nativeById.set(requestId, entry);
    nativeRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = nativeById.get(requestId);
    if (entry) entry.status = response.status;
    if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) {
      report.errors.push({ kind: 'http', status: response.status, endpoint: entry?.endpoint });
    }
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = nativeById.get(requestId);
    if (!entry) return;
    const task = browser.cdp.send('Network.getResponseBody', { requestId })
      .then(({ body, base64Encoded }) => {
        const text = base64Encoded ? Buffer.from(body, 'base64').toString('utf8') : body;
        try { entry.response = JSON.parse(text); } catch { entry.response = undefined; }
      })
      .catch((error) => { entry.bodyError = String(error); })
      .finally(() => { entry.completedAt = Date.now(); entry.complete = true; });
    pendingReads.add(task);
    void task.finally(() => pendingReads.delete(task));
  });
  browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
    report.errors.push({ kind: 'runtime', message: exceptionDetails.exception?.description ?? exceptionDetails.text });
  });
  browser.cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (type === 'error') report.errors.push({
      kind: 'console',
      message: args.map((arg) => arg.value ?? arg.description ?? '').join(' ').slice(0, 300),
    });
  });
  browser.cdp.on('Network.loadingFailed', ({ type, errorText }) => {
    if (type === 'Script' && errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', message: errorText });
  });
};

const waitNative = async (endpoint, startedAt, predicate = () => true, timeoutMs = 5000) => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const entry = nativeRequests.findLast((candidate) =>
      candidate.endpoint === endpoint &&
      candidate.startedAt >= startedAt &&
      candidate.complete &&
      predicate(candidate),
    );
    if (entry) return entry;
    await sleep(40);
  }
  throw new Error(`Timed out waiting for ${endpoint}: ${JSON.stringify(nativeRequests.filter((entry) => entry.startedAt >= startedAt).map(({ endpoint: path, status }) => ({ endpoint: path, status }))) }`);
};

const panelInfo = async () => browserEval(browser.cdp, `
  const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
  const choice = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
  const panel = proposal ?? choice;
  return panel ? {
    selector: proposal ? 'construction-proposal-panel' : 'construction-choice-proposal-panel',
    status: panel.dataset.proposalStatus,
    proposalId: panel.dataset.proposalId,
    text: panel.innerText,
  } : null;
`);

const previewValueFor = (preview, label, rowIndex) => {
  const column = preview.columns?.find((candidate) => candidate.label === label);
  assert(column, `Preview omitted column label ${JSON.stringify(label)}: ${JSON.stringify(preview.columns)}`);
  assert(preview.rows?.[rowIndex], `Preview omitted row ${rowIndex}`);
  return preview.rows[rowIndex][column.column];
};

const assertPreviewRows = (response, expectedRows, name) => {
  assert.equal(response.status, 200, `${name}: proposal HTTP status`);
  assert.equal(response.response?.previewStatus, 'READY', `${name}: ${JSON.stringify(response.response)}`);
  const preview = response.response.preview;
  assert(preview?.receiptId, `${name}: preview receipt missing`);
  assert.equal(preview.receiptId, response.response.proposalId, `${name}: preview must belong to the current proposal`);
  assert.equal(preview.rowCount, expectedRows.length, `${name}: ${JSON.stringify(preview)}`);
  assert.equal(preview.rows.length, expectedRows.length, `${name}: bounded fixture must include every row`);
  const labels = [...new Set(expectedRows.flatMap((row) => Object.keys(row)))];
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
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector('[data-testid="construction-proposal-panel"]') || document.querySelector('[data-testid="construction-choice-proposal-panel"]')) && ['ready','error','needs-repair'].includes((document.querySelector('[data-testid="construction-proposal-panel"]') ?? document.querySelector('[data-testid="construction-choice-proposal-panel"]'))?.dataset.proposalStatus)`,
    5000);
  const panel = await panelInfo();
  assert(panel, `${name}: no proposal panel`);
  assert.equal(panel.status, 'ready', `${name}: ${panel.text}`);
  let request;
  const deadline = startedAt + 5000;
  while (Date.now() < deadline) {
    request = nativeRequests.findLast((entry) =>
      entry.endpoint === '/construction-proposals' &&
      entry.startedAt >= startedAt &&
      entry.complete &&
      entry.response?.proposalId === panel.proposalId,
    );
    if (request) break;
    await sleep(40);
  }
  assert(request, `${name}: no matching native construction-proposal response`);
  const expectedRows = typeof expectedRowsOrFactory === 'function'
    ? expectedRowsOrFactory(request.response)
    : expectedRowsOrFactory;
  const preview = assertPreviewRows(request, expectedRows, name);
  record(name, startedAt, { proposalId: request.response.proposalId, rowCount: preview.rowCount });
  return { panel, request, preview, expectedRows };
};

const visibleTable = async (rowCount, columnCount, name, startedAt) => {
  await waitForBrowser(browser.cdp, `
    (() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table?.getAttribute('aria-rowcount') === ${JSON.stringify(String(Math.min(25, rowCount) + 1))}
        && table?.getAttribute('aria-colcount') === ${JSON.stringify(String(columnCount))}
        && !document.body.innerText.includes('Loading your table…')
        && !document.body.innerText.includes('Preview failed:');
    })()
  `, 5000);
  record(name, startedAt, { rowCount, columnCount });
};

const reloadTable = async (expectedRows, name) => {
  const startedAt = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp,
    `Boolean(document.querySelector(${JSON.stringify(`[data-testid="construction-table-${outputId}"]`)}))`,
    5000);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  const state = await api(base + '/builder');
  builder = state;
  const columnCount = doc().columns.length;
  const preview = await waitNative('/preview', startedAt, (entry) => entry.outputId === outputId);
  assert.equal(preview.status, 200, `${name}: ${JSON.stringify(preview.response)}`);
  assert.equal(preview.response?.rowCount, expectedRows.length);
  assertPreviewRows({ status: preview.status, response: { proposalId: preview.response?.receiptId, previewStatus: 'READY', preview: preview.response } }, expectedRows, name);
  await visibleTable(expectedRows.length, columnCount, name + '-render', startedAt);
  return preview.response;
};

const applyProposal = async (expectedRows, name) => {
  const panel = await panelInfo();
  assert(panel, `${name}: no active proposal`);
  const startedAt = Date.now();
  if (panel.selector === 'construction-choice-proposal-panel') {
    await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Apply columns' });
  } else {
    await click(browser.cdp, '[data-testid="construction-apply-proposal"]');
  }
  await waitForBrowser(browser.cdp,
    `!document.querySelector('[data-testid="construction-proposal-panel"]') && !document.querySelector('[data-testid="construction-choice-proposal-panel"]')`,
    5000);
  const applied = await waitNative('/preview', startedAt, (entry) => entry.outputId === outputId);
  assert.equal(applied.status, 200, `${name}: saved preview returned HTTP ${applied.status}`);
  assert.equal(applied.response?.rowCount, expectedRows.length, `${name}: saved row count`);
  assertPreviewRows({ status: applied.status, response: { proposalId: applied.response?.receiptId, previewStatus: 'READY', preview: applied.response } }, expectedRows, name);
  builder = await api(base + '/builder');
  await visibleTable(expectedRows.length, doc().columns.length, name, startedAt);
  return applied.response;
};

const cancelProposal = async (expectedWorkspace, name) => {
  const startedAt = Date.now();
  const panel = await panelInfo();
  assert(panel, `${name}: no proposal to cancel`);
  if (panel.selector === 'construction-choice-proposal-panel') {
    await click(browser.cdp, '[data-testid="construction-choice-proposal-panel"] button', { name: 'Cancel' });
  } else {
    await click(browser.cdp, '[data-testid="construction-cancel-proposal"]');
  }
  await waitForBrowser(browser.cdp,
    `!document.querySelector('[data-testid="construction-proposal-panel"]') && !document.querySelector('[data-testid="construction-choice-proposal-panel"]')`,
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

const assertDirectPatientRoute = (step, path) => {
  const related = step.operation.relatedSource;
  assert(related, `Step ${step.id} lost its RELATED_SOURCE payload`);
  assert.equal(related.source.resourceType, 'Patient');
  assert(related.source.path === path || related.source.path.endsWith('.' + path), `Unexpected source path: ${related.source.path}`);
  assert.equal(related.form, 'ALL');
  assert.equal(related.route.length, 1, JSON.stringify(related.route));
  const [hop] = related.route;
  assert.equal(hop.fromResourceType, 'Specimen');
  assert.equal(hop.toResourceType, 'Patient');
  assert.equal(hop.relationship, 'subject_Patient');
  assert.equal(hop.storageDirection, 'OUTBOUND');
};

const openRelatedField = async (fieldPath) => {
  await click(browser.cdp, '[data-testid="construction-action-add-columns"]');
  await click(browser.cdp, '[aria-label="Column types"] button', { includes: 'Fields and related data' });
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-add-columns-source"]'))`, 5000);
  if (!await browserEval(browser.cdp, `return document.querySelector('[aria-label="Related resources"] summary')?.parentElement.open;`)) {
    await click(browser.cdp, '[aria-label="Related resources"] summary');
  }
  const sources = await browserEval(browser.cdp, `
    return [...document.querySelectorAll('[data-testid="construction-add-columns-source-option"]')]
      .map((option) => ({ label: option.getAttribute('aria-label') ?? '', disabled: option.disabled }))
      .filter((option) => option.label.includes('Patient') && option.label.includes('Related resource'));
  `);
  assert.equal(sources.length, 1, `Expected one retained Patient related source, got ${JSON.stringify(sources)}`);
  assert.equal(sources[0].disabled, false, `Retained Patient source is disabled: ${sources[0].label}`);
  await click(browser.cdp,
    `[data-testid="construction-add-columns-source-option"][aria-label=${JSON.stringify(sources[0].label)}]`);
  if (!await browserEval(browser.cdp, `return document.querySelector('[data-testid="feature-catalog-raw-fields"] summary')?.parentElement.open;`)) {
    await click(browser.cdp, '[data-testid="feature-catalog-raw-fields"] summary');
  }
  const candidateSelector = `input[aria-label=${JSON.stringify(`Select Patient.${fieldPath}`)}]`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(candidateSelector + ':not(:disabled)')}))`, 5000);
  const startedAt = Date.now();
  await click(browser.cdp, candidateSelector);
  await click(browser.cdp, '[aria-label="Add columns editor"] button', { includes: 'Add 1 selected feature' });
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 5000);
  const controls = await browserEval(browser.cdp, `
    const dialog = document.querySelector('[role="dialog"]');
    const radios = [...(dialog?.querySelectorAll('input[type="radio"]') ?? [])]
      .map((input) => ({ aria: input.getAttribute('aria-label') ?? '', checked: input.checked }));
    return { radios, groupedPolicy: dialog?.querySelector('select[aria-label="Values per grouped row"]')?.value };
  `);
  const route = controls.radios.find((radio) =>
    radio.aria.includes('Specimen -[subject]-> Patient'));
  const form = controls.radios.find((radio) => radio.aria.includes('Keep all matching values'));
  assert(route, `No exact Specimen.subject -> Patient choice: ${JSON.stringify(controls.radios)}`);
  assert(form, `No ALL values choice for Patient.${fieldPath}: ${JSON.stringify(controls.radios)}`);
  await click(browser.cdp, `[role="dialog"] input[aria-label=${JSON.stringify(route.aria)}]`);
  await click(browser.cdp, `[role="dialog"] input[aria-label=${JSON.stringify(form.aria)}]`);
  if (controls.groupedPolicy !== undefined) {
    await selectOption(browser.cdp, '[role="dialog"] select[aria-label="Values per grouped row"]', 'ALL');
  }
  await click(browser.cdp, '[role="dialog"] button', { name: 'Add 1 column' });
  return startedAt;
};

const beginUnpivot = async (sourceLabel) => {
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp,
    `Boolean([...document.querySelectorAll('button')].find((button) => button.innerText.trim() === 'Turn columns into rows' && !button.disabled))`,
    5000);
  await click(browser.cdp, 'button', { name: 'Turn columns into rows' });
  const selector = `input[aria-label=${JSON.stringify(`Unpivot ${sourceLabel}`)}]`;
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(selector + ':not(:disabled)')}))`, 5000);
  const startedAt = Date.now();
  await click(browser.cdp, selector);
  return startedAt;
};

const editRelatedLabel = async (step, nextLabel) => {
  await click(browser.cdp, `[data-testid="construction-history-step-${step.id}"]`);
  await click(browser.cdp, `[data-testid="construction-edit-step-${step.id}"]`);
  const selector = '[data-testid="related-source-step-editor"] input[aria-label="Output column label"]';
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector(${JSON.stringify(selector + ':not(:disabled)')}))`, 5000);
  await click(browser.cdp, selector);
  await browserEval(browser.cdp, `document.activeElement.select();`);
  const startedAt = Date.now();
  await browser.cdp.send('Input.insertText', { text: nextLabel });
  return startedAt;
};

const removeStep = async (step, name, expectedRows, expectedPreviousConstruction) => {
  await click(browser.cdp, `[data-testid="construction-history-step-${step.id}"]`);
  const startedAt = Date.now();
  await click(browser.cdp, `[data-testid="construction-remove-step-${step.id}"]`);
  const preview = await proposal(name, startedAt, expectedRows);
  const removalSteps = await browserEval(browser.cdp,
    `return [...document.querySelectorAll('[data-testid^="construction-removal-step-"]')].map((element) => element.dataset.testid);`);
  assert(removalSteps.includes('construction-removal-step-' + step.id), `${name}: removal preview omitted selected step ${step.id}`);
  if (expectedPreviousConstruction) {
    assert(removalSteps.length === 1, `${name}: expected only the selected step to be removed, got ${JSON.stringify(removalSteps)}`);
    assert.deepEqual(preview.request.response.candidateConstruction, expectedPreviousConstruction,
      `${name}: removal proposal did not restore the exact preceding construction.`);
  }
  return preview;
};

try {
  const oracleScope = { project, generation };
  const candidates = rawQuery(scopedSpecimenCandidatesQuery, oracleScope);
  assert(candidates.length <= 2000, 'Scoped raw-source candidate scan exceeded 2000 Specimens');
  assert(candidates.every(candidate => candidate.project === project && candidate.generation === generation && candidate._id?.startsWith('Specimen/')),
    'Scoped raw-source candidate scan returned a resource outside the requested CDA slice');
  if (candidates.length === 0) {
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: 2000, candidateCount: 0,
      result: 'no-candidates-within-bounded-scan',
    };
    const unavailable = new Error('The bounded scoped Specimen candidate query returned no resources; no Builder behavior was tested.');
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'bounded-candidate-scan-empty';
    unavailable.productFailure = false;
    unavailable.diagnostics = { candidateLimit: 2000, candidateCount: 0 };
    throw unavailable;
  }

  let witnesses;
  try {
    witnesses = rawQuery(linkedPatientWitnessQuery, {
      ...oracleScope,
      specimenKeys: candidates.map(candidate => candidate._id),
    });
  } catch (error) {
    if (!error.rawOracleFailure) throw error;
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: 2000, candidateCount: candidates.length,
    };
    throw error;
  }
  const [candidateWitness] = witnesses;
  if (!candidateWitness) {
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: 2000, candidateCount: candidates.length,
      linkedPatientWitnesses: 0,
      result: 'no-witness-within-bounded-candidate-scan',
    };
    const unavailable = new Error('No qualifying direct Patient link with populated gender was found among the first 2000 scoped Specimens by _key; this does not establish absence from the full project generation.');
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'bounded-linked-patient-witness-not-found';
    unavailable.productFailure = false;
    unavailable.diagnostics = { candidateLimit: 2000, candidateCount: candidates.length, linkedPatientWitnesses: 0 };
    throw unavailable;
  }

  let exactRows;
  try {
    exactRows = rawQuery(exactLinkedPatientQuery, {
      ...oracleScope,
      specimenKey: candidateWitness.specimen._id,
      specimenID: candidateWitness.specimen.id,
    });
  } catch (error) {
    if (!error.rawOracleFailure) throw error;
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: 2000, candidateCount: candidates.length,
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
    exactRows[0].project === candidateWitness.project &&
    exactRows[0].generation === candidateWitness.generation;
  if (!exactRereadMatches) {
    report.oracle = {
      oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
      project, generation, candidateLimit: 2000, candidateCount: candidates.length,
      candidateWitness: candidateWitness.specimen.id,
      exactRereadCount: exactRows.length,
      result: 'exact-record-reread-did-not-confirm-witness',
    };
    const unavailable = new Error('The independently queried exact selected Specimen no longer matches the bounded witness; no Builder behavior was tested.');
    unavailable.name = 'RawOracleUnavailableError';
    unavailable.rawOracleFailure = true;
    unavailable.unverifiedKind = 'exact-record-reread-mismatch';
    unavailable.productFailure = false;
    unavailable.diagnostics = { candidateLimit: 2000, candidateWitness: candidateWitness.specimen.id, exactRereadCount: exactRows.length };
    throw unavailable;
  }
  const source = exactRows[0];
  assert(source?.specimen?.id && source?.patient?.id && source?.patient?.gender,
    'The scoped CDA oracle needs one Specimen with exactly one linked Patient whose gender is populated.');
  assert.equal(source.project, project);
  assert.equal(source.generation, generation);
  report.oracle = {
    oracleSource: 'ArangoDB raw Specimen documents plus fhir_edge links',
    candidateQuery: scopedSpecimenCandidatesQuery,
    witnessQuery: linkedPatientWitnessQuery,
    exactRereadQuery: exactLinkedPatientQuery,
    route: source.route,
    project, generation, candidateLimit: 2000, candidateCount: candidates.length,
    selectedCandidateWitnessCount: 1,
    exactRereadCount: exactRows.length,
    source: {
      specimenID: source.specimen.id,
      patientID: source.patient.id,
      patientGender: source.patient.gender,
      project: source.project,
      generation: source.generation,
    },
    patientMatches: 1,
  };

  await api(apiRoot, { name: explorer, title: 'Related field after Unpivot QA' });
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
  browser = await launchBrowser(evidence);
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
  assert(/patient/i.test(patientIDLabel) && /\bid\b/i.test(patientIDLabel),
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

  const beforeGender = structuredClone(builder);
  start = await openRelatedField('gender');
  let genderLabel;
  const genderLabelProposal = await proposal('add-Patient-gender-after-Unpivot-preview', start, (response) => {
    const step = response.candidateConstruction.steps.findLast((candidate) =>
      candidate.operation.kind === 'RELATED_SOURCE' &&
      (candidate.operation.relatedSource?.source?.path === 'gender' || candidate.operation.relatedSource?.source?.path?.endsWith('.gender')));
    assert(step, 'Post-Unpivot Patient.gender access did not produce a related source operation.');
    genderLabel = outputForStep(step, step.operation.relatedSource.outputColumnId).label;
    return [{
      [patientIDLabel]: [source.patient.id],
      [keyLabel]: 'Specimen ID',
      [valueLabel]: source.specimen.id,
      [genderLabel]: [source.patient.gender],
    }];
  });
  const genderStepProposal = genderLabelProposal.request.response.candidateConstruction.steps.findLast((step) =>
    step.operation.kind === 'RELATED_SOURCE' &&
    (step.operation.relatedSource?.source?.path === 'gender' || step.operation.relatedSource?.source?.path?.endsWith('.gender')));
  assert(genderStepProposal, 'Post-Unpivot Patient.gender access did not produce a related source operation.');
  assertDirectPatientRoute(genderStepProposal, 'gender');
  assert.equal(outputForStep(genderStepProposal, genderStepProposal.operation.relatedSource.outputColumnId).label, genderLabel);
  await cancelProposal(beforeGender.workspace, 'cancel-Patient-gender-after-Unpivot-preserves-existing-binding');

  start = await openRelatedField('gender');
  const genderProposal = await proposal('confirm-Patient-gender-after-Unpivot-preview', start, [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [genderLabel]: [source.patient.gender],
  }]);
  await applyProposal([{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [genderLabel]: [source.patient.gender],
  }], 'apply-Patient-gender-after-Unpivot');
  let genderBaseline = structuredClone(builder);
  let savedGender = relatedSourceStepFor(genderBaseline, 'gender');
  assert(savedGender, 'Applied post-Unpivot Patient.gender binding is missing.');
  assertDirectPatientRoute(savedGender, 'gender');
  const savedGenderOutput = outputForStep(savedGender, savedGender.operation.relatedSource.outputColumnId);
  assert.equal(savedGenderOutput.label, genderLabel);
  assert.deepEqual(savedGender.operation.relatedSource.route, savedPatientID.operation.relatedSource.route,
    'Post-Unpivot source selection changed the existing exact Patient route.');
  assert.deepEqual(savedPatientID.operation.relatedSource.route, patientIDStep.operation.relatedSource.route,
    'The pre-Unpivot Patient route did not survive the reshape.');
  previewRows = [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [genderLabel]: [source.patient.gender],
  }];
  await reloadTable(previewRows, 'reload-related-Patient-gender-after-Unpivot');

  const beforeEdit = structuredClone(builder);
  const editedLabel = 'Patient gender after Unpivot';
  start = await editRelatedLabel(savedGender, editedLabel);
  await proposal('edit-related-source-label-after-Unpivot-preview', start, [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [editedLabel]: [source.patient.gender],
  }]);
  await cancelProposal(beforeEdit.workspace, 'cancel-related-source-edit-preserves-route-and-output');

  savedGender = relatedSourceStepFor(builder, 'gender');
  start = await editRelatedLabel(savedGender, editedLabel);
  await proposal('confirm-related-source-label-edit-after-Unpivot-preview', start, [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [editedLabel]: [source.patient.gender],
  }]);
  await applyProposal([{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [editedLabel]: [source.patient.gender],
  }], 'apply-related-source-label-edit-after-Unpivot');
  genderBaseline = structuredClone(builder);
  savedGender = relatedSourceStepFor(genderBaseline, 'gender');
  assert(savedGender);
  assert.equal(outputForStep(savedGender, savedGender.operation.relatedSource.outputColumnId).label, editedLabel);
  assertDirectPatientRoute(savedGender, 'gender');
  assert.deepEqual(savedGender.operation.relatedSource.source, genderStepProposal.operation.relatedSource.source);
  assert.deepEqual(savedGender.operation.relatedSource.route, genderStepProposal.operation.relatedSource.route);
  assert.deepEqual(steps(genderBaseline).find((step) => step.id === savedUnpivot.id), savedUnpivot,
    'Editing the related source changed the saved Unpivot operation.');
  previewRows = [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
    [editedLabel]: [source.patient.gender],
  }];
  await reloadTable(previewRows, 'reload-edited-related-source-after-Unpivot');

  await removeStep(savedGender, 'remove-post-Unpivot-Patient-gender-preview', [
    {
      [patientIDLabel]: [source.patient.id],
      [keyLabel]: 'Specimen ID',
      [valueLabel]: source.specimen.id,
    },
  ], unpivotBaseline.workspace.documents[0].construction);
  await cancelProposal(genderBaseline.workspace, 'cancel-post-Unpivot-related-source-removal-preserves-binding');

  savedGender = relatedSourceStepFor(builder, 'gender');
  await removeStep(savedGender, 'confirm-remove-post-Unpivot-Patient-gender-preview', [
    {
      [patientIDLabel]: [source.patient.id],
      [keyLabel]: 'Specimen ID',
      [valueLabel]: source.specimen.id,
    },
  ], unpivotBaseline.workspace.documents[0].construction);
  await applyProposal([{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
  }], 'remove-post-Unpivot-Patient-gender');
  assert.deepEqual(doc().construction, doc(unpivotBaseline).construction,
    'Removing the post-Unpivot field must restore the exact saved Unpivot construction.');
  assert.deepEqual(doc().population, doc(unpivotBaseline).population);
  previewRows = [{
    [patientIDLabel]: [source.patient.id],
    [keyLabel]: 'Specimen ID',
    [valueLabel]: source.specimen.id,
  }];
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

  await Promise.all(pendingReads);
  assert.deepEqual(report.errors, [], 'No unexpected native HTTP, runtime, console, or module errors are allowed.');
  assert(report.protectedExplorerUntouched, `A browser request targeted protected Explorer ${protectedExplorer}.`);
  report.status = 'passed';
} catch (error) {
  report.status = error.rawOracleFailure ? 'unverified' : 'failed';
  if (error.rawOracleFailure) {
    report.productFailure = false;
    report.unverifiedReason = error.message;
    report.unverified = { kind: error.unverifiedKind ?? 'raw-source-command', message: error.message, diagnostics: error.diagnostics };
  }
  report.error = String(error.stack ?? error);
  if (builder) {
    const state = await api(base + '/builder').catch((readError) => ({ error: String(readError) }));
    report.savedStateSummary = state.workspace ? {
      draftVersion: state.draftVersion,
      stepKinds: state.workspace.documents?.find((document) => document.output.id === outputId)?.construction.steps.map((step) => step.operation.kind),
      columnLabels: state.workspace.documents?.find((document) => document.output.id === outputId)?.columns.map((column) => column.label),
    } : state;
  }
  if (!error.rawOracleFailure || error.unverifiedKind === 'raw-source-command' || error.unverifiedKind === 'exact-record-reread-command') {
    process.exitCode = 1;
  }
} finally {
  await Promise.allSettled([...pendingReads]);
  report.nativeRequests = nativeRequests.map(({ endpoint, startedAt, completedAt, status, response }) => ({
    endpoint,
    durationMs: completedAt === undefined ? undefined : completedAt - startedAt,
    status,
    proposalId: response?.proposalId,
    previewRowCount: response?.preview?.rowCount ?? response?.rowCount,
  }));
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
