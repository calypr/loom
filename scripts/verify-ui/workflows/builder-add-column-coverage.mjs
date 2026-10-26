import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { readBasicAddColumnCoverageOracle } from '../helpers/basic-add-column-coverage-oracle.mjs';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';

const ACTION_TIMEOUT_MS = 5_000;

export const requireUniqueVisibleTarget = async locator => {
  await locator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const count = await locator.count();
  assert.equal(count, 1, `Expected one native UI target for ${locator.toString()}, found ${count}`);
  return locator;
};

const requestBody = request => {
  try { return request.postDataJSON(); } catch { return undefined; }
};

const commandHas = (request, type) => requestBody(request)?.commands?.some(command => command.type === type) === true;

const waitForPreview = async (page, expectedRows, expectedColumns) => {
  await page.waitForFunction(({ expectedRows, expectedColumns }) => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(expectedRows + 1) &&
      table?.getAttribute('aria-colcount') === String(expectedColumns) &&
      !document.body.innerText.includes('Loading your table…') &&
      !document.body.innerText.includes('Preview failed:');
  }, { expectedRows, expectedColumns }, { timeout: ACTION_TIMEOUT_MS });
};

const previewSnapshot = async page => page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
  rowCount: Number(table.getAttribute('aria-rowcount')) - 1,
  columnCount: Number(table.getAttribute('aria-colcount')),
  headers: [...table.querySelectorAll('[role="columnheader"]')].map(header => header.textContent?.trim() ?? ''),
  rows: [...table.querySelectorAll('[role="row"]')].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
}));

const headerIndex = (headers, label) => {
  const matches = headers.flatMap((header, index) => header === label ? [index] : []);
  assert.equal(matches.length, 1, `Expected one ${JSON.stringify(label)} preview header, found ${JSON.stringify(headers)}`);
  return matches[0];
};

const savedRowsByObservationID = (snapshot, valueLabel) => {
  const idIndex = headerIndex(snapshot.headers, 'Observation ID');
  const valueIndex = headerIndex(snapshot.headers, valueLabel);
  assert(snapshot.rows.every(row => row.length === snapshot.headers.length),
    `Rendered preview has an incomplete row: ${JSON.stringify(snapshot)}`);
  return snapshot.rows.map(row => ({ id: row[idIndex], value: row[valueIndex] }))
    .sort((left, right) => left.id.localeCompare(right.id));
};

const expectedDisplayRows = oracle => oracle.rows
  .map(row => ({ id: row.id, value: row.state === 'present' ? String(row.value) : '—' }))
  .sort((left, right) => left.id.localeCompare(right.id));

const proposalRows = (response, oracle) => {
  const preview = response?.preview;
  const candidateIDs = response?.candidateColumnIds;
  assert(preview && Array.isArray(preview.rows), 'Choice proposal must return rendered rows.');
  assert(Array.isArray(candidateIDs) && candidateIDs.length === 1,
    `The candidate proposal must add exactly one column: ${JSON.stringify(candidateIDs)}`);
  const rootIDColumns = preview.columns.filter(column => column.label === 'Observation ID');
  assert.equal(rootIDColumns.length, 1, 'Choice proposal must preserve the Observation ID column.');
  const valueColumnID = candidateIDs[0];
  const valueColumn = preview.columns.find(column => column.column === valueColumnID);
  assert(valueColumn, `Candidate column ${valueColumnID} is absent from proposal preview columns.`);
  const rows = preview.rows.map(row => ({
    id: String(row[rootIDColumns[0].column]),
    value: row[valueColumnID] === undefined || row[valueColumnID] === null
      ? null
      : String(row[valueColumnID]),
  })).sort((left, right) => left.id.localeCompare(right.id));
  const expected = oracle.rows
    .map(row => ({ id: row.id, value: row.state === 'present' ? String(row.value) : null }))
    .sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(rows, expected,
    'Candidate preview rows must match the independent raw Observation oracle (missing and explicit null both render empty).');
  return { rows, candidateColumnID: valueColumnID, label: valueColumn.label };
};

export const builderDocument = (builder, outputId) => {
  const documents = builder?.workspace?.documents;
  assert(Array.isArray(documents), 'Builder state must contain its authored workspace documents.');
  const matches = documents.filter(document => document?.output?.id === outputId);
  assert.equal(matches.length, 1, `Builder must contain exactly one document for output ${outputId}.`);
  return matches[0];
};

