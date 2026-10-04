import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { executeScenario, runPlaywrightCase, browserURL } from './common.mjs';
import { recordCheck } from './report.mjs';

const patientOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const ids = patients.map(patient => patient.id).sort();
  assert.deepEqual(ids, ['dev-patient-001', 'dev-patient-002'], 'Builder fixture oracle must contain exactly the two independent Patient identities');
  return { path, sha256: createHash('sha256').update(bytes).digest('hex'), ids };
};

const assertPatientRows = (rows, expectedIDs) => {
  const actualIDs = rows.map(row => {
    const matches = expectedIDs.filter(id => row.includes(id));
    return matches.length === 1 ? matches[0] : `INVALID:${row}`;
  }).sort();
  assert.deepEqual(actualIDs, [...expectedIDs].sort(), 'Preview must contain the exact independent fixture Patient identities');
  return actualIDs;
};

const previewRows = async page => page.getByTestId('preview-table-scroll').getByRole('row').allInnerTexts();

const checkPreviewPatients = async (page, report, expectedIDs, check) => {
  const table = page.getByTestId('preview-table-scroll').getByRole('table');
  await table.waitFor({ state: 'visible', timeout: 30000 });
  const rows = await previewRows(page);
  const ids = assertPatientRows(rows.slice(1), expectedIDs);
  check('correctness', 'Preview renders both independent fixture Patients', ids.length === expectedIDs.length,
    { rows, patientIDs: ids, expectedPatientIDs: expectedIDs });
  recordCheck(report, 'correctness', 'automatic Preview is visible after authoring', true);
  return rows;
};

const currentPreviewReady = () => {
  const workspace = document.querySelector('[data-testid="construction-workspace"]');
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const selectedTable = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
  const outputId = selectedTable?.getAttribute('data-testid')?.slice('construction-table-'.length);
  const pending = [...document.querySelectorAll('[role="status"]')].some(node =>
    /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(node.innerText?.trim() || ''));
  return Boolean(workspace && preview && outputId && !pending && preview.dataset.previewStatus === 'ready' &&
    preview.dataset.previewReceiptId && preview.dataset.previewOutputId === outputId &&
    preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
    preview.dataset.currentDraftDigest === workspace.dataset.draftDigest);
};

const installFirstTableObserver = async page => page.evaluate(() => {
  const key = '__loomPlaywrightFirstTableAddColumnsObserver';
  window[key]?.observer?.disconnect?.();
  const events = [];
  const sample = source => {
    const workspace = document.querySelector('[data-testid="construction-workspace"]');
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    const ready = document.body.innerText.includes('DATASET WORKSPACE') && document.body.innerText.includes('Patients') &&
      Boolean(workspace) && Boolean(document.querySelector('button[aria-label^="Select Patient ID"]'));
    if (!ready || !button || button.disabled) return;
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const selectedTable = document.querySelector('[data-testid^="construction-table-"][aria-current="page"]');
    const outputId = selectedTable?.getAttribute('data-testid')?.slice('construction-table-'.length);
    const pending = [...document.querySelectorAll('[role="status"]')].some(node =>
      /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(node.innerText?.trim() || ''));
    const acceptedCurrentPreview = Boolean(workspace && preview && outputId && !pending &&
      preview.dataset.previewStatus === 'ready' && preview.dataset.previewReceiptId &&
      preview.dataset.previewOutputId === outputId && preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
      preview.dataset.currentDraftDigest === workspace.dataset.draftDigest);
    events.push({
      source,
      enabled: true,
      acceptedCurrentPreview,
      selectedOutputId: outputId || '',
      draftVersion: workspace?.dataset.draftVersion || '',
      previewDraftVersion: document.querySelector('[data-testid="construction-preview"]')?.dataset.currentDraftVersion || '',
      at: Math.round(performance.now()),
    });
  };
  const observer = new MutationObserver(() => sample('mutation'));
  observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: [
    'disabled', 'aria-current', 'data-preview-status', 'data-preview-receipt-id', 'data-preview-output-id',
    'data-current-draft-version', 'data-current-draft-digest', 'data-draft-version', 'data-draft-digest',
  ] });
  window[key] = { events, observer, sample };
  sample('installed');
});

