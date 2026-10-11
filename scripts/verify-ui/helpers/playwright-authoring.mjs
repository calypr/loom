import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect } from '@playwright/test';
import { browserURL } from '../workflows/builder-url.mjs';
import { collectPreviewRows, previewHeaderIndex } from './playwright-preview-rows.mjs';
import { configureNativePage, recordPlaywrightTiming } from './playwright-authoring-page.mjs';

const patientSource = target => {
  const contents = readFileSync(join(target.fixtureDir, 'Patient.ndjson'));
  return {
    sha256: createHash('sha256').update(contents).digest('hex'),
    ids: contents.toString('utf8').trim().split('\n').filter(Boolean)
      .map(line => JSON.parse(line).id).sort(),
  };
};

const patientSourceDigest = target => createHash('sha256')
  .update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');

export const existingRootChangeOracle = target => {
  const patientPath = join(target.fixtureDir, 'Patient.ndjson');
  const observationPath = join(target.fixtureDir, 'Observation.ndjson');
  const patientContents = readFileSync(patientPath);
  const observationContents = readFileSync(observationPath);
  const patientRows = patientContents.toString('utf8').trim().split('\n').filter(Boolean)
    .map(line => JSON.parse(line)).map(patient => ({ patientId: patient.id, gender: patient.gender ?? null }))
    .sort((left, right) => left.patientId.localeCompare(right.patientId));
  const patients = new Map(patientRows.map(patient => [patient.patientId, patient.gender]));
  const rows = observationContents.toString('utf8').trim().split('\n').filter(Boolean)
    .map(line => JSON.parse(line))
    .map(observation => {
      const match = /^Patient\/(.+)$/.exec(observation.subject?.reference ?? '');
      const patientId = match && patients.has(match[1]) ? match[1] : null;
      return { observationId: observation.id, patientId, gender: patientId ? patients.get(patientId) : null };
    }).sort((left, right) => left.observationId.localeCompare(right.observationId));
  return {
    patient: { path: patientPath, sha256: createHash('sha256').update(patientContents).digest('hex') },
    observation: { path: observationPath, sha256: createHash('sha256').update(observationContents).digest('hex') },
    patientRows,
    rows,
  };
};

export const rootOption = async (selector, resourceType) => {
  const options = await selector.locator('option').evaluateAll(optionNodes => optionNodes
    .map(option => ({ value: option.value, label: option.textContent?.trim(), disabled: option.disabled })));
  return options.find(option => option.value !== 'base' && option.label?.startsWith(resourceType));
};

export const normalizeRootChangePreviewCell = value => value === '—' ? null : value ?? null;

const sourceIdentifierValues = (target, ids) => {
  const wanted = new Set(ids);
  const values = new Map();
  for (const line of readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8').split('\n')) {
    if (!line) continue;
    const patient = JSON.parse(line);
    if (wanted.has(patient.id)) {
      values.set(patient.id, (patient.identifier ?? []).map(identifier => identifier.value ?? null));
    }
  }
  assert.equal(values.size, wanted.size, 'Independent CDA source must contain every expected preview Patient');
  return values;
};

const identifierDisplay = values => {
  const visible = values.filter(value => value !== null).map(String);
  return visible.length ? visible.join('; ') : '—';
};

export const patientWindow = (target, ids) => {
  if (ids.length <= 25) return ids;
  assert.equal(new Set(ids).size, ids.length, 'Patient source IDs must have distinct identities');
  const storageKey = id => createHash('sha256')
    .update(['vertex', target.fixtureProject, target.fixtureGeneration, 'Patient', id, ''].join('\0'))
    .digest('hex');
  return ids.map(id => ({ id, key: storageKey(id) }))
    .sort((left, right) => left.key.localeCompare(right.key))
    .slice(0, 25).map(item => item.id);
};

const waitVisible = (locator, timeout = 5000) => locator.waitFor({ state: 'visible', timeout: Math.min(5000, timeout) });

export const assertPreviewPatientIds = async (page, expectedIds) => {
  const rows = await page.getByTestId('preview-table-scroll').getByRole('row').all();
  const visibleIds = (await Promise.all(rows.slice(1).map(async row =>
    (await row.getByRole('cell').first().innerText()).trim()))).sort();
  assert.deepEqual(visibleIds, expectedIds, 'Preview must contain the exact independent fixture Patient IDs');
  return visibleIds;
};

