import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
import { chromium } from 'playwright';
import { collectSavedGridRows, OBSERVATION_DEFAULT_PAGE_IDS, readGridInPage, replacementTypeOptionsInPage, rowIDs, savedGridWindowChanged, savedPreviewReady } from '../../workflows/cda-same-table-root-replacement.mjs';
import { waitForBrowser } from '../cda-playwright.mjs';

const fixture = state => ({
  querySelector(selector) {
    if (selector === 'section[aria-label="Starting collection"]') {
      return { getAttribute: name => name === 'data-attached-selection-revision-id' ? state.selectionId : null };
    }
    if (selector === '[data-testid="construction-preview"]') {
      return { dataset: { previewStatus: state.status, previewOutputId: state.outputId } };
    }
    if (selector === '[data-testid="preview-table-scroll"] [role="table"]') {
      if (!state.table) return null;
      return {
        getAttribute: name => name === 'aria-rowcount' ? String(state.rowCount) : null,
        querySelectorAll: selectorName => selectorName === '[role="columnheader"]'
          ? state.headers.map(textContent => ({ textContent }))
          : [],
      };
    }
    return null;
  },
});

const evaluate = (state, args) => runInNewContext(`(${savedPreviewReady.toString()})(args)`, {
  document: fixture(state),
  args,
});

test('attached selection is not ready until the exact saved preview has rendered', () => {
  const state = {
    selectionId: 'selection-patient-1',
    status: 'loading',
    outputId: 'output-patient',
    table: true,
    rowCount: 1,
    headers: [],
  };
  assert.equal(fixture(state).querySelector('section[aria-label="Starting collection"]')
    .getAttribute('data-attached-selection-revision-id'), state.selectionId);
  assert.equal(evaluate(state, [1, 'output-patient', 'Patient ID']), false,
    'the selection marker can precede its saved-preview reconcile');

  state.status = 'ready';
  state.rowCount = 2;
  state.headers = ['Patient ID'];
  assert.equal(evaluate(state, [1, 'output-patient', 'Patient ID']), true);
  assert.equal(evaluate(state, [1, 'another-output', 'Patient ID']), false,
    'a ready preview from another output is not accepted');
  assert.equal(evaluate(state, [2, 'output-patient', 'Patient ID']), false,
    'a preview with the wrong exact row count is not accepted');
  assert.equal(evaluate({ ...state, headers: ['id'] }, [1, 'output-patient', 'Patient ID']), false,
    'a preview with a different server-authored column label is not accepted');
});

