import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { recordCheck } from '../helpers/report.mjs';

export const patientOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const ids = patients.map(patient => patient.id).sort();
  assert.deepEqual(ids, ['dev-patient-001', 'dev-patient-002'], 'Builder fixture oracle must contain exactly the two independent Patient identities');
  const genderByID = Object.fromEntries(patients.map(patient => [patient.id, patient.gender ?? null]));
  return { path, sha256: createHash('sha256').update(bytes).digest('hex'), ids, genderByID };
};

export const assertPatientRows = (rows, expectedIDs, expectedGenderByID, headerRow) => {
  const actualIDs = rows.map(row => {
    const matches = expectedIDs.filter(id => row.includes(id));
    return matches.length === 1 ? matches[0] : `INVALID:${row}`;
  }).sort();
  assert.deepEqual(actualIDs, [...expectedIDs].sort(), 'Preview must contain the exact independent fixture Patient identities');
  if (expectedGenderByID) {
    assert.deepEqual(Object.keys(expectedGenderByID).sort(), [...expectedIDs].sort(),
      'Gender oracle must cover exactly the independent fixture Patient identities');
    const headerCells = (headerRow ?? '').split(/\r?\n/).map(cell => cell.trim().toLowerCase());
    assert.ok(headerCells.includes('gender'), 'Preview must show the Gender column header for its source-value oracle');
    const actualGenderByID = rows.map(row => {
      const cells = row.split(/\r?\n/).map(cell => cell.trim());
      const matchingIDs = expectedIDs.filter(id => cells.includes(id));
      assert.equal(matchingIDs.length, 1, `Preview row must identify one independent fixture Patient: ${row}`);
      const genderCell = cells.at(-1);
      assert.notEqual(genderCell, matchingIDs[0], `Preview row must include a Gender cell: ${row}`);
      return [matchingIDs[0], genderCell === '—' ? null : genderCell];
    }).sort(([left], [right]) => left.localeCompare(right));
    const expectedGenderPairs = Object.entries(expectedGenderByID).sort(([left], [right]) => left.localeCompare(right));
    assert.deepEqual(actualGenderByID, expectedGenderPairs,
      'Preview must preserve the exact independent fixture Patient Gender values, including null');
  }
  return actualIDs;
};

export const tableIdentityByTitle = async (page, title) => {
  const tab = page.getByRole('button', { name: title, exact: true });
  assert.equal(await tab.count(), 1, `Expected exactly one table tab titled "${title}"`);
  const testId = await tab.getAttribute('data-testid');
  const prefix = 'construction-table-';
  assert.ok(testId?.startsWith(prefix) && testId.length > prefix.length,
    `Table tab "${title}" must expose its output-derived test ID`);
  return { testId, outputId: testId.slice(prefix.length) };
};

const previewRows = async page => page.getByTestId('preview-table-scroll').getByRole('row').allInnerTexts();

const recordPreviewPatients = (rows, ids, report, expectedIDs, check, expectedGenderByID) => {
  check('correctness', 'Preview renders both independent fixture Patients', ids.length === expectedIDs.length,
    { rows, patientIDs: ids, expectedPatientIDs: expectedIDs, ...(expectedGenderByID ? { expectedGenderByID } : {}) });
  recordCheck(report, 'correctness', 'automatic Preview is visible after authoring', true);
  return rows;
};

const checkPreviewPatients = async (page, report, expectedIDs, check, expectedGenderByID, timeoutMs = 5000) => {
  const table = page.getByTestId('preview-table-scroll').getByRole('table');
  await table.waitFor({ state: 'visible', timeout: timeoutMs });
  const rows = await previewRows(page);
  const ids = assertPatientRows(rows.slice(1), expectedIDs, expectedGenderByID, rows[0]);
  return recordPreviewPatients(rows, ids, report, expectedIDs, check, expectedGenderByID);
};

export const currentPreviewReady = expectedOutputId => {
  const workspace = document.querySelector('[data-testid="construction-workspace"]');
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const selectedTables = document.querySelectorAll('[data-testid^="construction-table-"][aria-current="page"]');
  const selectedTable = selectedTables.length === 1 ? selectedTables[0] : null;
  const outputId = selectedTable?.getAttribute('data-testid')?.slice('construction-table-'.length);
  const pending = [...document.querySelectorAll('[role="status"]')].some(node =>
    /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(node.innerText?.trim() || ''));
  return Boolean(workspace && preview && outputId && !pending && preview.dataset.previewStatus === 'ready' &&
    preview.dataset.previewReceiptId && preview.dataset.previewOutputId === outputId &&
    (!expectedOutputId || outputId === expectedOutputId) &&
    preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
    preview.dataset.currentDraftDigest === workspace.dataset.draftDigest);
};

