import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { performance } from 'node:perf_hooks';
import { hashScopedPayloadRows } from '../helpers/cda-payload-content-hashes.mjs';
import { validatedArangoContainer } from '../helpers/native-cda-workflow-tools.mjs';

const ACTION_BUDGET_MS = 5_000;
const CODING_PATH = 'code.coding[]';
const ROOT_TITLE = 'CDA direct-code grouping';
const SOURCE_IDS = Object.freeze([
  '26c7500c-f54f-5229-b5a7-a6757614bc21',
  '64704041-c4da-5375-aac0-88f951f78344',
  '7d2fe423-f934-56ff-bd96-426fb1fdf94e',
]);
const SUBJECT = 'Patient/0000da62-8ff3-5dc2-b9c5-c2c0b75442a2';
const RAW_VALUE_TUPLES = Object.freeze([
  Object.freeze({ id: SOURCE_IDS[0], status: 'final', subjectReference: SUBJECT,
    system: 'http://snomed.info/sct', version: null, code: '1222593009' }),
  Object.freeze({ id: SOURCE_IDS[1], status: 'final', subjectReference: SUBJECT,
    system: 'http://snomed.info/sct', version: null, code: '1222593009' }),
  Object.freeze({ id: SOURCE_IDS[2], status: 'final', subjectReference: SUBJECT,
    system: 'http://loinc.org', version: null, code: '81247-9' }),
]);
const EXPECTED_HEADERS = Object.freeze(['Code system', 'Code version', 'Code', 'Source records']);
const EXPECTED_ROWS = Object.freeze([
  Object.freeze(['http://loinc.org', '—', '81247-9', '1']),
  Object.freeze(['http://snomed.info/sct', '—', '1222593009', '2']),
]);

const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const sortedRows = rows => [...rows].map(row => JSON.stringify(row)).sort();
const projectBase = (project, explorer) => `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;

function rawArangoRows(container, project, generation) {
  const query = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation" AND r.id IN ${JSON.stringify(SOURCE_IDS)}
    SORT r.id
    RETURN {id:r.id,project:r.project,generation:r.dataset_generation,resourceType:r.payload.resourceType,
      status:r.payload.status,subjectReference:r.payload.subject.reference,
      codings:(IS_ARRAY(r.payload.code.coding) ? r.payload.code.coding[* RETURN {
        system:CURRENT.system,version:(IS_STRING(CURRENT.version) ? CURRENT.version : null),code:CURRENT.code
      }] : [])}`;
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', container, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 4 * 1024 * 1024 });
  assert.equal(result.status, 0, `Bounded CDA code oracle failed: ${clean(result.stderr || result.stdout).slice(0, 1200)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `CDA code oracle returned no JSON array: ${clean(result.stdout).slice(-500)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return rows;
}

function rawPayloadHashes(container, project, generation) {
  const query = `FOR r IN Observation
    FILTER r.project == ${JSON.stringify(project)} AND r.dataset_generation == ${JSON.stringify(generation)}
      AND r.payload.resourceType == "Observation" AND r.id IN ${JSON.stringify(SOURCE_IDS)}
    SORT r.id
    RETURN {id:r.id,payload:r.payload}`;
  const javascript = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`.replaceAll('@', '\\u0040');
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', container, 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30_000, maxBuffer: 4 * 1024 * 1024 });
  assert.equal(result.status, 0, `Bounded CDA payload hash read failed: ${clean(result.stderr || result.stdout).slice(0, 1200)}`);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, `CDA payload hash read returned no JSON array: ${clean(result.stdout).slice(-500)}`);
  const rows = JSON.parse(result.stdout.slice(start));
  assert(Array.isArray(rows));
  return hashScopedPayloadRows(rows, SOURCE_IDS);
}

function normalizeRawRows(rows, project, generation) {
  assert.equal(rows.length, SOURCE_IDS.length, 'The exact CDA source read must return all three selected Observations.');
  const byID = new Map(rows.map(row => [row.id, row]));
  assert.equal(byID.size, rows.length, 'The exact CDA source read must not duplicate Observation IDs.');
  assert.deepEqual([...byID.keys()].sort(), [...SOURCE_IDS].sort(), 'The exact CDA source read must contain only selected IDs.');
  const tuples = rows.map(row => {
    assert.equal(row.project, project);
    assert.equal(row.generation, generation);
    assert.equal(row.resourceType, 'Observation');
    assert.equal(row.status, 'final');
    assert.equal(row.subjectReference, SUBJECT);
    assert.equal(row.codings.length, 1, `Observation/${row.id} must have exactly one direct Coding.`);
    const [coding] = row.codings;
    assert.equal(typeof coding.system, 'string');
    assert.equal(typeof coding.code, 'string');
    assert.equal(coding.version, null);
    return { id: row.id, status: row.status, subjectReference: row.subjectReference,
      system: coding.system, version: coding.version, code: coding.code };
  }).sort((left, right) => left.id.localeCompare(right.id));
  assert.deepEqual(tuples, [...RAW_VALUE_TUPLES].sort((left, right) => left.id.localeCompare(right.id)),
    'The raw direct-code tuples must match the independent CDA witness contract.');
  return tuples;
}

function expectedRowsFromRawTuples(tuples) {
  const groups = new Map();
  for (const tuple of tuples) {
    const key = JSON.stringify([tuple.system, tuple.version, tuple.code]);
    groups.set(key, (groups.get(key) ?? 0) + 1);
  }
  return [...groups].map(([key, count]) => {
    const [system, version, code] = JSON.parse(key);
    return [system, version ?? '—', code, String(count)];
  }).sort((left, right) => left[0].localeCompare(right[0]) || left[1].localeCompare(right[1]) || left[2].localeCompare(right[2]));
}

async function bounded(promise, timeout, label) {
  const budgetMs = typeof timeout === 'function' ? timeout() : timeout;
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${label} exceeded the remaining ${budgetMs} ms action budget.`)), budgetMs);
    })]);
  } finally {
    clearTimeout(timer);
  }
}