export const assertPreviewPatientWindow = async (page, expectedIds, identifierValues) => {
  const scroll = page.getByTestId('preview-table-scroll');
  const table = scroll.getByRole('table');
  assert.equal(Number(await table.getAttribute('aria-rowcount')), expectedIds.length + 1,
    'Preview row count must equal the bounded independent Patient window');
  const dimensions = await scroll.evaluate(element => ({
    top: element.scrollTop, max: element.scrollHeight - element.clientHeight,
    step: Math.max(1, Math.floor(element.clientHeight * 0.7)),
  }));
  const seen = new Map();
  const inspect = async () => {
    const rows = await table.evaluate(element => [...element.querySelectorAll('[role="row"]')].slice(1).map(row => ({
      ordinal: Number(row.firstElementChild?.textContent?.trim()),
      id: row.querySelector('[role="cell"]')?.textContent?.trim(),
      identifierValue: row.querySelectorAll('[role="cell"]')[1]?.textContent?.trim(),
    })));
    for (const row of rows) {
      assert(Number.isInteger(row.ordinal) && row.ordinal >= 1 && row.ordinal <= expectedIds.length,
        `Preview row has an invalid ordinal: ${JSON.stringify(row)}`);
      assert.equal(row.id, expectedIds[row.ordinal - 1], `Patient identity differs at preview row ${row.ordinal}`);
      assert.equal(row.identifierValue, identifierDisplay(identifierValues.get(row.id)),
        `Identifier ALL value differs from the independent CDA source at preview row ${row.ordinal}`);
      seen.set(row.ordinal, row.id);
    }
  };
  await inspect();
  await scroll.hover();
  let top = dimensions.top;
  while (top < dimensions.max - 1) {
    await page.mouse.wheel(0, Math.min(dimensions.step, dimensions.max - top));
    await page.waitForFunction(previous => {
      const element = document.querySelector('[data-testid="preview-table-scroll"]');
      return element && element.scrollTop > previous + 0.5;
    }, top);
    top = await scroll.evaluate(element => element.scrollTop);
    await inspect();
  }
  assert.equal(seen.size, expectedIds.length, 'Vertical sweep must inspect every Patient in the preview window');
  if (Math.abs(top - dimensions.top) > 1) {
    await page.mouse.wheel(0, dimensions.top - top);
    await page.waitForFunction(original => {
      const element = document.querySelector('[data-testid="preview-table-scroll"]');
      return element && Math.abs(element.scrollTop - original) <= 1;
    }, dimensions.top);
  }
  return [...seen.entries()].sort(([left], [right]) => left - right).map(([, id]) => id);
};

export const assertRestoredBuilder = async (page, expected) => {
  const explorerControl = page.getByRole('combobox', { name: 'Explorer' });
  const actual = {
    explorer: await explorerControl.inputValue(),
    title: (await explorerControl.locator('option:checked').textContent())?.trim(),
    patientId: await page.getByRole('button', { name: /^Select Patient ID/ }).count(),
    gender: await page.getByRole('button', { name: /^Select Gender/ }).count(),
  };
  assert.deepEqual(actual, { ...expected, patientId: 1, gender: 1 },
    'Published Builder configuration must survive reload on the same Explorer');
  return actual;
};

const assertRestoredCdaBuilder = async (page, expected) => {
  const explorerControl = page.getByRole('combobox', { name: 'Explorer' });
  const actual = {
    explorer: await explorerControl.inputValue(),
    title: (await explorerControl.locator('option:checked').textContent())?.trim(),
    patientId: await page.getByRole('button', { name: /^Select Patient ID/ }).count(),
    identifierValue: await page.getByRole('button', { name: /^Select Identifier Value/ }).count(),
  };
  assert.deepEqual(actual, { ...expected, patientId: 1, identifierValue: 1 },
    'Published CDA Builder configuration must survive reload on the same Explorer');
  return actual;
};


