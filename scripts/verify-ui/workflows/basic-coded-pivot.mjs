import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import {
  BASIC_CODED_PIVOT_EXPECTED_IDS,
  BASIC_CODED_PIVOT_EXPECTED_PATIENT_ID,
  assertBasicCodedPivotPreviewRendered,
  BASIC_CODED_PIVOT_EXPECTED_ROWS,
  BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS,
  assertBasicCodedPivotRendered,
  readBasicCodedPivotFixture,
  readBasicCodedPivotPatientFixture,
} from '../helpers/basic-coded-pivot-fixture.mjs';
import { codedPivotSourceRadioFor } from '../helpers/coded-pivot-fixture.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';

const requestBody = request => {
  try { return request.postDataJSON(); } catch { return undefined; }
};

const unique = async locator => {
  const count = await locator.count();
  assert.equal(count, 1, `Expected one native UI target for ${locator.toString()}, found ${count}`);
  return locator;
};

const normalizeLabel = value => String(value ?? '').replace(/\s+/g, ' ').trim();
export const normalizeRenderedHeader = value => String(value ?? '').split(/\r?\n/, 1)[0]
  .replace(/\s+/g, ' ').trim().toLowerCase();
const maxOwnedResponseBodyBytes = 512 * 1024;
const ownedResponseBodyTimeoutMs = 5000;
const actionToRenderBudgetMs = 5000;

const ownedResponseWaiter = (page, { origin, path, method = 'POST', matches = () => true, timeoutMs = ownedResponseBodyTimeoutMs }) => page.waitForResponse(response => {
  let url;
  try { url = new URL(response.url()); } catch { return false; }
  const body = requestBody(response.request());
  return url.origin === origin && url.pathname === path && response.request().method() === method && matches(body);
}, { timeout: Math.min(actionToRenderBudgetMs, timeoutMs) });

const readBoundedResponseText = async (response, timeoutMs = ownedResponseBodyTimeoutMs) => {
  const declaredBytes = Number(response.headers()['content-length']);
  assert(!Number.isFinite(declaredBytes) || declaredBytes <= maxOwnedResponseBodyBytes,
    `Owned response declares ${declaredBytes} bytes, above the ${maxOwnedResponseBodyBytes}-byte limit.`);
  const read = Promise.resolve().then(() => response.text()).then(
    text => ({ state: 'read', text }),
    error => ({ state: 'error', error }),
  );
  let timer;
  const timeout = new Promise(resolve => {
    timer = setTimeout(() => resolve({ state: 'timeout' }), Math.min(actionToRenderBudgetMs, timeoutMs));
  });
  let result;
  try { result = await Promise.race([read, timeout]); }
  finally { clearTimeout(timer); }
  assert.equal(result.state, 'read', result.state === 'timeout'
    ? `Owned response body read exceeded ${Math.min(actionToRenderBudgetMs, timeoutMs)} ms.`
    : `Owned response body read failed: ${result.error?.message ?? result.error}`);
  assert(Buffer.byteLength(result.text, 'utf8') <= maxOwnedResponseBodyBytes,
    `Owned response body exceeds the ${maxOwnedResponseBodyBytes}-byte limit.`);
  return result.text;
};

const readOwnedResponse = async (response, { origin, path, method = 'POST', request, expectedStatus = 200, timeoutMs = ownedResponseBodyTimeoutMs }) => {
  const rawText = await readBoundedResponseText(response, timeoutMs);
  let body;
  try { body = JSON.parse(rawText); }
  catch { throw new Error(`${method} ${path} returned non-JSON HTTP ${response.status()}: ${rawText.slice(0, 800)}`); }
  const url = new URL(response.url());
  assert.equal(url.origin, origin, `${path} response must come from the owned API origin.`);
  assert.equal(url.pathname, path, `${path} response must use the exact owned route.`);
  assert.equal(response.request().method(), method);
  assert.equal(response.status(), expectedStatus,
    `${method} ${path} expected HTTP ${expectedStatus}, got ${response.status()}: ${JSON.stringify(body)}`);
  return { status: response.status(), request, body };
};

const tableSnapshot = (page, timeoutMs = 5000) => page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
  headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
  rows: [...table.querySelectorAll('[role="row"]')].slice(1)
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
}), undefined, { timeout: timeoutMs });

const proposalSnapshot = (page, timeoutMs = 5000) => page.locator('[data-testid="construction-proposal-preview"]').evaluate(preview => ({
  headers: [...preview.querySelectorAll('thead th')].map(cell => cell.innerText.trim()),
  rows: [...preview.querySelectorAll('[data-testid="construction-proposal-preview-row"]')]
    .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())),
}), undefined, { timeout: timeoutMs });

const codedStepFor = body => body?.candidateConstruction?.steps?.find(step => step.operation?.kind === 'CODED_PIVOT');

export const outputIdFromInitialTableCaptures = (tableCapture, idColumnCapture) => {
  assert.equal(tableCapture.body.commandId, tableCapture.request.commandId);
  assert.equal(idColumnCapture.body.commandId, idColumnCapture.request.commandId);
  const createdTable = tableCapture.body.results?.[0];
  assert.equal(createdTable?.type, 'TABLE_CREATED');
  assert.equal(typeof createdTable?.outputId, 'string');
  assert(createdTable.outputId.length > 0, 'CREATE_TABLE result must provide the new output identity.');
  const addIdColumn = idColumnCapture.request.commands?.[0];
  assert.equal(addIdColumn?.type, 'ADD_COLUMN');
  assert.equal(addIdColumn.outputId, createdTable.outputId);
  const addedIdColumn = idColumnCapture.body.results?.[0];
  assert.equal(addedIdColumn?.type, 'COLUMN_ADDED');
  assert.equal(addedIdColumn.outputId, createdTable.outputId);
  return createdTable.outputId;
};