async function readGrid(page, proposal = false, timeout = ACTION_BUDGET_MS) {
  return bounded(page.evaluate(({ proposal: isProposal }) => {
    const preview = isProposal
      ? document.querySelector('[data-testid="construction-proposal-preview"]')
      : document.querySelector('[data-testid="construction-preview"]');
    const table = isProposal ? preview?.querySelector('table')
      : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    if (!preview || !table) return { ready: false, outputId: null, status: null, headers: [], rows: [] };
    const tidy = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    const headers = isProposal
      ? [...table.querySelectorAll('thead th')].map(cell => tidy(cell.querySelector('span')?.textContent ?? cell.textContent))
      : [...table.querySelectorAll('[role="columnheader"]')].map(cell => tidy(cell.textContent));
    const rows = isProposal
      ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => tidy(cell.innerText)))
      : [...table.querySelectorAll('[role="row"]')].slice(1)
        .filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => tidy(cell.innerText)));
    return { ready: preview.dataset.previewStatus === 'ready', outputId: preview.dataset.previewOutputId,
      status: preview.dataset.previewStatus, headers, rows,
      ariaRowCount: table.getAttribute('aria-rowcount'), ariaColumnCount: table.getAttribute('aria-colcount') };
  }, { proposal }), timeout, 'Reading the CDA preview grid');
}

async function readBuilder(page, apiOrigin, project, explorer, timeout = ACTION_BUDGET_MS) {
  const path = `${projectBase(project, explorer)}/authoring/v2/builder`;
  const response = await page.context().request.get(`${apiOrigin}${path}`, { timeout });
  const body = await response.json();
  assert(response.ok(), `Builder read returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 1000)}`);
  return body;
}

function documentFor(builder, outputId) {
  const matches = (builder.workspace?.documents ?? []).filter(document => document.output?.id === outputId);
  assert.equal(matches.length, 1, `Expected exactly one document for output ${outputId}.`);
  return matches[0];
}