test('collection attachment waits for exact saved rows instead of reading the grid immediately', async () => {
  const workflowSource = await readFile(new URL('../../workflows/cda-same-table-root-replacement.mjs', import.meta.url), 'utf8');
  const start = workflowSource.indexOf("act('Attach the exact one-Patient collection through the native control'");
  const end = workflowSource.indexOf('const populationCommand', start);
  assert(start >= 0 && end > start);
  const attachAction = workflowSource.slice(start, end);
  const closeSettings = attachAction.indexOf("rowSettingsDialog.getByRole('button', { name: 'Back to table'");
  const settingsHidden = attachAction.indexOf("rowSettingsDialog.waitFor({ state: 'hidden'");
  const readRows = attachAction.indexOf("await assertSavedRows([PATIENT_ID], 'Patient ID')");
  assert(closeSettings >= 0 && closeSettings < settingsHidden && settingsHidden < readRows,
    'The native settings dialog must close before saved rows are inspected.');
  assert.match(attachAction, /await assertSavedRows\(\[PATIENT_ID\], 'Patient ID'\)/);
  assert.doesNotMatch(attachAction, /readGrid\('saved'\)|rowIDs\(/,
    'the action must not inspect an unsettled saved grid after only the attachment marker appears');
  const filterStart = workflowSource.indexOf("await act('Apply the Patient ID filter to establish the source setup'");
  const filterEnd = workflowSource.indexOf("}, 'Patient ID filter applies and renders the exact member within five seconds');", filterStart);
  const filterApply = workflowSource.slice(filterStart, filterEnd);
  assert(filterStart >= 0 && filterEnd > filterStart);
  assert(filterApply.indexOf("getByRole('navigation', { name: 'Table actions'") <
    filterApply.indexOf("await assertSavedRows([PATIENT_ID], 'Patient ID')"),
  'The applied filter must return to the native table actions before saved rows are inspected.');
  assert.doesNotMatch(filterApply, /construction-close-operation-editor/,
    'Filter Apply closes the operation editor itself; the workflow must not wait for an editor-close button.');
  assert.match(workflowSource, /const deadlineAt = Date\.now\(\) \+ ACTION_TIMEOUT_MS/);
  assert.match(workflowSource, /await cda\.wait\(savedPreviewReady, \[expectedIDs\.length, outputId, idLabel\], remaining\(\)\)/);
  assert.match(workflowSource, /collectSavedGridRows\(\{ page, cda, expectedIDs, idLabel, outputId, deadlineAt \}\)/);
});

test('retained REPLACE assessment binds output identity through the proposal', async () => {
  const outputId = 'out_0231db255e06bea56cdb65cc';
  const response = JSON.parse(JSON.stringify({
    status: 'READY',
    mode: 'REPLACE',
    snapshotToken: 'sha256:catalog-snapshot',
    draftVersion: 4,
    draftDigest: 'sha256:patient-draft',
    candidateReceiptId: 'receipt_replacement',
    proposal: {
      mode: 'REPLACE',
      outputId,
      rootNodeId: 'n_observation',
      rootOccurrenceId: 'base',
      catalogSnapshotToken: 'sha256:catalog-snapshot',
    },
  }));
  assert.equal(response.status, 'READY');
  assert.equal(Object.hasOwn(response, 'outputId'), false,
    'The row-change assessment schema does not define a top-level outputId.');
  assert.equal(response.proposal.outputId, outputId,
    'The server-authored replacement proposal carries the table output binding.');

  const workflowSource = await readFile(new URL('../../workflows/cda-same-table-root-replacement.mjs', import.meta.url), 'utf8');
  assert.match(workflowSource, /assert\.equal\(proposal\.outputId, outputId\)/);
  assert.match(workflowSource, /outputId: secondReview\.assessment\.proposal\.outputId/);
  assert.doesNotMatch(workflowSource, /assessment\.outputId/);
});


test('replacement assessment keeps its server ID column in the returned review scope', async () => {
  const workflowSource = await readFile(new URL('../../workflows/cda-same-table-root-replacement.mjs', import.meta.url), 'utf8');
  const start = workflowSource.indexOf('const assessObservationReplacement = async');
  const end = workflowSource.indexOf("const firstReview = await assessObservationReplacement(", start);
  assert(start >= 0 && end > start);
  const assess = workflowSource.slice(start, end);
  assert.match(assess, /let idColumn;\s*await act\(/);
  assert.match(assess, /idColumn = idColumns\[0\]/);
  assert.match(assess, /return \{ assessment, proposalGrid, typedPreview, idColumn \}/);
});

test('filter and replacement candidate previews are read from their distinct native table owners', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html><html><body>
      <section aria-label="Table result" data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="patients">
        <div data-testid="construction-proposal-preview" data-preview-status="ready" data-preview-receipt-id="filter-receipt" data-preview-output-id="patients">
          <table><thead><tr><th><span>Patient ID</span><span>string</span></th></tr></thead><tbody>
            <tr data-testid="construction-proposal-preview-row"><td>patient-1</td></tr>
          </tbody></table>
        </div>
      </section>
      <section role="dialog" aria-labelledby="root-replacement-title">
        <h2 id="root-replacement-title">Replace this table’s current setup?</h2>
        <select aria-label="Choose replacement starting type">
          <option value="observation-node"> Observation   observations </option>
          <option value="blocked-node" disabled>Blocked   type</option>
        </select>
        <section class="mt-4 rounded-lg border border-slate-200 bg-white p-3">
          <h3>Candidate preview</h3>
          <div class="mt-2 max-h-72 overflow-auto">
            <table data-testid="construction-proposal-preview"><thead><tr><th>id</th></tr></thead><tbody>
              ${OBSERVATION_DEFAULT_PAGE_IDS.map(id => `<tr><td>${id}</td></tr>`).join('')}
            </tbody></table>
          </div>
        </section>
      </section>
    </body></html>`);

    const filter = await page.evaluate(readGridInPage, { kind: 'filter-proposal', outputId: 'patients' });
    assert.equal(filter.ready, true);
    assert.equal(filter.previewOutputId, 'patients');
    assert.deepEqual(filter.headers, ['Patient ID']);
    assert.deepEqual(filter.rows, [['patient-1']]);

    const replacement = await page.evaluate(readGridInPage, { kind: 'replacement-proposal', outputId: 'patients' });
    assert.equal(replacement.ready, true);
    assert.deepEqual(replacement.headers, ['id']);
    assert.deepEqual(rowIDs(replacement, 'id'), OBSERVATION_DEFAULT_PAGE_IDS,
      'The actual row reader and ID assertion must match the retained 25-row replacement DOM exactly.');
    assert.equal(replacement.rows.length, OBSERVATION_DEFAULT_PAGE_IDS.length);

    await page.locator('table[data-testid="construction-proposal-preview"] tbody').evaluate(tbody => tbody.replaceChildren());
    const emptyReplacement = await page.evaluate(readGridInPage, { kind: 'replacement-proposal', outputId: 'patients' });
    assert.notDeepEqual(rowIDs(emptyReplacement, 'id'), OBSERVATION_DEFAULT_PAGE_IDS,
      'A mounted but empty proposal table cannot pass the exact independent row oracle.');
    await page.locator('table[data-testid="construction-proposal-preview"] tbody')
      .evaluate(tbody => { tbody.innerHTML = '<tr><td colspan="1">No sample rows are available.</td></tr>'; });
    const emptyStateReplacement = await page.evaluate(readGridInPage, { kind: 'replacement-proposal', outputId: 'patients' });
    assert.notDeepEqual(rowIDs(emptyStateReplacement, 'id'), OBSERVATION_DEFAULT_PAGE_IDS,
      'The component empty-state row cannot pass the exact independent row oracle.');

    const replacementOptions = await page.getByRole('combobox', { name: 'Choose replacement starting type' })
      .locator('option').evaluateAll(replacementTypeOptionsInPage);
    assert.deepEqual(replacementOptions, [
      { value: 'observation-node', label: 'Observation observations', disabled: false },
      { value: 'blocked-node', label: 'Blocked type', disabled: true },
    ]);

    const workflowSource = await readFile(new URL('../../workflows/cda-same-table-root-replacement.mjs', import.meta.url), 'utf8');
    assert.match(workflowSource, /const filterCandidate = await readGrid\('filter-proposal'\)/);
    assert.match(workflowSource, /proposalGrid = await readGrid\('replacement-proposal'\)/);
    assert.doesNotMatch(workflowSource, /readGrid\('proposal'\)/);
  } finally {
    await browser.close();
  }
});

test('applied filter exposes Table actions and the saved grid without an operation editor', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html><html><body>
      <nav aria-label="Table actions">
        <button type="button" aria-label="Filter rows: Choose which rows appear in the table output.">Filter rows</button>
      </nav>
      <section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="patients">
        <div data-testid="preview-table-scroll" style="height:220px;overflow:auto">
          <div role="table" aria-rowcount="2">
            <div role="row"><div role="columnheader">Patient ID</div></div>
            <div role="row"><button aria-label="Inspect row 1 identity">1</button><div role="cell">patient-1</div></div>
          </div>
        </div>
      </section>
    </body></html>`);

    await page.getByRole('navigation', { name: 'Table actions', exact: true })
      .getByRole('button', { name: 'Filter rows: Choose which rows appear in the table output.', exact: true })
      .waitFor({ state: 'visible' });
    assert.equal(await page.getByTestId('construction-operation-editor').count(), 0,
      'After filter Apply the operation editor can already be absent.');
    const saved = await page.evaluate(readGridInPage, { kind: 'saved', outputId: 'patients' });
    assert.equal(saved.ready, true);
    assert.deepEqual(rowIDs(saved, 'Patient ID'), ['patient-1']);
  } finally {
    await browser.close();
  }
});