export const builderAddColumnCoverageWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const fixtureNames = ['Patient.ndjson', 'Observation.ndjson'];
  const fixtureFilesBefore = fixtureNames.map(name => {
    const path = join(target.fixtureDir, name);
    const bytes = readFileSync(path);
    return { name, path, byteLength: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') };
  });
  const patientFixture = fixtureFilesBefore.find(file => file.name === 'Patient.ndjson');
  const observationFixture = fixtureFilesBefore.find(file => file.name === 'Observation.ndjson');
  assert(patientFixture && observationFixture, 'The independent fixture must provide both required source files.');
  const fixtureFile = observationFixture.path;
  const fixtureSHA256 = observationFixture.sha256;
  const oracle = readBasicAddColumnCoverageOracle({ fixtureDir: target.fixtureDir });
  const rawStates = oracle.rows.map(row => row.state).sort();
  assert.equal(oracle.resourceType, 'Observation');
  assert.equal(oracle.rows.length, 3, 'The independent fixture must have exactly three Observation records.');
  assert.deepEqual(rawStates, ['missing', 'null', 'present'],
    'The independent raw oracle must distinguish an omitted property from explicit null and a value.');
  const present = oracle.rows.find(row => row.state === 'present');
  const explicitNull = oracle.rows.find(row => row.state === 'null');
  const missing = oracle.rows.find(row => row.state === 'missing');
  assert.equal(present?.value, 'd');
  assert.equal(Object.hasOwn(explicitNull ?? {}, 'value'), true);
  assert.equal(explicitNull?.value, null);
  assert.equal(Object.hasOwn(missing ?? {}, 'value'), false);
  assert.equal(oracle.populatedCount, 1);
  assert.equal(oracle.displayedCount, 3);
  assert.deepEqual(oracle.populatedExamples, ['d']);
  const expectedIDs = oracle.rows.map(row => row.id).sort();
  const expectedRows = expectedDisplayRows(oracle);
  report.target.fixtureOracle = {
    path: fixtureFile,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sha256: fixtureSHA256,
    sourceFiles: fixtureFilesBefore,
    supportPatient: {
      resourceType: 'Patient',
      id: 'basic-add-column-coverage-inert-patient',
      sha256: patientFixture.sha256,
    },
    observationCount: oracle.rows.length,
    rows: oracle.rows,
    uiPopulation: {
      populatedCount: oracle.populatedCount,
      displayedCount: oracle.displayedCount,
      populatedExamples: oracle.populatedExamples,
      missingAndNullDisplay: 'both render as an empty cell',
    },
  };
  check('correctness', 'fresh fixture has three exact Observation IDs with present, explicit-null, and missing valueQuantity.code states',
    context.custom === false && context.seed?.fresh === true &&
      target.fixtureProject.startsWith('loom_dev_verify_') &&
      expectedIDs.length === 3 && new Set(expectedIDs).size === 3 &&
      rawStates.join(',') === 'missing,null,present',
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      fresh: context.seed?.fresh === true, rows: oracle.rows });

  const act = async (name, locator, perform, options = {}) => {
    await requireUniqueVisibleTarget(locator);
    return action(name, locator, perform, options);
  };
  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const title = `Verify ${context.runID.slice(-10)} direct field coverage`;

  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'),
    { waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  const newExplorer = page.getByText('New explorer', { exact: true });
  const nameInput = page.locator('#new-explorer-name');
  const tableName = page.locator('#first-table-name');
  await act('open New explorer', newExplorer, () => newExplorer.click(), {
    after: () => nameInput.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
  });
  await act('name disposable coverage Explorer', nameInput, () => nameInput.fill(title), {
    editable: true,
    after: async () => assert.equal(await nameInput.inputValue({ timeout: ACTION_TIMEOUT_MS }), title),
  });
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin &&
      url.pathname === explorerCollectionPath && requestBody(request)?.title === title;
  }, { timeout: ACTION_TIMEOUT_MS });
  const createBlank = page.getByRole('button', { name: 'Create blank' });
  let explorer;
  await act('create blank disposable coverage Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createResponsePromise;
      assert(response.ok(), `Native Explorer creation returned HTTP ${response.status()}.`);
      await page.waitForFunction(expectedTitle => {
        const select = document.querySelector('select[aria-label="Explorer"]');
        return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
      }, title, { timeout: ACTION_TIMEOUT_MS });
      explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue({ timeout: ACTION_TIMEOUT_MS });
      assert(explorer && explorer !== target.bootstrapExplorerId,
        'The user-created Explorer must be distinct from the fixture bootstrap.');
      await tableName.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    },
  });
  report.target.explorer = explorer;
  report.target.nativeMutationInventory = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorerId: explorer,
    setup: ['fresh fixture project/generation/bootstrap Explorer from the fixture harness', 'native New explorer → Create blank'],
    authoring: [],
    cleanup: [],
    fixtureProjectTeardown: 'The fixture harness retains this uniquely named devloop project and its disposable Explorer.',
  };
  check('correctness', 'Builder authoring is scoped to a new disposable Explorer in the fresh fixture project',
    Boolean(explorer && explorer !== target.bootstrapExplorerId &&
      target.fixtureProject.startsWith('loom_dev_verify_')),
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      bootstrapExplorerId: target.bootstrapExplorerId, explorerId: explorer });

  await act('name Observation coverage table', tableName, () => tableName.fill('Observation coverage'), {
    editable: true,
    after: async () => assert.equal(await tableName.inputValue({ timeout: ACTION_TIMEOUT_MS }), 'Observation coverage'),
  });
  const commandPath = `${explorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2/commands`;
  const apiRoot = `${explorerCollectionPath}/${encodeURIComponent(explorer)}`;
  const readBuilder = async () => {
    const response = await fetch(`${target.apiUrl}${apiRoot}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(ACTION_TIMEOUT_MS),
    });
    const value = await response.json();
    assert(response.ok, `Builder state read returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 600)}`);
    return value;
  };
  const createTablePromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      commandHas(request, 'CREATE_TABLE');
  }, { timeout: ACTION_TIMEOUT_MS });
  const addRootIDPromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      requestBody(request)?.commands?.some(command =>
        command.type === 'ADD_COLUMN' && command.title === 'Observation ID');
  }, { timeout: ACTION_TIMEOUT_MS });
  let outputId;
  let rootPreview;
  let rootIDs;
  let draftBeforeProposal;
  let documentBeforeProposal;
  await act('choose Observation rows', page.getByRole('button', { name: 'Choose Observation rows' }),
    () => page.getByRole('button', { name: 'Choose Observation rows' }).click(), {
      after: async () => {
        const [createResponse, addIDResponse] = await Promise.all([createTablePromise, addRootIDPromise]);
        assert(createResponse.ok(), `Native Observation table command returned HTTP ${createResponse.status()}.`);
        assert(addIDResponse.ok(), `Native Observation ID column command returned HTTP ${addIDResponse.status()}.`);
        const addColumnsButton = page.getByTestId('construction-action-add-columns');
        await addColumnsButton.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await page.waitForFunction(() => {
          const button = document.querySelector('[data-testid="construction-action-add-columns"]');
          return button && !button.disabled;
        }, undefined, { timeout: ACTION_TIMEOUT_MS });
        await waitForPreview(page, expectedIDs.length, 1);
        rootPreview = await previewSnapshot(page);
        const rootIDIndex = headerIndex(rootPreview.headers, 'Observation ID');
        rootIDs = rootPreview.rows.map(row => row[rootIDIndex]).sort();
        assert.equal(rootPreview.rowCount, 3, 'The saved Observation table must render all three raw fixture rows.');
        assert.equal(rootPreview.columnCount, 1, 'The fresh Observation table must start with only its verified ID column.');
        assert.deepEqual(rootIDs, expectedIDs, 'The native root table must render the exact raw Observation IDs.');
        const tableTab = page.locator('[data-testid^="construction-table-"]');
        await requireUniqueVisibleTarget(tableTab);
        const tableTestId = await tableTab.getAttribute('data-testid', { timeout: ACTION_TIMEOUT_MS });
        outputId = tableTestId.replace(/^construction-table-/, '');
        report.target.outputId = outputId;
        draftBeforeProposal = await readBuilder();
        documentBeforeProposal = builderDocument(draftBeforeProposal, outputId);
        assert.equal(draftBeforeProposal.catalog?.generation, target.fixtureGeneration,
          'The draft baseline must retain the exact fixture generation.');
        assert(draftBeforeProposal.draftVersion > 0 && draftBeforeProposal.draftDigest,
          'The saved table baseline must expose its current draft version and digest.');
        check('correctness', 'native Observation preview renders all three raw fixture rows', true,
          { expectedIDs, rootIDs, headers: rootPreview.headers,
            rowCount: rootPreview.rowCount, columnCount: rootPreview.columnCount,
            draftVersion: draftBeforeProposal.draftVersion, draftDigest: draftBeforeProposal.draftDigest });
      },
    });

  const commandWrites = [];
  const onCommandRequest = request => {
    const url = new URL(request.url());
    if (request.method() === 'POST' && url.pathname === commandPath) commandWrites.push(request);
  };
  page.on('request', onCommandRequest);
  const mutationCountBeforeFirstProposal = commandWrites.length;
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Choose Observation rows'],
    capturedCommands: ['CREATE_TABLE', 'ADD_COLUMN Observation ID'],
    outputId,
  });

  const openAddColumns = async (suffix, expectedDraft) => {
    const addColumns = page.getByRole('button', { name: 'Add columns: Bring more information into each row.', exact: true });
    const fieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
    const search = page.getByRole('searchbox', { name: 'Search features by field name, concept, or code' });
    const field = page.getByRole('checkbox', { name: 'Select Observation.valueQuantity.code', exact: true });
    const selectedFeature = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    await act(`${suffix} open Add columns editor`, addColumns, () => addColumns.click(), {
      after: async () => page.locator('[aria-label="Add columns editor"]').waitFor({
        state: 'visible', timeout: ACTION_TIMEOUT_MS,
      }),
    });
    await act(`${suffix} open Fields and related data`, fieldsTab, () => fieldsTab.click(), {
      after: async () => assert.equal(await fieldsTab.getAttribute('aria-pressed', { timeout: ACTION_TIMEOUT_MS }), 'true'),
    });
    await act(`${suffix} search exact FHIR field path`, search, () => search.fill('valueQuantity.code'), {
      editable: true,
      after: async () => assert.equal(await search.inputValue({ timeout: ACTION_TIMEOUT_MS }), 'valueQuantity.code'),
    });
    await act(`${suffix} submit field search`, page.getByRole('button', { name: 'Search', exact: true }),
      () => page.getByRole('button', { name: 'Search', exact: true }).click(), {
        after: () => page.getByText('No concepts match this search.', { exact: true }).waitFor({
          state: 'visible', timeout: ACTION_TIMEOUT_MS,
        }),
      });
    const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
    await act(`${suffix} open raw FHIR fields`, rawFields, () => rawFields.click(), {
      after: () => field.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }),
    });
    await act(`${suffix} select valueQuantity.code`, field, () => field.check(), {
      after: async () => assert.equal(await field.isChecked(), true, 'The exact valueQuantity.code field must be selected.'),
    });
    const proposalPromise = page.waitForResponse(response => {
      const request = response.request();
      const url = new URL(response.url());
      return request.method() === 'POST' && url.origin === uiOrigin &&
        url.pathname === `${explorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2/construction-choice-proposals` &&
        requestBody(request)?.outputId === outputId;
    }, { timeout: ACTION_TIMEOUT_MS });
    let response;
    let proposal;
    let mappedRows;
    let candidatePreviewRows;
    let visibleCandidateLabel;
    let coverageText;
    await act(`${suffix} request selected-field preview`, selectedFeature, () => selectedFeature.click(), {
      after: async () => {
        response = await proposalPromise;
        assert(response.ok(), `Native candidate proposal returned HTTP ${response.status()}.`);
        const request = requestBody(response.request());
        assert.equal(request?.expectedDraftVersion, expectedDraft.draftVersion,
          'Candidate proposal must target the saved draft version under test.');
        assert.equal(request?.expectedDraftDigest, expectedDraft.draftDigest,
          'Candidate proposal must target the saved draft digest under test.');
        proposal = await response.json();
        mappedRows = proposalRows(proposal, oracle);
        assert.equal(proposal.previewStatus, 'READY', 'Candidate preview must be ready before it is shown for review.');
        assert.equal(proposal.outputId, outputId, 'Candidate proposal must remain scoped to the selected table.');
        assert.equal(proposal.preview.outputId, outputId, 'Candidate preview must remain scoped to the selected table.');
        assert(proposal.preview.receiptId, 'Candidate preview must include its exact preview receipt.');
        assert(proposal.candidateWorkspaceDigest, 'Candidate proposal must identify the proposed workspace digest.');
        assert.equal(proposal.constructionChoices?.length, 1,
          'Candidate proposal must contain exactly the selected direct field choice.');
        assert(proposal.constructionChoices[0]?.choiceId, 'Candidate proposal must identify the exact selected field choice.');
        assert.equal(proposal.snapshotToken, expectedDraft.catalog.snapshotToken,
          'Candidate proposal must use the fixture catalog snapshot bound to the saved table.');
        assert.equal(proposal.draftVersion, expectedDraft.draftVersion,
          'Candidate preview must remain bound to the unchanged saved draft version.');
        assert.equal(proposal.draftDigest, expectedDraft.draftDigest,
          'Candidate preview must remain bound to the unchanged saved draft digest.');
        const preview = proposal.preview;
        assert.equal(preview.rowCount, 3, 'Candidate preview must retain all three rows.');
        assert.equal(preview.sampled, false, 'Candidate preview must be unsampled for the exact three-record fixture.');
        assert.notEqual(preview.partialValidation, true, 'Candidate preview must not be partial.');
        const panel = page.getByTestId('construction-choice-proposal-panel');
        const coverage = page.getByTestId('construction-preview-value-coverage');
        const proposalPreview = page.getByTestId('construction-proposal-preview');
        await panel.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await coverage.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await proposalPreview.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
        await page.waitForFunction(({ outputId, receiptId }) => {
          const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
          return preview?.dataset.previewStatus === 'ready' &&
            preview.dataset.previewOutputId === outputId &&
            preview.dataset.previewReceiptId === receiptId &&
            preview.querySelector('table') !== null;
        }, { outputId, receiptId: proposal.preview.receiptId }, { timeout: ACTION_TIMEOUT_MS });
        const proposalTable = proposalPreview.locator('table');
        assert.equal(await proposalTable.count(), 1, 'Candidate preview must render exactly one HTML table.');
        const visibleCandidatePreview = await proposalTable.evaluate(table => ({
          rowCount: table.querySelectorAll('tbody tr').length,
          columnCount: table.querySelectorAll('thead th').length,
          headers: [...table.querySelectorAll('thead th')].map(header =>
            header.querySelector('span')?.textContent?.trim() ?? ''),
          rows: [...table.querySelectorAll('tbody tr')].map(row =>
            [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
        }));
        assert.deepEqual(visibleCandidatePreview.headers, ['Observation ID', 'Value Quantity Code'],
          'Rendered candidate Preview must retain the exact authored ID and value labels.');
        candidatePreviewRows = savedRowsByObservationID(visibleCandidatePreview, 'Value Quantity Code');
        assert.equal(visibleCandidatePreview.rowCount, oracle.displayedCount,
          'The rendered candidate Preview must retain all three fixture rows.');
        assert.equal(visibleCandidatePreview.columnCount, 2,
          'The rendered candidate Preview must contain the saved ID and one proposed field column.');
        assert.deepEqual(candidatePreviewRows, expectedDisplayRows(oracle),
          'The rendered candidate Preview must show exact values, with missing and explicit null both empty.');
        coverageText = await coverage.innerText({ timeout: ACTION_TIMEOUT_MS });
        assert.match(coverageText, /1 of 3 rows contain a value\./,
          `Candidate coverage must report one populated value across the full three-row preview: ${coverageText}`);
        assert.match(coverageText, /Examples: d(?:$|\n)/,
          `Candidate coverage must show the exact populated example d: ${coverageText}`);
        const coverageItem = coverage.locator('[data-column-id]');
        assert.equal(await coverageItem.count(), 1, 'Coverage panel must describe exactly one new candidate column.');
        visibleCandidateLabel = await coverageItem.locator('span').first().innerText({ timeout: ACTION_TIMEOUT_MS });
        assert.equal(visibleCandidateLabel, mappedRows.label,
          'The displayed coverage must be bound to the candidate column returned by the preview.');
      },
    });
    return {
      response,
      proposal,
      mappedRows,
      candidatePreviewRows,
      candidateLabel: visibleCandidateLabel,
      coverageText,
      mutationCount: commandWrites.length,
    };
  };

  const firstProposal = await openAddColumns('first', draftBeforeProposal);
  check('correctness', 'candidate preview reports one populated row of three and example d against the raw oracle',
    firstProposal.mappedRows.rows.length === 3 &&
      JSON.stringify(firstProposal.candidatePreviewRows) === JSON.stringify(expectedRows) &&
      firstProposal.proposal.preview.rowCount === 3 &&
      firstProposal.proposal.preview.sampled === false &&
      firstProposal.proposal.preview.partialValidation !== true &&
      /1 of 3 rows contain a value\./.test(firstProposal.coverageText) &&
      /Examples: d(?:$|\n)/.test(firstProposal.coverageText),
    { candidateColumnID: firstProposal.mappedRows.candidateColumnID,
      candidateLabel: firstProposal.candidateLabel,
      coverageText: firstProposal.coverageText,
      rawRows: oracle.rows, proposalRows: firstProposal.mappedRows.rows });

  const cancel = page.getByRole('button', { name: 'Cancel', exact: true });
  let afterCancelPreview;
  let afterCancelIDs;
  let draftAfterCancel;
  let documentAfterCancel;
  await act('cancel first candidate proposal', cancel, () => cancel.click(), {
    after: async () => {
      await page.getByTestId('construction-choice-proposal-panel').waitFor({
        state: 'detached', timeout: ACTION_TIMEOUT_MS,
      });
      await waitForPreview(page, expectedIDs.length, 1);
      afterCancelPreview = await previewSnapshot(page);
      const afterCancelIDIndex = headerIndex(afterCancelPreview.headers, 'Observation ID');
      afterCancelIDs = afterCancelPreview.rows.map(row => row[afterCancelIDIndex]).sort();
      draftAfterCancel = await readBuilder();
      documentAfterCancel = builderDocument(draftAfterCancel, outputId);
      assert.equal(afterCancelPreview.rowCount, 3, 'Cancel must restore all three saved Observation rows.');
      assert.equal(afterCancelPreview.columnCount, 1, 'Cancel must restore only the saved Observation ID column.');
      assert.deepEqual(afterCancelIDs, expectedIDs, 'Cancel must restore the exact raw Observation IDs.');
      assert(!afterCancelPreview.headers.includes(firstProposal.candidateLabel),
        'Cancel must remove the proposed field from the rendered saved table.');
      assert.equal(commandWrites.length, mutationCountBeforeFirstProposal,
        'Cancel must leave the command-write count unchanged.');
      assert.equal(draftAfterCancel.draftVersion, draftBeforeProposal.draftVersion,
        'Cancel must leave the saved draft version unchanged.');
      assert.equal(draftAfterCancel.draftDigest, draftBeforeProposal.draftDigest,
        'Cancel must leave the saved draft digest unchanged.');
      assert.deepEqual(documentAfterCancel, documentBeforeProposal,
        'Cancel must leave the saved table document unchanged.');
      check('persistence', 'Cancel leaves the saved table unchanged and restores its exact three-row Preview', true,
        { expectedIDs, actualIDs: afterCancelIDs, headers: afterCancelPreview.headers,
          rowCount: afterCancelPreview.rowCount, columnCount: afterCancelPreview.columnCount,
          commandWritesBefore: mutationCountBeforeFirstProposal, commandWritesAfter: commandWrites.length,
          draftVersionBefore: draftBeforeProposal.draftVersion, draftVersionAfter: draftAfterCancel.draftVersion,
          draftDigestBefore: draftBeforeProposal.draftDigest, draftDigestAfter: draftAfterCancel.draftDigest,
          documentUnchanged: true });
    },
  });
  const cancelCommandDelta = commandWrites.length - mutationCountBeforeFirstProposal;

  const pageBody = page.locator('body');
  let draftAfterCancelReload;
  await act('reload after Cancel and verify the saved Observation table', pageBody,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        await waitForPreview(page, expectedIDs.length, 1);
        const afterCancelReload = await previewSnapshot(page);
        draftAfterCancelReload = await readBuilder();
        const documentAfterCancelReload = builderDocument(draftAfterCancelReload, outputId);
        const afterCancelReloadIDs = afterCancelReload.rows.map(row =>
          row[headerIndex(afterCancelReload.headers, 'Observation ID')]).sort();
        assert.equal(afterCancelReload.rowCount, 3, 'Reload after Cancel must retain all three Observation rows.');
        assert.equal(afterCancelReload.columnCount, 1, 'Reload after Cancel must retain only the saved ID column.');
        assert.deepEqual(afterCancelReloadIDs, expectedIDs, 'Reload after Cancel must retain exact raw Observation IDs.');
        assert(!afterCancelReload.headers.includes(firstProposal.candidateLabel),
          'Reload after Cancel must not restore the canceled candidate column.');
        assert.equal(draftAfterCancelReload.draftVersion, draftBeforeProposal.draftVersion,
          'Reload after Cancel must retain the saved draft version.');
        assert.equal(draftAfterCancelReload.draftDigest, draftBeforeProposal.draftDigest,
          'Reload after Cancel must retain the saved draft digest.');
        assert.deepEqual(documentAfterCancelReload, documentBeforeProposal,
          'Reload after Cancel must retain the exact saved table document.');
        check('persistence', 'reload after Cancel retains only the saved Observation ID column and exact rows', true,
          { expectedIDs, actualIDs: afterCancelReloadIDs, headers: afterCancelReload.headers,
            rowCount: afterCancelReload.rowCount, columnCount: afterCancelReload.columnCount,
            draftVersionBefore: draftBeforeProposal.draftVersion, draftVersionAfter: draftAfterCancelReload.draftVersion,
            draftDigestBefore: draftBeforeProposal.draftDigest, draftDigestAfter: draftAfterCancelReload.draftDigest,
            documentUnchanged: true });
      },
    });

  const draftBeforeApplyProposal = await readBuilder();
  assert.equal(draftBeforeApplyProposal.draftDigest, draftBeforeProposal.draftDigest,
    'Reproposal must start from the exact unchanged saved draft.');
  const appliedProposal = await openAddColumns('second', draftBeforeApplyProposal);
  const apply = page.getByRole('button', { name: 'Apply columns', exact: true });
  const applyPromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      requestBody(request)?.commands?.some(command =>
        command.type === 'APPLY_CONSTRUCTION_CHOICE' &&
        command.outputId === outputId &&
        command.constructionChoice?.choiceId);
  }, { timeout: ACTION_TIMEOUT_MS });
  let appliedPreview;
  let appliedRows;
  let draftAfterApply;
  let documentAfterApply;
  let savedCandidateColumnIDs;
  await act('apply candidate column proposal', apply, () => apply.click(), {
    after: async () => {
      const response = await applyPromise;
      assert(response.ok(), `Native Apply columns returned HTTP ${response.status()}.`);
      const request = requestBody(response.request());
      const command = request?.commands?.find(candidate =>
        candidate.type === 'APPLY_CONSTRUCTION_CHOICE' && candidate.outputId === outputId);
      assert.equal(request?.commands?.length, 1, 'Apply must save only the candidate column under review.');
      assert.equal(request?.commandId, appliedProposal.proposal.commandId,
        'Apply must reuse the command identity bound to this candidate proposal.');
      assert.equal(request?.snapshotToken, appliedProposal.proposal.snapshotToken,
        'Apply must reuse the candidate proposal catalog snapshot.');
      assert.equal(request?.expectedDraftVersion, draftBeforeApplyProposal.draftVersion,
        'Apply must target the unchanged draft version under review.');
      assert.equal(request?.expectedDraftDigest, draftBeforeApplyProposal.draftDigest,
        'Apply must target the unchanged draft digest under review.');
      assert.equal(command?.constructionChoice?.choiceId, appliedProposal.proposal.constructionChoices[0]?.choiceId,
        'Apply must save the exact construction choice that was previewed.');
      await waitForPreview(page, expectedIDs.length, 2);
      appliedPreview = await previewSnapshot(page);
      appliedRows = savedRowsByObservationID(appliedPreview, appliedProposal.candidateLabel);
      assert.equal(appliedPreview.rowCount, 3, 'Apply must render all three Observation rows.');
      assert.equal(appliedPreview.columnCount, 2, 'Apply must render the ID and candidate columns.');
      assert.deepEqual(appliedRows, expectedRows,
        'Apply must render exact candidate values, with missing and explicit null both empty.');
      draftAfterApply = await readBuilder();
      documentAfterApply = builderDocument(draftAfterApply, outputId);
      savedCandidateColumnIDs = documentAfterApply.columns.map(column => column.column);
      assert(savedCandidateColumnIDs.includes(appliedProposal.mappedRows.candidateColumnID),
        'Apply must persist the exact proposed candidate column ID.');
      assert(draftAfterApply.draftVersion > draftBeforeProposal.draftVersion &&
        draftAfterApply.draftDigest !== draftBeforeProposal.draftDigest,
      'Apply must advance the saved draft version and digest.');
      check('correctness', 'Apply renders the exact candidate values and preserves all three Observation rows', true,
        { expectedRows, appliedRows, headers: appliedPreview.headers,
          rowCount: appliedPreview.rowCount, columnCount: appliedPreview.columnCount,
          candidateColumnID: appliedProposal.mappedRows.candidateColumnID, savedCandidateColumnIDs,
          draftVersionBefore: draftBeforeProposal.draftVersion, draftVersionAfter: draftAfterApply.draftVersion,
          draftDigestBefore: draftBeforeProposal.draftDigest, draftDigestAfter: draftAfterApply.draftDigest });
    },
  });

  let draftAfterApplyReload;
  await act('reload after Apply and verify the saved candidate column', pageBody,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS }), {
      after: async () => {
        await waitForPreview(page, expectedIDs.length, 2);
        const reloadedPreview = await previewSnapshot(page);
        const reloadedRows = savedRowsByObservationID(reloadedPreview, appliedProposal.candidateLabel);
        const reloadedIDs = reloadedRows.map(row => row.id);
        draftAfterApplyReload = await readBuilder();
        const documentAfterApplyReload = builderDocument(draftAfterApplyReload, outputId);
        const reloadedCandidateColumnIDs = documentAfterApplyReload.columns.map(column => column.column);
        assert.equal(reloadedPreview.rowCount, 3, 'Reload after Apply must retain all three Observation rows.');
        assert.equal(reloadedPreview.columnCount, 2, 'Reload after Apply must retain the saved ID and candidate columns.');
        assert.deepEqual(reloadedRows, expectedRows, 'Reload after Apply must render the exact saved values.');
        assert.deepEqual(reloadedIDs, expectedIDs, 'Reload after Apply must retain the exact raw Observation IDs.');
        assert.equal(reloadedCandidateColumnIDs.length, 2, 'Reload after Apply must retain exactly two saved columns.');
        assert(reloadedCandidateColumnIDs.includes(appliedProposal.mappedRows.candidateColumnID),
          'Reload after Apply must retain the exact proposed candidate column ID.');
        assert(draftAfterApplyReload.draftVersion > draftBeforeProposal.draftVersion &&
          draftAfterApplyReload.draftDigest !== draftBeforeProposal.draftDigest,
        'Reload after Apply must retain the advanced saved draft version and digest.');
        assert.notDeepEqual(documentAfterApplyReload, documentBeforeProposal,
          'Reload after Apply must restore a changed saved table document.');
        check('persistence', 'Builder reload restores the saved candidate column and exact three-row values', true,
          { expectedRows, reloadedRows, headers: reloadedPreview.headers,
            rowCount: reloadedPreview.rowCount, columnCount: reloadedPreview.columnCount,
            candidateColumnID: appliedProposal.mappedRows.candidateColumnID,
            savedCandidateColumnIDs: reloadedCandidateColumnIDs,
            draftVersionBefore: draftBeforeProposal.draftVersion, draftVersionAfter: draftAfterApplyReload.draftVersion,
            draftDigestBefore: draftBeforeProposal.draftDigest, draftDigestAfter: draftAfterApplyReload.draftDigest });
      },
    });

  const fixtureFilesAfter = fixtureNames.map(name => {
    const path = join(target.fixtureDir, name);
    const bytes = readFileSync(path);
    return { name, path, byteLength: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') };
  });
  const fixtureFilesUnchanged = JSON.stringify(fixtureFilesBefore) === JSON.stringify(fixtureFilesAfter);
  check('correctness', 'independent Patient and Observation fixture files are unchanged after the native lifecycle',
    fixtureFilesUnchanged,
    { before: fixtureFilesBefore, after: fixtureFilesAfter });

  report.target.nativeMutationInventory.authoring.push({
    controls: [
      'Add columns: Bring more information into each row.',
      'Fields and related data',
      'Search features by field name, concept, or code',
      'Raw FHIR fields (advanced)',
      'Select Observation.valueQuantity.code',
      'Add 1 selected feature',
      'Cancel',
      'Apply columns',
    ],
    capturedCommands: ['CREATE_TABLE', 'ADD_COLUMN Observation ID', 'APPLY_CONSTRUCTION_CHOICE'],
    canceledProposalCommandDelta: cancelCommandDelta,
    outputId,
  });
  page.off('request', onCommandRequest);
};
