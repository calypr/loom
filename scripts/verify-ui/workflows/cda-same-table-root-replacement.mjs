import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';

const ACTION_TIMEOUT_MS = 5_000;
const PATIENT_ID = '01c165b1-6cf3-5fad-9ac8-a526d1cd707c';
export const OBSERVATION_DEFAULT_PAGE_IDS = Object.freeze([
  '52f5e622-0181-5feb-936c-e9992ab26616',
  '485e2567-b566-56f3-b5bd-5f025f37cd95',
  '2cad1748-5e1e-5018-b5b4-f87ac1d8aef8',
  '5577788c-1255-5439-85bb-8651f6f597a4',
  '671ad22b-87be-586b-9e35-e1a7082dfb76',
  '202756f9-3d68-5bb4-8aa7-c40353e6714b',
  'f5f60d35-443a-59a7-8957-59c4b630e5bb',
  'c8782672-b8d1-5c05-9060-905b6a4498bc',
  '86f418a4-4b32-500c-86ab-c79f1751ab9d',
  '29141156-8d4c-5ef9-af43-8ebd6ed8d514',
  'f56be6b4-a036-548c-afa5-4aef487f3ad2',
  'e1b4dc5a-de6e-5888-a5d9-3428c771681a',
  '03d0499b-d516-58e4-a134-aaf61a10b415',
  '3ea21633-23e1-599b-91fc-b7666953ea26',
  '5b01aaf9-cbb7-5641-aca0-4506af29e996',
  'bbe89242-e9d2-5927-bbb2-2f3157d76f9f',
  '212acce3-1b4f-5eb5-8edd-e2bb93afc56b',
  '51918d25-30b0-517e-ad06-088f1d889b24',
  '5a6d5e4d-b78c-522d-95da-533452ba53e6',
  'faca12ec-b153-5301-9879-ceb028690bc9',
  'ae62521a-e7c6-5c3a-a815-128ce294006c',
  '30795d3d-83ab-56bd-9aff-cfd3798d7880',
  '1edf8b76-5932-556c-b198-a90064044741',
  '82e38179-2d8e-522a-ab08-7fed3edca027',
  'd178359c-4e24-5bd0-a33f-d4d4a3b71f1d',
]);
const ORACLE_PROVENANCE = Object.freeze({
  artifact: '/private/tmp/cda-unfiltered-observation-page-20261010/query-probe.json',
  artifactSha256: '5911799e031008d7d270d4456da4905e940c431621e340346b47742bc13333c6',
  querySha256: '05a8a763cf44121cc82d6a9003d377944c29cb8be7dbc68b4459537c3e5c39fb',
  bindVarsSha256: 'e45f51083aeda3b17bba7e84d9905e6713bbc6fb690c575381e33819f4e16ffc',
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
  authorization: 'unrestricted',
  ordering: 'root._key ASC',
  pageLimit: 25,
  expectedIndex: 'idx_1876600546502115329',
  scannedIndex: 25,
  scannedFull: 0,
});

const encoded = value => encodeURIComponent(value);
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const apiRoot = (project, explorer) => `/api/v1/projects/${encoded(project)}/explorers/${encoded(explorer)}`;
const browserApiRoot = (project, explorer) =>
  `/api/v1/projects/${encoded(encoded(project))}/explorers/${encoded(explorer)}`;
