import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseArgs } from 'node:util';
import { browserEval, click, launchBrowser, navigate, selectOption, waitForBrowser } from './lib/browser.mjs';

const { values } = parseArgs({ options: {
  'api-origin': { type: 'string', default: 'http://127.0.0.1:8188' },
  'ui-origin': { type: 'string', default: 'http://127.0.0.1:30008' },
  project: { type: 'string', default: 'loom_dev_cda_fhir' },
  generation: { type: 'string', default: 'cda-fhir-v1' },
  evidence: { type: 'string', default: '/tmp/loom-cda-repeated-empty-' + Date.now() },
  'arango-container': { type: 'string', default: 'loom-dev-6d7df93d6a37-arangodb-1' },
} });

const protectedExplorer = 'cda-builder-full-qa-1790440983382';
const explorer = 'cda-repeated-empty-' + Date.now();
assert.notEqual(explorer, protectedExplorer);
const report = {
  started: new Date().toISOString(), invocation: process.argv,
  target: { apiOrigin: values['api-origin'], uiOrigin: values['ui-origin'], project: values.project, generation: values.generation, protectedExplorer },
  explorer, assertions: [], gaps: [], failures: [], timings: [], requests: [], browserRequests: [],
  browserErrors: { exceptions: [], console: [], modules: [], http: [], incidental: [] }, evidencePaths: [],
};
const root = '/api/v1/projects/' + encodeURIComponent(values.project) + '/explorers';
const base = root + '/' + encodeURIComponent(explorer) + '/authoring/v2';
const shapeSelect = 'select[aria-label="What should each row represent?"]';
const policySelect = 'select[aria-label="Unmatched record policy"]';
const tableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
let builder;
let outputId;
let browser;
let browserPending = new Set();
let expectedEmptyErrorMode = false;