function assertSavedGroup(builder, project, generation, outputId, stepId, missingKeyPolicy) {
  assert.equal(builder.catalog?.generation, generation);
  const document = documentFor(builder, outputId);
  assert.equal(document.rootResourceType, 'Observation');
  assert.equal(document.output.title, ROOT_TITLE);
  const steps = document.construction?.steps ?? [];
  assert.equal(steps.length, 1);
  const [step] = steps;
  assert.equal(step.operation.kind, 'CODED_GROUP');
  assert.equal(step.operation.codedGroup.source.occurrenceId, 'base');
  assert.equal(step.operation.codedGroup.source.resourceType, 'Observation');
  assert.equal(step.operation.codedGroup.source.codingPath, CODING_PATH);
  assert.equal(step.operation.codedGroup.source.fhirType, 'Coding');
  assert.equal(step.operation.codedGroup.source.cardinality, 'MANY');
  assert.equal(step.operation.codedGroup.source.shape, 'ARRAY');
  assert.deepEqual(step.operation.codedGroup.source.route, []);
  assert.equal(step.operation.codedGroup.missingKeyPolicy, missingKeyPolicy);
  if (stepId) assert.equal(step.id, stepId);
  assert.deepEqual(step.outputs.map(({ name, label }) => ({ name, label })), [
    { name: 'code_system', label: 'Code system' },
    { name: 'code_version', label: 'Code version' },
    { name: 'code', label: 'Code' },
    { name: 'source_records', label: 'Source records' },
  ]);
  return { document, step };
}