const proposalMatches = (body, { outputId, categoryCount = 2, duplicatePolicy }) => {
  const step = codedStepFor(body);
  return body?.outputId === outputId && Array.isArray(step?.operation?.codedPivot?.categories) &&
    step.operation.codedPivot.categories.length === categoryCount &&
    (!duplicatePolicy || step.operation.codedPivot.duplicatePolicy === duplicatePolicy);
};

export const openCodedPivotAdvancedValueHandling = async (editor, timeoutFor) => {
  const disclosure = editor.locator('details').filter({ hasText: 'Advanced value handling' });
  assert.equal(await disclosure.count(), 1, 'Advanced value handling must belong to one native details disclosure.');
  const summary = disclosure.locator(':scope > summary');
  assert.equal(await summary.count(), 1, 'Saved Coded Pivot editor must expose one advanced value-handling summary.');
  if (await disclosure.getAttribute('open') === null) {
    await summary.click({ timeout: timeoutFor('advanced value handling disclosure') });
  }
  assert.notEqual(await disclosure.getAttribute('open'), null, 'Advanced value handling must open before reading the saved policy.');
  const policy = editor.getByLabel('When several values match');
  await policy.waitFor({ state: 'visible', timeout: timeoutFor('duplicate policy control') });
  return policy;
};

export const proposalReady = async (page, expectedRows, timeoutFor = () => 30_000) => {
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')
    ?.getAttribute('data-proposal-status') === 'ready', undefined, { timeout: timeoutFor('proposal ready') });
  const panel = page.locator('[data-testid="construction-proposal-panel"]');
  const status = await panel.getAttribute('data-proposal-status', { timeout: timeoutFor('proposal status') });
  const apply = page.getByTestId('construction-apply-proposal');
  assert.equal(status, 'ready', await panel.innerText({ timeout: timeoutFor('proposal details') }));
  assert.equal(await apply.isEnabled({ timeout: timeoutFor('Apply readiness') }), true, 'The native coded Pivot proposal must be applicable.');
  const snapshot = await proposalSnapshot(page, timeoutFor('preview grid'));
  assertBasicCodedPivotPreviewRendered(snapshot, expectedRows);
  return snapshot;
};

const click = (page, action, label, locator, options = {}) => action(label, locator,
  target => target.click({ timeout: options.timeout ?? actionToRenderBudgetMs }), options);