const q = value => JSON.stringify(value);
const recordAssertion = (name, evidence) => report.assertions.push({ name, status: 'passed', evidence });
const api = async (path, body, allowFailure = false) => {
  const requestId = 'cda-repeated-empty-' + randomUUID();
  const startedAt = Date.now();
  const response = await fetch(values['api-origin'] + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let responseBody;
  try { responseBody = JSON.parse(text); } catch { responseBody = text; }
  report.requests.push({ path, requestId, body, status: response.status, durationMs: Date.now() - startedAt, response: responseBody });
  if (!allowFailure) assert(response.ok, response.status + ' ' + path + ': ' + JSON.stringify(responseBody));
  return { status: response.status, body: responseBody };
};
const identity = (value = builder) => ({
  snapshotToken: value.catalog.snapshotToken, expectedDraftVersion: value.draftVersion, expectedDraftDigest: value.draftDigest,
});
const command = async commands => {
  await api(base + '/commands', {
    ...identity(), commandId: randomUUID(), semanticsVersion: builder.workspace?.semanticsVersion ?? 10, commands,
  });
  builder = (await api(base + '/builder')).body;
};
const document = () => builder.workspace.documents.find(doc => doc.output.id === outputId);
const rowsReady = count => {
  const countCondition = count === 0 ? 'rowCount <= 1' : 'rowCount === ' + (count + 1);
  return '(()=>{const table=document.querySelector(' + q(tableSelector) + ');const rowCount=Number(table?.getAttribute("aria-rowcount"));return Boolean(table)&&' +
    countCondition + '&&!document.body.innerText.includes("Loading your table")})()';
};
const domText = () => browserEval(browser.cdp, 'return document.body.innerText;');
const responseEvidence = value => {
  if (value === undefined) return undefined;
  const source = typeof value === 'string' ? value : String(value);
  try { return JSON.parse(source); } catch { return source.slice(0, 32768); }
};

const boundedRawOracle = () => {
  const query = 'FOR r IN Observation FILTER r.project == ' + q(values.project) + ' AND r.dataset_generation == ' +
    q(values.generation) + ' SORT r.id LIMIT 1000 RETURN {id:r.id,generation:r.dataset_generation,payload:r.payload}';
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev',
    '--javascript.execute-string', 'print(JSON.stringify(db._query(' + q(query) + ').toArray()));',
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr);
  const jsonStart = result.stdout.indexOf('[');
  assert(jsonStart >= 0, 'Arango oracle returned no JSON array: ' + result.stdout.slice(0, 300));
  const scanned = JSON.parse(result.stdout.slice(jsonStart));
  assert(scanned.length <= 1000, 'Raw source scan exceeded the 1000 Observation bound');
  const positives = scanned.flatMap(resource => {
    const component = resource.payload?.component;
    if (!Array.isArray(component) || component.length < 2 || component.length > 3) return [];
    const componentValues = component.map((item, ordinal) => ({ ordinal, value: item?.valueString }));
    if (!componentValues.every(item => typeof item.value === 'string' && item.value.trim().length > 0)) return [];
    if (new Set(componentValues.map(item => item.value)).size < 2) return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: resource.payload.resourceType, componentValues }];
  });
  const empties = scanned.flatMap(resource => {
    const payload = resource.payload ?? {};
    const component = payload.component;
    let emptyComponentKind;
    if (!Object.hasOwn(payload, 'component')) emptyComponentKind = 'missing';
    else if (component === null) emptyComponentKind = 'null';
    else if (Array.isArray(component) && component.length === 0) emptyComponentKind = 'empty-array';
    else if (!Array.isArray(component)) emptyComponentKind = 'non-array';
    else return [];
    return [{ id: resource.id, generation: resource.generation, resourceType: payload.resourceType, emptyComponentKind }];
  });
  const positive = positives[0];
  if (!positive) {
    report.oracle = { source: 'ArangoDB raw Observation payloads', queryLimit: 1000, scanned: scanned.length, selected: [] };
    return [];
  }
  const emptyLimit = Math.min(3, 6 - positive.componentValues.length);
  const selectedEmpty = empties.filter(item => item.id !== positive.id).slice(0, emptyLimit);
  const selected = [positive, ...selectedEmpty];
  report.oracle = {
    source: 'ArangoDB raw Observation payloads', queryLimit: 1000, scanned: scanned.length,
    selected: selected.map(item => item.componentValues
      ? { id: item.id, generation: item.generation, resourceType: item.resourceType, componentValues: item.componentValues }
      : { id: item.id, generation: item.generation, resourceType: item.resourceType, emptyComponentKind: item.emptyComponentKind }),
    expectedComponentRows: positive.componentValues.map(({ ordinal, value }) => ({ id: positive.id, ordinal, value })),
    expectedRecordIDs: selected.map(item => item.id),
    expectedPreservedEmptyIDs: selectedEmpty.map(item => item.id),
    maxSelectedRoots: 4, maxExpectedExpandedRows: 6,
  };
  return selected;
};