export async function cdaCodedGroupNativeWorkflow({ page, cda }) {
  const project = cda.project;
  const generation = cda.generation;
  const apiOrigin = String(cda.apiOrigin).replace(/\/$/, '');
  const uiOrigin = String(cda.uiOrigin).replace(/\/$/, '');
  const arango = validatedArangoContainer(cda.target);
  assert.equal(project, 'loom_dev_cda_fhir');
  assert.equal(generation, 'cda-fhir-v1');
  assert.equal(project, cda.target.fixtureProject);
  assert.equal(generation, cda.target.fixtureGeneration);
  assert(apiOrigin && uiOrigin);

  const report = cda.report;
  report.phase = 'CDA direct Observation.code.coding[] CODED_GROUP lifecycle';
  report.scope = { project, generation, selectedObservationIDs: SOURCE_IDS, codingPath: CODING_PATH,
    actionBudgetMs: ACTION_BUDGET_MS, rawRowLimit: SOURCE_IDS.length };
  const initialRaw = normalizeRawRows(rawArangoRows(arango, project, generation), project, generation);
  const initialPayloadHashes = rawPayloadHashes(arango, project, generation);
  const expectedRows = expectedRowsFromRawTuples(initialRaw);
  assert.deepEqual(expectedRows, EXPECTED_ROWS, 'The literal direct-code Group rows must match the CDA raw witness.');
  report.oracle = { project, generation, selectedIDs: SOURCE_IDS, codingPath: CODING_PATH,
    rawValueTuples: initialRaw, expectedHeaders: EXPECTED_HEADERS, expectedRows };
  cda.check('correctness', 'bounded raw CDA source matches exact direct code tuples and Group rows', true, report.oracle);

  const api = async (path, data) => {
    const headers = { 'X-Request-ID': `cda-coded-group-${randomUUID()}` };
    const response = data === undefined
      ? await cda.request.get(`${apiOrigin}${path}`, { headers, timeout: 30_000 })
      : await cda.request.post(`${apiOrigin}${path}`, { headers, data, timeout: 30_000 });
    const text = await response.text();
    let body;
    try { body = text ? JSON.parse(text) : null; }
    catch { throw new Error(`${path} returned non-JSON HTTP ${response.status()}: ${clean(text).slice(0, 1000)}`); }
    assert(response.ok(), `${path} returned HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 1200)}`);
    return body;
  };
  const rootPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  const explorer = `cda-coded-group-${randomUUID()}`;
  report.explorer = explorer;
  const created = await api(rootPath, { name: explorer, title: 'CDA direct-code grouping verification' });
  assert.equal(created.explorerId ?? created.id ?? created.explorer?.id, explorer);
  const base = projectBase(project, explorer);
  const builder0 = await readBuilder(page, apiOrigin, project, explorer);
  assert.equal(builder0.catalog.generation, generation);
  const selection = await api(`${base}/selections`, {
    snapshotToken: builder0.catalog.snapshotToken,
    idempotencyKey: `cda-coded-group-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs: SOURCE_IDS.map(id => ({
      project, generation, resourceType: 'Observation', id,
    })) } },
  });
  assert(selection.id);
  const url = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}`
    + `&mode=builder&selection=${encodeURIComponent(selection.id)}`;

  const action = async (label, locator, settle, perform = (target, remaining) => target.click({ timeout: remaining() })) => {
    const started = performance.now();
    const remaining = () => Math.max(1, ACTION_BUDGET_MS - (performance.now() - started));
    await cda.action(label, locator, target => perform(target, remaining), {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      after: async () => {
        await settle?.(remaining);
        const elapsedMs = performance.now() - started;
        assert(elapsedMs <= ACTION_BUDGET_MS, `${label} exceeded the ${ACTION_BUDGET_MS} ms action-to-render budget (${Math.round(elapsedMs)} ms).`);
      },
      requiredCheck: 'native CDA direct-code actions settle within five seconds',
    });
  };
  const select = async (label, locator, value, settle) => {
    const started = performance.now();
    const remaining = () => Math.max(1, ACTION_BUDGET_MS - (performance.now() - started));
    await cda.action(label, locator, target => target.selectOption(value, { timeout: remaining() }), {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      after: async () => {
        assert.equal(await locator.inputValue({ timeout: remaining() }), value);
        await settle?.(remaining);
        const elapsedMs = performance.now() - started;
        assert(elapsedMs <= ACTION_BUDGET_MS, `${label} exceeded the ${ACTION_BUDGET_MS} ms action-to-render budget (${Math.round(elapsedMs)} ms).`);
      },
      requiredCheck: 'native CDA direct-code actions settle within five seconds',
    });
  };
  const fill = async (label, locator, value) => {
    const started = performance.now();
    const remaining = () => Math.max(1, ACTION_BUDGET_MS - (performance.now() - started));
    await cda.action(label, locator, target => target.fill(value, { timeout: remaining() }), {
      timeout: ACTION_BUDGET_MS,
      budget: ACTION_BUDGET_MS,
      editable: true,
      after: async () => {
        assert.equal(await locator.inputValue({ timeout: remaining() }), value);
        const elapsedMs = performance.now() - started;
        assert(elapsedMs <= ACTION_BUDGET_MS, `${label} exceeded the ${ACTION_BUDGET_MS} ms budget.`);
      },
      requiredCheck: 'native CDA direct-code actions settle within five seconds',
    });
  };
  const timeoutMs = timeout => Math.max(1, Math.min(ACTION_BUDGET_MS,
    typeof timeout === 'function' ? timeout() : timeout));
  const wait = async (predicate, args = undefined, timeout = ACTION_BUDGET_MS) => page.waitForFunction(predicate, args,
    { timeout: timeoutMs(timeout) });
  const rowsReady = async (outputId, count, proposal = false, timeout = ACTION_BUDGET_MS) => {
    await wait(({ outputId: expectedOutput, count: expectedCount, proposal: isProposal }) => {
      const preview = isProposal ? document.querySelector('[data-testid="construction-proposal-preview"]')
        : document.querySelector('[data-testid="construction-preview"]');
      const table = isProposal ? preview?.querySelector('table')
        : document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const visibleCount = isProposal
        ? table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length
        : Number(table?.getAttribute('aria-rowcount')) - 1;
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput
        && visibleCount === expectedCount && (isProposal || (!document.body.innerText.includes('Loading your table…')
          && !document.body.innerText.includes('Preview failed:')));
    }, { outputId, count, proposal }, timeout);
  };
  const grid = async (outputId, rows, proposal = false, label = 'CDA coded-group preview', timeout = ACTION_BUDGET_MS) => {
    await rowsReady(outputId, rows.length, proposal, timeout);
    const actual = await readGrid(page, proposal, timeout);
    assert(actual.ready, `${label} must be ready.`);
    assert.equal(actual.outputId, outputId, `${label} must remain bound to the selected Observation output.`);
    assert.deepEqual(actual.headers, EXPECTED_HEADERS, `${label} headers must match CODED_GROUP outputs.`);
    assert.deepEqual(sortedRows(actual.rows), sortedRows(rows), `${label} rows must match independent raw observations.`);
    if (!proposal) {
      assert.equal(actual.ariaColumnCount, String(EXPECTED_HEADERS.length));
      assert.equal(Number(actual.ariaRowCount), rows.length + 1);
    }
    return actual;
  };
  const sourceGrid = async (outputId, proposal = false, timeout = ACTION_BUDGET_MS) => {
    await rowsReady(outputId, SOURCE_IDS.length, proposal, timeout);
    const actual = await readGrid(page, proposal, timeout);
    assert(actual.ready);
    assert.equal(actual.outputId, outputId);
    const idIndex = actual.headers.findIndex(header => /^Observation ID$/i.test(header) || /^ID$/i.test(header));
    assert(idIndex >= 0, `Source rows must expose the Observation ID; headers=${JSON.stringify(actual.headers)}`);
    assert.deepEqual(actual.rows.map(row => row[idIndex]).sort(), [...SOURCE_IDS].sort(),
      'Source rows must match the exact selected CDA Observation IDs.');
    return actual;
  };
  const openCodeGroup = async label => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    if (await settings.getAttribute('aria-expanded') !== 'true') {
      await action(`${label}: open row settings`, settings, async remaining => {
        await page.getByRole('dialog', { name: 'Row definition settings', exact: true })
          .waitFor({ state: 'visible', timeout: remaining() });
      });
    }
    const coded = page.getByTestId('construction-action-coded-group-rows');
    await action(`${label}: choose By recorded code`, coded, async remaining => {
      await page.getByTestId('construction-coded-group-path').waitFor({ state: 'visible', timeout: remaining() });
      const options = await bounded(page.getByTestId('construction-coded-group-path').locator('option')
        .evaluateAll(items => items.map(item => item.value)), remaining, 'Reading native coded-group choices');
      assert(options.includes(CODING_PATH), `Native coded-group choices must include ${CODING_PATH}.`);
    }, async (target, remaining) => {
      await target.waitFor({ state: 'visible', timeout: remaining() });
      await wait(() => {
        const button = document.querySelector('[data-testid="construction-action-coded-group-rows"]');
        return Boolean(button && !button.disabled);
      }, undefined, remaining());
      await target.click({ timeout: remaining() });
    });
  };
  const proposalReady = async (outputId, expected, label, timeout = ACTION_BUDGET_MS) => {
    const actual = await grid(outputId, expected, true, label, timeout);
    await wait(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', undefined, timeout);
    return actual;
  };

  await cda.navigate(url);
  await page.getByRole('button', { name: 'Choose Observation rows', exact: true }).waitFor({ state: 'visible', timeout: ACTION_BUDGET_MS });
  await fill('name the direct-code Observation root table', page.locator('#first-table-name'), ROOT_TITLE);
  await action('select Observation as the native CDA root', page.getByRole('button', { name: 'Choose Observation rows', exact: true }), async remaining => {
    await page.getByTestId('construction-rows-settings-trigger').waitFor({ state: 'visible', timeout: remaining() });
  });
  await action('open the native Observation starting-collection settings', page.getByTestId('construction-rows-settings-trigger'), async remaining => {
    await page.getByRole('button', { name: 'Use selected resources', exact: true }).waitFor({ state: 'visible', timeout: remaining() });
  });
  let initialGrid;
  let baseline;
  await action('apply the exact selected CDA Observation resources', page.getByRole('button', { name: 'Use selected resources', exact: true }), async remaining => {
    await wait(({ selectedCount }) => document.querySelector('[aria-label="Starting collection settings"]')?.innerText
      .includes(`${selectedCount} Observation resources attached`), { selectedCount: SOURCE_IDS.length }, remaining);
    baseline = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assert.equal(baseline.catalog?.generation, generation);
    const rootTables = (baseline.workspace?.documents ?? []).filter(document =>
      document.output?.title === ROOT_TITLE && document.rootResourceType === 'Observation');
    assert.equal(rootTables.length, 1, 'Native root setup must save exactly one Observation table.');
    const [rootTable] = rootTables;
    const outputId = rootTable.output.id;
    assert(outputId);
    assert.equal(rootTable.population?.selectionRevisionId, selection.id,
      'The saved root table must use the exact selected CDA resource revision.');
    assert.deepEqual(rootTable.construction?.steps ?? [], [], 'Source selection must not add a row operation.');
    initialGrid = await sourceGrid(outputId, false, remaining);
  });
  const outputId = initialGrid.outputId;
  assert(outputId);
  const baselineDocument = documentFor(baseline, outputId);
  assert.equal(baselineDocument.rootResourceType, 'Observation');
  assert.equal(baselineDocument.output.title, ROOT_TITLE);
  assert.deepEqual(baselineDocument.construction?.steps ?? [], []);
  const baselineDigest = baseline.draftDigest;
  cda.check('correctness', 'native source selection saves the exact Observation root and three selected IDs', true, {
    explorer, outputId, project, generation, selectedIDs: SOURCE_IDS, draftDigest: baselineDigest,
  });

  await openCodeGroup('initial Preview');
  const path = page.getByTestId('construction-coded-group-path');
  await select('select direct Observation.code.coding[] for native CODED_GROUP', path, CODING_PATH,
    async remaining => proposalReady(outputId, EXPECTED_ROWS, 'Initial native CODED_GROUP Preview', remaining));
  const preview = await proposalReady(outputId, EXPECTED_ROWS, 'Initial native CODED_GROUP Preview');
  cda.check('correctness', 'native direct-code Preview renders exact LOINC and SNOMED tuples with source counts', true,
    { outputId, headers: preview.headers, rows: preview.rows, expectedRows: EXPECTED_ROWS });

  await action('Cancel initial native CODED_GROUP Preview', page.getByTestId('construction-cancel-proposal'), async remaining => {
    await page.getByTestId('construction-proposal-panel').waitFor({ state: 'hidden', timeout: remaining() });
    await rowsReady(outputId, SOURCE_IDS.length, false, remaining);
    const restored = await sourceGrid(outputId, false, remaining);
    const current = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assert.equal(current.draftDigest, baselineDigest, 'Preview Cancel must preserve the saved draft digest.');
    assert.deepEqual(documentFor(current, outputId), baselineDocument, 'Preview Cancel must preserve the exact root document.');
    assert.equal(restored.rows.length, SOURCE_IDS.length);
  });
  cda.check('persistence', 'Cancel restores exact source IDs and leaves the native draft unchanged', true,
    { outputId, selectedIDs: SOURCE_IDS, draftDigest: baselineDigest });

  await openCodeGroup('confirmed Apply Preview');
  await select('reselect direct Observation.code.coding[] before Apply', path, CODING_PATH,
    async remaining => proposalReady(outputId, EXPECTED_ROWS, 'Confirmed native CODED_GROUP Preview', remaining));
  await action('Apply native direct-code CODED_GROUP', page.getByTestId('construction-apply-proposal'), async remaining => {
    await grid(outputId, EXPECTED_ROWS, false, 'Applied native CODED_GROUP rows', remaining);
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    const group = assertSavedGroup(saved, project, generation, outputId, undefined, 'GROUP');
    assert(group.step.id);
  });
  const applied = await readBuilder(page, apiOrigin, project, explorer);
  const appliedGroup = assertSavedGroup(applied, project, generation, outputId, undefined, 'GROUP');
  const stepId = appliedGroup.step.id;
  cda.check('persistence', 'Apply saves direct CODED_GROUP path, row schema, and exact source counts', true,
    { outputId, stepId, codingPath: CODING_PATH, rows: EXPECTED_ROWS });

  const rootTab = page.getByTestId(`construction-table-${outputId}`);
  await action('reload Builder with applied CDA CODED_GROUP', rootTab, async remaining => {
    await grid(outputId, EXPECTED_ROWS, false, 'Reloaded native CODED_GROUP rows', remaining);
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assertSavedGroup(saved, project, generation, outputId, stepId, 'GROUP');
  }, (_target, remaining) => page.reload({ waitUntil: 'domcontentloaded', timeout: remaining() }));
  cda.check('persistence', 'Reload retains exact grouped rows and the saved direct-code step identity', true,
    { outputId, stepId, rows: EXPECTED_ROWS });

  const settings = page.getByTestId('construction-rows-settings-trigger');
  await action('open row operations for CDA CODED_GROUP edit', settings, async remaining => {
    await page.getByTestId('construction-row-operation-history').waitFor({ state: 'visible', timeout: remaining() });
  });
  await action('edit the saved CDA CODED_GROUP step', page.getByTestId(`construction-row-edit-${stepId}`), async remaining => {
    await page.getByTestId('construction-reshape-coded-group').waitFor({ state: 'visible', timeout: remaining() });
    assert.equal(await path.inputValue({ timeout: remaining() }), CODING_PATH);
    assert.equal(await page.getByTestId('construction-coded-group-missing').inputValue({ timeout: remaining() }), 'GROUP');
    await grid(outputId, EXPECTED_ROWS, false, 'Unchanged saved CODED_GROUP edit Preview', remaining);
  });
  await action('open the native missing-code policy controls', page.locator('[data-testid="construction-reshape-coded-group"] summary'), async remaining => {
    await wait(() => document.querySelector('[data-testid="construction-reshape-coded-group"] details')?.open === true, undefined, remaining());
  });
  await select('edit missing-code policy to EXCLUDE', page.getByTestId('construction-coded-group-missing'), 'EXCLUDE',
    async remaining => proposalReady(outputId, EXPECTED_ROWS, 'Edited EXCLUDE CODED_GROUP Preview', remaining));
  await action('Apply the native CDA CODED_GROUP edit', page.getByTestId('construction-apply-proposal'), async remaining => {
    await grid(outputId, EXPECTED_ROWS, false, 'Applied edited CODED_GROUP rows', remaining);
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assertSavedGroup(saved, project, generation, outputId, stepId, 'EXCLUDE');
  });
  cda.check('persistence', 'native CODED_GROUP edit saves EXCLUDE and preserves exact code tuples', true,
    { outputId, stepId, missingKeyPolicy: 'EXCLUDE', rows: EXPECTED_ROWS });

  await action('reload Builder after CDA CODED_GROUP edit', rootTab, async remaining => {
    await grid(outputId, EXPECTED_ROWS, false, 'Reloaded edited CODED_GROUP rows', remaining);
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assertSavedGroup(saved, project, generation, outputId, stepId, 'EXCLUDE');
  }, (_target, remaining) => page.reload({ waitUntil: 'domcontentloaded', timeout: remaining() }));
  cda.check('persistence', 'edited CODED_GROUP and exact code counts survive reload', true,
    { outputId, stepId, rows: EXPECTED_ROWS });

  await action('open row operations for CDA CODED_GROUP removal', settings, async remaining => {
    await page.getByTestId('construction-row-operation-history').waitFor({ state: 'visible', timeout: remaining() });
  });
  await action('preview removal of the CDA CODED_GROUP step', page.getByTestId(`construction-row-remove-${stepId}`), async remaining => {
    const restored = await sourceGrid(outputId, true, remaining);
    assert.deepEqual(restored.rows.map(row => row[0]).sort(), [...SOURCE_IDS].sort());
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assertSavedGroup(saved, project, generation, outputId, stepId, 'EXCLUDE');
    await page.getByTestId('construction-removal-summary').waitFor({ state: 'visible', timeout: remaining() });
  });
  cda.check('correctness', 'removal Preview restores the exact three selected Observation IDs while remaining unsaved', true,
    { outputId, selectedIDs: SOURCE_IDS, stepId });

  await action('Apply removal and restore CDA Observation source rows', page.getByTestId('construction-apply-proposal'), async remaining => {
    const restored = await sourceGrid(outputId, false, remaining);
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    assert.deepEqual(documentFor(saved, outputId).construction?.steps ?? [], []);
    assert.deepEqual(restored.rows.map(row => row[0]).sort(), [...SOURCE_IDS].sort());
  });
  await action('reload Builder after CDA CODED_GROUP removal', rootTab, async remaining => {
    const restored = await sourceGrid(outputId, false, remaining);
    const saved = await readBuilder(page, apiOrigin, project, explorer, remaining());
    const document = documentFor(saved, outputId);
    assert.equal(document.rootResourceType, 'Observation');
    assert.deepEqual(document.construction?.steps ?? [], []);
    assert.deepEqual(restored.rows.map(row => row[0]).sort(), [...SOURCE_IDS].sort());
  }, (_target, remaining) => page.reload({ waitUntil: 'domcontentloaded', timeout: remaining() }));
  cda.check('persistence', 'final reload restores exact CDA Observation source IDs with no CODED_GROUP step', true,
    { outputId, selectedIDs: SOURCE_IDS });

  const finalRaw = normalizeRawRows(rawArangoRows(arango, project, generation), project, generation);
  const finalPayloadHashes = rawPayloadHashes(arango, project, generation);
  assert.deepEqual(finalRaw, initialRaw, 'The selected CDA source identity and direct code tuples must remain unchanged.');
  assert.deepEqual(finalPayloadHashes, initialPayloadHashes, 'The exact selected CDA source payloads must remain unchanged.');
  cda.check('correctness', 'full payload SHA-256 hashes for selected CDA source records remain unchanged', true,
    { selectedIDs: SOURCE_IDS, payloadHashes: finalPayloadHashes });
  report.result = { outputId, stepId, sourceIDs: SOURCE_IDS, codingPath: CODING_PATH,
    rawValueTuples: initialRaw, expectedHeaders: EXPECTED_HEADERS, expectedRows: EXPECTED_ROWS,
    finalDraftDigest: (await readBuilder(page, apiOrigin, project, explorer)).draftDigest };
}
