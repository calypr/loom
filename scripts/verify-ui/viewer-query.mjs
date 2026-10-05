import assert from 'node:assert/strict';
import { expect } from '@playwright/test';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import { sanitizePayload, sanitizeText } from '../lib/playwright-browser.mjs';
import { recordCheck, recordUntested } from './report.mjs';

const graphPath = '/graphql/graph';
const explorerRuntimePath = (project, explorer) =>
  `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}`;

const fixturePatientOracle = target => {
  const path = join(target.fixtureDir, 'Patient.ndjson');
  const bytes = readFileSync(path);
  const patients = bytes.toString('utf8').split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line));
  const ids = patients.map(patient => patient.id).sort();
  assert.deepEqual(ids, ['dev-patient-001', 'dev-patient-002'], 'Viewer fixture oracle must contain exactly the two independent Patient identities');
  return {
    path,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    ids,
    femaleIDs: patients.filter(patient => patient.gender === 'female').map(patient => patient.id).sort(),
  };
};

export const assertViewerPatientRows = (rows, expectedIDs) => {
  const actualIDs = rows.map(text => expectedIDs.find(id => text.includes(id)) ?? `UNEXPECTED:${text}`).sort();
  assert.deepEqual(actualIDs, [...expectedIDs].sort(), 'Viewer rows must contain the exact independent fixture Patient identities');
  return actualIDs;
};

export const matchesViewerRequestBody = (body, { project, selector }) => {
  const input = body?.variables?.input;
  return input?.projectId === project && input.selector?.recipe === selector?.recipe &&
    input.selector?.translationVersion === selector?.translationVersion &&
    input.selector?.output === selector?.output;
};

const captureViewerFailure = async (page, report, testInfo, error, details = {}) => {
  const { action, ...safeDetails } = details;
  const location = new URL(page.url());
  const dom = await page.locator('body').innerText().catch(() => '');
  const evidence = sanitizePayload({
    message: sanitizeText(error?.message ?? error),
    stack: sanitizeText(error?.stack ?? ''),
    url: `${location.origin}${location.pathname}`,
    ...safeDetails,
    ...(action ? { action: { label: action.label, locator: action.locator } } : {}),
    dom: sanitizeText(dom).slice(0, 8000),
  });
  report.failureTrace ??= evidence;
  await testInfo?.attach('viewer-query-first-failure.json', {
    body: JSON.stringify(evidence, null, 2),
    contentType: 'application/json',
  }).catch(() => undefined);
};

const requestBody = request => {
  try { return request.postDataJSON(); }
  catch { return null; }
};

const sameSelector = (left, right) => Boolean(left && right && left.recipe === right.recipe &&
  left.translationVersion === right.translationVersion && left.output === right.output);

const checkViewerScope = (report, { target, explorer, runtime, output, request }) => {
  const body = requestBody(request);
  const project = target.fixtureProject;
  const selectorOwned = runtime.outputs.some(candidate => sameSelector(candidate.selector, body?.variables?.input?.selector));
  const scope = {
    requestProject: body?.variables?.input?.projectId ?? null,
    expectedProject: project,
    outputId: output.outputId,
    explorer,
    generation: runtime.generation ?? runtime.publication?.generation ?? null,
    expectedGeneration: target.fixtureGeneration ?? null,
    selectorOwned,
  };
  const projectMatches = body?.variables?.input?.projectId === project;
  const generationMatches = target.fixtureGeneration === null || target.fixtureGeneration === undefined ||
    scope.generation === target.fixtureGeneration;
  const passed = projectMatches && selectorOwned && generationMatches;
  recordCheck(report, 'correctness', 'Viewer query retains the exact project, generation, Explorer output, and published selector scope', passed, scope);
  assert(passed, 'Viewer GraphQL request did not match the selected project, generation, and published Explorer selector');
  report.target.viewerQueryOwnership = {
    project,
    explorer,
    generation: scope.generation,
    outputId: output.outputId,
    selector: output.selector,
  };
  return body.variables.input.selector;
};