const monitorBrowser = () => {
  const byId = new Map();
  const interested = /\/authoring\/v2\/(?:row-definition-proposals|commands|preview|builder)(?:\/|\?|$)/;
  browser.cdp.on('Network.requestWillBeSent', ({ requestId, request, wallTime }) => {
    let url;
    try { url = new URL(request.url); } catch { return; }
    if (!url.pathname.startsWith(root + '/' + encodeURIComponent(explorer) + '/') || !interested.test(url.pathname + url.search)) return;
    const entry = {
      requestId, method: request.method, path: url.pathname + url.search,
      startedAt: wallTime ? Math.round(wallTime * 1000) : Date.now(), body: responseEvidence(request.postData),
    };
    byId.set(requestId, entry);
    report.browserRequests.push(entry);
  });
  browser.cdp.on('Network.responseReceived', ({ requestId, response }) => {
    const entry = byId.get(requestId);
    if (entry) {
      entry.status = response.status;
      entry.responseAt = Date.now();
      entry.durationMs = entry.responseAt - entry.startedAt;
    }
    if (response.status < 400) return;
    const error = { url: response.url, status: response.status };
    if (response.url.endsWith('/favicon.ico')) report.browserErrors.incidental.push(error);
    else if (entry?.path.endsWith('/row-definition-proposals') && expectedEmptyErrorMode && [400, 422].includes(response.status)) {
      report.expectedPolicyErrors ??= [];
      report.expectedPolicyErrors.push(error);
    } else report.browserErrors.http.push(error);
  });
  browser.cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = byId.get(requestId);
    if (!entry) return;
    entry.completedAt = Date.now();
    const read = browser.cdp.send('Network.getResponseBody', { requestId })
      .then(result => { entry.response = responseEvidence(result.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : result.body); })
      .catch(error => { entry.responseReadError = String(error); })
      .finally(() => browserPending.delete(read));
    browserPending.add(read);
  });
  browser.cdp.on('Runtime.exceptionThrown', event => report.browserErrors.exceptions.push(event.exceptionDetails));
  browser.cdp.on('Runtime.consoleAPICalled', event => {
    if (event.type === 'error') report.browserErrors.console.push(event.args.map(arg => arg.value ?? arg.description));
  });
  browser.cdp.on('Network.loadingFailed', event => {
    if (event.type === 'Script' && event.errorText !== 'net::ERR_ABORTED') report.browserErrors.modules.push(event.errorText);
  });
};
const remaining = startedAt => Math.max(100, 5000 - (Date.now() - startedAt));
const fastWait = async (startedAt, expression, message) => {
  try { await waitForBrowser(browser.cdp, expression, remaining(startedAt)); }
  catch (error) { throw new Error(message + ' within the five-second action budget: ' + String(error)); }
};
const measure = async (name, action) => {
  const startedAt = Date.now();
  const result = await action(startedAt);
  const durationMs = Date.now() - startedAt;
  report.timings.push({ name, durationMs, limitMs: 5000 });
  assert(durationMs <= 5000, name + ' took ' + durationMs + 'ms');
  return result;
};
const tableURL = () => values['ui-origin'] + '/?project=' + encodeURIComponent(values.project) + '&explorer=' + encodeURIComponent(explorer) + '&mode=builder';
const openTable = async (expectedRows, name) => measure(name, async startedAt => {
  await navigate(browser.cdp, tableURL());
  const table = '[data-testid="construction-table-' + outputId + '"]';
  await fastWait(startedAt, 'document.querySelector(' + q(table) + ')', 'Explorer table discovery');
  await click(browser.cdp, table);
  await fastWait(startedAt, rowsReady(expectedRows), 'CDA table render');
});
const openRowSettings = async () => measure('row-definition-choice-discovery', async startedAt => {
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await fastWait(startedAt, 'document.querySelector(' + q(shapeSelect) + ')?.disabled === false', 'Row definition choice discovery');
});
const rowProposalRequest = selection => report.browserRequests.findLast(entry =>
  entry.path.endsWith('/row-definition-proposals') && entry.body?.selection?.kind === selection.kind &&
  (selection.kind !== 'EXPANDED' || (entry.body.selection.expanded?.rowChoiceId === selection.expanded.rowChoiceId &&
    entry.body.selection.expanded?.emptyCollectionPolicy === selection.expanded.emptyCollectionPolicy)));
