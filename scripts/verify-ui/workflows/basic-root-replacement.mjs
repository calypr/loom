import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { browserURL } from './builder-url.mjs';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import {
  explicitResourceRefs,
  replacementApplyCommandFromRequest,
  replacementApplyCommandMatches,
  replacementAssessmentEffectsMatch,
  replacementAssessmentMatches,
  replacementProposalMatches,
  replacementRowOperationRemovalMatches,
  workspaceColumnConsumerEvidence,
  workspaceTabIdentitySnapshot,
} from '../helpers/basic-root-replacement-contract.mjs';
import { readBasicRootReplacementOracle } from '../helpers/basic-root-replacement-oracle.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const REPLACEMENT_TYPE_CONTROL = 'Record type';
const REPLACEMENT_ACTION = 'Review changes';
const REPLACEMENT_REVIEW_HEADING = 'Replace this table’s current setup?';
const REPLACEMENT_APPLY = 'Change starting record type';
const REPLACEMENT_CANCEL = 'Cancel';
const PREVIEW_SELECTOR = '[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]';
const CONSTRUCTION_PREVIEW_CANDIDATE_TABLE_SELECTOR = '[data-testid="construction-preview"] [data-testid="construction-proposal-preview"][data-preview-status="ready"] table';
const ROOT_REPLACEMENT_TABLE_SELECTOR = 'table[data-testid="construction-proposal-preview"]';
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const header = value => normalize(value).split('\n', 1)[0].toLowerCase();
const requestBody = request => {
  try { return request.postDataJSON(); } catch { return undefined; }
};

export const browserExplorerCollectionPathFor = project =>
  `/api/v1/projects/${encodeURIComponent(encodeURIComponent(canonicalProjectID(project)))}/explorers`;

const snapshotTable = locator => locator.evaluate(table => ({
  headers: [...table.querySelectorAll('[role="columnheader"], thead th')]
    .map(cell => String(
      cell.getAttribute('aria-label') ?? cell.querySelector('span')?.textContent ?? cell.textContent ?? cell.innerText ?? '',
    ).replace(/\s+/g, ' ').trim()),
  rows: [...table.querySelectorAll('[role="row"], tbody tr')]
    .filter(row => row.querySelector('[role="cell"], td'))
    .map(row => [...row.querySelectorAll('[role="cell"], td')].map(cell => String(cell.innerText ?? '').trim())),
}));

