import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp, localCDAApiContainer } from './lib/api-build-freeze.mjs';

const project = 'loom_dev_cda_fhir';
const expectedGeneration = 'cda-fhir-v1';
const resourceType = 'Observation';
const explorer = `root-quantity-category-${Date.now()}`;
const evidence = process.argv[2] ?? `/tmp/loom-root-quantity-category-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const root = `/api/v1/projects/${project}/explorers`;
const base = `${root}/${explorer}/authoring/v2`;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1';
const sourceRoot = fileURLToPath(new URL('..', import.meta.url));
const report = { project, expectedGeneration, resourceType, explorer, cases: [], requests: [], authoringRequests: [], browserErrors: { runtime: [], console: [], network: [] }, started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
const sourceFreeze = await captureSourceFreeze(sourceRoot);
let apiBuildFreeze;
let browser;
let builder;
let outputId;
const pendingResponseReads = new Set();
const networkRequests = new Map();
const parseJSON = value => { try { return JSON.parse(value); } catch { return value; } };
const drainResponseReads = async () => { while (pendingResponseReads.size) await Promise.all([...pendingResponseReads]); };

const api = async (path, body) => {
  const response = await fetch(apiOrigin + path, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': `root-quantity-category-${randomUUID()}` },
    ...(body ? { body: JSON.stringify(body) } : {}),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, body, status: response.status, response: value });
  assert(response.ok, JSON.stringify(value));
  return value;
};

const runRawCategoryOracle = generation => {
  const query = `FOR o IN Observation FILTER o.project == ${JSON.stringify(project)} AND o.dataset_generation == ${JSON.stringify(generation)} LET quantity = o.payload.valueQuantity LET present = IS_OBJECT(quantity) ? HAS(quantity, "code") : false LET value = o.payload.valueQuantity.code COLLECT categoryPresent = present, categoryValue = value WITH COUNT INTO rowCount SORT categoryPresent ASC, TYPENAME(categoryValue), categoryValue RETURN {present: categoryPresent, value: categoryValue, rowCount}`;
  const program = `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', arangoContainer,
    'arangosh', '--server.database', process.env.LOOM_ARANGO_DATABASE ?? 'loom_dev',
    '--javascript.execute-string', program,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const payloadLine = result.stdout.split(/\r?\n/).findLast(line => line.trimStart().startsWith('['));
  assert(payloadLine, `Raw root category query returned no JSON array: ${result.stdout.slice(-1000)}`);
  const categories = JSON.parse(payloadLine);
  assert(categories.length > 0, 'The scoped Observation collection must have category values');
  for (const category of categories) {
    assert.equal(typeof category.present, 'boolean', `Raw category presence must be boolean: ${JSON.stringify(category)}`);
    if (!category.present) {
      assert.equal(category.value, null, `An absent code path must project to AQL null while retaining present=false: ${JSON.stringify(category)}`);
    } else {
      assert(category.value === null || typeof category.value === 'string', `Unexpected present code category type: ${JSON.stringify(category)}`);
    }
    assert(Number.isInteger(category.rowCount) && category.rowCount > 0, `Raw category row count must be positive: ${JSON.stringify(category)}`);
  }
  const missingRows = categories.filter(category => !category.present).reduce((sum, category) => sum + category.rowCount, 0);
  const explicitNullRows = categories.filter(category => category.present && category.value === null).reduce((sum, category) => sum + category.rowCount, 0);
  const presentStringRows = categories.filter(category => category.present && typeof category.value === 'string').reduce((sum, category) => sum + category.rowCount, 0);
  return {
    query,
    scope: { project, generation, collection: 'Observation', authorization: 'local Compose no-auth / unrestricted' },
    sourceRows: categories.reduce((sum, category) => sum + category.rowCount, 0),
    missingRows,
    explicitNullRows,
    presentStringRows,
    categories,
    categoryIdentities: categories.map(category => !category.present
      ? 'MISSING'
      : category.value === null
        ? 'NULL'
        : JSON.stringify({ kind: 'STRING', string: category.value })).sort(),
  };
};