const awaitRequestBody = async (entry, startedAt) => {
  while (entry && !Object.hasOwn(entry, 'response') && !entry.responseReadError && Date.now() - startedAt < 5000) {
    await new Promise(resolve => setTimeout(resolve, 30));
  }
  assert(entry && Object.hasOwn(entry, 'response'), 'Native row proposal response was not captured: ' + JSON.stringify(entry));
};
const selectAndPreview = async (policy, expectedRows, emptyError = false) => measure('row-definition-preview-' + policy, async startedAt => {
  expectedEmptyErrorMode = emptyError;
  const selectedShape = await browserEval(browser.cdp, 'return document.querySelector(' + q(shapeSelect) + ')?.value;');
  if (selectedShape !== 'expanded:' + report.choice.choiceId) {
    await selectOption(browser.cdp, shapeSelect, 'expanded:' + report.choice.choiceId);
  }
  const wanted = 'expanded:' + report.choice.choiceId + ':' + policy;
  const selectedPolicy = await browserEval(browser.cdp, 'return document.querySelector(' + q(policySelect) + ')?.value;');
  if (selectedPolicy !== wanted) await selectOption(browser.cdp, policySelect, wanted);
  const terminal = emptyError
    ? 'Boolean(document.querySelector(\'[aria-label="Row definition settings"] [role="alert"]\')) || Boolean(document.querySelector(\'[aria-label="Row definition preview"]\'))'
    : 'document.querySelector(\'[aria-label="Row definition preview"]\')?.innerText.includes(' + q('→ ' + expectedRows + ' rows') + ')';
  await fastWait(startedAt, terminal, emptyError ? 'Expected empty-collection validation' : 'Automatic row definition preview');
  await Promise.all([...browserPending]);
  const selection = { kind: 'EXPANDED', expanded: { rowChoiceId: report.choice.choiceId, emptyCollectionPolicy: policy } };
  const request = rowProposalRequest(selection);
  await awaitRequestBody(request, startedAt);
  report.latestProposal = request;
  if (!emptyError) {
    assert.equal(request.status, 200, 'Row-definition ' + policy + ' proposal response: ' + JSON.stringify(request.response));
    assert.equal(request.response?.mode, 'EXPANDED');
    assert.equal(request.response?.comparison?.candidate?.rowCount, expectedRows);
  }
  return request;
});
const selectRecordsPreview = expectedRows => measure('row-definition-preview-records', async startedAt => {
  await selectOption(browser.cdp, shapeSelect, 'records');
  await fastWait(startedAt, 'document.querySelector(\'[aria-label="Row definition preview"]\')?.innerText.includes(' + q('→ ' + expectedRows + ' rows') + ')', 'Source-record row preview');
  await Promise.all([...browserPending]);
  const request = rowProposalRequest({ kind: 'RECORDS' });
  await awaitRequestBody(request, startedAt);
  assert.equal(request.status, 200, 'RECORDS proposal response: ' + JSON.stringify(request.response));
  assert.equal(request.response?.comparison?.candidate?.rowCount, expectedRows);
  report.latestProposal = request;
});
const applyRowDefinition = expectedRows => measure('row-definition-apply', async startedAt => {
  await click(browser.cdp, '[aria-label="Row definition settings"] button', { name: 'Apply row definition' });
  await fastWait(startedAt, '!document.querySelector(\'[aria-label="Row definition settings"]\') && ' + rowsReady(expectedRows), 'Applied row definition render');
});
const cancelRowDefinition = expectedRows => measure('row-definition-cancel', async startedAt => {
  await click(browser.cdp, '[aria-label="Row definition settings"] button', { name: 'Cancel' });
  await fastWait(startedAt, '!document.querySelector(\'[aria-label="Row definition settings"]\') && ' + rowsReady(expectedRows), 'Canceled row definition restoration');
});
const fieldPreviewRows = () => browserEval(browser.cdp,
  'const proposalRow=document.querySelector(\'[data-testid="construction-proposal-preview-row"]\');' +
  'const root=proposalRow?.closest("table")??document.querySelector(\'[data-testid="preview-table-scroll"] [role="table"]\');' +
  'if(!root)return null;const proposal=Boolean(proposalRow);' +
  'const headers=[...root.querySelectorAll(proposal?"thead th":"[role=columnheader]")].map(cell=>cell.innerText.trim());' +
  'const rows=[...root.querySelectorAll(proposal?\'[data-testid="construction-proposal-preview-row"]\':"[role=row]")].slice(proposal?0:1)' +
  '.map(row=>[...row.querySelectorAll(proposal?"td":"[role=cell]")].map(cell=>({text:cell.innerText.trim(),raw:cell.title}))).filter(row=>row.length);' +
  'return {headers,rows,rowCount:root.getAttribute("aria-rowcount")};');