export const builderAuthoringWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const { ids: allIds, sha256: fixtureSHA256 } = patientSource(target);
  const expectedIds = patientWindow(target, allIds);
  const cdaData = allIds.length > 25;
  const identifierValues = cdaData ? sourceIdentifierValues(target, expectedIds) : null;
  if (cdaData) report.requiredChecks.push('CDA identifier ALL preview matches independent raw values including duplicates');
  const addedField = cdaData
    ? { path: 'identifier[].value', label: 'Identifier Value' }
    : { path: 'gender', label: 'Gender' };
  report.target.fixtureOracle = {
    path: join(target.fixtureDir, 'Patient.ndjson'),
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sha256: fixtureSHA256,
    sourcePatientCount: allIds.length,
    previewLimit: 25,
    previewIds: expectedIds,
  };
  if (cdaData) report.target.fixtureOracle.identifierValues = Object.fromEntries(identifierValues);
  const act = (name, locator, perform, options) => action(name, locator, perform, options);
  const title = `Verify ${context.runID.slice(-10)} authoring`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  await waitVisible(page.getByText('New explorer', { exact: true }));
  await act('open Explorer creation', page.getByText('New explorer', { exact: true }),
    () => page.getByText('New explorer', { exact: true }).click());
  await act('name Explorer', page.locator('#new-explorer-name'),
    () => page.locator('#new-explorer-name').fill(title));
  await act('create blank Explorer', page.getByRole('button', { name: 'Create blank' }),
    () => page.getByRole('button', { name: 'Create blank' }).click());
  await page.waitForFunction(expectedTitle => {
    const select = document.querySelector('select[aria-label="Explorer"]');
    return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
  }, title);
  await waitVisible(page.getByText('Build your first table', { exact: true }));
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'Explorer creation must select a fresh Explorer');
  report.target.explorer = explorer;
  await act('name Patient table', page.locator('#first-table-name'),
    () => page.locator('#first-table-name').fill('Patients'));
  const rootStarted = Date.now();
  await act('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows' }),
    () => page.getByRole('button', { name: 'Choose Patient rows' }).click());
  await waitVisible(page.getByTestId('construction-workspace'));
  await waitVisible(page.getByTestId('preview-table-scroll').getByRole('table'));
  await page.waitForFunction(rowCount =>
    document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
  expectedIds.length + 1);
  const rootRenderMs = Date.now() - rootStarted;
  report.timings['choose Patient rows action-to-render'] = rootRenderMs;
  check('performance', 'choose Patient rows action-to-render within budget', rootRenderMs <= 5000,
    { elapsedMs: rootRenderMs, budgetMs: 5000 });
  await act('open Add columns', page.getByRole('button', { name: /Add columns:/ }),
    () => page.getByRole('button', { name: /Add columns:/ }).click());
  await waitVisible(page.locator('[aria-label="Add columns editor"]'));
  await act('choose Fields and related data', page.getByRole('button', { name: 'Fields and related data' }),
    () => page.getByRole('button', { name: 'Fields and related data' }).click());
  await act('open raw FHIR fields', page.getByText('Raw FHIR fields (advanced)', { exact: true }),
    () => page.getByText('Raw FHIR fields (advanced)', { exact: true }).click());
  const fieldCheckbox = page.getByRole('checkbox', { name: `Select Patient.${addedField.path}` });
  await act(`select Patient.${addedField.path}`, fieldCheckbox, () => fieldCheckbox.check());
  await act('add selected feature', page.getByRole('button', { name: 'Add 1 selected feature' }),
    () => page.getByRole('button', { name: 'Add 1 selected feature' }).click());
  if (cdaData) {
    const choiceDialog = page.getByRole('dialog', { name: 'Choose how to add these fields' });
    await waitVisible(choiceDialog);
    const allValues = choiceDialog.getByRole('radio', { name: 'Identifier Value: Keep all matching values' });
    await act('keep all Patient identifier values', allValues, () => allValues.check());
    assert(await allValues.isChecked(), 'Identifier Value must use ALL without deduplication');
    const addColumn = choiceDialog.getByRole('button', { name: 'Add 1 column' });
    await act('confirm Identifier Value column', addColumn, () => addColumn.click());
  }
  const apply = page.getByRole('button', { name: 'Apply columns' });
  await waitVisible(apply);
  const renderStarted = Date.now();
  await act(`apply ${addedField.label} column`, apply, () => apply.click());
  const fieldButton = page.getByRole('button', { name: new RegExp(`^Select ${addedField.label}`) });
  await waitVisible(fieldButton);
  await page.waitForFunction(rowCount =>
    document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
  expectedIds.length + 1);
  const renderMs = Date.now() - renderStarted;
  report.timings[`apply ${addedField.label} column action-to-render`] = renderMs;
  check('performance', `apply ${addedField.label} column action-to-render within budget`, renderMs <= 5000, { elapsedMs: renderMs, budgetMs: 5000 });
  await act('close operation editor', page.getByRole('button', { name: 'Close operation editor' }),
    () => page.getByRole('button', { name: 'Close operation editor' }).click());

  const visibleIds = allIds.length <= 25
    ? await assertPreviewPatientIds(page, expectedIds)
    : await assertPreviewPatientWindow(page, expectedIds, identifierValues);
  check('correctness', 'Preview renders both independent fixture Patients', true,
    { expectedIds, visibleIds });
  if (cdaData) {
    const hasDuplicate = [...identifierValues.values()].some(values =>
    values.filter(value => value !== null).length > new Set(values.filter(value => value !== null)).size);
    check('correctness', 'CDA identifier ALL preview matches independent raw values including duplicates',
    hasDuplicate, { visibleIds, rawIdentifierValues: Object.fromEntries(identifierValues) });
  }

  const publishResponse = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/authoring/v2/publish'),
    { timeout: 5000 });
  const publishStarted = Date.now();
  await act('publish Explorer', page.getByRole('button', { name: 'Publish', exact: true }),
    () => page.getByRole('button', { name: 'Publish', exact: true }).click());
  const published = await publishResponse;
  await page.waitForFunction(() => [...document.querySelectorAll('button')]
    .some(button => button.textContent?.trim() === 'Publish' && button.disabled));
  const publishMs = Date.now() - publishStarted;
  report.timings['publish Explorer action-to-render'] = publishMs;
  const publishWithinBudget = publishMs <= 5000;
  if (!publishWithinBudget) {
    const error = new Error(`Publish rendered after ${publishMs} ms; budget is 5000 ms`);
    report.errors.push({ kind: 'performance', message: error.message });
    report.failureAction = {
    name: 'publish Explorer', locator: page.getByRole('button', { name: 'Publish', exact: true }).toString(),
    elapsedMs: publishMs,
    };
  }
  check('performance', 'publish Explorer action-to-render within budget', publishWithinBudget,
    { elapsedMs: publishMs, budgetMs: 5000 });
  check('correctness', 'publish endpoint returned success', published.ok(), {
    path: new URL(published.url()).pathname, status: published.status(),
  });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await waitVisible(page.getByRole('button', { name: /^Select Patient ID/ }));
  await waitVisible(page.getByRole('button', { name: new RegExp(`^Select ${addedField.label}`) }));
  const restored = cdaData
    ? await assertRestoredCdaBuilder(page, { explorer, title })
    : await assertRestoredBuilder(page, { explorer, title });
  check('persistence', 'published Builder table and configured fields survive reload', true,
    { expected: { explorer, title }, restored });
  const fixtureSHA256After = patientSourceDigest(target);
  check('correctness', 'independent Patient source stayed unchanged during browser run',
    fixtureSHA256After === fixtureSHA256, { before: fixtureSHA256, after: fixtureSHA256After });

  if (!cdaData) {
    const oracle = existingRootChangeOracle(target);
    const apiRoot = `${target.apiUrl.replace(/\/$/, '')}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
      + `/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
    const readBuilder = async (timeoutMs = 5000) => {
      const timeout = Math.max(1, Math.min(5000, Math.floor(timeoutMs)));
      const response = await fetch(`${apiRoot}/builder`, { signal: AbortSignal.timeout(timeout) });
      const value = await response.json();
      assert.equal(response.status, 200, 'Existing-output root-change setup must read the exact Builder successfully');
      return value;
    };
    const seededBuilder = await readBuilder();
    const seededDocument = seededBuilder.workspace?.documents?.find(document => document.output?.id);
    assert.equal(seededDocument?.rootResourceType, 'Patient');
    const patientNode = seededBuilder.catalog?.nodes?.find(node => node.resourceType === 'Patient');
    const observationNode = seededBuilder.catalog?.nodes?.find(node => node.resourceType === 'Observation');
    const subjectEdge = seededBuilder.catalog?.edges?.find(edge =>
      edge.fromNodeId === patientNode?.nodeId && edge.toNodeId === observationNode?.nodeId &&
      edge.label === 'subject_Patient');
    assert(subjectEdge, 'The fixture Builder catalog must expose the direct Patient-to-Observation Subject route');
    const routeResponse = await fetch(`${apiRoot}/commands`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': `authoring-root-route-${randomUUID()}` },
      body: JSON.stringify({
        commandId: randomUUID(),
        semanticsVersion: seededBuilder.workspace?.semanticsVersion ?? 10,
        snapshotToken: seededBuilder.catalog.snapshotToken,
        expectedDraftVersion: seededBuilder.draftVersion,
        expectedDraftDigest: seededBuilder.draftDigest,
        commands: [{ type: 'ADD_ROUTE', outputId: seededDocument.output.id, parentOccurrenceId: 'base', edgeId: subjectEdge.edgeId }],
      }),
      signal: AbortSignal.timeout(5000),
    });
    const routeResult = await routeResponse.json();
    assert.equal(routeResponse.status, 200,
      `Existing-output root-change setup must add only the legal Subject route: ${JSON.stringify(routeResult)}`);
    const beforeRootChange = await readBuilder();
    const originalDocument = beforeRootChange.workspace?.documents?.find(document => document.output?.id === seededDocument.output.id);
    assert.equal(originalDocument?.rootResourceType, 'Patient');
    assert.equal(originalDocument?.output?.id, seededDocument.output.id);
    assert.equal(originalDocument?.route?.children?.[0]?.resourceType, 'Observation',
      'The existing Patient table must expose Observation as a legal starting record type');
    const originalIdentity = {
      draftVersion: beforeRootChange.draftVersion,
      draftDigest: beforeRootChange.draftDigest,
      document: structuredClone(originalDocument),
    };
    report.target.existingRootChangeOracle = oracle;

    const networkObserver = workflow.networkObserver;
    const observerStartIndex = Array.isArray(networkObserver?.requests) ? networkObserver.requests.length : 0;
    const observerAuthoringPath = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
      + `/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
    const rootChangePath = `${observerAuthoringPath}/row-change`;
    const previewPath = `${observerAuthoringPath}/preview`;
    const proposalReceipts = [];
    const proposalRequests = [];
    const sortedRows = rows => [...rows].sort((left, right) =>
      `${left.patientId}\0${left.gender ?? ''}`.localeCompare(`${right.patientId}\0${right.gender ?? ''}`));
    const assertExactGrid = (grid, expectedRows, phase) => {
      const idIndex = previewHeaderIndex(grid.headers, 'Patient ID');
      const genderIndex = previewHeaderIndex(grid.headers, 'Gender');
      const cells = grid.rows.map(row => Array.isArray(row) ? row : row.values);
      const actual = sortedRows(cells.map(row => ({
        patientId: normalizeRootChangePreviewCell(row[idIndex]),
        gender: normalizeRootChangePreviewCell(row[genderIndex]),
      })));
      assert.equal(grid.rowCount ?? cells.length, expectedRows.length, `${phase} row count must match the independent fixture`);
      assert.deepEqual(actual, sortedRows(expectedRows), `${phase} must render exact independent Patient ID/Gender values`);
      return actual;
    };
    const expectedPatientRows = oracle.patientRows;
    const expectedObservationRows = oracle.rows.map(({ patientId, gender }) => ({ patientId, gender }));
    const reloadAndVerify = async (rootResourceType, expectedRows, name, expectedDraftDigest) => {
      let result;
      const expectedAriaRows = JSON.stringify(String(expectedRows.length + 1));
      await recordPlaywrightTiming(report, page, workflow, {
        name,
        action: () => page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 }),
        after: `(()=>{const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');return Boolean(table&&table.getAttribute('aria-rowcount')===${expectedAriaRows})})()`,
        settle: async ({ remainingMs }) => {
          const timeoutMs = () => Math.max(1, Math.floor(remainingMs()));
          await waitVisible(page.getByRole('button', { name: /^Select Patient ID/ }), timeoutMs());
          await waitVisible(page.getByRole('button', { name: /^Select Gender/ }), timeoutMs());
          const builder = await readBuilder(timeoutMs());
          const document = builder.workspace?.documents?.find(item => item.output?.id === originalIdentity.document.output.id);
          assert.equal(document?.rootResourceType, rootResourceType,
            `${name} must restore the exact ${rootResourceType} root`);
          assert.equal(document?.output?.id, originalIdentity.document.output.id,
            `${name} must restore the same output identity`);
          if (expectedDraftDigest) assert.equal(builder.draftDigest, expectedDraftDigest,
            `${name} must restore the saved draft digest`);
          const grid = await collectPreviewRows(page, { timeout: timeoutMs() });
          const rows = assertExactGrid(grid, expectedRows, name);
          result = { builder, document, rows };
        },
        timeout: 5000,
        budget: 5000,
        dimension: 'persistence',
      });
      return result;
    };
    const readProposalGrid = async phase => {
      const panel = page.getByRole('region', { name: 'Review proposed rows' });
      const preview = panel.getByTestId('construction-proposal-preview');
      await expect(preview).toHaveAttribute('data-preview-status', 'ready', { timeout: 5000 });
      const receiptId = await preview.getAttribute('data-preview-receipt-id');
      const outputId = await preview.getAttribute('data-preview-output-id');
      assert(receiptId, `${phase} must expose its native candidate preview receipt`);
      assert.equal(outputId, originalIdentity.document.output.id, `${phase} preview must retain the existing output identity`);
      const grid = await preview.evaluate(element => {
        const table = element.querySelector('table');
        const headers = [...(table?.querySelectorAll('thead th') ?? [])]
          .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
        const rows = [...(table?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
          .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
        return { headers, rowCount: rows.length, rows };
      });
      return { receiptId, outputId, grid, rows: assertExactGrid(grid, phase.expectedRows, phase.name) };
    };
    const captureProposalRequestDiagnostics = ({ fromIndex, receiptId }) => {
      const entries = Array.isArray(networkObserver?.requests)
        ? networkObserver.requests.slice(fromIndex, fromIndex + 32) : [];
      const exactOutput = entry => entry.origin === new URL(target.uiUrl).origin && entry.method === 'POST' &&
        entry.request?.outputId === originalIdentity.document.output.id;
      const rowChange = entries.find(entry => exactOutput(entry) && entry.path === rootChangePath);
      const preview = entries.find(entry => exactOutput(entry) && entry.path === previewPath &&
        entry.request?.receiptId === receiptId);
      const summarize = entry => entry ? {
        index: entry.index,
        browserRequestId: entry.browserRequestId ?? null,
        status: entry.status ?? null,
        networkComplete: Boolean(entry.networkComplete),
        responseCapture: entry.responseCapture ?? 'unavailable',
      } : null;
      return { rowChange: summarize(rowChange), preview: summarize(preview) };
    };
    const proposeRoot = async ({ selector, choice, rootResourceType, expectedRows, label }) => {
      const panel = page.getByRole('region', { name: 'Review proposed rows' });
      const applyButton = panel.getByRole('button', { name: 'Apply row change', exact: true });
      const fromIndex = Array.isArray(networkObserver?.requests) ? networkObserver.requests.length : 0;
      let candidate;
      await act(label, selector, () => selector.selectOption(choice.value), {
        after: async () => {
          await expect(panel).toBeVisible({ timeout: 5000 });
          await expect(applyButton).toBeEnabled({ timeout: 5000 });
          await expect(panel.getByTestId('construction-proposal-preview'))
            .toHaveAttribute('data-preview-status', 'ready', { timeout: 5000 });
          candidate = await readProposalGrid({ name: label, expectedRows });
          const heading = (await panel.getByRole('heading').innerText()).trim();
          assert.equal(heading, `One row per ${rootResourceType}`, `${label} must show the exact proposed root`);
        },
      });
      proposalReceipts.push({ rootResourceType, receiptId: candidate.receiptId, outputId: candidate.outputId });
      proposalRequests.push({ rootResourceType, receiptId: candidate.receiptId,
        ...captureProposalRequestDiagnostics({ fromIndex, receiptId: candidate.receiptId }) });
      return candidate;
    };
    const openRootSettings = async label => {
      await act(label, page.getByTestId('construction-rows-settings-trigger'),
        () => page.getByTestId('construction-rows-settings-trigger').click(), {
          after: () => page.getByRole('dialog', { name: 'Row definition settings' })
            .waitFor({ state: 'visible', timeout: 5000 }),
        });
      const dialog = page.getByRole('dialog', { name: 'Row definition settings' });
      const section = dialog.getByRole('region', { name: 'Starting record type' });
      const selector = section.getByRole('combobox', { name: 'Choose starting record type', exact: true });
      return { dialog, section, selector };
    };
    const initialTable = await reloadAndVerify('Patient', expectedPatientRows,
      'reload existing Patient table with starting record choices');
    assert.deepEqual(initialTable.rows, sortedRows(expectedPatientRows));
    const { dialog: settings, section: startingType, selector: rootSelector } =
      await openRootSettings('open existing table row settings');
    await expect(settings.getByText('One row per Patient record.', { exact: true })).toBeVisible();
    await expect(startingType).toBeVisible();
    await expect(rootSelector).toHaveValue('base');
    const observationChoice = await rootOption(rootSelector, 'Observation');
    assert(observationChoice && !observationChoice.disabled && /subject_Patient$/.test(observationChoice.label),
      `The native selector must expose the exact legal Observation Subject route: ${JSON.stringify(observationChoice)}`);
    const controlPlacement = await settings.evaluate(dialog => {
      const summary = [...dialog.querySelectorAll('p')].find(item => item.textContent?.trim() === 'One row per Patient record.');
      const startingRecord = dialog.querySelector('section[aria-label="Starting record type"]');
      const rowActions = dialog.querySelector('section[aria-label="Choose a row change"]');
      return Boolean(summary && startingRecord && rowActions &&
        (summary.compareDocumentPosition(startingRecord) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0 &&
        (startingRecord.compareDocumentPosition(rowActions) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0);
    });
    assert.equal(controlPlacement, true,
      'Starting record type must appear beside the current row summary and before row-shape actions');
    check('correctness', 'existing Patient table exposes the starting record type control beside its row summary', true, {
      summary: 'One row per Patient record.', selectorValue: await rootSelector.inputValue(),
      section: 'Starting record type', option: observationChoice.label, placement: controlPlacement,
    });

    const canceledProposal = await proposeRoot({ selector: rootSelector, choice: observationChoice,
      rootResourceType: 'Observation', expectedRows: expectedObservationRows,
      label: 'select Observation as the existing table starting record type' });
    await act('cancel existing table root change', page.getByRole('button', { name: 'Keep current rows', exact: true }),
      () => page.getByRole('button', { name: 'Keep current rows', exact: true }).click(), {
        after: () => page.getByRole('region', { name: 'Review proposed rows' }).waitFor({ state: 'hidden', timeout: 5000 }),
      });
    const afterCancel = await readBuilder();
    const canceledDocument = afterCancel.workspace?.documents?.find(document => document.output?.id === originalIdentity.document.output.id);
    assert.equal(afterCancel.draftVersion, originalIdentity.draftVersion);
    assert.equal(afterCancel.draftDigest, originalIdentity.draftDigest);
    assert.deepEqual(canceledDocument, originalIdentity.document,
      'Cancel must preserve the exact existing table and draft after the native preview');
    assertExactGrid(await collectPreviewRows(page), expectedPatientRows, 'Cancel restoring original Patient rows');
    check('correctness', 'root-change Cancel preserves the original Patient table, output identity, and exact Builder draft', true, {
      draftVersion: afterCancel.draftVersion, draftDigest: afterCancel.draftDigest,
      outputId: canceledDocument.output.id, rootResourceType: canceledDocument.rootResourceType,
      candidateReceiptId: canceledProposal.receiptId,
    });

    await openRootSettings('reopen existing table root selector');
    const applySettings = page.getByRole('dialog', { name: 'Row definition settings' });
    const applySelector = applySettings.getByRole('region', { name: 'Starting record type' })
      .getByRole('combobox', { name: 'Choose starting record type', exact: true });
    const applyObservationChoice = await rootOption(applySelector, 'Observation');
    assert(applyObservationChoice && !applyObservationChoice.disabled && applyObservationChoice.value === observationChoice.value,
      'Reopening the selector must preserve the same legal Observation option');
    const appliedProposal = await proposeRoot({ selector: applySelector, choice: applyObservationChoice,
      rootResourceType: 'Observation', expectedRows: expectedObservationRows,
      label: 'select Observation again for existing table Apply' });
    assert.deepEqual(appliedProposal.rows, canceledProposal.rows,
      'A fresh native proposal must match the exact preview checked before Cancel');
    const applyButton = page.getByRole('region', { name: 'Review proposed rows' })
      .getByRole('button', { name: 'Apply row change', exact: true });
    let appliedGrid;
    await act('apply Observation starting record type to existing table', applyButton,
      () => applyButton.click(), {
        after: async () => {
          await page.getByRole('region', { name: 'Review proposed rows' })
            .waitFor({ state: 'hidden', timeout: 5000 });
          const grid = await collectPreviewRows(page, { timeout: 5000 });
          appliedGrid = assertExactGrid(grid, expectedObservationRows, 'Apply to Observation table');
        },
      });
    const appliedBuilder = await readBuilder();
    const appliedDocument = appliedBuilder.workspace?.documents?.find(document => document.output?.id === originalIdentity.document.output.id);
    assert.equal(appliedDocument?.rootResourceType, 'Observation');
    assert.equal(appliedDocument?.output?.id, originalIdentity.document.output.id,
      'Apply must preserve the existing table output identity');
    assert.deepEqual(appliedDocument.columns.map(column => column.columnId).sort(),
      originalIdentity.document.columns.map(column => column.columnId).sort(),
      'Apply must preserve the existing table column identities');
    check('correctness', 'Apply saves the existing output as Observation rows with exact independent preview values', true, {
      outputId: appliedDocument.output.id, rootResourceType: appliedDocument.rootResourceType,
      rows: appliedGrid, candidateReceiptId: appliedProposal.receiptId,
    });

    const restoredObservation = await reloadAndVerify('Observation', expectedObservationRows,
      'reload restores the Observation root and exact existing-output rows', appliedBuilder.draftDigest);
    check('persistence', 'Observation root, output identity, and exact rows survive reload', true, {
      outputId: restoredObservation.document.output.id, rootResourceType: restoredObservation.document.rootResourceType,
      draftDigest: restoredObservation.builder.draftDigest, rows: restoredObservation.rows,
    });

    const { dialog: observationSettings, selector: observationSelector } =
      await openRootSettings('open Observation table root settings for native restoration');
    await expect(observationSettings.getByText('One row per Observation record.', { exact: true })).toBeVisible();
    await expect(observationSelector).toHaveValue('base');
    const patientChoice = await rootOption(observationSelector, 'Patient');
    assert(patientChoice && !patientChoice.disabled && /subject_Patient$/.test(patientChoice.label),
      `The saved Observation table must expose Patient as a legal return root: ${JSON.stringify(patientChoice)}`);
    const restoredProposal = await proposeRoot({ selector: observationSelector, choice: patientChoice,
      rootResourceType: 'Patient', expectedRows: expectedPatientRows,
      label: 'select Patient to restore the existing table root' });
    const restoreButton = page.getByRole('region', { name: 'Review proposed rows' })
      .getByRole('button', { name: 'Apply row change', exact: true });
    let restoredGrid;
    await act('restore Patient starting record type on existing table', restoreButton,
      () => restoreButton.click(), {
        after: async () => {
          await page.getByRole('region', { name: 'Review proposed rows' })
            .waitFor({ state: 'hidden', timeout: 5000 });
          const grid = await collectPreviewRows(page, { timeout: 5000 });
          restoredGrid = assertExactGrid(grid, expectedPatientRows, 'Restore Patient table');
        },
      });
    const restoredBuilder = await readBuilder();
    const restoredDocument = restoredBuilder.workspace?.documents?.find(document => document.output?.id === originalIdentity.document.output.id);
    assert.equal(restoredDocument?.rootResourceType, 'Patient');
    assert.equal(restoredDocument?.output?.id, originalIdentity.document.output.id);
    assert.deepEqual(restoredDocument.columns, originalIdentity.document.columns,
      'Returning to Patient must preserve the original table column bindings and identities');

    const finalPatient = await reloadAndVerify('Patient', expectedPatientRows,
      'reload restores the original Patient root and exact rows', restoredBuilder.draftDigest);
    check('correctness', 'native restoration returns the existing output to Patient rows and survives reload', true, {
      outputId: finalPatient.document.output.id, rootResourceType: finalPatient.document.rootResourceType,
      draftDigest: finalPatient.builder.draftDigest, rows: finalPatient.rows,
      restorationReceiptId: restoredProposal.receiptId,
    });

    report.target.existingRootChangeRequestDiagnostics = {
      scope: { project: target.fixtureProject, explorer, origin: new URL(target.uiUrl).origin,
        fromRequestIndex: observerStartIndex },
      proposals: proposalRequests,
    };
    check('correctness', 'existing Patient table selects a legal Observation root and renders exact independent row preview', true, {
      observationRows: canceledProposal.rows,
      candidateReceipts: proposalReceipts.filter(item => item.rootResourceType === 'Observation'),
      nativeRequestDiagnostics: proposalRequests,
    });
    const finalOracle = existingRootChangeOracle(target);
    check('correctness', 'independent Patient and Observation fixture sources stay unchanged during browser run',
      finalOracle.patient.sha256 === oracle.patient.sha256 && finalOracle.observation.sha256 === oracle.observation.sha256,
      { before: { patient: oracle.patient.sha256, observation: oracle.observation.sha256 },
        after: { patient: finalOracle.patient.sha256, observation: finalOracle.observation.sha256 } });
  }
};