const readCurrentPreviewSnapshot = expectedOutputId => {
  const workspace = document.querySelector('[data-testid="construction-workspace"]');
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const selectedTables = document.querySelectorAll('[data-testid^="construction-table-"][aria-current="page"]');
  const selectedTable = selectedTables.length === 1 ? selectedTables[0] : null;
  const outputId = selectedTable?.getAttribute('data-testid')?.slice('construction-table-'.length);
  const pending = [...document.querySelectorAll('[role="status"]')].some(node =>
    /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview|Loom is (?:refreshing the current table draft|finishing the previous table update))/.test(node.innerText?.trim() || ''));
  const ready = Boolean(workspace && preview && outputId && !pending && preview.dataset.previewStatus === 'ready' &&
    preview.dataset.previewReceiptId && preview.dataset.previewOutputId === outputId && outputId === expectedOutputId &&
    preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
    preview.dataset.currentDraftDigest === workspace.dataset.draftDigest);
  const table = ready && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return null;
  return {
    ariaRowCount: table.getAttribute('aria-rowcount'),
    rows: [...table.querySelectorAll('[role="row"]')].map(row => row.innerText),
  };
};

export const currentReadyPreviewOutputId = () => {
  const workspaces = document.querySelectorAll('[data-testid="construction-workspace"]');
  const previews = document.querySelectorAll('[data-testid="construction-preview"]');
  if (workspaces.length !== 1 || previews.length !== 1 ||
      !document.querySelector('[aria-label="Add columns editor"]')) return null;
  const [workspace] = workspaces;
  const [preview] = previews;
  const outputId = preview.dataset.previewOutputId;
  const table = preview.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  const ready = Boolean(outputId && table && preview.dataset.previewStatus === 'ready' &&
    preview.dataset.previewReceiptId && preview.dataset.currentDraftVersion === workspace.dataset.draftVersion &&
    preview.dataset.currentDraftDigest === workspace.dataset.draftDigest);
  return ready ? outputId : null;
};

export const startFirstTableProgressObserver = () => {
  const key = '__loomFirstTableProgressCancellationObserver';
  window[key]?.observer?.disconnect?.();
  const events = [];
  const sample = () => {
    const statusText = [...document.querySelectorAll('[role="status"]')]
      .map(node => node.innerText?.trim() || '')
      .find(text => /^(Checking .* fields|Creating .* table|Adding the ID column|Loading the preview)/.test(text)) || null;
    const patientIDControl = document.querySelector('button[aria-label^="Select Patient ID"]');
    const state = {
      firstTableProgress: Boolean(statusText && /^(Checking .* fields|Creating .* table|Adding the ID column)/.test(statusText)),
      progressText: statusText,
      patientIDControlDisabled: patientIDControl ? patientIDControl.disabled : null,
    };
    const previous = events.at(-1);
    if (!previous || previous.firstTableProgress !== state.firstTableProgress ||
        previous.progressText !== state.progressText ||
        previous.patientIDControlDisabled !== state.patientIDControlDisabled) {
      events.push({ ...state, atEpochMs: performance.timeOrigin + performance.now() });
    }
  };
  const observer = new MutationObserver(sample);
  observer.observe(document.body, {
    subtree: true,
    childList: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['disabled', 'aria-label'],
  });
  window[key] = { events, observer, sample };
};

export const stopFirstTableProgressObserver = () => {
  const key = '__loomFirstTableProgressCancellationObserver';
  const probe = window[key];
  if (!probe) throw new Error('first-table progress observer was not installed');
  probe.sample();
  probe.observer.disconnect();
  const events = [...probe.events];
  delete window[key];
  return events;
};

export const waitForCurrentPreviewRows = async ({
  page, report, outputId, expectedIDs, expectedGenderByID, check, timeoutMs = 5000,
}) => {
  assert(outputId, 'A current preview check must bind the selected output ID');
  const deadline = performance.now() + timeoutMs;
  let lastMismatch;
  while (performance.now() < deadline) {
    const snapshot = await page.evaluate(readCurrentPreviewSnapshot, outputId);
    if (performance.now() >= deadline) break;
    if (snapshot && snapshot.ariaRowCount === String(expectedIDs.length + 1) &&
        snapshot.rows.length === expectedIDs.length + 1) {
      let ids;
      try {
        ids = assertPatientRows(snapshot.rows.slice(1), expectedIDs, expectedGenderByID, snapshot.rows[0]);
      } catch (error) {
        lastMismatch = error;
      }
      if (ids) return recordPreviewPatients(snapshot.rows, ids, report, expectedIDs, check, expectedGenderByID);
    }
    const remainingMs = deadline - performance.now();
    if (remainingMs <= 0) break;
    await page.waitForTimeout(Math.min(50, remainingMs));
  }
  const detail = lastMismatch instanceof Error ? `: ${lastMismatch.message}` : '';
  throw new Error(`Preview for output ${outputId} did not render the exact fixture rows within its action deadline${detail}`, {
    cause: lastMismatch,
  });
};

