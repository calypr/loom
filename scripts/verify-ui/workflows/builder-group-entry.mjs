import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import {
  findRenderedBuilderHeaderIndex,
  normalizeRenderedBuilderHeader,
  waitForBuilderRenderedGrid,
} from '../helpers/builder-rendered-grid.mjs';
import { fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import {
  addFieldWithCommandTrace,
  captureBuilderObservation,
  combineAuthoringObservation,
  observeAuthoringAttempt,
} from '../helpers/fast-fixtures.mjs';
import { addField } from '../helpers/cda-journey-fields.mjs';

const expectedPatientIDs = ['dev-patient-001', 'dev-patient-002'];
const legacyBasicSourceLabels = ['id', 'name[].family [0]', 'name[].family [1]', 'name__count', 'gender'];
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const journeyContractPath = 'verification/builder-interactions/builder-edit-remove-contract.json';
const journeyTraceContractPath = join(repositoryRoot, journeyContractPath);
const sourceManifestPath = join(repositoryRoot, 'verification/builder-interactions/builder-preview-contract.json');
const proposalPanel = '[data-testid="construction-proposal-panel"]';
const proposalPreview = '[data-testid="construction-proposal-preview"]';
const renderedTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';

const readNDJSON = path => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map(line => JSON.parse(line));

const canonicalJson = value => Array.isArray(value)
  ? `[${value.map(canonicalJson).join(',')}]`
  : value && typeof value === 'object'
    ? `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJson(value[key])}`).join(',')}}`
    : JSON.stringify(value);

const journeyTraceProvenance = () => {
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: repositoryRoot, encoding: 'utf8' });
  if (head.error || head.status !== 0) {
    throw new Error(`Cannot resolve Builder trace source HEAD: ${head.error?.message ?? head.stderr ?? head.stdout}`);
  }
  const sourceManifest = JSON.parse(readFileSync(sourceManifestPath, 'utf8'));
  const sourceEntries = sourceManifest.sources.map(source => {
    const actual = createHash('sha256').update(readFileSync(join(repositoryRoot, source.path))).digest('hex');
    if (actual !== source.sha256) {
      throw new Error(`Builder trace source pin drift at ${source.path}: expected ${source.sha256}, found ${actual}`);
    }
    return { path: source.path, sha256: actual };
  }).sort((left, right) => left.path.localeCompare(right.path));
  const contract = JSON.parse(readFileSync(journeyTraceContractPath, 'utf8'));
  return {
    sourceProfile: 'journey',
    sourceCommit: head.stdout.trim(),
    contractSourceCommit: contract.sourceCommit,
    sourceFingerprint: createHash('sha256').update(canonicalJson(sourceEntries)).digest('hex'),
  };
};

const recordPreviewStart = (actions, started) => actions.push({
  label: 'Preview',
  event: 'preview',
  owner: started.owner,
  request: started.request,
  observation: started.requestObservation,
});

const previewSettlementAction = (started, exchange, observation = exchange.responseObservation, replacementStarted = null) => {
  const cancelled = exchange.response === null
    && exchange.settlement?.kind === 'request-failed'
    && exchange.settlement.errorText === 'net::ERR_ABORTED';
  const cancellationArrivedAfterReplacement = cancelled
    && replacementStarted
    && exchange.responseSequence > replacementStarted.sequence;
  const ready = exchange.response?.status >= 200
    && exchange.response.status < 300
    && exchange.response.errorCode == null
    && exchange.response.proposalPreviewStatus === 'READY'
    && Boolean(exchange.response.preview?.receiptId)
    && observation.status === 'ready';
  if (!cancelled && !ready) {
    throw new Error(`Cannot classify proposal preview ${started.owner.attempt} from the captured response and rendered state.`);
  }
  return {
    label: cancelled
      ? cancellationArrivedAfterReplacement ? 'LatePreviewErrorIgnored' : 'PreviewRequestCancelled'
      : 'PreviewComplete',
    event: cancelled && !cancellationArrivedAfterReplacement ? 'preview-request-cancelled' : 'preview-response',
    owner: started.owner,
    request: exchange.request,
    response: exchange.response,
    settlement: exchange.settlement,
    observation: cancelled ? observation : combineAuthoringObservation(exchange, observation),
  };
};

const recordPreviewSettlement = (actions, started, exchange, observation = exchange.responseObservation) =>
  actions.push(previewSettlementAction(started, exchange, observation));

const sourceRowsFromPreview = (preview, idLabel) => {
  const columns = preview?.columns ?? [];
  const column = label => columns.find(item => item.label === label)?.column;
  const id = column(idLabel);
  const families = column('Name Family');
  const gender = column('Gender');
  assert(id && families && gender, 'Authored Patient source preview must expose ID, family-list, and gender columns.');
  return (preview?.rows ?? []).map(row => {
    const familyValues = row[families];
    assert(Array.isArray(familyValues), 'Patient.name[].family ALL projection must preserve the exact source list.');
    return {
      id: row[id],
      families: familyValues,
      gender: row[gender] ?? null,
    };
  }).sort((left, right) => left.id.localeCompare(right.id));
};

const sourceRowsFromRendered = (rendered, idLabel) => {
  const index = label => findRenderedBuilderHeaderIndex(rendered.headers, label);
  const cell = (row, label) => row[index(label)];
  const familyValues = renderedCell => {
    const title = renderedCell?.title;
    let value;
    try { value = JSON.parse(title); } catch {
      throw new Error('Rendered Patient.name[].family cell must expose its exact JSON-list title.');
    }
    assert(Array.isArray(value), 'Rendered Patient.name[].family cell title must be a JSON array.');
    return value;
  };
  return rendered.rawRows.map(row => ({
    id: cell(row, idLabel).text,
    families: familyValues(cell(row, 'Name Family')),
    gender: cell(row, 'Gender').text === '—' ? null : cell(row, 'Gender').text,
  })).sort((left, right) => left.id.localeCompare(right.id));
};

const renderedSourceRowExpectations = (sourceRows, idLabel) => sourceRows.map(({ id, families, gender }) => ({
  [idLabel]: id,
  'Name Family': { titleJsonArray: families },
  Gender: gender ?? '—',
}));

const legacyBasicRowsFromFixture = sourceRows => sourceRows.map(({ id, families, gender }) => {
  assert(families.length <= 2, 'The five-column Basic projection can represent at most two Patient family values.');
  return {
    id,
    family0: families[0] ?? null,
    family1: families[1] ?? null,
    familyCount: families.length,
    gender: gender ?? null,
  };
}).sort((left, right) => left.id.localeCompare(right.id));

const legacyBasicRowsFromPreview = preview => {
  const columns = preview?.columns ?? [];
  assert.deepEqual(columns.map(column => column.label), legacyBasicSourceLabels,
    'Legacy Basic bootstrap Preview must retain its exact five flattened source columns.');
  const column = label => columns.find(item => item.label === label)?.column;
  const id = column('id');
  const firstFamily = column('name[].family [0]');
  const secondFamily = column('name[].family [1]');
  const familyCount = column('name__count');
  const gender = column('gender');
  assert(id && firstFamily && secondFamily && familyCount && gender,
    'Legacy Basic Preview must expose all five flattened source columns.');
  return (preview?.rows ?? []).map(row => {
    return {
      id: row[id],
      family0: row[firstFamily] ?? null,
      family1: row[secondFamily] ?? null,
      familyCount: Number(row[familyCount]),
      gender: row[gender] ?? null,
    };
  }).sort((left, right) => left.id.localeCompare(right.id));
};

const sourceRowsFromLegacyBasicPreview = preview => legacyBasicRowsFromPreview(preview).map((row) => {
  assert(Number.isInteger(row.familyCount) && row.familyCount >= 0 && row.familyCount <= 2,
    'Legacy indexed family values must match the exact name__count value.');
  return {
    id: row.id,
    families: [row.family0, row.family1].slice(0, row.familyCount),
    gender: row.gender,
  };
});

const legacyBasicRowsFromRendered = rendered => {
  const index = label => findRenderedBuilderHeaderIndex(rendered.headers, label);
  const cell = (row, label) => row[index(label)]?.text ?? '';
  return rendered.rawRows.map(row => {
    const missing = value => value === '' || value === '—' ? null : value;
    return {
      id: cell(row, 'id'),
      family0: missing(cell(row, 'name[].family [0]')),
      family1: missing(cell(row, 'name[].family [1]')),
      familyCount: Number(cell(row, 'name__count')),
      gender: missing(cell(row, 'gender')),
    };
  }).sort((left, right) => left.id.localeCompare(right.id));
};

const sourceRowsFromLegacyBasicRendered = rendered => legacyBasicRowsFromRendered(rendered).map((row) => {
  assert(Number.isInteger(row.familyCount) && row.familyCount >= 0 && row.familyCount <= 2,
    'Rendered indexed family values must match the exact name__count value.');
  return {
    id: row.id,
    families: [row.family0, row.family1].slice(0, row.familyCount),
    gender: row.gender,
  };
});

const legacyBasicRenderedSourceRowExpectations = sourceRows => sourceRows.map(({ id, families, gender }) => ({
  id,
  'name[].family [0]': families[0] ?? '—',
  'name[].family [1]': families[1] ?? '—',
  name__count: String(families.length),
  gender: gender ?? '—',
}));

const readRenderedBuilderRows = async page => page.evaluate(() => {
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
  const headers = [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim());
  const rawRows = rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => ({
    text: cell.innerText.replace(/\s+/g, ' ').trim(),
    title: cell.getAttribute('title') ?? cell.querySelector('[title]')?.getAttribute('title') ?? null,
  })));
  return { headers, rawRows, rows: rawRows.map(row => row.map(cell => cell.text)) };
});

const assertRenderedHeaders = (actual, expected, message) => assert.deepEqual(
  actual.map(normalizeRenderedBuilderHeader),
  expected.map(normalizeRenderedBuilderHeader),
  message,
);

const writeAndReplayGroupJourney = (journeyTrace, report, fixture) => {
  const artifactStem = journeyTrace.name.replaceAll(':', '-');
  const tracePath = process.env.LOOM_JOURNEY_TRACE
    ? resolve(repositoryRoot, process.env.LOOM_JOURNEY_TRACE)
    : join(report.evidenceDirectory, `${artifactStem}-trace.json`);
  const replayReportPath = join(report.evidenceDirectory, `${artifactStem}-tla.json`);
  const tracePayload = {
    schemaVersion: 1,
    ...journeyTraceProvenance(),
    fixture,
    traces: [journeyTrace],
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(tracePayload, null, 2)}\n`);
  const replay = spawnSync(process.execPath, [
    join(repositoryRoot, 'scripts/verify-builder-traces.mjs'),
    '--traces', tracePath,
    '--contract', journeyContractPath,
    '--output', replayReportPath,
    '--quiet',
  ], {
    cwd: repositoryRoot,
    env: process.env,
    encoding: 'utf8',
    maxBuffer: 32 * 1024 * 1024,
  });
  if (replay.error) throw replay.error;
  report.target.builderJourneyTrace = {
    caseKey: journeyTrace.name,
    tracePath,
    replayReportPath,
    actionLabels: journeyTrace.actions.map(action => action.label),
    status: replay.status === 0 ? 'pass' : 'fail',
  };
  if (replay.status !== 0) {
    const output = `${replay.stdout ?? ''}${replay.stderr ?? ''}`.trim();
    throw new Error(`Builder Group trace replay failed for ${journeyTrace.name}: ${output.split('\n').slice(-20).join('\n')}`);
  }
};