export const viewerQueryWorkflow = async ({ page, report, action, fault, check }, context, testInfo) => {
  const target = context.target;
  const project = target.fixtureProject;
  let explorer = target.bootstrapExplorerId;
  let expectedIDs;
  let sourceOracle;
  let sourceBytesBefore;
  try {
  if (!context.custom) {
    sourceOracle = fixturePatientOracle(target);
    expectedIDs = sourceOracle.ids;
    sourceBytesBefore = sourceOracle.sha256;
    assert.equal(sourceOracle.femaleIDs.length, 1, 'Viewer fixture oracle must identify exactly one female Patient for the optional facet contract');
    report.target.fixtureOracle = {
      path: sourceOracle.path,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
      sha256: sourceOracle.sha256,
      patientIDs: expectedIDs,
      femaleIDs: sourceOracle.femaleIDs,
    };

    await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
    const newExplorer = page.getByText('New explorer', { exact: true });
    await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
      after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
    });
    const title = `Verify ${context.runID.slice(-10)} viewer`;
    const explorerName = page.locator('#new-explorer-name');
    await action('name Viewer Explorer', explorerName, () => explorerName.fill(title), { editable: true });
    const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
    await action('create blank Viewer Explorer', createBlank, () => createBlank.click(), {
      after: async () => {
        const picker = page.getByRole('combobox', { name: 'Explorer' });
        await picker.waitFor({ state: 'visible' });
        await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected, title);
      },
    });
    explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
    assert(explorer && explorer !== target.bootstrapExplorerId, 'Viewer setup must create a fresh Explorer');
    report.target.explorer = explorer;

    const tableName = page.locator('#first-table-name');
    await action('name Viewer Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
    const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
    await action('create Viewer Patient table and render Preview', choosePatients, () => choosePatients.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.getByTestId('construction-workspace').waitFor({ state: 'visible', timeout: 5000 });
        await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), expectedIDs.length + 1, { timeout: 5000 });
      },
    });
    await assertViewerPatientRows(await page.getByTestId('preview-table-scroll').locator('tbody tr').allInnerTexts(), expectedIDs);

    const addColumns = page.getByRole('button', { name: /Add columns:/ });
    await action('open Add columns for Viewer table', addColumns, () => addColumns.click(), {
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
      after: async () => {
        await page.getByRole('button', { name: 'Apply columns', exact: true }).waitFor({ state: 'visible' });
      },
    });
    const applyColumns = page.getByRole('button', { name: 'Apply columns', exact: true });
    await action('apply Gender and render Viewer source Preview', applyColumns, () => applyColumns.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await page.getByRole('button', { name: /^Select Gender/ }).waitFor({ state: 'visible', timeout: 5000 });
        await page.waitForFunction(rowCount => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount), expectedIDs.length + 1, { timeout: 5000 });
      },
    });
    await assertViewerPatientRows(await page.getByTestId('preview-table-scroll').locator('tbody tr').allInnerTexts(), expectedIDs);
    const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
    await action('close Viewer operation editor', closeEditor, () => closeEditor.click(), {
      after: () => page.getByTestId('construction-workspace').waitFor({ state: 'visible' }),
    });

    const publishPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/publish`;
    const publishResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
      new URL(response.url()).origin === new URL(target.uiUrl).origin && new URL(response.url()).pathname === publishPath, { timeout: 5000 });
    const publish = page.getByRole('button', { name: 'Publish', exact: true });
    await action('publish Viewer Explorer', publish, () => publish.click(), {
      timeout: 5000,
      budget: 5000,
      after: async () => {
        await Promise.all([
          publishResponse,
          page.getByRole('button', { name: 'Publish', exact: true }).waitFor({ state: 'disabled', timeout: 5000 }),
        ]);
      },
    });
    const published = await publishResponse;
    check('correctness', 'Viewer setup Publish returned success', published.status() >= 200 && published.status() < 300,
      { path: new URL(published.url()).pathname, status: published.status(), explorer });
  }

  report.target.project = project;
  report.target.explorer = explorer;
  const runtimePath = explorerRuntimePath(project, explorer);
  const uiOrigin = new URL(target.uiUrl).origin;
  const runtimeResponsePromise = page.waitForResponse(response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === uiOrigin && new URL(response.url()).pathname === runtimePath, { timeout: 5000 });
  const initialQueryPromise = page.waitForRequest(request => {
    const url = new URL(request.url());
    return request.method() === 'POST' && url.origin === uiOrigin && url.pathname === graphPath &&
      requestBody(request)?.variables?.input?.projectId === project;
  }, { timeout: 5000 });
  const viewerURL = browserURL(target, project, explorer, 'viewer');
  await page.goto(viewerURL, { waitUntil: 'domcontentloaded' });
  const runtimeResponse = await runtimeResponsePromise;
  assert(runtimeResponse.ok(), `Viewer runtime GET failed with HTTP ${runtimeResponse.status()}`);
  const runtimeEnvelope = await runtimeResponse.json();
  const runtime = runtimeEnvelope.runtime;
  assert(runtime && Array.isArray(runtime.outputs) && runtime.outputs.length > 0, 'Viewer runtime must expose its published outputs');
  if (!context.custom) {
    const generation = runtime.generation ?? runtime.publication?.generation;
    check('correctness', 'published Viewer runtime retains the fixture generation', generation === target.fixtureGeneration,
      { generation, expectedGeneration: target.fixtureGeneration, project, explorer });
  }
  const initialQuery = await initialQueryPromise;
  const initialBody = requestBody(initialQuery);
  const output = runtime.outputs.find(candidate => matchesViewerRequestBody({ variables: { input: initialBody?.variables?.input } }, {
    project,
    selector: candidate.selector,
  }));
  assert(output, 'Initial Viewer query must use a selector from this exact published Explorer runtime');
  report.target.viewerOutputId = output.outputId;
  const outputSelector = output.selector;
  const outputTable = page.locator('table[aria-label$=" results"]');
  await outputTable.waitFor({ state: 'visible', timeout: 5000 });
  const initialRows = await outputTable.locator('tbody tr').allInnerTexts();
  if (context.custom) {
    check('correctness', 'custom Viewer runtime and output table load before fault injection', true,
      { outputId: output.outputId, rowCount: initialRows.length, explorer });
  } else {
    assertViewerPatientRows(initialRows, expectedIDs);
  }
  checkViewerScope(report, { target, explorer, runtime, output, request: initialQuery });

  const injection = await fault({
    method: 'POST',
    path: graphPath,
    matchesRequest: body => matchesViewerRequestBody(body, { project, selector: outputSelector }),
  });
  const retryURL = new URL(viewerURL);
  retryURL.searchParams.set('verify_run', context.runID);
  await page.goto(retryURL.toString(), { waitUntil: 'domcontentloaded' });
  const queryError = page.getByRole('alert').filter({ hasText: 'Results could not be loaded.' });
  try {
    await queryError.waitFor({ state: 'visible', timeout: 5000 });
  } catch (error) {
    await captureViewerFailure(page, report, testInfo, error, {
      action: { label: 'wait for Viewer query error', locator: queryError.toString(), targetLocator: queryError },
      phase: 'injected-query-failure',
      project,
      explorer,
      outputId: output.outputId,
      selector: outputSelector,
      injectedRequestCount: injection.count(),
    });
    throw error;
  }
  recordCheck(report, 'correctness', 'injected result-query error is visibly reported', true,
    { alert: await queryError.innerText(), project, explorer, outputId: output.outputId });
  check('correctness', 'specific owned GraphQL result read fault was injected exactly once', injection.count() === 1,
    { origin: uiOrigin, path: graphPath, method: 'POST', project, explorer, outputId: output.outputId, count: injection.count() });

  const retry = page.getByRole('button', { name: /^(Try again|Retry)$/ });
  try {
    await expect(retry).toHaveCount(1);
    await expect(retry).toBeVisible();
    await expect(retry).toBeEnabled();
  } catch (error) {
    await captureViewerFailure(page, report, testInfo, error, {
      action: { label: 'inspect Viewer Retry', locator: retry.toString(), targetLocator: retry },
      phase: 'retry-unavailable', project, explorer, outputId: output.outputId, selector: outputSelector,
    });
    throw error;
  }
  check('usability', 'result-query error exposes an actionable Retry control', true,
    { count: 1, visible: true, enabled: true });

  const retryResponse = page.waitForResponse(response => {
    const url = new URL(response.url());
    return response.request().method() === 'POST' && url.origin === uiOrigin && url.pathname === graphPath &&
      matchesViewerRequestBody(requestBody(response.request()), { project, selector: outputSelector });
  }, { timeout: 5000 });
  await action('retry result query', retry, () => retry.click(), {
    timeout: 5000,
    budget: 5000,
    after: async () => {
      await Promise.all([
        retryResponse,
        outputTable.waitFor({ state: 'visible', timeout: 5000 }),
        queryError.waitFor({ state: 'hidden', timeout: 5000 }),
      ]);
    },
  });
  const recoveredResponse = await retryResponse;
  check('correctness', 'retried Viewer query returned success', recoveredResponse.status() >= 200 && recoveredResponse.status() < 300,
    { status: recoveredResponse.status(), origin: new URL(recoveredResponse.url()).origin, path: new URL(recoveredResponse.url()).pathname, project, explorer, outputId: output.outputId });
  const restoredRows = await outputTable.locator('tbody tr').allInnerTexts();
  if (context.custom) {
    check('correctness', 'Retry restored the custom output table', await outputTable.isVisible(), { rowCount: restoredRows.length });
    recordUntested(report, 'correctness', 'fixture-specific Patient row identities', 'Custom target rows are unknown and remain unasserted.');
    recordUntested(report, 'usability', 'custom target filter semantics', 'The custom Explorer schema is unknown; only its output query recovery is checked.');
    return;
  }
  const actualIDs = assertViewerPatientRows(restoredRows, expectedIDs);
  check('correctness', 'retried Viewer results contain both independent fixture Patients', actualIDs.length === expectedIDs.length,
    { rows: restoredRows, patientIDs: actualIDs, expectedPatientIDs: expectedIDs, sourceSHA256: sourceOracle.sha256 });

  const loadValues = page.getByRole('button', { name: 'Load values', exact: true });
  await expect(loadValues).toHaveCount(1);
  await expect(loadValues).toBeVisible();
  await expect(loadValues).toBeEnabled();
  recordCheck(report, 'usability', 'Gender facet can be opened to load values', true, { count: 1 });
  await action('load Gender facet values', loadValues, () => loadValues.click(), {
    after: () => page.getByRole('checkbox', { name: /female/i }).waitFor({ state: 'visible' }),
  });
  const female = page.getByRole('checkbox', { name: /female/i });
  await expect(female).toHaveCount(1);
  await expect(female).toBeVisible();
  await expect(female).toBeEnabled();
  recordCheck(report, 'usability', 'female facet value is visible and actionable', true, { count: 1 });
  await action('apply female filter', female, () => female.check(), {
    after: async () => page.waitForFunction(expectedID => {
      const rows = [...document.querySelectorAll('table[aria-label$=" results"] tbody tr')];
      return rows.length === 1 && rows[0].innerText.includes(expectedID);
    }, sourceOracle.femaleIDs[0], { timeout: 5000 }),
  });
  const filteredRows = await outputTable.locator('tbody tr').allInnerTexts();
  const filteredIDs = assertViewerPatientRows(filteredRows, sourceOracle.femaleIDs);
  recordCheck(report, 'correctness', 'Gender facet returns only the independently identified female fixture Patient',
    filteredIDs.length === 1, { rows: filteredRows, patientIDs: filteredIDs, expectedPatientIDs: sourceOracle.femaleIDs });
  const currentMode = new URL(page.url()).searchParams.get('mode');
  recordCheck(report, 'correctness', 'Viewer route remains represented in the URL', currentMode === 'viewer', { mode: currentMode });
  recordUntested(report, 'persistence', 'Gender filter selection survives reload',
    'This Viewer workflow does not claim that filter selection persists across a full page reload.');

  } finally {
    if (sourceBytesBefore) {
      try {
        const sourceAfter = createHash('sha256').update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');
        recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during Viewer verification', sourceAfter === sourceBytesBefore,
          { before: sourceBytesBefore, after: sourceAfter });
      } catch (error) {
        recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during Viewer verification', false,
          { before: sourceBytesBefore, error: error.message });
      }
    }
  }
};
