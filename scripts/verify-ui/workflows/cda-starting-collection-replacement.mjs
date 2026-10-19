import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { isDeepStrictEqual } from 'node:util';
import { scenarioCaseFor } from '../registry.mjs';
import {
  assertCdaStartingCollectionReplacementReread,
  bindCdaStartingCollectionReplacementSelectionRevisions,
  cdaStartingCollectionReplacementRereadQuery,
  cdaStartingCollectionReplacementScanQuery,
  prepareCdaStartingCollectionReplacementOracle,
} from '../helpers/cda-starting-collection-replacement-oracle.mjs';
import { buildArangoShellInvocation } from '../helpers/owned-arangosh-command.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';

const CASE_ID = 'cda-starting-collection-replacement';
const CASE_NAME = 'cross-resource-route-dependencies';
const ACTION_BUDGET_MS = 5_000;
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const encoded = value => encodeURIComponent(value);
const sortRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const refKey = ref => `${ref.project}/${ref.generation}/${ref.resourceType}/${ref.id}`;
const apiRoot = (project, explorer) => `/api/v1/projects/${encoded(project)}/explorers/${encoded(explorer)}`;

const runAQL = (container, database, query, label) => {
  const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const invocation = buildArangoShellInvocation({ container, database, script });
  const result = spawnSync(invocation.command, invocation.args, {
    encoding: 'utf8', timeout: 30_000, maxBuffer: 8 * 1024 * 1024,
  });
  assert.equal(result.status, 0, `${label} failed: ${normalize(result.stderr || result.stdout).slice(0, 1_500)}`);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, `${label} returned no JSON array`);
  const rows = JSON.parse(result.stdout.slice(jsonStart));
  assert(Array.isArray(rows), `${label} did not return an array`);
  return rows;
};

export async function cdaStartingCollectionReplacementWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.target.fixtureGeneration ?? cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arangoContainer = validatedArangoContainer(cda.target);
  const arangoDatabase = process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev';
  assert(project && generation && apiOrigin && uiOrigin,
    'Starting-collection replacement needs the exact owned CDA project, generation, and origins');
  assert.equal(project, cda.target.fixtureProject, 'The raw replacement oracle must use the owned CDA project');

  const requiredChecks = scenarioCaseFor(CASE_ID, CASE_NAME).requiredChecks;
  assert.deepEqual(requiredChecks, cda.report.requiredChecks,
    'Fixture report must use the registered cross-resource starting-collection replacement contract');
  const requiredCheckSet = new Set(requiredChecks);
  const check = (dimension, name, passed, evidence = {}) => {
    assert(requiredCheckSet.has(name), `Unregistered starting-collection replacement check: ${name}`);
    cda.check(dimension, name, passed, evidence);
    assert(passed, `${name} failed: ${JSON.stringify(evidence).slice(0, 2_000)}`);
  };

  const report = Object.assign(cda.report, {
    source: 'native PopulationPanel handoff and current-draft construction proposal',
    apiRequests: [],
    nativeLifecycle: {},
  });
  const actionTimings = [];
  const timedAction = async (label, locator, perform, after, options = {}) => {
    const started = performance.now();
    await cda.action(label, locator, perform, {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      ...(after ? { after } : {}),
      ...options,
    });
    const durationMs = Math.round(performance.now() - started);
    actionTimings.push({ label, durationMs, budgetMs: ACTION_BUDGET_MS, withinBudget: durationMs <= ACTION_BUDGET_MS });
    assert(durationMs <= ACTION_BUDGET_MS, `${label} exceeded the ${ACTION_BUDGET_MS} ms native action budget`);
    return durationMs;
  };

  const api = async (path, body, timeoutMs = 30_000) => {
    const method = body === undefined ? 'GET' : 'POST';
    const started = performance.now();
    const headers = { 'X-Request-ID': `starting-collection-replacement-${randomUUID()}` };
    const response = method === 'GET'
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: timeoutMs })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data: body, timeout: timeoutMs });
    const text = await response.text();
    let value;
    try { value = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${method} ${path} returned non-JSON HTTP ${response.status()}: ${normalize(text).slice(0, 1_000)}`); }
    report.apiRequests.push({ method, path, status: response.status(), durationMs: Math.round(performance.now() - started) });
    assert(response.ok(), `${method} ${path} returned HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 1_500)}`);
    return value;
  };
  const waitForBrowserFunction = (predicate, args, timeout = ACTION_BUDGET_MS) =>
    page.waitForFunction(predicate, args, { timeout: Math.max(1, Math.min(ACTION_BUDGET_MS, timeout)) });

  const scanRows = runAQL(arangoContainer, arangoDatabase,
    cdaStartingCollectionReplacementScanQuery({ project, generation }),
    'Bounded scoped Observation.subject_Patient witness scan');
  const oracle = prepareCdaStartingCollectionReplacementOracle(scanRows, { project, generation });
  const rawReread = runAQL(arangoContainer, arangoDatabase, cdaStartingCollectionReplacementRereadQuery({
    project,
    generation,
    patientDocumentIDs: oracle.witnesses.originalPatients.map(patient => patient._id),
    candidateObservationDocumentID: oracle.witnesses.candidateObservation._id,
    edgeDocumentID: oracle.witnesses.edge._id,
    candidatePatientDocumentID: oracle.witnesses.originalPatients.find(patient => patient.id === oracle.candidatePatientID)._id,
  }), 'Exact scoped Patient, Observation, and subject_Patient edge reread');
  const rereadEvidence = assertCdaStartingCollectionReplacementReread(rawReread, oracle);
  report.rawOracle = {
    scope: oracle.scope,
    originalPatientIDs: oracle.originalPatientIDs,
    candidateObservationID: oracle.candidateObservationID,
    candidatePatientID: oracle.candidatePatientID,
    edgeDocumentID: oracle.witnesses.edge._id,
    rereadEvidence,
  };

  const rawExplorer = `cda-starting-replace-${randomUUID()}`;
  const title = `CDA starting collection replacement ${randomUUID().slice(0, 8)}`;
  const createdExplorer = await api(`/api/v1/projects/${encoded(project)}/explorers`, { name: rawExplorer, title });
  const explorer = createdExplorer.explorerId ?? createdExplorer.id ?? createdExplorer.explorer?.id ?? rawExplorer;
  assert.equal(explorer, rawExplorer, 'Fresh CDA Explorer must retain its exact unique requested identity');
  const explorerPath = apiRoot(project, explorer);
  const authoringPath = `${explorerPath}/authoring/v2`;
  report.explorerId = explorer;
  report.target.explorer = explorer;

  const explorerList = await api(`/api/v1/projects/${encoded(project)}/explorers`);
  const summaries = Array.isArray(explorerList) ? explorerList : explorerList.explorers ?? explorerList.value ?? [];
  const matchingExplorer = summaries.filter(item => (item.explorerId ?? item.id ?? item.name) === explorer);
  assert.equal(matchingExplorer.length, 1, 'Fresh replacement Explorer must appear exactly once in the owned project');
  assert.equal(matchingExplorer[0].project, project);
  assert.equal(matchingExplorer[0].title, title);
  assert.equal(matchingExplorer[0].management, 'INTERACTIVE');

  let builder = await api(`${authoringPath}/builder`);
  assert.equal(builder.catalog?.generation, generation);
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest);
  const initialScope = {
    generation: builder.catalog.generation,
    snapshotToken: builder.catalog.snapshotToken,
    authorizationScopeDigest: builder.catalog.authorizationScopeDigest,
  };
  const patientRoots = builder.catalog.nodes.filter(node => node.resourceType === 'Patient' && node.rowRootEligible);
  assert.equal(patientRoots.length, 1, 'Scoped catalog must expose exactly one Patient row root');
  const patientIdCandidates = builder.catalog.candidates.filter(candidate =>
    candidate.nodeId === patientRoots[0].nodeId && candidate.fieldPath === 'id' && candidate.logicalType === 'string' && !candidate.repeated);
  assert.equal(patientIdCandidates.length, 1, 'Scoped Patient catalog must expose one scalar Patient.id candidate');

  const documentForOutput = (state, outputId) => {
    const matches = (state.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
    assert.equal(matches.length, 1, `Expected exactly one saved document for output ${outputId}`);
    return matches[0];
  };
  const readBuilder = () => api(`${authoringPath}/builder`);
  const selectionPath = `${explorerPath}/selections`;
  const readSelection = async selectionId => api(`${selectionPath}/${encoded(selectionId)}?limit=100`);
  const requestCapture = cda.captureRequests(explorerPath, {
    browserRequestOrigin: uiOrigin,
    responsePaths: /selections|population-routes|commands|construction-(?:choice-)?proposals|reconcile|preview/,
  });
  const routePath = `${authoringPath}/population-routes`;
  const commandPath = `${authoringPath}/commands`;
  const proposalPath = `${authoringPath}/construction-proposals`;

  const builderURL = new URL(`${uiOrigin}/`);
  builderURL.searchParams.set('project', project);
  builderURL.searchParams.set('explorer', explorer);
  builderURL.searchParams.set('mode', 'builder');
  await cda.navigate(builderURL.toString());
  await page.getByRole('heading', { name: 'Build your first table', exact: true })
    .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  const tableTitle = `Starting collection replacement Patient rows ${randomUUID().slice(0, 8)}`;
  const tableName = page.locator('#first-table-name');
  await timedAction('Name the native Patient starting table', tableName,
    locator => locator.fill(tableTitle, { timeout: ACTION_BUDGET_MS }), undefined, { editable: true });
  let outputId;
  let patientIDColumn;
  const choosePatientRows = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await timedAction('Create the Patient table and direct ID column through the native row picker', choosePatientRows,
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitForBrowserFunction(() => {
        const tables = [...document.querySelectorAll('button[data-testid^="construction-table-"]')];
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return tables.length === 1 && preview?.dataset.previewStatus === 'ready' &&
          !document.body.innerText.includes('Loading your table…') &&
          !document.body.innerText.includes('Creating Patient table…') &&
          !document.body.innerText.includes('Adding the ID column…');
      });
      builder = await readBuilder();
      const created = (builder.workspace?.documents ?? []).filter(document => document.output?.title === tableTitle);
      assert.equal(created.length, 1, 'Native Patient row picker must create exactly one named table');
      const seededDocument = created[0];
      outputId = seededDocument.output.id;
      assert.equal(seededDocument.rootResourceType, 'Patient');
      assert.equal(seededDocument.population, undefined, 'Native table creation must precede selection attachment');
      const idColumns = seededDocument.columns.filter(column => column.source?.kind === 'field' &&
        column.source?.field?.path === 'id' && column.source?.field?.projectionMode === 'VALUE');
      assert.equal(idColumns.length, 1, 'Native Patient row picker must add exactly one direct Patient.id column');
      patientIDColumn = idColumns[0];
      assert(patientIDColumn.columnId, 'Native Patient.id must have a stable current-draft column ID');
    });
  assert.equal(builder.catalog?.snapshotToken, initialScope.snapshotToken);
  assert.equal(builder.catalog?.generation, generation);
  assert.equal(builder.catalog?.authorizationScopeDigest, initialScope.authorizationScopeDigest);
  const createRequest = requestCapture.requests.find(request => request.path === commandPath && request.status === 200 &&
    request.request?.commands?.some(item => item.type === 'CREATE_TABLE' && item.rootNodeId === patientRoots[0].nodeId));
  assert(createRequest, 'Native Patient table setup must emit its exact CREATE_TABLE command from the visible row picker');
  const idColumnRequest = requestCapture.requests.find(request => request.path === commandPath && request.status === 200 &&
    request.request?.commands?.some(item => item.type === 'ADD_COLUMN' && item.outputId === outputId &&
      item.candidateId === patientIdCandidates[0].candidateId));
  assert(idColumnRequest, 'Native Patient row picker must add the catalog-verified Patient.id column');
  report.nativeLifecycle.setup = {
    tableTitle,
    outputId,
    rootResourceType: 'Patient',
    patientIdColumnId: patientIDColumn.columnId,
    createCommandStatus: createRequest.status,
    idColumnCommandStatus: idColumnRequest.status,
  };
  check('correctness', 'native first-table row picker creates the Patient root and direct ID column',
    builder.catalog?.generation === generation && createdExplorer.project === project &&
      patientRoots.length === 1 && patientIDColumn.source?.field?.path === 'id' &&
      createRequest.request?.commands?.some(item => item.type === 'CREATE_TABLE' && item.rootNodeId === patientRoots[0].nodeId) &&
      idColumnRequest.request?.commands?.some(item => item.type === 'ADD_COLUMN' && item.outputId === outputId &&
        item.candidateId === patientIdCandidates[0].candidateId),
    report.nativeLifecycle.setup);

  const createSelection = async (resourceType, refs) => {
    const selection = await api(selectionPath, {
      snapshotToken: initialScope.snapshotToken,
      idempotencyKey: `cda-starting-replace-${randomUUID()}`,
      source: { kind: 'resources', resources: { refs } },
    });
    assert.equal(selection.project, project);
    assert.equal(selection.generation, generation);
    assert.equal(selection.resourceType, resourceType);
    assert.equal(selection.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(selection.memberCount, refs.length);
    const read = await readSelection(selection.id);
    assert.equal(read.revision?.id, selection.id);
    assert.equal(read.revision?.project, project);
    assert.equal(read.revision?.generation, generation);
    assert.equal(read.revision?.resourceType, resourceType);
    assert.equal(read.revision?.scopeDigest, initialScope.authorizationScopeDigest);
    assert.equal(read.revision?.memberCount, refs.length);
    assert.equal(read.members?.length, refs.length);
    const actualRefs = (read.members ?? []).map(member => refKey(member.ref)).sort();
    const expectedRefs = refs.map(refKey).sort();
    assert.deepEqual(actualRefs, expectedRefs, `${resourceType} immutable selection must match exact raw references`);
    assert(read.members.every(member => typeof member.memberKey === 'string' && member.memberKey),
      `${resourceType} immutable selection must expose an opaque member key for each exact reference`);
    return { selection, read, actualRefs };
  };
  const patientSeed = await createSelection('Patient', oracle.originalPatientReferences);
  const observationSeed = await createSelection('Observation', [oracle.candidateObservationReference]);
  const bound = bindCdaStartingCollectionReplacementSelectionRevisions(oracle, {
    patientRevisionId: patientSeed.selection.id,
    observationRevisionId: observationSeed.selection.id,
  });
  assert.deepEqual(bound.sources.patient.references, oracle.originalPatientReferences);
  assert.deepEqual(bound.sources.observation.references, [oracle.candidateObservationReference]);
  assert.equal(bound.relation.label, 'subject_Patient');
  assert.equal(bound.relation.to.id, oracle.candidatePatientID);
  assert.equal(bound.sources.patient.expectedGroupRows.length, 2);
  assert.deepEqual(bound.sources.observation.expectedGroupRows, [{ id: oracle.candidatePatientID, count: 1 }]);
  const exactRawSelectionBindings = patientSeed.selection.project === project && patientSeed.selection.generation === generation &&
    patientSeed.selection.resourceType === 'Patient' && patientSeed.selection.scopeDigest === initialScope.authorizationScopeDigest &&
    observationSeed.selection.project === project && observationSeed.selection.generation === generation &&
    observationSeed.selection.resourceType === 'Observation' && observationSeed.selection.scopeDigest === initialScope.authorizationScopeDigest &&
    isDeepStrictEqual(bound.sources.patient.references, oracle.originalPatientReferences) &&
    isDeepStrictEqual(bound.sources.observation.references, [oracle.candidateObservationReference]) &&
    bound.relation.label === 'subject_Patient' && bound.relation.to.id === oracle.candidatePatientID;
  check('correctness', 'immutable Patient and Observation selections match the exact scoped raw revisions',
    exactRawSelectionBindings,
    { scope: oracle.scope, patient: bound.sources.patient, observation: bound.sources.observation,
      relation: bound.relation, rereadEvidence });
  report.selectionRevisions = {
    patient: { id: patientSeed.selection.id, resourceType: patientSeed.read.revision.resourceType, memberRefs: patientSeed.actualRefs },
    observation: { id: observationSeed.selection.id, resourceType: observationSeed.read.revision.resourceType, memberRefs: observationSeed.actualRefs },
  };

  const tableSelector = `[data-testid="construction-table-${outputId}"]`;
  const readGrid = async kind => page.evaluate(({ kind, tableSelector, outputId }) => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const previewOutputId = preview?.dataset.previewOutputId ?? null;
    const table = kind === 'proposal'
      ? document.querySelector('[data-testid="construction-proposal-preview"] table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!table) return { kind, ready: false, previewOutputId,
      headers: [], rows: [], ariaRowCount: null, previewStatus: preview?.dataset.previewStatus ?? null };
    const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = kind === 'proposal'
      ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
    const rows = kind === 'proposal'
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
    return {
      kind,
      ready: kind === 'proposal' ? true : preview?.dataset.previewStatus === 'ready' && previewOutputId === outputId,
      previewOutputId,
      headers,
      rows,
      ariaRowCount: table.getAttribute('aria-rowcount'),
      previewStatus: preview?.dataset.previewStatus ?? null,
    };
  }, { kind, tableSelector, outputId });
  const waitForGrid = async (kind, rowCount) => {
    await waitForBrowserFunction(({ kind, rowCount, outputId }) => {
      const table = kind === 'proposal'
        ? document.querySelector('[data-testid="construction-proposal-preview"] table')
        : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      if (!table) return false;
      if (Number(table.getAttribute('aria-rowcount')) !== rowCount + 1) return false;
      if (kind === 'proposal') return document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready';
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === outputId &&
        !document.body.innerText.includes('Loading your table…') &&
        !document.body.innerText.includes('Preview failed:');
    }, { kind, rowCount, outputId });
    return readGrid(kind);
  };
  const assertExactGrid = (label, grid, expectedHeaders, expectedRows) => {
    const actualRows = grid.rows.map(row => row.map(normalize));
    const wantedRows = expectedRows.map(row => row.map(normalize));
    const outputIdentityMatches = grid.kind === 'proposal' || grid.previewOutputId === outputId;
    const rowCountMatches = grid.kind === 'proposal'
      ? grid.rows.length === expectedRows.length
      : Number(grid.ariaRowCount) === expectedRows.length + 1;
    const exact = grid.ready && outputIdentityMatches && isDeepStrictEqual(grid.headers, expectedHeaders) &&
      rowCountMatches && isDeepStrictEqual(sortRows(actualRows), sortRows(wantedRows));
    assert(exact, `${label}: rendered rows differ from the literal raw CDA oracle: ${JSON.stringify({ grid, expectedHeaders, expectedRows })}`);
    return { exact, kind: grid.kind, previewOutputId: grid.previewOutputId,
      headers: grid.headers, rows: actualRows, expectedRows: wantedRows, rowCount: expectedRows.length };
  };
  const gridGroupRows = rows => rows.map(row => [row.id, String(row.count)]);
  const gridSourceRows = rows => rows.map(row => [row.id]);
  const selectionURL = selectionId => {
    const url = new URL(`${uiOrigin}/`);
    url.searchParams.set('project', project);
    url.searchParams.set('explorer', explorer);
    url.searchParams.set('mode', 'builder');
    url.searchParams.set('selection', selectionId);
    return url.toString();
  };
  const waitForSelectedTable = async selectionId => {
    await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    await page.locator(tableSelector).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    await timedAction('Select the exact Patient-root output for its starting-collection handoff', page.locator(tableSelector),
      locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
      });
    await timedAction('Open native starting-collection settings for the selected output',
      page.getByTestId('construction-rows-settings-trigger'),
      locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await waitForBrowserFunction(({ selectionId }) => {
          const panel = document.querySelector('section[aria-label="Starting collection"]');
          return panel?.getAttribute('data-selection-revision-id') === selectionId &&
            !panel.innerText.includes('Loading the saved selection…');
        }, { selectionId });
      });
  };
  const waitForRouteResponse = async (selectionId, fromIndex) => {
    const entry = await requestCapture.waitForResponse(request => request.method === 'POST' && request.path === routePath &&
      request.status === 200 && request.request?.outputId === outputId &&
      request.request?.selectionRevisionId === selectionId && request.response?.outputId === outputId &&
      request.response?.selectionRevisionId === selectionId && Array.isArray(request.response?.choices),
    { fromIndex, timeoutMs: ACTION_BUDGET_MS });
    assert.equal(entry.request?.snapshotToken, initialScope.snapshotToken, 'Native route request must use the fresh Explorer snapshot');
    assert.equal(entry.response?.snapshotToken, initialScope.snapshotToken, 'Native route response must use the fresh Explorer snapshot');
    return entry;
  };
  const openHandoff = async (selection, expectedAttachedSelectionId) => {
    const fromIndex = requestCapture.requests.length;
    await cda.navigate(selectionURL(selection.id));
    await waitForSelectedTable(selection.id);
    const panel = page.locator('section[aria-label="Starting collection"]');
    await waitForBrowserFunction(({ selectionId, attachedSelectionId }) => {
      const current = document.querySelector('section[aria-label="Starting collection"]');
      return current?.getAttribute('data-selection-revision-id') === selectionId &&
        current?.getAttribute('data-attached-selection-revision-id') === attachedSelectionId &&
        !current.innerText.includes('Loading the saved selection…');
    }, { selectionId: selection.id, attachedSelectionId: expectedAttachedSelectionId });
    assert.equal(await panel.getAttribute('data-selection-revision-id'), selection.id);
    assert.equal(await panel.getAttribute('data-attached-selection-revision-id'), expectedAttachedSelectionId);
    const routeEvent = await waitForRouteResponse(selection.id, fromIndex);
    return { fromIndex, panel, routeEvent, choices: routeEvent.response.choices };
  };
  const chooseSubjectPatientRoute = async handoff => {
    const matching = handoff.choices.filter(choice => choice.route?.length === 1 &&
      choice.route[0].relationship === 'subject_Patient' &&
      choice.route[0].fromResourceType === 'Patient' && choice.route[0].toResourceType === 'Observation' &&
      choice.route[0].storageDirection === 'INBOUND');
    assert.equal(matching.length, 1, 'Native route response must expose exactly one scoped Patient-to-Observation subject_Patient route');
    const choice = matching[0];
    const selector = page.locator('section[aria-label="Starting collection"] select[aria-label="Population connection"]');
    if (await selector.count()) {
      const options = await selector.evaluate(select => [...select.options].map(option => ({
        value: option.value,
        label: String(option.textContent ?? '').replace(/\s+/g, ' ').trim(),
        disabled: option.disabled,
      })));
      const subject = options.filter(option => !option.disabled && /subject_Patient/i.test(option.label));
      if (subject.length === 1) {
        await timedAction('Choose the native subject_Patient collection connection', selector,
          locator => locator.selectOption(subject[0].value, { timeout: ACTION_BUDGET_MS }));
      } else {
        const index = handoff.choices.indexOf(choice);
        assert(options.some(option => option.value === String(index)),
          `Native subject_Patient route is not available in the visible connection selector: ${JSON.stringify(options)}`);
        await timedAction('Choose the native subject_Patient collection connection', selector,
          locator => locator.selectOption(String(index), { timeout: ACTION_BUDGET_MS }));
      }
    } else {
      const panelText = await handoff.panel.innerText();
      assert.match(panelText, /subject_Patient/i,
        'The only visible collection connection must be the exact subject_Patient route');
    }
    return choice;
  };
  const waitForCommand = async (fromIndex, predicate, label) => {
    const event = await requestCapture.waitForResponse(request => request.method === 'POST' && request.path === commandPath &&
      request.status === 200 && predicate(request.request),
    { fromIndex, timeoutMs: ACTION_BUDGET_MS });
    assert(Number.isFinite(event.completedAt), `${label} command must have a terminal browser response`);
    return event;
  };
  const expectedSavedRoute = choice => choice.route.map(step => ({
    resourceType: step.toResourceType,
    relationship: step.relationship,
    catalogEdgeId: step.edgeId,
    storageDirection: step.storageDirection,
  }));
  const checkSavedSelection = async (state, selectionDescriptor, expectedRoute) => {
    const saved = documentForOutput(state, outputId);
    assert.equal(saved.rootResourceType, 'Patient');
    assert.equal(saved.population?.selectionRevisionId, selectionDescriptor.revisionId);
    assert.deepEqual(saved.population?.route ?? [], expectedRoute);
    const revision = await readSelection(selectionDescriptor.revisionId);
    assert.equal(revision.revision?.id, selectionDescriptor.revisionId);
    assert.equal(revision.revision?.resourceType, selectionDescriptor.resourceType);
    assert.equal(revision.revision?.project, project);
    assert.equal(revision.revision?.generation, generation);
    assert.equal(revision.revision?.scopeDigest, initialScope.authorizationScopeDigest);
    const memberRefs = (revision.members ?? []).map(member => refKey(member.ref)).sort();
    assert.deepEqual(memberRefs, selectionDescriptor.references.map(refKey).sort());
    return { saved, revision: revision.revision, memberRefs };
  };

  const initialURL = selectionURL(patientSeed.selection.id);
  const captureStart = requestCapture.requests.length;
  await cda.navigate(initialURL);
  await waitForSelectedTable(patientSeed.selection.id);
  const initialRoute = await waitForRouteResponse(patientSeed.selection.id, captureStart);
  const directChoices = initialRoute.response.choices.filter(choice => choice.route?.length === 0);
  assert.equal(directChoices.length, 1, 'Patient selection must expose exactly one native empty-route choice for the Patient root');
  const beforePatientAttach = await readBuilder();
  assert.equal(documentForOutput(beforePatientAttach, outputId).population, undefined);
  const initialAttachFrom = requestCapture.requests.length;
  let patientSourceGrid;
  const patientAttachTiming = await timedAction('Attach the two exact Patient resources through the native Use selected resources action',
    page.getByRole('button', { name: 'Use selected resources', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitForBrowserFunction(({ selectionId }) =>
        document.querySelector('section[aria-label="Starting collection"]')
          ?.getAttribute('data-attached-selection-revision-id') === selectionId,
      { selectionId: patientSeed.selection.id });
      await waitForCommand(initialAttachFrom, body => body?.commands?.some(item =>
        item.type === 'SET_TABLE_POPULATION' && item.outputId === outputId &&
        item.selectionRevisionId === patientSeed.selection.id && item.routeChoiceId === directChoices[0].routeChoiceId),
      'Native direct Patient attach');
      const savedState = await readBuilder();
      const verified = await checkSavedSelection(savedState, bound.sources.patient, []);
      assert.deepEqual(verified.saved.population.route, []);
      await waitForGrid('saved', 2);
      patientSourceGrid = assertExactGrid('Native empty-route Patient source shows the exact two raw IDs',
        await readGrid('saved'), [patientIDColumn.label], gridSourceRows(bound.sources.patient.expectedGroupRows));
    });
  const patientAttached = await readBuilder();
  const patientAttachedDocument = documentForOutput(patientAttached, outputId);
  assert(patientAttachTiming <= ACTION_BUDGET_MS, 'Patient native attach and saved route proof exceeded five seconds');
  check('correctness', 'native Patient selection attaches the exact two scoped revisions with an empty route',
    patientSourceGrid.exact && patientAttachedDocument.population?.selectionRevisionId === patientSeed.selection.id &&
      patientAttachedDocument.population?.route?.length === 0 && patientSeed.read.members.length === 2,
    { project, generation, selection: bound.sources.patient, population: patientAttachedDocument.population,
      sourceRows: patientSourceGrid.rows, expectedRows: gridSourceRows(bound.sources.patient.expectedGroupRows),
      previewOutputId: patientSourceGrid.previewOutputId,
      scopeDigest: patientSeed.read.revision.scopeDigest, routeChoiceId: directChoices[0].routeChoiceId,
      nativeCommandStatus: requestCapture.requests.find(request => request.path === commandPath &&
        request.request?.commands?.some(item => item.type === 'SET_TABLE_POPULATION' && item.outputId === outputId &&
          item.selectionRevisionId === patientSeed.selection.id))?.status });

  const groupAction = page.getByTestId('construction-action-group-rows');
  await timedAction('Open native Group editor for Patient ID', groupAction,
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('select[aria-label="Summary 1"]').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
  const summary = page.locator('select[aria-label="Summary 1"]');
  assert.equal(await summary.inputValue(), 'COUNT_ROWS', 'Native Patient Group must start with COUNT_ROWS');
  const groupKey = page.getByRole('checkbox', { name: `Group by ${patientIDColumn.label}`, exact: true });
  const originalGroupRows = gridGroupRows(bound.sources.patient.expectedGroupRows);
  const groupProposalFrom = requestCapture.requests.length;
  let originalGroupProposalGrid;
  await timedAction('Group the exact Patient ID column with COUNT_ROWS', groupKey,
    locator => locator.check({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitForBrowserFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready');
      originalGroupProposalGrid = await readGrid('proposal');
      const keyIndex = originalGroupProposalGrid.headers.indexOf(patientIDColumn.label);
      const countIndex = originalGroupProposalGrid.headers.findIndex(header => /row count/i.test(header));
      assert(keyIndex >= 0 && countIndex >= 0, `Patient Group proposal must show Patient ID and row count: ${JSON.stringify(originalGroupProposalGrid.headers)}`);
      assertExactGrid('Initial native Patient Group proposal matches raw Patient IDs', {
        ...originalGroupProposalGrid,
        rows: originalGroupProposalGrid.rows.map(row => [row[keyIndex], row[countIndex]]),
      }, [patientIDColumn.label, originalGroupProposalGrid.headers[countIndex]], originalGroupRows);
      const event = await requestCapture.waitForResponse(request => request.method === 'POST' && request.path === proposalPath &&
        request.status === 200 && request.request?.outputId === outputId,
      { fromIndex: groupProposalFrom, timeoutMs: ACTION_BUDGET_MS });
      assert.equal(event.response?.outputId, outputId);
      assert.equal(event.request?.snapshotToken, initialScope.snapshotToken);
    });
  const groupApplyTiming = await timedAction('Apply the native Patient ID COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('[data-testid="construction-proposal-panel"]').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitForGrid('saved', originalGroupRows.length);
      const grid = await readGrid('saved');
      const state = await readBuilder();
      const saved = documentForOutput(state, outputId);
      const step = saved.construction?.steps?.find(candidate => candidate.operation?.kind === 'GROUP');
      assert(step, 'Native Group Apply must save one GROUP construction step');
      const key = step.operation.group.keys?.[0];
      const aggregate = step.operation.group.aggregates?.find(item => item.operation === 'COUNT_ROWS');
      const keyOutput = step.outputs?.find(item => item.id === key?.outputColumnId);
      const countOutput = step.outputs?.find(item => item.id === aggregate?.outputColumnId);
      assert(key && aggregate && keyOutput && countOutput, 'Saved Patient Group must bind its exact key and COUNT_ROWS outputs');
      const expectedHeaders = [keyOutput.label, countOutput.label];
      assertExactGrid('Saved Patient Group equals exact two-Patient raw counts', grid, expectedHeaders, originalGroupRows);
    });
  const groupState = await readBuilder();
  const initialGroupDocument = documentForOutput(groupState, outputId);
  const groupStep = initialGroupDocument.construction.steps.find(candidate => candidate.operation?.kind === 'GROUP');
  assert(groupStep);
  const groupKeyDescriptor = groupStep.operation.group.keys[0];
  const groupAggregate = groupStep.operation.group.aggregates.find(item => item.operation === 'COUNT_ROWS');
  const groupKeyOutput = groupStep.outputs.find(output => output.id === groupKeyDescriptor.outputColumnId);
  const groupCountOutput = groupStep.outputs.find(output => output.id === groupAggregate.outputColumnId);
  const groupIdentity = {
    outputId,
    groupStepId: groupStep.id,
    keyColumnId: groupKeyOutput.id,
    countColumnId: groupCountOutput.id,
    sourceColumnId: groupKeyDescriptor.inputColumnId,
  };
  assert.equal(groupIdentity.sourceColumnId, patientIDColumn.columnId,
    'Native Group must use the stable Patient.id source column binding');
  const assertStableGroupIdentity = (document, label) => {
    const step = document.construction?.steps?.find(candidate => candidate.operation?.kind === 'GROUP');
    assert(step, `${label} must retain the dependent GROUP step`);
    assert.equal(step.id, groupIdentity.groupStepId, `${label} changed the dependent Group step ID`);
    const key = step.operation.group.keys?.[0];
    const aggregate = step.operation.group.aggregates?.find(item => item.operation === 'COUNT_ROWS');
    assert.equal(key?.inputColumnId, groupIdentity.sourceColumnId, `${label} changed the source Patient.id column binding`);
    assert.equal(key?.outputColumnId, groupIdentity.keyColumnId, `${label} changed the Group key output column ID`);
    assert.equal(aggregate?.outputColumnId, groupIdentity.countColumnId, `${label} changed the Group count output column ID`);
    assert.deepEqual(step.outputs?.map(output => output.id).sort(),
      [groupIdentity.keyColumnId, groupIdentity.countColumnId].sort(), `${label} changed dependent Group output IDs`);
    return step;
  };
  const initialGroupGrid = assertExactGrid('Saved initial Patient Group values', await readGrid('saved'),
    [groupKeyOutput.label, groupCountOutput.label], originalGroupRows);
  assert(groupApplyTiming <= ACTION_BUDGET_MS, 'Group creation and rendered result exceeded five seconds');
  check('correctness', 'Patient COUNT_ROWS Group binds the stable ID column and shows the exact two raw members',
    initialGroupGrid.exact && groupStep.id === groupIdentity.groupStepId &&
      groupKeyDescriptor.inputColumnId === patientIDColumn.columnId && groupAggregate.operation === 'COUNT_ROWS',
    { groupIdentity, population: initialGroupDocument.population, headers: initialGroupGrid.headers,
      rows: initialGroupGrid.rows, expectedRows: originalGroupRows });

  const groupColumns = [groupKeyOutput.label, groupCountOutput.label];
  const obsGroupRows = gridGroupRows(bound.sources.observation.expectedGroupRows);
  const beforeAbandonment = await readBuilder();
  const beforeAbandonDocument = documentForOutput(beforeAbandonment, outputId);
  const beforeAbandonGrid = assertExactGrid('Group output before the pending Observation handoff', await readGrid('saved'),
    groupColumns, originalGroupRows);
  const pendingFromIndex = requestCapture.requests.length;
  const pending = await openHandoff(observationSeed.selection, patientSeed.selection.id);
  const subjectRoute = pending.choices.filter(choice => choice.route?.length === 1 &&
    choice.route[0].relationship === 'subject_Patient' && choice.route[0].fromResourceType === 'Patient' &&
    choice.route[0].toResourceType === 'Observation' && choice.route[0].storageDirection === 'INBOUND');
  assert.equal(subjectRoute.length, 1, 'Pending Observation handoff must expose the exact inbound subject_Patient route');
  const pendingStatus = await pending.panel.innerText();
  assert.match(pendingStatus, /Current collection stays attached until you replace it/i);
  assert.match(pendingStatus, /New selection:\s*1 Observation resource/i);
  assert.equal(await page.getByTestId('construction-proposal-panel').count(), 0,
    'A pending source handoff must not create a construction proposal');
  await timedAction('Abandon the pending Observation handoff without replacing the Patient source',
    page.getByRole('button', { name: 'Back to table', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('section[aria-label="Starting collection"]').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitForGrid('saved', originalGroupRows.length);
    });
  const afterAbandonment = await readBuilder();
  const afterAbandonDocument = documentForOutput(afterAbandonment, outputId);
  const afterAbandonGrid = assertExactGrid('Group output after abandoning the pending Observation handoff', await readGrid('saved'),
    groupColumns, originalGroupRows);
  const abandonmentCommands = requestCapture.requests.slice(pendingFromIndex).filter(request =>
    request.path === commandPath && request.request?.commands?.length);
  const abandonmentUnchanged = afterAbandonment.draftVersion === beforeAbandonment.draftVersion &&
    afterAbandonment.draftDigest === beforeAbandonment.draftDigest &&
    isDeepStrictEqual(afterAbandonment.workspace, beforeAbandonment.workspace) &&
    isDeepStrictEqual(afterAbandonDocument, beforeAbandonDocument) &&
    isDeepStrictEqual(afterAbandonGrid.rows, beforeAbandonGrid.rows) && abandonmentCommands.length === 0;
  assert(abandonmentUnchanged,
    'Abandoning an uncommitted Observation handoff must preserve the exact saved Patient source, route, construction/output IDs, Group rows, digest, and draft version');
  assert.equal(afterAbandonDocument.population?.selectionRevisionId, patientSeed.selection.id);
  assert.deepEqual(afterAbandonDocument.population?.route ?? [], []);
  assert.deepEqual(afterAbandonDocument.construction?.steps?.map(step => step.id), [groupStep.id]);
  assert.deepEqual(afterAbandonDocument.output?.id, groupIdentity.outputId);
  check('persistence', 'abandoning an Observation handoff preserves the exact saved Patient source and Group state', abandonmentUnchanged,
    { draftVersionBefore: beforeAbandonment.draftVersion, draftVersionAfter: afterAbandonment.draftVersion,
      draftDigestBefore: beforeAbandonment.draftDigest, draftDigestAfter: afterAbandonment.draftDigest,
      population: afterAbandonDocument.population, construction: afterAbandonDocument.construction,
      outputId: afterAbandonDocument.output.id, groupIdentity, expectedRows: originalGroupRows,
      visibleRows: afterAbandonGrid.rows, commandsObserved: abandonmentCommands.length,
      pendingSelectionRevisionId: observationSeed.selection.id });

  const replaceHandoff = await openHandoff(observationSeed.selection, patientSeed.selection.id);
  const replaceChoice = await chooseSubjectPatientRoute(replaceHandoff);
  const replaceFromIndex = requestCapture.requests.length;
  const observationReplaceMs = await timedAction('Replace the current Patient collection with the exact Observation selection',
    page.getByRole('button', { name: 'Replace current collection', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitForBrowserFunction(({ selectionId }) =>
        document.querySelector('section[aria-label="Starting collection"]')
          ?.getAttribute('data-attached-selection-revision-id') === selectionId,
      { selectionId: observationSeed.selection.id });
      await waitForCommand(replaceFromIndex, body => body?.commands?.some(item =>
        item.type === 'SET_TABLE_POPULATION' && item.outputId === outputId &&
        item.selectionRevisionId === observationSeed.selection.id && item.routeChoiceId === replaceChoice.routeChoiceId),
      'Native Observation collection replacement');
      const savedState = await readBuilder();
      const verified = await checkSavedSelection(savedState, bound.sources.observation, expectedSavedRoute(replaceChoice));
      const savedGroup = verified.saved.construction?.steps?.find(candidate => candidate.id === groupStep.id);
      assert(savedGroup, 'Native source replacement must preserve the dependent Group step identity');
    });
  await timedAction('Return from the attached Observation handoff to its Group result',
    page.getByRole('button', { name: 'Back to table', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('section[aria-label="Starting collection"]').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitForGrid('saved', obsGroupRows.length);
    });
  const observationState = await readBuilder();
  const observationDocument = documentForOutput(observationState, outputId);
  const observationSelectionProof = await checkSavedSelection(observationState, bound.sources.observation, expectedSavedRoute(replaceChoice));
  const observationGroup = observationDocument.construction.steps.find(step => step.id === groupStep.id);
  assert(observationGroup, 'Observation replacement must retain the exact dependent Group step');
  assertStableGroupIdentity(observationDocument, 'Observation replacement');
  assert.deepEqual(observationDocument.construction.steps.map(step => step.id), [groupStep.id]);
  assert.equal(observationDocument.output.id, groupIdentity.outputId);
  const observationGroupGrid = assertExactGrid('Observation replacement Group result is the exact subject Patient row',
    await readGrid('saved'), groupColumns, obsGroupRows);
  assert(observationReplaceMs <= ACTION_BUDGET_MS,
    'Native Observation Replace and its exact saved-route proof exceeded five seconds');
  check('correctness', 'native Replace binds the exact Observation revision and subject_Patient route to one raw Patient Group row',
    observationGroupGrid.exact && observationDocument.population?.selectionRevisionId === observationSeed.selection.id &&
      observationDocument.population?.route?.length === 1 &&
      observationDocument.population.route[0].relationship === 'subject_Patient' &&
      observationSelectionProof.revision.resourceType === 'Observation',
    { outputId, groupIdentity, selection: observationSelectionProof.revision,
      population: observationDocument.population, routeChoiceId: replaceChoice.routeChoiceId,
      expectedRows: obsGroupRows, visibleRows: observationGroupGrid.rows });

  const observationSnapshot = {
    draftVersion: observationState.draftVersion,
    draftDigest: observationState.draftDigest,
    outputId: observationDocument.output.id,
    population: structuredClone(observationDocument.population),
    construction: structuredClone(observationDocument.construction),
    columns: structuredClone(observationDocument.columns),
  };
  const reloadAndAssert = async ({ selection, expectedRoute, expectedHeaders, expectedRows, expectedGroupPresent, label }) => {
    const started = performance.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_BUDGET_MS });
    await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    await page.locator(tableSelector).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    const state = await readBuilder();
    const document = documentForOutput(state, outputId);
    const selectionProof = await checkSavedSelection(state, selection, expectedRoute);
    const hasGroup = document.construction?.steps?.some(step => step.operation?.kind === 'GROUP' && step.id === groupStep.id) ?? false;
    assert.equal(hasGroup, expectedGroupPresent, `${label} must preserve the expected dependent Group presence`);
    if (expectedGroupPresent) assertStableGroupIdentity(document, label);
    assert.equal(document.output.id, groupIdentity.outputId);
    assert.deepEqual(document.construction?.steps?.map(step => step.id) ?? [], expectedGroupPresent ? [groupStep.id] : []);
    await waitForGrid('saved', expectedRows.length);
    const grid = assertExactGrid(`${label} exact visible rows`, await readGrid('saved'), expectedHeaders, expectedRows);
    return { durationMs: Math.round(performance.now() - started), state, document, selectionProof, grid };
  };

  const observationReload = await reloadAndAssert({
    selection: bound.sources.observation,
    expectedRoute: expectedSavedRoute(replaceChoice),
    expectedHeaders: groupColumns,
    expectedRows: obsGroupRows,
    expectedGroupPresent: true,
    label: 'Reloaded Observation replacement',
  });
  assert(observationReload.durationMs <= ACTION_BUDGET_MS, 'Observation replacement reload and exact Group rows exceeded five seconds');
  check('persistence', 'Observation replacement preserves selection revision, subject route, Group IDs, and exact rows through reload',
    observationReload.grid.exact && observationReload.selectionProof.revision.id === observationSeed.selection.id &&
      observationReload.selectionProof.revision.resourceType === 'Observation' &&
      observationReload.document.population?.selectionRevisionId === observationSeed.selection.id &&
      isDeepStrictEqual(observationReload.document.population?.route ?? [], expectedSavedRoute(replaceChoice)) &&
      isDeepStrictEqual(observationReload.document.construction?.steps?.map(step => step.id) ?? [], [groupStep.id]) &&
      observationReload.document.output?.id === groupIdentity.outputId,
    { durationMs: observationReload.durationMs, selection: observationReload.selectionProof.revision,
      population: observationReload.document.population, groupIdentity, rows: observationReload.grid.rows,
      expectedRows: obsGroupRows, construction: observationReload.document.construction });

  const removeStep = page.getByTestId(`construction-history-step-${groupStep.id}`);
  await timedAction('Select the exact dependent Group step for removal', removeStep,
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator(`[data-testid="construction-remove-step-${groupStep.id}"]:not(:disabled)`)
        .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
  const openRemovalProposal = async () => {
    const fromIndex = requestCapture.requests.length;
    await timedAction('Preview removal of the exact dependent Group step',
      page.getByTestId(`construction-remove-step-${groupStep.id}`),
      locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
        await waitForBrowserFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready');
        const event = await requestCapture.waitForResponse(request => request.method === 'POST' && request.path === proposalPath &&
          request.status === 200 && request.request?.outputId === outputId &&
          isDeepStrictEqual(request.request?.removeStepIds, [groupStep.id]),
        { fromIndex, timeoutMs: ACTION_BUDGET_MS });
        assert.equal(event.response?.outputId, outputId);
        assert.equal(event.request?.snapshotToken, observationState.catalog.snapshotToken);
        assert.equal(event.request?.expectedDraftVersion, observationSnapshot.draftVersion);
        assert.equal(event.request?.expectedDraftDigest, observationSnapshot.draftDigest);
        assert.deepEqual(event.response?.candidateConstruction?.steps ?? [], [],
          'Group removal proposal must remove only the exact saved Group step');
        const proposalGrid = await readGrid('proposal');
        assertExactGrid('Group removal proposal preview shows the exact single related Patient source row', proposalGrid,
          [patientIDColumn.label], gridSourceRows(bound.sources.observation.expectedGroupRows));
      });
  };
  const beforeCancelState = await readBuilder();
  const beforeCancelDocument = documentForOutput(beforeCancelState, outputId);
  const beforeCancelGrid = assertExactGrid('Grouped Observation state before removal Cancel', await readGrid('saved'),
    groupColumns, obsGroupRows);
  await openRemovalProposal();
  const cancelFromIndex = requestCapture.requests.length;
  const cancelMs = await timedAction('Cancel dependent Group removal and preserve the saved Group',
    page.getByTestId('construction-cancel-proposal'),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('[data-testid="construction-proposal-panel"]').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitForGrid('saved', obsGroupRows.length);
      const afterCancel = await readBuilder();
      const document = documentForOutput(afterCancel, outputId);
      assert.equal(afterCancel.draftVersion, beforeCancelState.draftVersion);
      assert.equal(afterCancel.draftDigest, beforeCancelState.draftDigest);
      assert(isDeepStrictEqual(afterCancel.workspace, beforeCancelState.workspace),
        'Cancel must preserve the exact complete current-draft workspace');
      assert(isDeepStrictEqual(document, beforeCancelDocument), 'Cancel must preserve the exact source, route, Group, and output IDs');
      assertExactGrid('Group removal Cancel keeps exact saved rows', await readGrid('saved'), groupColumns, obsGroupRows);
    });
  const afterCancelState = await readBuilder();
  const afterCancelDocument = documentForOutput(afterCancelState, outputId);
  const afterCancelGrid = assertExactGrid('Group removal Cancel saved Group rows', await readGrid('saved'), groupColumns, obsGroupRows);
  const cancelCommands = requestCapture.requests.slice(cancelFromIndex).filter(request =>
    request.path === commandPath && request.request?.commands?.length);
  assert(cancelMs <= ACTION_BUDGET_MS, 'Group removal Cancel and exact saved-state proof exceeded five seconds');
  const cancelUnchanged = afterCancelState.draftVersion === beforeCancelState.draftVersion &&
    afterCancelState.draftDigest === beforeCancelState.draftDigest &&
    isDeepStrictEqual(afterCancelState.workspace, beforeCancelState.workspace) &&
    isDeepStrictEqual(afterCancelDocument, beforeCancelDocument) &&
    isDeepStrictEqual(afterCancelGrid.rows, beforeCancelGrid.rows) && cancelCommands.length === 0;
  const cancelReload = await reloadAndAssert({
    selection: bound.sources.observation,
    expectedRoute: expectedSavedRoute(replaceChoice),
    expectedHeaders: groupColumns,
    expectedRows: obsGroupRows,
    expectedGroupPresent: true,
    label: 'Reload after canceled Group removal',
  });
  assert(cancelReload.durationMs <= ACTION_BUDGET_MS, 'Canceled Group reload and exact rows exceeded five seconds');
  assert.deepEqual(cancelReload.document, beforeCancelDocument,
    'Reload after Group removal Cancel must restore the exact pre-proposal current-draft document');
  check('persistence', 'Canceling dependent Group removal preserves the exact Observation source and Group rows through reload',
    cancelUnchanged && cancelReload.durationMs <= ACTION_BUDGET_MS && cancelReload.grid.exact &&
      isDeepStrictEqual(cancelReload.document, beforeCancelDocument) &&
      cancelReload.selectionProof.revision.resourceType === 'Observation',
    { cancelDurationMs: cancelMs, cancelReloadDurationMs: cancelReload.durationMs,
      cancelReloadWithinBudget: cancelReload.durationMs <= ACTION_BUDGET_MS,
      draftVersion: afterCancelState.draftVersion, draftDigest: afterCancelState.draftDigest,
      population: afterCancelDocument.population, construction: afterCancelDocument.construction, commandsObserved: cancelCommands.length,
      groupIdentity, rowsBeforeReload: afterCancelGrid.rows, rowsAfterReload: cancelReload.grid.rows, expectedRows: obsGroupRows,
      documentMatchesAfterReload: isDeepStrictEqual(cancelReload.document, beforeCancelDocument) });

  await page.getByTestId(`construction-history-step-${groupStep.id}`).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await timedAction('Reselect the exact Group step after Cancel reload', page.getByTestId(`construction-history-step-${groupStep.id}`),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator(`[data-testid="construction-remove-step-${groupStep.id}"]:not(:disabled)`)
        .waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
    });
  await openRemovalProposal();
  const removeApplyMs = await timedAction('Apply removal of the exact dependent Group step', page.getByTestId('construction-apply-proposal'),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('[data-testid="construction-proposal-panel"]').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitForGrid('saved', 1);
      const state = await readBuilder();
      const document = documentForOutput(state, outputId);
      assert.deepEqual(document.construction?.steps ?? [], [], 'Applying removal must remove the saved Group step');
      assert.equal(document.population?.selectionRevisionId, observationSeed.selection.id);
      assert.deepEqual(document.population?.route, expectedSavedRoute(replaceChoice));
      assertExactGrid('Applied Group removal restores the exact Observation-routed Patient source row',
        await readGrid('saved'), [patientIDColumn.label], gridSourceRows(bound.sources.observation.expectedGroupRows));
    });
  const afterRemoval = await readBuilder();
  const afterRemovalDocument = documentForOutput(afterRemoval, outputId);
  const afterRemovalProof = await checkSavedSelection(afterRemoval, bound.sources.observation, expectedSavedRoute(replaceChoice));
  assert.deepEqual(afterRemovalDocument.construction?.steps ?? [], []);
  assert.equal(afterRemovalDocument.output.id, groupIdentity.outputId);
  const afterRemovalGrid = assertExactGrid('Saved Group removal returns the exact Observation-routed Patient source row',
    await readGrid('saved'), [patientIDColumn.label], gridSourceRows(bound.sources.observation.expectedGroupRows));
  assert(removeApplyMs <= ACTION_BUDGET_MS, 'Group removal Apply and exact Observation-routed source proof exceeded five seconds');
  check('correctness', 'applying Group removal preserves the Observation subject route and restores its exact Patient source row',
    afterRemovalProof.revision.resourceType === 'Observation' && afterRemovalDocument.population?.selectionRevisionId === observationSeed.selection.id &&
      afterRemovalDocument.population?.route?.length === 1 && afterRemovalDocument.construction.steps.length === 0 &&
      afterRemovalGrid.exact,
    { outputId, population: afterRemovalDocument.population, groupStepsAfterApply: afterRemovalDocument.construction.steps,
      sourceRows: afterRemovalGrid.rows, expectedRows: gridSourceRows(bound.sources.observation.expectedGroupRows),
      selectionRevision: afterRemovalProof.revision });

  const restoreHandoff = await openHandoff(patientSeed.selection, observationSeed.selection.id);
  const restoreDirectChoices = restoreHandoff.choices.filter(choice => choice.route?.length === 0);
  assert.equal(restoreDirectChoices.length, 1, 'Restoration must expose exactly one direct Patient route');
  const restoreFromIndex = requestCapture.requests.length;
  const restoreApplyMs = await timedAction('Restore the original Patient collection through native Replace current collection',
    page.getByRole('button', { name: 'Replace current collection', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await waitForBrowserFunction(({ selectionId }) =>
        document.querySelector('section[aria-label="Starting collection"]')
          ?.getAttribute('data-attached-selection-revision-id') === selectionId,
      { selectionId: patientSeed.selection.id });
      await waitForCommand(restoreFromIndex, body => body?.commands?.some(item =>
        item.type === 'SET_TABLE_POPULATION' && item.outputId === outputId &&
        item.selectionRevisionId === patientSeed.selection.id && item.routeChoiceId === restoreDirectChoices[0].routeChoiceId),
      'Native Patient source restoration');
      const state = await readBuilder();
      const verified = await checkSavedSelection(state, bound.sources.patient, []);
      assert.deepEqual(verified.saved.construction?.steps ?? [], [], 'Restoring Patient selection must not recreate the removed Group');
    });
  await timedAction('Return to exact restored Patient source rows',
    page.getByRole('button', { name: 'Back to table', exact: true }),
    locator => locator.click({ timeout: ACTION_BUDGET_MS }), async () => {
      await page.locator('section[aria-label="Starting collection"]').waitFor({ state: 'hidden', timeout: ACTION_BUDGET_MS });
      await waitForGrid('saved', 2);
    });
  const restoredState = await readBuilder();
  const restoredDocument = documentForOutput(restoredState, outputId);
  const restoredProof = await checkSavedSelection(restoredState, bound.sources.patient, []);
  assert.deepEqual(restoredDocument.construction?.steps ?? [], []);
  assert.equal(restoredDocument.output.id, groupIdentity.outputId);
  const restoredGrid = assertExactGrid('Restored Patient source renders the exact two original IDs after Group removal',
    await readGrid('saved'), [patientIDColumn.label], gridSourceRows(bound.sources.patient.expectedGroupRows));
  assert(restoreApplyMs <= ACTION_BUDGET_MS, 'Patient restoration and exact saved-route proof exceeded five seconds');
  check('persistence', 'native Patient restoration returns the empty route and exact two source rows after Group removal',
    restoredProof.revision.resourceType === 'Patient' && restoredDocument.population?.selectionRevisionId === patientSeed.selection.id &&
      restoredDocument.population?.route?.length === 0 && restoredDocument.construction.steps.length === 0 && restoredGrid.exact,
    { outputId, population: restoredDocument.population, construction: restoredDocument.construction,
      rows: restoredGrid.rows, expectedRows: gridSourceRows(bound.sources.patient.expectedGroupRows),
      selectionRevision: restoredProof.revision, groupIdentity });
  const restoreReload = await reloadAndAssert({
    selection: bound.sources.patient,
    expectedRoute: [],
    expectedHeaders: [patientIDColumn.label],
    expectedRows: gridSourceRows(bound.sources.patient.expectedGroupRows),
    expectedGroupPresent: false,
    label: 'Reload after original Patient restoration',
  });
  assert(restoreReload.durationMs <= ACTION_BUDGET_MS, 'Restored Patient reload and exact rows exceeded five seconds');
  assert.deepEqual(restoreReload.document, restoredDocument,
    'Reload must retain the exact restored Patient population and removed Group state');

  const finalRereadRows = runAQL(arangoContainer, arangoDatabase, cdaStartingCollectionReplacementRereadQuery({
    project,
    generation,
    patientDocumentIDs: oracle.witnesses.originalPatients.map(patient => patient._id),
    candidateObservationDocumentID: oracle.witnesses.candidateObservation._id,
    edgeDocumentID: oracle.witnesses.edge._id,
    candidatePatientDocumentID: oracle.witnesses.originalPatients.find(patient => patient.id === oracle.candidatePatientID)._id,
  }), 'Final exact scoped Patient, Observation, and subject_Patient reread');
  const finalReread = assertCdaStartingCollectionReplacementReread(finalRereadRows, oracle);
  check('correctness', 'raw Patient and Observation witnesses remain exact and scoped through the complete replacement lifecycle',
    finalReread.exact && finalReread.patients === 2 && finalReread.candidateObservationID === oracle.candidateObservationID,
    finalReread);
  const reloadTimings = [
    { label: 'Observation replacement reload', durationMs: observationReload.durationMs },
    { label: 'Canceled Group removal reload', durationMs: cancelReload.durationMs },
    { label: 'Patient restoration reload', durationMs: restoreReload.durationMs },
  ].map(item => ({ ...item, budgetMs: ACTION_BUDGET_MS, withinBudget: item.durationMs <= ACTION_BUDGET_MS }));
  check('performance', 'every native starting-collection replacement lifecycle action completes within five seconds',
    actionTimings.every(item => item.withinBudget) && reloadTimings.every(item => item.withinBudget),
    { budgetMs: ACTION_BUDGET_MS, actions: actionTimings, reloads: reloadTimings });
  assert.deepEqual(cda.diagnostics.pageErrors, [], 'Browser JavaScript exceptions occurred during replacement lifecycle');
  assert.deepEqual(report.errors, [], 'Unexpected browser HTTP, runtime, console, or module errors occurred');
  check('correctness', 'starting-collection replacement lifecycle has no unexpected browser errors',
    report.errors.length === 0 && cda.diagnostics.pageErrors.length === 0,
    { errors: report.errors, pageErrors: cda.diagnostics.pageErrors, assetFailures: report.assetFailures });

  report.nativeLifecycle = {
    status: 'passed',
    setup: report.nativeLifecycle.setup,
    outputId,
    initialGroupIdentity: groupIdentity,
    abandonedSelectionRevisionId: observationSeed.selection.id,
    replacementSelectionRevisionId: observationSeed.selection.id,
    replacementRoute: observationDocument.population.route,
    replacementRows: observationGroupGrid.rows,
    observationReloadRows: observationReload.grid.rows,
    canceledRemovalRetainedGroup: groupStep.id,
    removalApplied: afterRemovalDocument.construction.steps.length === 0,
    restoredSelectionRevisionId: patientSeed.selection.id,
    restoredRoute: restoredDocument.population.route,
    restoredRows: restoredGrid.rows,
    restoredReloadRows: restoreReload.grid.rows,
    rawReread: finalReread,
    actionTimings,
  };
  await cda.attachReport('starting-collection-replacement-lifecycle.json', report);
}