export const openFreshPatientTable = async ({ page, report, check, action }, context, purpose, { populateBootstrap = false } = {}) => {
  configureNativePage(page);
  assert.equal(context.custom, false, 'This case requires a fresh owned fixture project.');
  assert.equal(context.seed?.fresh, true, 'This case must use a fresh verification project.');
  assert(context.target.fixtureDir && context.target.fixtureGeneration, 'Expected an isolated fixture generation.');

  const fixtureIDs = readNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter(resource => resource?.resourceType === 'Patient')
    .map(resource => resource.id)
    .sort();
  assert.deepEqual(fixtureIDs, expectedPatientIDs, 'fresh Patient.ndjson must provide the exact two-record identity oracle');
  const fixtureOracle = {
    source: 'fresh project Patient.ndjson',
    path: join(context.target.fixtureDir, 'Patient.ndjson'),
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    resourceType: 'Patient',
    patientIDs: fixtureIDs,
    expectedCountRows: fixtureIDs.length,
  };
  report.target.fixtureRawOracle = fixtureOracle;
  report.target.qaIsolation = {
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    fixtureDir: context.target.fixtureDir,
    fixtureOracle,
    bootstrapExplorerId: context.target.bootstrapExplorerId,
    explorer: null,
    workflowMutations: [],
    cleanupMutations: [],
    workflowCleanup: 'native Group removal restores the original Patient row shape; the harness retains the fresh loom_dev_verify project and its Explorers after the case',
  };
  check('correctness', 'basic Patient fixture contains the two independently known records',
    JSON.stringify(fixtureIDs) === JSON.stringify(expectedPatientIDs), fixtureOracle);

  const explorerName = `Verify ${context.runID.slice(-10)} ${purpose}`;
  const bootstrapBuilderPath = `${fixtureProjectExplorerCollectionPath(context.target.fixtureProject)}`
    + `/${encodeURIComponent(context.target.bootstrapExplorerId)}/authoring/v2/builder`;
  const bootstrapResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === new URL(context.target.uiUrl).origin
      && url.pathname === bootstrapBuilderPath && response.request().method() === 'GET';
  }, { timeout: 5000 });
  await page.goto(browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'),
    { waitUntil: 'domcontentloaded' });
  const bootstrapResponse = await bootstrapResponsePromise;
  assert.equal(bootstrapResponse.status(), 200,
    'bootstrap Builder state request must complete before Explorer creation');
  const bootstrapRequestBody = await bootstrapResponse.json();
  assert(bootstrapRequestBody, 'bootstrap Builder state response must be readable');

  if (populateBootstrap) {
    assert.equal(context.seed?.bootstrapWorkspace?.seeded, true,
      'The raw Basic case must use the fixture harness seeded bootstrap workspace.');
    const explorer = context.target.bootstrapExplorerId;
    const apiRoot = `${fixtureProjectExplorerCollectionPath(context.target.fixtureProject)}`
      + `/${encodeURIComponent(explorer)}`;
    const builder = bootstrapRequestBody;
    assert.equal(builder.catalog?.generation, context.target.fixtureGeneration,
      'Bootstrapped Builder must expose the exact isolated fixture generation.');
    const document = builder.workspace?.documents?.find(item => item.rootResourceType === 'Patient');
    assert(document?.output?.id, 'Legacy Basic bootstrap must contain its preseeded Patient table.');
    const sourcePaths = document.columns?.map(column => column.source?.field?.path ?? null);
    assert.deepEqual(sourcePaths, ['id', 'name[].family', 'gender'],
      'Legacy Basic bootstrap must use the existing catalog-seeded Patient fields.');
    const sourceIdColumn = document.columns.find(column => column.source?.field?.path === 'id');
    const sourceIdColumnId = sourceIdColumn?.columnId ?? sourceIdColumn?.id;
    assert(sourceIdColumnId && sourceIdColumn.label,
      'Legacy Basic bootstrap must retain its exact source Patient.id column identity.');
    report.target.explorer = explorer;
    report.target.qaIsolation.explorer = explorer;
    report.target.qaIsolation.workflowMutations.push(
      'fixture harness seeded the existing bootstrap Patient source from id, name[].family, and gender catalog candidates before the native Group workflow',
    );
    report.target.table = {
      outputId: document.output.id,
      rootResourceType: 'Patient',
      rawRowCount: fixtureIDs.length,
      patientIDColumn: {
        id: sourceIdColumnId,
        label: sourceIdColumn.label,
        sourcePath: 'id',
      },
      sourceColumns: document.columns.map(column => ({
        id: column.columnId ?? column.id,
        label: column.label,
        sourcePath: column.source?.field?.path ?? null,
        projectionMode: column.source?.field?.projectionMode ?? null,
      })),
    };
    await waitForBuilderRenderedGrid(page, {
      tableSelector: renderedTableSelector,
      expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })),
    });
    return {
      fixtureIDs,
      fixtureOracle,
      explorerName: null,
      explorer,
      apiRoot,
      builder,
      document,
      outputId: document.output.id,
      sourceIdColumn,
      sourceIdColumnId,
      bootstrapRawBasic: true,
    };
  }

  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible' });
  await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
    timeout: 5000,
    budget: 5000,
  });
  const explorerNameControl = page.locator('#new-explorer-name');
  await action('name Explorer', explorerNameControl, () => explorerNameControl.fill(explorerName), {
    editable: true,
    timeout: 5000,
    budget: 5000,
  });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create blank Explorer', createBlank, () => createBlank.click(), {
    after: () => page.waitForFunction(expectedTitle => {
      const select = document.querySelector('select[aria-label="Explorer"]');
      return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
    }, explorerName, { timeout: 5000 }),
    timeout: 5000,
    budget: 5000,
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const explorer = await explorerControl.inputValue();
  assert(explorer && explorer !== context.target.bootstrapExplorerId,
    'Group entry requires a newly created Explorer');
  report.target.explorer = explorer;
  report.target.qaIsolation.explorer = explorer;
  report.target.qaIsolation.workflowMutations.push('native UI created a blank Explorer in the fresh fixture project');

  const tableName = page.locator('#first-table-name');
  await action('name Patient table', tableName, () => tableName.fill('Patients'), {
    editable: true,
    timeout: 5000,
    budget: 5000,
  });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await action('choose Patient rows', choosePatients, () => choosePatients.click(), {
    after: () => page.waitForFunction(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table && Number(table.getAttribute('aria-rowcount')) > 1
        && !document.body.innerText.includes('Loading your table…');
    }, undefined, { timeout: 5000 }),
    timeout: 5000,
    budget: 5000,
  });
  report.target.qaIsolation.workflowMutations.push('native UI created a Patient root table from the two fixture Patient records');

  const apiRoot = `${fixtureProjectExplorerCollectionPath(context.target.fixtureProject)}`
    + `/${encodeURIComponent(explorer)}`;
  const builderResponse = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
    signal: AbortSignal.timeout(30000),
  });
  const builder = await builderResponse.json();
  if (!builderResponse.ok) throw new Error(`Builder read returned HTTP ${builderResponse.status}.`);
  assert.equal(builder.catalog?.generation, context.target.fixtureGeneration,
    'Builder must expose the exact isolated fixture generation.');
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest,
    'Expected a scoped current Builder snapshot.');
  const document = builder.workspace?.documents?.find(item => item.rootResourceType === 'Patient');
  assert(document?.output?.id, 'Fresh Builder Explorer must contain its native Patient table.');
  const sourceIdColumn = document.columns?.find(column => column.source?.field?.path === 'id');
  const sourceIdColumnId = sourceIdColumn?.columnId ?? sourceIdColumn?.id;
  assert(sourceIdColumnId && sourceIdColumn.label, 'Fresh Patient table must expose its native Patient.id column.');
  const outputId = document.output.id;
  report.target.table = { outputId, rootResourceType: 'Patient', rawRowCount: fixtureIDs.length,
    patientIDColumn: { id: sourceIdColumnId, label: sourceIdColumn.label } };
  return { fixtureIDs, fixtureOracle, explorerName, explorer, apiRoot, builder, document, outputId, sourceIdColumn, sourceIdColumnId };
};