export const adjudicateFirstTableConfiguredContextAbort = ({
  network, uiOrigin, project, explorer, beforeDraft, afterDraft, action, rows, expectedIDs, progressSamples,
}) => {
  if (!Array.isArray(network) || !action || action.label !== 'create Patient table and render Preview' ||
      action.status !== 'passed' || !Number.isFinite(action.startedAtMs) || !Number.isFinite(action.endedAtMs) ||
      !Number.isFinite(action.startedAtEpochMs) || !Number.isFinite(action.finishedAtEpochMs) ||
      !beforeDraft?.version || !beforeDraft?.digest || !afterDraft?.version || !afterDraft?.digest ||
      !Array.isArray(rows) || !Array.isArray(expectedIDs) || !expectedIDs.length || !Array.isArray(progressSamples)) return null;

  try {
    assertPatientRows(rows.slice(1), expectedIDs);
  } catch {
    return null;
  }

  const expectedURL = new URL(
    `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/configured-column-context`,
    uiOrigin,
  ).href;
  const candidates = network.filter(entry => entry.kind === 'network' && entry.method === 'POST' &&
    entry.status == null && entry.errorText === 'net::ERR_ABORTED' && entry.rawURL === expectedURL);
  if (candidates.length !== 1) return null;

  const [entry] = candidates;
  const timeline = entry.requestTimeline;
  const details = entry.requestDetails;
  const requestID = entry.playwrightRequestId;
  const requestStartedMs = timeline?.requestStartedMs;
  const failedAtMs = timeline?.failedAtMs;
  const navigations = timeline?.mainFrameNavigations;
  const beforeVersion = Number(beforeDraft.version);
  const afterVersion = Number(afterDraft.version);
  const exactOwner = typeof requestID === 'string' && requestID.length > 0 &&
    network.filter(candidate => candidate.playwrightRequestId === requestID).length === 1 &&
    Number.isInteger(beforeVersion) && Number.isInteger(afterVersion) && afterVersion > beforeVersion &&
    beforeDraft.digest !== afterDraft.digest &&
    details?.draftVersion === beforeVersion && details?.draftDigest === beforeDraft.digest &&
    details?.outputId === null && details?.stageId === null;
  const exactWindow = Number.isFinite(requestStartedMs) && Number.isFinite(failedAtMs) &&
    requestStartedMs <= action.startedAtMs && failedAtMs >= action.startedAtMs &&
    failedAtMs <= action.endedAtMs && timeline.action === null && entry.triggerAction === null &&
    Array.isArray(navigations) && navigations.length === 0;
  const failedEpochMs = action.startedAtEpochMs + failedAtMs - action.startedAtMs;
  const queryOwnerDisabledAtFailure = progressSamples.some((sample, index) => {
    const next = progressSamples[index + 1];
    const intervalEnd = next?.atEpochMs ?? action.finishedAtEpochMs;
    return sample.firstTableProgress === true && sample.patientIDControlDisabled === true &&
      Number.isFinite(sample.atEpochMs) && Number.isFinite(intervalEnd) &&
      sample.atEpochMs <= failedEpochMs && intervalEnd >= failedEpochMs;
  });
  if (!exactOwner || !exactWindow || !queryOwnerDisabledAtFailure) return null;

  entry.canceled = true;
  entry.cancellationReason = 'first-table progress disabled the configured-column context owner for the previous draft';
  return {
    playwrightRequestId: requestID,
    url: entry.url,
    requestStartedMs,
    failedAtMs,
    actionStartedAtMs: action.startedAtMs,
    actionEndedAtMs: action.endedAtMs,
    beforeDraft: { version: beforeVersion, digest: beforeDraft.digest },
    afterDraft: { version: afterVersion, digest: afterDraft.digest },
    patientIDs: [...expectedIDs].sort(),
  };
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
    timeout: 5000,
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
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5000 });
      await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), expectedIDs.length + 1, { timeout: 5000 });
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
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await page.getByRole('button', { name: /^Select Gender/ }).waitFor({ state: 'visible', timeout: 5000 });
      await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), 3, { timeout: 5000 });
    },
  });
};

export const recompileWorkflow = async ({ page, report, action, check, fault }, context) => {
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
    timeout: 5000,
    budget: 5000,
    after: async () => Promise.all([
      alert.waitFor({ state: 'visible', timeout: 5000 }),
      recompileButton.waitFor({ state: 'visible', timeout: 5000 }),
    ]),
  });
  await alert.waitFor({ state: 'visible', timeout: 5000 });
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
};

