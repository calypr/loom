import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { recordCheck } from '../helpers/report.mjs';

const unique = async locator => {
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
  }, { expectedRows, expectedColumns });
};

const previewSnapshot = async page => page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
  rowCount: Number(table.getAttribute('aria-rowcount')) - 1,
  columnCount: Number(table.getAttribute('aria-colcount')),
  headers: [...table.querySelectorAll('[role="columnheader"]')].map(header => header.innerText.trim()),
  rows: [...table.querySelectorAll('[role="row"]')].slice(1).map(row =>
    [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
}));

const baseHeader = label => label.replace(/\s+\([^)]*\)$/, '').trim();

const normalizePreviewHeader = label => baseHeader(label).normalize('NFKC').replace(/\s+/g, ' ').trim().toLocaleLowerCase('en-US');

export const previewHeaderMatches = (actual, expected) =>
  normalizePreviewHeader(actual) === normalizePreviewHeader(expected);

const rowsByObservationID = (snapshot, expectedLabel) => {
  const idIndex = snapshot.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  const codedIndex = snapshot.headers.findIndex(label => previewHeaderMatches(label, expectedLabel));
  assert.notEqual(idIndex, -1, `The preview has no Observation ID column: ${JSON.stringify(snapshot.headers)}`);
  assert.notEqual(codedIndex, -1, `The preview has no ${expectedLabel} column: ${JSON.stringify(snapshot.headers)}`);
  assert(snapshot.rows.every(row => row.length === snapshot.headers.length),
    `The preview has an incomplete row: ${JSON.stringify(snapshot)}`);
  return snapshot.rows.map(row => ({
    id: row[idIndex],
    value: row[codedIndex] === '—' ? null : row[codedIndex],
  })).sort((left, right) => left.id.localeCompare(right.id));
};

const hasVerifiedCapabilityReplacement = item => {
  const binding = item.binding;
  const replacement = item.replacement;
  return item.kind === 'network' && item.errorText === 'net::ERR_ABORTED' &&
    item.canceled === true && item.cancellationReason === 'superseded capability binding has a later successful replacement' &&
    item.method === 'POST' && Number.isInteger(item.sequence) &&
    binding?.route === item.url && new URL(binding.route).pathname.endsWith('/authoring/v2/construction-capabilities') &&
    replacement && Number.isInteger(replacement.sequence) && replacement.sequence > item.sequence &&
    Number.isInteger(replacement.status) && replacement.status >= 200 && replacement.status < 300 &&
    replacement.finished === true && replacement.responseMatches === true && replacement.failed !== true &&
    replacement.binding?.route === binding.route &&
    JSON.stringify(replacement.binding) !== JSON.stringify(binding);
};

export const classifyCodedColumnDiagnostics = report => {
  const cancelledReads = report.network.filter(item => {
    try { return hasVerifiedCapabilityReplacement(item); } catch { return false; }
  });
  return {
    cancelledReads,
    unexpected: report.network.filter(item => !cancelledReads.includes(item)),
  };
};

export const directHeightQuantityChoices = (renderedChoices, sourceOptions) => {
  const optionsByChoiceID = new Map(sourceOptions.map(source => [source.choiceId, source]));
  return renderedChoices.filter(choice => {
    if (!/Example:\s*Height\b/i.test(choice.text)) return false;
    const prefix = 'frame-source-choice-';
    if (typeof choice.testId !== 'string' || !choice.testId.startsWith(prefix)) return false;
    const source = optionsByChoiceID.get(choice.testId.slice(prefix.length));
    return source?.resourceType === 'Observation' && Array.isArray(source.route) && source.route.length === 0 &&
      /(?:^|\.)code(?:\.|$)/i.test(source.sourcePath) && source.valuePath === 'valueQuantity.value';
  });
};

const checkUnexpectedDiagnostics = report => {
  const { cancelledReads, unexpected } = classifyCodedColumnDiagnostics(report);
  report.cancelledOwnedReads = cancelledReads;
  recordCheck(report, 'correctness', 'no unexpected network, API, or browser errors', unexpected.length === 0,
    { console: unexpected.filter(item => item.kind === 'console-error'),
      pageErrors: unexpected.filter(item => item.kind === 'exception'),
      networkFailures: unexpected.filter(item => item.kind === 'network') });
};

