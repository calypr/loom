import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserURL } from './builder-url.mjs';
import { writeAndReplayBuilderJourneyTrace } from '../helpers/builder-journey-trace.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import {
  findRenderedBuilderHeaderIndex,
  normalizeRenderedBuilderHeader,
  waitForBuilderRenderedGrid,
} from '../helpers/builder-rendered-grid.mjs';
import { bindFixtureOracleToQaIsolation, fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import {
  addFieldWithCommandTrace,
  captureAuthoringExchange,
  captureBuilderObservation,
  combineAuthoringObservation,
  observeAuthoringAttempt,
} from '../helpers/fast-fixtures.mjs';
import { addField } from '../helpers/cda-journey-fields.mjs';

const expectedPatientIDs = ['dev-patient-001', 'dev-patient-002'];
const legacyBasicSourceLabels = ['id', 'name[].family [0]', 'name[].family [1]', 'name__count', 'gender'];
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const proposalPanel = '[data-testid="construction-proposal-panel"]';
const proposalPreview = '[data-testid="construction-proposal-preview"]';
const renderedTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const groupProposalDelayMs = 300; // useConstructionLifecycle.ts delays candidate proposals by 300 ms.

const readNDJSON = path => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map(line => JSON.parse(line));

const recordPreviewStart = (actions, started, groupLifecycle) => actions.push({
  label: 'Preview',
  event: 'preview',
  owner: started.owner,
  request: started.request,
  observation: started.requestObservation,
  groupLifecycle,
});

const previewSettlementAction = (started, exchange, observation = exchange.responseObservation, replacementStarted = null, groupLifecycle) => {
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
    groupLifecycle,
  };
};