test('saved replacement rows are collected by stable position across native virtual-grid scrolling', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 640, height: 280 } });
    await page.setContent(`<!doctype html><html><body>
      <section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="observation-output">
        <div data-testid="preview-table-scroll" style="height:220px;overflow:auto">
          <div role="table" aria-rowcount="26" style="position:relative;width:480px;height:1142px"></div>
        </div>
      </section>
      <div role="dialog" aria-label="Row definition settings" style="position:fixed;inset:0;z-index:10;background:white">
        <button type="button" id="back-to-table">Back to table</button>
      </div>
      <script>
        const ids = ${JSON.stringify(OBSERVATION_DEFAULT_PAGE_IDS)};
        const scroller = document.querySelector('[data-testid="preview-table-scroll"]');
        const table = scroller.querySelector('[role="table"]');
        const renderWindow = () => {
          const headerHeight = 42;
          const rowHeight = 44;
          const offset = Math.max(0, scroller.scrollTop - headerHeight);
          const viewport = Math.max(0, scroller.clientHeight - headerHeight);
          const start = Math.max(0, Math.floor(offset / rowHeight) - 3);
          const end = Math.min(ids.length, Math.ceil((offset + viewport) / rowHeight) + 3);
          const header = document.createElement('div');
          header.setAttribute('role', 'row');
          const name = document.createElement('div');
          name.setAttribute('role', 'columnheader');
          name.textContent = 'id';
          header.append(name);
          table.replaceChildren(header);
          for (let index = start; index < end; index += 1) {
            const row = document.createElement('div');
            row.setAttribute('role', 'row');
            row.style.position = 'absolute';
            row.style.top = (headerHeight + index * rowHeight) + 'px';
            const inspect = document.createElement('button');
            inspect.setAttribute('aria-label', 'Inspect row ' + (index + 1) + ' identity');
            inspect.textContent = String(index + 1);
            const cell = document.createElement('div');
            cell.setAttribute('role', 'cell');
            cell.textContent = ids[index];
            row.append(inspect, cell);
            table.append(row);
          }
        };
        scroller.scrollTop = 600;
        let renderTimer;
        scroller.addEventListener('scroll', () => {
          clearTimeout(renderTimer);
          renderTimer = setTimeout(renderWindow, 400);
        });
        document.getElementById('back-to-table').addEventListener('click', () =>
          document.querySelector('[role="dialog"]').remove());
        renderWindow();
      </script>
    </body></html>`);

    const firstWindow = await page.evaluate(readGridInPage, { kind: 'saved', outputId: 'observation-output' });
    assert.equal(firstWindow.ready, true);
    assert.equal(firstWindow.ariaRowCount, '26');
    assert(firstWindow.rows.length < OBSERVATION_DEFAULT_PAGE_IDS.length,
      'The fixture must mount only the visible rows and overscan, like PreviewTable.');
    assert(firstWindow.scroll.top > 0, 'The saved table fixture must start below its first row.');
    assert(!firstWindow.rowIndexes.includes(1), 'The initial viewport must omit leading row positions.');
    assert.equal(await page.evaluate(savedPreviewReady, [25, 'observation-output', 'id']), true);
    const settingsDialog = page.getByRole('dialog', { name: 'Row definition settings' });
    await page.getByRole('button', { name: 'Back to table', exact: true }).click();
    await settingsDialog.waitFor({ state: 'hidden' });
    await page.getByTestId('preview-table-scroll').hover();
    await page.mouse.wheel(0, 100);
    await page.waitForFunction(previousTop =>
      document.querySelector('[data-testid="preview-table-scroll"]').scrollTop > previousTop,
    firstWindow.scroll.top, { timeout: 1_000 });
    const scrolledBeforeMount = await page.evaluate(readGridInPage, { kind: 'saved', outputId: 'observation-output' });
    assert(scrolledBeforeMount.scroll.top > firstWindow.scroll.top,
      'Native input changes scroll position before the delayed virtual window is mounted.');
    assert.deepEqual(scrolledBeforeMount.rowIndexes, firstWindow.rowIndexes,
      'The fixture must expose the stale mounted window during the delayed-render race.');
    assert.equal(await page.evaluate(savedGridWindowChanged,
      [firstWindow.scroll.top, firstWindow.rowIndexes]), false,
    'A scrollTop change alone must not satisfy the wait for newly mounted virtual rows.');

    const collected = await collectSavedGridRows({
      page,
      cda: { wait: (predicate, args, timeout) => waitForBrowser(page, predicate, args, timeout) },
      expectedIDs: OBSERVATION_DEFAULT_PAGE_IDS,
      idLabel: 'id',
      outputId: 'observation-output',
      timeoutMs: 5_000,
    });
    assert.deepEqual(collected.rowIndexes, Array.from({ length: 25 }, (_, index) => index + 1),
      'Each expected row position must be represented exactly once in the ordered result.');
    assert.deepEqual(rowIDs(collected, 'id'), OBSERVATION_DEFAULT_PAGE_IDS,
      'The native scroll collector must preserve every exact oracle ID in row order.');
  } finally {
    await browser.close();
  }
});