const headerKey = value => normalize(value).toLowerCase();
const documentForOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Expected exactly one document for output ${outputId}`);
  return matches[0];
};
export function readGridInPage({ kind, outputId }) {
  const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const savedPreview = document.querySelector('[data-testid="construction-preview"]');
  let table;
  let owner;
  if (kind === 'saved') {
    owner = savedPreview;
    table = savedPreview?.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  } else if (kind === 'filter-proposal') {
    owner = savedPreview?.querySelector('[data-testid="construction-proposal-preview"]');
    table = owner?.querySelector('table');
  } else if (kind === 'replacement-proposal') {
    owner = document.querySelector('[role="dialog"][aria-labelledby="root-replacement-title"]');
    table = owner?.querySelector('table[data-testid="construction-proposal-preview"]');
  } else {
    throw new Error(`Unknown CDA replacement grid owner: ${kind}`);
  }
  if (!table) return { kind, ready: false, previewOutputId: savedPreview?.dataset.previewOutputId ?? null,
    headers: [], rows: [], ariaRowCount: null };

  const proposalTable = kind !== 'saved';
  const headers = proposalTable
    ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
    : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
  const rows = proposalTable
    ? [...table.querySelectorAll('tbody tr')]
      .filter(row => row.querySelector('td'))
      .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
    : [...table.querySelectorAll('[role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
  const rowIndexes = proposalTable
    ? rows.map((_, index) => index + 1)
    : [...table.querySelectorAll('[role="row"]')].slice(1).map(row => {
      const label = row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label') ?? '';
      const match = /^Inspect row (\d+) identity$/.exec(label);
      return match ? Number(match[1]) : null;
    });
  const scroll = kind === 'saved'
    ? document.querySelector('[data-testid="preview-table-scroll"]')
    : null;
  const ready = kind === 'saved'
    ? savedPreview?.dataset.previewStatus === 'ready' && savedPreview.dataset.previewOutputId === outputId
    : kind === 'filter-proposal'
      ? owner?.dataset.previewStatus === 'ready' && owner.dataset.previewOutputId === outputId && Boolean(owner.dataset.previewReceiptId)
      : Boolean(table);
  return {
    kind,
    ready,
    previewOutputId: kind === 'filter-proposal' ? owner?.dataset.previewOutputId ?? null : savedPreview?.dataset.previewOutputId ?? null,
    headers,
    rows,
    rowIndexes,
    ariaRowCount: table.getAttribute('aria-rowcount'),
    scroll: scroll ? {
      top: scroll.scrollTop,
      clientHeight: scroll.clientHeight,
      scrollHeight: scroll.scrollHeight,
    } : null,
  };
}

export function replacementTypeOptionsInPage(optionElements) {
  return optionElements.map(option => ({
    value: option.value,
    label: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(),
    disabled: option.disabled,
  }));
}

export const savedPreviewReady = ([expectedCount, expectedOutput, expectedLabel]) => {
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput && table &&
    Number(table.getAttribute('aria-rowcount')) === expectedCount + 1 &&
    [...table.querySelectorAll('[role="columnheader"]')].some(cell =>
      String(cell.textContent ?? '').replace(/\s+/g, ' ').trim() === expectedLabel);
};
export const savedGridWindowChanged = ([previousTop, previousIndexes]) => {
  const scroll = document.querySelector('[data-testid="preview-table-scroll"]');
  const table = scroll?.querySelector('[role="table"]');
  if (!scroll || !table || scroll.scrollTop <= previousTop) return false;
  const indexes = [...table.querySelectorAll('[role="row"]')].slice(1).map(row => {
    const label = row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label') ?? '';
    const match = /^Inspect row (\d+) identity$/.exec(label);
    return match ? Number(match[1]) : null;
  });
  return indexes.some(index => index !== null && !previousIndexes.includes(index));
};

export const savedGridAtTop = () => {
  const scroll = document.querySelector('[data-testid="preview-table-scroll"]');
  const table = scroll?.querySelector('[role="table"]');
  if (scroll?.scrollTop !== 0 || !table) return false;
  return [...table.querySelectorAll('[role="row"]')].slice(1).some(row => {
    const label = row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label') ?? '';
    return /^Inspect row 1 identity$/.test(label);
  });
};

export async function collectSavedGridRows({ page, cda, expectedIDs, idLabel, outputId, timeoutMs = ACTION_TIMEOUT_MS, deadlineAt }) {
  const deadline = deadlineAt ?? Date.now() + timeoutMs;
  const budgetMs = deadlineAt === undefined ? timeoutMs : Math.max(1, deadlineAt - Date.now());
  const remainingMs = () => {
    const remaining = deadline - Date.now();
    assert(remaining > 0, `Collecting virtualized saved rows exceeded its ${budgetMs} ms deadline.`);
    return remaining;
  };
  const scrollLocator = page.getByTestId('preview-table-scroll');
  await scrollLocator.waitFor({ state: 'visible', timeout: remainingMs() });
  let grid = await page.evaluate(readGridInPage, { kind: 'saved', outputId });
  assert.equal(grid.ready, true, 'Saved preview must be settled for the same output.');
  assert.equal(grid.ariaRowCount, String(expectedIDs.length + 1));
  const columnIndex = grid.headers.findIndex(value => headerKey(value) === headerKey(idLabel));
  assert.notEqual(columnIndex, -1, `Rendered table must contain ${idLabel}: ${JSON.stringify(grid.headers)}`);
  const alreadyComplete = grid.rowIndexes.length === expectedIDs.length &&
    grid.rows.length === expectedIDs.length &&
    grid.rowIndexes.every((rowIndex, index) => rowIndex === index + 1);
  if (alreadyComplete) {
    assert.deepEqual(rowIDs(grid, idLabel), expectedIDs,
      `${idLabel} preview must match the independently retained exact ordered source page.`);
    remainingMs();
    return grid;
  }

  await scrollLocator.hover({ timeout: remainingMs() });
  grid = await page.evaluate(readGridInPage, { kind: 'saved', outputId });

  while (grid.scroll?.top > 0) {
    const remaining = remainingMs();
    await page.mouse.wheel(0, -Math.max(80, Math.ceil(grid.scroll.top + grid.scroll.clientHeight)));
    await cda.wait(savedGridAtTop, [], remaining);
    grid = await page.evaluate(readGridInPage, { kind: 'saved', outputId });
  }

  const rowsByIndex = new Map();
  for (let attempt = 0; attempt < expectedIDs.length + 4; attempt += 1) {
    assert.equal(grid.ready, true, 'Saved preview must be settled for the same output.');
    assert.equal(grid.ariaRowCount, String(expectedIDs.length + 1));
    const currentColumnIndex = grid.headers.findIndex(value => headerKey(value) === headerKey(idLabel));
    assert.notEqual(currentColumnIndex, -1, `Rendered table must contain ${idLabel}: ${JSON.stringify(grid.headers)}`);
    assert.equal(grid.rowIndexes.length, grid.rows.length,
      'Every currently rendered saved row must expose its stable visible row position.');
    grid.rowIndexes.forEach((rowIndex, visibleIndex) => {
      assert(Number.isInteger(rowIndex) && rowIndex >= 1 && rowIndex <= expectedIDs.length,
        `Rendered saved row has no valid position: ${rowIndex}`);
      const previous = rowsByIndex.get(rowIndex);
      const row = grid.rows[visibleIndex];
      if (previous) {
        assert.deepEqual(row, previous, `Saved row ${rowIndex} changed while collecting the virtualized grid.`);
      } else {
        rowsByIndex.set(rowIndex, row);
      }
    });
    if (rowsByIndex.size === expectedIDs.length) {
      const complete = {
        ...grid,
        rowIndexes: Array.from({ length: expectedIDs.length }, (_, index) => index + 1),
        rows: Array.from({ length: expectedIDs.length }, (_, index) => rowsByIndex.get(index + 1)),
      };
      assert.deepEqual(rowIDs(complete, idLabel), expectedIDs,
        `${idLabel} preview must match the independently retained exact ordered source page.`);
      remainingMs();
      return complete;
    }
    assert(grid.scroll && grid.scroll.clientHeight > 0 && grid.scroll.scrollHeight > grid.scroll.clientHeight,
      `Saved preview rendered ${rowsByIndex.size}/${expectedIDs.length} rows without a scrollable viewport.`);
    const maxScrollTop = grid.scroll.scrollHeight - grid.scroll.clientHeight;
    assert(grid.scroll.top < maxScrollTop - 1,
      `Saved preview reached the bottom with only ${rowsByIndex.size}/${expectedIDs.length} row positions rendered.`);
    const remaining = remainingMs();
    await page.mouse.wheel(0, Math.max(80, Math.floor(grid.scroll.clientHeight * 0.75)));
    await cda.wait(savedGridWindowChanged, [grid.scroll.top, grid.rowIndexes], remaining);
    grid = await page.evaluate(readGridInPage, { kind: 'saved', outputId });
  }
  assert.fail(`Collected ${rowsByIndex.size}/${expectedIDs.length} stable saved row positions.`);
}

export function rowIDs(grid, label) {
  const columnIndex = grid.headers.findIndex(value => headerKey(value) === headerKey(label));
  assert.notEqual(columnIndex, -1, `Rendered table must contain ${label}: ${JSON.stringify(grid.headers)}`);
  return grid.rows.map(row => row[columnIndex]);
}

export async function cdaSameTableRootReplacementWorkflow({ page, cda }) {
  const project = cda.target.fixtureProject ?? cda.project;
  const generation = cda.target.fixtureGeneration ?? cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  assert.equal(project, ORACLE_PROVENANCE.project, 'The default-page oracle is bound to the loaded CDA project.');
  assert.equal(generation, ORACLE_PROVENANCE.generation, 'The default-page oracle is bound to the loaded CDA generation.');
  assert.equal(cda.report.authorization?.browserNoAuth, true,
    'The native browser must use the same unauthenticated Builder context as the retained oracle.');
  assert.equal(OBSERVATION_DEFAULT_PAGE_IDS.length, ORACLE_PROVENANCE.pageLimit);

  const rawExplorer = `cda-same-table-replace-${randomUUID()}`;
  const explorerTitle = `CDA same-table root replacement ${randomUUID().slice(0, 8)}`;
  const tableTitle = `Patient setup before Observation replacement ${randomUUID().slice(0, 8)}`;
  const explorerCollectionPath = `/api/v1/projects/${encoded(project)}/explorers`;
  const explorerPath = apiRoot(project, rawExplorer);
  const browserExplorerPath = browserApiRoot(project, rawExplorer);
  const authoringPath = `${explorerPath}/authoring/v2`;
  const browserAuthoringPath = `${browserExplorerPath}/authoring/v2`;
  const builderPath = `${authoringPath}/builder`;
  const report = cda.report;
  report.sameTableRootReplacement = {
    oracle: { ...ORACLE_PROVENANCE, observationIDs: [...OBSERVATION_DEFAULT_PAGE_IDS] },
    lifecycle: {},
  };
  report.explorer = rawExplorer;
  report.target.explorer = rawExplorer;
  const apiRequests = report.sameTableRootReplacement.apiRequests = [];
  const checks = [
    'native Patient root table keeps one named output before replacement',
    'Patient population and ID filter are saved before replacement',
    'REPLACE proposal renders the exact unfiltered default Observation page',
    'Cancel preserves Patient population, filter, draft, and rows through reload',
    'Cancel reload-to-exact-Patient-row within five seconds',
    'Apply keeps the same output identity and clears the Patient population and filter',
    'Reload retains the exact default Observation page on the same output',
    'Applied Observation reload-to-exact-rows within five seconds',
  ];
  const check = (dimension, name, passed, evidence = {}) => {
    assert(checks.includes(name), `Unregistered same-table replacement check: ${name}`);
    cda.check(dimension, name, passed, evidence);
  };
  const api = async (path, body) => {
    const headers = { 'X-Request-ID': `cda-same-table-replacement-${randomUUID()}` };
    const response = body === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: 30_000 });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${normalize(text).slice(0, 800)}`); }
    apiRequests.push({ method: body === undefined ? 'GET' : 'POST', path, status: response.status() });
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1200)}`);
    return value;
  };
  const act = (label, locator, perform, after, requiredCheck, options = {}) => cda.action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after } : {}),
    ...(requiredCheck ? { requiredCheck } : {}),
    ...options,
  });
  const readBuilder = () => api(builderPath);
  const capture = cda.captureRequests(explorerPath, {
    browserRequestOrigin: uiOrigin,
    responsePaths: /population-routes|selections|commands|row-change|preview/,
  });
  let outputId;
  let builder;

  const readGrid = async kind => page.evaluate(readGridInPage, { kind, outputId });
  const assertSavedRows = async (expectedIDs, idLabel) => {
    const deadlineAt = Date.now() + ACTION_TIMEOUT_MS;
    const remaining = () => {
      const timeLeft = deadlineAt - Date.now();
      assert(timeLeft > 0, 'Waiting for exact saved rows exceeded the five-second action budget.');
      return timeLeft;
    };
    await cda.wait(savedPreviewReady, [expectedIDs.length, outputId, idLabel], remaining());
    const grid = await collectSavedGridRows({ page, cda, expectedIDs, idLabel, outputId, deadlineAt });
    assert(grid.ready, 'Saved preview must be settled for the same output.');
    assert.equal(grid.ariaRowCount, String(expectedIDs.length + 1));
    assert.deepEqual(rowIDs(grid, idLabel), expectedIDs,
      `${idLabel} preview must match the independently retained exact ordered source page.`);
    return grid;
  };
  const ensureSelected = async () => {
    const card = page.getByTestId(`construction-table-${outputId}`);
    await card.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    if (await card.getAttribute('aria-current') !== 'page') {
      await card.click({ timeout: ACTION_TIMEOUT_MS });
      await cda.wait(([id]) => document.querySelector(`[data-testid="construction-table-${id}"]`)?.getAttribute('aria-current') === 'page',
        [outputId], ACTION_TIMEOUT_MS);
    }
  };
  const closeRowSettingsIfOpen = async () => {
    const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    if (await dialog.count() === 0 || !await dialog.isVisible()) return;
    await dialog.getByRole('button', { name: 'Back to table', exact: true }).click({ timeout: ACTION_TIMEOUT_MS });
    await dialog.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
  };
  const builderURL = () => {
    const url = new URL(`${uiOrigin}/`);
    url.searchParams.set('project', project);
    url.searchParams.set('explorer', rawExplorer);
    url.searchParams.set('mode', 'builder');
    return url.toString();
  };

  try {
    const created = await api(explorerCollectionPath, { name: rawExplorer, title: explorerTitle });
    const createdExplorer = created.explorerId ?? created.id ?? created.explorer?.id;
    assert.equal(createdExplorer, rawExplorer, 'The temporary CDA Explorer must keep its unique requested ID.');
    builder = await readBuilder();
    assert.equal(builder.catalog?.generation, generation);
    assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest && builder.catalog?.resolvedSchemaDigest,
      'The exact authorized catalog snapshot must be available before selection and replacement.');
    report.sameTableRootReplacement.catalogScope = {
      project,
      generation: builder.catalog.generation,
      snapshotToken: builder.catalog.snapshotToken,
      authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
      resolvedSchemaDigest: builder.catalog.resolvedSchemaDigest,
      browserNoAuth: cda.report.authorization.browserNoAuth,
    };
    const patientNodes = (builder.catalog.nodes ?? []).filter(node => node.resourceType === 'Patient' && node.rowRootEligible);
    const observationNodes = (builder.catalog.nodes ?? []).filter(node => node.resourceType === 'Observation' && node.rowRootEligible);
    assert.equal(patientNodes.length, 1, 'The loaded catalog must expose one Patient root choice.');
    assert.equal(observationNodes.length, 1, 'The loaded catalog must expose one Observation root choice.');

    await cda.navigate(builderURL());
    await page.getByRole('heading', { name: 'Build your first table', exact: true })
      .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const tableName = page.locator('#first-table-name');
    await act('Name the Patient table before same-table replacement', tableName,
      locator => locator.fill(tableTitle, { timeout: ACTION_TIMEOUT_MS }),
      async () => assert.equal(await tableName.inputValue(), tableTitle),
      'Patient table name entry action-to-render within five seconds', { editable: true });
    const choosePatientRows = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await act('Create the Patient root with the native row picker', choosePatientRows,
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
        await cda.wait(() => {
          const preview = document.querySelector('[data-testid="construction-preview"]');
          const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
          return preview?.dataset.previewStatus === 'ready' && table &&
            [...table.querySelectorAll('[role="columnheader"]')].some(cell =>
              String(cell.textContent ?? '').replace(/\s+/g, ' ').trim() === 'Patient ID');
        }, [], ACTION_TIMEOUT_MS);
        builder = await readBuilder();
        const documents = (builder.workspace?.documents ?? []).filter(document => document.output?.title === tableTitle);
        assert.equal(documents.length, 1, 'The native Patient row picker must create one named output.');
        outputId = documents[0].output.id;
        assert(outputId);
        const sourceDocument = documents[0];
        assert.equal(sourceDocument.rootResourceType, 'Patient');
        assert.equal(sourceDocument.population, undefined);
        assert.equal(sourceDocument.rows?.kind, 'RECORDS');
        assert.equal(sourceDocument.columns.length, 1);
        assert.equal(sourceDocument.columns[0].source?.kind, 'field');
        assert.equal(sourceDocument.columns[0].source?.field?.path, 'id');
        assert.equal(sourceDocument.columns[0].source?.field?.projectionMode, 'VALUE');
      }, 'native Patient row picker renders its first table within five seconds');
    check('correctness', 'native Patient root table keeps one named output before replacement',
      outputId && documentForOutput(builder, outputId).rootResourceType === 'Patient' &&
        documentForOutput(builder, outputId).output.title === tableTitle,
      { outputId, title: tableTitle, rootResourceType: documentForOutput(builder, outputId).rootResourceType,
        columns: documentForOutput(builder, outputId).columns });
    report.explorer = rawExplorer;
    report.target.explorer = rawExplorer;

    const patientSelection = await api(`${explorerPath}/selections`, {
      snapshotToken: builder.catalog.snapshotToken,
      idempotencyKey: `same-table-replacement-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs: [{
        project, generation, resourceType: 'Patient', id: PATIENT_ID,
      }] } },
    });
    assert.equal(patientSelection.project, project);
    assert.equal(patientSelection.generation, generation);
    assert.equal(patientSelection.resourceType, 'Patient');
    assert.equal(patientSelection.memberCount, 1);
    assert.equal(patientSelection.scopeDigest, builder.catalog.authorizationScopeDigest);
    const selectionRead = await api(`${explorerPath}/selections/${encoded(patientSelection.id)}?limit=100`);
    assert.equal(selectionRead.revision?.id, patientSelection.id);
    assert.equal(selectionRead.revision?.complete, true);
    assert.equal(selectionRead.revision?.project, project);
    assert.equal(selectionRead.revision?.generation, generation);
    assert.equal(selectionRead.revision?.resourceType, 'Patient');
    assert.equal(selectionRead.revision?.scopeDigest, builder.catalog.authorizationScopeDigest);
    assert.deepEqual(selectionRead.members?.map(member => member.ref.id), [PATIENT_ID]);
    assert.deepEqual(selectionRead.members?.map(member => member.ref.project), [project]);

    const routes = await api(`${authoringPath}/population-routes`, {
      snapshotToken: builder.catalog.snapshotToken,
      outputId,
      selectionRevisionId: patientSelection.id,
      limit: 50,
    });
    const directRoutes = (routes.choices ?? []).filter(choice => Array.isArray(choice.route) && choice.route.length === 0);
    assert.equal(directRoutes.length, 1, 'The one-Patient selection must have one direct Patient population route.');
    const sourceRouteChoiceId = directRoutes[0].routeChoiceId;
    assert(sourceRouteChoiceId);
    const selectionURL = new URL(builderURL());
    selectionURL.searchParams.set('selection', patientSelection.id);
    await cda.navigate(selectionURL.toString());
    await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    await ensureSelected();
    const rowSettingsTrigger = page.getByTestId('construction-rows-settings-trigger');
    await rowSettingsTrigger.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    await rowSettingsTrigger.click({ timeout: ACTION_TIMEOUT_MS });
    const rowSettingsDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    await rowSettingsDialog.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const startingCollection = rowSettingsDialog.getByRole('region', { name: 'Starting collection', exact: true });
    await startingCollection.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const attachFrom = capture.requests.length;
    await act('Attach the exact one-Patient collection through the native control',
      startingCollection.getByRole('button', { name: 'Use selected resources', exact: true }),
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
        await cda.wait(([selectionId]) => document.querySelector('section[aria-label="Starting collection"]')
          ?.getAttribute('data-attached-selection-revision-id') === selectionId, [patientSelection.id], ACTION_TIMEOUT_MS);
        await rowSettingsDialog.getByRole('button', { name: 'Back to table', exact: true }).click({ timeout: ACTION_TIMEOUT_MS });
        await rowSettingsDialog.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
        await assertSavedRows([PATIENT_ID], 'Patient ID');
      }, 'native Patient population attaches and renders the exact row within five seconds');
    const populationCommand = await capture.waitForResponse(entry => entry.path === `${browserAuthoringPath}/commands` &&
      entry.method === 'POST' && entry.status === 200 && entry.request?.commands?.some(command =>
        command.type === 'SET_TABLE_POPULATION' && command.outputId === outputId &&
        command.selectionRevisionId === patientSelection.id && command.routeChoiceId === sourceRouteChoiceId),
    { fromIndex: attachFrom, timeoutMs: ACTION_TIMEOUT_MS });
    assert(Number.isFinite(populationCommand.completedAt), 'Native population command must have a completed response.');
    builder = await readBuilder();
    let baselineDocument = documentForOutput(builder, outputId);
    assert.equal(baselineDocument.population?.selectionRevisionId, patientSelection.id);
    assert.deepEqual(baselineDocument.population?.route ?? [], []);
    assert.equal(baselineDocument.rootResourceType, 'Patient');
    await cda.navigate(builderURL());
    await ensureSelected();
    const patientIDColumn = baselineDocument.columns.find(column => column.source?.kind === 'field' &&
      column.source.field?.path === 'id' && column.source.field?.projectionMode === 'VALUE');
    assert(patientIDColumn?.columnId);

    await page.getByTestId('construction-action-keep-rows').click({ timeout: ACTION_TIMEOUT_MS });
    const filterEditor = page.getByTestId('construction-filter-editor');
    await filterEditor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const filterColumn = filterEditor.getByRole('combobox', { name: 'Column', exact: true });
    await filterColumn.selectOption(patientIDColumn.columnId, { timeout: ACTION_TIMEOUT_MS });
    assert.equal(await filterColumn.inputValue(), patientIDColumn.columnId);
    const filterCondition = filterEditor.getByRole('combobox', { name: 'Condition', exact: true });
    await filterCondition.selectOption('EQUALS', { timeout: ACTION_TIMEOUT_MS });
    assert.equal(await filterCondition.inputValue(), 'EQUALS');
    const filterValue = filterEditor.getByLabel('Value', { exact: true });
    await filterValue.fill(PATIENT_ID, { timeout: ACTION_TIMEOUT_MS });
    assert.equal(await filterValue.inputValue(), PATIENT_ID);
    await cda.wait(() => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      return panel?.dataset.proposalStatus === 'ready' &&
        document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready';
    }, [], ACTION_TIMEOUT_MS);
    const filterCandidate = await readGrid('filter-proposal');
    assert.deepEqual(rowIDs(filterCandidate, 'Patient ID'), [PATIENT_ID],
      'Patient ID EQUALS must preview the one exact selected Patient.');
    const filterApply = page.getByTestId('construction-apply-proposal');
    await act('Apply the Patient ID filter to establish the source setup', filterApply,
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
        await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
        await page.getByRole('navigation', { name: 'Table actions', exact: true })
          .getByRole('button', { name: 'Filter rows: Choose which rows appear in the table output.', exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await assertSavedRows([PATIENT_ID], 'Patient ID');
      }, 'Patient ID filter applies and renders the exact member within five seconds');
    builder = await readBuilder();
    baselineDocument = structuredClone(documentForOutput(builder, outputId));
    const baselineFilterSteps = (baselineDocument.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
    assert.equal(baselineFilterSteps.length, 1, 'The source table must retain one authored Patient ID filter.');
    assert.equal(baselineFilterSteps[0].operation.filter.columnId, patientIDColumn.columnId);
    assert.equal(baselineFilterSteps[0].operation.filter.operator, 'EQUALS');
    assert.deepEqual(baselineFilterSteps[0].operation.filter.values, [{ kind: 'STRING', string: PATIENT_ID }]);
    assert.equal(baselineDocument.population?.selectionRevisionId, patientSelection.id);
    assert.equal(baselineDocument.output.id, outputId);
    assert.equal(baselineDocument.output.title, tableTitle);
    const baseline = {
      draftVersion: builder.draftVersion,
      draftDigest: builder.draftDigest,
      document: baselineDocument,
      selectionRevisionId: patientSelection.id,
      filterStepId: baselineFilterSteps[0].id,
    };
    report.sameTableRootReplacement.lifecycle.patientBaseline = baseline;
    check('persistence', 'Patient population and ID filter are saved before replacement',
      baselineDocument.population?.selectionRevisionId === patientSelection.id &&
        baselineFilterSteps.length === 1 && baselineFilterSteps[0].operation.filter.operator === 'EQUALS',
      { outputId, tableTitle, selectionRevisionId: patientSelection.id, filterStepId: baseline.filterStepId,
        exactPatientRows: [PATIENT_ID], draftVersion: baseline.draftVersion, draftDigest: baseline.draftDigest });

    const requestCapture = capture;
    const targetNode = observationNodes[0];
    const rowChangePath = `${browserAuthoringPath}/row-change`;
    const commandPath = `${browserAuthoringPath}/commands`;
    const replacementTypeControl = 'Choose replacement starting type';
    const reviewHeading = 'Replace this table’s current setup?';
    const applyLabel = 'Change starting record type';
    const replacementApply = entry => (entry.request?.commands ?? []).filter(command =>
      command.type === 'APPLY_TABLE_ROOT_REBASE' && command.rowChange?.mode === 'REPLACE');
    let replacementApplyCount = requestCapture.requests.filter(entry => entry.path === commandPath && replacementApply(entry).length > 0).length;

    const openSettingsAndChooseObservation = async () => {
      await ensureSelected();
      await page.getByTestId('construction-rows-settings-trigger').click({ timeout: ACTION_TIMEOUT_MS });
      const rowSettings = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
      await rowSettings.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      const targetType = rowSettings.getByRole('combobox', { name: replacementTypeControl, exact: true });
      await targetType.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      const options = await targetType.locator('option').evaluateAll(replacementTypeOptionsInPage);
      assert(options.some(option => option.value === targetNode.nodeId && !option.disabled && /Observation/.test(option.label)),
        `Loaded Observation must be an explicit replacement choice: ${JSON.stringify(options)}`);
      await targetType.selectOption(targetNode.nodeId, { timeout: ACTION_TIMEOUT_MS });
      assert.equal(await targetType.inputValue(), targetNode.nodeId);
      return targetType;
    };
    const assessObservationReplacement = async (label, requiredCheck) => {
      await openSettingsAndChooseObservation();
      const fromIndex = requestCapture.requests.length;
      const rowSettings = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
      const replaceButton = rowSettings.getByRole('button', { name: 'Replace current setup', exact: true });
      let assessment;
      let proposalGrid;
      let typedPreview;
      let idColumn;
      await act(label, replaceButton, locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
        const entry = await requestCapture.waitForResponse(candidate => candidate.path === rowChangePath &&
          candidate.method === 'POST' && candidate.status === 200 && candidate.request?.mode === 'REPLACE' &&
          candidate.request?.outputId === outputId && candidate.request?.rootNodeId === targetNode.nodeId &&
          !Object.hasOwn(candidate.request ?? {}, 'rootOccurrenceId'),
        { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
        assert(Number.isFinite(entry.completedAt), 'Replacement assessment must have a terminal response.');
        assessment = entry.response;
        assert.equal(assessment.status, 'READY', `Observation replacement must be ready: ${JSON.stringify(assessment).slice(0, 900)}`);
        assert.equal(assessment.mode, 'REPLACE');
        assert.equal(assessment.snapshotToken, builder.catalog.snapshotToken);
        assert.equal(assessment.draftVersion, builder.draftVersion);
        assert.equal(assessment.draftDigest, builder.draftDigest);
        assert(assessment.candidateReceiptId && assessment.proposal);
        const proposal = assessment.proposal;
        assert.equal(proposal.mode, 'REPLACE');
        assert.equal(proposal.outputId, outputId);
        assert.equal(proposal.rootNodeId, targetNode.nodeId);
        assert.equal(proposal.rootOccurrenceId, 'base');
        assert.equal(proposal.catalogSnapshotToken, builder.catalog.snapshotToken);
        assert.equal(proposal.catalogGeneration, generation);
        assert.equal(typeof proposal.catalogDigest, 'string');
        assert(proposal.catalogDigest.length > 0,
          'Replacement proposal must carry its own nonempty catalog digest.');
        assert.deepEqual(proposal.routeRebase, [], 'REPLACE must not request a preservation route.');
        assert(typeof proposal.sourceDocumentDigest === 'string' && proposal.sourceDocumentDigest.length > 0,
          'Replacement proposal must bind the exact saved Patient source document.');

        const dialog = page.getByRole('dialog', { name: reviewHeading, exact: true });
        await dialog.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await dialog.getByRole('heading', { name: reviewHeading, exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        const consequences = dialog.getByRole('region', { name: 'Replacement consequences', exact: true });
        await consequences.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await dialog.getByRole('heading', { name: 'Candidate preview', exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        const previewEntry = await requestCapture.waitForResponse(candidate => candidate.path === `${browserAuthoringPath}/preview` &&
          candidate.method === 'POST' && candidate.status === 200 &&
          candidate.request?.outputId === outputId && candidate.request?.receiptId === assessment.candidateReceiptId,
        { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
        typedPreview = previewEntry.response;
        assert.equal(typedPreview.receiptId, assessment.candidateReceiptId);
        assert.equal(typedPreview.outputId, outputId);
        const idColumns = (typedPreview.columns ?? []).filter(column =>
          column.sourceResourceType === 'Observation' && column.sourcePath === 'id' && column.column === 'id');
        assert.equal(idColumns.length, 1,
          `Candidate must expose one Observation id column with Observation source provenance: ${JSON.stringify(typedPreview.columns)}`);
        assert.equal(typedPreview.columns.length, 1, 'Replacement must seed only the Observation id column.');
        idColumn = idColumns[0];
        assert.equal(typeof idColumn.label, 'string');
        assert(idColumn.label.trim().length > 0, 'The server-seeded Observation id column must have a nonempty label.');
        assert.deepEqual(typedPreview.rows.map(row => row[idColumn.column]), OBSERVATION_DEFAULT_PAGE_IDS,
          'Receipt-bound typed preview must equal the independent unfiltered default Observation page.');
        const previewTable = dialog.locator('table[data-testid="construction-proposal-preview"]');
        await previewTable.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        proposalGrid = await readGrid('replacement-proposal');
        assert.deepEqual(rowIDs(proposalGrid, idColumn.label), OBSERVATION_DEFAULT_PAGE_IDS,
          'Native replacement proposal table must use the server-seeded Observation id label and render the exact unfiltered page.');
        assert.equal(proposalGrid.rows.length, OBSERVATION_DEFAULT_PAGE_IDS.length);
        const reviewText = normalize(await consequences.innerText());
        assert(reviewText.includes('Observation'));
        assert(/column|field/i.test(reviewText), 'The replacement consequences must identify the discarded source fields.');
        assert(/filter/i.test(reviewText), 'The replacement consequences must identify the discarded Patient filter.');
        assert(/population|collection/i.test(reviewText), 'The replacement consequences must identify the Patient population reset.');
      }, requiredCheck);
      return { assessment, proposalGrid, typedPreview, idColumn };
    };

    const firstReview = await assessObservationReplacement(
      'Review Observation replacement without a preservation route',
      'Initial Observation REPLACE preview renders within five seconds');
    const observationIDLabel = firstReview.idColumn.label;
    const baselineCapture = requestCapture.requests.length;
    const firstReplacementDialog = page.getByRole('dialog', { name: reviewHeading, exact: true });
    const cancelButton = firstReplacementDialog.getByRole('button', { name: 'Cancel', exact: true });
    await act('Cancel the first Observation replacement assessment', cancelButton,
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
        await firstReplacementDialog.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
        await closeRowSettingsIfOpen();
        await assertSavedRows([PATIENT_ID], 'Patient ID');
      }, 'Cancel restores the exact Patient row within five seconds');
    replacementApplyCount = requestCapture.requests.slice(baselineCapture)
      .filter(entry => entry.path === commandPath && replacementApply(entry).length > 0).length;
    assert.equal(replacementApplyCount, 0, 'Cancel must not apply a root replacement.');
    builder = await readBuilder();
    assert.equal(builder.draftVersion, baseline.draftVersion);
    assert.equal(builder.draftDigest, baseline.draftDigest);
    assert(isDeepStrictEqual(documentForOutput(builder, outputId), baselineDocument),
      'Cancel must preserve the exact Patient root, filter, selection, columns, and output.');

    const cancelReloadStarted = performance.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    await ensureSelected();
    const afterCancelReloadGrid = await assertSavedRows([PATIENT_ID], 'Patient ID');
    const cancelReloadMs = Math.round(performance.now() - cancelReloadStarted);
    assert(cancelReloadMs <= ACTION_TIMEOUT_MS, `Cancel reload-to-exact-Patient-row took ${cancelReloadMs} ms.`);
    builder = await readBuilder();
    assert.equal(builder.draftDigest, baseline.draftDigest);
    assert(isDeepStrictEqual(documentForOutput(builder, outputId), baselineDocument));
    check('persistence', 'Cancel preserves Patient population, filter, draft, and rows through reload',
      isDeepStrictEqual(documentForOutput(builder, outputId), baselineDocument) && afterCancelReloadGrid.rows.length === 1,
      { outputId, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
        selectionRevisionId: patientSelection.id, filterStepId: baseline.filterStepId, rows: afterCancelReloadGrid.rows,
        reloadDurationMs: cancelReloadMs });
    check('performance', 'Cancel reload-to-exact-Patient-row within five seconds', cancelReloadMs <= ACTION_TIMEOUT_MS,
      { durationMs: cancelReloadMs, budgetMs: ACTION_TIMEOUT_MS });

    builder = await readBuilder();
    const secondReview = await assessObservationReplacement(
      'Reassess the same Observation replacement after Cancel',
      'Reassessed Observation REPLACE preview renders within five seconds');
    assert.equal(secondReview.idColumn.label, observationIDLabel,
      'Reassessment must retain the server-seeded Observation id label.');
    assert.equal(secondReview.assessment.proposal.sourceDocumentDigest, firstReview.assessment.proposal.sourceDocumentDigest,
      'Reassessment after Cancel must remain bound to the unchanged Patient source document.');
    assert.equal(secondReview.assessment.proposal.catalogDigest, firstReview.assessment.proposal.catalogDigest,
      'Reassessment after Cancel must remain bound to the same catalog digest.');
    assert.equal(secondReview.assessment.proposal.catalogGeneration, firstReview.assessment.proposal.catalogGeneration);
    assert.equal(secondReview.assessment.proposal.catalogSnapshotToken, firstReview.assessment.proposal.catalogSnapshotToken);
    assert.equal(secondReview.assessment.snapshotToken, firstReview.assessment.snapshotToken);
    assert.equal(secondReview.assessment.draftVersion, firstReview.assessment.draftVersion);
    assert.equal(secondReview.assessment.draftDigest, firstReview.assessment.draftDigest);
    const beforeApplyRequests = requestCapture.requests.length;
    const secondReplacementDialog = page.getByRole('dialog', { name: reviewHeading, exact: true });
    const applyButton = secondReplacementDialog.getByRole('button', { name: applyLabel, exact: true });
    await act('Apply Observation as the new root on the existing output', applyButton,
      locator => locator.click({ timeout: ACTION_TIMEOUT_MS }), async () => {
        await secondReplacementDialog.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
        await closeRowSettingsIfOpen();
        await assertSavedRows(OBSERVATION_DEFAULT_PAGE_IDS, observationIDLabel);
      }, 'Apply renders all 25 exact Observation IDs within five seconds');
    const applyEvents = requestCapture.requests.slice(beforeApplyRequests).filter(entry =>
      entry.path === commandPath && entry.method === 'POST' && replacementApply(entry).length > 0);
    assert.equal(applyEvents.length, 1, 'Apply must send one atomic root replacement command.');
    const appliedCommand = replacementApply(applyEvents[0])[0];
    assert.deepEqual(appliedCommand.rowChange, secondReview.assessment.proposal,
      'Apply must submit the exact server-assessed replacement proposal.');
    assert.equal(applyEvents[0].request?.expectedDraftVersion, baseline.draftVersion);
    assert.equal(applyEvents[0].request?.expectedDraftDigest, baseline.draftDigest);
    const proposal = secondReview.assessment.proposal;
    const appliedBuilder = await readBuilder();
    const appliedDocument = documentForOutput(appliedBuilder, outputId);
    assert.equal(appliedDocument.rootResourceType, 'Observation');
    assert.equal(appliedDocument.output.id, outputId);
    assert.equal(appliedDocument.output.title, tableTitle);
    assert.equal(appliedDocument.rows?.kind, 'RECORDS');
    assert.equal(appliedDocument.columns.length, 1);
    assert.equal(appliedDocument.columns[0].source?.field?.path, 'id');
    assert.equal(appliedDocument.columns[0].source?.field?.projectionMode, 'VALUE');
    assert.equal(appliedDocument.population, undefined, 'Replacement must clear the old Patient selection.');
    assert.equal((appliedDocument.construction?.steps ?? []).length, 0, 'Replacement must clear the old Patient ID filter.');
    const appliedGrid = await assertSavedRows(OBSERVATION_DEFAULT_PAGE_IDS, observationIDLabel);
    const afterApplyCapture = requestCapture.requests.slice(beforeApplyRequests);
    const applyResponse = afterApplyCapture.find(entry => entry.path === commandPath && replacementApply(entry).length > 0);
    assert(applyResponse && Number.isFinite(applyResponse.completedAt));
    check('correctness', 'Apply keeps the same output identity and clears the Patient population and filter',
      appliedDocument.rootResourceType === 'Observation' && appliedDocument.output.id === outputId &&
        appliedDocument.output.title === tableTitle && appliedDocument.population === undefined &&
        (appliedDocument.construction?.steps ?? []).length === 0,
      { outputId, title: appliedDocument.output.title, rootResourceType: appliedDocument.rootResourceType,
        clearedPopulation: appliedDocument.population === undefined, clearedFilter: (appliedDocument.construction?.steps ?? []).length === 0,
        appliedDraftVersion: appliedBuilder.draftVersion, replacementProposal: proposal });
    const typedObservationIDs = secondReview.typedPreview.rows.map(row => row[secondReview.idColumn.column]);
    check('correctness', 'REPLACE proposal renders the exact unfiltered default Observation page',
      isDeepStrictEqual(rowIDs(secondReview.proposalGrid, observationIDLabel), OBSERVATION_DEFAULT_PAGE_IDS) &&
        isDeepStrictEqual(typedObservationIDs, OBSERVATION_DEFAULT_PAGE_IDS),
      { oracle: ORACLE_PROVENANCE, idLabel: observationIDLabel,
        candidateRows: rowIDs(secondReview.proposalGrid, observationIDLabel),
        typedRows: typedObservationIDs });

    const appliedReloadStarted = performance.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    await ensureSelected();
    const reloadedGrid = await assertSavedRows(OBSERVATION_DEFAULT_PAGE_IDS, observationIDLabel);
    const appliedReloadMs = Math.round(performance.now() - appliedReloadStarted);
    assert(appliedReloadMs <= ACTION_TIMEOUT_MS, `Applied Observation reload-to-exact-rows took ${appliedReloadMs} ms.`);
    const reloadedBuilder = await readBuilder();
    const reloadedDocument = documentForOutput(reloadedBuilder, outputId);
    assert.equal(reloadedBuilder.draftDigest, appliedBuilder.draftDigest);
    assert(isDeepStrictEqual(reloadedDocument, appliedDocument), 'Reload must preserve the exact replaced document.');
    assert.deepEqual(rowIDs(reloadedGrid, observationIDLabel), OBSERVATION_DEFAULT_PAGE_IDS);
    report.sameTableRootReplacement.lifecycle = {
      explorer: rawExplorer,
      outputId,
      outputTitle: tableTitle,
      patientBaseline: baseline,
      cancel: { draftVersion: baseline.draftVersion, draftDigest: baseline.draftDigest,
        exactRows: [PATIENT_ID], reloadDurationMs: cancelReloadMs },
      proposal: { status: secondReview.assessment.status, mode: secondReview.assessment.mode,
        candidateReceiptId: secondReview.assessment.candidateReceiptId,
        outputId: secondReview.assessment.proposal.outputId, rootNodeId: secondReview.assessment.proposal.rootNodeId,
        exactDefaultObservationIDs: [...OBSERVATION_DEFAULT_PAGE_IDS] },
      apply: { outputId, outputTitle: appliedDocument.output.title, rootResourceType: appliedDocument.rootResourceType,
        populationCleared: appliedDocument.population === undefined,
        filterCleared: (appliedDocument.construction?.steps ?? []).length === 0,
        idLabel: observationIDLabel, exactObservationIDs: rowIDs(appliedGrid, observationIDLabel), reloadDurationMs: appliedReloadMs },
      nativeReplacementApplyRequests: applyEvents.length,
    };
    check('persistence', 'Reload retains the exact default Observation page on the same output',
      reloadedDocument.output.id === outputId && reloadedDocument.output.title === tableTitle &&
        reloadedDocument.rootResourceType === 'Observation' &&
        isDeepStrictEqual(rowIDs(reloadedGrid, observationIDLabel), OBSERVATION_DEFAULT_PAGE_IDS),
      report.sameTableRootReplacement.lifecycle.apply);
    check('performance', 'Applied Observation reload-to-exact-rows within five seconds', appliedReloadMs <= ACTION_TIMEOUT_MS,
      { durationMs: appliedReloadMs, budgetMs: ACTION_TIMEOUT_MS });
    assert.deepEqual(report.errors, [], 'CDA replacement lifecycle must have no unexpected browser/API errors.');
  } catch (error) {
    report.sameTableRootReplacement.failure = String(error?.stack ?? error);
    report.sameTableRootReplacement.failedAt = new Date().toISOString();
    throw error;
  }
}