const finishFirstTableObserver = async page => page.evaluate(() => {
  const key = '__loomPlaywrightFirstTableAddColumnsObserver';
  const probe = window[key];
  if (!probe) throw new Error('first-table Add columns observer was not installed');
  probe.sample('final');
  probe.observer.disconnect();
  const events = [...probe.events];
  delete window[key];
  return events;
});

const createBlankExplorerWithUI = async ({ page, action, target, context, check }, label) => {
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  const newExplorer = page.getByText('New explorer', { exact: true });
  await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
  });
  const title = `Verify ${context.runID.slice(-10)} ${label}`;
  const name = page.locator('#new-explorer-name');
  await action('name blank Explorer', name, () => name.fill(title), { editable: true });
  const create = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create blank Explorer', create, () => create.click(), {
    timeout: 10000,
    after: () => page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected && document.body.innerText.includes('Build your first table'), title),
  });
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  check('correctness', 'created a fresh Explorer distinct from the bootstrap', Boolean(explorer && explorer !== target.bootstrapExplorerId), { explorer, title });
  assert(explorer && explorer !== target.bootstrapExplorerId, 'Builder controls case must create a fresh Explorer');
  return { explorer, title };
};

const createPatientTableWithUI = async ({ page, action }, expectedIDs) => {
  const name = page.locator('#first-table-name');
  await action('name Patient table', name, () => name.fill('Patients'), { editable: true });
  const choose = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await action('create Patient table and render Preview', choose, () => choose.click(), {
    timeout: 30000,
    budget: 5000,
    after: async () => {
      await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 30000 });
      await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), expectedIDs.length + 1, { timeout: 30000 });
    },
  });
};

const configurePatientGenderWithUI = async ({ page, action }) => {
  const addColumns = page.getByRole('button', { name: /Add columns:/ });
  await action('open Add columns', addColumns, () => addColumns.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' }),
  });
  const fields = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await action('open Fields and related data', fields, () => fields.click());
  const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  await action('open raw Patient fields', rawFields, () => rawFields.click());
  const gender = page.getByRole('checkbox', { name: 'Select Patient.gender', exact: true });
  await action('select Patient Gender', gender, () => gender.check());
  const addFeature = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  await action('add Gender feature', addFeature, () => addFeature.click(), {
    after: () => page.getByRole('button', { name: 'Apply columns', exact: true }).waitFor({ state: 'visible' }),
  });
  const applyColumns = page.getByRole('button', { name: 'Apply columns', exact: true });
  await action('apply Gender and render Preview', applyColumns, () => applyColumns.click(), {
    timeout: 30000,
    budget: 5000,
    after: async () => {
      await page.getByRole('button', { name: /^Select Gender/ }).waitFor({ state: 'visible', timeout: 30000 });
      await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), 3, { timeout: 30000 });
    },
  });
};