const parseCell = cell => {
  if (cell?.raw) { try { return JSON.parse(cell.raw); } catch { return cell.raw; } }
  const value = cell?.text?.trim() ?? '';
  if (!value) return '';
  try { return JSON.parse(value); } catch { return value; }
};
const columnIndices = preview => {
  assert(preview, 'Native table preview is unavailable');
  const idIndex = preview.headers.findIndex(header => header.split(String.fromCharCode(10))[0].trim().toUpperCase() === 'OBSERVATION ID');
  const valueIndex = preview.headers.findIndex(header => /component.*value.?string/i.test(header));
  assert(idIndex >= 0, 'Observation ID is missing from native preview: ' + JSON.stringify(preview.headers));
  assert(valueIndex >= 0, 'component[].valueString is missing from native preview: ' + JSON.stringify(preview.headers));
  return { idIndex, valueIndex };
};
const verifyPreview = async (policy, expectedPairs, emptyIDs = []) => {
  const preview = await fieldPreviewRows();
  const { idIndex, valueIndex } = columnIndices(preview);
  assert.equal(preview.rows.length, expectedPairs.length + (policy === 'PRESERVE_PARENT' ? emptyIDs.length : 0), policy + ' native preview row count');
  const emptySet = new Set(emptyIDs);
  const actualPairs = [];
  const preservedEmptyIDs = [];
  for (const row of preview.rows) {
    const id = String(parseCell(row[idIndex]));
    const value = parseCell(row[valueIndex]);
    if (emptySet.has(id)) {
      preservedEmptyIDs.push(id);
      assert(Array.isArray(value) ? value.length === 0 : value === '' || value === null || (value === '—' && !row[valueIndex].raw),
        'Preserved empty-component row ' + id + ' contains a value: ' + JSON.stringify(value));
    } else {
      const items = Array.isArray(value) ? value : [value];
      assert.equal(items.length, 1, 'Expanded row ' + id + ' must contain one component value: ' + JSON.stringify(value));
      assert.equal(typeof items[0], 'string', 'Expanded component value must be text');
      actualPairs.push([id, items[0]]);
    }
  }
  const sortPairs = pairs => pairs.slice().sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  const expected = report.oracle.expectedComponentRows.map(item => [item.id, item.value]);
  assert.deepEqual(sortPairs(actualPairs), sortPairs(expected), policy + ' values must match raw component array items');
  assert.deepEqual(preservedEmptyIDs.sort(), (policy === 'PRESERVE_PARENT' ? emptyIDs : []).slice().sort(), policy + ' empty owner rows');
  const response = report.browserRequests.findLast(request => request.path.endsWith('/preview') && request.status === 200)?.response;
  assert(response?.rows && response?.columns, 'Saved native preview protocol is missing');
  const idColumn = response.columns.find(column => column.label === 'Observation ID')?.column;
  const valueColumn = response.columns.find(column => /component.*value.?string/i.test(column.label))?.column;
  assert(idColumn && valueColumn, 'Saved native preview protocol is missing field metadata');
  for (const id of preservedEmptyIDs) {
    const rows = response.rows.filter(row => row[idColumn] === id);
    assert.equal(rows.length, 1, 'Saved protocol must preserve exactly one empty owner row');
    assert.deepEqual(rows[0][valueColumn], [], 'Rendered empty placeholder must correspond to an empty value list');
  }
  return { headers: preview.headers, rowCount: preview.rows.length, positivePairs: sortPairs(actualPairs), preservedEmptyIDs };
};
const createSelection = async (refs, idempotencyKey) => {
  const selectionsPath = base.replace('/authoring/v2', '/selections');
  const selection = (await api(selectionsPath, {
    snapshotToken: builder.catalog.snapshotToken, idempotencyKey,
    source: { kind: 'resources', resources: { refs: refs.map(resource => ({
      project: values.project, generation: resource.generation, resourceType: 'Observation', id: resource.id,
    })) } },
  })).body;
  const routes = (await api(base + '/population-routes', {
    snapshotToken: builder.catalog.snapshotToken, outputId, selectionRevisionId: selection.id, limit: 25,
  })).body;
  const route = routes.choices.find(choice => choice.route.length === 0);
  assert(route, 'No root Observation population route for selection ' + selection.id);
  return { selection, route };
};
const setPopulation = selectionChoice => command([{
  type: 'SET_TABLE_POPULATION', outputId, selectionRevisionId: selectionChoice.selection.id,
  routeChoiceId: selectionChoice.route.routeChoiceId,
}]);
const saveDOM = async name => {
  const path = join(values.evidence, name + '.dom.txt');
  await writeFile(path, await domText());
  report.evidencePaths.push(path);
};
const verifyEmptyError = async request => {
  assert(request, 'No native ERROR-policy row proposal was captured');
  assert([400, 422].includes(request.status), 'Empty-only ERROR must return 400/422, got ' + request.status + ': ' + JSON.stringify(request.response));
  const alertText = await browserEval(browser.cdp, 'return document.querySelector(\'[aria-label="Row definition settings"] [role="alert"]\')?.innerText ?? "";');
  const responseText = JSON.stringify(request.response) + ' ' + alertText;
  const code = request.response?.error?.code ?? request.response?.code ?? request.response?.errorCode ?? request.response?.error?.errorCode;
  assert.notEqual(code, 'INTERNAL_ERROR', 'Empty-only ERROR returned INTERNAL_ERROR: ' + responseText);
  assert(/empty|no values|at least one|collection/i.test(responseText), 'Validation did not explain the empty collection: ' + responseText);
  const policy = await browserEval(browser.cdp,
    'const select=document.querySelector(' + q(policySelect) + ');return {disabled:select?.disabled,options:[...(select?.options??[])].map(option=>({value:option.value,label:option.text,disabled:option.disabled}))};');
  assert.equal(policy.disabled, false, 'Policy repair control must remain enabled after ERROR validation');
  assert(policy.options.some(option => option.value.endsWith(':PRESERVE_PARENT') && !option.disabled), 'PRESERVE_PARENT repair choice must remain enabled');
  assert(policy.options.some(option => option.value.endsWith(':EXCLUDE') && !option.disabled), 'EXCLUDE repair choice must remain enabled');
  report.expectedEmptyError = { status: request.status, code, response: request.response, message: alertText, repairOptions: policy.options };
  recordAssertion('ERROR policy gives understandable empty-only validation and keeps repair choices enabled', report.expectedEmptyError);
};
const finish = async () => {
  report.finished = new Date().toISOString();
  report.status = report.failures.length ? 'failed' : report.gaps.length ? 'partial' : report.assertions.length ? 'passed' : 'untested';
  const path = join(values.evidence, 'report.json');
  await writeFile(path, JSON.stringify(report, null, 2));
  report.evidencePaths.push(path);
  console.log(JSON.stringify({
    status: report.status, evidence: values.evidence, explorer,
    assertions: report.assertions.map(({ name, status }) => ({ name, status })), timings: report.timings, gaps: report.gaps, failures: report.failures,
  }, null, 2));
  if (report.status === 'failed') process.exitCode = 1;
};