export const firstTableWorkflow = async ({ page, report, action, check }, context) => {
  const oracle = patientOracle(context.target);
  report.target.fixtureOracle = { path: oracle.path, sha256: oracle.sha256, patientIDs: oracle.ids };
  const { explorer } = await createBlankExplorerWithUI({ page, action, target: context.target, context, check }, 'first-table');
  report.target.explorer = explorer;
  const tableName = page.locator('#first-table-name');
  await action('name first Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
  await page.waitForFunction(() => {
    const button = document.querySelector('button[aria-label="Choose Patient rows"]');
    return Boolean(button && !button.disabled);
  }, undefined, { timeout: 5000 });
  await installFirstTableObserver(page);
  let availabilityEvents;
  let observerInstalled = true;
  try {
    const choose = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('create verified-ID Patient first table with accepted preview', choose, () => choose.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.waitForFunction(currentPreviewReady, undefined, { timeout: 5000 });
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

  const explorerSelector = page.getByRole('combobox', { name: 'Explorer' });
  const selectedExplorerBeforeReload = await explorerSelector.inputValue();
  const selectedTable = page.locator('[data-testid^="construction-table-"][aria-current="page"]');
  const selectedTableCount = await selectedTable.count();
  const selectedTableTestId = selectedTableCount === 1 ? await selectedTable.getAttribute('data-testid') : null;
  const selectedOutputId = selectedTableTestId?.slice('construction-table-'.length) ?? null;
  const workspaceBeforeReload = page.getByTestId('construction-workspace');
  const beforeReload = {
    explorerId: selectedExplorerBeforeReload,
    selectedTableTestId,
    outputId: selectedOutputId,
    draftVersion: await workspaceBeforeReload.getAttribute('data-draft-version'),
    draftDigest: await workspaceBeforeReload.getAttribute('data-draft-digest'),
    patientIDs: oracle.ids,
  };
  const reloadStartedAt = performance.now();
  const reloadDeadline = reloadStartedAt + 5000;
  const remainingReloadMs = () => Math.max(1, Math.ceil(reloadDeadline - performance.now()));
  let reloadFailure;
  let reloadedRows = [];
  let selectedExplorerAfterReload = null;
  let selectedTableAfterReload = null;
  let idFieldCountAfterReload = 0;
  try {
    assert(selectedTableCount === 1 && selectedTableTestId && selectedOutputId,
      'first-table reload starts from exactly one selected saved table');
    assert.equal(selectedExplorerBeforeReload, explorer, 'first-table reload stays scoped to its newly created Explorer');
    await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingReloadMs() });
    await page.waitForFunction(explorerId =>
      document.querySelector('select[aria-label="Explorer"]')?.value === explorerId,
    selectedExplorerBeforeReload, { timeout: remainingReloadMs() });
    await page.waitForFunction(tableTestId => {
      const selected = [...document.querySelectorAll('[data-testid^="construction-table-"][aria-current="page"]')];
      return selected.length === 1 && selected[0].getAttribute('data-testid') === tableTestId;
    }, selectedTableTestId, { timeout: remainingReloadMs() });
    await page.waitForFunction(currentPreviewReady, undefined, { timeout: remainingReloadMs() });
    await page.waitForFunction(rowCount =>
      document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
    oracle.ids.length + 1, { timeout: remainingReloadMs() });
    await page.getByTestId('preview-table-scroll').getByRole('table').waitFor({
      state: 'visible', timeout: remainingReloadMs(),
    });
    reloadedRows = await previewRows(page);
    selectedExplorerAfterReload = await explorerSelector.inputValue();
    selectedTableAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
    idFieldCountAfterReload = await page.getByRole('button', { name: /^Select Patient ID/ }).count();
  } catch (error) {
    reloadFailure = error;
  }
  const reloadElapsedMs = Math.round(performance.now() - reloadStartedAt);
  const reloadedPatientIDs = [];
  let rowIdentityFailure;
  if (!reloadFailure) {
    try {
      reloadedPatientIDs.push(...assertPatientRows(reloadedRows.slice(1), oracle.ids));
    } catch (error) {
      rowIdentityFailure = error;
    }
  }
  const explorerRestored = selectedExplorerAfterReload === selectedExplorerBeforeReload;
  const tableRestored = selectedTableAfterReload === selectedTableTestId;
  const idFieldRestored = idFieldCountAfterReload === 1;
  const rowsRestored = !reloadFailure && !rowIdentityFailure && reloadedPatientIDs.length === oracle.ids.length;
  const reloadWithinBudget = !reloadFailure && reloadElapsedMs <= 5000;
  const afterReload = {
    explorerId: selectedExplorerAfterReload,
    selectedTableTestId: selectedTableAfterReload,
    outputId: selectedTableAfterReload?.slice('construction-table-'.length) ?? null,
    draftVersion: await page.getByTestId('construction-workspace').getAttribute('data-draft-version').catch(() => null),
    draftDigest: await page.getByTestId('construction-workspace').getAttribute('data-draft-digest').catch(() => null),
    patientIDs: reloadedPatientIDs,
    rows: reloadedRows,
  };
  report.target.firstTablePersistence = {
    beforeReload,
    afterReload,
    elapsedMs: reloadElapsedMs,
    budgetMs: 5000,
    ...(reloadFailure ? { reloadFailure: reloadFailure instanceof Error ? reloadFailure.message : String(reloadFailure) } : {}),
    ...(rowIdentityFailure ? { rowIdentityFailure: rowIdentityFailure instanceof Error ? rowIdentityFailure.message : String(rowIdentityFailure) } : {}),
  };
  recordCheck(report, 'persistence', 'first Patient table remains selected after reload', explorerRestored && tableRestored,
    { expectedExplorerId: selectedExplorerBeforeReload, actualExplorerId: selectedExplorerAfterReload,
      expectedTableTestId: selectedTableTestId, actualTableTestId: selectedTableAfterReload });
  recordCheck(report, 'persistence', 'first Patient ID field survives reload', idFieldRestored,
    { count: idFieldCountAfterReload, selectedTableTestId: selectedTableAfterReload });
  recordCheck(report, 'persistence', 'reloaded Preview renders both exact independent fixture Patients', rowsRestored,
    { rows: reloadedRows, patientIDs: reloadedPatientIDs, expectedPatientIDs: oracle.ids,
      rowIdentityFailure: rowIdentityFailure instanceof Error ? rowIdentityFailure.message : null });
  recordCheck(report, 'performance', 'first-table reload-to-exact-rows within five seconds', reloadWithinBudget,
    { elapsedMs: reloadElapsedMs, budgetMs: 5000, afterMs: reloadElapsedMs,
      failure: reloadFailure instanceof Error ? reloadFailure.message : null });
  assert(!reloadFailure, 'first-table reload restored a settled current-draft Preview');
  assert(explorerRestored && tableRestored, 'first Patient table remains selected after reload');
  assert(idFieldRestored, 'first Patient ID field survives reload');
  assert(rowsRestored, 'reloaded Preview renders both exact independent fixture Patients');
  assert(reloadWithinBudget, 'first-table reload-to-exact-rows stayed within five seconds');
  const sourceAfter = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'independent Patient source stayed unchanged during Builder verification', sourceAfter === oracle.sha256,
    { before: oracle.sha256, after: sourceAfter, path: oracle.path });
};