export const builderCodedSourceColumnWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const sourcePath = join(target.fixtureDir, 'Observation.ndjson');
  const sourceBytes = readFileSync(sourcePath);
  const sourceSHA256 = createHash('sha256').update(sourceBytes).digest('hex');
  const observations = sourceBytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const expectedIDs = observations.map(observation => observation.id).sort();
  const expectedHeightRows = observations.map(observation => {
    const heightCoding = observation.code?.coding?.find(coding =>
      coding.system === 'https://example.test/codes' && coding.code === 'height');
    return { id: observation.id, value: heightCoding ? String(observation.valueQuantity?.value) : null };
  }).sort((left, right) => left.id.localeCompare(right.id));
  assert.equal(observations.length, 6, 'The devloop fixture must contain exactly six Observation records.');
  assert.equal(new Set(expectedIDs).size, expectedIDs.length, 'The Observation fixture IDs must be unique.');
  assert.deepEqual(expectedHeightRows.filter(row => row.value !== null), [
    { id: 'dev-observation-001', value: '172.5' },
    { id: 'dev-observation-003', value: '180' },
  ], 'The independent Observation fixture must contain the expected two Height values.');
  assert.equal(expectedHeightRows.filter(row => row.value === null).length, 4);
  report.target.fixtureOracle = {
    path: sourcePath,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sha256: sourceSHA256,
    observationCount: observations.length,
    observationIDs: expectedIDs,
    heightRows: expectedHeightRows,
  };
  check('correctness', 'fresh fixture contains the exact six independent Observation IDs and Height values',
    context.custom === false && context.seed?.fresh === true && target.fixtureProject.startsWith('loom_dev_verify_') &&
      target.bootstrapExplorerId && expectedIDs.length === 6,
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      bootstrapExplorerId: target.bootstrapExplorerId, fresh: context.seed?.fresh === true,
      observationIDs: expectedIDs, heightRows: expectedHeightRows });

  const act = async (name, locator, perform, options = {}) => {
    await unique(locator);
    return action(name, locator, perform, options);
  };
  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const title = `Verify ${context.runID.slice(-10)} coded source`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible' });
  await act('open New explorer', page.getByText('New explorer', { exact: true }),
    () => page.getByText('New explorer', { exact: true }).click());
  const nameInput = page.locator('#new-explorer-name');
  await act('name disposable coded-source Explorer', nameInput, () => nameInput.fill(title), { editable: true });
  const createResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const url = new URL(response.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === explorerCollectionPath &&
      requestBody(request)?.title === title;
  });
  const createBlank = page.getByRole('button', { name: 'Create blank' });
  await act('create blank disposable Explorer', createBlank, () => createBlank.click(), {
    after: async () => {
      const response = await createResponsePromise;
      assert(response.ok(), `Native Explorer creation returned HTTP ${response.status()}.`);
    },
  });
  await page.waitForFunction(expectedTitle => {
    const select = document.querySelector('select[aria-label="Explorer"]');
    return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
  }, title);
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The user-created Explorer must be distinct from the fixture bootstrap.');
  report.target.explorer = explorer;
  report.target.nativeMutationInventory = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    bootstrapExplorerId: target.bootstrapExplorerId,
    explorerId: explorer,
    setup: ['fresh fixture project/generation/bootstrap Explorer from the fixture harness', 'native New explorer → Create blank'],
    authoring: [],
    cleanup: [],
    fixtureProjectTeardown: 'The fixture harness retains each uniquely named loom_dev_verify_* project; this case removes its coded source column through Builder UI and leaves the disposable project/Explorer retained.',
  };
  check('correctness', 'Builder authoring is scoped to one new disposable Explorer in the fresh fixture project',
    Boolean(explorer && explorer !== target.bootstrapExplorerId && target.fixtureProject.startsWith('loom_dev_verify_')),
    { project: target.fixtureProject, generation: target.fixtureGeneration,
      bootstrapExplorerId: target.bootstrapExplorerId, explorerId: explorer,
      createStatus: (await createResponsePromise).status() });

  const tableName = page.locator('#first-table-name');
  await act('name Observation table', tableName, () => tableName.fill('Observations'), { editable: true });
  const commandPath = `${explorerCollectionPath}/${encodeURIComponent(explorer)}/authoring/v2/commands`;
  const createTableCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'CREATE_TABLE');
  });
  const addRootIDCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      body?.commands?.some(command => command.type === 'ADD_COLUMN' && command.title === 'Observation ID');
  });
  await act('choose Observation rows', page.getByRole('button', { name: 'Choose Observation rows' }),
    () => page.getByRole('button', { name: 'Choose Observation rows' }).click(), {
      after: async () => {
        const [createResponse, addIDResponse] = await Promise.all([
          createTableCommandPromise, addRootIDCommandPromise,
        ]);
        assert(createResponse.ok(), `Native Observation table command returned HTTP ${createResponse.status()}.`);
        assert(addIDResponse.ok(), `Native Observation ID column command returned HTTP ${addIDResponse.status()}.`);
      },
    });
  await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible' });
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  });
  await waitForPreview(page, expectedIDs.length, 1);
  const rootPreview = await previewSnapshot(page);
  const rootIDIndex = rootPreview.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  assert.notEqual(rootIDIndex, -1, `The root preview has no Observation ID column: ${JSON.stringify(rootPreview.headers)}`);
  const rootIDs = rootPreview.rows.map(row => row[rootIDIndex]).sort();
  check('correctness', 'native Observation root preview renders all six independent fixture IDs',
    JSON.stringify(rootIDs) === JSON.stringify(expectedIDs),
    { expectedIDs, rootIDs, headers: rootPreview.headers, rowCount: rootPreview.rowCount });
  const tableTab = page.locator('[data-testid^="construction-table-"]');
  await unique(tableTab);
  const tableTestId = await tableTab.getAttribute('data-testid');
  const outputId = tableTestId.replace(/^construction-table-/, '');
  report.target.outputId = outputId;
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Choose Observation rows'],
    capturedCommands: ['CREATE_TABLE', 'ADD_COLUMN Observation ID'],
    outputId,
  });

  const addColumns = page.getByRole('button', { name: /Add columns:/ });
  await act('open Add columns', addColumns, () => addColumns.click());
  await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
  await act('open Coded values', page.getByRole('button', { name: 'Coded values', exact: true }),
    () => page.getByRole('button', { name: 'Coded values', exact: true }).click());
  const framePanel = page.getByTestId('frame-source-panel');
  await framePanel.waitFor({ state: 'visible' });
  const browseButton = framePanel.getByRole('button', { name: /^Browse sources/ });
  await act('browse coded source values', browseButton, () => browseButton.click());
  const sourceSearch = page.getByRole('searchbox', { name: 'Search framing sources' });
  await sourceSearch.waitFor({ state: 'visible' });
  await act('search Observation coded sources', sourceSearch, () => sourceSearch.fill('Observation'), { editable: true });
  const sourceOptionsResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/frame-source-options') &&
      body?.outputId === outputId && body?.query === 'Observation';
  });
  let sourceOptionsBody;
  const sourceSearchButton = framePanel.getByRole('button', { name: 'Search', exact: true });
  await act('search native Observation framing choices', sourceSearchButton, () => sourceSearchButton.click(), {
    after: async () => {
      const response = await sourceOptionsResponsePromise;
      assert(response.ok(), `Native Observation frame search returned HTTP ${response.status()}.`);
      sourceOptionsBody = await response.json();
      assert.equal(sourceOptionsBody.outputId, outputId, 'Native frame search must stay on the selected Observation table.');
    },
  });
  const choiceButtons = page.locator('[data-testid^="frame-source-choice-"]');
  await choiceButtons.first().waitFor({ state: 'visible' });
  const sourceChoices = await choiceButtons.evaluateAll(buttons => buttons.map(button => ({
    testId: button.getAttribute('data-testid'),
    text: button.parentElement?.innerText?.replace(/\s+/g, ' ').trim() ?? '',
  })));
  const heightSourceChoices = directHeightQuantityChoices(sourceChoices, sourceOptionsBody.sources);
  assert.equal(heightSourceChoices.length, 1,
    `The native search must expose one direct Observation Height Quantity source; choices: ${JSON.stringify(sourceChoices)}`);
  const sourceChoiceId = heightSourceChoices[0].testId.replace(/^frame-source-choice-/, '');
  const sourceOption = sourceOptionsBody.sources.find(source => source.choiceId === sourceChoiceId);
  assert(sourceOption, 'The native Height source control must map to its own frame-source response item.');
  assert.equal(sourceOption.resourceType, 'Observation');
  assert.equal(sourceOption.route.length, 0, 'The Height source must be on direct Observation records.');
  assert.match(sourceOption.sourcePath.toLowerCase(), /code/);
  assert.equal(sourceOption.valuePath, 'valueQuantity.value', 'The selected Height code must pair with the fixture Quantity value field.');
  const sourceChoice = page.getByTestId(heightSourceChoices[0].testId);
  const initialCategoryResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/semantic-inventory') &&
      body?.outputId === outputId && Boolean(body?.frameId) && !body?.query;
  });
  const frameSourceResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'SET_FRAME_SOURCE');
  });
  await act('use the direct Observation Height source', sourceChoice, () => sourceChoice.click(), {
    after: async () => {
      const response = await frameSourceResponsePromise;
      assert(response.ok(), `Native SET_FRAME_SOURCE command returned HTTP ${response.status()}.`);
      const command = requestBody(response.request()).commands.find(candidate => candidate.type === 'SET_FRAME_SOURCE');
      assert.equal(command?.frameChoiceId, sourceChoiceId, 'The saved frame must use the exact source choice selected in the UI.');
    },
  });
  const savedFrame = page.locator('[data-testid^="saved-frame-"]');
  await savedFrame.waitFor({ state: 'visible' });
  await unique(savedFrame);
  const savedFrameTestId = await savedFrame.getAttribute('data-testid');
  const frameId = savedFrameTestId.replace(/^saved-frame-/, '');
  report.target.frameId = frameId;
  const savedFrameText = (await savedFrame.innerText()).replace(/\s+/g, ' ').trim();
  check('correctness', 'native Coded values controls save the direct Observation Height frame',
    Boolean(frameId && /Observation/i.test(savedFrameText) && /On each Observation record/i.test(savedFrameText)),
    { frameId, sourceCard: heightSourceChoices[0].text, savedFrameText });
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Add columns', 'Coded values', 'Browse sources', 'Search framing sources: Observation', 'Use this source'],
    capturedCommand: 'SET_FRAME_SOURCE',
    frameId,
  });

  const categoryPanel = page.getByTestId(`frame-categories-${frameId}`);
  if (!(await categoryPanel.isVisible())) {
    await act('open saved Observation coded values', savedFrame.getByRole('button', { name: 'Choose values', exact: true }),
      () => savedFrame.getByRole('button', { name: 'Choose values', exact: true }).click());
  }
  await categoryPanel.waitFor({ state: 'visible' });
  const categorySearch = categoryPanel.getByRole('searchbox', { name: /^Search coded values in / });
  await categorySearch.waitFor({ state: 'visible' });
  const initialCategoryResponse = await initialCategoryResponsePromise;
  assert(initialCategoryResponse.ok(), `Initial native coded-value inventory returned HTTP ${initialCategoryResponse.status()}.`);
  assert.equal(requestBody(initialCategoryResponse.request())?.frameId, frameId,
    'The initial coded-value inventory must belong to the new Observation frame.');
  await act('search Height coded value', categorySearch, () => categorySearch.fill('height'), { editable: true });
  const heightInventoryResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/semantic-inventory') &&
      body?.frameId === frameId && body?.outputId === outputId && body?.query === 'height';
  });
  let heightInventoryBody;
  await act('search native Height categories', categoryPanel.getByRole('button', { name: 'Search', exact: true }),
    () => categoryPanel.getByRole('button', { name: 'Search', exact: true }).click(), {
      after: async () => {
        const response = await heightInventoryResponsePromise;
        assert(response.ok(), `Height semantic inventory returned HTTP ${response.status()}.`);
        heightInventoryBody = await response.json();
        assert.equal(heightInventoryBody.frameId, frameId);
        assert.equal(heightInventoryBody.state, 'complete');
      },
    });
  const independentHeightChoices = heightInventoryBody.entries.filter(entry =>
    entry.resourceType === 'Observation' && entry.system === 'https://example.test/codes' &&
    entry.code === 'height' && entry.display === 'Height');
  assert.equal(independentHeightChoices.length, 1, 'The UI inventory must expose the exact source Height coding once.');
  const heightChoice = categoryPanel.getByRole('checkbox', { name: 'Select Height', exact: true });
  await heightChoice.waitFor({ state: 'visible' });
  check('correctness', 'native Height category matches the independent Observation code identity',
    await heightChoice.isEnabled() && independentHeightChoices.length === 1,
    { frameId, system: independentHeightChoices[0].system, code: independentHeightChoices[0].code,
      display: independentHeightChoices[0].display, sourcePath: independentHeightChoices[0].sourcePath,
      valueSelector: independentHeightChoices[0].valueSelector });
  await act('select Height coded value', heightChoice, () => heightChoice.check());
  const addHeightColumn = categoryPanel.getByRole('button', { name: 'Add 1 column', exact: true });
  const constructionChoiceResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return request.method() === 'POST' && new URL(response.url()).pathname.endsWith('/construction-choice-proposals') &&
      body?.outputId === outputId && body?.constructionChoices?.length === 1 &&
      body.constructionChoices[0]?.frameId === frameId;
  });
  const applyHeightCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    const body = requestBody(request);
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' &&
      body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_CHOICE' &&
        command.constructionChoice?.frameId === frameId);
  });
  await act('add and save the Height coded source column', addHeightColumn, () => addHeightColumn.click(), {
    after: async () => {
      const [proposalResponse, applyResponse] = await Promise.all([
        constructionChoiceResponsePromise, applyHeightCommandPromise,
      ]);
      assert(proposalResponse.ok(), `Native coded-column proposal returned HTTP ${proposalResponse.status()}.`);
      assert(applyResponse.ok(), `Native coded-column save returned HTTP ${applyResponse.status()}.`);
      const proposal = await proposalResponse.json();
      assert.equal(proposal.previewStatus, 'READY', 'The UI-owned coded-column proposal must include a ready preview.');
      assert.equal(proposal.candidateColumnIds?.length, 1, 'The Height proposal must identify one candidate column.');
      const command = requestBody(applyResponse.request());
      assert.equal(command.commands.length, 1, 'The UI must save only the selected Height coded column.');
      assert.equal(command.commands[0].type, 'APPLY_CONSTRUCTION_CHOICE');
      assert.equal(command.commands[0].constructionChoice.frameId, frameId);
    },
  });
  await waitForPreview(page, expectedIDs.length, 2);
  const savedLabel = 'Height';
  const savedPreview = await previewSnapshot(page);
  const actualHeightRows = rowsByObservationID(savedPreview, savedLabel);
  check('correctness', 'saved Height column preview matches the independent raw Observation oracle',
    JSON.stringify(actualHeightRows) === JSON.stringify(expectedHeightRows) &&
      savedPreview.rowCount === expectedIDs.length && savedPreview.columnCount === 2,
    { expectedHeightRows, actualHeightRows, headers: savedPreview.headers,
      rowCount: savedPreview.rowCount, columnCount: savedPreview.columnCount });
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Select Height', 'Add 1 column'],
    capturedRequests: ['POST /construction-choice-proposals → READY preview', 'POST /commands with APPLY_CONSTRUCTION_CHOICE'],
    frameId,
    savedLabel,
  });

  const editedLabel = `Fixture Height ${context.runID.slice(-6)}`;
  await act('open table Columns menu', page.getByRole('button', { name: 'Columns', exact: true }),
    () => page.getByRole('button', { name: 'Columns', exact: true }).click());
  const labelInput = page.getByRole('textbox', { name: `Column name for ${savedLabel}`, exact: true });
  await act('edit saved Height column label', labelInput, () => labelInput.fill(editedLabel), { editable: true });
  const renameResponsePromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'UPDATE_COLUMN');
  });
  await act('save edited Height column label', labelInput, () => labelInput.press('Enter'), {
    after: async () => {
      const response = await renameResponsePromise;
      assert(response.ok(), `Native coded-column edit returned HTTP ${response.status()}.`);
    },
  });
  const editedHeader = page.locator('[data-testid="preview-table-scroll"] [role="columnheader"]')
    .filter({ hasText: new RegExp(editedLabel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i') });
  await editedHeader.waitFor({ state: 'visible' });
  const editedPreview = await previewSnapshot(page);
  const editedHeightRows = rowsByObservationID(editedPreview, editedLabel);
  check('persistence', 'edited coded source column label preserves the exact Height values',
    JSON.stringify(editedHeightRows) === JSON.stringify(expectedHeightRows),
    { editedLabel, expectedHeightRows, actualHeightRows: editedHeightRows, headers: editedPreview.headers });
  report.target.nativeMutationInventory.authoring.push({
    controls: ['Columns', `Column name for ${savedLabel}`, 'Enter'],
    capturedCommand: 'UPDATE_COLUMN',
    editedLabel,
  });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreview(page, expectedIDs.length, 2);
  const reloadedEditedPreview = await previewSnapshot(page);
  const reloadedEditedHeightRows = rowsByObservationID(reloadedEditedPreview, editedLabel);
  check('persistence', 'edited Height column and exact values survive Builder reload',
    JSON.stringify(reloadedEditedHeightRows) === JSON.stringify(expectedHeightRows) &&
      reloadedEditedPreview.headers.some(header => previewHeaderMatches(header, editedLabel)),
    { explorer, outputId, frameId, editedLabel, expectedHeightRows,
      actualHeightRows: reloadedEditedHeightRows, headers: reloadedEditedPreview.headers });

  await act('reopen Add columns after reload', page.getByRole('button', { name: /Add columns:/ }),
    () => page.getByRole('button', { name: /Add columns:/ }).click());
  await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
  await act('open Coded values after reload', page.getByRole('button', { name: 'Coded values', exact: true }),
    () => page.getByRole('button', { name: 'Coded values', exact: true }).click());
  const reloadedFrame = page.getByTestId(`saved-frame-${frameId}`);
  await reloadedFrame.waitFor({ state: 'visible' });
  const removeHeightColumn = reloadedFrame.getByRole('button', { name: `Remove ${editedLabel} column`, exact: true });
  const removeCommandPromise = page.waitForResponse(response => {
    const request = response.request();
    return new URL(response.url()).pathname === commandPath && request.method() === 'POST' && commandHas(request, 'REMOVE_COLUMN');
  });
  await act('remove saved Height coded column', removeHeightColumn, () => removeHeightColumn.click(), {
    after: async () => {
      const response = await removeCommandPromise;
      assert(response.ok(), `Native REMOVE_COLUMN command returned HTTP ${response.status()}.`);
      const command = requestBody(response.request());
      assert.equal(command.commands.length, 1, 'The native removal must affect only the Height coded column.');
      assert.equal(command.commands[0].type, 'REMOVE_COLUMN');
    },
  });
  await waitForPreview(page, expectedIDs.length, 1);
  const removedPreview = await previewSnapshot(page);
  const removedIDIndex = removedPreview.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  const removedIDs = removedPreview.rows.map(row => row[removedIDIndex]).sort();
  check('correctness', 'native Remove column restores the six Observation ID rows',
    JSON.stringify(removedIDs) === JSON.stringify(expectedIDs) &&
      !removedPreview.headers.some(header => previewHeaderMatches(header, editedLabel)),
    { expectedIDs, removedIDs, headers: removedPreview.headers, rowCount: removedPreview.rowCount });
  report.target.nativeMutationInventory.cleanup.push({
    control: `Remove ${editedLabel} column`,
    capturedCommand: 'REMOVE_COLUMN',
    explorerId: explorer,
    outputId,
  });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreview(page, expectedIDs.length, 1);
  const reloadedRemovedPreview = await previewSnapshot(page);
  const finalIDIndex = reloadedRemovedPreview.headers.findIndex(label => previewHeaderMatches(label, 'Observation ID'));
  const finalIDs = reloadedRemovedPreview.rows.map(row => row[finalIDIndex]).sort();
  check('persistence', 'removed coded column stays absent after Builder reload',
    JSON.stringify(finalIDs) === JSON.stringify(expectedIDs) &&
      !reloadedRemovedPreview.headers.some(header => normalizePreviewHeader(header).includes('height')) &&
      reloadedRemovedPreview.columnCount === 1,
    { expectedIDs, finalIDs, headers: reloadedRemovedPreview.headers,
      rowCount: reloadedRemovedPreview.rowCount, columnCount: reloadedRemovedPreview.columnCount });

  checkUnexpectedDiagnostics(report);
  const sourceSHA256After = createHash('sha256').update(readFileSync(sourcePath)).digest('hex');
  recordCheck(report, 'correctness', 'independent Observation source stayed unchanged during browser lifecycle',
    sourceSHA256After === sourceSHA256, { before: sourceSHA256, after: sourceSHA256After });
};