const main = async () => {
  await mkdir(values.evidence, { recursive: true });
  const selected = boundedRawOracle();
  const positive = selected.find(resource => resource.componentValues);
  const emptyResources = selected.filter(resource => resource.emptyComponentKind);
  if (!positive || emptyResources.length === 0) {
    report.gaps.push({ assertion: 'bounded positive and zero-component Observation oracle', status: 'untested',
      reason: 'The bounded 1000-resource scan needs one Observation with 2–3 distinct non-empty component values and at least one zero-component Observation.' });
    return;
  }
  assert(selected.length <= 4, 'Raw oracle selected more than four Observation roots');
  assert(report.oracle.expectedComponentRows.length + emptyResources.length <= 6, 'PRESERVE_PARENT expansion exceeds six expected rows');
  assert(selected.every(resource => resource.generation === values.generation && resource.resourceType === 'Observation'));
  if (!emptyResources.some(resource => resource.emptyComponentKind === 'empty-array')) {
    report.gaps.push({ assertion: 'literal component: [] source shape', status: 'untested',
      reason: 'Only missing/null/non-array component witnesses were selected; this run does not prove a literal empty-array source.' });
  }
  recordAssertion('bounded raw CDA oracle selected positive and zero-component Observations', {
    roots: selected.length, positiveItems: report.oracle.expectedComponentRows.length,
    emptyWitnesses: emptyResources.map(resource => ({ id: resource.id, kind: resource.emptyComponentKind })),
    expectedPreservedRows: report.oracle.expectedComponentRows.length + emptyResources.length,
  });

  await api(root, { name: explorer, title: 'CDA empty component row policy QA' });
  builder = (await api(base + '/builder')).body;
  assert.equal(builder.catalog.generation, values.generation, 'Fresh QA Explorer must use the requested CDA generation');
  const observationNode = builder.catalog.nodes.find(node => node.resourceType === 'Observation');
  assert(observationNode, 'CDA catalog has no Observation root');
  await command([{ type: 'CREATE_TABLE', title: 'Empty component policies', rootNodeId: observationNode.nodeId }]);
  outputId = builder.workspace.documents[0].output.id;
  const idCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'id');
  const valueCandidate = builder.catalog.candidates.find(candidate => candidate.nodeId === observationNode.nodeId && candidate.fieldPath === 'component[].valueString');
  assert(idCandidate, 'Observation ID field is unavailable');
  assert(valueCandidate, 'Observation.component[].valueString field is unavailable');
  await command([
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Observation ID' },
    { type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidate.candidateId, projectionMode: 'ALL', initialPresentation: 'TABLE', title: 'Component Value String' },
  ]);
  const originalPopulation = await createSelection(selected, explorer + '-original');
  await setPopulation(originalPopulation);
  report.baselineDocument = structuredClone(document());
  const choices = (await api(base + '/row-definition-choices?' + new URLSearchParams({
    snapshotToken: builder.catalog.snapshotToken, outputId,
  }))).body;
  const choice = choices.choices.find(item => item.kind === 'EXPANDED' && item.fieldPath === 'component[]' && item.occurrenceId === 'base');
  assert(choice, 'Fresh QA table has no root Observation.component[] expansion choice');
  for (const policy of ['PRESERVE_PARENT', 'EXCLUDE', 'ERROR']) {
    assert(choice.policies.find(item => item.name === 'emptyCollectionPolicy')?.options.includes(policy), 'component[] choice omits ' + policy);
  }
  report.choice = { choiceId: choice.choiceId, fieldPath: choice.fieldPath, occurrenceId: choice.occurrenceId };

  browser = await launchBrowser(values.evidence);
  monitorBrowser();
  const rootsCount = selected.length;
  const preserveCount = report.oracle.expectedComponentRows.length + emptyResources.length;
  const excludeCount = report.oracle.expectedComponentRows.length;
  await openTable(rootsCount, 'fresh-owned-explorer-load');
  await saveDOM('source-record-table');
  const initial = await fieldPreviewRows();
  const initialIndexes = columnIndices(initial);
  assert.deepEqual(initial.rows.map(row => String(parseCell(row[initialIndexes.idIndex]))).sort(), report.oracle.expectedRecordIDs.slice().sort(),
    'Initial RECORDS table must contain the exact selected source resources');
  recordAssertion('seeded table starts with exact selected source records and both field bindings', { headers: initial.headers, rowCount: initial.rows.length, ids: report.oracle.expectedRecordIDs });

  await openRowSettings();
  await selectAndPreview('PRESERVE_PARENT', preserveCount);
  const beforeCancel = (await api(base + '/builder')).body;
  await cancelRowDefinition(rootsCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(builder.draftDigest, beforeCancel.draftDigest, 'PRESERVE_PARENT Cancel must not mutate the saved draft');
  assert.equal(document().rows.kind, 'RECORDS');
  recordAssertion('native PRESERVE_PARENT preview Cancel restores source-record rows', { rowCount: rootsCount, rowKind: document().rows.kind });

  await openRowSettings();
  await selectAndPreview('PRESERVE_PARENT', preserveCount);
  await applyRowDefinition(preserveCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.kind, 'EXPANDED');
  assert.equal(document().rows.expanded.scopePath, 'component[]');
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'PRESERVE_PARENT');
  report.preserveParentApplied = await verifyPreview('PRESERVE_PARENT', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  await saveDOM('preserve-parent-applied');
  await openTable(preserveCount, 'reload-preserve-parent');
  report.preserveParentReload = await verifyPreview('PRESERVE_PARENT', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  recordAssertion('PRESERVE_PARENT preserves positive item values and one empty row per owner across reload', report.preserveParentReload);

  await openRowSettings();
  await selectAndPreview('EXCLUDE', excludeCount);
  await applyRowDefinition(excludeCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'EXCLUDE');
  report.excludeApplied = await verifyPreview('EXCLUDE', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  await saveDOM('exclude-applied');
  await openTable(excludeCount, 'reload-exclude');
  report.excludeReload = await verifyPreview('EXCLUDE', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  recordAssertion('EXCLUDE omits zero-component owners and retains exact positive item values across reload', report.excludeReload);

  const emptyPopulation = await createSelection(emptyResources, explorer + '-empty-only');
  await setPopulation(emptyPopulation);
  await openTable(0, 'reload-empty-only-exclude');
  await openRowSettings();
  const errorProposal = await selectAndPreview('ERROR', undefined, true);
  await verifyEmptyError(errorProposal);
  expectedEmptyErrorMode = false;
  await measure('repair-policy-after-error', async startedAt => {
    await selectOption(browser.cdp, policySelect, 'expanded:' + report.choice.choiceId + ':PRESERVE_PARENT');
    await fastWait(startedAt, 'document.querySelector(\'[aria-label="Row definition preview"]\')?.innerText.includes(' +
      q('→ ' + emptyResources.length + ' rows') + ')', 'Actionable empty-collection policy repair');
    await Promise.all([...browserPending]);
  });
  const repair = rowProposalRequest({ kind: 'EXPANDED', expanded: { rowChoiceId: report.choice.choiceId, emptyCollectionPolicy: 'PRESERVE_PARENT' } });
  assert.equal(repair?.status, 200, 'Changing from ERROR to PRESERVE_PARENT must produce a valid preview');
  await cancelRowDefinition(0);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.expanded.emptyCollectionPolicy, 'EXCLUDE', 'Cancel after repair preview must retain saved EXCLUDE');
  recordAssertion('empty-only ERROR leaves an actionable repair policy and Cancel retains saved EXCLUDE', {
    error: report.expectedEmptyError, repairStatus: repair.status,
  });

  await setPopulation(originalPopulation);
  await openTable(excludeCount, 'restore-original-selection');
  report.restoredExclude = await verifyPreview('EXCLUDE', report.oracle.expectedComponentRows, emptyResources.map(resource => resource.id));
  await openRowSettings();
  await selectRecordsPreview(rootsCount);
  await applyRowDefinition(rootsCount);
  builder = (await api(base + '/builder')).body;
  assert.equal(document().rows.kind, 'RECORDS');
  assert.deepEqual(document().population, report.baselineDocument.population, 'Final restore must preserve the exact original selection and route');
  assert.deepEqual(document().columns, report.baselineDocument.columns, 'Final restore must preserve ID and ALL-value field bindings');
  await saveDOM('source-records-restored');
  await openTable(rootsCount, 'reload-final-source-records');
  const finalPreview = await fieldPreviewRows();
  const finalIndexes = columnIndices(finalPreview);
  assert.deepEqual(finalPreview.rows.map(row => String(parseCell(row[finalIndexes.idIndex]))).sort(), report.oracle.expectedRecordIDs.slice().sort(),
    'Final RECORDS reload must restore exact selected Observation IDs');
  builder = (await api(base + '/builder')).body;
  for (const key of ['output', 'rootResourceType', 'route', 'population', 'columns']) {
    assert.deepEqual(document()[key], report.baselineDocument[key], 'Final restoration changed original ' + key);
  }
  report.restoration = { rowKind: document().rows.kind, population: document().population, ids: report.oracle.expectedRecordIDs };
  recordAssertion('edit and reload restore RECORDS with exact selection and source field bindings', report.restoration);

  await Promise.all([...browserPending]);
  assert.deepEqual(report.browserErrors.exceptions, [], 'Browser raised JavaScript exceptions');
  assert.deepEqual(report.browserErrors.console, [], 'Browser logged console errors');
  assert.deepEqual(report.browserErrors.modules, [], 'Browser failed to load a module');
  assert.deepEqual(report.browserErrors.http, [], 'Browser received unexpected 4xx/5xx responses');
  recordAssertion('native row-policy lifecycle had no browser, module, console, or unexpected HTTP errors', report.browserErrors);
};

try {
  await main();
} catch (error) {
  report.failures.push({ error: String(error.stack ?? error), phase: report.assertions.length });
  try { if (browser) report.failureDOM = await domText(); } catch { /* Browser may not have opened. */ }
  try { if (outputId) report.failureBuilder = (await api(base + '/builder')).body; } catch (readError) { report.builderReadError = String(readError); }
} finally {
  try { await Promise.all([...browserPending]); } catch { /* Network response reads remain in the report. */ }
  if (browser) await browser.close().catch(error => { report.browserCloseError = String(error); });
  report.finished = new Date().toISOString();
  report.status = report.failures.length ? 'failed' : report.gaps.length ? 'partial' : report.assertions.length ? 'passed' : 'untested';
  const path = join(values.evidence, 'report.json');
  await writeFile(path, JSON.stringify(report, null, 2));
  report.evidencePaths.push(path);
  console.log(JSON.stringify({
    status: report.status, evidence: values.evidence, explorer,
    assertions: report.assertions.map(({ name, status }) => ({ name, status })), timings: report.timings,
    gaps: report.gaps, failures: report.failures,
  }, null, 2));
  if (report.status === 'failed') process.exitCode = 1;
}