const documentForOutput = (builder, outputId) => {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Builder must retain one document for output ${outputId}.`);
  return matches[0];
};

const idColumnFor = (document, resourceType) => {
  const columns = (document.columns ?? []).filter(column =>
    column.source?.kind === 'field' && column.source.field?.path === 'id' &&
    column.source.field?.projectionMode === 'VALUE' && column.occurrenceId === 'base');
  assert.equal(columns.length, 1, `${resourceType} table must expose one base ID VALUE column.`);
  return columns[0];
};

export const replacementRootIDColumnFromPreview = (preview, resourceType) => {
  const columns = Array.isArray(preview?.columns) ? preview.columns : [];
  const matches = columns.filter(column => column?.column === 'id' && column?.sourcePath === 'id' &&
    column?.sourceResourceType === resourceType);
  assert.equal(matches.length, 1, `${resourceType} replacement Preview must expose one source-bound root ID column.`);
  assert.equal(columns.length, 1, 'Replacement Preview must contain only the seeded root ID column.');
  assert(typeof matches[0].label === 'string' && matches[0].label.trim().length > 0,
    'The server-authored root ID column must retain a visible label.');
  return matches[0];
};

export const visibleRowsWaitArgs = (expectedIDs, resourceType, idLabel = `${resourceType} ID`, expectedOutputId) => ({
  ids: expectedIDs,
  type: resourceType,
  expectedHeader: String(idLabel).replace(/\s+/g, ' ').trim().toLowerCase(),
  expectedOutput: expectedOutputId,
});

const rowIDs = (snapshot, resourceType, idLabel = `${resourceType} ID`) => {
  const column = snapshot.headers.findIndex(value => header(value) === header(idLabel));
  assert.notEqual(column, -1, `Visible preview must show the ${idLabel} column: ${JSON.stringify(snapshot.headers)}`);
  return snapshot.rows.map(row => row[column]).sort();
};

export const basicRootReplacementWorkflow = async ({ page, report, check, action }, context) => {
  configureNativePage(page);
  const { target, seed } = context;
  assert.equal(context.custom, false, 'The replacement case requires the isolated Basic fixture loader.');
  assert.equal(seed?.fresh, true, 'Each replacement case requires a fresh Basic project and generation.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_') && target.fixtureGeneration && target.fixtureDir,
    'Replacement must be bound to an owned fresh Basic fixture project/generation.');

  const oracle = readBasicRootReplacementOracle({ fixtureDir: target.fixtureDir });
  const patientIDs = oracle.patients.map(row => row.id).sort();
  const selectedPatientIDs = [...oracle.patientPopulationIDs].sort();
  const filteredPatientIDs = [...oracle.patientFilteredPopulationIDs].sort();
  const studyIDs = oracle.researchStudyRows.map(row => row.id).sort();
  const project = target.fixtureProject;
  const generation = target.fixtureGeneration;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const browserExplorerCollectionPath = browserExplorerCollectionPathFor(project);
  const browserExplorerRootFor = explorerID => `${browserExplorerCollectionPath}/${encodeURIComponent(explorerID)}`;
  const browserAuthoringPathFor = (explorerID, endpoint) =>
    `${browserExplorerRootFor(explorerID)}/authoring/v2/${endpoint}`;
  const uiOrigin = new URL(target.uiUrl).origin;
  const title = `Verify ${context.runID.slice(-10)} root replacement`;
  const tableTitle = `Patient replacement baseline ${context.runID.slice(-6)}`;
  const actionRequests = [];
  const candidatePreviewResponses = new Map();

  const act = (name, locator, perform, options = {}) => {
    const deadlineAt = performance.now() + ACTION_TIMEOUT_MS;
    const remainingTimeout = () => {
      const remaining = Math.floor(deadlineAt - performance.now());
      assert(remaining > 0, `${name} exceeded its ${ACTION_TIMEOUT_MS} ms action-to-render budget.`);
      return remaining;
    };
    const { after, ...actionOptions } = options;
    return action(name, locator, control => perform(control, { remainingTimeout }), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      ...actionOptions,
      ...(after ? { after: () => after({ remainingTimeout }) } : {}),
    });
  };

  const apiJSON = async (path, body) => {
    const response = await fetch(`${target.apiUrl}${path}`, {
      method: body ? 'POST' : 'GET',
      headers: { 'Content-Type': 'application/json' },
      ...(body ? { body: JSON.stringify(body) } : {}),
      signal: AbortSignal.timeout(ACTION_TIMEOUT_MS),
    });
    let value;
    try { value = await response.json(); }
    catch (error) { throw new Error(`Native setup API ${path} returned non-JSON HTTP ${response.status}: ${error.message}`); }
    assert(response.ok, `Native setup API ${path} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 700)}`);
    return value;
  };

  const builderPathFor = explorer => `${explorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2/builder`;
  let explorer;
  let outputId;
  let birthDateColumn;
  let duplicateOutputId;
  let consumerOutputId;
  let consumerTitle;
  let consumerDocument;
  let consumerStep;
  let consumerEvidence;
  let expectedConsumerEffects;
  let baselineTabs;
  let selection;
  let baselineBuilder;
  let baselineDocument;
  let initialReplacementSourceDigest;
  let initialReplacementCatalogDigest;
  let replacementApplyRequestCount = 0;
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.origin !== uiOrigin || request.method() !== 'POST') return;
    const body = requestBody(request);
    if (explorer && url.pathname === browserAuthoringPathFor(explorer, 'row-change') && body?.outputId === outputId) {
      actionRequests.push({ kind: 'replacement-assessment', path: url.pathname, body });
    }
    if (explorer && url.pathname === browserAuthoringPathFor(explorer, 'preview') && body?.outputId === outputId && body?.receiptId) {
      actionRequests.push({ kind: 'candidate-preview', path: url.pathname, body });
    }
    if (explorer && url.pathname === browserAuthoringPathFor(explorer, 'commands')) {
      const rowRootCommands = (body?.commands ?? []).filter(command =>
        command?.type === 'APPLY_TABLE_ROOT_REBASE' && command?.rowChange?.outputId === outputId);
      if (rowRootCommands.length > 0) actionRequests.push({ kind: 'row-root-command', path: url.pathname, body });
      if (replacementApplyCommandFromRequest(body).command?.rowChange?.outputId === outputId) {
        replacementApplyRequestCount += 1;
        actionRequests.push({ kind: 'replacement-apply', path: url.pathname, body });
      }
    }
  });
  page.on('response', response => {
    const request = response.request();
    const url = new URL(response.url());
    const body = requestBody(request);
    if (url.origin !== uiOrigin || request.method() !== 'POST' || !explorer ||
      url.pathname !== browserAuthoringPathFor(explorer, 'preview') || body?.outputId !== outputId || !body?.receiptId) return;
    const key = `${body.receiptId}:${body.outputId}`;
    candidatePreviewResponses.set(key, response.json().then(value => ({ status: response.status(), body: value }))
      .catch(error => ({ status: response.status(), error: error.message })));
  });

  const readBuilder = async () => {
    const builder = await apiJSON(builderPathFor(explorer));
    assert.equal(builder.catalog?.generation, generation, 'Builder must retain the exact fixture generation.');
    assert(builder.catalog?.snapshotToken && builder.draftDigest && Number.isInteger(builder.draftVersion),
      'Builder must expose its current snapshot and source draft CAS.');
    return builder;
  };
  const visibleRows = async (expectedIDs, resourceType, idLabel = `${resourceType} ID`, expectedOutputId = outputId) => {
    const waitArgs = visibleRowsWaitArgs(expectedIDs, resourceType, idLabel, expectedOutputId);
    await page.waitForFunction(({ ids, expectedHeader, expectedOutput }) => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const table = document.querySelector('[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]');
      return preview?.dataset.previewStatus === 'ready' && (!expectedOutput || preview.dataset.previewOutputId === expectedOutput) &&
        table && Number(table.getAttribute('aria-rowcount')) === ids.length + 1 &&
        !document.body.innerText.includes('Loading your table…') &&
        [...table.querySelectorAll('[role="columnheader"]')].some(cell =>
          String(cell.textContent ?? '').replace(/\s+/g, ' ').trim().toLowerCase() === expectedHeader);
    }, waitArgs, { timeout: ACTION_TIMEOUT_MS });
    const snapshot = await snapshotTable(page.locator(PREVIEW_SELECTOR));
    assert.deepEqual(rowIDs(snapshot, resourceType, idLabel), [...expectedIDs].sort(),
      `Visible ${resourceType} rows must equal the exact independent fixture oracle.`);
    return snapshot;
  };
  const visibleProjectedValues = async (expectedValues, label, expectedOutputId) => {
    await page.waitForFunction(({ values, expectedLabel, expectedOutput }) => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const table = document.querySelector('[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput &&
        table && Number(table.getAttribute('aria-rowcount')) === values.length + 1 &&
        !document.body.innerText.includes('Loading your table…') &&
        [...table.querySelectorAll('[role="columnheader"]')].some(cell =>
          String(cell.textContent ?? '').replace(/\s+/g, ' ').trim() === expectedLabel);
    }, { values: expectedValues, expectedLabel: label, expectedOutput: expectedOutputId }, { timeout: ACTION_TIMEOUT_MS });
    const snapshot = await snapshotTable(page.locator(PREVIEW_SELECTOR));
    const valueIndex = snapshot.headers.findIndex(value => normalize(value) === label);
    assert.notEqual(valueIndex, -1, `Visible preview must show the ${label} field.`);
    assert.deepEqual(snapshot.rows.map(row => row[valueIndex]).sort(), [...expectedValues].sort(),
      `Visible ${label} values must equal the exact independent fixture oracle.`);
    return snapshot;
  };
  const waitForCandidatePreview = async expectedOutputId => page.waitForFunction(({ outputId }) => {
    const workspacePreview = document.querySelector('[data-testid="construction-preview"]');
    const candidatePreview = workspacePreview?.querySelector('[data-testid="construction-proposal-preview"]');
    return workspacePreview?.dataset.previewStatus === 'ready' &&
      workspacePreview.dataset.previewOutputId === outputId &&
      candidatePreview?.dataset.previewStatus === 'ready' &&
      candidatePreview.dataset.previewOutputId === outputId;
  }, { outputId: expectedOutputId }, { timeout: ACTION_TIMEOUT_MS });

  report.target.fixtureRawOracle = {
    project,
    generation,
    fixturePath: oracle.source.projectFixturePath,
    files: oracle.source.files,
    patientRows: oracle.patients,
    connectedObservationRows: oracle.observations,
    selectedPatientIDs,
    filteredPatientIDs,
    researchStudyRows: oracle.researchStudyRows,
    diagnosticReportIDs: oracle.diagnosticReportIDs,
    disconnectedResourceTypes: oracle.disconnectedResourceTypes,
    connectedPreservationRelationship: oracle.connectedPreservationRelationship,
    connectedReplacementRelationship: oracle.connectedReplacementRelationship,
  };
  check('correctness', 'fixture oracle independently proves Patient and replacement-root rows plus their relationship',
    patientIDs.length === 3 && selectedPatientIDs.length === 2 && filteredPatientIDs.length === 1 &&
      studyIDs.length === 2 && oracle.diagnosticReportIDs.length === 2 &&
      oracle.disconnectedResourceTypes.length === 1 && oracle.disconnectedResourceTypes.includes('ResearchStudy') &&
      oracle.connectedPreservationRelationship.relationship === 'subject_Patient' &&
      oracle.connectedReplacementRelationship.relationship === 'result_DiagnosticReport',
    report.target.fixtureRawOracle);

  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await act('open native Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await act('name the fresh replacement Explorer', explorerName, () => explorerName.fill(title), {
    editable: true,
    after: async () => assert.equal(await explorerName.inputValue(), title),
  });
  const createResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === browserExplorerCollectionPath &&
      requestBody(request)?.title === title;
  }, { timeout: ACTION_TIMEOUT_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await act('create the isolated native Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createResponse;
      assert.equal(response.status(), 201, `Native Explorer creation returned HTTP ${response.status()}.`);
      await page.waitForFunction(expectedTitle =>
        document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expectedTitle,
      title, { timeout: ACTION_TIMEOUT_MS });
    },
  });
  explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The replacement table must use a fresh Explorer.');
  report.target.explorer = explorer;
  report.target.qaIsolation = {
    project,
    generation,
    fixturePath: target.fixtureDir,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorer,
    mutations: ['native blank Explorer creation', 'native Patient table creation',
      'native birthDate field addition', `native Patient selection ${selection?.id ?? 'created later'}`,
      'native Patient birthDate FILTER', 'native root replacement Cancel and Apply'],
    cleanup: 'All mutations are scoped to the run-unique Basic project and run-created Explorer.',
  };

  const tableName = page.locator('#first-table-name');
  await act('name the source Patient table', tableName, () => tableName.fill(tableTitle), {
    editable: true,
    after: async () => assert.equal(await tableName.inputValue(), tableTitle),
  });
  const choosePatientRows = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await act('choose Patient as the native starting type', choosePatientRows, () => choosePatientRows.click(), {
    after: async () => {
      await page.waitForFunction(count => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        const table = document.querySelector('[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]');
        return preview?.dataset.previewStatus === 'ready' && table && Number(table.getAttribute('aria-rowcount')) === count + 1;
      }, patientIDs.length, { timeout: ACTION_TIMEOUT_MS });
      const initialGrid = await snapshotTable(page.locator(PREVIEW_SELECTOR));
      assert.deepEqual(rowIDs(initialGrid, 'Patient'), patientIDs,
        'Initial native Patient table must render every literal fixture ID.');
    },
  });
  let builder = await readBuilder();
  const patientDocument = (builder.workspace?.documents ?? []).find(document => document.rootResourceType === 'Patient');
  assert(patientDocument?.output?.id, 'Native Patient table creation must persist a root output.');
  outputId = patientDocument.output.id;
  assert.equal(patientDocument.output.title, tableTitle, 'The source output must retain its native table title.');
  assert.equal(patientDocument.rows?.kind, 'RECORDS', 'The baseline must start from Patient record rows.');
  assert.equal(patientDocument.columns.length, 1, 'The source table starts with only its default Patient ID column.');
  idColumnFor(patientDocument, 'Patient');
  await visibleRows(patientIDs, 'Patient');

  const openColumns = page.getByRole('button', { name: 'Add columns: Bring more information into each row.', exact: true });
  await act('open the native Patient field chooser', openColumns, () => openColumns.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const fieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await act('open fields and related data', fieldsTab, () => fieldsTab.click(), {
    after: async () => assert.equal(await fieldsTab.getAttribute('aria-pressed'), 'true'),
  });
  const featureSearch = page.getByRole('searchbox', { name: 'Search features by field name, concept, or code' });
  await act('search Patient.birthDate', featureSearch, () => featureSearch.fill('birthDate'), {
    editable: true,
    after: async () => assert.equal(await featureSearch.inputValue(), 'birthDate'),
  });
  const search = page.getByRole('button', { name: 'Search', exact: true });
  await act('show the raw birthDate field option', search, () => search.click(), {
    after: () => page.getByText('Raw FHIR fields (advanced)', { exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  const birthDateChoice = page.getByRole('checkbox', { name: 'Select Patient.birthDate', exact: true });
  if (await birthDateChoice.count() === 0 || !await birthDateChoice.isVisible()) {
    await act('open raw FHIR field options', rawFields, () => rawFields.click(), {
      after: () => birthDateChoice.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    });
  }
  await act('select Patient.birthDate for the source table', birthDateChoice, () => birthDateChoice.check(), {
    after: async () => assert.equal(await birthDateChoice.isChecked(), true),
  });
  const addBirthDate = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  const columnProposalResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin &&
      url.pathname === browserAuthoringPathFor(explorer, 'construction-choice-proposals') &&
      requestBody(request)?.outputId === outputId;
  }, { timeout: ACTION_TIMEOUT_MS });
  await act('preview the source birthDate column', addBirthDate, () => addBirthDate.click(), {
    after: async () => {
      const response = await columnProposalResponse;
      assert.equal(response.status(), 200, `Column proposal returned HTTP ${response.status()}.`);
      const proposal = await response.json();
      assert.equal(proposal.outputId, outputId);
      assert.equal(proposal.previewStatus, 'READY');
      await page.waitForFunction(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready',
        null, { timeout: ACTION_TIMEOUT_MS });
    },
  });
  const columnReview = page.getByTestId('construction-choice-proposal-panel');
  const applyBirthDate = columnReview.getByRole('button', { name: 'Apply columns', exact: true });
  await act('apply the source birthDate field', applyBirthDate, () => applyBirthDate.click(), {
    after: async () => {
      await columnReview.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      const saved = documentForOutput(await readBuilder(), outputId);
      birthDateColumn = saved.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'birthDate');
      assert(birthDateColumn?.columnId, 'Applied source table must retain one stable birthDate column ID.');
      const rows = await visibleRows(patientIDs, 'Patient');
      const idIndex = rows.headers.findIndex(value => header(value) === 'patient id');
      const birthDateIndex = rows.headers.findIndex(value => header(value) === header(birthDateColumn.label));
      assert(idIndex >= 0 && birthDateIndex >= 0, 'Saved source Preview must show Patient IDs and birthDate values.');
      assert.deepEqual(rows.rows.map(row => [row[idIndex], row[birthDateIndex]]).sort((a, b) => a[0].localeCompare(b[0])),
        oracle.patients.map(row => [row.id, row.birthDate]), 'The custom Patient field must equal raw fixture values.');
    },
  });
  const closeColumns = page.getByTestId('construction-close-operation-editor');
  await act('return to the Patient table', closeColumns, () => closeColumns.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
  });

  builder = await readBuilder();
  const patientRefs = explicitResourceRefs({ project, generation, resourceType: 'Patient', ids: selectedPatientIDs });
  assert(patientRefs.every(ref => ref.project === project),
    'Every selected Patient resource must use the exact Builder route project.');
  selection = await apiJSON(`${explorerCollectionPath}/${encodeURIComponent(explorer)}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `basic-root-replacement-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: patientRefs } },
  });
  const canonicalProject = canonicalProjectID(project);
  assert.deepEqual({ project: selection.project, generation: selection.generation, resourceType: selection.resourceType,
    scopeDigest: selection.scopeDigest, memberCount: selection.memberCount }, {
    project: canonicalProject, generation, resourceType: 'Patient',
    scopeDigest: builder.catalog.authorizationScopeDigest, memberCount: patientRefs.length,
  }, 'Nondefault Patient population must be an exact immutable selection in the authorized fixture scope.');
  const selectionPage = await apiJSON(`${explorerCollectionPath}/${encodeURIComponent(explorer)}/selections/${encodeURIComponent(selection.id)}?limit=100`);
  assert.equal(selectionPage.revision?.id, selection.id);
  assert.equal(selectionPage.revision?.complete, true);
  assert.equal(selectionPage.revision?.project, canonicalProject,
    'Selection readback must match the canonical identity of the authorized Builder project.');
  assert.deepEqual(selectionPage.members.map(member => member.ref.project), selectedPatientIDs.map(() => canonicalProject),
    'Every selected member must remain in the canonical authorized project.');
  assert.deepEqual(selectionPage.members.map(member => member.ref.id).sort(), selectedPatientIDs,
    'Selection revision readback must prove the exact literal Patient population.');
  report.target.qaIsolation.mutations[3] = `native Patient selection ${selection.id} with IDs ${selectedPatientIDs.join(', ')}`;

  const selectionURL = new URL(browserURL(target, project, explorer, 'builder'));
  selectionURL.searchParams.set('selection', selection.id);
  await page.goto(selectionURL.toString(), { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  const startingCollection = page.getByRole('region', { name: 'Starting collection', exact: true });
  const configureRows = page.getByRole('button', { name: 'Configure rows', exact: true });
  const rowSettingsDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
  await act('open starting-collection settings', configureRows, () => configureRows.click(), {
    after: async ({ remainingTimeout }) => {
      await rowSettingsDialog.waitFor({ state: 'visible', timeout: remainingTimeout() });
      await startingCollection.waitFor({ state: 'visible', timeout: remainingTimeout() });
    },
  });
  const attachPopulation = startingCollection.getByRole('button', { name: 'Use selected resources', exact: true });
  await page.waitForFunction(id => {
    const panel = document.querySelector('[aria-label="Starting collection"]');
    const button = [...(panel?.querySelectorAll('button') ?? [])].find(candidate => candidate.textContent.trim() === 'Use selected resources');
    return panel?.dataset.selectionRevisionId === id && button && !button.disabled;
  }, selection.id, { timeout: ACTION_TIMEOUT_MS });
  await act('attach a nondefault two-Patient starting collection', attachPopulation, () => attachPopulation.click(), {
    after: async () => {
      await page.waitForFunction(id => document.querySelector('[aria-label="Starting collection"]')?.dataset.attachedSelectionRevisionId === id,
        selection.id, { timeout: ACTION_TIMEOUT_MS });
      await visibleRows(selectedPatientIDs, 'Patient');
    },
  });
  const closeRowSettings = rowSettingsDialog.getByRole('button', { name: 'Back to table', exact: true });
  await act('close starting-collection settings before filtering', closeRowSettings, () => closeRowSettings.click(), {
    after: () => rowSettingsDialog.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
  });
  builder = await readBuilder();
  let configuredDocument = documentForOutput(builder, outputId);
  assert.equal(configuredDocument.population?.selectionRevisionId, selection.id,
    'The source document must persist the exact nondefault collection revision.');
  assert.equal(configuredDocument.rootResourceType, 'Patient');

  const keepRows = page.getByTestId('construction-action-keep-rows');
  await act('open the source-table FILTER editor', keepRows, () => keepRows.click(), {
    after: () => page.getByTestId('construction-filter-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const filterColumn = page.getByRole('combobox', { name: 'Column', exact: true });
  await act('bind the source filter to birthDate', filterColumn, () => filterColumn.selectOption(birthDateColumn.columnId), {
    after: async () => assert.equal(await filterColumn.inputValue(), birthDateColumn.columnId),
  });
  const condition = page.getByRole('combobox', { name: 'Condition', exact: true });
  await act('choose the inclusive birthDate condition', condition, () => condition.selectOption('GTE'), {
    after: async () => assert.equal(await condition.inputValue(), 'GTE'),
  });
  const filterValue = page.getByLabel('Value', { exact: true });
  await act('set the literal birthDate boundary', filterValue, () => filterValue.fill(oracle.patientFilter.value), {
    editable: true,
    after: async () => assert.equal(await filterValue.inputValue(), oracle.patientFilter.value),
  });
  await waitForCandidatePreview(outputId);
  const filterCandidateTable = page.locator(CONSTRUCTION_PREVIEW_CANDIDATE_TABLE_SELECTOR);
  await filterCandidateTable.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const filterCandidate = await snapshotTable(filterCandidateTable);
  assert.deepEqual(rowIDs(filterCandidate, 'Patient'), filteredPatientIDs,
    'Native FILTER candidate must retain only the exact literal member at the configured boundary.');
  const applyFilter = page.getByTestId('construction-apply-proposal');
  await act('apply the Patient birthDate FILTER', applyFilter, () => applyFilter.click(), {
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      await visibleRows(filteredPatientIDs, 'Patient');
    },
  });
  baselineBuilder = await readBuilder();
  baselineDocument = structuredClone(documentForOutput(baselineBuilder, outputId));
  assert.equal(baselineDocument.population?.selectionRevisionId, selection.id);
  assert.equal(baselineDocument.rootResourceType, 'Patient');
  assert.equal(baselineDocument.output.title, tableTitle);
  assert.equal(baselineDocument.columns.length, 2);
  assert.equal(idColumnFor(baselineDocument, 'Patient').source.field.projectionMode, 'VALUE');
  assert.equal(baselineDocument.columns.find(column => column.columnId === birthDateColumn.columnId)?.source.field.path, 'birthDate');
  const baselineFilters = (baselineDocument.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
  assert.equal(baselineDocument.construction?.steps?.length, 1,
    'The source table must have exactly one saved row operation before replacement.');
  assert.equal(baselineFilters.length, 1, 'Source setup must contain the applied birthDate filter.');
  assert.equal(baselineFilters[0].operation.filter.columnId, birthDateColumn.columnId);
  assert.equal(baselineFilters[0].operation.filter.operator, 'GTE');
  assert.deepEqual(baselineFilters[0].operation.filter.values, [{ kind: 'DATE', date: oracle.patientFilter.value }]);
  report.target.sourceTableBaseline = {
    outputId,
    outputTitle: tableTitle,
    population: { selectionRevisionId: selection.id, memberIDs: selectedPatientIDs },
    filter: { columnId: birthDateColumn.columnId, operator: 'GTE', value: oracle.patientFilter.value },
    exactRenderedPatientIDs: filteredPatientIDs,
    document: baselineDocument,
  };
  check('persistence', 'configured Patient setup has a persisted field, filter, and nondefault collection before replacement', true,
    report.target.sourceTableBaseline);
  const baselineURL = page.url();
  await act('reload the customized Patient source table', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        await visibleRows(filteredPatientIDs, 'Patient');
        const reloaded = await readBuilder();
        assert.equal(reloaded.draftDigest, baselineBuilder.draftDigest);
        assert(isDeepStrictEqual(documentForOutput(reloaded, outputId), baselineDocument),
          'Pre-replacement reload must preserve exact columns, filter, population, and output identity.');
      },
    });
  assert(new URL(page.url()).searchParams.get('selection') === selection.id,
    `Reload must retain the native starting-collection handoff query from ${baselineURL}.`);

  const duplicateTitle = `Patient consumer source ${context.runID.slice(-6)}`;
  const newTable = page.getByTestId('construction-new-table');
  await act('start a second native Patient source for the current-draft consumer', newTable, () => newTable.click(), {
    after: () => page.locator('#first-table-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const duplicateName = page.locator('#first-table-name');
  await act('name the second Patient source', duplicateName, () => duplicateName.fill(duplicateTitle), {
    editable: true,
    after: async () => assert.equal(await duplicateName.inputValue(), duplicateTitle),
  });
  const duplicateRootChoice = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await act('create a separate native Patient root for the consumer join', duplicateRootChoice,
    () => duplicateRootChoice.click(), {
      after: async () => {
        await page.waitForFunction(count => {
          const preview = document.querySelector('[data-testid="construction-preview"]');
          const table = document.querySelector('[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]');
          return preview?.dataset.previewStatus === 'ready' && table && Number(table.getAttribute('aria-rowcount')) === count + 1;
        }, patientIDs.length, { timeout: ACTION_TIMEOUT_MS });
        const grid = await snapshotTable(page.locator(PREVIEW_SELECTOR));
        assert.deepEqual(rowIDs(grid, 'Patient'), patientIDs,
          'The second native Patient root must contain the exact literal fixture IDs.');
      },
    });
  builder = await readBuilder();
  const duplicateDocument = (builder.workspace?.documents ?? []).find(document => document.output?.title === duplicateTitle);
  assert(duplicateDocument?.output?.id && duplicateDocument.output.id !== outputId,
    'The consumer join must have a second independent current-draft Patient output.');
  duplicateOutputId = duplicateDocument.output.id;
  assert.equal(duplicateDocument.rootResourceType, 'Patient');
  const duplicateIDColumn = idColumnFor(duplicateDocument, 'Patient');

  const sourceTableCard = page.getByTestId(`construction-table-${outputId}`);
  await act('select the customized Patient source before creating its consumer', sourceTableCard,
    () => sourceTableCard.click(), {
      after: () => page.waitForFunction(id =>
        document.querySelector(`[data-testid="construction-table-${id}"]`)?.getAttribute('aria-current') === 'page',
      outputId, { timeout: ACTION_TIMEOUT_MS }),
    });
  const beforeConsumerCreate = await readBuilder();
  assert.deepEqual((beforeConsumerCreate.workspace?.documents ?? []).map(document => document.output.id).sort(),
    [outputId, duplicateOutputId].sort(), 'The consumer must be added to this same Explorer after both Patient sources exist.');
  const createCombineTargetResponse = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    const body = requestBody(request);
    return request.method() === 'POST' && url.origin === uiOrigin &&
      url.pathname === browserAuthoringPathFor(explorer, 'commands') &&
      (body?.commands ?? []).some(command => command.type === 'CREATE_TABLE');
  }, { timeout: ACTION_TIMEOUT_MS });
  const combineAction = page.getByTestId('construction-action-combine');
  await act('create a native same-Explorer current-draft consumer table', combineAction, () => combineAction.click(), {
    after: async () => {
      const editor = page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"][data-output-id]');
      await editor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      await page.getByTestId('construction-combine-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      const response = await createCombineTargetResponse;
      assert.equal(response.status(), 200, `Native consumer table creation returned HTTP ${response.status()}.`);
    },
  });
  const combineEditor = page.getByTestId('construction-combine-editor');
  const operationEditor = page.locator('[data-testid="construction-operation-editor"][data-operation-family="COMBINE"]');
  consumerOutputId = await operationEditor.getAttribute('data-output-id');
  assert(consumerOutputId && consumerOutputId !== outputId && consumerOutputId !== duplicateOutputId,
    'Native Combine must create a distinct output to host the source-column consumer.');
  builder = await readBuilder();
  const blankConsumer = documentForOutput(builder, consumerOutputId);
  assert.equal(blankConsumer.rootResourceType, 'Patient', 'Native Combine must preserve the selected Patient root on its new table.');
  assert.equal((blankConsumer.construction?.steps ?? []).length, 0, 'The consumer output must start without a fabricated operation.');
  const chooseJoin = page.getByTestId('construction-combine-choice-key_join');
  await act('choose a current-draft matching operation for the consumer', chooseJoin, () => chooseJoin.click(), {
    after: async () => {
      await page.getByRole('combobox', { name: 'Input table 1', exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      await page.getByRole('combobox', { name: 'Input table 2', exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    },
  });
  for (const [index, sourceId] of [outputId, duplicateOutputId].entries()) {
    const input = page.getByRole('combobox', { name: `Input table ${index + 1}`, exact: true });
    const optionValue = JSON.stringify(['WORKSPACE_OUTPUT', sourceId]);
    const sourceOptions = await input.locator('option').evaluateAll((options, value) =>
      options.filter(option => option.value === value).map(option => ({ disabled: option.disabled, group: option.parentElement?.label })), optionValue);
    assert.deepEqual(sourceOptions, [{ disabled: false, group: 'Current draft tables' }],
      `Combine input ${index + 1} must offer its exact selectable current-draft source.`);
    await act(`bind current-draft Patient source as Combine input ${index + 1}`, input,
      control => control.selectOption(optionValue), {
        after: async () => assert.equal(await input.inputValue(), optionValue),
      });
  }
  const firstMatch = page.getByRole('combobox', { name: 'Matching pair 1 first field', exact: true });
  await act('match the customized Patient source ID', firstMatch,
    control => control.selectOption(idColumnFor(baselineDocument, 'Patient').columnId), {
      after: async () => assert.equal(await firstMatch.inputValue(), idColumnFor(baselineDocument, 'Patient').columnId),
    });
  const secondMatch = page.getByRole('combobox', { name: 'Matching pair 1 second field', exact: true });
  await act('match the second Patient source ID', secondMatch, control => control.selectOption(duplicateIDColumn.columnId), {
    after: async () => assert.equal(await secondMatch.inputValue(), duplicateIDColumn.columnId),
  });
  const joinType = page.getByRole('combobox', { name: 'If a row in the first table has no match', exact: true });
  await act('keep only matching Patient rows in the consumer output', joinType, control => control.selectOption('INNER'), {
    after: async () => assert.equal(await joinType.inputValue(), 'INNER'),
  });
  const addConsumerOutput = combineEditor.getByRole('button', { name: 'Add output field', exact: true });
  await act('add one consumer output sourced from Patient.birthDate', addConsumerOutput, () => addConsumerOutput.click(), {
    after: () => combineEditor.getByRole('textbox', { name: 'Output field 1 name', exact: true })
      .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const consumerOutputName = combineEditor.getByRole('textbox', { name: 'Output field 1 name', exact: true });
  await act('name the Patient.birthDate consumer output', consumerOutputName,
    () => consumerOutputName.fill('consumed_patient_birth_date'), {
      editable: true,
      after: async () => assert.equal(await consumerOutputName.inputValue(), 'consumed_patient_birth_date'),
    });
  const consumerOutputLabel = combineEditor.getByRole('textbox', { name: 'Output field 1 label', exact: true });
  const consumerColumnLabel = 'Consumed Patient birthDate';
  await act('label the projected source field for the consumer', consumerOutputLabel,
    () => consumerOutputLabel.fill(consumerColumnLabel), {
      editable: true,
      after: async () => assert.equal(await consumerOutputLabel.inputValue(), consumerColumnLabel),
    });
  const sourceBirthDate = combineEditor.getByRole('combobox', { name: 'Output field 1 source field in input 1', exact: true });
  await act('bind the consumer projection to the original Patient.birthDate column', sourceBirthDate,
    control => control.selectOption(birthDateColumn.columnId), {
      after: async () => assert.equal(await sourceBirthDate.inputValue(), birthDateColumn.columnId),
    });
  await waitForCandidatePreview(consumerOutputId);
  const consumerCandidateTable = page.locator(CONSTRUCTION_PREVIEW_CANDIDATE_TABLE_SELECTOR);
  await consumerCandidateTable.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const consumerBirthDates = oracle.patients.filter(patient => filteredPatientIDs.includes(patient.id)).map(patient => patient.birthDate);
  const candidateConsumerGrid = await snapshotTable(consumerCandidateTable);
  const candidateDateIndex = candidateConsumerGrid.headers.findIndex(value => normalize(value) === consumerColumnLabel);
  assert.notEqual(candidateDateIndex, -1, 'The native consumer candidate must show the projected Patient.birthDate label.');
  assert.deepEqual(candidateConsumerGrid.rows.map(row => row[candidateDateIndex]).sort(), [...consumerBirthDates].sort(),
    'Native consumer candidate must carry the exact filtered Patient.birthDate value.');
  const applyConsumer = page.getByTestId('construction-apply-proposal');
  await act('apply the current-draft Patient.birthDate consumer', applyConsumer, () => applyConsumer.click(), {
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      await visibleProjectedValues(consumerBirthDates, consumerColumnLabel, consumerOutputId);
    },
  });
  builder = await readBuilder();
  consumerDocument = structuredClone(documentForOutput(builder, consumerOutputId));
  consumerTitle = consumerDocument.output.title;
  assert.equal(consumerDocument.rootResourceType, 'Patient');
  assert(consumerTitle, 'The consumer table must retain its native output title.');
  const consumerSteps = (consumerDocument.construction?.steps ?? []).filter(step =>
    step.operation?.kind === 'COMBINE' && step.operation.combine?.kind === 'KEY_JOIN');
  assert.equal(consumerSteps.length, 1, 'Consumer setup must persist one native current-draft KEY_JOIN step.');
  consumerStep = consumerSteps[0];
  assert.deepEqual(consumerStep.inputs, [
    { kind: 'WORKSPACE_OUTPUT', outputId },
    { kind: 'WORKSPACE_OUTPUT', outputId: duplicateOutputId },
  ], 'Consumer join must bind the exact current-draft source outputs in order.');
  assert(consumerStep.operation.combine.projections.some(projection =>
    projection.inputIndex === 0 && projection.inputColumnId === birthDateColumn.columnId),
  'Consumer document must project the exact Patient.birthDate column from input zero.');
  consumerEvidence = workspaceColumnConsumerEvidence(builder, { sourceOutputId: outputId, columnId: birthDateColumn.columnId });
  assert.equal(consumerEvidence.length, 1,
    'The changed Patient birthDate column must have exactly one current-draft consumer in this owned Explorer.');
  assert.deepEqual(consumerEvidence, [{
    outputId: consumerOutputId,
    stepId: consumerStep.id,
    inputIndex: 0,
    columnId: birthDateColumn.columnId,
  }]);
  expectedConsumerEffects = consumerEvidence.map(({ outputId: affectedOutputId, stepId, inputIndex }) => ({
    outputId: affectedOutputId, stepId, inputIndex,
  }));
  assert(isDeepStrictEqual(documentForOutput(builder, outputId), baselineDocument),
    'Adding a current-draft consumer must not alter the customized Patient source document.');
  assert.deepEqual((builder.workspace?.documents ?? []).map(document => document.output.id).sort(),
    [outputId, duplicateOutputId, consumerOutputId].sort(),
    'Owned Explorer must contain exactly the configured source, its second Patient source, and the dependent consumer.');
  baselineBuilder = builder;
  baselineTabs = workspaceTabIdentitySnapshot(builder);
  assert.deepEqual(baselineTabs.map(tab => tab.outputId).sort(),
    [outputId, duplicateOutputId, consumerOutputId].sort(),
    'The pre-replacement workspace must retain source, duplicate, and consumer tabs.');
  report.target.currentDraftConsumer = {
    outputId: consumerOutputId,
    title: consumerTitle,
    sourceOutputId: outputId,
    sourceColumnId: birthDateColumn.columnId,
    inputIndex: 0,
    stepId: consumerStep.id,
    exactRenderedBirthDates: consumerBirthDates,
    exactWorkspaceTabs: baselineTabs,
    savedDocument: consumerDocument,
  };
  report.target.qaIsolation.mutations.push(
    `native second Patient root ${duplicateOutputId}`,
    `native current-draft consumer ${consumerOutputId} projects ${birthDateColumn.columnId}`,
  );
  check('correctness', 'native current-draft consumer projects the exact source Patient birthDate field', true,
    report.target.currentDraftConsumer);
  await page.getByTestId('construction-operation-editor').waitFor({
    state: 'detached', timeout: ACTION_TIMEOUT_MS,
  });
  await page.waitForFunction(id =>
    document.querySelector(`[data-testid="construction-table-${id}"]`)?.getAttribute('aria-current') === 'page',
  consumerOutputId, { timeout: ACTION_TIMEOUT_MS });

  const researchStudyNode = (baselineBuilder.catalog?.nodes ?? []).find(node =>
    node.resourceType === 'ResearchStudy' && node.rowRootEligible);
  assert(researchStudyNode?.nodeId, 'Fixture catalog must load a ResearchStudy root candidate.');
  assert(oracle.disconnectedResourceTypes.includes('ResearchStudy'), 'The expected replacement target must be independently disconnected.');
  const replacementRouteChangePath = browserAuthoringPathFor(explorer, 'row-change');
  const applyCommandPath = browserAuthoringPathFor(explorer, 'commands');

  const openRowSettings = async ({
    currentResourceType = 'Patient',
    connectedResourceType = 'Observation',
    relationshipName = 'Subject',
  } = {}) => {
    const tableCard = page.getByTestId(`construction-table-${outputId}`);
    if (await tableCard.getAttribute('aria-current') !== 'page') {
      await act('select the existing Patient output before replacement', tableCard, () => tableCard.click(), {
        after: () => page.waitForFunction(id =>
          document.querySelector(`[data-testid="construction-table-${id}"]`)?.getAttribute('aria-current') === 'page',
        outputId, { timeout: ACTION_TIMEOUT_MS }),
      });
    }
    const rowSettings = page.getByTestId('construction-rows-settings-trigger');
    await rowSettings.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const trigger = page.getByTestId('construction-rows-settings-trigger');
    const dialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
    await act('open shared row meaning settings for the existing table', trigger, () => trigger.click(), {
      after: async () => {
        await dialog.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await dialog.getByRole('region', { name: 'What should each row represent?', exact: true })
          .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      },
    });
    const rowMeaning = dialog.getByRole('region', { name: 'What should each row represent?', exact: true });
    const recordType = rowMeaning.getByRole('combobox', { name: REPLACEMENT_TYPE_CONTROL, exact: true });
    const review = rowMeaning.getByRole('button', { name: REPLACEMENT_ACTION, exact: true });
    assert.equal(await recordType.count(), 1,
      'The row setup must expose one Record type picker for preservation and replacement.');
    assert.equal(await dialog.getByRole('combobox', { name: 'Choose starting record type', exact: true }).count(), 0,
      'The old separate preservation picker must be absent.');
    assert.equal(await dialog.getByRole('combobox', { name: 'Choose replacement starting type', exact: true }).count(), 0,
      'The old separate replacement picker must be absent.');
    assert.equal(await dialog.getByRole('heading', { name: 'Replace current setup', exact: true }).count(), 0,
      'Disconnected replacement choices must not appear in a separate setup card.');
    assert.equal(await dialog.getByRole('button', { name: 'Replace current setup', exact: true }).count(), 0,
      'The shared picker must use Review changes for a replacement proposal.');
    await review.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const currentOption = recordType.locator('option:checked');
    const currentChoice = normalize(await currentOption.innerText());
    assert.equal(currentChoice, currentResourceType + ' (current)',
      'The shared picker must retain the current root as its disabled identity choice: ' + currentChoice);
    assert.equal(await currentOption.evaluate(option => option.disabled), true,
      'The current root choice must not become a second actionable route.');
    const preserveOptions = (await recordType.locator('option').allTextContents()).map(normalize);
    const routeOption = preserveOptions.find(label => label.startsWith(connectedResourceType));
    assert(routeOption && routeOption.includes(currentResourceType) && routeOption.includes(relationshipName),
      'The shared picker must retain the route-readable ' + currentResourceType + '-to-' +
        connectedResourceType + ' choice: ' + JSON.stringify(preserveOptions));
    assert.equal(await dialog.getByRole('combobox', { name: REPLACEMENT_TYPE_CONTROL, exact: true }).count(), 1);
    return { dialog, rowMeaning, recordType, review, preserveOptions, routeOption };
  };

  const assessReplacement = async label => {
    const { dialog: rowSettingsDialog, recordType: targetType, review } = await openRowSettings();
    const targetOptions = await targetType.locator('option').evaluateAll(options => options.map(option => ({ value: option.value, label: option.textContent.trim() })));
    assert(targetOptions.some(option => option.value === 'replacement:' + researchStudyNode.nodeId && /ResearchStudy/.test(option.label)),
      `Disconnected ResearchStudy must be an explicit option in the shared Record type picker: ${JSON.stringify(targetOptions)}`);
    await act('select the loaded disconnected ResearchStudy in the shared Record type picker', targetType,
      () => targetType.selectOption('replacement:' + researchStudyNode.nodeId), {
        after: async () => assert.equal(await targetType.inputValue(), 'replacement:' + researchStudyNode.nodeId),
      });
    const replace = review;
    assert.equal(await rowSettingsDialog.getByRole('combobox', { name: REPLACEMENT_TYPE_CONTROL, exact: true }).count(), 1,
      'ResearchStudy replacement must use the same single Record type picker as connected preservation.');
    let assessmentResult;
    let assessmentResponsePromise;
    await act(label, replace, async (control, { remainingTimeout }) => {
      assessmentResponsePromise = page.waitForResponse(response => {
        const request = response.request();
        const url = new URL(response.url());
        const body = requestBody(request);
        return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === replacementRouteChangePath &&
          body?.outputId === outputId && body.mode === 'REPLACE' && body.rootNodeId === researchStudyNode.nodeId;
      }, { timeout: remainingTimeout() });
      await control.click({ timeout: remainingTimeout() });
    }, {
      after: async ({ remainingTimeout }) => {
        const response = await assessmentResponsePromise;
        assert.equal(response.status(), 200, `Replacement assessment returned HTTP ${response.status()}.`);
        const body = requestBody(response.request());
        const expectedRequest = {
          outputId,
          rootNodeId: researchStudyNode.nodeId,
          snapshotToken: baselineBuilder.catalog.snapshotToken,
          draftVersion: baselineBuilder.draftVersion,
          draftDigest: baselineBuilder.draftDigest,
        };
        const requestCheck = replacementAssessmentMatches(body, expectedRequest);
        assert.deepEqual(requestCheck, { ok: true, failures: [] });
        const assessment = await response.json();
        assert.equal(assessment.status, 'READY', `Replacement assessment must be ready: ${JSON.stringify(assessment).slice(0, 700)}`);
        assert.equal(assessment.mode, 'REPLACE', 'Assessment response must retain the explicit replacement mode.');
        assert.equal(assessment.snapshotToken, baselineBuilder.catalog.snapshotToken,
          'Assessment must retain the exact authorized catalog snapshot.');
        assert.equal(assessment.draftVersion, baselineBuilder.draftVersion,
          'Assessment must retain the exact saved draft version.');
        assert.equal(assessment.draftDigest, baselineBuilder.draftDigest,
          'Assessment must retain the exact saved draft digest.');
        assert(assessment.candidateReceiptId, 'Replacement assessment must return a server-owned candidate receipt.');
        assert(assessment.proposal, 'Replacement assessment must return its server-owned proposal.');
        const proposalCheck = replacementProposalMatches(assessment.proposal, {
          outputId,
          rootNodeId: researchStudyNode.nodeId,
          snapshotToken: baselineBuilder.catalog.snapshotToken,
          catalogGeneration: baselineBuilder.catalog.generation,
          ...(initialReplacementCatalogDigest ? { catalogDigest: initialReplacementCatalogDigest } : {}),
          ...(initialReplacementSourceDigest ? { sourceDocumentDigest: initialReplacementSourceDigest } : {}),
        });
        assert.deepEqual(proposalCheck, { ok: true, failures: [] });
        const rowOperationRemoval = replacementRowOperationRemovalMatches(baselineDocument, assessment.proposal);
        assert.deepEqual(rowOperationRemoval, {
          ok: true,
          expected: [`construction:${baselineFilters[0].id}`],
          removed: [`construction:${baselineFilters[0].id}`],
          failures: [],
        }, 'Replacement consequences must name the exact saved birthDate FILTER row operation for removal.');
        assert.deepEqual(replacementAssessmentEffectsMatch(assessment, assessment.proposal), { ok: true, failures: [] },
          'Assessment effects must be the same server-authored arrays retained by its proposal.');
        assert.deepEqual(assessment.workspaceOutputConsumers, expectedConsumerEffects,
          'Replacement assessment must identify the exact current-draft consumer input from the saved consumer document.');
        assert.deepEqual(assessment.proposal.workspaceOutputConsumers, expectedConsumerEffects,
          'Replacement proposal must retain the exact affected consumer output, step, and input index.');
        if (!initialReplacementSourceDigest) {
          initialReplacementSourceDigest = assessment.proposal.sourceDocumentDigest;
          assert(initialReplacementSourceDigest, 'The proposal must carry its source document digest.');
        }
        if (!initialReplacementCatalogDigest) {
          initialReplacementCatalogDigest = assessment.proposal.catalogDigest;
          assert(initialReplacementCatalogDigest, 'The proposal must carry its catalog digest.');
        }
        const replacementDialog = page.getByRole('dialog', { name: REPLACEMENT_REVIEW_HEADING, exact: true });
        await replacementDialog.waitFor({ state: 'visible', timeout: remainingTimeout() });
        await replacementDialog.getByRole('heading', { name: REPLACEMENT_REVIEW_HEADING, exact: true })
          .waitFor({ state: 'visible', timeout: remainingTimeout() });
        const reviewPanel = replacementDialog.getByRole('region', { name: 'Replacement consequences', exact: true });
        await reviewPanel.waitFor({ state: 'visible', timeout: remainingTimeout() });
        await replacementDialog.getByRole('button', { name: REPLACEMENT_APPLY, exact: true })
          .waitFor({ state: 'visible', timeout: remainingTimeout() });
        const reviewText = normalize(await reviewPanel.innerText());
        assert(reviewText.includes('ResearchStudy'), 'Replacement review must name the selected target root.');
        assert(/column|field/i.test(reviewText), 'Replacement review must describe source-column loss.');
        assert(/filter/i.test(reviewText), 'Replacement review must describe source-filter loss.');
        assert(/population|collection/i.test(reviewText), 'Replacement review must describe the source-population change.');
        const removedSetup = reviewPanel.getByRole('list', { name: 'Removed setup features', exact: true });
        assert.deepEqual((await removedSetup.getByRole('listitem').allTextContents()).map(normalize)
          .filter(label => label === 'Construction step — Filter rows'), ['Construction step — Filter rows'],
        'The user-visible consequence list must identify the saved FILTER operation that replacement removes.');
        assert.equal(await reviewPanel.getByRole('heading', { name: 'Current-draft tables to repair', exact: true }).count(), 1,
          'Replacement review must group downstream effects under its human-readable repair heading.');
        assert(reviewText.includes(`${consumerTitle} · Join fields:`) && reviewText.includes(birthDateColumn.label),
          'Replacement review must show the consumer title and affected Patient field label in its human-readable Join fields message.');
        assert(!reviewText.includes(consumerOutputId) && !reviewText.includes(consumerStep.id) &&
          !reviewText.includes(birthDateColumn.columnId),
        'Replacement review must not expose opaque output, step, or column identifiers to the user.');
        assert.equal(await replacementDialog.getByRole('heading', { name: REPLACEMENT_REVIEW_HEADING, exact: true }).count(), 1,
          'The replacement dialog must contain one accessible review heading.');
        await replacementDialog.getByRole('heading', { name: 'Candidate preview', exact: true })
          .waitFor({ state: 'visible', timeout: remainingTimeout() });
        const replacementPreviewTable = replacementDialog.locator(ROOT_REPLACEMENT_TABLE_SELECTOR);
        await replacementPreviewTable.waitFor({ state: 'visible', timeout: remainingTimeout() });
        const previewRequest = actionRequests.find(entry => entry.kind === 'candidate-preview' &&
          entry.body.receiptId === assessment.candidateReceiptId && entry.body.outputId === outputId);
        assert(previewRequest, 'Replacement Preview must use the exact server-issued candidate receipt and existing output ID.');
        const proposalGrid = await snapshotTable(replacementPreviewTable);
        const previewResponse = candidatePreviewResponses.get(`${assessment.candidateReceiptId}:${outputId}`);
        assert(previewResponse, 'The receipt-bound Preview response must be captured with the candidate review.');
        const previewResult = await previewResponse;
        assert.equal(previewResult.status, 200, `Replacement Preview returned HTTP ${previewResult.status}.`);
        const typedPreview = previewResult.body;
        assert.equal(typedPreview?.receiptId, assessment.candidateReceiptId,
          'Typed candidate Preview must carry the exact server-issued receipt.');
        assert.equal(typedPreview?.outputId, outputId,
          'Typed candidate Preview must retain the existing table output ID.');
        const candidateIDColumn = replacementRootIDColumnFromPreview(typedPreview, 'ResearchStudy');
        assert.deepEqual(proposalGrid.headers, [candidateIDColumn.label],
          'The visible candidate table must use only the server-authored root ID label.');
        assert.deepEqual(typedPreview.rows.map(row => row[candidateIDColumn.column]).sort(), studyIDs,
          'Typed replacement Preview must equal the independent literal ResearchStudy fixture IDs.');
        assert.deepEqual(rowIDs(proposalGrid, 'ResearchStudy', candidateIDColumn.label), studyIDs,
          'Server-receipted replacement Preview must show exact literal ResearchStudy IDs.');
        assessmentResult = { assessment, requestCheck, proposalGrid, typedPreview, reviewText, previewRequest,
          rowOperationRemoval, candidateIDColumn, replacementDialog, reviewPanel };
      },
    });
    assert(assessmentResult?.assessment, 'Native replacement review must retain the server assessment result.');
    return assessmentResult;
  };

  const firstAssessmentCount = actionRequests.length;
  const firstReview = await assessReplacement('review explicit ResearchStudy replacement consequences');
  const firstAssessment = firstReview.assessment;
  const firstRequest = actionRequests.slice(firstAssessmentCount).find(entry => entry.kind === 'replacement-assessment');
  assert(firstRequest, 'Native target selection must send its explicit replacement assessment request.');
  const cancelReplacement = firstReview.replacementDialog.getByRole('button', { name: REPLACEMENT_CANCEL, exact: true });
  assert.equal(await cancelReplacement.count(), 1, 'Replacement review must expose one explicit Cancel action.');
  const actionRequestCountBeforeCancel = actionRequests.length;
  const replacementApplyCountBeforeCancel = replacementApplyRequestCount;
  await act('cancel the proposed same-table replacement', cancelReplacement, () => cancelReplacement.click(), {
    after: async () => {
      await firstReview.replacementDialog.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      const rowSettingsAfterCancel = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
      const closeRowSettingsAfterCancel = rowSettingsAfterCancel.getByRole('button', { name: 'Back to table', exact: true });
      await act('return to the Patient table after cancelling replacement', closeRowSettingsAfterCancel,
        () => closeRowSettingsAfterCancel.click(), {
          after: () => rowSettingsAfterCancel.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
        });
      await visibleRows(filteredPatientIDs, 'Patient');
      const afterCancel = await readBuilder();
      assert.equal(afterCancel.draftVersion, baselineBuilder.draftVersion, 'Cancel must preserve the source draft version.');
      assert.equal(afterCancel.draftDigest, baselineBuilder.draftDigest, 'Cancel must preserve the source draft digest.');
      assert(isDeepStrictEqual(documentForOutput(afterCancel, outputId), baselineDocument),
        'Cancel must leave the exact Patient columns, filter, population, root, and output identity unchanged.');
      assert(isDeepStrictEqual(documentForOutput(afterCancel, consumerOutputId), consumerDocument),
        'Cancel must leave the current-draft consumer document unchanged.');
      assert.deepEqual(workspaceTabIdentitySnapshot(afterCancel), baselineTabs,
        'Cancel must preserve every workspace tab ID, output mapping, order, visibility, and title.');
    },
  });
  const cancelRequests = actionRequests.slice(actionRequestCountBeforeCancel);
  const cancelRowRootCommands = cancelRequests.filter(entry => entry.kind === 'row-root-command');
  const cancelReplacementApplies = cancelRequests.filter(entry => entry.kind === 'replacement-apply');
  report.target.cancelMutationRequests = cancelRowRootCommands.map(entry => entry.body);
  assert.equal(cancelReplacementApplies.length, 0,
    `Cancel must not Apply a replacement proposal: ${JSON.stringify(cancelReplacementApplies.map(entry => entry.body))}`);
  assert.equal(cancelRowRootCommands.length, 0,
    `Cancel must not submit any row-root command: ${JSON.stringify(cancelRowRootCommands.map(entry => entry.body))}`);
  assert.equal(replacementApplyRequestCount, replacementApplyCountBeforeCancel,
    'Cancel must leave the total replacement-Apply request count unchanged from its pre-click baseline.');
  check('persistence', 'Cancel leaves customized Patient membership, columns, filter, collection, and output identity unchanged', true,
    { outputId, draftVersion: baselineBuilder.draftVersion, draftDigest: baselineBuilder.draftDigest,
      patientIDs: filteredPatientIDs, populationRevisionId: selection.id, columnCount: baselineDocument.columns.length,
      filterStepId: baselineFilters[0].id, proposalReceiptId: firstAssessment.candidateReceiptId,
      consumer: expectedConsumerEffects, tabs: baselineTabs });

  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await visibleRows(filteredPatientIDs, 'Patient');
  builder = await readBuilder();
  assert(isDeepStrictEqual(documentForOutput(builder, outputId), baselineDocument),
    'Reload after Cancel must retain the exact configured Patient source document.');
  assert(isDeepStrictEqual(documentForOutput(builder, consumerOutputId), consumerDocument),
    'Reload after Cancel must retain the exact current-draft consumer document.');
  assert.deepEqual(workspaceTabIdentitySnapshot(builder), baselineTabs,
    'Reload after Cancel must preserve every workspace tab ID, output mapping, order, visibility, and title.');
  assert.equal(builder.draftDigest, baselineBuilder.draftDigest);

  const secondAssessmentStart = actionRequests.length;
  const secondReview = await assessReplacement('review the same replacement after Cancel');
  const secondAssessment = secondReview.assessment;
  const secondRequest = actionRequests.slice(secondAssessmentStart).find(entry => entry.kind === 'replacement-assessment');
  assert(secondRequest, 'Repeated target selection must send a fresh server replacement assessment.');
  assert.equal(secondRequest.body.snapshotToken, firstRequest.body.snapshotToken);
  assert.equal(secondRequest.body.draftVersion, firstRequest.body.draftVersion);
  assert.equal(secondRequest.body.draftDigest, firstRequest.body.draftDigest);
  assert.equal(secondRequest.body.outputId, firstRequest.body.outputId);
  assert.equal(secondAssessment.proposal.sourceDocumentDigest, firstAssessment.proposal.sourceDocumentDigest,
    'Cancel and reassessment must remain bound to the same unchanged source document.');
  assert.equal(secondAssessment.proposal.catalogDigest, firstAssessment.proposal.catalogDigest,
    'Cancel and reassessment must remain bound to the exact same catalog snapshot digest.');
  const applyReplacement = secondReview.replacementDialog.getByRole('button', { name: REPLACEMENT_APPLY, exact: true });
  const actionRequestCountBeforeApply = actionRequests.length;
  const replacementApplyCountBeforeApply = replacementApplyRequestCount;
  let applyResponsePromise;
  await act('apply the reviewed replacement to the same table', applyReplacement,
    async (control, { remainingTimeout }) => {
      applyResponsePromise = page.waitForResponse(response => {
        const request = response.request();
        const url = new URL(response.url());
        const command = replacementApplyCommandFromRequest(requestBody(request)).command;
        return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === applyCommandPath &&
          command?.rowChange?.outputId === outputId;
      }, { timeout: remainingTimeout() });
      await control.click({ timeout: remainingTimeout() });
    }, {
      after: async ({ remainingTimeout }) => {
        const response = await applyResponsePromise;
        assert.equal(response.status(), 200, `Replacement Apply returned HTTP ${response.status()}.`);
        const body = requestBody(response.request());
        assert.equal(body.snapshotToken, baselineBuilder.catalog.snapshotToken,
          'Apply must compare and set against the exact reviewed catalog snapshot.');
        assert.equal(body.expectedDraftVersion, baselineBuilder.draftVersion,
          'Apply must compare and set against the exact reviewed draft version.');
        assert.equal(body.expectedDraftDigest, baselineBuilder.draftDigest,
          'Apply must compare and set against the exact reviewed draft digest.');
        const applyCheck = replacementApplyCommandMatches(body, secondAssessment.proposal);
        assert.deepEqual(applyCheck, { ok: true, failures: [], command: body.commands[0] });
        const applyRequests = actionRequests.slice(actionRequestCountBeforeApply);
        const applyRowRootCommands = applyRequests.filter(entry => entry.kind === 'row-root-command');
        assert.equal(applyRowRootCommands.length, 1,
          `Explicit Apply must submit exactly one row-root command: ${JSON.stringify(applyRowRootCommands.map(entry => entry.body))}`);
        assert.deepEqual(replacementApplyCommandFromRequest(applyRowRootCommands[0].body).command?.rowChange,
          secondAssessment.proposal, 'The captured Apply request must carry the exact reviewed replacement proposal.');
        assert.equal(replacementApplyRequestCount, replacementApplyCountBeforeApply + 1,
          'Explicit Apply must add exactly one replacement request after its pre-click baseline.');
        await secondReview.replacementDialog.waitFor({ state: 'detached', timeout: remainingTimeout() });
        await visibleRows(studyIDs, 'ResearchStudy', secondReview.candidateIDColumn.label);
      },
    });
  assert.equal(replacementApplyRequestCount, replacementApplyCountBeforeCancel + 1,
    'The reviewed replacement must add exactly one Apply after Cancel leaves the request count unchanged.');

  const replacedBuilder = await readBuilder();
  const replacedDocument = documentForOutput(replacedBuilder, outputId);
  assert(isDeepStrictEqual(documentForOutput(replacedBuilder, consumerOutputId), consumerDocument),
    'Apply must leave the dependent current-draft consumer document unchanged for human repair.');
  assert.deepEqual(workspaceTabIdentitySnapshot(replacedBuilder), baselineTabs,
    'Apply must preserve every workspace tab ID, output mapping, order, visibility, and title.');
  assert.equal(replacedDocument.output.id, outputId, 'Replacement must keep the exact existing output ID.');
  assert.equal(replacedDocument.output.title, tableTitle, 'Replacement must keep the user-authored table title.');
  assert.equal(replacedDocument.rootResourceType, 'ResearchStudy', 'Replacement must change only the selected table root.');
  assert.equal(replacedDocument.rows?.kind, 'RECORDS', 'Replacement must seed default ResearchStudy record rows.');
  assert.equal(replacedDocument.columns.length, 1, 'Replacement must clear prior columns and seed one default ID projection.');
  const replacementIDColumn = idColumnFor(replacedDocument, 'ResearchStudy');
  assert.equal(replacementIDColumn.source.field.projectionMode, 'VALUE');
  assert.equal(replacedDocument.population, undefined, 'Replacement must clear the Patient-only population.');
  assert.equal((replacedDocument.construction?.steps ?? []).length, 0, 'Replacement must clear the saved Patient filter.');
  const appliedPreview = await visibleRows(studyIDs, 'ResearchStudy', secondReview.candidateIDColumn.label);
  report.target.appliedReplacement = {
    outputId,
    outputTitle: replacedDocument.output.title,
    sourceDocumentDigest: secondAssessment.proposal.sourceDocumentDigest,
    rootResourceType: replacedDocument.rootResourceType,
    rowsKind: replacedDocument.rows.kind,
    idColumn: replacementIDColumn,
    populationCleared: replacedDocument.population === undefined,
    oldFilterCleared: (replacedDocument.construction?.steps ?? []).length === 0,
    removedRowOperation: {
      kind: baselineFilters[0].operation.kind,
      id: baselineFilters[0].id,
      effectKey: secondReview.rowOperationRemoval.removed[0],
      removedSetupLabel: 'Construction step — Filter rows',
      clearedAfterApply: (replacedDocument.construction?.steps ?? []).length === 0,
      replacementRowsKind: replacedDocument.rows.kind,
      exactReplacementIDs: rowIDs(appliedPreview, 'ResearchStudy', secondReview.candidateIDColumn.label),
    },
    exactVisibleIDs: rowIDs(appliedPreview, 'ResearchStudy', secondReview.candidateIDColumn.label),
    replacementApplyCount: replacementApplyRequestCount - replacementApplyCountBeforeCancel,
    consumerDocumentUnchanged: isDeepStrictEqual(documentForOutput(replacedBuilder, consumerOutputId), consumerDocument),
    consumerEffects: secondAssessment.proposal.workspaceOutputConsumers,
    workspaceTabs: workspaceTabIdentitySnapshot(replacedBuilder),
  };
  check('correctness', 'Apply replaces only this table setup and previews exact disconnected ResearchStudy rows', true,
    report.target.appliedReplacement);

  await act('reload the applied ResearchStudy table', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        await visibleRows(studyIDs, 'ResearchStudy', secondReview.candidateIDColumn.label);
        const reloaded = await readBuilder();
        const document = documentForOutput(reloaded, outputId);
        assert(isDeepStrictEqual(documentForOutput(reloaded, consumerOutputId), consumerDocument),
          'Reload after Apply must keep the dependent consumer document unchanged.');
        assert.deepEqual(workspaceTabIdentitySnapshot(reloaded), baselineTabs,
          'Reload after Apply must preserve every workspace tab ID, output mapping, order, visibility, and title.');
        assert.equal(document.output.id, outputId);
        assert.equal(document.output.title, tableTitle);
        assert.equal(document.rootResourceType, 'ResearchStudy');
        assert.equal(document.rows.kind, 'RECORDS');
        assert.equal(document.columns.length, 1);
        assert.equal(idColumnFor(document, 'ResearchStudy').source.field.projectionMode, 'VALUE');
        assert.equal(document.population, undefined);
        assert.equal((document.construction?.steps ?? []).length, 0);
        assert(isDeepStrictEqual(document, replacedDocument), 'Reload must preserve the exact default replacement setup.');
      },
    });
  check('persistence', 'Same output ID/title and default ResearchStudy ID rows survive reload', true,
    { outputId, outputTitle: tableTitle, rootResourceType: 'ResearchStudy', ids: studyIDs,
      column: replacementIDColumn, population: null, construction: [], sourceReplacementReceiptId: secondAssessment.candidateReceiptId,
      consumerDocumentUnchanged: true, consumerEffects: secondAssessment.proposal.workspaceOutputConsumers, tabs: baselineTabs });

  const afterReload = await readBuilder();
  const studyNode = afterReload.catalog.nodes.find(node => node.nodeId === researchStudyNode.nodeId);
  const reportNode = afterReload.catalog.nodes.find(node => node.resourceType === 'DiagnosticReport' && node.rowRootEligible);
  assert(studyNode && reportNode, 'Reloaded catalog must retain both disconnected replacement and related resource types.');
  const relationship = afterReload.catalog.edges.find(edge =>
    edge.fromNodeId === studyNode.nodeId && edge.toNodeId === reportNode.nodeId &&
    edge.label === oracle.connectedReplacementRelationship.relationship && edge.populated !== false);
  assert(relationship, 'Reloaded catalog must expose the fixture-proven ResearchStudy→DiagnosticReport relationship.');
  const addColumnsAfterReload = page.getByRole('button', { name: 'Add columns: Bring more information into each row.', exact: true });
  await act('open relationship options on the replacement root', addColumnsAfterReload, () => addColumnsAfterReload.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const relatedFieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await act('show ResearchStudy fields and related data', relatedFieldsTab, () => relatedFieldsTab.click(), {
    after: async () => assert.equal(await relatedFieldsTab.getAttribute('aria-pressed'), 'true'),
  });
  const relationshipSearch = page.getByRole('searchbox', { name: 'Search features by field name, concept, or code' });
  await act('search the ResearchStudy relationship field', relationshipSearch, () => relationshipSearch.fill('result'), {
    editable: true,
    after: async () => assert.equal(await relationshipSearch.inputValue(), 'result'),
  });
  const relationshipSearchButton = page.getByRole('button', { name: 'Search', exact: true });
  await act('load replacement-root relationship choices', relationshipSearchButton, () => relationshipSearchButton.click(), {
    after: () => page.getByText('Raw FHIR fields (advanced)', { exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const rawResearchStudyFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  const resultField = page.getByRole('checkbox', { name: 'Select ResearchStudy.result[].reference', exact: true });
  if (await resultField.count() === 0 || !await resultField.first().isVisible()) {
    await act('show raw ResearchStudy relationship fields', rawResearchStudyFields,
      () => rawResearchStudyFields.click(), {
        after: () => resultField.first().waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
      });
  }
  assert(await resultField.count() > 0,
    'Native replacement-root field chooser must expose the ResearchStudy.result relationship-backed feature.');
  assert((await page.locator('[aria-label="Add columns editor"]').innerText()).includes('result'),
    'Visible replacement-root chooser must show the ResearchStudy result relationship option.');
  await act('close the relationship chooser without changing the replacement table', page.getByTestId('construction-close-operation-editor'),
    close => close.click(), {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }),
    });
  const finalBuilder = await readBuilder();
  assert(isDeepStrictEqual(documentForOutput(finalBuilder, outputId), replacedDocument),
    'Inspecting relationship options must not mutate the saved replacement table.');
  check('correctness', 'Reloaded ResearchStudy table offers the independently fixture-proved DiagnosticReport relationship', true,
    { outputId, targetRoot: 'ResearchStudy', relationship: relationship.label,
      toResourceType: oracle.connectedReplacementRelationship.toResourceType,
      featureChooserSearch: 'result', visibleRelationshipOption: true });

  const beforeConnectedStartFresh = await readBuilder();
  assert(isDeepStrictEqual(documentForOutput(beforeConnectedStartFresh, outputId), replacedDocument),
    'ResearchStudy Start Fresh must begin from the exact saved default replacement document.');
  const {
    dialog: startFreshSettings,
    rowMeaning: startFreshRowMeaning,
    recordType: startFreshRecordType,
    review: reviewConnectedType,
    routeOption: diagnosticRouteOption,
  } = await openRowSettings({
    currentResourceType: 'ResearchStudy',
    connectedResourceType: 'DiagnosticReport',
    relationshipName: 'Result',
  });
  const connectedTypeOptions = await startFreshRecordType.locator('option')
    .evaluateAll(options => options.map(option => ({
      value: option.value,
      label: option.textContent,
      disabled: option.disabled,
    }))).then(options => options.map(option => ({ ...option, label: normalize(option.label) })));
  const connectedDiagnosticReport = connectedTypeOptions.find(option =>
    option.value === 'node:' + reportNode.nodeId && option.label.startsWith('DiagnosticReport'));
  assert(connectedDiagnosticReport && !connectedDiagnosticReport.disabled,
    'The shared Record type picker must expose the connected DiagnosticReport catalog node.');
  assert.equal(connectedDiagnosticReport.label,
    'DiagnosticReport via ResearchStudy → Result → DiagnosticReport',
    'The connected choice must retain its exact readable ResearchStudy Result route.');
  assert.equal(diagnosticRouteOption, connectedDiagnosticReport.label,
    'The shared picker route contract must identify the same DiagnosticReport option selected below.');
  await act('select connected DiagnosticReport in the shared Record type picker', startFreshRecordType,
    () => startFreshRecordType.selectOption(connectedDiagnosticReport.value), {
      after: async () => assert.equal(await startFreshRecordType.inputValue(), connectedDiagnosticReport.value),
    });
  const startFresh = startFreshRowMeaning.getByRole('button', {
    name: 'Start fresh with DiagnosticReport', exact: true,
  });
  await startFresh.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await reviewConnectedType.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  assert.equal(await startFreshSettings.getByRole('combobox', {
    name: REPLACEMENT_TYPE_CONTROL, exact: true,
  }).count(), 1, 'Start Fresh must use the same single Record type picker.');
  const startFreshRequestIndex = actionRequests.length;
  const replacementApplyCountBeforeStartFresh = replacementApplyRequestCount;
  let startFreshAssessment;
  let startFreshDialog;
  let startFreshReviewText;
  let startFreshCandidate;
  let startFreshCandidateGrid;
  await act('Start fresh with connected DiagnosticReport', startFresh, async (control, { remainingTimeout }) => {
    const responsePromise = page.waitForResponse(response => {
      const request = response.request();
      const url = new URL(response.url());
      const body = requestBody(request);
      return request.method() === 'POST' && url.origin === uiOrigin &&
        url.pathname === replacementRouteChangePath && body?.outputId === outputId &&
        body.mode === 'REPLACE' && body.rootNodeId === reportNode.nodeId;
    }, { timeout: remainingTimeout() });
    await control.click({ timeout: remainingTimeout() });
    const response = await responsePromise;
    assert.equal(response.status(), 200, 'Connected DiagnosticReport Start Fresh assessment must return HTTP 200.');
    const request = requestBody(response.request());
    assert.deepEqual(replacementAssessmentMatches(request, {
      outputId,
      rootNodeId: reportNode.nodeId,
      snapshotToken: beforeConnectedStartFresh.catalog.snapshotToken,
      draftVersion: beforeConnectedStartFresh.draftVersion,
      draftDigest: beforeConnectedStartFresh.draftDigest,
    }), { ok: true, failures: [] },
    'Start Fresh must assess explicit REPLACE against the exact saved ResearchStudy draft and target node.');
    startFreshAssessment = await response.json();
    assert.equal(startFreshAssessment.status, 'READY',
      'Connected DiagnosticReport Start Fresh must return a ready replacement assessment.');
    assert.equal(startFreshAssessment.mode, 'REPLACE');
    assert.equal(startFreshAssessment.candidateRootResourceType, 'DiagnosticReport');
    assert.equal(startFreshAssessment.snapshotToken, beforeConnectedStartFresh.catalog.snapshotToken);
    assert.equal(startFreshAssessment.draftVersion, beforeConnectedStartFresh.draftVersion);
    assert.equal(startFreshAssessment.draftDigest, beforeConnectedStartFresh.draftDigest);
    assert.deepEqual(replacementProposalMatches(startFreshAssessment.proposal, {
      outputId,
      rootNodeId: reportNode.nodeId,
      snapshotToken: beforeConnectedStartFresh.catalog.snapshotToken,
      catalogGeneration: beforeConnectedStartFresh.catalog.generation,
    }), { ok: true, failures: [] },
    'Start Fresh must return the exact receipt-bound replacement proposal for DiagnosticReport.');
    assert.deepEqual([...startFreshAssessment.proposal.removedFeatureKeys].sort(),
      ['column:id', 'root:ResearchStudy'],
      'Resetting the already-default ResearchStudy table must report only its root and default ID as replaced.');
    assert.deepEqual(startFreshAssessment.proposal.workspaceOutputConsumers, [],
      'The reset must report no current-draft inputs using the existing default ResearchStudy output.');
    assert.deepEqual(startFreshAssessment.proposal.sharedFilterBindings, [],
      'The reset must report no shared filters to remove from the default ResearchStudy setup.');
    assert.deepEqual(replacementAssessmentEffectsMatch(startFreshAssessment, startFreshAssessment.proposal),
      { ok: true, failures: [] },
      'The visible Start Fresh assessment and submitted proposal must retain the same consequence arrays.');
    startFreshDialog = page.getByRole('dialog', { name: REPLACEMENT_REVIEW_HEADING, exact: true });
    await startFreshDialog.waitFor({ state: 'visible', timeout: remainingTimeout() });
    const consequencePanel = startFreshDialog.getByRole('region', { name: 'Replacement consequences', exact: true });
    await consequencePanel.waitFor({ state: 'visible', timeout: remainingTimeout() });
    startFreshReviewText = normalize(await consequencePanel.innerText());
    assert(startFreshReviewText.includes('DiagnosticReport'),
      'Start Fresh consequences must name the selected connected DiagnosticReport root.');
    assert(startFreshReviewText.includes('One row per source record · ' +
      oracle.diagnosticReportIDs.length + ' preview rows'),
    'Start Fresh consequences must report the exact fixture-backed DiagnosticReport row count.');
    assert(startFreshReviewText.includes('Default ID column'),
      'Start Fresh consequences must identify the seeded default root ID column.');
    const removedSetup = consequencePanel.getByRole('list', { name: 'Removed setup features', exact: true });
    const removedSetupLabels = (await removedSetup.getByRole('listitem').allTextContents()).map(normalize).sort();
    const currentResearchStudyIDColumn = idColumnFor(replacedDocument, 'ResearchStudy');
    assert.deepEqual(removedSetupLabels, [
      'Field — ' + currentResearchStudyIDColumn.label,
      'Starting record type — ResearchStudy',
    ].sort(),
      'Start Fresh must show the exact current root and default ID that its REPLACE action resets.');
    assert(startFreshReviewText.includes('No current-draft table inputs use this output.'),
      'The reset review must show that no current-draft table inputs depend on this default output.');
    assert(startFreshReviewText.includes('No shared-filter bindings will be removed.'),
      'The reset review must show that no shared-filter bindings need removal.');
    await startFreshDialog.getByRole('heading', { name: 'Candidate preview', exact: true })
      .waitFor({ state: 'visible', timeout: remainingTimeout() });
    const previewTable = startFreshDialog.locator(ROOT_REPLACEMENT_TABLE_SELECTOR);
    await previewTable.waitFor({ state: 'visible', timeout: remainingTimeout() });
    const capturedRequest = actionRequests.slice(startFreshRequestIndex)
      .find(entry => entry.kind === 'replacement-assessment' &&
        entry.body.mode === 'REPLACE' && entry.body.rootNodeId === reportNode.nodeId);
    assert(capturedRequest, 'Start Fresh must retain its exact connected DiagnosticReport replacement request.');
    const candidateReceiptId = startFreshAssessment.candidateReceiptId;
    const candidatePreviewRequest = actionRequests.slice(startFreshRequestIndex)
      .find(entry => entry.kind === 'candidate-preview' &&
        entry.body.receiptId === candidateReceiptId && entry.body.outputId === outputId);
    assert(candidatePreviewRequest,
      'Start Fresh preview must use the exact server-issued receipt and existing output identity.');
    const previewResult = await candidatePreviewResponses.get(candidateReceiptId + ':' + outputId);
    assert(previewResult && previewResult.status === 200,
      'Start Fresh must capture the receipt-bound DiagnosticReport preview response.');
    const typedPreview = previewResult.body;
    assert.equal(typedPreview.receiptId, candidateReceiptId);
    assert.equal(typedPreview.outputId, outputId);
    startFreshCandidate = replacementRootIDColumnFromPreview(typedPreview, 'DiagnosticReport');
    assert.deepEqual(typedPreview.rows.map(row => row[startFreshCandidate.column]).sort(),
      oracle.diagnosticReportIDs,
      'Start Fresh typed preview rows must equal the exact literal DiagnosticReport fixture IDs.');
    startFreshCandidateGrid = await snapshotTable(previewTable);
    assert.deepEqual(startFreshCandidateGrid.headers, [startFreshCandidate.label],
      'Start Fresh visible preview must contain only the server-authored DiagnosticReport ID column.');
    assert.deepEqual(rowIDs(startFreshCandidateGrid, 'DiagnosticReport', startFreshCandidate.label),
      oracle.diagnosticReportIDs,
      'Start Fresh visible preview rows must equal the independent DiagnosticReport fixture oracle.');
    const unchangedDuringReview = await readBuilder();
    assert(isDeepStrictEqual(documentForOutput(unchangedDuringReview, outputId), replacedDocument),
      'Reviewing connected Start Fresh must leave the saved ResearchStudy setup unchanged until Apply.');
    assert.deepEqual(workspaceTabIdentitySnapshot(unchangedDuringReview), baselineTabs,
      'Reviewing connected Start Fresh must keep all existing workspace tab identities.');
    check('correctness', 'connected DiagnosticReport Start Fresh reviews exact reset consequences and default-ID rows', true, {
      outputId,
      targetRoot: 'DiagnosticReport',
      route: diagnosticRouteOption,
      assessmentRequest: capturedRequest.body,
      proposal: startFreshAssessment.proposal,
      removedFeatureKeys: startFreshAssessment.proposal.removedFeatureKeys,
      noCurrentDraftConsumers: true,
      noSharedFilterBindings: true,
      reviewText: startFreshReviewText,
      candidateReceiptId,
      candidateIDColumn: startFreshCandidate,
      preview: startFreshCandidateGrid,
      exactDiagnosticReportIDs: oracle.diagnosticReportIDs,
    });
  }, {
    after: async ({ remainingTimeout }) => {
      assert(startFreshAssessment?.proposal && startFreshDialog && startFreshCandidate,
        'Start Fresh review must retain its assessment, consequence dialog, and default-ID candidate.');
      await startFreshDialog.getByRole('button', { name: REPLACEMENT_APPLY, exact: true })
        .waitFor({ state: 'visible', timeout: remainingTimeout() });
    },
  });
  const startFreshApply = startFreshDialog.getByRole('button', { name: REPLACEMENT_APPLY, exact: true });
  const actionRequestCountBeforeStartFreshApply = actionRequests.length;
  const replacementApplyCountBeforeStartFreshApply = replacementApplyRequestCount;
  let startFreshApplyResponsePromise;
  await act('Apply connected Start Fresh to DiagnosticReport', startFreshApply,
    async (control, { remainingTimeout }) => {
      startFreshApplyResponsePromise = page.waitForResponse(response => {
        const request = response.request();
        const url = new URL(response.url());
        const command = replacementApplyCommandFromRequest(requestBody(request)).command;
        return request.method() === 'POST' && url.origin === uiOrigin &&
          url.pathname === applyCommandPath && command?.rowChange?.outputId === outputId &&
          command.rowChange.rootNodeId === reportNode.nodeId;
      }, { timeout: remainingTimeout() });
      await control.click({ timeout: remainingTimeout() });
    }, {
      after: async ({ remainingTimeout }) => {
        const response = await startFreshApplyResponsePromise;
        assert.equal(response.status(), 200, 'Connected DiagnosticReport Start Fresh Apply must return HTTP 200.');
        const body = requestBody(response.request());
        assert.equal(body.snapshotToken, beforeConnectedStartFresh.catalog.snapshotToken);
        assert.equal(body.expectedDraftVersion, beforeConnectedStartFresh.draftVersion);
        assert.equal(body.expectedDraftDigest, beforeConnectedStartFresh.draftDigest);
        assert.deepEqual(replacementApplyCommandMatches(body, startFreshAssessment.proposal), {
          ok: true, failures: [], command: body.commands[0],
        }, 'Start Fresh Apply must submit the exact reviewed REPLACE proposal as its sole command.');
        const applyRequests = actionRequests.slice(actionRequestCountBeforeStartFreshApply);
        const rootCommands = applyRequests.filter(entry => entry.kind === 'row-root-command');
        assert.equal(rootCommands.length, 1,
          'Start Fresh Apply must submit exactly one atomic root replacement command.');
        assert.deepEqual(replacementApplyCommandFromRequest(rootCommands[0].body).command?.rowChange,
          startFreshAssessment.proposal);
        assert.equal(replacementApplyRequestCount, replacementApplyCountBeforeStartFreshApply + 1,
          'Start Fresh Apply must add exactly one replacement command after its pre-click baseline.');
        await startFreshDialog.waitFor({ state: 'detached', timeout: remainingTimeout() });
        await visibleRows(oracle.diagnosticReportIDs, 'DiagnosticReport', startFreshCandidate.label);
      },
    });
  assert.equal(replacementApplyRequestCount, replacementApplyCountBeforeStartFresh + 1,
    'The connected Start Fresh path must add exactly one Apply after its reviewed REPLACE assessment.');
  const appliedStartFreshBuilder = await readBuilder();
  const appliedStartFreshDocument = documentForOutput(appliedStartFreshBuilder, outputId);
  assert.equal(appliedStartFreshDocument.output.id, outputId);
  assert.equal(appliedStartFreshDocument.output.title, tableTitle);
  assert.equal(appliedStartFreshDocument.rootResourceType, 'DiagnosticReport');
  assert.equal(appliedStartFreshDocument.rows.kind, 'RECORDS');
  assert.equal(appliedStartFreshDocument.columns.length, 1);
  const diagnosticReportIDColumn = idColumnFor(appliedStartFreshDocument, 'DiagnosticReport');
  assert.equal(diagnosticReportIDColumn.source.field.path, 'id');
  assert.equal(diagnosticReportIDColumn.source.field.projectionMode, 'VALUE');
  assert.equal(appliedStartFreshDocument.population, undefined);
  assert.equal((appliedStartFreshDocument.construction?.steps ?? []).length, 0);
  assert.equal((appliedStartFreshDocument.route?.children ?? []).length, 0,
    'Start Fresh must clear the ResearchStudy route and make DiagnosticReport the only root occurrence.');
  const appliedStartFreshRows = await visibleRows(oracle.diagnosticReportIDs, 'DiagnosticReport',
    diagnosticReportIDColumn.label);
  assert(isDeepStrictEqual(documentForOutput(appliedStartFreshBuilder, consumerOutputId), consumerDocument),
    'Connected Start Fresh must leave the dependent consumer document unchanged for human repair.');
  assert.deepEqual(workspaceTabIdentitySnapshot(appliedStartFreshBuilder), baselineTabs,
    'Connected Start Fresh must preserve every output/tab identity and order.');
  const appliedStartFreshSnapshot = {
    outputId,
    outputTitle: appliedStartFreshDocument.output.title,
    rootResourceType: appliedStartFreshDocument.rootResourceType,
    rowsKind: appliedStartFreshDocument.rows.kind,
    route: appliedStartFreshDocument.route,
    defaultIDColumn: diagnosticReportIDColumn,
    population: appliedStartFreshDocument.population ?? null,
    constructionSteps: appliedStartFreshDocument.construction?.steps ?? [],
    exactVisibleIDs: rowIDs(appliedStartFreshRows, 'DiagnosticReport', diagnosticReportIDColumn.label),
    candidateReceiptId: startFreshAssessment.candidateReceiptId,
    removedFeatureKeys: startFreshAssessment.proposal.removedFeatureKeys,
    consumerDocumentUnchanged: isDeepStrictEqual(
      documentForOutput(appliedStartFreshBuilder, consumerOutputId), consumerDocument,
    ),
    workspaceTabs: workspaceTabIdentitySnapshot(appliedStartFreshBuilder),
  };
  check('correctness', 'connected Start Fresh Apply preserves the output identity and saves exact DiagnosticReport defaults', true,
    appliedStartFreshSnapshot);
  await act('reload after connected Start Fresh', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        await visibleRows(oracle.diagnosticReportIDs, 'DiagnosticReport', diagnosticReportIDColumn.label);
        const reloaded = await readBuilder();
        const reloadedDocument = documentForOutput(reloaded, outputId);
        assert(isDeepStrictEqual(reloadedDocument, appliedStartFreshDocument),
          'Reload must preserve the exact DiagnosticReport default setup saved by Start Fresh.');
        assert(isDeepStrictEqual(documentForOutput(reloaded, consumerOutputId), consumerDocument),
          'Reload after Start Fresh must keep the dependent consumer document unchanged.');
        assert.deepEqual(workspaceTabIdentitySnapshot(reloaded), baselineTabs,
          'Reload after Start Fresh must preserve all workspace tab identities.');
      },
    });
  const finalStartFreshBuilder = await readBuilder();
  const finalStartFreshDocument = documentForOutput(finalStartFreshBuilder, outputId);
  assert.equal(finalStartFreshDocument.rootResourceType, 'DiagnosticReport');
  assert(isDeepStrictEqual(finalStartFreshDocument, appliedStartFreshDocument));
  check('persistence', 'Start Fresh DiagnosticReport ID rows and default setup survive reload', true, {
    outputId,
    outputTitle: finalStartFreshDocument.output.title,
    rootResourceType: finalStartFreshDocument.rootResourceType,
    route: finalStartFreshDocument.route,
    rows: finalStartFreshDocument.rows,
    columns: finalStartFreshDocument.columns,
    ids: oracle.diagnosticReportIDs,
    consumerDocumentUnchanged: true,
    tabs: workspaceTabIdentitySnapshot(finalStartFreshBuilder),
  });
  const finalOracle = readBasicRootReplacementOracle({ fixtureDir: target.fixtureDir });
  assert.deepEqual(finalOracle.source.files.map(file => file.sha256),
    oracle.source.files.map(file => file.sha256),
    'The independent Basic fixture bytes must remain unchanged through replacement and connected Start Fresh.');
  check('correctness', 'independent Basic fixture sources stayed unchanged through replacement and Start Fresh', true,
    { files: finalOracle.source.files, exactDiagnosticReportIDs: finalOracle.diagnosticReportIDs });

  report.lifecycle = {
    stages: [
      'fresh Basic Patient table with native birthDate VALUE field',
      'nondefault two-member Patient starting collection and active GTE birthDate filter',
      'native second Patient root and current-draft KEY_JOIN consumer of the source birthDate column',
      'disconnected ResearchStudy selected through the shared native Record type picker',
      'Review changes opened the replacement consequence dialog, which Cancel closed with the exact saved Patient source preserved',
      'same replacement reassessed and applied using the atomic root-replacement command',
      'replacement output identity/title/default ID rows verified after reload',
      'ResearchStudy→DiagnosticReport relationship option inspected after reload',
      'connected DiagnosticReport selected through the shared Record type picker and contextual Start fresh action',
      'Start Fresh reviewed the exact ResearchStudy root and ID reset consequences and DiagnosticReport ID rows',
      'connected Start Fresh applied and reloaded the same output with exact DiagnosticReport IDs',
    ],
    replacementAssessments: actionRequests.filter(entry => entry.kind === 'replacement-assessment').map(entry => entry.body),
    replacementApplyCount: replacementApplyRequestCount,
    finalOutputId: outputId,
    finalRoot: 'DiagnosticReport',
    exactFinalIDs: oracle.diagnosticReportIDs,
  };
};
