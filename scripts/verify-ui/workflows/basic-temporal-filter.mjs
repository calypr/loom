import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserURL } from './builder-url.mjs';
import { findRenderedBuilderHeaderIndex, waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { createTemporalFilterCapture } from '../helpers/temporal-filter-capture.mjs';
import { adaptTemporalFilterJourney } from '../helpers/temporal-filter-trace-adapter.mjs';
import { bindFixtureOracleToQaIsolation, fixtureProjectExplorerCollectionPath } from '../helpers/fixture-context.mjs';
import { TEMPORAL_FILTER_FORMAL_PROOF_ASSERTIONS } from '../helpers/temporal-filter-tla-adapter.mjs';
import {
  BASIC_TEMPORAL_FILTER_CASES,
  matchesBasicTemporalChoiceProposal,
  matchesBasicTemporalFilterCandidate,
  readBasicTemporalFilterOracle,
} from '../helpers/basic-temporal-filter-oracle.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const ACTION_TIMEOUT_MS = 5_000;
const previewSelector = '[data-testid="construction-preview"] [data-testid="preview-table-scroll"] [role="table"]';
const proposalPreviewSelector = '[data-testid="construction-proposal-preview"] table';
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const header = value => normalize(value).split('\n', 1)[0].toLowerCase();

const requestBody = request => {
  try { return request.postDataJSON(); } catch { return undefined; }
};

export const matchesTemporalExplorerCreateResponse = (response, { uiOrigin, explorerCollectionPath, title }) => {
  const request = response.request();
  const url = new URL(response.url());
  return request.method() === 'POST' && url.origin === uiOrigin &&
    url.pathname === explorerCollectionPath && requestBody(request)?.title === title;
};

const snapshotTable = async (locator, rowSelector = '[role="row"], tbody tr') => locator.evaluate((table, selectedRows) => ({
  headers: [...table.querySelectorAll('[role="columnheader"], thead th')]
    .map(cell => (cell.querySelector('span')?.textContent ?? cell.innerText ?? '').trim().split(/\r?\n/, 1)[0]),
  rows: [...table.querySelectorAll(selectedRows)]
    .filter(row => row.querySelector('[role="cell"], td'))
    .map(row => [...row.querySelectorAll('[role="cell"], td')].map(cell => String(cell.innerText ?? '').trim())),
}), rowSelector);

const columnForPath = (document, fieldPath) => {
  const matches = (document.columns ?? []).filter(column =>
    column.source?.kind === 'field' && column.source.field?.path === fieldPath);
  assert.equal(matches.length, 1, `Saved output must contain one direct ${fieldPath} source column.`);
  const column = matches[0];
  assert(column.columnId && column.label, `Saved ${fieldPath} column must retain its identity and label.`);
  return column;
};

const documentForOutput = (builder, outputId) => {
  const documents = builder.workspace?.documents;
  assert(Array.isArray(documents), 'Builder read must include saved workspace documents.');
  const matches = documents.filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Builder must retain exactly one document for output ${outputId}.`);
  return matches[0];
};

export const basicTemporalFilterWorkflow = async ({ page, report, check, action, caseName, networkObserver }, context) => {
  configureNativePage(page);
  const contract = BASIC_TEMPORAL_FILTER_CASES[caseName];
  assert(contract, `Unregistered Basic temporal filter case ${JSON.stringify(caseName)}.`);
  const { target, seed } = context;
  assert.equal(context.custom, false, 'The temporal case requires the normal isolated fixture loader.');
  assert.equal(seed?.fresh, true, 'Each temporal case requires a fresh Basic project and generation.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_') && target.fixtureGeneration && target.fixtureDir,
    'The temporal case must be bound to an owned fresh Basic fixture project/generation.');

  const oracle = readBasicTemporalFilterOracle({ fixtureDir: target.fixtureDir, caseName });
  const expectedAllIDs = [...oracle.ids];
  const expectedBefore = expectedAllIDs.map(id => {
    const row = oracle.rows.find(candidate => candidate.id === id);
    return [id, row.value];
  });
  const fixtureOracle = {
    path: oracle.path, sha256: oracle.sha256, project: target.fixtureProject,
    generation: target.fixtureGeneration, resourceType: contract.resourceType,
    fieldPath: contract.fieldPath, rows: oracle.rows,
  };
  check('correctness', `fresh ${contract.resourceType} temporal fixture matches the exact literal row oracle`,
    JSON.stringify(expectedBefore) === JSON.stringify(contract.rows.map(row => [row.id, row.value])),
    fixtureOracle);

  const act = (name, locator, perform, options = {}) => {
    const deadlineAt = performance.now() + ACTION_TIMEOUT_MS;
    const remainingTimeout = () => {
      const remaining = Math.floor(deadlineAt - performance.now());
      assert(remaining > 0, `${name} exhausted its ${ACTION_TIMEOUT_MS} ms action-to-render budget.`);
      return remaining;
    };
    const { after, ...actionOptions } = options;
    return action(name, locator,
      (control) => perform(control, { remainingTimeout }),
      {
        timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS, ...actionOptions,
        ...(after ? { after: () => after({ remainingTimeout }) } : {}),
      });
  };
  const project = target.fixtureProject;
  const browserExplorerCollectionPath = fixtureProjectExplorerCollectionPath(project);
  const directAPIExplorerCollectionPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const uiOrigin = new URL(target.uiUrl).origin;
  const title = `Verify ${context.runID.slice(-10)} ${contract.resourceType} ${contract.fieldPath} filter`;
  const capture = createTemporalFilterCapture({ page, networkObserver, initialMountEpoch: 0 });
  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  let reconcileCursor = networkObserver.requests.length;
  const createFromIndex = networkObserver.requests.length;
  const createRequest = capture.waitForRequest(entry => entry.method === 'POST' &&
    entry.path === browserExplorerCollectionPath && entry.request?.title === title, { fromIndex: createFromIndex });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await act('open a new disposable Explorer', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const explorerName = page.locator('#new-explorer-name');
  await act('name the temporal-filter Explorer', explorerName, () => explorerName.fill(title), {
    editable: true,
    after: async () => assert.equal(await explorerName.inputValue(), title),
  });
  const createResponsePromise = page.waitForResponse(response => matchesTemporalExplorerCreateResponse(response, {
    uiOrigin, explorerCollectionPath: browserExplorerCollectionPath, title,
  }), { timeout: ACTION_TIMEOUT_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await act('create the fresh temporal-filter Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createResponsePromise;
      assert.equal(response.status(), 201, `Native Explorer creation returned HTTP ${response.status()}.`);
      const responseBody = await response.json();
      const observerRequest = await createRequest;
      capture.associateResponseBody(observerRequest, responseBody);
      await page.waitForFunction(expectedTitle =>
        document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expectedTitle,
      title, { timeout: ACTION_TIMEOUT_MS });
      assert.notEqual(await page.getByRole('combobox', { name: 'Explorer' }).inputValue(), target.bootstrapExplorerId,
        'The native Explorer must differ from the fixture bootstrap Explorer.');
    },
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  report.target.explorer = explorer;
  const browserExplorerRoot = `${browserExplorerCollectionPath}/${encodeURIComponent(explorer)}`;
  const directAPIExplorerRoot = `${directAPIExplorerCollectionPath}/${encodeURIComponent(explorer)}`;
  const commandPath = `${browserExplorerRoot}/authoring/v2/commands`;
  const builderPath = `${directAPIExplorerRoot}/authoring/v2/builder`;
  const reconcilePath = `${browserExplorerRoot}/authoring/v2/reconcile`;
  const readBuilder = async () => {
    const started = capture.beginBuilderRead();
    const response = await fetch(`${target.apiUrl}${builderPath}`, { signal: AbortSignal.timeout(ACTION_TIMEOUT_MS) });
    let body;
    try { body = await response.json(); }
    catch (error) { throw new Error(`Builder read returned non-JSON HTTP ${response.status}: ${error.message}`); }
    capture.finishBuilderRead(started, response, body, {
      path: builderPath,
      project,
      explorerId: explorer,
    });
    assert(response.ok, `Builder read returned HTTP ${response.status}: ${JSON.stringify(body).slice(0, 600)}`);
    assert.equal(body.catalog?.generation, target.fixtureGeneration, 'Builder read must retain the exact isolated generation.');
    return body;
  };

  const collectReconcileEntries = async (fromIndex, { waitForFirst = false, settle = false } = {}) => {
    const matchesReconcile = entry => entry.method === 'POST' && entry.path === reconcilePath;
    if (waitForFirst) {
      await capture.waitForRequest(matchesReconcile, { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    }
    const entries = [];
    let cursor = fromIndex;
    for (;;) {
      const window = capture.requestWindow(cursor);
      cursor = window.endIndex;
      const matches = window.entries.filter(matchesReconcile);
      entries.push(...matches);
      if (!settle || matches.length === 0) break;
      await capture.settleRequests(matches);
    }
    return { entries, endIndex: cursor };
  };

  const recordStep = async (step, {
    reconcileFromIndex = reconcileCursor,
    waitForReconcile = false,
    relatedMutationEntries = [],
  } = {}) => {
    const collected = await collectReconcileEntries(reconcileFromIndex, {
      waitForFirst: waitForReconcile,
      settle: waitForReconcile,
    });
    const reconcileEntries = collected.entries;
    if (step.label === 'Reload') {
      assert(reconcileEntries.length > 0, 'Each real Builder reload must capture its actual reconcile receipt request.');
    }
    let recordedStep = step;
    if (waitForReconcile && reconcileEntries.length > 0 && step.domObservationIndex !== null &&
      step.domObservationIndex !== undefined) {
      recordedStep = {
        ...step,
        domObservationIndex: await capture.captureDOM({ resourceType: contract.resourceType }),
      };
    }
    const recorded = capture.recordStep({ ...recordedStep,
      relatedMutationCaptureIds: relatedMutationEntries.map(entry => entry.browserRequestId),
      reconcileCaptureIds: reconcileEntries.map(entry => entry.browserRequestId) });
    reconcileCursor = collected.endIndex;
    return recorded;
  };

  await readBuilder();
  const explorerCreatedReadIndex = capture.builderReads.length - 1;
  await recordStep({ label: 'ExplorerCreated', requestEntry: await createRequest,
    afterReadIndex: explorerCreatedReadIndex });
  report.target.qaIsolation = {
    project, generation: target.fixtureGeneration, fixtureDir: target.fixtureDir,
    bootstrapExplorerId: target.bootstrapExplorerId, explorer,
    fixtureRows: oracle.rows,
    mutations: [`native blank Explorer creation: ${explorer}`, `native ${contract.resourceType} table creation`,
      `native ${contract.fieldPath} column addition`, 'native FILTER proposal/apply/edit/remove'],
    cleanup: 'The harness retains this run-unique Basic fixture project and Explorer; filter removal restores the saved source table.',
  };
  bindFixtureOracleToQaIsolation(report, fixtureOracle);

  const tableName = page.locator('#first-table-name');
  await act(`name the ${contract.resourceType} table`, tableName, () => tableName.fill(`${contract.resourceType} temporal rows`), {
    editable: true,
    after: async () => assert.equal(await tableName.inputValue(), `${contract.resourceType} temporal rows`),
  });
  const waitSavedRows = async (expectedIDs, expectedOutputId, timeout = ACTION_TIMEOUT_MS) => {
    assert(expectedOutputId, 'The native Preview must identify its exact saved output before row comparison.');
    const idLabel = contract.resourceType === 'Patient' ? 'patient id' : 'observation id';
    await Promise.all([
      page.waitForFunction(({ outputId }) => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === outputId;
      }, { outputId: expectedOutputId }, { timeout }),
      waitForBuilderRenderedGrid(page, {
        tableSelector: previewSelector,
        expectedRows: expectedIDs.map(id => ({ [idLabel]: id })),
      }, timeout),
    ]);
    const snapshot = await snapshotTable(page.locator(previewSelector));
    const idIndex = findRenderedBuilderHeaderIndex(snapshot.headers, idLabel);
    assert.notEqual(idIndex, -1, `Rendered source Preview must show the ${contract.resourceType} ID column: ${JSON.stringify(snapshot.headers)}`);
    const ids = snapshot.rows.map(row => row[idIndex]).sort();
    assert.deepEqual(ids, [...expectedIDs].sort(), 'Rendered Preview must preserve the exact literal source record IDs.');
    return { ...snapshot, ids };
  };
  const chooseRoot = page.getByRole('button', { name: `Choose ${contract.resourceType} rows`, exact: true });
  let previewOutputId;
  const builderBeforeRoot = await readBuilder();
  const builderBeforeRootReadIndex = capture.builderReads.length - 1;
  const rootCommandFromIndex = networkObserver.requests.length;
  await act(`choose native ${contract.resourceType} rows`, chooseRoot, () => chooseRoot.click(), {
    after: async ({ remainingTimeout }) => {
      await page.waitForFunction(() => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return Boolean(document.querySelector('[data-testid="construction-action-add-columns"]') &&
          preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId);
      }, null, { timeout: remainingTimeout() });
      previewOutputId = await page.getByTestId('construction-preview').getAttribute('data-preview-output-id');
      await waitSavedRows(expectedAllIDs, previewOutputId, remainingTimeout());
    },
  });
  const builderAfterRoot = await readBuilder();
  const builderAfterRootReadIndex = capture.builderReads.length - 1;
  const rootDocument = builderAfterRoot.workspace?.documents?.find(document => document.rootResourceType === contract.resourceType);
  assert(rootDocument?.output?.id, `Fresh Explorer must contain one ${contract.resourceType} table output.`);
  const outputId = rootDocument.output.id;
  assert.equal(outputId, previewOutputId, 'The visible root Preview must belong to the saved root output.');
  const rootIDColumn = columnForPath(rootDocument, 'id');
  const rootGrid = await waitSavedRows(expectedAllIDs, outputId);
  const rootDomObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  const rootCommand = await capture.waitForRequest(entry => entry.method === 'POST' && entry.path === commandPath &&
    entry.request?.commands?.some(command => command.type === 'CREATE_TABLE'),
  { fromIndex: rootCommandFromIndex });
  const rootIDCommand = await capture.waitForRequest(entry => entry.method === 'POST' && entry.path === commandPath &&
    entry.request?.commands?.some(command => command.type === 'ADD_COLUMN' &&
      command.outputId === outputId && command.title === `${contract.resourceType} ID`),
  { fromIndex: rootCommand.index + 1 });
  assert(rootIDCommand.index > rootCommand.index,
    'Default root ID provisioning must be a distinct physical request after CREATE_TABLE.');
  await recordStep({ label: 'SourceTableCreated', requestEntry: rootCommand,
    beforeReadIndex: builderBeforeRootReadIndex, afterReadIndex: builderAfterRootReadIndex,
    domObservationIndex: rootDomObservationIndex }, { relatedMutationEntries: [rootIDCommand] });
  assert.equal(rootGrid.rows.length, expectedAllIDs.length, 'Root table must render every literal source row.');
  check('correctness', `native ${contract.resourceType} root Preview renders every exact source ID`, true,
    { outputId, rootColumnId: rootIDColumn.columnId, headers: rootGrid.headers, ids: rootGrid.ids });

  const addColumnsButton = page.getByRole('button', { name: 'Add columns: Bring more information into each row.', exact: true });
  await act('open Add columns for the temporal source field', addColumnsButton, () => addColumnsButton.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const fieldsTab = page.getByRole('tab', { name: 'Other fields', exact: true });
  await act('open Other fields for the temporal source field', fieldsTab, () => fieldsTab.click(), {
    after: async () => assert.equal(await fieldsTab.getAttribute('aria-selected'), 'true'),
  });
  const search = page.getByRole('searchbox', { name: 'Search dataset fields by name or type', exact: true });
  await act(`search ${contract.resourceType}.${contract.fieldPath}`, search, () => search.fill(contract.fieldPath), {
    editable: true,
    after: async () => assert.equal(await search.inputValue(), contract.fieldPath),
  });
  const searchButton = page.getByRole('button', { name: 'Search', exact: true });
  const fieldChoiceButton = page.getByRole('button', {
    name: `Select ${contract.resourceType}.${contract.fieldPath}`, exact: true,
  });
  await act(`show ${contract.fieldPath} field choice`, searchButton, () => searchButton.click(), {
    after: () => fieldChoiceButton.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  await act(`select ${contract.resourceType}.${contract.fieldPath}`, fieldChoiceButton, () => fieldChoiceButton.click(), {
    after: async () => assert.equal(await fieldChoiceButton.getAttribute('aria-pressed'), 'true'),
  });

  const addSelected = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  const builderBeforeFieldAdd = await readBuilder();
  const builderBeforeFieldAddReadIndex = capture.builderReads.length - 1;
  assert.equal(builderBeforeFieldAdd.draftDigest, builderAfterRoot.draftDigest,
    'Selecting a source field must leave the saved root draft unchanged.');
  const choiceProposalFromIndex = networkObserver.requests.length;
  let choiceProposalPromise;
  let applyFieldCommandPromise;
  let fieldChoiceProposal;
  let savedBuilder = builderAfterRoot;
  let savedDocument = rootDocument;
  let temporalColumn;
  await act(`add ${contract.fieldPath} as a native output column`, addSelected,
    async (_control, { remainingTimeout }) => {
      const choiceProposalPath = `${browserExplorerRoot}/authoring/v2/construction-choice-proposals`;
      choiceProposalPromise = page.waitForResponse(response => {
        const request = response.request();
        const url = new URL(response.url());
        return matchesBasicTemporalChoiceProposal({
          method: request.method(), origin: url.origin, path: url.pathname, body: requestBody(request),
        }, { origin: uiOrigin, path: choiceProposalPath, outputId, fieldLabel: contract.fieldLabel });
      }, { timeout: remainingTimeout() });
      applyFieldCommandPromise = page.waitForResponse(response => {
        const request = response.request();
        const url = new URL(response.url());
        return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === commandPath &&
          requestBody(request)?.commands?.some(command =>
            command.type === 'APPLY_CONSTRUCTION_CHOICE' && command.outputId === outputId);
      }, { timeout: remainingTimeout() });
      await addSelected.click({ timeout: remainingTimeout() });
    }, {
      after: async ({ remainingTimeout }) => {
        const proposalResponse = await choiceProposalPromise;
        assert.equal(proposalResponse.status(), 200,
          `Native field-choice proposal returned HTTP ${proposalResponse.status()}.`);
        fieldChoiceProposal = await proposalResponse.json();
        assert.equal(fieldChoiceProposal.outputId, outputId,
          'Field-choice response must target the exact native output.');
        assert.equal(fieldChoiceProposal.draftVersion, builderBeforeFieldAdd.draftVersion,
          'Field-choice response must use the exact pre-add Builder version.');
        assert.equal(fieldChoiceProposal.draftDigest, builderBeforeFieldAdd.draftDigest,
          'Field-choice response must use the exact pre-add Builder digest.');
        assert.equal(fieldChoiceProposal.previewStatus, 'READY',
          'Field-choice response must report its actual preview as ready.');
        assert.equal(fieldChoiceProposal.preview?.outputId, outputId,
          'Field-choice response preview must retain the exact native output.');
        assert.equal(fieldChoiceProposal.preview?.rowCount, oracle.rows.length,
          'Field-choice response must retain the exact source row count.');
        assert.equal(fieldChoiceProposal.constructionChoices?.length, 1,
          'Field-choice response must retain one actual selected source choice.');
        assert.equal(fieldChoiceProposal.constructionChoices[0]?.form, 'VALUE');
        assert.equal(fieldChoiceProposal.constructionChoices[0]?.title, contract.fieldLabel);
        assert(fieldChoiceProposal.constructionChoices[0]?.choiceId,
          'Field-choice response must retain the exact source choice identity.');
        assert(Array.isArray(fieldChoiceProposal.candidateColumnIds) &&
          fieldChoiceProposal.candidateColumnIds.length === 1,
        'The selected field response must identify exactly one candidate output column.');
        const proposedColumn = fieldChoiceProposal.preview.columns.find(column =>
          fieldChoiceProposal.candidateColumnIds.includes(column.column));
        assert.equal(proposedColumn?.sourceResourceType, contract.resourceType,
          'Field-choice response must identify the exact source resource type.');
        assert.equal(proposedColumn?.sourcePath, contract.fieldPath,
          'Field-choice response must identify the exact source field path.');
        assert.equal(proposedColumn?.label, contract.fieldLabel,
          'Field-choice response must retain the exact native field label.');
        assert(fieldChoiceProposal.preview.receiptId,
          'Field-choice response must retain its exact preview receipt.');

        const applyFieldResponse = await applyFieldCommandPromise;
        assert.equal(applyFieldResponse.status(), 200,
          `Applying the selected source field returned HTTP ${applyFieldResponse.status()}.`);
        const applyFieldResponseBody = await applyFieldResponse.json();
        assert.equal(applyFieldResponseBody.commandId, fieldChoiceProposal.commandId,
          'The auto-apply command must belong to the captured field-choice request.');
        assert.equal(applyFieldResponseBody.draftVersion, builderBeforeFieldAdd.draftVersion + 1,
          'Applying the field must advance the saved draft by exactly one version.');
        assert.equal(applyFieldResponseBody.draftDigest, fieldChoiceProposal.candidateWorkspaceDigest,
          'Auto-apply must save the exact workspace produced by the captured field-choice response.');
        assert.equal(applyFieldResponseBody.results?.length, 1,
          'Auto-apply response must retain exactly one result for the selected field.');
        assert(applyFieldResponseBody.results[0].outputId === outputId &&
          applyFieldResponseBody.results[0].type === 'COLUMN_ADDED' &&
          fieldChoiceProposal.candidateColumnIds.includes(applyFieldResponseBody.results[0].column),
        'Auto-apply response must confirm the exact candidate column was added.');

        await page.waitForFunction(({ expectedOutputId, expectedDraftVersion }) => {
          const preview = document.querySelector('[data-testid="construction-preview"]');
          return Boolean(!document.querySelector('[aria-label="Add columns editor"]') &&
            preview?.dataset.previewStatus === 'ready' &&
            preview.dataset.previewOutputId === expectedOutputId &&
            Number(preview.dataset.currentDraftVersion) === expectedDraftVersion);
        }, { expectedOutputId: outputId, expectedDraftVersion: applyFieldResponseBody.draftVersion },
        { timeout: remainingTimeout() });
        await page.getByTestId('construction-action-keep-rows').waitFor({
          state: 'visible', timeout: remainingTimeout(),
        });
        savedBuilder = await readBuilder();
        assert.equal(savedBuilder.draftVersion, applyFieldResponseBody.draftVersion,
          'Post-settlement Builder read must observe the field-add command version.');
        assert.equal(savedBuilder.draftDigest, applyFieldResponseBody.draftDigest,
          'Post-settlement Builder read must observe the exact field-add command digest.');
        savedDocument = documentForOutput(savedBuilder, outputId);
        temporalColumn = columnForPath(savedDocument, contract.fieldPath);
        assert(fieldChoiceProposal.candidateColumnIds.includes(temporalColumn.column),
          'The persisted source field must retain the candidate column identity returned by the response.');
        if (contract.filterValueKind === 'DATE') {
          assert.equal(temporalColumn.logicalType, 'date', 'Patient.birthDate must persist as a date column.');
        }
        const savedGrid = await waitSavedRows(expectedAllIDs, outputId, remainingTimeout());
        const savedIDIndex = findRenderedBuilderHeaderIndex(savedGrid.headers, `${contract.resourceType.toLowerCase()} id`);
        const savedValueIndex = findRenderedBuilderHeaderIndex(savedGrid.headers, contract.fieldLabel);
        assert(savedIDIndex >= 0 && savedValueIndex >= 0,
          `Saved table must visibly render the exact ID and ${contract.fieldLabel} columns.`);
        const actualRows = savedGrid.rows.map(row => [row[savedIDIndex], row[savedValueIndex]])
          .sort((left, right) => left[0].localeCompare(right[0]));
        assert.deepEqual(actualRows, expectedBefore,
          'Saved table must show the exact literal source values and record identities.');
      },
    });
  const choiceProposalRequests = capture.requestsSince(choiceProposalFromIndex, entry =>
    entry.method === 'POST' && entry.path.endsWith('/construction-choice-proposals'));
  await capture.settleRequests(choiceProposalRequests);
  const fieldChoiceProposalEntryMatches = choiceProposalRequests.filter(entry =>
    entry.request?.commandId === fieldChoiceProposal.commandId &&
    entry.response?.commandId === fieldChoiceProposal.commandId &&
    entry.request?.outputId === outputId && entry.response?.outputId === outputId &&
    entry.response?.previewStatus === 'READY' &&
    entry.request?.constructionChoices?.some(choice => choice.form === 'VALUE' &&
      choice.title === contract.fieldLabel &&
      choice.choiceId === fieldChoiceProposal.constructionChoices?.[0]?.choiceId));
  assert.equal(fieldChoiceProposalEntryMatches.length, 1,
    'Field-choice response must bind one actual observer request with its own command ID and field choice.');
  const fieldChoiceProposalEntry = fieldChoiceProposalEntryMatches[0];
  assert.equal(fieldChoiceProposalEntry.request.expectedDraftVersion, builderBeforeFieldAdd.draftVersion,
    'Field-choice request must retain its exact pre-add Builder version.');
  assert.equal(fieldChoiceProposalEntry.request.expectedDraftDigest, builderBeforeFieldAdd.draftDigest,
    'Field-choice request must retain its exact pre-add Builder digest.');
  check('correctness', `native ${contract.fieldPath} choice response is ready for the exact output`, true,
    { outputId, commandId: fieldChoiceProposal.commandId, candidateColumnIds: fieldChoiceProposal.candidateColumnIds,
      previewReceiptId: fieldChoiceProposal.preview.receiptId, rowCount: fieldChoiceProposal.preview.rowCount });
  check('correctness', `native ${contract.fieldPath} column keeps exact source values and identities`, true,
    { outputId, columnId: temporalColumn.columnId, label: temporalColumn.label,
      type: temporalColumn.logicalType, rows: expectedBefore });

  const applyFieldEntries = capture.requestsSince(choiceProposalFromIndex, entry => entry.method === 'POST' &&
    entry.path === commandPath && entry.request?.commands?.some(command =>
      command.type === 'APPLY_CONSTRUCTION_CHOICE' && command.outputId === outputId));
  await capture.settleRequests(applyFieldEntries);
  assert.equal(applyFieldEntries.length, 1, 'Adding the field must make one captured native choice command.');
  const applyFieldEntry = applyFieldEntries[0];
  assert.equal(applyFieldEntry.request.commands.length, 1,
    'The field-add command must not bundle an unmodeled second operation.');
  const appliedChoice = applyFieldEntry.request.commands.find(command =>
    command.type === 'APPLY_CONSTRUCTION_CHOICE' && command.outputId === outputId);
  assert.equal(fieldChoiceProposalEntry.request.constructionChoices.length, 1,
    'The captured request must contain exactly one source field choice.');
  assert.equal(applyFieldEntry.request.commandId, fieldChoiceProposal.commandId,
    'The captured apply command must retain the proposal request command ID.');
  assert.equal(applyFieldEntry.request.expectedDraftVersion, builderBeforeFieldAdd.draftVersion,
    'Apply command must use the exact pre-add Builder version.');
  assert.equal(applyFieldEntry.request.expectedDraftDigest, builderBeforeFieldAdd.draftDigest,
    'Apply command must use the exact pre-add Builder digest.');
  assert.equal(appliedChoice.constructionChoice.choiceId,
    fieldChoiceProposalEntry.request.constructionChoices[0].choiceId,
    'The captured apply command must consume the exact selected field choice.');
  assert.equal(applyFieldEntry.response?.draftDigest, savedBuilder.draftDigest,
    'Captured auto-apply response must bind the exact post-settlement Builder digest.');
  const appliedFieldDomObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'TemporalColumnApplied', requestEntry: applyFieldEntry,
    proposalCaptureIds: choiceProposalRequests.map(entry => entry.browserRequestId),
    beforeReadIndex: builderBeforeFieldAddReadIndex, afterReadIndex: capture.builderReads.length - 1,
    domObservationIndex: appliedFieldDomObservationIndex });

  const readFilter = async expected => {
    const current = await readBuilder();
    const readIndex = capture.builderReads.length - 1;
    const document = documentForOutput(current, outputId);
    const filters = (document.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
    assert.equal(filters.length, expected ? 1 : 0, 'Saved output must contain exactly the expected number of row filters.');
    if (!expected) return { builder: current, readIndex, document, filter: undefined };
    const step = filters[0];
    assert.equal(step.operation.filter.columnId, temporalColumn.columnId, 'Saved FILTER must bind the exact temporal source column.');
    assert.equal(step.operation.filter.operator, expected.operator, 'Saved FILTER must retain the exact selected temporal comparison.');
    assert.deepEqual(step.operation.filter.values, [expected.savedValue], 'Saved FILTER must retain the exact typed temporal boundary.');
    return { builder: current, readIndex, document, filter: step };
  };
  const candidateIDs = async (expectedIDs, proposalId, remainingTimeout) => {
    await page.waitForFunction(({ expectedProposalId, expectedOutputId }) => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      const ready = document.querySelector('[data-testid="construction-proposal-ready"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return Boolean(panel?.dataset.proposalStatus === 'ready' &&
        panel.dataset.proposalId === expectedProposalId && ready &&
        preview?.dataset.previewStatus === 'ready' &&
        preview.dataset.previewReceiptId === expectedProposalId &&
        preview.dataset.previewOutputId === expectedOutputId);
    }, { expectedProposalId: proposalId, expectedOutputId: outputId }, { timeout: remainingTimeout() });
    const candidate = await snapshotTable(page.locator(proposalPreviewSelector), '[data-testid="construction-proposal-preview-row"]');
    if (expectedIDs.length === 0) {
      assert.deepEqual(candidate.rows, [], 'Exact-boundary proposal must visibly report zero matching rows.');
      assert.match(await page.getByTestId('construction-proposal-preview').innerText(), /No rows matched this proposal\./,
        'Empty temporal proposal must show the native no-matching-rows explanation.');
      return [];
    }
    const idIndex = candidate.headers.findIndex(value => header(value) === `${contract.resourceType.toLowerCase()} id`);
    assert(idIndex >= 0, 'Temporal proposal Preview must visibly retain the root identity column.');
    const actualIDs = candidate.rows.map(row => row[idIndex]).sort();
    assert.deepEqual(actualIDs, [...expectedIDs].sort(), 'Temporal proposal must match the exact literal record identities.');
    return actualIDs;
  };
  const openFilterEditor = async (label) => {
    const filterButton = page.getByTestId('construction-action-keep-rows');
    const proposalCaptureFromIndex = networkObserver.requests.length;
    await act(label, filterButton, () => filterButton.click(), {
      after: () => page.getByTestId('construction-filter-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    });
    return proposalCaptureFromIndex;
  };
  const filterInputValue = async filter => {
    if (filter.inputValue !== undefined) return filter.inputValue;
    assert(filter.boundaryInstant, 'A dateTime filter must declare its independent UTC boundary instant.');
    return page.evaluate(instant => {
      const utc = new Date(instant);
      const local = new Date(utc.getTime() - utc.getTimezoneOffset() * 60_000);
      return local.toISOString().slice(0, 16);
    }, filter.boundaryInstant);
  };
  const setFilter = async ({ operator, inputValue, initial, expectedIDs, requestValue, savedValue, proposalCaptureFromIndex }) => {
    const proposalStartIndex = proposalCaptureFromIndex ?? networkObserver.requests.length;
    const column = page.getByRole('combobox', { name: 'Column', exact: true });
    if (initial) {
      await act(`choose ${contract.fieldPath} for the row filter`, column,
        () => column.selectOption(temporalColumn.columnId), {
          after: async () => assert.equal(await column.inputValue(), temporalColumn.columnId),
        });
    } else {
      assert.equal(await column.inputValue(), temporalColumn.columnId,
        'Editing the saved filter must retain its exact temporal source column.');
    }
    const condition = page.getByRole('combobox', { name: 'Condition', exact: true });
    const supportedOperators = await condition.locator('option').evaluateAll(options => options.map(option => option.value));
    assert(['GT', 'GTE', 'LT', 'LTE'].every(value => supportedOperators.includes(value)),
      `Temporal filter must expose the exact four comparison operators: ${JSON.stringify(supportedOperators)}`);
    const valueInput = page.getByLabel('Value', { exact: true });
    assert.equal(await valueInput.getAttribute('type'), contract.inputType,
      `${contract.fieldPath} must use a native ${contract.inputType} input.`);
    const currentValue = await valueInput.inputValue();
    let proposal;
    await act(`set the ${contract.filterValueKind} ${operator} comparison`, condition,
      async (_control, { remainingTimeout }) => {
        const currentProposalResponse = page.waitForResponse(response => {
          const request = response.request();
          const url = new URL(response.url());
          return request.method() === 'POST' && url.origin === uiOrigin &&
            url.pathname === `${browserExplorerRoot}/authoring/v2/construction-proposals` &&
            matchesBasicTemporalFilterCandidate(requestBody(request), {
              outputId, columnId: temporalColumn.columnId, operator, savedValue: requestValue ?? savedValue,
            });
        }, { timeout: remainingTimeout() });
        if (!initial && currentValue !== inputValue) {
          await valueInput.fill(inputValue, { timeout: remainingTimeout() });
        }
        await condition.selectOption(operator, { timeout: remainingTimeout() });
        if (initial && currentValue !== inputValue) {
          await valueInput.fill(inputValue, { timeout: remainingTimeout() });
        }
        const response = await currentProposalResponse;
        assert.equal(response.status(), 200, `Current ${contract.filterValueKind} proposal returned HTTP ${response.status()}.`);
        proposal = await response.json();
      }, {
        after: async ({ remainingTimeout }) => {
          assert.equal(await condition.inputValue(), operator,
            'The filter must retain the exact requested comparison operator.');
          assert.equal(await valueInput.inputValue(), inputValue,
            'The temporal input must retain the exact literal boundary value.');
          assert.equal(proposal.outputId, outputId, 'Current temporal proposal must retain the exact output identity.');
          assert.equal(proposal.previewStatus, 'READY', 'Current temporal proposal must be ready before row assertions.');
          assert.equal(proposal.preview?.receiptId, proposal.proposalId,
            'Current temporal proposal must bind its preview to its exact proposal receipt.');
          assert.equal(proposal.preview?.outputId, outputId,
            'Current temporal proposal preview must retain the exact output identity.');
          assert(matchesBasicTemporalFilterCandidate(proposal, {
            outputId, columnId: temporalColumn.columnId, operator, savedValue,
          }), 'Current temporal proposal response must echo the exact typed filter predicate.');
          assert(proposal.proposalId, 'Current temporal proposal must expose its exact preview receipt.');
          await candidateIDs(expectedIDs, proposal.proposalId, remainingTimeout);
        },
      });
    return { condition, valueInput, supportedOperators, proposal, proposalStartIndex, operator,
      requestValue: requestValue ?? savedValue };
  };

  const recordFilterProposal = async ({ result, intent, beforeReadIndex }) => {
    const proposalEntries = capture.requestsSince(result.proposalStartIndex, entry =>
      entry.method === 'POST' && entry.path.endsWith('/construction-proposals'));
    await capture.settleRequests(proposalEntries);
    const current = proposalEntries.filter(entry => entry.response?.proposalId === result.proposal.proposalId &&
      matchesBasicTemporalFilterCandidate(entry.request, {
        outputId, columnId: temporalColumn.columnId, operator: result.operator, savedValue: result.requestValue,
      }));
    assert.equal(current.length, 1, 'Ready temporal proposal must bind its actual observer request/response capture ID.');
    const requestEntry = current[0];
    const domObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
    const proposalCaptureIds = proposalEntries.map(entry => entry.browserRequestId);
    const step = await recordStep({ label: `FilterProposed(${intent})`, requestEntry, proposalCaptureIds,
      beforeReadIndex, domObservationIndex });
    return { step, requestEntry, proposalCaptureIds, domObservationIndex };
  };

  const applyFilterProposal = async ({ expected, beforeExpected, label, intent, proposalId }) => {
    const before = await readFilter(beforeExpected);
    const beforeReadIndex = before.readIndex;
    const beforeDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
    const applyFromIndex = networkObserver.requests.length;
    const apply = page.getByTestId('construction-apply-proposal');
    let after;
    await act(label, apply, () => apply.click(), {
      after: async () => {
        await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
        const grid = await waitSavedRows(expected.ids, outputId);
        after = await readFilter(expected);
        assert.deepEqual(after.filter.operation.filter.values, [expected.savedValue]);
        return grid;
      },
    });
    const requestEntry = await capture.waitForRequest(entry => entry.method === 'POST' && entry.path === commandPath &&
      entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' &&
        command.proposalId === proposalId), { fromIndex: applyFromIndex });
    const domObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
    await recordStep({ label: `FilterApplied(${intent})`, requestEntry,
      beforeReadIndex, afterReadIndex: after.readIndex, domObservationIndex,
      proposalCaptureIds: [] });
    return { before, after, requestEntry, beforeDOMObservationIndex, domObservationIndex };
  };

  const beforeFilter = await readBuilder();
  const beforeFilterReadIndex = capture.builderReads.length - 1;
  const initial = contract.initial;
  const initialInputValue = await filterInputValue(initial);
  const firstProposalCaptureFromIndex = await openFilterEditor(`open Filter rows for ${contract.fieldPath}`);
  const initialControls = await setFilter({ ...initial, inputValue: initialInputValue, initial: true, expectedIDs: initial.ids,
    proposalCaptureFromIndex: firstProposalCaptureFromIndex });
  await recordFilterProposal({ result: initialControls, intent: 'create',
    beforeReadIndex: beforeFilterReadIndex });
  check('correctness', `native ${contract.inputType} filter exposes exact ${contract.filterValueKind} comparison controls`,
    initialControls.supportedOperators.includes('GT') && initialControls.supportedOperators.includes('GTE') &&
      initialControls.supportedOperators.includes('LT') && initialControls.supportedOperators.includes('LTE') &&
      await initialControls.valueInput.getAttribute('type') === contract.inputType,
    { fieldPath: contract.fieldPath, filterValueKind: contract.filterValueKind,
      inputType: await initialControls.valueInput.getAttribute('type'), operators: initialControls.supportedOperators });
  const firstProposalIDs = initial.ids;
  const cancel = page.getByTestId('construction-cancel-proposal');
  let afterCancel;
  await act('Cancel the first temporal filter proposal', cancel, () => cancel.click(), {
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      const grid = await waitSavedRows(expectedAllIDs, outputId);
      afterCancel = await readFilter(undefined);
      assert.equal(afterCancel.builder.draftVersion, beforeFilter.draftVersion, 'Cancel must preserve the saved draft version.');
      assert.equal(afterCancel.builder.draftDigest, beforeFilter.draftDigest, 'Cancel must preserve the saved draft digest.');
      assert.deepEqual(afterCancel.document, savedDocument, 'Cancel must leave the authored source table unchanged.');
      return grid;
    },
  });
  const cancelDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'FilterCancelled', beforeReadIndex: beforeFilterReadIndex,
    afterReadIndex: afterCancel.readIndex, domObservationIndex: cancelDOMObservationIndex });
  check('persistence', `Cancel leaves the exact unfiltered ${contract.resourceType} source rows`, true,
    { expectedIDs: expectedAllIDs, canceledProposalIDs: firstProposalIDs, draftVersion: beforeFilter.draftVersion,
      draftDigest: beforeFilter.draftDigest });
  const cancelReloadBeforeReadIndex = afterCancel.readIndex;
  const cancelReloadReconcileFromIndex = reconcileCursor;
  let cancelReloadAfter;
  await act('reload after temporal-filter Cancel', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        const grid = await waitSavedRows(expectedAllIDs, outputId);
        cancelReloadAfter = await readFilter(undefined);
        assert.equal(cancelReloadAfter.builder.draftDigest, beforeFilter.draftDigest,
          'Reload after Cancel must retain the exact unchanged draft.');
        return grid;
      },
    });
  const cancelReloadDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'Reload', beforeReadIndex: cancelReloadBeforeReadIndex,
    afterReadIndex: cancelReloadAfter.readIndex, domObservationIndex: cancelReloadDOMObservationIndex },
  { reconcileFromIndex: cancelReloadReconcileFromIndex, waitForReconcile: true });

  const secondProposalCaptureFromIndex = await openFilterEditor(`reopen Filter rows for ${contract.fieldPath}`);
  const secondInitialControls = await setFilter({ ...initial, inputValue: initialInputValue, initial: true,
    expectedIDs: initial.ids, proposalCaptureFromIndex: secondProposalCaptureFromIndex });
  const secondFilterProposal = await recordFilterProposal({ result: secondInitialControls, intent: 'create',
    beforeReadIndex: cancelReloadAfter.readIndex });
  const createdFilterApply = await applyFilterProposal({ expected: initial, beforeExpected: undefined, intent: 'create',
    proposalId: secondFilterProposal.requestEntry.response.proposalId,
    label: `Apply the exact ${contract.filterValueKind} ${initial.operator} filter` });
  check('correctness', `Apply saves exact ${contract.filterValueKind} values and matching ${contract.resourceType} IDs`, true,
    { filter: initial, expectedIDs: initial.ids, outputId, columnId: temporalColumn.columnId });

  let createReloadAfter;
  const createReloadReconcileFromIndex = reconcileCursor;
  await act('reload the applied temporal filter', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        const grid = await waitSavedRows(initial.ids, outputId);
        const saved = await readFilter(initial);
        assert(saved.filter.id, 'Reload must retain a stable filter-step identity.');
        createReloadAfter = saved;
        return grid;
      },
    });
  const createReloadDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'Reload', beforeReadIndex: createdFilterApply.after.readIndex,
    afterReadIndex: createReloadAfter.readIndex, domObservationIndex: createReloadDOMObservationIndex },
  { reconcileFromIndex: createReloadReconcileFromIndex, waitForReconcile: true });
  const savedState = await readFilter(initial);
  const stepID = savedState.filter.id;
  const historyStep = page.getByTestId(`construction-history-step-${stepID}`);
  await act('select the saved temporal filter step', historyStep, () => historyStep.click(), {
    after: () => page.getByTestId(`construction-edit-step-${stepID}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const editStep = page.getByTestId(`construction-edit-step-${stepID}`);
  const editOpenedFromIndex = networkObserver.requests.length;
  await act('edit the saved temporal filter', editStep, () => editStep.click(), {
    after: () => page.getByTestId('construction-filter-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  const editOpenedProposals = capture.requestsSince(editOpenedFromIndex, entry => entry.method === 'POST' &&
    entry.path.endsWith('/construction-proposals'));
  await capture.settleRequests(editOpenedProposals);
  const editOpenedDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'FilterEditOpened', beforeReadIndex: savedState.readIndex,
    proposalCaptureIds: editOpenedProposals.map(entry => entry.browserRequestId),
    domObservationIndex: editOpenedDOMObservationIndex });
  const edited = contract.edited;
  const editedInputValue = await filterInputValue(edited);
  const editedControls = await setFilter({ ...edited, inputValue: editedInputValue, initial: false, expectedIDs: edited.ids });
  const editedFilterProposal = await recordFilterProposal({ result: editedControls, intent: 'edit',
    beforeReadIndex: savedState.readIndex });
  check('correctness', `editing preserves the saved ${contract.fieldPath} source and exposes typed ${contract.inputType} controls`,
    await page.getByRole('combobox', { name: 'Column', exact: true }).inputValue() === temporalColumn.columnId &&
      await editedControls.valueInput.getAttribute('type') === contract.inputType,
    { stepID, columnId: temporalColumn.columnId, inputType: await editedControls.valueInput.getAttribute('type') });
  const editedFilterApply = await applyFilterProposal({ expected: edited, beforeExpected: initial, intent: 'edit',
    proposalId: editedFilterProposal.requestEntry.response.proposalId,
    label: `Apply the edited ${contract.filterValueKind} ${edited.operator} condition` });
  let editReloadAfter;
  const editReloadReconcileFromIndex = reconcileCursor;
  await act('reload the edited temporal filter', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        const grid = await waitSavedRows(edited.ids, outputId);
        const saved = await readFilter(edited);
        assert.equal(saved.filter.id, stepID, 'Editing must preserve the saved filter-step identity.');
        editReloadAfter = saved;
        return grid;
      },
    });
  const editReloadDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'Reload', beforeReadIndex: editedFilterApply.after.readIndex,
    afterReadIndex: editReloadAfter.readIndex, domObservationIndex: editReloadDOMObservationIndex },
  { reconcileFromIndex: editReloadReconcileFromIndex, waitForReconcile: true });
  check('persistence', `edited ${contract.filterValueKind} condition and exact rows survive reload`, true,
    { stepID, filter: edited, expectedIDs: edited.ids });

  await act('select the edited temporal filter for removal', page.getByTestId(`construction-history-step-${stepID}`),
    () => page.getByTestId(`construction-history-step-${stepID}`).click(), {
      after: () => page.getByTestId(`construction-remove-step-${stepID}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    });
  const removeStep = page.getByTestId(`construction-remove-step-${stepID}`);
  const removalProposalFromIndex = networkObserver.requests.length;
  await act('preview removal of the saved temporal filter', removeStep, () => removeStep.click(), {
    after: async () => {
      await page.getByTestId('construction-proposal-ready').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      const proposal = await snapshotTable(page.locator(proposalPreviewSelector));
      const idIndex = proposal.headers.findIndex(value => header(value) === `${contract.resourceType.toLowerCase()} id`);
      assert(idIndex >= 0);
      const ids = proposal.rows.map(row => row[idIndex]).sort();
      assert.deepEqual(ids, expectedAllIDs, 'Removing FILTER must preview all exact source identities.');
    },
  });
  const removalProposalEntries = capture.requestsSince(removalProposalFromIndex, entry =>
    entry.method === 'POST' && entry.path.endsWith('/construction-proposals'));
  await capture.settleRequests(removalProposalEntries);
  const removalProposalDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  const removalProposalDOM = capture.domObservations[removalProposalDOMObservationIndex];
  const removalProposalMatches = removalProposalEntries.filter(entry => entry.response?.proposalId === removalProposalDOM.proposalId &&
    entry.response?.previewStatus === 'READY' && entry.request?.outputId === outputId);
  assert.equal(removalProposalMatches.length, 1, 'Removing FILTER must bind its actual ready proposal response capture ID.');
  const removalProposalEntry = removalProposalMatches[0];
  await recordStep({ label: 'FilterProposed(remove)', requestEntry: removalProposalEntry,
    proposalCaptureIds: removalProposalEntries.map(entry => entry.browserRequestId),
    beforeReadIndex: editReloadAfter.readIndex, domObservationIndex: removalProposalDOMObservationIndex });

  const builderBeforeRemoval = await readFilter(edited);
  const removalFromIndex = networkObserver.requests.length;
  const applyRemoval = page.getByRole('button', { name: 'Apply removal', exact: true });
  let removalAfter;
  await act('apply removal and restore the unfiltered source rows', applyRemoval, () => applyRemoval.click(), {
    after: async () => {
      await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      const grid = await waitSavedRows(expectedAllIDs, outputId);
      removalAfter = await readFilter(undefined);
      assert.deepEqual(removalAfter.document.columns, savedDocument.columns,
        'Removing FILTER must preserve the exact source columns and stable temporal-column identity.');
      return grid;
    },
  });
  const removalRequest = await capture.waitForRequest(entry => entry.method === 'POST' && entry.path === commandPath &&
    entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' &&
      command.proposalId === removalProposalEntry.response.proposalId), { fromIndex: removalFromIndex });
  const removalDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'FilterRemoved', requestEntry: removalRequest,
    beforeReadIndex: builderBeforeRemoval.readIndex, afterReadIndex: removalAfter.readIndex,
    domObservationIndex: removalDOMObservationIndex });

  let finalReloadAfter;
  const finalReloadReconcileFromIndex = reconcileCursor;
  await act('reload after temporal-filter removal', page.locator('body'),
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        const grid = await waitSavedRows(expectedAllIDs, outputId);
        finalReloadAfter = await readFilter(undefined);
        assert.deepEqual(finalReloadAfter.document.columns, savedDocument.columns,
          'Final reload must retain the original source projection and temporal column.');
        return grid;
      },
    });
  const finalReloadDOMObservationIndex = await capture.captureDOM({ resourceType: contract.resourceType });
  await recordStep({ label: 'Reload', beforeReadIndex: removalAfter.readIndex,
    afterReadIndex: finalReloadAfter.readIndex, domObservationIndex: finalReloadDOMObservationIndex },
  { reconcileFromIndex: finalReloadReconcileFromIndex, waitForReconcile: true });
  check('persistence', `removing ${contract.filterValueKind} filter restores exact source rows after reload`, true,
    { expectedIDs: expectedAllIDs, sourceColumns: savedDocument.columns, outputId });

  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: target.sourceRoot, encoding: 'utf8' }).trim();
  const pendingReadScope = {
    uiOrigin,
    project,
    explorerId: explorer,
    browserExplorerRoot,
    outputId,
  };
  const captureMetadata = {
    caseName,
    sourceCommit,
    sourceFingerprint: context.sourceAtStart?.fingerprint ?? report.target.sourceFingerprint ?? null,
    project,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorerId: explorer,
    uiOrigin,
    browserExplorerRoot,
    pendingReadScope,
    resourceType: contract.resourceType,
    fieldPath: contract.fieldPath,
    fixturePath: oracle.path,
    fixtureSha256: oracle.sha256,
    rows: oracle.rows,
    ids: oracle.ids,
    outputId,
    temporalColumnId: temporalColumn.columnId,
  };
  const captureCheckpoint = capture.checkpoint(captureMetadata);
  report.temporalFilterCaptureCheckpoint = captureCheckpoint;
  try {
    report.temporalFilterJourney = await capture.finish({
      ...captureMetadata,
      checkpointId: captureCheckpoint.checkpointId,
    });
  } catch (error) {
    const failureCheckpoint = capture.checkpoint(captureMetadata);
    report.temporalFilterCaptureFailure = {
      checkpointId: failureCheckpoint.checkpointId,
      observedAtEpochMs: Date.now(),
      name: error?.name ?? 'Error',
      message: String(error?.message ?? error),
    };
    report.temporalFilterCaptureFailureCheckpoint = failureCheckpoint;
    throw error;
  }
  report.journeyTrace = adaptTemporalFilterJourney(report.temporalFilterJourney);

  const { writeAndReplayBuilderJourneyTrace } = await import('../helpers/builder-journey-trace.mjs');
  const formalProof = writeAndReplayBuilderJourneyTrace({
    scenarioID: report.scenario,
    caseName,
    journeyTrace: report.journeyTrace,
    report,
    fixture: {
      project,
      generation: target.fixtureGeneration,
      explorer,
      outputId,
      fixtureIDs: [...oracle.ids],
    },
    repositoryRoot,
  });
  check('correctness', TEMPORAL_FILTER_FORMAL_PROOF_ASSERTIONS[caseName],
    formalProof.status === 'pass' && formalProof.replayStatus === 'pass' && formalProof.selfTestStatus === 'pass',
    formalProof);
};