const command = async commands => {
  await api(base + '/commands', {
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await api(base + '/builder');
};

const selectPivotSource = async (label, path) => {
  const selector = `select[aria-label=${JSON.stringify(label)}]`;
  const options = await browserEval(browser.cdp, `return [...document.querySelector(${JSON.stringify(selector)}).options].map(option=>({label:option.textContent,value:option.value}));`);
  const matches = options.filter(option => option.value.startsWith('source:') && option.label.includes(path));
  assert.equal(matches.length, 1, `Expected one exact root source option for ${path}: ${JSON.stringify(options)}`);
  const settledWhen = label === 'Add pivot group field'
    ? `Boolean(document.querySelector(${JSON.stringify(`input[aria-label="Pivot group ${path}"]`)} )?.checked)`
    : `document.querySelector(${JSON.stringify(selector)})?.selectedOptions[0]?.textContent.includes(${JSON.stringify(path)})`;
  await selectOption(browser.cdp, selector, matches[0].value, { settledWhen });
  return matches[0].value;
};

try {
  apiBuildFreeze = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(localCDAApiContainer()));
  assert.equal(new URL(apiOrigin).hostname, '127.0.0.1', 'This independent oracle is only valid for local Compose no-auth');
  assert.equal(new URL(apiOrigin).port, '8188', 'This independent oracle is only valid for local Compose no-auth');
  await api(root, { name: explorer, title: 'Root quantity category QA' });
  builder = await api(base + '/builder');
  assert.equal(builder.catalog.generation, expectedGeneration);
  const oracle = runRawCategoryOracle(builder.catalog.generation);
  report.oracle = oracle;

  const rootNode = builder.catalog.nodes.find(node => node.resourceType === resourceType);
  const idField = builder.catalog.candidates.find(candidate => candidate.nodeId === rootNode?.nodeId && candidate.fieldPath === 'id');
  assert(rootNode && idField, 'The current catalog must expose the Observation root and its ID field');
  await command([{ type: 'CREATE_TABLE', title: 'Root Observation quantity QA', rootNodeId: rootNode.nodeId }]);
  outputId = builder.workspace.documents[0]?.output.id;
  assert(outputId);
  await command([{
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idField.candidateId,
    projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID',
  }]);

  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
    report.browserErrors.runtime.push({ text: exceptionDetails.text, message: exceptionDetails.exception?.description });
  });
  browser.cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (type === 'error') report.browserErrors.console.push(args.map(argument => argument.value ?? argument.description ?? '').join(' '));
  });
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, timestamp }) => {
    const pathname = new URL(request.url).pathname;
    const endpoint = ['construction-capabilities', 'construction-category-discoveries', 'construction-proposals', 'reconcile', 'preview']
      .find(candidate => pathname.endsWith(`/${candidate}`));
    const captured = { pathname, method: request.method, requestId, requestStartedAtMs: timestamp * 1000 };
    networkRequests.set(requestId, captured);
    if (!endpoint) return;
    Object.assign(captured, {
      endpoint,
      body: request.postData ? parseJSON(request.postData) : undefined,
    });
    report.authoringRequests.push(captured);
  });
  browser.cdp.on('Network.loadingFailed', ({ requestId, errorText, type, canceled }) => {
    const captured = networkRequests.get(requestId);
    if (!captured) return;
    const failure = { pathname: captured.pathname, type, errorText, canceled: Boolean(canceled) };
    captured.loadingFailure = failure;
    report.browserErrors.network.push(failure);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response, timestamp }) => {
    const captured = networkRequests.get(requestId);
    if (captured) {
      captured.status = response.status;
      captured.responseStartedAtMs = timestamp * 1000;
    }
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId, timestamp }) => {
    const captured = networkRequests.get(requestId);
    if (!captured?.endpoint) return;
    const responseRead = browser.cdp.send('Network.getResponseBody', { requestId }).then(result => {
      const body = result.isBase64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body;
      captured.response = parseJSON(body);
      captured.responseFinishedAtMs = timestamp * 1000;
      captured.durationMs = captured.responseFinishedAtMs - captured.requestStartedAtMs;
    }).catch(error => {
      captured.responseReadError = String(error);
    }).finally(() => pendingResponseReads.delete(responseRead));
    pendingResponseReads.add(responseRead);
  });

  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid=${JSON.stringify(`construction-table-${outputId}`)}]'))`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-action-table-pivot-rows"]')?.disabled===false`);
  const reshapeActions = await browserEval(browser.cdp, `const menu=document.querySelector('[aria-label="Choose a row change"]');return [...(menu?.querySelectorAll('button')??[])].map(button=>({testId:button.dataset.testid??'',label:button.innerText.trim().replace(/\s+/g,' ').slice(0,140),disabled:button.disabled}));`);
  report.reshapeActions = reshapeActions;
  const genericPivotAction = reshapeActions.find(action => action.testId === 'construction-action-table-pivot-rows' && !action.disabled);
  assert(genericPivotAction, 'Generic source Pivot must be enabled after capability discovery settles');
  const pivotActionTestId = 'construction-action-table-pivot-rows';
  report.pivotEntryControl = pivotActionTestId;
  await click(browser.cdp, `[data-testid="${pivotActionTestId}"]`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-reshape-pivot"] select[aria-label="Pivot category field"]'))`);

  const groupSourceValue = await selectPivotSource('Add pivot group field', 'Observation.id');
  const categorySourceValue = await selectPivotSource('Pivot category field', 'Observation.valueQuantity.code');
  const startedAt = Date.now();
  const requestOffset = report.authoringRequests.length;
  const valueSourceValue = await selectPivotSource('Pivot values field', 'Observation.valueQuantity.value');
  await waitForBrowser(browser.cdp,
    `(()=>{const editor=document.querySelector('[data-testid="construction-reshape-pivot"]');const text=editor?.innerText??'';return !text.includes('Finding categories')&&(Boolean(document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]'))||text.includes('Some records have no category field')||text.includes('more than 256 category values'))})()`,
    15000);
  await drainResponseReads();
  const discovery = report.authoringRequests.slice(requestOffset).findLast(request => request.endpoint === 'construction-category-discoveries');
  const editorState = await browserEval(browser.cdp, `const editor=document.querySelector('[data-testid="construction-reshape-pivot"]');const prefix='Include category ';return {text:editor?.innerText??'',summary:document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')?.innerText,categoryControls:[...document.querySelectorAll('[data-testid="construction-reshape-pivot-categories"] input[aria-label^="Include category "]')].map(input=>({label:input.getAttribute('aria-label')?.slice(prefix.length),checked:input.checked})).filter(item=>item.label)};`);
  const durationMs = Date.now() - startedAt;
  const selectedChoiceId = value => typeof value === 'string' && value.startsWith('source:') ? value.slice('source:'.length) : undefined;
  const recordedBindings = discovery?.body?.pivotSources ?? [];
  const bindingFor = value => recordedBindings.find(binding => binding.choiceId === selectedChoiceId(value));
  const groupBinding = bindingFor(groupSourceValue);
  const categoryBinding = bindingFor(categorySourceValue);
  const valueBinding = bindingFor(valueSourceValue);
  const currentSteps = builder.workspace.documents.find(document => document.output.id === outputId)?.construction?.steps ?? [];
  report.cases.push({
    name: 'root-quantity-category-discovery-to-render',
    observedStatus: discovery?.status,
    durationMs,
    summary: editorState.summary,
    visibleMessage: editorState.text,
    rawSourceRows: oracle.sourceRows,
    rawCategoryCount: oracle.categories.length,
    missingRows: oracle.missingRows,
    explicitNullRows: oracle.explicitNullRows,
    discoveryOutcome: discovery?.response?.outcome,
    discoveryComplete: discovery?.response?.complete,
    discoveryMessage: discovery?.response?.message,
    discoveryDurationMs: discovery?.durationMs,
    prePivot: {
      savedStepCount: currentSteps.length,
      candidateStepCount: discovery?.body?.candidateConstruction?.steps?.length,
      pivotStepIdPresent: Boolean(discovery?.body?.pivotStepId),
      groupColumnId: groupBinding?.columnId,
      categoryColumnId: categoryBinding?.columnId,
      valueColumnId: valueBinding?.columnId,
      groupKeyIds: discovery?.body?.groupKeyIds,
      exactBindingsSelected: Boolean(groupBinding && categoryBinding && valueBinding),
    },
  });
  assert(discovery, 'Selecting root quantity fields must issue category discovery');
  assert.equal(discovery.status, 200, JSON.stringify(discovery.response));
  assert.equal(discovery.responseReadError, undefined, `Category discovery response body must be readable: ${discovery.responseReadError}`);
  assert.equal(discovery.loadingFailure, undefined, JSON.stringify(discovery.loadingFailure));
  assert.equal(discovery.body?.outputId, outputId);
  assert.equal(discovery.body?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(discovery.body?.expectedDraftVersion, builder.draftVersion);
  assert.equal(discovery.body?.expectedDraftDigest, builder.draftDigest);
  assert.equal(discovery.response?.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(discovery.response?.draftVersion, builder.draftVersion);
  assert.equal(discovery.response?.draftDigest, builder.draftDigest);
  assert.equal(discovery.response?.outputId, outputId);
  assert.equal(discovery.response?.stageId, discovery.body?.stageId);
  const candidateConstruction = discovery.body?.candidateConstruction;
  assert(candidateConstruction, 'Root source Pivot discovery must carry its pre-Pivot construction');
  assert.equal(candidateConstruction.version, 1);
  assert.deepEqual(currentSteps, [], 'This root fixture must have no saved construction steps before Pivot');
  assert.deepEqual(candidateConstruction.steps, currentSteps, 'Discovery must receive the empty root pre-Pivot construction');
  assert.equal(typeof discovery.body?.pivotStepId, 'string');
  assert(discovery.body.pivotStepId.length > 0, 'The candidate Pivot identity must travel separately from pre-Pivot construction');
  const expectedChoiceIds = [groupSourceValue, categorySourceValue, valueSourceValue].map(selectedChoiceId);
  assert(expectedChoiceIds.every(Boolean), 'Each selected source binding must be an exact source-choice value');
  assert.equal(recordedBindings.length, 3, 'Discovery must receive exactly the selected root group/category/value bindings');
  assert.deepEqual(recordedBindings.map(binding => binding.choiceId).sort(), [...expectedChoiceIds].sort());
  assert(groupBinding && categoryBinding && valueBinding, 'All selected root source choices must map to binding IDs');
  assert.equal(new Set(recordedBindings.map(binding => binding.columnId)).size, 3, 'Root source binding column IDs must be distinct');
  assert.deepEqual(discovery.body.groupKeyIds, [groupBinding.columnId]);
  assert.equal(discovery.body.categoryColumnId, categoryBinding.columnId);
  assert.equal(discovery.body.valueColumnId, valueBinding.columnId);
  assert(groupBinding.columnId !== discovery.body.pivotStepId);
  assert(categoryBinding.columnId !== discovery.body.pivotStepId);
  assert(valueBinding.columnId !== discovery.body.pivotStepId);
  assert.deepEqual(report.browserErrors.runtime, [], `Browser runtime exceptions: ${JSON.stringify(report.browserErrors.runtime)}`);
  assert.deepEqual(report.browserErrors.console, [], `Browser console errors: ${JSON.stringify(report.browserErrors.console)}`);
  const discoveryFailures = report.browserErrors.network.filter(failure => failure.pathname.endsWith('/construction-category-discoveries') && !failure.canceled);
  assert.deepEqual(discoveryFailures, [], `Category discovery network failures: ${JSON.stringify(discoveryFailures)}`);
  assert(durationMs <= 5000, `Root quantity category discovery took ${durationMs}ms`);
  report.rawCategoryPresence = {
    missingRows: oracle.missingRows,
    explicitNullRows: oracle.explicitNullRows,
    categories: oracle.categories,
    semantics: 'MISSING and explicit NULL remain distinct in the independent raw source oracle; the discovery outcome is checked separately without merging identities',
  };
  if (oracle.categories.length > 256) {
    assert.equal(discovery.response?.outcome, 'LIMIT_EXCEEDED', JSON.stringify(discovery.response));
    assert.equal(discovery.response?.complete, false);
    assert.deepEqual(discovery.response?.categories, []);
    assert(editorState.text.includes('more than 256 category values'), 'The UI must show the typed category limit response');
    report.categoryCorrectness = 'raw full scoped category domain exceeds the documented 256-value limit; API returned no partial set';
    report.status = 'failed';
    report.productFailure = 'The root category domain exceeds the supported Pivot category limit.';
    report.error = report.productFailure;
    process.exitCode = 1;
  } else if (discovery.response?.outcome === 'MISSING_UNSUPPORTED') {
    assert(oracle.missingRows > 0, 'MISSING_UNSUPPORTED is valid only when the full scoped raw oracle proves absent category fields');
    assert.equal(discovery.response.complete, false);
    assert.deepEqual(discovery.response.categories, []);
    assert(editorState.text.includes('Some records have no category field'), 'The UI must show the typed MISSING limitation');
    report.categoryCorrectness = 'raw oracle distinguishes MISSING from explicit NULL; API declared MISSING_UNSUPPORTED and exposed the limitation';
    report.status = 'failed';
    report.productFailure = 'Root Observation quantity Pivot cannot complete while any scoped source row lacks valueQuantity.code.';
    report.error = report.productFailure;
    process.exitCode = 1;
  } else if (discovery.response?.outcome === 'COMPLETE') {
    assert.equal(discovery.response.complete, true);
    const expected = oracle.categoryIdentities;
    const actual = discovery.response.categories.map(category => category.key.kind === 'MISSING'
      ? 'MISSING'
      : category.key.kind === 'NULL'
        ? 'NULL'
        : JSON.stringify(category.key)).sort();
    assert.deepEqual(actual, expected, 'Complete root categories must exactly match every scoped raw identity, keeping MISSING, NULL, and typed values distinct');
    const expectedLabels = discovery.response.categories.map(category => category.label).sort();
    assert(editorState.summary, 'Complete discovery must render the Pivot category summary');
    assert(editorState.summary.startsWith(`Selected ${expected.length} of ${expected.length} categories`), 'Visible category summary must show every typed category selected');
    assert.deepEqual(editorState.categoryControls.map(item => item.label).sort(), expectedLabels, 'Visible Pivot category controls must preserve every API category label');
    assert(editorState.categoryControls.every(item => item.checked), 'Every complete raw category identity must be selected in the initial Pivot form');
    report.categoryCorrectness = 'complete raw project/generation Observation oracle matched MISSING, NULL, and typed scalar categories';
    report.status = 'passed';
  } else {
    assert.fail(`Unexpected root category discovery outcome: ${JSON.stringify(discovery.response)}`);
  }
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (browser) {
    try {
      report.failureDiagnostics = await browserEval(browser.cdp, `return {
        url: location.href,
        title: document.title,
        bodyText: (document.body?.innerText??'').slice(0,5000),
        actions: [...document.querySelectorAll('[data-testid^="construction-action-"]')].map(button=>({testId:button.dataset.testid??'',label:button.innerText.trim().replace(/\s+/g,' ').slice(0,140),disabled:button.disabled})),
        selects: [...document.querySelectorAll('select')].map(select=>({label:select.getAttribute('aria-label'),disabled:select.disabled,value:select.value,options:[...select.options].map(option=>({label:option.textContent,value:option.value})).slice(0,100)})),
        statusMessages: [...document.querySelectorAll('[role="status"],[role="alert"]')].map(node=>node.innerText.trim()).filter(Boolean).slice(0,60),
        editorKinds: {pivot:Boolean(document.querySelector('[data-testid="construction-reshape-pivot"]')),codedPivot:Boolean(document.querySelector('[data-testid="construction-coded-pivot-editor"],[data-testid="construction-reshape-coded-pivot"]'))}
      };`);
    } catch (diagnosticError) {
      report.failureDiagnosticsError = String(diagnosticError);
    }
  }
  process.exitCode = 1;
} finally {
  await drainResponseReads();
  if (sourceFreeze) {
    try { report.sourceFreeze = await sourceFreeze.assertUnchanged(); }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.sourceFreeze = { unchanged: false, changedPaths: error.changedPaths ?? [], invalidatesRun: true, productFailure: false };
      process.exitCode = 1;
    }
  }
  if (apiBuildFreeze) {
    try { report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged(); }
    catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreeze = { unchanged: false, invalidatesRun: true, productFailure: false, reason: error.reason };
      process.exitCode = 1;
    }
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases, error: report.error }));