export const groupEntryWorkflow = async (workflow, context) => {
  const { page, report, check, action } = workflow;
  await page.setViewportSize({ width: 1440, height: 1000 });
  const backendCalls = [];
  page.on('response', response => {
    const url = new URL(response.url());
    const authoringPrefix = '/authoring/v2/';
    const prefixIndex = url.pathname.indexOf(authoringPrefix);
    if (prefixIndex < 0) return;
    backendCalls.push({
      method: response.request().method(),
      path: url.pathname.slice(prefixIndex + authoringPrefix.length),
      status: response.status(),
    });
  });
  const rawBasicCase = context.args.caseName === 'group-entry-raw-basic';
  let { fixtureIDs, fixtureOracle, explorer, apiRoot, builder, outputId, sourceIdColumn, sourceIdColumnId } =
    await openFreshPatientTable(workflow, context, 'group entry', { populateBootstrap: rawBasicCase });
  const fixturePatients = readNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter(resource => resource?.resourceType === 'Patient');
  const fixtureIDGenderPairs = fixturePatients
    .map(({ id, gender }) => ({ id, gender: gender ?? null }))
    .sort((left, right) => left.id.localeCompare(right.id));
  const fixtureSourceRows = fixturePatients.map(patient => ({
    id: patient.id,
    families: (patient.name ?? []).map(name => name?.family ?? null),
    gender: patient.gender ?? null,
  })).sort((left, right) => left.id.localeCompare(right.id));
  fixtureOracle = {
    ...fixtureOracle,
    idGenderPairs: fixtureIDGenderPairs,
    sourceRows: fixtureSourceRows,
    nativeSourceShape: rawBasicCase
      ? 'bootstrap-seeded Basic source preview with five flattened columns from Patient.id, Patient.name[].family, and Patient.gender'
      : 'three authored columns: Patient.id, Patient.name[].family ALL, Patient.gender',
    separateLegacyShape: rawBasicCase
      ? 'the authored three-column ALL-family native lifecycle remains a separate registered Group case'
      : 'the five-column flattened Basic raw-table oracle is covered by the separately registered group-entry-raw-basic case, which does not replace this authored list-column evidence',
  };
  report.target.fixtureRawOracle = fixtureOracle;

  const sourcePreviewAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'preview',
    owner: { kind: 'preview', attempt: 'P1' },
  });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator(renderedTableSelector).isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
    expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })) });
  const sourcePreviewStarted = await sourcePreviewAttempt.started;
  const sourcePreviewExchange = await sourcePreviewAttempt.settled;
  assert.equal(sourcePreviewExchange.response?.status, 200,
    'The recaptured source-table Preview must return HTTP 200 before Group entry.');
  const sourcePreviewObservation = await captureBuilderObservation(page);
  assert.equal(sourcePreviewObservation.tablePreviewStatus, 'ready',
    'The exact source Preview must be rendered before Group entry.');
  const sourcePreview = {
    owner: sourcePreviewStarted.owner,
    request: sourcePreviewExchange.request,
    response: sourcePreviewExchange.response,
    settlement: sourcePreviewExchange.settlement,
  };
  const initialObservation = await captureBuilderObservation(page);
  const traceActions = [];
  const journeyTrace = {
    name: `builder-authoring:${context.args.caseName}`,
    model: 'BuilderJourney',
    initialObservation,
    initialPreview: {
      owner: sourcePreview.owner,
      request: sourcePreview.request,
      response: sourcePreview.response,
      settlement: sourcePreview.settlement,
    },
    actions: traceActions,
    unmodeledEvents: [],
    backendCalls,
  };

  let sourceDocument;
  let sourcePreviewLabels;
  let sourceRowsForPreview;
  let sourceRowsForRendered;
  let renderedSourceExpectations;
  let flatSourceRowsFromPreview;
  let flatSourceRowsFromRendered;
  let expectedFlatSourceRows;
  if (rawBasicCase) {
    sourceDocument = builder.workspace.documents.find(item => item.output?.id === outputId);
    assert(sourceDocument, 'Bootstrapped Patient table must retain its original Builder document.');
    sourceIdColumn = sourceDocument.columns?.find(column => column.source?.field?.path === 'id');
    sourceIdColumnId = sourceIdColumn?.columnId ?? sourceIdColumn?.id;
    assert(sourceIdColumnId && sourceIdColumn.label, 'Bootstrapped Patient table must retain its source ID identity.');
    assert.deepEqual(sourceDocument.columns?.map(column => column.source?.field?.path ?? null),
      ['id', 'name[].family', 'gender'],
      'Bootstrap workspace must retain the catalog-seeded Patient source field bindings.');
    const sourcePreviewPayload = sourcePreviewExchange.payload?.preview ?? sourcePreviewExchange.payload;
    sourcePreviewLabels = sourcePreviewPayload?.columns?.map(column => column.label) ?? [];
    assert.deepEqual(sourcePreviewLabels, legacyBasicSourceLabels,
      'The native Group workflow must start from the legacy five-column Basic Patient preview.');
    assert.equal(Number(sourcePreviewPayload.rowCount ?? sourcePreviewPayload.rows?.length ?? 0), fixtureIDs.length,
      'The legacy five-column Basic Patient preview must preserve both fixture rows.');
    sourceRowsForPreview = preview => sourceRowsFromLegacyBasicPreview(preview?.preview ?? preview);
    sourceRowsForRendered = sourceRowsFromLegacyBasicRendered;
    renderedSourceExpectations = legacyBasicRenderedSourceRowExpectations;
    flatSourceRowsFromPreview = preview => legacyBasicRowsFromPreview(preview?.preview ?? preview);
    flatSourceRowsFromRendered = legacyBasicRowsFromRendered;
    expectedFlatSourceRows = legacyBasicRowsFromFixture(fixtureSourceRows);
    assert.deepEqual(flatSourceRowsFromPreview(sourcePreviewPayload), expectedFlatSourceRows,
      'All five flattened source columns, including both indexed values and name__count, must equal Patient.ndjson.');
    assert.deepEqual(sourceRowsForPreview(sourcePreviewPayload), fixtureSourceRows,
      'The five flattened source columns must equal the exact independent Patient.ndjson values.');
    const initialRawRows = await readRenderedBuilderRows(page);
    assertRenderedHeaders(initialRawRows.headers, legacyBasicSourceLabels,
      'Rendered legacy Basic Patient table must preserve all five source columns in order.');
    assert.deepEqual(flatSourceRowsFromRendered(initialRawRows), expectedFlatSourceRows,
      'All five rendered source columns, including both indexed values and name__count, must equal Patient.ndjson.');
    assert.deepEqual(sourceRowsForRendered(initialRawRows), fixtureSourceRows,
      'Rendered five-column Basic rows must equal the exact independent Patient.ndjson values.');
    await waitForBuilderRenderedGrid(page, {
      tableSelector: renderedTableSelector,
      expectedRows: renderedSourceExpectations(fixtureSourceRows),
    });
    report.target.table = {
      outputId,
      rootResourceType: 'Patient',
      rawRowCount: fixtureIDs.length,
      patientIDColumn: { id: sourceIdColumnId, label: sourceIdColumn.label, sourcePath: 'id' },
      sourceColumns: sourceDocument.columns.map(column => ({
        id: column.columnId ?? column.id,
        label: column.label,
        sourcePath: column.source?.field?.path ?? null,
        projectionMode: column.source?.field?.projectionMode ?? null,
      })),
      previewColumns: sourcePreviewLabels,
      sourceRows: fixtureSourceRows,
    };
    report.target.qaIsolation.workflowMutations.push(
      'native Group lifecycle reused the fixture harness bootstrap Patient table with its original five-column Basic preview',
    );
    report.target.legacySourceShape = {
      status: 'proved-by-this-native-case',
      source: 'fixture bootstrap default Patient source; the five-column Basic Group oracle migrated from the former Basic branch of scripts/verify-ui/journey-specs/edit-remove-reload.spec.mjs',
      columns: sourcePreviewLabels,
      rows: fixtureSourceRows,
    };
    check('correctness', 'legacy Basic bootstrap renders the exact five-column Patient source values', true, {
      columns: sourcePreviewLabels,
      sourcePaths: sourceDocument.columns.map(column => column.source?.field?.path ?? null),
      rows: fixtureSourceRows,
      flattenedRows: expectedFlatSourceRows,
      rawSourceColumnIdentity: report.target.table.sourceColumns,
    });
  } else {
    await addFieldWithCommandTrace(page, 'Select Patient.name[].family', traceActions, addField);
    const sourceFieldsAdded = await addFieldWithCommandTrace(page, 'Select Patient.gender', traceActions, addField);
    builder = await (async () => {
      const response = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
        signal: AbortSignal.timeout(30000),
      });
      const value = await response.json();
      if (!response.ok) throw new Error(`Builder read after raw source-field setup returned HTTP ${response.status}.`);
      return value;
    })();
    sourceDocument = builder.workspace.documents.find(item => item.output?.id === outputId);
    assert(sourceDocument, 'Patient root table must survive raw source-field setup.');
    sourceIdColumn = sourceDocument.columns?.find(column => column.source?.field?.path === 'id');
    sourceIdColumnId = sourceIdColumn?.columnId ?? sourceIdColumn?.id;
    assert(sourceIdColumnId && sourceIdColumn.label, 'Patient root must retain its source id column identity.');
    sourcePreviewLabels = [sourceIdColumn.label, 'Name Family', 'Gender'];
    assert.deepEqual(sourceDocument.columns.map(column => column.label), sourcePreviewLabels,
      'Fresh Patient root must retain the exact three native authored source columns.');
    const authoredSourcePaths = sourceDocument.columns.map(column => column.source?.field?.path ?? null);
    assert.deepEqual(authoredSourcePaths, ['id', 'name[].family', 'gender'],
      'The authored list-value source must retain the exact Patient field bindings.');
    const familyColumn = sourceDocument.columns.find(column => column.source?.field?.path === 'name[].family');
    assert.equal(familyColumn?.source?.field?.projectionMode, 'ALL',
      'The native family source column must preserve the complete Patient.name[].family list.');
    const sourceColumnIDs = sourceDocument.columns.map(column => column.columnId ?? column.id);
    assert.equal(new Set(sourceColumnIDs).size, sourcePreviewLabels.length,
      'Each native authored Patient source column must retain a distinct Builder identity.');
    assert.deepEqual((sourceFieldsAdded.columns ?? []).map(column => column.label), sourcePreviewLabels,
      'Adding the two raw fields must produce the exact three-column authored source table.');
    assert.equal(Number(sourceFieldsAdded.rowCount ?? sourceFieldsAdded.rows?.length ?? 0), fixtureIDs.length,
      'The authored source preview must preserve both raw Patient rows.');
    sourceRowsForPreview = preview => sourceRowsFromPreview(preview?.preview ?? preview, sourceIdColumn.label);
    sourceRowsForRendered = rendered => sourceRowsFromRendered(rendered, sourceIdColumn.label);
    renderedSourceExpectations = rows => renderedSourceRowExpectations(rows, sourceIdColumn.label);
    assert.deepEqual(sourceRowsForPreview(sourceFieldsAdded), fixtureSourceRows,
      'The ID, full family-list, and gender preview must equal Patient.ndjson before Group entry.');
    const initialRawRows = await readRenderedBuilderRows(page);
    assertRenderedHeaders(initialRawRows.headers, sourcePreviewLabels,
      'Rendered Patient table must preserve the exact source header order and names.');
    assert.deepEqual(sourceRowsForRendered(initialRawRows), fixtureSourceRows,
      'Rendered authored source rows must equal the exact independent Patient.ndjson field values before Group entry.');
    await waitForBuilderRenderedGrid(page, {
      tableSelector: renderedTableSelector,
      expectedRows: renderedSourceExpectations(fixtureSourceRows),
    });
    report.target.table = {
      outputId,
      rootResourceType: 'Patient',
      rawRowCount: fixtureIDs.length,
      patientIDColumn: { id: sourceIdColumnId, label: sourceIdColumn.label, sourcePath: 'id' },
      sourceColumns: sourceDocument.columns.map(column => ({
        id: column.columnId ?? column.id,
        label: column.label,
        sourcePath: column.source?.field?.path ?? null,
        projectionMode: column.source?.field?.projectionMode ?? null,
      })),
      previewColumns: sourcePreviewLabels,
      sourceRows: fixtureSourceRows,
    };
    report.target.qaIsolation.workflowMutations.push('native UI added Patient.name[].family with ALL projection and Patient.gender, producing the exact three authored source columns before Group entry');
    report.target.legacySourceShape = {
      status: 'not-covered-by-authored-three-column-case',
      source: 'builder-authoring/group-entry-raw-basic native case',
      columns: legacyBasicSourceLabels,
    };
    check('correctness', 'fresh Patient source table renders exact authored ID, family-list, and gender values', true, {
      columns: sourcePreviewLabels,
      sourcePaths: authoredSourcePaths,
      familyProjectionMode: familyColumn.source.field.projectionMode,
      rows: fixtureSourceRows,
      rawSourceColumnIdentity: report.target.table.sourceColumns,
    });
  }

  const groupProposalPath = `${apiRoot}/authoring/v2/construction-proposals`;
  const groupRequests = [];
  const groupRequestsByRequest = new Map();
  let requestSequence = 0;
  page.on('request', request => {
    let url;
    let body;
    try {
      url = new URL(request.url());
      body = request.postDataJSON();
    } catch {
      return;
    }
    if (url.origin !== new URL(context.target.uiUrl).origin || url.pathname !== groupProposalPath
      || request.method() !== 'POST' || body?.outputId !== outputId) return;
    const sequence = ++requestSequence;
    const entry = {
      requestObjectIdentity: `playwright-request-${sequence}`,
      requestId: request.headers()['x-request-id'] ?? null,
      body,
      startedAt: Date.now(),
    };
    groupRequests.push(entry);
    groupRequestsByRequest.set(request, entry);
  });
  const proposalResponsePromise = page.waitForResponse(response =>
    groupRequestsByRequest.has(response.request()), { timeout: 5000 });
  const captureProposalPreview = async (attemptNumber, trigger) => {
    const attempt = observeAuthoringAttempt(page, {
      method: 'POST',
      path: 'construction-proposals',
      owner: { kind: 'proposal', attempt: attemptNumber },
    });
    await trigger();
    const started = await attempt.started;
    const exchange = await attempt.settled;
    recordPreviewStart(traceActions, started);
    recordPreviewSettlement(traceActions, started, exchange, await captureBuilderObservation(page));
    return { started, exchange };
  };
  const captureApply = async (label, event, attemptNumber, trigger) => {
    const attempt = observeAuthoringAttempt(page, {
      method: 'POST',
      path: 'commands',
      owner: { kind: 'apply', attempt: attemptNumber },
    });
    await trigger();
    const started = await attempt.started;
    const exchange = await attempt.settled;
    assert(exchange.response?.status >= 200 && exchange.response.status < 300,
      `${label} must be captured as an accepted HTTP response.`);
    traceActions.push({
      label,
      event,
      owner: started.owner,
      request: exchange.request,
      response: exchange.response,
      settlement: exchange.settlement,
      observation: await captureBuilderObservation(page),
    });
    return { started, exchange };
  };
  const initialProposalAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });

  const patientTable = page.getByTestId(`construction-table-${outputId}`);
  await action('select Patient table', patientTable, () => patientTable.click(), {
    after: () => page.waitForFunction(id =>
      document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)?.getAttribute('aria-current') === 'page',
    outputId, { timeout: 5000 }),
    timeout: 5000,
    budget: 5000,
  });
  const rowSettings = page.getByTestId('construction-rows-settings-trigger');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-rows-settings-trigger"]');
    return button && !button.disabled;
  }, undefined, { timeout: 5000 });
  await action('open row definition settings', rowSettings, () => rowSettings.click(), {
    after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
    timeout: 5000,
    budget: 5000,
  });
  const groupAction = page.getByTestId('construction-action-group-rows');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-group-rows"]');
    return button && !button.disabled;
  }, undefined, { timeout: 5000 });
  const actionability = {
    count: await groupAction.count(),
    visible: await groupAction.isVisible(),
    enabled: await groupAction.isEnabled(),
    receivesEvents: false,
  };
  if (actionability.count === 1 && actionability.visible && actionability.enabled) {
    try {
      await groupAction.click({ trial: true });
      actionability.receivesEvents = true;
    } catch {
      actionability.receivesEvents = false;
    }
  }
  const groupActionIsActionable = actionability.count === 1 && actionability.visible
    && actionability.enabled && actionability.receivesEvents;
  check('correctness', 'Direct Group action is actionable', groupActionIsActionable, actionability);

  await action('direct Group entry automatically previews empty-key COUNT_ROWS', groupAction,
    () => groupAction.click(), {
      after: async () => {
        await page.waitForFunction(({ panelSelector, previewSelector, expectedCount }) => {
          const panel = document.querySelector(panelSelector);
          const preview = document.querySelector(previewSelector);
          const summary = document.querySelector('select[aria-label="Summary 1"]');
          const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
            .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim().toLowerCase());
          const row = preview?.querySelector('tbody tr[data-testid="construction-proposal-preview-row"]');
          const cells = [...(row?.querySelectorAll('td') ?? [])]
            .map(cell => cell.innerText.replace(/\s+/g, ' ').trim());
          const countIndex = headers.findIndex(header => header === 'row count');
          return Boolean(panel?.dataset.proposalStatus === 'ready'
            && preview?.dataset.previewStatus === 'ready'
            && summary?.value === 'COUNT_ROWS'
            && countIndex >= 0
            && cells.length === headers.length
            && cells[countIndex] === expectedCount
            && preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === 1);
        }, {
          panelSelector: proposalPanel,
          previewSelector: proposalPreview,
          expectedCount: String(fixtureIDs.length),
        }, { timeout: 5000 });
      },
      timeout: 5000,
      budget: 5000,
    });

  const initialProposalStarted = await initialProposalAttempt.started;
  const initialProposalExchange = await initialProposalAttempt.settled;
  recordPreviewStart(traceActions, initialProposalStarted);
  recordPreviewSettlement(traceActions, initialProposalStarted, initialProposalExchange,
    await captureBuilderObservation(page));

  const proposalResponse = await proposalResponsePromise;
  assert.equal(proposalResponse.status(), 200,
    'direct Group construction proposal must return HTTP 200');
  const proposalResponseBody = await proposalResponse.json();
  assert(proposalResponseBody, 'direct Group construction proposal response must be readable');
  const respondedRequest = groupRequestsByRequest.get(proposalResponse.request());
  assert(respondedRequest, 'Group proposal response must match its exact observed Playwright Request.');
  respondedRequest.status = proposalResponse.status();

  const initialView = await page.evaluate(({ panelSelector, previewSelector }) => {
    const preview = document.querySelector(previewSelector);
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim()));
    return {
      proposalStatus: document.querySelector(panelSelector)?.dataset.proposalStatus,
      previewStatus: preview?.dataset.previewStatus,
      summary: document.querySelector('select[aria-label="Summary 1"]')?.value,
      headers,
      rows,
      groupKeys: [...document.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]')]
        .filter(input => input.checked).map(input => input.getAttribute('aria-label')),
    };
  }, { panelSelector: proposalPanel, previewSelector: proposalPreview });
  assert.equal(initialView.summary, 'COUNT_ROWS', 'Initial standard Group must select Count rows before any edit.');
  assert.deepEqual(initialView.groupKeys, [], 'Initial standard Group must keep the row key list empty.');
  const countHeaderIndex = findRenderedBuilderHeaderIndex(initialView.headers, 'row count');
  assert(countHeaderIndex >= 0, 'Initial standard Group Preview must expose the Row count column.');
  assert.equal(initialView.rows.length, 1, 'A whole-table COUNT_ROWS summary must preview one group row.');
  assert.equal(initialView.rows[0]?.[countHeaderIndex], String(fixtureIDs.length),
    'COUNT_ROWS must equal the independent fixture count.');
  check('correctness', 'direct standard Group entry defaults to empty-key COUNT_ROWS without an edit',
    initialView.summary === 'COUNT_ROWS' && initialView.groupKeys.length === 0,
    initialView);

  const request = groupRequests.findLast(entry => {
    const steps = entry.body?.candidateConstruction?.steps;
    const last = steps?.at(-1);
    return last?.operation?.kind === 'GROUP' && last.operation.group?.keys?.length === 0;
  });
  assert(request, 'Opening direct Group must send a native Builder construction proposal before any field edit.');
  const steps = request.body.candidateConstruction.steps;
  const step = steps.at(-1);
  assert(step.id && step.operation.group.constructionId === step.id,
    'The entry-owned GROUP identity must be preserved in its proposal.');
  assert.deepEqual(step.operation.group.aggregates.map(({ operation }) => operation), ['COUNT_ROWS']);
  assert.equal(request.body.outputId, outputId);
  assert.equal(request.body.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(request.body.expectedDraftVersion, builder.draftVersion);
  assert.equal(request.body.expectedDraftDigest, builder.draftDigest);
  report.target.initialGroupProposal = {
    requestObjectIdentity: request.requestObjectIdentity,
    requestId: request.requestId,
    status: request.status,
    outputId: request.body.outputId,
    snapshotTokenMatched: request.body.snapshotToken === builder.catalog.snapshotToken,
    draftVersion: request.body.expectedDraftVersion,
    draftDigestMatched: request.body.expectedDraftDigest === builder.draftDigest,
    stepId: step.id,
    operation: step.operation,
    outputLabels: step.outputs.map(({ name, label }) => ({ name, label })),
  };
  check('correctness', 'native Builder automatically proposes the exact empty-key COUNT_ROWS GROUP',
    step.operation.kind === 'GROUP' && step.operation.group.keys.length === 0
      && step.operation.group.aggregates.length === 1
      && step.operation.group.aggregates[0].operation === 'COUNT_ROWS'
      && request.body.outputId === outputId
      && request.body.snapshotToken === builder.catalog.snapshotToken
      && request.body.expectedDraftVersion === builder.draftVersion
      && request.body.expectedDraftDigest === builder.draftDigest,
    report.target.initialGroupProposal);
  check('correctness', 'automatic Group proposal Preview returns one row with the independently expected count 2',
    initialView.rows.length === 1 && initialView.rows[0]?.[countHeaderIndex] === String(fixtureIDs.length), {
      headers: initialView.headers,
      rows: initialView.rows,
      expectedCount: fixtureIDs.length,
    });
  const groupEditor = page.getByTestId('construction-reshape-group');
  await groupEditor.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(report.evidenceDirectory, 'group-entry-initial-count-rows.png') });
  await page.setViewportSize({ width: 700, height: 1000 });
  await groupEditor.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(report.evidenceDirectory, 'group-entry-initial-count-rows-narrow.png') });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await groupEditor.scrollIntoViewIfNeeded();

  const directGroupCheckNames = [
    'direct standard Group entry defaults to empty-key COUNT_ROWS without an edit',
    'native Builder automatically proposes the exact empty-key COUNT_ROWS GROUP',
    'automatic Group proposal Preview returns one row with the independently expected count 2',
  ];
  report.target.qaIsolation.requiredEntryChecks = directGroupCheckNames;

  const initialGroupApplyAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: 'P1' },
  });
  await action('Apply initial empty-key COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: [{ 'Row count': String(fixtureIDs.length) }] }),
    });
  const initialGroupApplyStarted = await initialGroupApplyAttempt.started;
  const initialGroupApplyExchange = await initialGroupApplyAttempt.settled;
  assert(initialGroupApplyExchange.response?.status >= 200 && initialGroupApplyExchange.response.status < 300,
    'Applying the initial empty-key Group must return an accepted HTTP response.');
  traceActions.push({
    label: 'Group',
    event: 'group',
    owner: initialGroupApplyStarted.owner,
    request: initialGroupApplyExchange.request,
    response: initialGroupApplyExchange.response,
    settlement: initialGroupApplyExchange.settlement,
    observation: await captureBuilderObservation(page),
  });
  report.target.qaIsolation.workflowMutations.push('native UI applied the empty-key COUNT_ROWS Group');

  const rows = await page.evaluate(() => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    return {
      headers: [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
    };
  });
  const countIndex = findRenderedBuilderHeaderIndex(rows.headers, 'row count');
  assert.equal(rows.rows.length, 1, 'applied empty-key Group must retain one group row');
  assert.equal(rows.rows[0]?.[countIndex], String(fixtureIDs.length), 'applied Group must render the exact fixture count');
  check('correctness', 'applied empty-key Group renders the independently expected count 2', true, {
    headers: rows.headers,
    rows: rows.rows,
    expectedCount: fixtureIDs.length,
  });

  const readBuilder = async () => {
    const response = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    if (!response.ok) throw new Error(`Builder read returned HTTP ${response.status}.`);
    return value;
  };
  let savedBuilder = await readBuilder();
  const savedDocument = savedBuilder.workspace.documents.find(item => item.output?.id === outputId);
  const originalGroup = savedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert(originalGroup?.id, 'applied Group must retain its exact saved step identity');
  assert(sourceIdColumnId && sourceIdColumn.label, 'Patient root must expose its source identity as a Group key');

  const openRowsHistory = async () => {
    const history = page.getByTestId('construction-row-operation-history');
    if (!await history.isVisible().catch(() => false)) {
      const settings = page.getByTestId('construction-rows-settings-trigger');
      await action('open saved row operations', settings, () => settings.click(), {
        after: () => history.waitFor({ state: 'visible' }),
      });
    }
  };
  const editSelector = `[data-testid="construction-row-edit-${originalGroup.id}"]`;
  await openRowsHistory();
  await action('open saved Group edit', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  });
  const sourceIdKey = page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true });
  await captureProposalPreview('P1', () => action('preview keyed Patient Group edit', sourceIdKey,
    () => sourceIdKey.check(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
        && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready'),
    }));
  const idOnlyPreview = await page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
    const keys = [...document.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]')]
      .filter(input => input.checked).map(input => input.getAttribute('aria-label'));
    return { headers, rows, keys };
  });
  const idOnlyPreviewIDIndex = findRenderedBuilderHeaderIndex(idOnlyPreview.headers, sourceIdColumn.label);
  const idOnlyPreviewCountIndex = findRenderedBuilderHeaderIndex(idOnlyPreview.headers, 'row count');
  const idOnlyPreviewIDs = idOnlyPreview.rows.map(row => row[idOnlyPreviewIDIndex]).sort();
  assert.deepEqual(idOnlyPreviewIDs, fixtureIDs, 'keyed Group edit Preview must show both literal fixture Patient IDs');
  assert(idOnlyPreview.rows.every(row => row[idOnlyPreviewCountIndex] === '1'), 'each distinct Patient key must count exactly one source row');
  assert.deepEqual(idOnlyPreview.keys, [`Group by ${sourceIdColumn.label}`]);
  check('correctness', 'saved Group edit previews exact Patient IDs as one row per key', true, idOnlyPreview);

  const beforeEdit = savedBuilder;
  await action('Cancel keyed Group edit', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  savedBuilder = await readBuilder();
  assert.deepEqual(savedBuilder.workspace, beforeEdit.workspace, 'Cancel must leave the saved empty-key Group unchanged');
  assert.equal(savedBuilder.draftVersion, beforeEdit.draftVersion, 'Cancel must preserve the exact saved draft version');
  assert.equal(savedBuilder.draftDigest, beforeEdit.draftDigest, 'Cancel must preserve the exact saved draft digest');
  check('persistence', 'Cancel preserves the saved empty-key Group and exact draft', true, {
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
    groupStepId: originalGroup.id,
  });
  journeyTrace.unmodeledEvents.push({
    kind: 'group-edit-cancel',
    classification: 'unsupported',
    reason: 'BuilderJourney does not model the Group-edit Cancel control; the native workflow compares exact saved workspace and draft identity.',
    note: 'Cancelled the ID-only preview before the later ID-and-gender Apply; the exact empty-key Group remained saved.',
    afterActionIndex: traceActions.length - 1,
    afterActionLabel: traceActions.at(-1)?.label ?? null,
    groupStepId: originalGroup.id,
    assertions: ['workspace equality', 'draftVersion equality', 'draftDigest equality'],
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
  });

  const genderColumn = builder.workspace.documents.find(item => item.output?.id === outputId)?.columns
    ?.find(column => column.source?.field?.path === 'gender');
  assert(genderColumn?.columnId || genderColumn?.id, 'Fresh Patient table must expose its raw gender column.');
  const genderColumnId = genderColumn.columnId ?? genderColumn.id;
  const genderLabel = genderColumn.label;
  const genderKey = page.getByRole('checkbox', { name: `Group by ${genderLabel}`, exact: true });
  await openRowsHistory();
  await action('reopen saved Group edit for the ID-and-gender oracle', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  });
  assert.equal(await sourceIdKey.isChecked(), false, 'The prior Cancel must preserve the empty-key Group before the ID-and-gender edit.');
  assert.equal(await genderKey.isChecked(), false, 'The empty-key Group must not acquire a gender key before Apply.');
  const idPreviewAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-proposals', owner: { kind: 'proposal', attempt: 'P1' },
  });
  await sourceIdKey.check();
  const idPreviewStarted = await idPreviewAttempt.started;
  const idAndGenderPreviewAttempt = observeAuthoringAttempt(page, {
    method: 'POST', path: 'construction-proposals', owner: { kind: 'proposal', attempt: 'P2' },
  });
  await genderKey.check();
  const idAndGenderPreviewStarted = await idAndGenderPreviewAttempt.started;
  const [idPreviewExchange, idAndGenderPreviewExchange] = await Promise.all([
    idPreviewAttempt.settled,
    idAndGenderPreviewAttempt.settled,
  ]);
  assert.equal(idPreviewExchange.response, null,
    'The ID-only P1 proposal must be cancelled when the gender key takes ownership.');
  assert.equal(idPreviewExchange.settlement.kind, 'request-failed');
  assert.equal(idPreviewExchange.settlement.errorText, 'net::ERR_ABORTED',
    'The replaced P1 proposal must retain the exact browser abort outcome.');
  assert.equal(idAndGenderPreviewExchange.response?.status, 200,
    'The ID-and-gender P2 proposal must return HTTP 200.');
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
    && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready', undefined, { timeout: 5000 });
  const idAndGenderObservation = await captureBuilderObservation(page);
  const proposalRaceEvents = [
    {
      sequence: idPreviewStarted.sequence,
      action: {
        label: 'Preview', event: 'preview', owner: idPreviewStarted.owner,
        request: idPreviewStarted.request, observation: idPreviewStarted.requestObservation,
      },
    },
    {
      sequence: idPreviewExchange.responseSequence,
      action: previewSettlementAction(
        idPreviewStarted,
        idPreviewExchange,
        idPreviewExchange.responseObservation,
        idAndGenderPreviewStarted,
      ),
    },
    {
      sequence: idAndGenderPreviewStarted.sequence,
      action: {
        label: 'Preview', event: 'preview', owner: idAndGenderPreviewStarted.owner,
        request: idAndGenderPreviewStarted.request, observation: idAndGenderPreviewStarted.requestObservation,
      },
    },
    {
      sequence: idAndGenderPreviewExchange.responseSequence,
      action: previewSettlementAction(idAndGenderPreviewStarted, idAndGenderPreviewExchange, idAndGenderObservation),
    },
  ];
  proposalRaceEvents.sort((left, right) => left.sequence - right.sequence)
    .forEach(({ action: capturedAction }) => traceActions.push(capturedAction));
  const idAndGenderProposal = idAndGenderPreviewExchange.payload?.preview;
  const idAndGenderColumns = idAndGenderProposal?.columns ?? [];
  const idAndGenderRows = idAndGenderProposal?.rows ?? [];
  const idColumn = idAndGenderColumns.find(column => column.label === sourceIdColumn.label)?.column;
  const genderColumnName = idAndGenderColumns.find(column => column.label === genderLabel)?.column;
  const countColumn = idAndGenderColumns.find(column => column.label === 'Row count')?.column;
  assert.equal(idAndGenderProposal?.rowCount, fixtureIDs.length,
    'ID-and-gender proposal must group the exact two raw Patient rows.');
  assert.deepEqual(idAndGenderColumns.map(column => column.label), [sourceIdColumn.label, genderLabel, 'Row count'],
    'ID-and-gender proposal must expose exactly the applied three-column shape.');
  const idAndGenderOracle = idAndGenderRows
    .map(row => ({ id: row[idColumn], gender: row[genderColumnName] ?? null, count: row[countColumn] }))
    .sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(idAndGenderOracle, fixtureIDGenderPairs.map(pair => ({ ...pair, count: 1 })),
    'ID-and-gender proposal values must equal the independently read Patient.ndjson identity/gender pairs.');
  check('correctness', 'saved Group edit by id and gender preview matches exact fixture IDs, gender values, and row counts', true, {
    columns: idAndGenderColumns.map(column => column.label),
    rowCount: idAndGenderProposal.rowCount,
    rows: idAndGenderOracle,
    fixtureOracle: fixtureIDGenderPairs,
  });

  const idAndGenderRenderedPreview = await page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim()));
    return { headers, rows };
  });
  assertRenderedHeaders(idAndGenderRenderedPreview.headers, [sourceIdColumn.label, genderLabel, 'Row count'],
    'Rendered ID-and-gender Preview must preserve the exact ordered three-column shape.');
  assert.equal(idAndGenderRenderedPreview.rows.length, fixtureIDs.length);
  const renderedIDIndex = findRenderedBuilderHeaderIndex(idAndGenderRenderedPreview.headers, sourceIdColumn.label);
  const renderedGenderIndex = findRenderedBuilderHeaderIndex(idAndGenderRenderedPreview.headers, genderLabel);
  const renderedCountIndex = findRenderedBuilderHeaderIndex(idAndGenderRenderedPreview.headers, 'Row count');
  assert.deepEqual(idAndGenderRenderedPreview.rows.map(row => row[renderedIDIndex]).sort(), fixtureIDs);
  assert(idAndGenderRenderedPreview.rows.every(row => row[renderedCountIndex] === '1'));

  const idAndGenderApply = await captureApply('EditGroup', 'edit-group', 'P2', () =>
    action('Apply ID-and-gender Group edit', page.getByTestId('construction-apply-proposal'),
      () => page.getByTestId('construction-apply-proposal').click(), {
        after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
          expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID, 'Row count': '1' })) }),
      }));
  report.target.qaIsolation.workflowMutations.push('native UI applied the Group edit with both Patient ID and gender keys');
  const idAndGenderAppliedRows = await page.evaluate(() => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    return {
      headers: [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
    };
  });
  assertRenderedHeaders(idAndGenderAppliedRows.headers, [sourceIdColumn.label, genderLabel, 'Row count'],
    'Applied ID-and-gender Group must render exactly three columns.');
  const idAndGenderAppliedOracle = idAndGenderAppliedRows.rows
    .map(row => ({ id: row[0], gender: row[1] === '—' ? null : row[1], count: row[2] }))
    .sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(idAndGenderAppliedOracle, fixtureIDGenderPairs.map(pair => ({ ...pair, count: '1' })),
    'Applied ID-and-gender rows must render the exact Patient.ndjson IDs, genders, and counts.');
  check('correctness', 'applied Group edit by id and gender has the exact two-row, three-column result', true, {
    proposalOwner: idAndGenderPreviewStarted.owner,
    applyOwner: idAndGenderApply.started.owner,
    headers: idAndGenderAppliedRows.headers,
    rows: idAndGenderAppliedOracle,
    fixtureOracle: fixtureIDGenderPairs,
  });

  savedBuilder = await readBuilder();
  const idAndGenderGroup = savedBuilder.workspace.documents.find(item => item.output?.id === outputId)
    ?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert.equal(idAndGenderGroup?.id, originalGroup.id, 'ID-and-gender edit must preserve the original Group identity.');
  assert.deepEqual(idAndGenderGroup.operation.group.keys.map(key => key.inputColumnId), [sourceIdColumnId, genderColumnId]);
  await openRowsHistory();
  await action('open ID-and-gender Group edit to restore ID-only output', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${genderLabel}`, exact: true }).waitFor({ state: 'visible' }),
  });
  const savedGenderKey = page.getByRole('checkbox', { name: `Group by ${genderLabel}`, exact: true });
  assert.equal(await sourceIdKey.isChecked(), true);
  assert.equal(await savedGenderKey.isChecked(), true);
  await captureProposalPreview('P1', () => action('preview saved Group returning to ID-only', savedGenderKey,
    () => savedGenderKey.uncheck(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'),
    }));
  const restoredIDOnlyPreview = await page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
    return { headers, rows };
  });
  const restoredIDOnlyIDIndex = findRenderedBuilderHeaderIndex(restoredIDOnlyPreview.headers, sourceIdColumn.label);
  const restoredIDOnlyCountIndex = findRenderedBuilderHeaderIndex(restoredIDOnlyPreview.headers, 'row count');
  assertRenderedHeaders(restoredIDOnlyPreview.headers, [sourceIdColumn.label, 'Row count'],
    'Rendered ID-only Group Preview must preserve its exact two-column shape.');
  assert.deepEqual(restoredIDOnlyPreview.rows.map(row => row[restoredIDOnlyIDIndex]).sort(), fixtureIDs);
  assert(restoredIDOnlyPreview.rows.every(row => row[restoredIDOnlyCountIndex] === '1'));
  await captureApply('EditGroupAgain', 'edit-group', 'P1', () =>
    action('Apply ID-only Group edit', page.getByTestId('construction-apply-proposal'),
      () => page.getByTestId('construction-apply-proposal').click(), {
        after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
          expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID, 'Row count': '1' })) }),
      }));
  report.target.qaIsolation.workflowMutations.push('native UI applied the keyed ID-only Group edit after the ID-and-gender result');
  check('correctness', 'saved Group edit applies one row per exact Patient ID', true, {
    stepId: originalGroup.id,
    keyColumnId: sourceIdColumnId,
    patientIDs: fixtureIDs,
  });
  savedBuilder = await readBuilder();
  const keyedGroup = savedBuilder.workspace.documents.find(item => item.output?.id === outputId)
    ?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert.equal(keyedGroup?.id, originalGroup.id, 'editing Group must preserve its saved construction identity');
  assert.deepEqual(keyedGroup.operation.group.keys.map(key => key.inputColumnId), [sourceIdColumnId]);

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
    expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID, 'Row count': '1' })) });
  const keyedReload = await page.evaluate(({ tableSelector }) => {
    const table = document.querySelector(tableSelector);
    const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    const headers = [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim());
    const rows = rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
    return { headers, rows };
  }, { tableSelector: renderedTableSelector });
  const keyedReloadIDIndex = findRenderedBuilderHeaderIndex(keyedReload.headers, sourceIdColumn.label);
  const keyedReloadIDs = keyedReloadIDIndex < 0 ? [] : keyedReload.rows.map(row => row[keyedReloadIDIndex]).sort();
  assert.deepEqual(keyedReloadIDs, fixtureIDs, 'keyed Group output must retain exact Patient IDs after reload');
  journeyTrace.unmodeledEvents.push({
    kind: 'reload',
    classification: 'unsupported',
    reason: 'Page reload remounts the workspace; BuilderJourney models the same-mount proposal/apply lifecycle only.',
    note: 'The native ID-only Group result retained both exact Patient IDs through reload.',
  });
  check('persistence', 'keyed Group edit and exact Patient rows survive reload', true, {
    groupStepId: keyedGroup.id,
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
    headers: keyedReload.headers,
    rows: keyedReload.rows,
    patientIDs: keyedReloadIDs,
  });

  const beforeRemoval = savedBuilder;

  const removalSelector = `[data-testid="construction-row-remove-${keyedGroup.id}"]`;
  await openRowsHistory();
  const removalPreviewCapture = await captureProposalPreview('P1', () => action('propose removing keyed Group', page.locator(removalSelector), () => page.locator(removalSelector).click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
      && Boolean(document.querySelector('[data-testid="construction-removal-summary"]'))),
  }));
  const removalSourcePreview = removalPreviewCapture.exchange.payload?.preview;
  assert.deepEqual(removalSourcePreview?.columns?.map(column => column.label), sourcePreviewLabels,
    'Group removal proposal must restore the exact source preview columns for this registered fixture shape.');
  if (rawBasicCase) assert.deepEqual(flatSourceRowsFromPreview(removalSourcePreview), expectedFlatSourceRows,
    'Group removal must restore all five exact flattened source columns, including both indexed values and name__count.');
  assert.deepEqual(sourceRowsForPreview(removalSourcePreview), fixtureSourceRows,
    'Group removal proposal must restore the exact ID, family, count, and gender values from Patient.ndjson.');
  const restoredPreview = await page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rawRows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => ({
        text: cell.innerText.replace(/\s+/g, ' ').trim(),
        title: cell.getAttribute('title') ?? cell.querySelector('[title]')?.getAttribute('title') ?? null,
      })));
    return { headers, rawRows, rows: rawRows.map(row => row.map(cell => cell.text)) };
  });
  assertRenderedHeaders(restoredPreview.headers, sourcePreviewLabels,
    'Rendered removal proposal must contain the exact source columns for this registered fixture shape.');
  if (rawBasicCase) assert.deepEqual(flatSourceRowsFromRendered(restoredPreview), expectedFlatSourceRows,
    'Rendered Group removal must restore all five exact flattened source columns, including both indexed values and name__count.');
  assert.deepEqual(sourceRowsForRendered(restoredPreview), fixtureSourceRows,
    'Rendered removal proposal must restore the exact Patient.ndjson ID, family, count, and gender values.');
  const removalRestoredRows = sourceRowsForRendered(restoredPreview);
  assert.deepEqual(removalRestoredRows.map(row => row.id), fixtureIDs,
    'removing Group must preview the exact two original Patient rows');
  check('correctness', 'Group removal previews both literal source Patient rows', true, {
    headers: restoredPreview.headers,
    rows: restoredPreview.rows,
    sourceRows: removalRestoredRows,
    patientIDs: fixtureIDs,
  });
  await action('Cancel keyed Group removal', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  const afterRemovalCancel = await readBuilder();
  assert.deepEqual(afterRemovalCancel.workspace, savedBuilder.workspace, 'Cancel removal must preserve the saved keyed Group');
  assert.equal(afterRemovalCancel.draftVersion, savedBuilder.draftVersion, 'Cancel removal must preserve the exact saved draft version');
  assert.equal(afterRemovalCancel.draftDigest, savedBuilder.draftDigest, 'Cancel removal must preserve the exact saved draft digest');
  check('persistence', 'Cancel preserves the saved keyed Group and exact draft', true, {
    draftVersion: afterRemovalCancel.draftVersion,
    draftDigest: afterRemovalCancel.draftDigest,
    groupStepId: keyedGroup.id,
  });
  journeyTrace.unmodeledEvents.push({
    kind: 'group-removal-cancel',
    classification: 'unsupported',
    reason: 'BuilderJourney does not model the Group-removal Cancel control; the native workflow compares exact saved workspace and draft identity.',
    note: 'Cancelled the first keyed-Group removal proposal; the exact ID-only Group remained saved before reapplying removal.',
    afterActionIndex: traceActions.length - 1,
    afterActionLabel: traceActions.at(-1)?.label ?? null,
    groupStepId: keyedGroup.id,
    assertions: ['workspace equality', 'draftVersion equality', 'draftDigest equality'],
    draftVersion: afterRemovalCancel.draftVersion,
    draftDigest: afterRemovalCancel.draftDigest,
  });

  await openRowsHistory();
  await captureProposalPreview('P1', () => action('reopen keyed Group removal', page.locator(removalSelector), () => page.locator(removalSelector).click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-removal-summary"]') !== null),
  }));
  await captureApply('RemoveGroup', 'remove-group', 'P1', () =>
    action('Apply keyed Group removal', page.getByTestId('construction-apply-proposal'),
      () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })) }),
      }));
  report.target.qaIsolation.workflowMutations.push('native UI removed the keyed Group and restored source Patient rows');
  report.target.qaIsolation.cleanupMutations.push('native UI removed the case-owned Group step to restore the original Patient row shape');
  const removedBuilder = await readBuilder();
  const removedDocument = removedBuilder.workspace.documents.find(item => item.output?.id === outputId);
  assert.deepEqual(removedDocument?.construction?.steps ?? [], [], 'removing the sole Group must restore the source construction');
  const removedSourceColumnIdentity = removedDocument?.columns?.map(column => ({
    id: column.columnId ?? column.id,
    label: column.label,
    sourcePath: column.source?.field?.path ?? null,
    projectionMode: column.source?.field?.projectionMode ?? null,
  }));
  assert.deepEqual(removedSourceColumnIdentity, report.target.table.sourceColumns,
    'Removed Builder document must retain each original source column identity, label, and field binding.');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  await waitForBuilderRenderedGrid(page, {
    tableSelector: renderedTableSelector,
    expectedRows: renderedSourceExpectations(fixtureSourceRows),
  });
  const restoredRows = await readRenderedBuilderRows(page);
  assertRenderedHeaders(restoredRows.headers, sourcePreviewLabels,
    'Reload after Group removal must render the exact source columns for this registered fixture shape.');
  if (rawBasicCase) assert.deepEqual(flatSourceRowsFromRendered(restoredRows), expectedFlatSourceRows,
    'Reload after Group removal must restore both indexed values and the exact name__count values.');
  assert.deepEqual(sourceRowsForRendered(restoredRows), fixtureSourceRows,
    'Reload after Group removal must restore exact Patient.ndjson ID, family, count, and gender values.');
  const restoredBuilder = await readBuilder();
  const restoredDocument = restoredBuilder.workspace.documents.find(item => item.output?.id === outputId);
  assert.deepEqual(restoredDocument?.columns?.map(column => ({
    id: column.columnId ?? column.id,
    label: column.label,
    sourcePath: column.source?.field?.path ?? null,
    projectionMode: column.source?.field?.projectionMode ?? null,
  })), report.target.table.sourceColumns,
  'Reload after Group removal must restore each original source column binding and identity.');
  const restoredSourceRows = sourceRowsForRendered(restoredRows);
  const restoredIDs = restoredSourceRows.map(row => row.id);
  assert.deepEqual(restoredIDs, fixtureIDs, 'removing Group and reloading must restore both exact source Patient IDs');
  journeyTrace.unmodeledEvents.push({
    kind: 'reload',
    classification: 'unsupported',
    reason: 'Page reload remounts the workspace; BuilderJourney models the same-mount proposal/apply lifecycle only.',
    note: 'The native Group removal restored both exact Patient source IDs through reload.',
  });
  check('persistence', 'removing keyed Group restores the exact source Patient rows after reload', true, {
    draftVersion: removedBuilder.draftVersion,
    draftDigest: removedBuilder.draftDigest,
    headers: restoredRows.headers,
    rows: restoredRows.rows,
    sourceRows: restoredSourceRows,
    patientIDs: restoredIDs,
  });
  journeyTrace.outcome = {
    kind: 'table-preview',
    status: 'ready',
    rowCount: fixtureIDs.length,
    columns: sourcePreviewLabels,
    restoredRowCount: fixtureIDs.length,
    restoredColumns: restoredRows.headers,
  };
  const minimumBackendCalls = Number(process.env.LOOM_JOURNEY_MIN_BACKEND_CALLS ?? 10);
  assert(backendCalls.length >= minimumBackendCalls,
    `Group lifecycle must capture at least ${minimumBackendCalls} actual authoring backend responses; found ${backendCalls.length}.`);
  writeAndReplayGroupJourney(journeyTrace, report, {
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    explorer,
    patientIDs: fixtureIDs,
    idGenderPairs: fixtureIDGenderPairs,
    sourceRows: fixtureSourceRows,
  });
  report.target.qaIsolation.requiredLifecycleChecks = [
    'applied empty-key Group renders the independently expected count 2',
    'saved Group edit previews exact Patient IDs as one row per key',
    'Cancel preserves the saved empty-key Group and exact draft',
    'saved Group edit by id and gender preview matches exact fixture IDs, gender values, and row counts',
    'applied Group edit by id and gender has the exact two-row, three-column result',
    'saved Group edit applies one row per exact Patient ID',
    'keyed Group edit and exact Patient rows survive reload',
    'Group removal previews both literal source Patient rows',
    'Cancel preserves the saved keyed Group and exact draft',
    'removing keyed Group restores the exact source Patient rows after reload',
  ];
};