test('a fully mounted one-row saved table needs no hover or wheel input', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html><html><body>
      <section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="patient-output">
        <div data-testid="preview-table-scroll" style="height:220px;overflow:auto">
          <div role="table" aria-rowcount="2">
            <div role="row"><div role="columnheader">Patient ID</div></div>
            <div role="row"><button aria-label="Inspect row 1 identity">1</button><div role="cell">patient-1</div></div>
          </div>
        </div>
      </section>
    </body></html>`);
    let hoverCalls = 0;
    let wheelCalls = 0;
    const pageAdapter = {
      getByTestId: name => {
        const locator = page.getByTestId(name);
        return {
          waitFor: options => locator.waitFor(options),
          hover: options => {
            hoverCalls += 1;
            return locator.hover(options);
          },
        };
      },
      evaluate: (...args) => page.evaluate(...args),
      mouse: {
        wheel: (...args) => {
          wheelCalls += 1;
          return page.mouse.wheel(...args);
        },
      },
    };
    const grid = await collectSavedGridRows({
      page: pageAdapter,
      cda: { wait: async () => assert.fail('A fully mounted row must not need scrolling.') },
      expectedIDs: ['patient-1'],
      idLabel: 'Patient ID',
      outputId: 'patient-output',
    });
    assert.deepEqual(grid.rowIndexes, [1]);
    assert.deepEqual(rowIDs(grid, 'Patient ID'), ['patient-1']);
    assert.equal(hoverCalls, 0);
    assert.equal(wheelCalls, 0);
  } finally {
    await browser.close();
  }
});

test('a fully mounted saved grid with row values misaligned to stable positions is rejected', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<!doctype html><html><body>
      <section data-testid="construction-preview" data-preview-status="ready" data-preview-output-id="patient-output">
        <div data-testid="preview-table-scroll" style="height:220px;overflow:auto">
          <div role="table" aria-rowcount="3">
            <div role="row"><div role="columnheader">Patient ID</div></div>
            <div role="row"><button aria-label="Inspect row 2 identity">2</button><div role="cell">patient-1</div></div>
            <div role="row"><button aria-label="Inspect row 1 identity">1</button><div role="cell">patient-2</div></div>
          </div>
        </div>
      </section>
    </body></html>`);

    const malformed = await page.evaluate(readGridInPage, { kind: 'saved', outputId: 'patient-output' });
    assert.deepEqual(malformed.rowIndexes, [2, 1]);
    assert.deepEqual(rowIDs(malformed, 'Patient ID'), ['patient-1', 'patient-2']);
    await assert.rejects(() => collectSavedGridRows({
      page,
      cda: { wait: async () => assert.fail('A fully mounted malformed grid must not need scrolling.') },
      expectedIDs: ['patient-1', 'patient-2'],
      idLabel: 'Patient ID',
      outputId: 'patient-output',
    }), /Patient ID preview must match the independently retained exact ordered source page/);
  } finally {
    await browser.close();
  }
});