export const basicCodedPivotWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  const beginActionToRender = name => {
    const startedAt = performance.now();
    const deadlineAt = startedAt + actionToRenderBudgetMs;
    const remainingMs = phase => {
      const remaining = Math.floor(deadlineAt - performance.now());
      assert(remaining > 0, `${name} exhausted its ${actionToRenderBudgetMs} ms budget before ${phase}.`);
      return remaining;
    };
    const record = settled => {
      const durationMs = Math.round(performance.now() - startedAt);
      const checkName = `${name} action-to-render within ${actionToRenderBudgetMs} ms`;
      report.timings ??= {};
      report.timings[checkName] = durationMs;
      check('performance', checkName, settled && durationMs <= actionToRenderBudgetMs,
        { durationMs, budgetMs: actionToRenderBudgetMs, settled });
      return durationMs;
    };
    return { name, startedAt, deadlineAt, remainingMs, record };
  };
  const timedAction = (name, locator, perform, settle, deadline = beginActionToRender(name)) => action(
    name,
    locator,
    target => perform(target, deadline.remainingMs('native action')),
    {
      timeout: deadline.remainingMs('action target readiness'),
      budget: actionToRenderBudgetMs,
      requiredCheck: `${name} action-to-render within ${actionToRenderBudgetMs} ms`,
      after: async () => {
        await settle(deadline.remainingMs);
        deadline.remainingMs('settlement completion');
      },
    },
  );
  const expectedRootRows = BASIC_CODED_PIVOT_EXPECTED_IDS.map(id => [id]);
  const waitForExactTable = async (expectedHeaders, expectedRows, timeoutMs, expectedOutputId) => {
    await page.waitForFunction(({ expectedHeaders, expectedRows, expectedOutputId }) => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      if (!table || document.body.innerText.includes('Loading your table…') ||
          document.body.innerText.includes('Preview failed:')) return false;
      if (expectedOutputId) {
        const selected = [...document.querySelectorAll('[data-testid^="construction-table-"]')]
          .find(item => item.getAttribute('data-testid') === `construction-table-${expectedOutputId}`);
        if (selected?.getAttribute('aria-current') !== 'page') return false;
      }
      const headers = [...table.querySelectorAll('[role="columnheader"]')]
        .map(cell => cell.innerText.trim().split(/\r?\n/, 1)[0].replace(/\s+/g, ' ').trim().toLowerCase());
      const rows = [...table.querySelectorAll('[role="row"]')].slice(1)
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()))
        .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
      return JSON.stringify(headers) === JSON.stringify(expectedHeaders) &&
        JSON.stringify(rows) === JSON.stringify([...expectedRows]
          .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))));
    }, { expectedHeaders: expectedHeaders.map(normalizeRenderedHeader), expectedRows, expectedOutputId }, { timeout: timeoutMs });
  };
  const reloadToExactTable = async (name, expectedHeaders, expectedRows, expectedOutputId) => {
    const deadline = beginActionToRender(name);
    let settled = false;
    try {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: deadline.remainingMs('reload') });
      await waitForExactTable(expectedHeaders, expectedRows, deadline.remainingMs('ready grid'), expectedOutputId);
      const snapshot = await tableSnapshot(page, deadline.remainingMs('exact table read'));
      assert.deepEqual(snapshot.headers.map(normalizeRenderedHeader), expectedHeaders.map(normalizeRenderedHeader));
      assert.deepEqual(snapshot.rows.slice().sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))),
        expectedRows.slice().sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))));
      settled = true;
      return snapshot;
    } finally {
      const durationMs = deadline.record(settled);
      const usabilityName = `${name} completed`;
      check('usability', usabilityName, settled && durationMs <= actionToRenderBudgetMs,
        { durationMs, budgetMs: actionToRenderBudgetMs, settled, expectedHeaders, expectedRows, expectedOutputId });
    }
  };
  configureNativePage(page);
  const target = context.target;
  const fixture = readBasicCodedPivotFixture(join(target.fixtureDir, 'Observation.ndjson'));
  const patientFixture = readBasicCodedPivotPatientFixture(join(target.fixtureDir, 'Patient.ndjson'));
  assert.equal(patientFixture.id, BASIC_CODED_PIVOT_EXPECTED_PATIENT_ID);
  report.target.fixtureOracle = {
    path: fixture.path,
    sha256: fixture.sha256,
    observationCount: fixture.observations.length,
    observationIDs: fixture.ids,
    codedRows: fixture.rows,
    codingSystem: 'urn:loom:basic-coded-pivot',
    patientFixturePath: patientFixture.path,
    patientFixtureSha256: patientFixture.sha256,
    requiredPatientID: patientFixture.id,
  };
  check('correctness', 'fresh Basic fixture contains exact literal coded Observation rows',
    context.custom === false && context.seed?.fresh === true && target.fixtureProject.startsWith('loom_dev_verify_') &&
      JSON.stringify(fixture.rows) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_ROWS), report.target.fixtureOracle);

  const uiOrigin = new URL(target.uiUrl).origin;
  const explorerCollectionPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers`;
  const tableName = `Basic coded Pivot ${context.runID}`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible' });
  await click(page, action, 'open New explorer', newExplorer);
  const nameInput = page.locator('#new-explorer-name');
  await click(page, action, 'name a fresh coded Pivot Explorer', nameInput, { editable: true });
  await nameInput.fill(tableName);
  const createResponsePromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: explorerCollectionPath,
    matches: body => body?.title === tableName,
  });
  const createButton = page.getByRole('button', { name: 'Create blank', exact: true });
  await click(page, action, 'create blank coded Pivot Explorer', createButton, {
    after: async () => {
      const response = await createResponsePromise;
      const capture = await readOwnedResponse(response, {
        origin: uiOrigin, path: explorerCollectionPath, request: requestBody(response.request()), expectedStatus: 201,
      });
      assert.equal(typeof capture.body.explorerId, 'string');
    },
  });
  await page.waitForFunction(title => document.querySelector('select[aria-label="Explorer"]')
    ?.selectedOptions[0]?.textContent?.trim() === title, tableName);
  const explorerId = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  assert(explorerId && explorerId !== target.bootstrapExplorerId, 'Native authoring must use the newly created Explorer.');
  report.target.explorer = explorerId;
  const explorerPath = `${explorerCollectionPath}/${encodeURIComponent(explorerId)}`;
  const authoringPath = `${explorerPath}/authoring/v2`;
  const commandPath = `${authoringPath}/commands`;
  const sourceOptionsPath = `${authoringPath}/frame-source-options`;
  const semanticInventoryPath = `${authoringPath}/semantic-inventory`;
  const proposalPath = `${authoringPath}/construction-proposals`;
  const capturedCommands = [];
  page.on('request', request => {
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (url.origin !== uiOrigin || url.pathname !== commandPath || request.method() !== 'POST') return;
    capturedCommands.push({ body: requestBody(request), requestId: request.headers()['x-request-id'] ?? null });
  });

  const firstTableName = page.locator('#first-table-name');
  let outputId;
  let rootSnapshot;
  await firstTableName.waitFor({ state: 'visible' });
  await click(page, action, 'name Observation source table', firstTableName, { editable: true });
  await firstTableName.fill('Observations');
  const createTableDeadline = beginActionToRender('Create Observation source table and render exact rows');
  const createTableResponsePromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: commandPath,
    matches: body => body?.commands?.some(command => command.type === 'CREATE_TABLE'),
    timeoutMs: createTableDeadline.remainingMs('CREATE_TABLE response'),
  });
  const idColumnResponsePromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: commandPath,
    matches: body => body?.commands?.some(command => command.type === 'ADD_COLUMN' && command.title === 'Observation ID'),
    timeoutMs: createTableDeadline.remainingMs('Observation ID response'),
  });
  const chooseRows = page.getByRole('button', { name: 'Choose Observation rows', exact: true });
  await timedAction('Create Observation source table and render exact rows', chooseRows,
    target => target.click({ timeout: createTableDeadline.remainingMs('Choose Observation rows click') }),
    async remainingMs => {
      const [tableResponse, idResponse] = await Promise.all([createTableResponsePromise, idColumnResponsePromise]);
      const [tableCapture, idCapture] = await Promise.all([
        readOwnedResponse(tableResponse, { origin: uiOrigin, path: commandPath, request: requestBody(tableResponse.request()), timeoutMs: remainingMs('CREATE_TABLE response body') }),
        readOwnedResponse(idResponse, { origin: uiOrigin, path: commandPath, request: requestBody(idResponse.request()), timeoutMs: remainingMs('Observation ID response body') }),
      ]);
      outputId = outputIdFromInitialTableCaptures(tableCapture, idCapture);
      await waitForExactTable(['Observation ID'], expectedRootRows, remainingMs('exact source grid'), outputId);
      rootSnapshot = await tableSnapshot(page, remainingMs('source grid read'));
    }, createTableDeadline);
  assert(rootSnapshot, 'The source table action must settle on its exact rendered grid.');
  const rootIDs = rootSnapshot.rows.map(row => row[0]).sort();
  assert.deepEqual(rootIDs, [...BASIC_CODED_PIVOT_EXPECTED_IDS]);
  const tableTestId = await page.locator('[data-testid^="construction-table-"]').getAttribute('data-testid');
  assert(tableTestId, 'Native root table must expose its stable output identity.');
  assert.equal(tableTestId.replace(/^construction-table-/, ''), outputId,
    'The rendered source table must retain the output identity returned by the command.');
  report.target.outputId = outputId;
  check('correctness', 'native Observation source table preserves all four literal row identities',
    JSON.stringify(rootIDs) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_IDS),
    { outputId, headers: rootSnapshot.headers, rootIDs, expectedIDs: BASIC_CODED_PIVOT_EXPECTED_IDS });

  const openCodedEditor = async label => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    if (!(await settings.isVisible())) await settings.waitFor({ state: 'visible' });
    await click(page, action, `${label}: open row settings`, settings);
    const pivot = page.getByTestId('construction-action-pivot-rows');
    await pivot.waitFor({ state: 'visible' });
    const deadline = beginActionToRender(`${label}: open coded Pivot editor with selectable source and categories`);
    const sourceOptionsPromise = ownedResponseWaiter(page, {
      origin: uiOrigin, path: sourceOptionsPath,
      matches: body => body?.outputId === outputId && body?.resourceType === 'Observation',
      timeoutMs: deadline.remainingMs('frame-source-options response'),
    });
    const inventoryResponsePromise = ownedResponseWaiter(page, {
      origin: uiOrigin, path: semanticInventoryPath,
      matches: body => body?.outputId === outputId,
      timeoutMs: deadline.remainingMs('semantic inventory response'),
    });
    const section = page.getByRole('region', { name: 'Coded values as columns' });
    let opened;
    await timedAction(`${label}: choose coded values as columns`, pivot,
      (target, timeoutMs) => target.click({ timeout: timeoutMs }),
      async remainingMs => {
        await section.waitFor({ state: 'visible', timeout: remainingMs('coded Pivot editor') });
        const sourceOptionsResponse = await sourceOptionsPromise;
        const sourceRequest = requestBody(sourceOptionsResponse.request());
        const sourceCapture = await readOwnedResponse(sourceOptionsResponse, {
          origin: uiOrigin, path: sourceOptionsPath, request: sourceRequest,
          timeoutMs: remainingMs('frame-source-options response body'),
        });
        assert.equal(sourceCapture.request.outputId, outputId);
        assert.equal(sourceCapture.request.snapshotToken, sourceCapture.body.snapshotToken);
        assert.equal(sourceCapture.request.limit, 50);
        assert.equal(sourceCapture.body.outputId, outputId);
        assert.equal(sourceCapture.body.complete, true);
        assert(Array.isArray(sourceCapture.body.sources), 'Owned frame-source-options response must include sources.');
        const candidates = sourceCapture.body.sources.filter(option => option.resourceType === 'Observation' &&
          option.route?.length === 0 && option.sourcePath === 'component[]' && option.valuePath === 'valueInteger');
        assert.equal(candidates.length, 1,
          `The small fixture must expose one direct integer component source, got ${JSON.stringify(candidates)}`);
        const selectedSource = candidates[0];
        assert(selectedSource.choiceId && selectedSource.bindingId, 'Direct source option must preserve its choice and binding IDs.');
        report.target.codedPivotSource = {
          choiceId: selectedSource.choiceId,
          bindingId: selectedSource.bindingId,
          resourceType: selectedSource.resourceType,
          sourcePath: selectedSource.sourcePath,
          valuePath: selectedSource.valuePath,
          route: selectedSource.route,
          snapshotToken: sourceCapture.body.snapshotToken,
          outputId,
          status: sourceCapture.status,
        };
        const radio = codedPivotSourceRadioFor(page, normalizeLabel(`${selectedSource.title} ${selectedSource.description}`));
        await radio.waitFor({ state: 'visible', timeout: remainingMs('direct source radio') });
        assert.equal(await radio.count(), 1, 'Native direct integer source must have one rendered source radio.');
        if (!(await radio.isChecked({ timeout: remainingMs('direct source radio state') }))) {
          await radio.click({ timeout: remainingMs('direct source selection') });
        }
        const inventoryResponse = await inventoryResponsePromise;
        const inventoryRequest = requestBody(inventoryResponse.request());
        const inventoryCapture = await readOwnedResponse(inventoryResponse, {
          origin: uiOrigin, path: semanticInventoryPath, request: inventoryRequest,
          timeoutMs: remainingMs('semantic inventory response body'),
        });
        assert.equal(inventoryCapture.request.outputId, outputId);
        assert.equal(inventoryCapture.request.snapshotToken, sourceCapture.body.snapshotToken);
        assert.equal(inventoryCapture.request.sourceChoiceId, selectedSource.choiceId);
        assert.equal(inventoryCapture.body.state, 'complete');
        assert(Array.isArray(inventoryCapture.body.entries), 'Owned semantic inventory response must include entries.');
        const categories = inventoryCapture.body.entries.filter(item => item.system === 'urn:loom:basic-coded-pivot')
          .sort((left, right) => left.code.localeCompare(right.code));
        assert.deepEqual(categories.map(item => [item.code, item.display]).sort(), [['alpha', 'Alpha'], ['beta', 'Beta']]);
        assert(categories.every(item => item.constructionChoice?.choiceId &&
          ['READY', 'READY_WITH_WARNING'].includes(item.readiness?.status)),
        'Every fixture code must have a current selectable semantic construction choice.');
        for (const category of categories) {
          const control = section.locator('label').filter({ hasText: category.display });
          await control.waitFor({ state: 'visible', timeout: remainingMs(`${category.display} category control`) });
          assert.equal(await control.count(), 1, `${category.display} must be visible without typed search or paging.`);
          assert.equal(await control.locator('input[type="checkbox"]').isEnabled({ timeout: remainingMs(`${category.display} category readiness`) }), true,
            `${category.display} must be enabled from the complete semantic inventory.`);
        }
        opened = { section, selectedSource, sourceCapture, inventoryCapture, categories };
      }, deadline);
    return opened;
  };

  const selectCategories = async (label, editor, duplicatePolicy) => {
    let capture;
    let preview;
    for (const [index, category] of editor.categories.entries()) {
      const control = editor.section.locator('label').filter({ hasText: category.display });
      if (index < editor.categories.length - 1) {
        const selectionName = `${label}: select ${category.display} and render checked state`;
        const selectionDeadline = beginActionToRender(selectionName);
        await timedAction(selectionName, control,
          (target, timeoutMs) => target.click({ timeout: timeoutMs }),
          async remainingMs => {
            await page.waitForFunction(display => {
              const labels = [...document.querySelectorAll('section[aria-label="Coded values as columns"] label')];
              const matches = labels.filter(item => [...item.querySelectorAll('strong')]
                .some(strong => strong.innerText.trim() === display));
              return matches.length === 1 && matches[0].querySelector('input[type="checkbox"]')?.checked === true;
            }, category.display, { timeout: remainingMs('checked category render') });
            assert.equal(await control.locator('input[type="checkbox"]').isChecked({
              timeout: remainingMs('checked category assertion'),
            }), true, `Selecting ${category.display} must render it checked before the next category action.`);
          }, selectionDeadline);
        continue;
      }
      const deadline = beginActionToRender(`${label}: select ${category.display} and render coded Pivot preview`);
      const proposalResponsePromise = ownedResponseWaiter(page, {
        origin: uiOrigin, path: proposalPath,
        matches: body => proposalMatches(body, { outputId, duplicatePolicy }),
        timeoutMs: deadline.remainingMs('construction proposal response'),
      });
      await timedAction(`${label}: select ${category.display} and render coded Pivot preview`, control,
        (target, timeoutMs) => target.click({ timeout: timeoutMs }),
        async remainingMs => {
          const proposalResponse = await proposalResponsePromise;
          capture = await readOwnedResponse(proposalResponse, {
            origin: uiOrigin, path: proposalPath, request: requestBody(proposalResponse.request()),
            timeoutMs: remainingMs('proposal response body'),
          });
          assert.equal(capture.request.outputId, outputId);
          assert.equal(capture.request.snapshotToken, editor.sourceCapture.body.snapshotToken);
          assert.equal(typeof capture.body.proposalId, 'string');
          assert.equal(capture.body.previewStatus, 'READY');
          preview = await proposalReady(page, BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS, remainingMs);
        }, deadline);
    }
    return { capture, preview };
  };

  const cancelCommandCount = () => capturedCommands.length;
  const firstEditor = await openCodedEditor('Cancel check');
  const firstPreview = await selectCategories('Cancel check', firstEditor, 'ERROR');
  const commandsBeforeCancel = cancelCommandCount();
  const cancel = page.getByTestId('construction-cancel-proposal');
  let canceledRoot;
  const cancelDeadline = beginActionToRender('Cancel coded Pivot preview and render exact source rows');
  await timedAction('Cancel coded Pivot preview and render exact source rows', cancel,
    (target, timeoutMs) => target.click({ timeout: timeoutMs }),
    async remainingMs => {
      await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]') &&
        !document.querySelector('[data-testid="construction-operation-editor"]'), undefined,
      { timeout: remainingMs('proposal and editor dismissal') });
      await waitForExactTable(['Observation ID'], expectedRootRows, remainingMs('restored source rows'), outputId);
      canceledRoot = await tableSnapshot(page, remainingMs('restored source grid read'));
    }, cancelDeadline);
  assert.equal(cancelCommandCount(), commandsBeforeCancel, 'Cancel must not write a native authoring command.');
  assert.deepEqual(canceledRoot.rows.map(row => row[0]).sort(), [...BASIC_CODED_PIVOT_EXPECTED_IDS]);
  assert.equal(canceledRoot.headers.length, 1, 'Canceled preview must leave the original one-column table intact.');
  assert.equal(firstPreview.preview.rows.length, BASIC_CODED_PIVOT_EXPECTED_ROWS.length);
  check('persistence', 'preview Cancel leaves exact source rows and writes no authoring command',
    cancelCommandCount() === commandsBeforeCancel && canceledRoot.headers.length === 1 &&
      JSON.stringify(canceledRoot.rows.map(row => row[0]).sort()) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_IDS),
    { outputId, commandsBeforeCancel, commandsAfterCancel: cancelCommandCount(), headers: canceledRoot.headers,
      rows: canceledRoot.rows, expectedIDs: BASIC_CODED_PIVOT_EXPECTED_IDS });

  const secondEditor = await openCodedEditor('Apply check');
  const initialProposal = await selectCategories('Apply check', secondEditor, 'ERROR');
  const step = codedStepFor(initialProposal.capture.body);
  assert(step?.id, 'Applied proposal must retain its stable construction step identity.');
  const appliedHistory = page.locator(`[data-testid="construction-history-step-${step.id}"]`);
  const applyDeadline = beginActionToRender('Apply coded Pivot and render exact rows');
  const appliedResult = {};
  const applyOwnedResponsePromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: commandPath,
    matches: body => body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' && command.outputId === outputId),
    timeoutMs: applyDeadline.remainingMs('Apply command response'),
  });
  await timedAction('Apply coded Pivot and render exact rows', page.getByTestId('construction-apply-proposal'),
    (target, timeoutMs) => target.click({ timeout: timeoutMs }),
    async remainingMs => {
      const applyResponse = await applyOwnedResponsePromise;
      appliedResult.applyCapture = await readOwnedResponse(applyResponse, {
        origin: uiOrigin, path: commandPath, request: requestBody(applyResponse.request()),
        timeoutMs: remainingMs('Apply response body'),
      });
      assert.equal(typeof appliedResult.applyCapture.body.commandId, 'string');
      assert.equal(appliedResult.applyCapture.body.commandId, appliedResult.applyCapture.request.commandId);
      assert.equal(appliedResult.applyCapture.body.results?.[0]?.type, 'TABLE_CHANGED');
      assert.equal(appliedResult.applyCapture.body.results?.[0]?.outputId, outputId);
      await waitForExactTable(['Alpha', 'Beta'], BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS,
        remainingMs('applied exact rows'), outputId);
      appliedResult.applied = await tableSnapshot(page, remainingMs('applied grid read'));
      assert.deepEqual(assertBasicCodedPivotRendered(appliedResult.applied), BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS);
      await appliedHistory.waitFor({ state: 'visible', timeout: remainingMs('saved history step') });
    }, applyDeadline);
  const applyCapture = appliedResult.applyCapture;
  const applied = appliedResult.applied;
  check('correctness', 'native coded Pivot preview and Apply preserve exact projected pairs and duplicate-row multiplicity',
    JSON.stringify(assertBasicCodedPivotRendered(applied)) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS),
    { outputId, stepId: step.id, proposal: initialProposal.capture.body.proposalId,
      headers: applied.headers, rows: applied.rows, expectedProjectedRows: BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS });

  const reloaded = await reloadToExactTable('Reload applied coded Pivot to exact ready grid',
    ['Alpha', 'Beta'], BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS, outputId);
  assert.deepEqual(assertBasicCodedPivotRendered(reloaded), BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS);
  await click(page, action, 'select applied coded Pivot history', appliedHistory);
  const edit = page.locator(`[data-testid="construction-edit-step-${step.id}"]`);
  await edit.waitFor({ state: 'visible' });
  const editDeadline = beginActionToRender('Edit saved coded Pivot and render persisted editor choices');
  const editEditorPromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: sourceOptionsPath,
    matches: body => body?.outputId === outputId && body?.resourceType === 'Observation',
    timeoutMs: editDeadline.remainingMs('edit frame-source-options response'),
  });
  const editInventoryPromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: semanticInventoryPath,
    matches: body => body?.outputId === outputId,
    timeoutMs: editDeadline.remainingMs('edit semantic-inventory response'),
  });
  const editEditor = page.getByRole('region', { name: 'Coded values as columns' });
  let editSourceCapture;
  let editInventoryCapture;
  let duplicatePolicy;
  await timedAction('Edit saved coded Pivot and render persisted editor choices', edit,
    (target, timeoutMs) => target.click({ timeout: timeoutMs }),
    async remainingMs => {
      await editEditor.waitFor({ state: 'visible', timeout: remainingMs('coded Pivot edit editor') });
      const editSourceResponse = await editEditorPromise;
      editSourceCapture = await readOwnedResponse(editSourceResponse, {
        origin: uiOrigin, path: sourceOptionsPath, request: requestBody(editSourceResponse.request()),
        timeoutMs: remainingMs('edit source-options response body'),
      });
      assert.equal(editSourceCapture.request.outputId, outputId);
      assert.equal(editSourceCapture.request.snapshotToken, editSourceCapture.body.snapshotToken);
      const editOption = editSourceCapture.body.sources.find(option => option.choiceId === secondEditor.selectedSource.choiceId);
      assert(editOption, 'Edit reload must bind the exact persisted direct source choice.');
      const persistedRadio = codedPivotSourceRadioFor(page, normalizeLabel(`${editOption.title} ${editOption.description}`));
      await persistedRadio.waitFor({ state: 'visible', timeout: remainingMs('persisted source choice') });
      assert.equal(await persistedRadio.isChecked({ timeout: remainingMs('persisted source state') }), true);
      const editInventoryResponse = await editInventoryPromise;
      editInventoryCapture = await readOwnedResponse(editInventoryResponse, {
        origin: uiOrigin, path: semanticInventoryPath, request: requestBody(editInventoryResponse.request()),
        timeoutMs: remainingMs('edit semantic-inventory response body'),
      });
      assert.equal(editInventoryCapture.request.outputId, outputId);
      assert.equal(editInventoryCapture.request.snapshotToken, editSourceCapture.body.snapshotToken);
      assert.equal(editInventoryCapture.request.sourceChoiceId, secondEditor.selectedSource.choiceId);
      assert.equal(editInventoryCapture.body.state, 'complete');
      const editCategories = editInventoryCapture.body.entries.filter(item => item.system === 'urn:loom:basic-coded-pivot');
      assert.deepEqual(editCategories.map(item => [item.code, item.display]).sort(), [['alpha', 'Alpha'], ['beta', 'Beta']]);
      for (const category of editCategories) {
        const control = editEditor.locator('label').filter({ hasText: category.display });
        await control.waitFor({ state: 'visible', timeout: remainingMs(`${category.display} persisted category`) });
        assert.equal(await control.locator('input[type="checkbox"]').isChecked({ timeout: remainingMs(`${category.display} checked state`) }), true,
          `Edit must retain the selected ${category.display} coded category.`);
      }
      duplicatePolicy = await openCodedPivotAdvancedValueHandling(editEditor, remainingMs);
      assert.equal(await duplicatePolicy.inputValue({ timeout: remainingMs('saved duplicate policy value') }), 'ERROR');
    }, editDeadline);
  const policyDeadline = beginActionToRender('Change coded Pivot duplicate policy to SUM and render preview');
  const editedProposalOwnedPromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: proposalPath,
    matches: body => proposalMatches(body, { outputId, duplicatePolicy: 'SUM' }),
    timeoutMs: policyDeadline.remainingMs('SUM proposal response'),
  });
  let editedProposalCapture;
  let editedPreview;
  await timedAction('Change coded Pivot duplicate policy to SUM and render preview', duplicatePolicy,
    (target, timeoutMs) => target.selectOption('SUM', { timeout: timeoutMs }),
    async remainingMs => {
      const editedProposalResponse = await editedProposalOwnedPromise;
      editedProposalCapture = await readOwnedResponse(editedProposalResponse, {
        origin: uiOrigin, path: proposalPath, request: requestBody(editedProposalResponse.request()),
        timeoutMs: remainingMs('SUM proposal response body'),
      });
      assert.equal(editedProposalCapture.body.previewStatus, 'READY');
      editedPreview = await proposalReady(page, BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS, remainingMs);
    }, policyDeadline);
  const editApplyDeadline = beginActionToRender('Apply edited coded Pivot and render exact rows');
  const editApplyResponsePromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: commandPath,
    matches: body => body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' && command.outputId === outputId),
    timeoutMs: editApplyDeadline.remainingMs('edit Apply command response'),
  });
  const editedApplyResult = {};
  await timedAction('Apply edited coded Pivot and render exact rows', page.getByTestId('construction-apply-proposal'),
    (target, timeoutMs) => target.click({ timeout: timeoutMs }),
    async remainingMs => {
      const editApplyResponse = await editApplyResponsePromise;
      editedApplyResult.capture = await readOwnedResponse(editApplyResponse, {
        origin: uiOrigin, path: commandPath, request: requestBody(editApplyResponse.request()),
        timeoutMs: remainingMs('edit Apply response body'),
      });
      assert.equal(editedApplyResult.capture.body.commandId, editedApplyResult.capture.request.commandId);
      assert.equal(editedApplyResult.capture.body.results?.[0]?.type, 'TABLE_CHANGED');
      assert.equal(editedApplyResult.capture.body.results?.[0]?.outputId, outputId);
      await waitForExactTable(['Alpha', 'Beta'], BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS,
        remainingMs('edited exact rows'), outputId);
      editedApplyResult.snapshot = await tableSnapshot(page, remainingMs('edited grid read'));
      assert.deepEqual(assertBasicCodedPivotRendered(editedApplyResult.snapshot), BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS);
    }, editApplyDeadline);
  const editApplyCapture = editedApplyResult.capture;
  const editedApplied = editedApplyResult.snapshot;
  check('persistence', 'editing the coded Pivot policy preserves exact projected pairs and duplicate-row multiplicity',
    JSON.stringify(assertBasicCodedPivotRendered(editedApplied)) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS),
    { outputId, stepId: step.id, duplicatePolicy: 'SUM',
      previewRows: editedPreview.rows, appliedRows: editedApplied.rows, expectedProjectedRows: BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS });

  const editedReload = await reloadToExactTable('Reload edited coded Pivot to exact ready grid',
    ['Alpha', 'Beta'], BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS, outputId);
  assert.deepEqual(assertBasicCodedPivotRendered(editedReload), BASIC_CODED_PIVOT_EXPECTED_PREVIEW_ROWS);
  const reloadedHistory = page.locator(`[data-testid="construction-history-step-${step.id}"]`);
  await click(page, action, 'select coded Pivot for removal', reloadedHistory);
  const remove = page.getByTestId(`construction-remove-step-${step.id}`);
  await remove.waitFor({ state: 'visible' });
  const removalDeadline = beginActionToRender('Remove coded Pivot and render exact restoration preview');
  const removalProposalPromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: proposalPath,
    matches: body => body?.outputId === outputId && body?.removeStepIds?.length === 1 &&
      body.removeStepIds[0] === step.id && body?.candidateConstruction?.steps?.length === 0,
    timeoutMs: removalDeadline.remainingMs('removal proposal response'),
  });
  let removalCapture;
  let restorationPreview;
  await timedAction('Remove coded Pivot and render exact restoration preview', remove,
    (target, timeoutMs) => target.click({ timeout: timeoutMs }),
    async remainingMs => {
      const removalResponse = await removalProposalPromise;
      removalCapture = await readOwnedResponse(removalResponse, {
        origin: uiOrigin, path: proposalPath, request: requestBody(removalResponse.request()),
        timeoutMs: remainingMs('removal proposal response body'),
      });
      assert.equal(removalCapture.body.previewStatus, 'READY');
      await page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')
        ?.getAttribute('data-proposal-status') === 'ready', undefined,
      { timeout: remainingMs('removal proposal ready state') });
      const apply = page.getByTestId('construction-apply-proposal');
      assert.equal(await apply.isEnabled({ timeout: remainingMs('removal Apply readiness') }), true);
      restorationPreview = await proposalSnapshot(page, remainingMs('restoration preview grid'));
      assert.deepEqual(restorationPreview.headers.map(normalizeRenderedHeader), [normalizeRenderedHeader('Observation ID')]);
      assert.deepEqual(restorationPreview.rows.map(row => row[0]).sort(), [...BASIC_CODED_PIVOT_EXPECTED_IDS]);
    }, removalDeadline);
  assert.equal(removalCapture.body.previewStatus, 'READY');
  const removalApplyDeadline = beginActionToRender('Apply coded Pivot removal and render exact restored rows');
  const removalApplyOwnedPromise = ownedResponseWaiter(page, {
    origin: uiOrigin, path: commandPath,
    matches: body => body?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_PROPOSAL' && command.outputId === outputId),
    timeoutMs: removalApplyDeadline.remainingMs('removal Apply command response'),
  });
  let removalApplyCapture;
  let restored;
  await timedAction('Apply coded Pivot removal and render exact restored rows', page.getByTestId('construction-apply-proposal'),
    (target, timeoutMs) => target.click({ timeout: timeoutMs }),
    async remainingMs => {
      const removalApplyResponse = await removalApplyOwnedPromise;
      removalApplyCapture = await readOwnedResponse(removalApplyResponse, {
        origin: uiOrigin, path: commandPath, request: requestBody(removalApplyResponse.request()),
        timeoutMs: remainingMs('removal Apply response body'),
      });
      assert.equal(removalApplyCapture.body.commandId, removalApplyCapture.request.commandId);
      assert.equal(removalApplyCapture.body.results?.[0]?.type, 'TABLE_CHANGED');
      assert.equal(removalApplyCapture.body.results?.[0]?.outputId, outputId);
      await waitForExactTable(['Observation ID'], expectedRootRows, remainingMs('restored exact rows'), outputId);
      restored = await tableSnapshot(page, remainingMs('restored grid read'));
    }, removalApplyDeadline);
  assert.deepEqual(restored.headers.map(normalizeRenderedHeader), [normalizeRenderedHeader('Observation ID')]);
  assert.deepEqual(restored.rows.map(row => row[0]).sort(), [...BASIC_CODED_PIVOT_EXPECTED_IDS]);
  check('persistence', 'removing coded Pivot restores every exact source row',
    JSON.stringify(restored.rows.map(row => row[0]).sort()) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_IDS),
    { outputId, stepId: step.id, headers: restored.headers, rows: restored.rows, expectedIDs: BASIC_CODED_PIVOT_EXPECTED_IDS });

  const restoredReload = await reloadToExactTable('Reload removed coded Pivot to exact ready grid',
    ['Observation ID'], expectedRootRows, outputId);
  assert.deepEqual(restoredReload.headers.map(normalizeRenderedHeader), [normalizeRenderedHeader('Observation ID')]);
  assert.deepEqual(restoredReload.rows.map(row => row[0]).sort(), [...BASIC_CODED_PIVOT_EXPECTED_IDS]);
  report.target.codedPivotLifecycle = {
    fixtureSha256: fixture.sha256,
    patientFixtureSha256: patientFixture.sha256,
    outputId,
    stepId: step.id,
    sourceChoiceId: secondEditor.selectedSource.choiceId,
    sourceBindingId: secondEditor.selectedSource.bindingId,
    categoryChoices: secondEditor.categories.map(({ system, code, display }) => ({ system, code, display })),
    commands: capturedCommands.map(({ body }) => body?.commands?.map(command => command.type) ?? []),
    appliedRows: applied.rows,
    editedRows: editedApplied.rows,
    restoredRows: restoredReload.rows,
    restoredHeaders: restoredReload.headers,
  };
  check('persistence', 'removal survives reload with only the original Observation ID column',
    JSON.stringify(restoredReload.rows.map(row => row[0]).sort()) === JSON.stringify(BASIC_CODED_PIVOT_EXPECTED_IDS) &&
      restoredReload.headers.map(normalizeRenderedHeader).join('|') === normalizeRenderedHeader('Observation ID'),
    report.target.codedPivotLifecycle);
};