const recompile = context => runPlaywrightCase(context, 'builder-controls', 'recompile', async ({ page, report, action, fault, check }) => {
  const oracle = patientOracle(context.target);
  report.target.fixtureOracle = { path: oracle.path, sha256: oracle.sha256, patientIDs: oracle.ids };
  const { explorer } = await createBlankExplorerWithUI({ page, action, target: context.target, context, check }, 'recompile');
  report.target.explorer = explorer;
  const project = context.target.fixtureProject;
  const path = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/reconcile`;
  const injected = await fault({
    method: 'POST',
    path,
    response: {
      status: 422,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 'VERIFY_COMPILE_REJECTED',
        message: 'Controlled compilation rejection for the Recompile regression.',
        diagnostics: [{ severity: 'error', code: 'VERIFY_COMPILE_REJECTED', message: 'Controlled compilation rejection.' }],
      }),
    },
  });
  const alert = page.getByRole('alert');
  const recompileButton = page.getByRole('button', { name: 'Recompile', exact: true });
  const name = page.locator('#first-table-name');
  await action('name Patient table for Recompile recovery', name, () => name.fill('Patients'), { editable: true });
  const choose = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await action('trigger automatic compilation rejection', choose, () => choose.click(), {
    timeout: 30000,
    budget: 5000,
    after: async () => Promise.all([
      alert.waitFor({ state: 'visible', timeout: 30000 }),
      recompileButton.waitFor({ state: 'visible', timeout: 30000 }),
    ]),
  });
  await alert.waitFor({ state: 'visible', timeout: 15000 });
  check('correctness', 'controlled automatic compilation failure is visible', Boolean((await alert.innerText()).trim()),
    { project, explorer, path, message: await alert.innerText(), injectedCount: injected.count() });
  check('correctness', 'controlled compilation rejection was injected exactly once', injected.count() === 1,
    { origin: new URL(context.target.uiUrl).origin, method: 'POST', path, count: injected.count() });
  const reconcileResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
    new URL(response.url()).origin === new URL(context.target.uiUrl).origin && new URL(response.url()).pathname === path, { timeout: 5000 });
  await action('Recompile invokes the backend compiler', recompileButton, () => recompileButton.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      const response = await reconcileResponse;
      assert(response.status() >= 200 && response.status() < 300, `Recompile returned HTTP ${response.status()}`);
      await alert.waitFor({ state: 'hidden', timeout: 5000 });
      await page.waitForFunction(currentPreviewReady, undefined, { timeout: 5000 });
    },
  });
  const response = await reconcileResponse;
  check('correctness', 'Recompile returned a successful compilation response', response.status() >= 200 && response.status() < 300,
    { status: response.status(), path, project, explorer });
  await checkPreviewPatients(page, report, oracle.ids, check);
  const after = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'independent Patient source stayed unchanged during Builder verification', after === oracle.sha256,
    { before: oracle.sha256, after, path: oracle.path });
});

const firstTable = context => runPlaywrightCase(context, 'builder-controls', 'first-table', async ({ page, report, action, check }) => {
  const oracle = patientOracle(context.target);
  report.target.fixtureOracle = { path: oracle.path, sha256: oracle.sha256, patientIDs: oracle.ids };
  const { explorer } = await createBlankExplorerWithUI({ page, action, target: context.target, context, check }, 'first-table');
  report.target.explorer = explorer;
  const tableName = page.locator('#first-table-name');
  await action('name first Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
  await page.waitForFunction(() => {
    const button = document.querySelector('button[aria-label="Choose Patient rows"]');
    return Boolean(button && !button.disabled);
  }, undefined, { timeout: 30000 });
  await installFirstTableObserver(page);
  let availabilityEvents;
  let observerInstalled = true;
  try {
    const choose = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('create verified-ID Patient first table with accepted preview', choose, () => choose.click(), {
      timeout: 30000,
      budget: 5000,
      after: async () => {
        await page.waitForFunction(currentPreviewReady, undefined, { timeout: 30000 });
        const add = page.getByTestId('construction-action-add-columns');
        await add.waitFor({ state: 'visible', timeout: 5000 });
        assert(await add.isEnabled(), 'Add columns remains disabled after an accepted current-draft preview');
        availabilityEvents = await finishFirstTableObserver(page);
        observerInstalled = false;
      },
    });
  } finally {
    if (observerInstalled) {
      try { availabilityEvents = await finishFirstTableObserver(page); }
      catch (error) { report.errors.push({ kind: 'observer-cleanup-error', message: error instanceof Error ? error.message : String(error) }); }
    }
    if (availabilityEvents) report.target.firstTableAddColumnsAvailability = availabilityEvents;
  }
  const events = availabilityEvents ?? [];
  const prematureEvents = events.filter(event => !event.acceptedCurrentPreview);
  check('correctness', 'Add columns stays disabled until a current-draft preview is accepted', prematureEvents.length === 0,
    { enabledEventsBeforePreview: prematureEvents.length, events });
  const acceptedMutation = events.find(event => event.source === 'mutation' && event.acceptedCurrentPreview);
  check('correctness', 'Add columns becomes enabled after the accepted current-draft preview', Boolean(acceptedMutation),
    { acceptedPreviewEvent: acceptedMutation ?? null, eventCount: events.length });
  const patientID = page.getByRole('button', { name: /^Select Patient ID/ });
  const patientIDCount = await patientID.count();
  check('correctness', 'first Patient table uses its verified ID field', patientIDCount === 1 && await patientID.isVisible(),
    { count: patientIDCount, accessibleName: patientIDCount === 1 ? await patientID.getAttribute('aria-label') : null });
  await checkPreviewPatients(page, report, oracle.ids, check);

  const addColumns = page.getByTestId('construction-action-add-columns');
  await action('open Add columns from verified-ID first table', addColumns, () => addColumns.click(), {
    after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' }),
  });
  check('usability', 'Add columns opens from the verified-ID first table', await page.locator('[aria-label="Add columns editor"]').isVisible());
  const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
  await action('close Add columns on verified-ID first table', closeEditor, () => closeEditor.click(), {
    after: async () => {
      await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'hidden' });
      await addColumns.waitFor({ state: 'visible' });
    },
  });
  check('usability', 'Add columns closes from the verified-ID first table', !await page.locator('[aria-label="Add columns editor"]').isVisible());
  const sourceAfter = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'independent Patient source stayed unchanged during Builder verification', sourceAfter === oracle.sha256,
    { before: oracle.sha256, after: sourceAfter, path: oracle.path });
});

const tables = context => runPlaywrightCase(context, 'builder-controls', 'tables', async ({ page, report, action, check }) => {
  const oracle = patientOracle(context.target);
  report.target.fixtureOracle = { path: oracle.path, sha256: oracle.sha256, patientIDs: oracle.ids };
  const created = await createBlankExplorerWithUI({ page, action, target: context.target, context, check }, 'controls');
  report.target.explorer = created.explorer;
  await createPatientTableWithUI({ page, action }, oracle.ids);
  await checkPreviewPatients(page, report, oracle.ids, check);
  await configurePatientGenderWithUI({ page, action });
  await checkPreviewPatients(page, report, oracle.ids, check);
  const selectedTable = page.locator('[data-testid^="construction-table-"][aria-current="page"]');
  assert.equal(await selectedTable.count(), 1, 'Prepared Explorer must have exactly one selected table');
  const originalTableTestId = await selectedTable.getAttribute('data-testid');
  assert(originalTableTestId, 'Prepared Explorer selected table must expose its identity');
  report.target.originalTableTestId = originalTableTestId;

  const duplicate = page.getByTestId('construction-duplicate-table');
  await action('duplicate configured table', duplicate, () => duplicate.click(), {
    after: async () => {
      await page.getByTestId('construction-table-patients-copy').waitFor({ state: 'visible' });
      await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-table-"]').length === 2);
    },
  });
  const selectedAfterDuplicate = page.locator('[data-testid^="construction-table-"][aria-current="page"]');
  check('persistence', 'newly duplicated table is selected immediately',
    await selectedAfterDuplicate.getAttribute('data-testid') === 'construction-table-patients-copy',
    { selectedTableTestId: await selectedAfterDuplicate.getAttribute('data-testid') });

  const project = context.target.fixtureProject;
  const commandsPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(created.explorer)}/authoring/v2/commands`;
  const renameResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
    new URL(response.url()).origin === new URL(context.target.uiUrl).origin && new URL(response.url()).pathname === commandsPath, { timeout: 5000 });
  page.once('dialog', async dialog => {
    report.target.renameDialogType = dialog.type();
    await dialog.accept('Renamed Patients');
  });
  const rename = page.getByTestId('construction-rename-table-patients-copy');
  await action('rename duplicated table', rename, () => rename.click(), {
    after: async () => {
      await renameResponse;
      await page.getByTestId('construction-table-patients-copy').filter({ hasText: 'Renamed Patients' }).waitFor({ state: 'visible' });
    },
  });
  const renameResult = await renameResponse;
  check('correctness', 'rename request returned success', renameResult.status() >= 200 && renameResult.status() < 300,
    { status: renameResult.status(), path: commandsPath, dialogType: report.target.renameDialogType });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(({ explorer, count }) =>
    document.querySelector('select[aria-label="Explorer"]')?.value === explorer &&
    document.querySelectorAll('[data-testid^="construction-table-"]').length === count &&
    document.body.innerText.includes('DATASET WORKSPACE'),
  { explorer: created.explorer, count: 2 }, { timeout: 30000 });
  const renamedTable = page.getByTestId('construction-table-patients-copy');
  check('persistence', 'duplicated and renamed tables survive reload', await renamedTable.innerText().then(text => text.includes('Renamed Patients')));
  let selectedAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
  check('persistence', 'newly duplicated table selection survives reload', selectedAfterReload === 'construction-table-patients-copy',
    { selectedTableTestId: selectedAfterReload });

  const originalTable = page.getByTestId(originalTableTestId);
  await action('select original table manually', originalTable, () => originalTable.click(), {
    after: () => page.waitForFunction(testId => document.querySelector(`[data-testid="${CSS.escape(testId)}"]`)?.getAttribute('aria-current') === 'page', originalTableTestId),
  });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(({ explorer, testId }) => document.querySelector('select[aria-label="Explorer"]')?.value === explorer &&
    document.querySelector(`[data-testid="${CSS.escape(testId)}"]`)?.getAttribute('aria-current') === 'page',
  { explorer: created.explorer, testId: originalTableTestId }, { timeout: 30000 });
  selectedAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
  check('persistence', 'manual table selection survives reload', selectedAfterReload === originalTableTestId,
    { expectedTableTestId: originalTableTestId, selectedTableTestId: selectedAfterReload });

  const renamedCopy = page.getByTestId('construction-table-patients-copy');
  await action('select renamed table before deletion', renamedCopy, () => renamedCopy.click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-table-patients-copy"]')?.getAttribute('aria-current') === 'page'),
  });
  page.once('dialog', async dialog => {
    report.target.deleteDialogType = dialog.type();
    await dialog.accept();
  });
  const deleteButton = page.getByTestId('construction-delete-table');
  await action('delete duplicated table', deleteButton, () => deleteButton.click(), {
    after: async () => {
      await page.getByTestId('construction-table-patients').waitFor({ state: 'visible' });
      await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-table-"]').length === 1 &&
        document.querySelector('[data-testid="construction-table-patients"]')?.getAttribute('aria-current') === 'page');
    },
  });
  check('persistence', 'selected-table deletion immediately falls back to the remaining table',
    await page.getByTestId('construction-table-patients').getAttribute('aria-current') === 'page',
    { selectedTableTestId: await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid'), dialogType: report.target.deleteDialogType });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(({ explorer, count }) => document.querySelector('select[aria-label="Explorer"]')?.value === explorer &&
    document.querySelectorAll('[data-testid^="construction-table-"]').length === count &&
    document.body.innerText.includes('DATASET WORKSPACE'), { explorer: created.explorer, count: 1 }, { timeout: 30000 });
  const renamedStillPresent = await page.getByTestId('construction-table-patients-copy').count();
  check('persistence', 'deleted table stays absent after reload', renamedStillPresent === 0, { count: renamedStillPresent });
  selectedAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
  check('persistence', 'selected-table removal falls back to the remaining table after reload', selectedAfterReload === originalTableTestId,
    { expectedTableTestId: originalTableTestId, selectedTableTestId: selectedAfterReload });
  await checkPreviewPatients(page, report, oracle.ids, check);

  const newExplorer = page.getByText('New explorer', { exact: true });
  await action('open Explorer copy creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
  });
  const copyTitle = `Copy ${context.runID}`;
  const explorerName = page.locator('#new-explorer-name');
  await action('name copied Explorer', explorerName, () => explorerName.fill(copyTitle), { editable: true });
  const copyOption = page.getByRole('checkbox', { name: 'Start with a copy of the current explorer', exact: true });
  await action('select copy current Explorer option', copyOption, () => copyOption.check());
  const copyButton = page.getByRole('button', { name: 'Create copy', exact: true });
  await action('copy configured Explorer', copyButton, () => copyButton.click(), {
    timeout: 30000,
    budget: 5000,
    after: () => page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected &&
      Boolean(document.querySelector('button[aria-label^="Select Patient ID"]')) && Boolean(document.querySelector('button[aria-label^="Select Gender"]')), copyTitle, { timeout: 30000 }),
  });
  report.target.sourceExplorer = created.explorer;
  const copyExplorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  report.target.explorer = copyExplorer;
  check('correctness', 'copied Explorer is distinct from its source', Boolean(copyExplorer && copyExplorer !== created.explorer),
    { sourceExplorer: created.explorer, copiedExplorer: copyExplorer, title: copyTitle });
  await checkPreviewPatients(page, report, oracle.ids, check);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(({ explorer, title }) => document.querySelector('select[aria-label="Explorer"]')?.value === explorer &&
    document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === title &&
    Boolean(document.querySelector('button[aria-label^="Select Patient ID"]')) &&
    Boolean(document.querySelector('button[aria-label^="Select Gender"]')), { explorer: copyExplorer, title: copyTitle }, { timeout: 30000 });
  check('persistence', 'copied Explorer retains configured fields after reload', true,
    { sourceExplorer: created.explorer, copiedExplorer: copyExplorer, title: copyTitle });
  await checkPreviewPatients(page, report, oracle.ids, check);

  page.once('dialog', async dialog => {
    report.target.emptyWorkspaceDeleteDialogType = dialog.type();
    await dialog.accept();
  });
  const deleteLastTable = page.getByTestId('construction-delete-table');
  await action('delete the last configured table', deleteLastTable, () => deleteLastTable.click(), {
    after: async () => {
      await page.getByText('Build your first table', { exact: true }).waitFor({ state: 'visible' });
      await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-table-"]').length === 0);
    },
  });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByText('Build your first table', { exact: true }).waitFor({ state: 'visible', timeout: 30000 });
  check('persistence', 'deleting the last table persists an empty workspace', await page.locator('[data-testid^="construction-table-"]').count() === 0,
    { explorer: copyExplorer, title: copyTitle });
  await createPatientTableWithUI({ page, action }, oracle.ids);
  await checkPreviewPatients(page, report, oracle.ids, check);
  await configurePatientGenderWithUI({ page, action });
  await checkPreviewPatients(page, report, oracle.ids, check);
  const sourceAfter = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'independent Patient source stayed unchanged during Builder verification', sourceAfter === oracle.sha256,
    { before: oracle.sha256, after: sourceAfter, path: oracle.path });
});

export const runBuilderControls = async (context, cases) => {
  const reports = [];
  for (const name of cases) reports.push(await (name === 'recompile' ? recompile(context) : name === 'first-table' ? firstTable(context) : tables(context)));
  return reports;
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  await executeScenario({ id: 'builder-controls', argv: process.argv.slice(2), runner: runBuilderControls, mutating: true });
}