export const tablesWorkflow = async ({ page, report, action, check }, context) => {
  const oracle = patientOracle(context.target);
  report.target.fixtureOracle = { path: oracle.path, sha256: oracle.sha256, patientIDs: oracle.ids, genderByID: oracle.genderByID };
  const tableRenderCheck = 'table lifecycle actions and reloads render exact rows or the empty workspace within five seconds';
  const tableRenderCheckpoints = [];
  const publishTableCheckpoints = () => {
    report.target.tableLifecycleCheckpoints = [...tableRenderCheckpoints];
  };
  const recordFailedTableTransition = (name, startedAt, error) => {
    const elapsedMs = performance.now() - startedAt;
    const checkpoint = {
      name,
      durationMs: Math.round(elapsedMs),
      budgetMs: 5000,
      withinBudget: false,
      failure: error instanceof Error ? error.message : String(error),
    };
    tableRenderCheckpoints.push(checkpoint);
    publishTableCheckpoints();
    recordCheck(report, 'performance', tableRenderCheck, false, {
      budgetMs: 5000,
      checkpoints: [...tableRenderCheckpoints],
      failedTransition: name,
    });
  };
  const recordTableTransition = (name, startedAt) => {
    const elapsedMs = performance.now() - startedAt;
    const durationMs = Math.round(elapsedMs);
    const checkpoint = { name, durationMs, budgetMs: 5000, withinBudget: elapsedMs <= 5000 };
    tableRenderCheckpoints.push(checkpoint);
    publishTableCheckpoints();
    if (!checkpoint.withinBudget) {
      const error = new Error(`${name} exceeded its five-second action-to-render budget`);
      recordCheck(report, 'performance', tableRenderCheck, false, {
        budgetMs: 5000,
        checkpoints: [...tableRenderCheckpoints],
        failedTransition: name,
      });
      throw error;
    }
  };
  const remainingMs = (deadline, name) => {
    const remaining = Math.ceil(deadline - performance.now());
    assert(remaining > 0, `${name} exhausted its original five-second action-to-render deadline`);
    return remaining;
  };
  const selectedTableIdentity = async () => {
    const selected = page.locator('[data-testid^="construction-table-"][aria-current="page"]');
    assert.equal(await selected.count(), 1, 'Expected exactly one selected Builder table');
    const testId = await selected.getAttribute('data-testid');
    assert(testId?.startsWith('construction-table-'), 'The selected Builder table must expose its output identity');
    return { testId, outputId: testId.slice('construction-table-'.length) };
  };
  const waitForSelectedTable = (testId, timeout) => page.waitForFunction(expected => {
    const selected = [...document.querySelectorAll('[data-testid^="construction-table-"][aria-current="page"]')];
    return selected.length === 1 && selected[0].getAttribute('data-testid') === expected;
  }, testId, { timeout });
  const waitForWorkspaceTable = async ({ explorerId, count, testId, empty, remaining }) => {
    await page.waitForFunction(({ expectedExplorer, expectedCount, expectedTestId, expectEmpty }) => {
      const explorer = document.querySelector('select[aria-label="Explorer"]')?.value;
      const tabs = [...document.querySelectorAll('[data-testid^="construction-table-"]')];
      const selected = tabs.filter(tab => tab.getAttribute('aria-current') === 'page');
      return explorer === expectedExplorer && tabs.length === expectedCount &&
        (expectEmpty ? selected.length === 0 : selected.length === 1 && selected[0].getAttribute('data-testid') === expectedTestId);
    }, { expectedExplorer: explorerId, expectedCount: count, expectedTestId: testId, expectEmpty: empty },
    { timeout: remaining() });
  };
  const waitForRows = (outputId, timeout) => waitForCurrentPreviewRows({
    page,
    report,
    outputId,
    expectedIDs: oracle.ids,
    expectedGenderByID: oracle.genderByID,
    check,
    timeoutMs: timeout,
  });
  const measuredTableAction = async (label, locator, perform, { outputId, before, after, editable = false } = {}) => {
    const startedAt = performance.now();
    const deadline = startedAt + 5000;
    const remaining = () => remainingMs(deadline, label);
    try {
      if (before) await before(remaining);
      await action(label, locator, perform, {
        timeout: remaining(),
        budget: 5000,
        editable,
        after: async () => {
          if (after) await after(remaining);
          const expectedOutputId = typeof outputId === 'function' ? await outputId() : outputId;
          if (expectedOutputId) await waitForRows(expectedOutputId, remaining());
        },
      });
      recordTableTransition(label, startedAt);
    } catch (error) {
      if (!tableRenderCheckpoints.some(checkpoint => checkpoint.name === label &&
          (!checkpoint.withinBudget || checkpoint.failure))) {
        recordFailedTableTransition(label, startedAt, error);
      }
      throw error;
    }
  };
  const reloadTableWorkspace = async ({ label, explorerId, count, testId, outputId, after }) => {
    const startedAt = performance.now();
    const deadline = startedAt + 5000;
    const remaining = () => remainingMs(deadline, label);
    try {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: remaining() });
      await waitForWorkspaceTable({ explorerId, count, testId, empty: false, remaining });
      if (after) await after(remaining);
      await waitForRows(outputId, remaining());
      recordTableTransition(label, startedAt);
    } catch (error) {
      if (!tableRenderCheckpoints.some(checkpoint => checkpoint.name === label &&
          (!checkpoint.withinBudget || checkpoint.failure))) {
        recordFailedTableTransition(label, startedAt, error);
      }
      throw error;
    }
  };
  const reloadEmptyWorkspace = async ({ label, explorerId }) => {
    const startedAt = performance.now();
    const deadline = startedAt + 5000;
    const remaining = () => remainingMs(deadline, label);
    try {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: remaining() });
      await waitForWorkspaceTable({ explorerId, count: 0, testId: null, empty: true, remaining });
      await page.getByText('Build your first table', { exact: true }).waitFor({ state: 'visible', timeout: remaining() });
      recordTableTransition(label, startedAt);
    } catch (error) {
      if (!tableRenderCheckpoints.some(checkpoint => checkpoint.name === label &&
          (!checkpoint.withinBudget || checkpoint.failure))) {
        recordFailedTableTransition(label, startedAt, error);
      }
      throw error;
    }
  };

  const created = await createBlankExplorerWithUI({ page, action, target: context.target, context, check }, 'controls');
  report.target.explorer = created.explorer;
  await createPatientTableWithUI({ page, action }, oracle.ids);
  await checkPreviewPatients(page, report, oracle.ids, check);
  await configurePatientGenderWithUI({ page, action });
  await checkPreviewPatients(page, report, oracle.ids, check, oracle.genderByID);
  const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
  const previewOutputId = await page.evaluate(currentReadyPreviewOutputId);
  assert(previewOutputId, 'The ready Preview must bind the Patient table while its Add columns editor is open');
  let initialTableIdentity;
  await measuredTableAction('return to the selected Patient table after applying Gender', closeEditor, () => closeEditor.click(), {
    outputId: () => initialTableIdentity?.outputId,
    after: async remaining => {
      await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'hidden', timeout: remaining() });
      await page.getByRole('button', { name: /Add columns:/ }).waitFor({ state: 'visible', timeout: remaining() });
      await waitForSelectedTable(`construction-table-${previewOutputId}`, remaining());
      initialTableIdentity = await selectedTableIdentity();
      assert.equal(initialTableIdentity.outputId, previewOutputId,
        'Returning from the Add columns editor must restore the Preview-bound selected table');
    },
  });
  const originalTableTestId = initialTableIdentity.testId;
  const originalOutputId = initialTableIdentity.outputId;
  report.target.originalTableTestId = originalTableTestId;

  const duplicate = page.getByTestId('construction-duplicate-table');
  const duplicateTitle = 'Patients copy';
  let duplicatedTableIdentity;
  await measuredTableAction('duplicate configured table', duplicate, () => duplicate.click(), {
    after: async remaining => {
      await page.getByRole('button', { name: duplicateTitle, exact: true }).waitFor({ state: 'visible', timeout: remaining() });
      await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-table-"]').length === 2,
        undefined, { timeout: remaining() });
      duplicatedTableIdentity = await tableIdentityByTitle(page, duplicateTitle);
      await waitForSelectedTable(duplicatedTableIdentity.testId, remaining());
    },
    outputId: async () => {
      assert(duplicatedTableIdentity, 'Duplicating a table must expose its generated output identity');
      return duplicatedTableIdentity.outputId;
    },
  });
  assert.notEqual(duplicatedTableIdentity.testId, originalTableTestId,
    'Duplicating a table must create a distinct output-derived tab identity');
  report.target.duplicatedTableTestId = duplicatedTableIdentity.testId;
  const selectedAfterDuplicate = page.locator('[data-testid^="construction-table-"][aria-current="page"]');
  check('persistence', 'newly duplicated table is selected immediately',
    await selectedAfterDuplicate.getAttribute('data-testid') === duplicatedTableIdentity.testId,
    { selectedTableTestId: await selectedAfterDuplicate.getAttribute('data-testid') });

  const project = context.target.fixtureProject;
  const commandsPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(created.explorer)}/authoring/v2/commands`;
  let renameResponse;
  page.once('dialog', async dialog => {
    report.target.renameDialogType = dialog.type();
    await dialog.accept('Renamed Patients');
  });
  const rename = page.getByTestId(`construction-rename-table-${duplicatedTableIdentity.outputId}`);
  await measuredTableAction('rename duplicated table', rename, () => rename.click(), {
    before: remaining => {
      renameResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
        new URL(response.url()).origin === new URL(context.target.uiUrl).origin && new URL(response.url()).pathname === commandsPath,
      { timeout: remaining() });
    },
    after: async remaining => {
      await renameResponse;
      await page.getByTestId(duplicatedTableIdentity.testId).filter({ hasText: 'Renamed Patients' })
        .waitFor({ state: 'visible', timeout: remaining() });
    },
    outputId: duplicatedTableIdentity.outputId,
  });
  const renameResult = await renameResponse;
  check('correctness', 'rename request returned success', renameResult.status() >= 200 && renameResult.status() < 300,
    { status: renameResult.status(), path: commandsPath, dialogType: report.target.renameDialogType });

  await reloadTableWorkspace({
    label: 'reload duplicated and renamed table to exact rows',
    explorerId: created.explorer,
    count: 2,
    testId: duplicatedTableIdentity.testId,
    outputId: duplicatedTableIdentity.outputId,
    after: remaining => page.getByTestId(duplicatedTableIdentity.testId).filter({ hasText: 'Renamed Patients' })
      .waitFor({ state: 'visible', timeout: remaining() }),
  });
  const renamedTable = page.getByTestId(duplicatedTableIdentity.testId);
  check('persistence', 'duplicated and renamed tables survive reload', await renamedTable.innerText().then(text => text.includes('Renamed Patients')));
  let selectedAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
  check('persistence', 'newly duplicated table selection survives reload', selectedAfterReload === duplicatedTableIdentity.testId,
    { selectedTableTestId: selectedAfterReload });

  const originalTable = page.getByTestId(originalTableTestId);
  await measuredTableAction('select original table manually', originalTable, () => originalTable.click(), {
    after: remaining => waitForSelectedTable(originalTableTestId, remaining()),
    outputId: originalOutputId,
  });
  await reloadTableWorkspace({
    label: 'reload manual table selection to exact rows',
    explorerId: created.explorer,
    count: 2,
    testId: originalTableTestId,
    outputId: originalOutputId,
  });
  selectedAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
  check('persistence', 'manual table selection survives reload', selectedAfterReload === originalTableTestId,
    { expectedTableTestId: originalTableTestId, selectedTableTestId: selectedAfterReload });

  const renamedCopy = page.getByTestId(duplicatedTableIdentity.testId);
  await measuredTableAction('select renamed table before deletion', renamedCopy, () => renamedCopy.click(), {
    after: remaining => waitForSelectedTable(duplicatedTableIdentity.testId, remaining()),
    outputId: duplicatedTableIdentity.outputId,
  });
  page.once('dialog', async dialog => {
    report.target.deleteDialogType = dialog.type();
    await dialog.accept();
  });
  const deleteButton = page.getByTestId('construction-delete-table');
  await measuredTableAction('delete duplicated table and render fallback rows', deleteButton, () => deleteButton.click(), {
    after: async remaining => {
      await page.getByTestId(originalTableTestId).waitFor({ state: 'visible', timeout: remaining() });
      await waitForSelectedTable(originalTableTestId, remaining());
    },
    outputId: originalOutputId,
  });
  check('persistence', 'selected-table deletion immediately falls back to the remaining table',
    await page.getByTestId(originalTableTestId).getAttribute('aria-current') === 'page',
    { selectedTableTestId: await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid'), dialogType: report.target.deleteDialogType });
  await reloadTableWorkspace({
    label: 'reload deletion fallback to exact rows',
    explorerId: created.explorer,
    count: 1,
    testId: originalTableTestId,
    outputId: originalOutputId,
  });
  const renamedStillPresent = await page.getByTestId(duplicatedTableIdentity.testId).count();
  check('persistence', 'deleted table stays absent after reload', renamedStillPresent === 0, { count: renamedStillPresent });
  selectedAfterReload = await page.locator('[data-testid^="construction-table-"][aria-current="page"]').getAttribute('data-testid');
  check('persistence', 'selected-table removal falls back to the remaining table after reload', selectedAfterReload === originalTableTestId,
    { expectedTableTestId: originalTableTestId, selectedTableTestId: selectedAfterReload });

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
  let copyExplorer;
  let copiedTableIdentity;
  await measuredTableAction('copy configured Explorer and render exact rows', copyButton, () => copyButton.click(), {
    after: async remaining => {
      await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected &&
        Boolean(document.querySelector('button[aria-label^="Select Patient ID"]')) && Boolean(document.querySelector('button[aria-label^="Select Gender"]')),
      copyTitle, { timeout: remaining() });
      copyExplorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
      copiedTableIdentity = await selectedTableIdentity();
    },
    outputId: async () => {
      assert(copiedTableIdentity, 'Copied Explorer must select its configured output table');
      return copiedTableIdentity.outputId;
    },
  });
  report.target.sourceExplorer = created.explorer;
  report.target.explorer = copyExplorer;
  check('correctness', 'copied Explorer is distinct from its source', Boolean(copyExplorer && copyExplorer !== created.explorer),
    { sourceExplorer: created.explorer, copiedExplorer: copyExplorer, title: copyTitle });
  await reloadTableWorkspace({
    label: 'reload copied Explorer to exact rows',
    explorerId: copyExplorer,
    count: 1,
    testId: copiedTableIdentity.testId,
    outputId: copiedTableIdentity.outputId,
    after: async remaining => page.waitForFunction(({ explorer, title }) =>
      document.querySelector('select[aria-label="Explorer"]')?.value === explorer &&
      document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === title,
    { explorer: copyExplorer, title: copyTitle }, { timeout: remaining() }),
  });
  check('persistence', 'copied Explorer retains configured fields after reload', true,
    { sourceExplorer: created.explorer, copiedExplorer: copyExplorer, title: copyTitle });

  page.once('dialog', async dialog => {
    report.target.emptyWorkspaceDeleteDialogType = dialog.type();
    await dialog.accept();
  });
  const deleteLastTable = page.getByTestId('construction-delete-table');
  await measuredTableAction('delete the last configured table to an empty workspace', deleteLastTable, () => deleteLastTable.click(), {
    after: async remaining => {
      await page.getByText('Build your first table', { exact: true }).waitFor({ state: 'visible', timeout: remaining() });
      await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-table-"]').length === 0,
        undefined, { timeout: remaining() });
    },
  });
  await reloadEmptyWorkspace({ label: 'reload empty copied Explorer workspace', explorerId: copyExplorer });
  check('persistence', 'deleting the last table persists an empty workspace', await page.locator('[data-testid^="construction-table-"]').count() === 0,
    { explorer: copyExplorer, title: copyTitle });
  const workspaceBeforeFirstTable = await page.getByTestId('construction-workspace').evaluate(workspace => ({
    version: workspace.dataset.draftVersion,
    digest: workspace.dataset.draftDigest,
  }));
  const actionCountBeforeFirstTable = report.actions.length;
  await page.evaluate(startFirstTableProgressObserver);
  await createPatientTableWithUI({ page, action }, oracle.ids);
  const firstTableProgressSamples = await page.evaluate(stopFirstTableProgressObserver);
  const firstTableRows = await checkPreviewPatients(page, report, oracle.ids, check);
  const workspaceAfterFirstTable = await page.getByTestId('construction-workspace').evaluate(workspace => ({
    version: workspace.dataset.draftVersion,
    digest: workspace.dataset.draftDigest,
  }));
  const firstTableAction = report.actions.slice(actionCountBeforeFirstTable)
    .find(entry => entry.label === 'create Patient table and render Preview');
  const expectedCancellation = adjudicateFirstTableConfiguredContextAbort({
    network: report.network,
    uiOrigin: context.target.uiUrl,
    project,
    explorer: copyExplorer,
    beforeDraft: workspaceBeforeFirstTable,
    afterDraft: workspaceAfterFirstTable,
    action: firstTableAction,
    rows: firstTableRows,
    expectedIDs: oracle.ids,
    progressSamples: firstTableProgressSamples,
  });
  if (expectedCancellation) report.target.firstTableConfiguredContextCancellation = expectedCancellation;
  await configurePatientGenderWithUI({ page, action });
  await checkPreviewPatients(page, report, oracle.ids, check, oracle.genderByID);
  const allTableTransitionsWithinBudget = tableRenderCheckpoints.length === 13 &&
    tableRenderCheckpoints.every(checkpoint => checkpoint.withinBudget && checkpoint.durationMs <= 5000);
  check('performance', tableRenderCheck, allTableTransitionsWithinBudget, {
    budgetMs: 5000,
    checkpoints: [...tableRenderCheckpoints],
  });
  const sourceAfter = createHash('sha256').update(readFileSync(oracle.path)).digest('hex');
  check('correctness', 'independent Patient source stayed unchanged during Builder verification', sourceAfter === oracle.sha256,
    { before: oracle.sha256, after: sourceAfter, path: oracle.path });
};