const recordPreviewSettlement = (actions, started, exchange, observation = exchange.responseObservation, groupLifecycle) =>
  actions.push(previewSettlementAction(started, exchange, observation, null, groupLifecycle));

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
    bootstrapExplorerId: context.target.bootstrapExplorerId,
    explorer: null,
    workflowMutations: [],
    cleanupMutations: [],
    workflowCleanup: 'native Group removal restores the original Patient row shape; the harness retains the fresh loom_dev_verify project and its Explorers after the case',
  };
  bindFixtureOracleToQaIsolation(report, fixtureOracle);
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
  let mountEpoch = 0;
  let mountTimeOrigin = await page.evaluate(() => performance.timeOrigin);
  const mountProvenance = () => ({ mountEpoch, documentTimeOrigin: mountTimeOrigin });
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
  const capturedGroupProposalRequests = new Set();
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
  const readBuilder = async () => {
    const response = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    if (!response.ok) throw new Error(`Builder read returned HTTP ${response.status}.`);
    return value;
  };
  const groupStepFromBuilder = value => value.workspace.documents.find(item => item.output?.id === outputId)
    ?.construction?.steps?.find(step => step.operation?.kind === 'GROUP') ?? null;
  const groupRecordFromStep = step => {
    if (!step) return null;
    const group = step.operation?.group;
    const aggregates = group?.aggregates ?? [];
    assert.equal(aggregates.length, 1, 'The bounded Group lifecycle must retain its single observed aggregate.');
    return {
      stepId: step.id,
      keys: (group.keys ?? []).map(key => key.inputColumnId),
      aggregate: aggregates[0]?.operation ?? null,
    };
  };
  const savedGroupEvidence = value => ({
    outputId,
    group: groupRecordFromStep(groupStepFromBuilder(value)),
    workspace: structuredClone(value.workspace),
    draftVersion: value.draftVersion,
    draftDigest: value.draftDigest,
  });
  const clickedRowTargetFromEditControl = async locator => {
    const renderedTarget = await locator.evaluate(button => {
      const testId = button.getAttribute('data-testid');
      const editPrefix = 'construction-row-edit-';
      return {
        testId,
        stepId: testId?.startsWith(editPrefix) ? testId.slice(editPrefix.length) : null,
      };
    });
    assert(renderedTarget.testId, 'The rendered saved-step Edit control must expose its actual target identity.');
    return { evidence: 'clicked-row-action-provenance', ...renderedTarget };
  };
  const mountedGroupFromEditor = async (value, expectedRowActionStepId, confirmedMountedStepId, clickedRowTarget) => {
    const mounted = await page.evaluate(() => {
      const editor = document.querySelector('[data-testid="construction-reshape-group"]');
      if (!editor) return null;
      const operationEditor = document.querySelector('[data-testid="construction-operation-editor"]');
      const selectedKeyLabels = [...editor.querySelectorAll('input[aria-label^="Group by"]:checked')]
        .map(input => input.getAttribute('aria-label'));
      return {
        editorOutputId: operationEditor?.getAttribute('data-output-id') ?? null,
        selectedKeyLabels,
        aggregate: document.querySelector('select[aria-label="Summary 1"]')?.value ?? null,
      };
    });
    assert(mounted, 'The reopened saved Group editor must expose its real mounted controls.');
    assert(clickedRowTarget?.testId, 'The clicked saved-step Edit control must retain its rendered target identity.');
    assert.equal(clickedRowTarget.stepId, expectedRowActionStepId,
      'The clicked saved-step Edit control must target the expected persisted Group identity.');
    assert.equal(mounted.editorOutputId, outputId,
      'The mounted operation editor must target the selected Builder output.');
    if (confirmedMountedStepId !== null) {
      assert.equal(confirmedMountedStepId, expectedRowActionStepId,
        'The actual reloaded editor proposal must identify the clicked saved Group step.');
    }
    const documentColumns = value.workspace.documents.find(item => item.output?.id === outputId)?.columns ?? [];
    const keys = mounted.selectedKeyLabels.map(label => {
      const columnLabel = label?.replace(/^Group by /, '');
      const column = documentColumns.find(item => item.label === columnLabel);
      assert(column, `Mounted Group key ${String(label)} must map to a saved Builder column.`);
      return column.columnId ?? column.id;
    });
    return {
      group: {
        stepId: clickedRowTarget.stepId,
        keys,
        aggregate: mounted.aggregate,
        selectedKeyLabels: mounted.selectedKeyLabels,
      },
      clickedRowTarget,
      editorOutputId: mounted.editorOutputId,
    };
  };
  const candidateIntentFor = (started, kind, base) => {
    const captured = groupRequestsByRequest.get(started.rawRequest);
    const rawProposalBody = captured?.body ?? started.rawRequest.postDataJSON();
    assert(rawProposalBody?.candidateConstruction || kind === 'remove',
      'Captured Group proposal must retain its actual candidate construction body.');
    const removeStepIds = rawProposalBody.removeStepIds ?? [];
    const proposedStepId = kind === 'create'
      ? rawProposalBody.changedStepId ?? null
      : null;
    const targetStepId = kind === 'create'
      ? null
      : kind === 'remove' ? removeStepIds[0] ?? null : rawProposalBody.changedStepId ?? null;
    if (kind !== 'create') assert(targetStepId, `Captured ${kind} Group proposal must identify its target step.`);
    const proposedStep = kind === 'remove'
      ? null
      : rawProposalBody.candidateConstruction?.steps?.find(candidate => candidate.id === (proposedStepId ?? targetStepId)
        && candidate.operation?.kind === 'GROUP') ?? null;
    if (kind !== 'remove') assert(proposedStep, 'Captured Group proposal must contain its exact changed Group step.');
    const proposedGroup = proposedStep?.operation?.group;
    const aggregates = proposedGroup?.aggregates ?? [];
    if (proposedStep) assert.equal(aggregates.length, 1, 'The bounded Group proposal must retain its single aggregate.');
    return {
      kind,
      targetStepId,
      base: base ? structuredClone(base) : null,
      proposed: proposedStep ? {
        stepId: proposedStep.id,
        keys: (proposedGroup.keys ?? []).map(key => key.inputColumnId),
        aggregate: aggregates[0]?.operation ?? null,
      } : null,
      rawProposalBody: structuredClone(rawProposalBody),
    };
  };
  const lifecycleForPreview = (started, kind, base) => {
    assert(started.request.captureId, 'The captured proposal must retain its stable request captureId.');
    capturedGroupProposalRequests.add(started.rawRequest);
    const currentMount = mountProvenance();
    return {
      ...currentMount,
      owner: { mountEpoch: currentMount.mountEpoch, captureId: started.request.captureId },
      candidateIntent: candidateIntentFor(started, kind, base),
    };
  };
  const captureProposalPreview = async (attemptNumber, trigger, { kind = 'edit', base = null } = {}) => {
    const attempt = observeAuthoringAttempt(page, {
      method: 'POST',
      path: 'construction-proposals',
      owner: { kind: 'proposal', attempt: attemptNumber },
    });
    await trigger();
    const started = await attempt.started;
    const exchange = await attempt.settled;
    const groupLifecycle = lifecycleForPreview(started, kind, base);
    assert.equal(exchange.request.captureId, groupLifecycle.owner.captureId,
      'Proposal settlement must pair with the exact captured request owner.');
    recordPreviewStart(traceActions, started, groupLifecycle);
    recordPreviewSettlement(traceActions, started, exchange, await captureBuilderObservation(page), groupLifecycle);
    return { started, exchange, candidateIntent: groupLifecycle.candidateIntent, groupLifecycle };
  };
  const captureOptionalProposalOnOpen = async (trigger, { kind = 'edit', base = null } = {}) => {
    const requestCount = groupRequests.length;
    let rawRequest = null;
    let requestObservationPromise = null;
    let resolveObservedRequest;
    const observedRequest = new Promise(resolve => { resolveObservedRequest = resolve; });
    const onRequest = request => {
      const requestUrl = new URL(request.url());
      if (rawRequest || request.method() !== 'POST'
        || requestUrl.pathname.split('/authoring/v2/')[1] !== 'construction-proposals') return;
      let body;
      try { body = request.postDataJSON(); } catch { return; }
      if (body?.outputId !== outputId) return;
      rawRequest = request;
      requestObservationPromise = captureBuilderObservation(page);
      resolveObservedRequest(request);
    };
    page.on('request', onRequest);
    await trigger();
    await page.waitForTimeout(groupProposalDelayMs);
    const readEditorOpenObservation = () => page.evaluate(() => {
      const editor = document.querySelector('[data-testid="construction-reshape-group"]');
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return {
        groupEditorMounted: Boolean(editor),
        proposalPanelPresent: Boolean(panel),
        proposalStatus: panel?.getAttribute('data-proposal-status') ?? null,
        proposalPreviewStatus: panel?.getAttribute('data-proposal-preview-status') ?? null,
      };
    });
    let editorOpenObservation = await readEditorOpenObservation();
    if (!rawRequest && editorOpenObservation.proposalStatus === 'previewing') {
      const proposalSettled = page.waitForFunction(() => {
        const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
        return !panel || panel.getAttribute('data-proposal-status') !== 'previewing';
      }, undefined, { timeout: 5000 }).catch(() => null);
      await Promise.race([observedRequest, proposalSettled]);
      editorOpenObservation = await readEditorOpenObservation();
    }
    page.off('request', onRequest);
    const newRequests = groupRequests.length - requestCount;
    assert(newRequests <= 1, 'Opening one saved Group editor must not emit multiple unbound proposal requests.');
    assert(editorOpenObservation.groupEditorMounted,
      'The saved Group editor must be mounted at the end of the source proposal delay window.');
    if (newRequests === 0) {
      assert.equal(rawRequest, null, 'Optional proposal listener must agree with the workflow request observer.');
      assert.notEqual(editorOpenObservation.proposalStatus, 'previewing',
        'A previewing Group editor at the end of its source delay must have emitted its actual proposal request.');
      return { started: null, exchange: null, candidateIntent: null, groupLifecycle: null, editorOpenObservation };
    }
    assert(rawRequest, 'Opening saved Group editor proposal must be captured from its actual request.');
    const capturedExchange = await captureAuthoringExchange(rawRequest);
    const started = {
      owner: { kind: 'proposal', attempt: 'P1' },
      request: capturedExchange.request,
      requestObservation: await requestObservationPromise,
      rawRequest,
      sequence: 0,
    };
    const exchange = {
      ...capturedExchange,
      settlement: capturedExchange.rawResponse
        ? { kind: 'http-response', captureId: capturedExchange.request.captureId }
        : {
          kind: 'request-failed',
          captureId: capturedExchange.request.captureId,
          ...(rawRequest.failure()?.errorText ? { errorText: rawRequest.failure().errorText } : {}),
        },
      responseObservation: await captureBuilderObservation(page),
      responseSequence: 1,
    };
    const groupLifecycle = lifecycleForPreview(started, kind, base);
    groupLifecycle.editorOpenObservation = editorOpenObservation;
    assert.equal(exchange.request.captureId, groupLifecycle.owner.captureId,
      'Opened-editor proposal settlement must pair with its exact captured request.');
    recordPreviewStart(traceActions, started, groupLifecycle);
    recordPreviewSettlement(traceActions, started, exchange, await captureBuilderObservation(page), groupLifecycle);
    return { started, exchange, candidateIntent: groupLifecycle.candidateIntent, groupLifecycle, editorOpenObservation };
  };
  const captureApply = async (label, event, attemptNumber, trigger, candidateIntent, candidateOwner) => {
    const savedBeforeBuilder = await readBuilder();
    const savedBefore = savedGroupEvidence(savedBeforeBuilder);
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
    const savedAfterBuilder = await readBuilder();
    const savedAfter = savedGroupEvidence(savedAfterBuilder);
    const currentMount = mountProvenance();
    assert(candidateIntent, `${label} must bind the actual candidate intent that its preview owned.`);
    assert(candidateOwner, `${label} must bind the exact Preview owner consumed by its Apply action.`);
    assert.deepEqual(savedBefore.group, candidateIntent.base,
      `${label} must start from the exact saved Group bound by its proposal request.`);
    assert.deepEqual(savedAfter.group, candidateIntent.proposed,
      `${label} must persist the exact Group candidate bound by its proposal request.`);
    traceActions.push({
      label,
      event,
      owner: started.owner,
      request: exchange.request,
      response: exchange.response,
      settlement: exchange.settlement,
      observation: await captureBuilderObservation(page),
      groupLifecycle: {
        ...currentMount,
        candidateIntent,
        candidateOwner,
        savedBefore,
        savedAfter,
      },
    });
    return { started, exchange, savedBeforeBuilder, savedAfterBuilder };
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
  const initialGroupPreviewLifecycle = lifecycleForPreview(initialProposalStarted, 'create', null);
  assert.equal(initialProposalExchange.request.captureId, initialGroupPreviewLifecycle.owner.captureId);
  recordPreviewStart(traceActions, initialProposalStarted, initialGroupPreviewLifecycle);
  recordPreviewSettlement(traceActions, initialProposalStarted, initialProposalExchange,
    await captureBuilderObservation(page), initialGroupPreviewLifecycle);

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

  const initialGroupApply = await captureApply('Group', 'group', 'P1', () =>
    action('Apply initial empty-key COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'),
      () => page.getByTestId('construction-apply-proposal').click(), {
        after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
          expectedRows: [{ 'Row count': String(fixtureIDs.length) }] }),
      }), initialGroupPreviewLifecycle.candidateIntent, initialGroupPreviewLifecycle.owner);
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

  let savedBuilder = initialGroupApply.savedAfterBuilder;
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
  const originalGroupRecord = groupRecordFromStep(originalGroup);
  await openRowsHistory();
  await captureOptionalProposalOnOpen(() => action('open saved Group edit', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  }), { kind: 'edit', base: originalGroupRecord });
  const sourceIdKey = page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true });
  const keyedEditPreviewCapture = await captureProposalPreview('P1', () => action('preview keyed Patient Group edit', sourceIdKey,
    () => sourceIdKey.check(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
        && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready'),
    }), { kind: 'edit', base: originalGroupRecord });
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

  const savedBeforeEditCandidate = savedBuilder;
  const beforeEdit = await readBuilder();
  const editCancelButton = page.getByTestId('construction-cancel-proposal');
  let editCancelControl;
  await action('Cancel keyed Group edit', editCancelButton, async () => {
    const controlTestId = await editCancelButton.getAttribute('data-testid');
    assert.equal(controlTestId, 'construction-cancel-proposal',
      'The edit Cancel must retain the actual clicked proposal-control identity.');
    editCancelControl = { kind: 'proposal-cancel', controlTestId };
    await editCancelButton.click();
  }, {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  const editCancelObservation = await captureBuilderObservation(page);
  savedBuilder = await readBuilder();
  assert.deepEqual(savedGroupEvidence(beforeEdit).group, keyedEditPreviewCapture.candidateIntent.base,
    'Group edit Cancel must bind the saved Group observed before the user action.');
  assert.deepEqual(savedGroupEvidence(savedBuilder).group, savedGroupEvidence(beforeEdit).group,
    'Group edit Cancel must preserve the exact saved Group projection.');
  assert.deepEqual(savedBuilder.workspace, beforeEdit.workspace, 'Cancel must leave the saved empty-key Group unchanged');
  assert.equal(savedBuilder.draftVersion, beforeEdit.draftVersion, 'Cancel must preserve the exact saved draft version');
  assert.equal(savedBuilder.draftDigest, beforeEdit.draftDigest, 'Cancel must preserve the exact saved draft digest');
  assert.deepEqual(savedBuilder.workspace, savedBeforeEditCandidate.workspace,
    'Cancel must preserve the exact workspace from before the edit Preview.');
  assert.equal(savedBuilder.draftVersion, savedBeforeEditCandidate.draftVersion,
    'Cancel must preserve the exact draft version from before the edit Preview.');
  assert.equal(savedBuilder.draftDigest, savedBeforeEditCandidate.draftDigest,
    'Cancel must preserve the exact draft digest from before the edit Preview.');
  traceActions.push({
    label: 'GroupEditCancel',
    event: 'group-edit-cancel',
    observation: editCancelObservation,
    groupLifecycle: {
      ...mountProvenance(),
      exitControl: editCancelControl,
      candidateIntent: keyedEditPreviewCapture.candidateIntent,
      candidateOwner: keyedEditPreviewCapture.groupLifecycle.owner,
      savedBefore: savedGroupEvidence(beforeEdit),
      savedAfter: savedGroupEvidence(savedBuilder),
    },
  });
  check('persistence', 'Cancel preserves the saved empty-key Group and exact draft', true, {
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
    groupStepId: originalGroup.id,
  });
  const genderColumn = builder.workspace.documents.find(item => item.output?.id === outputId)?.columns
    ?.find(column => column.source?.field?.path === 'gender');
  assert(genderColumn?.columnId || genderColumn?.id, 'Fresh Patient table must expose its raw gender column.');
  const genderColumnId = genderColumn.columnId ?? genderColumn.id;
  const genderLabel = genderColumn.label;
  const genderKey = page.getByRole('checkbox', { name: `Group by ${genderLabel}`, exact: true });
  await openRowsHistory();
  await captureOptionalProposalOnOpen(() => action('reopen saved Group edit for the ID-and-gender oracle', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  }), { kind: 'edit', base: groupRecordFromStep(groupStepFromBuilder(savedBuilder)) });
  assert.equal(await sourceIdKey.isChecked(), false, 'The prior Cancel must preserve the empty-key Group before the ID-and-gender edit.');
  assert.equal(await genderKey.isChecked(), false, 'The empty-key Group must not acquire a gender key before Apply.');
  const editBase = groupRecordFromStep(groupStepFromBuilder(savedBuilder));
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
  const idPreviewLifecycle = lifecycleForPreview(idPreviewStarted, 'edit', editBase);
  const idAndGenderPreviewLifecycle = lifecycleForPreview(idAndGenderPreviewStarted, 'edit', editBase);
  assert.equal(idPreviewExchange.request.captureId, idPreviewLifecycle.owner.captureId);
  assert.equal(idAndGenderPreviewExchange.request.captureId, idAndGenderPreviewLifecycle.owner.captureId);
  const proposalRaceEvents = [
    {
      sequence: idPreviewStarted.sequence,
      action: {
        label: 'Preview', event: 'preview', owner: idPreviewStarted.owner,
        request: idPreviewStarted.request, observation: idPreviewStarted.requestObservation,
          groupLifecycle: idPreviewLifecycle,
      },
    },
    {
      sequence: idPreviewExchange.responseSequence,
      action: previewSettlementAction(
        idPreviewStarted,
        idPreviewExchange,
        idPreviewExchange.responseObservation,
        idAndGenderPreviewStarted,
        idPreviewLifecycle,
      ),
    },
    {
      sequence: idAndGenderPreviewStarted.sequence,
      action: {
        label: 'Preview', event: 'preview', owner: idAndGenderPreviewStarted.owner,
        request: idAndGenderPreviewStarted.request, observation: idAndGenderPreviewStarted.requestObservation,
          groupLifecycle: idAndGenderPreviewLifecycle,
      },
    },
    {
      sequence: idAndGenderPreviewExchange.responseSequence,
      action: previewSettlementAction(idAndGenderPreviewStarted, idAndGenderPreviewExchange,
        idAndGenderObservation, null, idAndGenderPreviewLifecycle),
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
      }), idAndGenderPreviewLifecycle.candidateIntent, idAndGenderPreviewLifecycle.owner);
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
  const idAndGenderGroupRecord = groupRecordFromStep(idAndGenderGroup);
  await openRowsHistory();
  await captureOptionalProposalOnOpen(() => action('open ID-and-gender Group edit to restore ID-only output', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${genderLabel}`, exact: true }).waitFor({ state: 'visible' }),
  }), { kind: 'edit', base: idAndGenderGroupRecord });
  const savedGenderKey = page.getByRole('checkbox', { name: `Group by ${genderLabel}`, exact: true });
  assert.equal(await sourceIdKey.isChecked(), true);
  assert.equal(await savedGenderKey.isChecked(), true);
  const restoredIDOnlyPreviewCapture = await captureProposalPreview('P1', () => action('preview saved Group returning to ID-only', savedGenderKey,
    () => savedGenderKey.uncheck(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'),
    }), { kind: 'edit', base: idAndGenderGroupRecord });
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
      }), restoredIDOnlyPreviewCapture.candidateIntent, restoredIDOnlyPreviewCapture.groupLifecycle.owner);
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
  const keyedGroupRecord = groupRecordFromStep(keyedGroup);
  const keyedReloadSavedBeforeBuilder = savedBuilder;

  await page.reload({ waitUntil: 'domcontentloaded' });
  const reloadedTimeOrigin = await page.evaluate(() => performance.timeOrigin);
  assert.notEqual(reloadedTimeOrigin, mountTimeOrigin, 'A real page reload must mount a new browser document.');
  mountEpoch += 1;
  assert(mountEpoch <= 2, 'The bounded Group lifecycle must remain inside its finite mount epochs.');
  mountTimeOrigin = reloadedTimeOrigin;
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  savedBuilder = await readBuilder();
  const keyedReloadGroup = groupStepFromBuilder(savedBuilder);
  assert.equal(keyedReloadGroup?.id, keyedGroup.id, 'Fresh postreload Builder read must retain the exact Group step identity.');
  assert.deepEqual(groupRecordFromStep(keyedReloadGroup), keyedGroupRecord,
    'Fresh postreload Builder read must retain the exact Group keys and aggregate.');
  const keyedReloadTraceAction = {
    label: 'Reload',
    event: 'reload',
    groupLifecycle: {
      ...mountProvenance(),
      savedBefore: savedGroupEvidence(keyedReloadSavedBeforeBuilder),
      savedAfter: savedGroupEvidence(savedBuilder),
      mountedGroup: null,
    },
  };
  traceActions.push(keyedReloadTraceAction);
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
  keyedReloadTraceAction.observation = await captureBuilderObservation(page);
  await openRowsHistory();
  const reloadedEditButton = page.locator(editSelector);
  let clickedReloadTarget;
  const reloadedGroupOpenCapture = await captureOptionalProposalOnOpen(() => action('open reloaded saved Group editor', reloadedEditButton, async () => {
    clickedReloadTarget = await clickedRowTargetFromEditControl(reloadedEditButton);
    assert.equal(clickedReloadTarget.stepId, keyedGroup.id,
      'The rendered reloaded saved-step Edit control must target the persisted Group identity before it is clicked.');
    await reloadedEditButton.click();
  }, {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  }), { kind: 'edit', base: keyedGroupRecord });
  const confirmedMountedStepId = reloadedGroupOpenCapture.candidateIntent?.proposed?.stepId ?? null;
  const mountedReloadState = await mountedGroupFromEditor(savedBuilder, keyedGroup.id, confirmedMountedStepId, clickedReloadTarget);
  const mountedReloadGroup = mountedReloadState.group;
  assert.equal(mountedReloadGroup.stepId, keyedGroup.id,
    'The reloaded mounted Group step identity must follow the exact clicked rendered row target.');
  assert.deepEqual(mountedReloadGroup.keys, [sourceIdColumnId],
    'Reopened Group editor after reload must mount the saved ID-only key.');
  assert.equal(mountedReloadGroup.aggregate, 'COUNT_ROWS',
    'Reopened Group editor after reload must mount COUNT_ROWS.');
  keyedReloadTraceAction.groupLifecycle.mountedGroup = mountedReloadGroup;
  keyedReloadTraceAction.groupLifecycle.clickedRowTarget = mountedReloadState.clickedRowTarget;
  keyedReloadTraceAction.groupLifecycle.mountedEditorOutputId = mountedReloadState.editorOutputId;
  keyedReloadTraceAction.groupLifecycle.editorOpenObservation = reloadedGroupOpenCapture.editorOpenObservation;
  check('persistence', 'reloaded Group editor mounts the exact saved ID-only key and COUNT_ROWS aggregate', true,
    mountedReloadGroup);
  const beforeReloadEditCancel = await readBuilder();
  const reloadedEditHasCandidate = Boolean(reloadedGroupOpenCapture?.candidateIntent);
  const reloadedEditExit = reloadedEditHasCandidate
    ? { kind: 'proposal-cancel', controlTestId: 'construction-cancel-proposal', label: 'Cancel reloaded Group proposal' }
    : { kind: 'editor-close', controlTestId: 'construction-close-operation-editor', label: 'Close unchanged reloaded Group editor' };
  const reloadedEditExitButton = page.getByTestId(reloadedEditExit.controlTestId);
  let reloadedEditExitControl;
  await action(reloadedEditExit.label, reloadedEditExitButton, async () => {
    const controlTestId = await reloadedEditExitButton.getAttribute('data-testid');
    assert.equal(controlTestId, reloadedEditExit.controlTestId,
      'The reloaded Group exit must retain the actual clicked control identity.');
    reloadedEditExitControl = { kind: reloadedEditExit.kind, controlTestId };
    await reloadedEditExitButton.click();
  }, {
    after: () => reloadedEditHasCandidate
      ? page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'))
      : page.getByTestId('construction-reshape-group').waitFor({ state: 'detached' }),
  });
  const reloadedEditExitObservation = await captureBuilderObservation(page);
  const afterReloadEditCancel = await readBuilder();
  if (reloadedGroupOpenCapture?.candidateIntent) {
    assert.deepEqual(savedGroupEvidence(beforeReloadEditCancel).group, reloadedGroupOpenCapture.candidateIntent.base,
      'The reloaded Group exit must bind the actual saved Group candidate base.');
  }
  assert.deepEqual(savedGroupEvidence(afterReloadEditCancel).group, savedGroupEvidence(beforeReloadEditCancel).group,
    'Exiting the reloaded Group editor without Apply must preserve the exact saved Group projection.');
  assert.deepEqual(afterReloadEditCancel.workspace, beforeReloadEditCancel.workspace,
    'Exiting the reloaded Group editor without Apply must preserve the exact saved workspace.');
  assert.equal(afterReloadEditCancel.draftVersion, beforeReloadEditCancel.draftVersion,
    'Exiting the reloaded Group editor without Apply must preserve the exact draft version.');
  assert.equal(afterReloadEditCancel.draftDigest, beforeReloadEditCancel.draftDigest,
    'Exiting the reloaded Group editor without Apply must preserve the exact draft digest.');
  assert.deepEqual(afterReloadEditCancel.workspace, savedBuilder.workspace,
    'Exiting the reloaded Group editor without Apply must preserve the fresh postreload workspace.');
  assert.equal(afterReloadEditCancel.draftVersion, savedBuilder.draftVersion,
    'Exiting the reloaded Group editor without Apply must preserve the fresh postreload draft version.');
  assert.equal(afterReloadEditCancel.draftDigest, savedBuilder.draftDigest,
    'Exiting the reloaded Group editor without Apply must preserve the fresh postreload draft digest.');
  traceActions.push({
    label: 'GroupEditCancel',
    event: 'group-edit-cancel',
    observation: reloadedEditExitObservation,
    groupLifecycle: {
      ...mountProvenance(),
      exitControl: reloadedEditExitControl,
      candidateIntent: reloadedGroupOpenCapture?.candidateIntent ?? null,
      candidateOwner: reloadedGroupOpenCapture?.groupLifecycle?.owner ?? null,
      savedBefore: savedGroupEvidence(beforeReloadEditCancel),
      savedAfter: savedGroupEvidence(afterReloadEditCancel),
    },
  });
  check('persistence', 'Exiting the reloaded Group editor without Apply preserves the exact saved workspace and draft', true, {
    groupStepId: keyedGroup.id,
    draftVersion: afterReloadEditCancel.draftVersion,
    draftDigest: afterReloadEditCancel.draftDigest,
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
  }), { kind: 'remove', base: keyedGroupRecord });
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
  const beforeRemovalCancel = await readBuilder();
  await action('Cancel keyed Group removal', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  const removalCancelObservation = await captureBuilderObservation(page);
  const afterRemovalCancel = await readBuilder();
  assert.deepEqual(savedGroupEvidence(beforeRemovalCancel).group, removalPreviewCapture.candidateIntent.base,
    'Group removal Cancel must bind the saved Group observed before the user action.');
  assert.deepEqual(savedGroupEvidence(afterRemovalCancel).group, savedGroupEvidence(beforeRemovalCancel).group,
    'Group removal Cancel must preserve the exact saved Group projection.');
  assert.deepEqual(afterRemovalCancel.workspace, beforeRemovalCancel.workspace, 'Cancel removal must preserve the saved keyed Group');
  assert.equal(afterRemovalCancel.draftVersion, beforeRemovalCancel.draftVersion, 'Cancel removal must preserve the exact saved draft version');
  assert.equal(afterRemovalCancel.draftDigest, beforeRemovalCancel.draftDigest, 'Cancel removal must preserve the exact saved draft digest');
  assert.deepEqual(afterRemovalCancel.workspace, beforeRemoval.workspace,
    'Cancel removal must preserve the exact workspace from before the removal Preview.');
  assert.equal(afterRemovalCancel.draftVersion, beforeRemoval.draftVersion,
    'Cancel removal must preserve the exact draft version from before the removal Preview.');
  assert.equal(afterRemovalCancel.draftDigest, beforeRemoval.draftDigest,
    'Cancel removal must preserve the exact draft digest from before the removal Preview.');
  traceActions.push({
    label: 'GroupRemovalCancel',
    event: 'group-removal-cancel',
    observation: removalCancelObservation,
    groupLifecycle: {
      ...mountProvenance(),
      candidateIntent: removalPreviewCapture.candidateIntent,
      candidateOwner: removalPreviewCapture.groupLifecycle.owner,
      savedBefore: savedGroupEvidence(beforeRemovalCancel),
      savedAfter: savedGroupEvidence(afterRemovalCancel),
    },
  });
  check('persistence', 'Cancel preserves the saved keyed Group and exact draft', true, {
    draftVersion: afterRemovalCancel.draftVersion,
    draftDigest: afterRemovalCancel.draftDigest,
    groupStepId: keyedGroup.id,
  });
  await openRowsHistory();
  const reappliedRemovalPreviewCapture = await captureProposalPreview('P1', () => action('reopen keyed Group removal', page.locator(removalSelector), () => page.locator(removalSelector).click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-removal-summary"]') !== null),
  }), { kind: 'remove', base: keyedGroupRecord });
  await captureApply('RemoveGroup', 'remove-group', 'P1', () =>
    action('Apply keyed Group removal', page.getByTestId('construction-apply-proposal'),
      () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })) }),
      }), reappliedRemovalPreviewCapture.candidateIntent, reappliedRemovalPreviewCapture.groupLifecycle.owner);
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
  const removedGroupRecord = groupRecordFromStep(groupStepFromBuilder(removedBuilder));
  assert.equal(removedGroupRecord, null, 'Saved Group must be absent before the post-removal reload.');
  await page.reload({ waitUntil: 'domcontentloaded' });
  const removalReloadTimeOrigin = await page.evaluate(() => performance.timeOrigin);
  assert.notEqual(removalReloadTimeOrigin, mountTimeOrigin, 'The post-removal reload must mount a new browser document.');
  mountEpoch += 1;
  assert(mountEpoch <= 2, 'The bounded Group lifecycle must remain inside its finite mount epochs.');
  mountTimeOrigin = removalReloadTimeOrigin;
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  const restoredBuilder = await readBuilder();
  assert.equal(groupStepFromBuilder(restoredBuilder), null,
    'Fresh postreload Builder read must bind the saved Group absence.');
  const removalReloadTraceAction = {
    label: 'Reload',
    event: 'reload',
    groupLifecycle: {
      ...mountProvenance(),
      savedBefore: savedGroupEvidence(removedBuilder),
      savedAfter: savedGroupEvidence(restoredBuilder),
      mountedGroup: null,
    },
  };
  traceActions.push(removalReloadTraceAction);
  await waitForBuilderRenderedGrid(page, {
    tableSelector: renderedTableSelector,
    expectedRows: renderedSourceExpectations(fixtureSourceRows),
  });
  const restoredRows = await readRenderedBuilderRows(page);
  removalReloadTraceAction.observation = await captureBuilderObservation(page);
  removalReloadTraceAction.groupLifecycle.renderedSourceRows = {
    headers: restoredRows.headers,
    rawRows: restoredRows.rawRows,
  };
  assertRenderedHeaders(restoredRows.headers, sourcePreviewLabels,
    'Reload after Group removal must render the exact source columns for this registered fixture shape.');
  if (rawBasicCase) assert.deepEqual(flatSourceRowsFromRendered(restoredRows), expectedFlatSourceRows,
    'Reload after Group removal must restore both indexed values and the exact name__count values.');
  assert.deepEqual(sourceRowsForRendered(restoredRows), fixtureSourceRows,
    'Reload after Group removal must restore exact Patient.ndjson ID, family, count, and gender values.');
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
  assert.equal(groupStepFromBuilder(restoredBuilder), null,
    'Fresh postreload Builder read must continue to show no saved Group.');
  check('persistence', 'removing keyed Group restores the exact source Patient rows after reload', true, {
    draftVersion: restoredBuilder.draftVersion,
    draftDigest: restoredBuilder.draftDigest,
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
  const requiredLifecycleObservationCounts = new Map([
    ['GroupEditCancel', 2],
    ['GroupRemovalCancel', 1],
    ['Reload', 2],
  ]);
  for (const [label, expectedCount] of requiredLifecycleObservationCounts) {
    const observedActions = traceActions.filter(action => action.label === label);
    assert.equal(observedActions.length, expectedCount,
      `Group lifecycle trace must retain exactly ${expectedCount} ${label} actions.`);
    assert(observedActions.every(action => action.observation && typeof action.observation === 'object'),
      `Every ${label} action must carry its actual page observation.`);
  }
  assert.deepEqual(traceActions.filter(action => action.label === 'GroupEditCancel')
    .map(action => action.groupLifecycle?.exitControl), [
    { kind: 'proposal-cancel', controlTestId: 'construction-cancel-proposal' },
    { kind: reloadedEditExit.kind, controlTestId: reloadedEditExit.controlTestId },
  ], 'Group edit exit evidence must retain the actual proposal Cancel or unchanged editor close selected after reload.');
  const minimumBackendCalls = Number(process.env.LOOM_JOURNEY_MIN_BACKEND_CALLS ?? 10);
  assert(backendCalls.length >= minimumBackendCalls,
    `Group lifecycle must capture at least ${minimumBackendCalls} actual authoring backend responses; found ${backendCalls.length}.`);
  await page.waitForTimeout(groupProposalDelayMs);
  const finalProposalUIState = await page.evaluate(() => ({
    groupEditorMounted: Boolean(document.querySelector('[data-testid="construction-reshape-group"]')),
    proposalStatus: document.querySelector('[data-testid="construction-proposal-panel"]')
      ?.getAttribute('data-proposal-status') ?? null,
  }));
  assert.equal(finalProposalUIState.groupEditorMounted, false,
    'The post-removal end-of-trace observation must have no mounted Group editor awaiting a delayed proposal.');
  assert.notEqual(finalProposalUIState.proposalStatus, 'previewing',
    'The post-removal end-of-trace observation must have no Group proposal still previewing.');
  assert.equal(capturedGroupProposalRequests.size, groupRequests.length,
    'Every actual Group construction-proposal request must have a trace Preview action with its request body and captureId.');
  for (const request of groupRequestsByRequest.keys()) {
    assert(capturedGroupProposalRequests.has(request),
      'Every observed Group construction-proposal request must be bound into the model trace.');
  }
  writeAndReplayBuilderJourneyTrace({
    scenarioID: report.scenario,
    caseName: context.args.caseName,
    journeyTrace,
    report,
    fixture: {
      project: context.target.fixtureProject,
      generation: context.target.fixtureGeneration,
      explorer,
      patientIDs: fixtureIDs,
      idGenderPairs: fixtureIDGenderPairs,
      sourceRows: fixtureSourceRows,
    },
    repositoryRoot,
  });
  report.target.qaIsolation.requiredLifecycleChecks = [
    'applied empty-key Group renders the independently expected count 2',
    'saved Group edit previews exact Patient IDs as one row per key',
    'Cancel preserves the saved empty-key Group and exact draft',
    'saved Group edit by id and gender preview matches exact fixture IDs, gender values, and row counts',
    'applied Group edit by id and gender has the exact two-row, three-column result',
    'saved Group edit applies one row per exact Patient ID',
    'keyed Group edit and exact Patient rows survive reload',
    'reloaded Group editor mounts the exact saved ID-only key and COUNT_ROWS aggregate',
    'Exiting the reloaded Group editor without Apply preserves the exact saved workspace and draft',
    'Group removal previews both literal source Patient rows',
    'Cancel preserves the saved keyed Group and exact draft',
    'removing keyed Group restores the exact source Patient rows after reload',
  ];
};
