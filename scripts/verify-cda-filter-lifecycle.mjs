import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

export async function filterLifecycleWorkflow({ page, cda }) {
  const project = cda.project;
  const uiOrigin = cda.uiOrigin;
  const apiOrigin = cda.apiOrigin;
  const apiContainer = cda.target.apiContainer;
  const arangoContainer = cda.target.arangoContainer;
  const explorerId = `filter-lifecycle-${Date.now()}`;
  const root = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const authoringURL = `${apiOrigin}${root}/${encodeURIComponent(explorerId)}/authoring/v2`;
  const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
  const evidenceDirectory = cda.evidence;
  const tableName = `CDA filter QA ${Date.now()}`;
  const state = cda.report;
  Object.assign(state, { pageURL, explorerId, project, tableName, clicks: [], timingsMs: {}, responses: [], errors: state.errors ?? [] });
    let requestCapture;
  let fatal;
  let cleanupError;
  let acceptingCleanupDialog = false;
  cda.onDialog(async dialog => {
    if (acceptingCleanupDialog) return { accept: true };
    throw new Error(`Unexpected ${dialog.type} dialog: ${dialog.message}`);
  });
  let outputId;
  let created = false;

  const builder = async () => {
    const response = await fetch(`${authoringURL}/builder`, { signal: AbortSignal.timeout(30000) });
    assert.equal(response.status, 200);
    return response.json();
  };
  const apiCreate = async () => {
    const response = await fetch(`${apiOrigin}${root}`, { method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ name: explorerId, title: 'CDA filter lifecycle verification' }), signal: AbortSignal.timeout(30000) });
    const body = await response.json();
    state.responses.push({ path: root, status: response.status });
    assert(response.ok, JSON.stringify(body));
    return body;
  };
  const rawPatients = (ids, generation) => {
    const query = `FOR d IN Patient FILTER d.project == ${JSON.stringify(project)} AND d.dataset_generation == ${JSON.stringify(generation)} AND d.id IN ${JSON.stringify(ids)} RETURN d.id`;
    const script = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()))`;
    const output = execFileSync('docker', ['exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', script], { encoding: 'utf8', timeout: 30000, maxBuffer: 200000 });
    const start = output.indexOf('[');
    assert(start >= 0, `Arango returned no JSON array: ${output.slice(-1000)}`);
    return JSON.parse(output.slice(start));
  };
  const selectTable = async () => {
    await cda.wait( ({ name }) => [...document.querySelectorAll('[data-testid^="construction-table-"]')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
    await cda.click( '[data-testid^="construction-table-"]', { includes: tableName });
  };
  const preview = async (stage, expectedID) => {
    const started = Date.now();
    await cda.wait( () => Boolean(document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount')) && !document.body.innerText.includes('Loading the preview…'), {}, 5000);
    const result = await cda.inspect( () => {
      const scroll = document.querySelector('[data-testid="preview-table-scroll"]');
      return { rowCount: scroll.querySelector('[role="table"]')?.getAttribute('aria-rowcount'), headers: [...scroll.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()), rows: [...scroll.querySelectorAll('[role="row"]')].slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())).filter(row => row.length) };
    });
    state.timingsMs[stage] = Date.now() - started;
    assert(state.timingsMs[stage] < 5000, `${stage} preview took ${state.timingsMs[stage]} ms`);
    if (expectedID) {
      assert.equal(result.rowCount, '2', `${stage} must have exactly one data row`);
      assert.deepEqual(result.rows, [[expectedID]], `${stage} does not show the selected CDA Patient`);
    }
    state[stage] = result;
    return result;
  };
  const filterEditor = async () => {
    await cda.wait( () => Boolean(document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]:not(:disabled)')), {}, 5000);
    const choices = await cda.inspect( () => {
      const panel = document.querySelector('[data-testid="construction-filter-editor"]');
      return { columns: [...panel.querySelector('select[aria-label="Column"]').options].map(option => ({ label: option.textContent.trim(), value: option.value, disabled: option.disabled })), conditions: [...panel.querySelector('select[aria-label="Condition"]').options].map(option => ({ label: option.textContent.trim(), value: option.value, disabled: option.disabled })) };
    });
    const id = choices.columns.find(choice => choice.label.startsWith('Patient ID'));
    const equals = choices.conditions.find(choice => choice.value === 'EQUALS');
    assert(id && !id.disabled, 'Patient ID cannot be filtered');
    assert(equals && !equals.disabled, 'Patient ID equality is unavailable');
    return { choices, id };
  };
  const setEquals = async (id, value, label) => {
    await cda.selectOption( '[data-testid="construction-filter-editor"] select[aria-label="Column"]', id.value);
    state.clicks.push('Choose Patient ID column');
    await cda.selectOption( '[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
    state.clicks.push('Choose equals condition');
    await cda.wait( () => Boolean(document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]:not(:disabled)')), {}, 5000);
    const started = Date.now();
    const requestStart = state.nativeRequests.length;
    await cda.fill( '[data-testid="construction-filter-editor"] input[aria-label="Value"]', value);
    state.clicks.push(label);
    const [request] = await Promise.all([
      requestCapture.waitFor(entry => entry.method === 'POST' && entry.path.endsWith('/construction-proposals'), { fromIndex: requestStart, timeoutMs: 5000 }),
      cda.wait( ({ value: expectedValue }) => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready' && document.querySelector('[data-testid="construction-proposal-preview"]')?.innerText.includes(expectedValue), { value }, 5000),
    ]);
    const proposed = await cda.inspect( () => { const panel = document.querySelector('[data-testid="construction-proposal-preview"]'); return { rows: [...panel.querySelectorAll('tbody tr')].map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())), applyDisabled: document.querySelector('[data-testid="construction-apply-proposal"]')?.disabled }; });
    assert.equal(request.status, 200, JSON.stringify(request));
    assert(request.response?.proposalId, `${label} proposal must include a fresh receipt`);
    assert.equal(request.response.outputId, outputId);
    assert.deepEqual(proposed.rows, [[value]]);
    assert.equal(proposed.applyDisabled, false);
    const elapsedMs = Date.now() - started;
    state.timingsMs[label] = elapsedMs;
    assert(elapsedMs < 5000, `${label} preview took ${elapsedMs} ms`);
    return { proposed, elapsedMs, request };
  };

  try {
    await apiCreate();
    requestCapture = cda.captureRequests(root, { responsePaths: /commands|builder|construction-proposals|preview|explorers/ });
    await cda.navigate( pageURL);
    await cda.wait( () => document.body.innerText.includes('Build your first table'), {}, 5000);
    await cda.wait( () => Boolean(document.querySelector('button[aria-label="Choose Patient rows"]:not(:disabled)')), {}, 5000);
    await cda.fill( '#first-table-name', tableName);
    await cda.click( 'button[aria-label="Choose Patient rows"]');
    state.clicks.push('Choose Patient rows');
    created = true;
    const createdBuilder = await builder();
    outputId = createdBuilder.workspace.documents.find(document => document.output.title === tableName)?.output.id;
    assert(outputId, 'Temporary Patient table was not created');
    const generation = createdBuilder.catalog.generation;
    assert(generation, 'CDA catalog must expose the active generation');
    const baseline = await preview('baseline');
    assert.equal(baseline.headers[0], 'PATIENT ID');
    assert(baseline.rows.length >= 2, 'Baseline preview did not show two Patient IDs');
    const ids = baseline.rows.slice(0, 2).map(row => row[0]);
    assert.deepEqual(new Set(rawPatients(ids, generation)), new Set(ids), 'Preview IDs are absent from independent project/generation scoped CDA Patient records');
    state.sourceIDs = ids;
    state.generation = generation;

    await cda.click( 'button[aria-label^="Filter rows:"]');
    state.clicks.push('Open Filter rows');
    const { choices, id } = await filterEditor();
    state.filterChoices = choices;
    state.firstProposal = await setEquals(id, ids[0], 'Enter Patient ID value');
    await cda.click( '[data-testid="construction-apply-proposal"]');
    state.clicks.push('Apply Patient ID filter');
    await cda.wait( () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 5000);
    const firstSaved = await builder();
    assert.deepEqual(firstSaved.workspace.documents.find(document => document.output.id === outputId)?.construction, state.firstProposal.request.response.candidateConstruction, 'Applied filter must persist the proposed construction');
    await cda.navigate( pageURL);
    await selectTable();
    await preview('saved', ids[0]);

    await cda.click( '[data-testid^="construction-history-step-"]');
    state.clicks.push('Select saved Filter rows step');
    await cda.wait( () => Boolean(document.querySelector('[data-testid^="construction-edit-step-"]')), {}, 5000);
    await cda.click( '[data-testid^="construction-edit-step-"]');
    state.clicks.push('Edit Filter rows step');
    await filterEditor();
    const original = await cda.inspect( () => document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]')?.value);
    assert.equal(original, ids[0], 'Saved filter did not reopen with its exact value');
    state.editedProposal = await setEquals(id, ids[1], 'Change Patient ID value');
    await cda.click( '[data-testid="construction-apply-proposal"]');
    state.clicks.push('Apply edited Filter rows step');
    await cda.wait( () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 1, {}, 5000);
    const editedSaved = await builder();
    assert.deepEqual(editedSaved.workspace.documents.find(document => document.output.id === outputId)?.construction, state.editedProposal.request.response.candidateConstruction, 'Edited filter must persist its proposed construction');
    await cda.navigate( pageURL);
    await selectTable();
    await preview('edited', ids[1]);

    await cda.click( '[data-testid^="construction-history-step-"]');
    state.clicks.push('Select edited Filter rows step');
    await cda.wait( () => Boolean(document.querySelector('[data-testid^="construction-remove-step-"]')), {}, 5000);
    const removeStart = state.nativeRequests.length;
    await cda.click( '[data-testid^="construction-remove-step-"]');
    state.clicks.push('Remove Filter rows step');
    await requestCapture.waitFor(entry => entry.method === 'POST' && entry.path.endsWith('/construction-proposals'), { fromIndex: removeStart, timeoutMs: 5000 });
    await cda.wait( () => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', {}, 5000);
    await cda.click( '[data-testid="construction-apply-proposal"]');
    state.clicks.push('Apply step removal');
    await cda.wait( () => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, {}, 5000);
    await cda.navigate( pageURL);
    await selectTable();
    const restored = await preview('restored');
    assert(restored.rows.length >= 2, 'Removing the filter did not restore multiple Patient rows');
    assert.deepEqual(new Set(rawPatients(restored.rows.map(row => row[0]), generation)), new Set(restored.rows.map(row => row[0])));
    await requestCapture.flush();
    assert(state.nativeRequests.every(response => response.status < 400), 'Browser received an authoring API error');
    cda.includeBrowserDiagnostics();
    assert.deepEqual(state.errors, [], 'Unexpected owned API/browser failures occurred');
    assert.deepEqual(cda.diagnostics.console, [], 'Unexpected browser console errors');
    assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected browser page errors');
    assert.deepEqual(cda.diagnostics.networkFailures, [], 'Unexpected browser network failures');
    assert.deepEqual(cda.diagnostics.httpFailures, [], 'Unexpected browser HTTP failures');
    state.outcome = 'passed';
  } catch (error) {
    state.outcome = 'failed';
    state.error = error instanceof Error ? error.message : String(error);
    state.failureUI = await cda.inspect( () => ({ text: document.body.innerText.slice(0, 3500), alerts: [...document.querySelectorAll('[role="alert"]')].map(node => node.innerText) })).catch(() => undefined);
    fatal = error;
  } finally {
    if (outputId) {
      try {
        await cda.navigate( pageURL);
        await cda.wait( ({ name }) => [...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
        await cda.click( '[data-testid^="construction-table-"]', { includes: tableName });
        await cda.wait( () => document.querySelector('[data-testid="construction-delete-table"]')?.disabled === false, {}, 5000);
        acceptingCleanupDialog = true;
        await cda.click( '[data-testid="construction-delete-table"]');
        acceptingCleanupDialog = false;
        await cda.wait( ({ name }) => ![...document.querySelectorAll('button')].some(button => button.innerText.trim().endsWith(name)), { name: tableName }, 5000);
        state.cleanup = 'temporary table deleted';
      } catch (error) {
        state.cleanup = error instanceof Error ? error.message : String(error);
        cleanupError = error;
      }
    }
    if (requestCapture) await requestCapture.flush();
    state.responses.push(...state.nativeRequests.map(response => ({ path: response.path, status: response.status })));
    state.diagnostics = cda.diagnostics;
    cda.includeBrowserDiagnostics();
    await cda.attachReport('filter-lifecycle', state);
  }
  if (fatal && cleanupError) throw new AggregateError([fatal, cleanupError], 'Filter workflow and cleanup both failed');
  if (fatal) throw fatal;
  if (cleanupError) throw cleanupError;
}
