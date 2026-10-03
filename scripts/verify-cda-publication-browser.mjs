import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const apiOrigin = (process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188').replace(/\/$/, '');
const uiOrigin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const explorerId = `cda-publication-browser-${Date.now()}-${randomUUID().slice(0, 8)}`;
const explorerRoot = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
const explorerPath = `${explorerRoot}/${encodeURIComponent(explorerId)}`;
const authoringPath = `${explorerPath}/authoring/v2`;
const pageURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=builder`;
const viewerURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorerId)}&mode=viewer`;
const evidenceDirectory = process.argv[2] ?? join('.artifacts', 'cda-publication-browser', explorerId);
const apiToken = process.env.LOOM_CDA_API_TOKEN;
const clickhouseContainer = process.env.LOOM_CLICKHOUSE_CONTAINER ?? 'loom-dev-6d7df93d6a37-clickhouse-1';
const report = {
  explorerId,
  project,
  pageURL,
  viewerURL,
  protectedOriginalMutations: 0,
  setup: [],
  protocol: [],
  errors: [],
  incidentalErrors: [],
  timingsMs: {},
  limitations: [
    'The run-owned Explorer and its materialization are retained because this local API has no Explorer or publication delete operation.',
    'The publication contract covers one direct Specimen ID output with at most three source records; it does not exercise broader CDA transformations.',
  ],
};
const protocolRequests = new Map();
const allNetworkRequests = new Map();
const responseTasks = [];
let browser;
let builder;
let sourceIds = [];
let outputId;
let datasetGeneration;
let fatal;

const api = async (path, body) => {
  const method = body === undefined ? 'GET' : 'POST';
  const requestId = `cda-publication-${randomUUID()}`;
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': requestId };
  if (apiToken) headers.Authorization = `Bearer ${apiToken}`;
  const response = await fetch(apiOrigin + path, {
    method,
    headers,
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : undefined; }
  catch { value = text; }
  report.setup.push({ path, method, requestId, status: response.status });
  assert(response.ok, `${response.status} ${path}: ${JSON.stringify(value)}`);
  return value;
};

const rawCdaOracle = (query, bindVars) => {
  const javascript = `const rows = db._query(${JSON.stringify(query)}, ${JSON.stringify(bindVars)}).toArray(); print(JSON.stringify(rows));`;
  const result = spawnSync('rtk', [
    'proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1',
    'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', javascript,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const start = result.stdout.indexOf('[');
  assert(start >= 0, 'Raw scoped CDA oracle returned no JSON rows');
  return JSON.parse(result.stdout.slice(start));
};

const identity = (value) => ({
  snapshotToken: value.catalog.snapshotToken,
  expectedDraftVersion: value.draftVersion,
  expectedDraftDigest: value.draftDigest,
});

const readBuilder = () => api(`${authoringPath}/builder`);

const apply = async (commands) => {
  const result = await api(`${authoringPath}/commands`, {
    ...identity(builder),
    commandId: randomUUID(),
    semanticsVersion: builder.workspace?.semanticsVersion ?? 10,
    commands,
  });
  builder = await readBuilder();
  return result;
};

const seedOwnedExplorer = async () => {
  assert(!explorerId.includes('cda-builder-full-qa-1790440983382'), 'Protected Explorer cannot be targeted');
  await api(explorerRoot, { name: explorerId, title: `CDA publication QA ${explorerId}` });
  builder = await readBuilder();
  datasetGeneration = builder.catalog?.generation;
  assert.equal(typeof datasetGeneration, 'string');
  assert(datasetGeneration.length > 0, 'Fresh Builder catalog has no active dataset generation');

  const oracleQuery = `FOR s IN Specimen FILTER s.project == ${JSON.stringify(project)} AND s.dataset_generation == ${JSON.stringify(datasetGeneration)} SORT s._key LIMIT 3 RETURN {id:s.id,key:s._key}`;
  const oracleRows = rawCdaOracle(oracleQuery, {});
  assert(oracleRows.length > 0 && oracleRows.length <= 3, 'Fresh active-generation oracle must return one to three Specimen records');
  assert(oracleRows.every((row) => typeof row.id === 'string' && row.id.length > 0 && typeof row.key === 'string'));
  sourceIds = oracleRows.map((row) => row.id);
  assert.equal(new Set(sourceIds).size, sourceIds.length, 'Scoped Specimen oracle returned duplicate source IDs');
  report.oracle = {
    collection: 'Specimen',
    project,
    generation: datasetGeneration,
    query: oracleQuery,
    bindVars: {},
    rows: oracleRows,
  };

  const node = builder.catalog.nodes.find((candidate) => candidate.resourceType === 'Specimen');
  assert(node, 'Fresh Builder catalog has no Specimen node');
  const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(idCandidate, 'Fresh Builder catalog has no direct Specimen ID field');

  await apply([{ type: 'CREATE_TABLE', title: 'Bounded Specimen publication', rootNodeId: node.nodeId }]);
  const document = builder.workspace.documents.find((entry) => entry.output.title === 'Bounded Specimen publication');
  assert(document, 'API seed did not create the run-owned publication table');
  outputId = document.output.id;
  await apply([{
    type: 'ADD_COLUMN',
    outputId,
    occurrenceId: 'base',
    candidateId: idCandidate.candidateId,
    projectionMode: 'VALUE',
    initialPresentation: 'TABLE',
    title: 'Specimen ID',
  }]);

  const selection = await api(`${explorerPath}/selections`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `cda-publication-${explorerId}`,
    source: {
      kind: 'resources',
      resources: {
        refs: sourceIds.map((id) => ({ project, generation: datasetGeneration, resourceType: 'Specimen', id })),
      },
    },
  });
  const routes = await api(`${authoringPath}/population-routes`, {
    snapshotToken: builder.catalog.snapshotToken,
    outputId,
    selectionRevisionId: selection.id,
    limit: 50,
  });
  const directRoute = routes.choices.find((choice) => choice.route.length === 0);
  assert(directRoute, 'Scoped source selection has no direct population route');
  await apply([{
    type: 'SET_TABLE_POPULATION',
    outputId,
    selectionRevisionId: selection.id,
    routeChoiceId: directRoute.routeChoiceId,
  }]);

  const documents = builder.workspace.documents;
  assert.equal(documents.length, 1, 'Publication would include an unexpected extra output');
  assert.deepEqual(builder.workspace.tabs.map((tab) => tab.outputId), [outputId]);
  const column = documents[0].columns.find((candidate) => candidate.occurrenceId === 'base');
  assert.equal(column?.source?.kind, 'field');
  assert.equal(column?.source?.field?.path, 'id');
  assert.equal(column?.source?.field?.projectionMode, 'VALUE');
  assert.equal(documents[0].population?.selectionRevisionId, selection.id);
  report.seed = {
    generation: datasetGeneration,
    selectionRevisionId: selection.id,
    outputId,
    title: documents[0].output.title,
    sourceIds,
    route: directRoute.route,
  };
};

const captureNativeProtocol = (cdp) => {
  cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
    report.errors.push({ kind: 'runtime', message: exceptionDetails?.exception?.description ?? exceptionDetails?.text ?? 'Browser exception' });
  });
  cdp.on('Runtime.consoleAPICalled', ({ type, args }) => {
    if (type === 'error') report.errors.push({ kind: 'console', message: args.map((arg) => arg.value ?? arg.description ?? '').join(' ') });
  });
  cdp.on('Network.requestWillBeSent', ({ requestId, request }) => {
    const url = new URL(request.url);
    const isOwnedRequest = url.pathname.includes(`/explorers/${explorerId}/`);
    allNetworkRequests.set(requestId, { path: url.pathname, url: request.url, owned: isOwnedRequest });
    if (!isOwnedRequest) return;
    const entry = {
      path: url.pathname,
      method: request.method,
      request: undefined,
    };
    if (request.postData) {
      try { entry.request = JSON.parse(request.postData); }
      catch { entry.request = request.postData; }
    }
    protocolRequests.set(requestId, entry);
    report.protocol.push(entry);
  });
  cdp.on('Network.responseReceived', ({ requestId, response, type }) => {
    const entry = protocolRequests.get(requestId);
    if (entry) {
      entry.status = response.status;
      entry.responseURL = response.url;
    }
    if (response.status < 400) return;
    const request = allNetworkRequests.get(requestId);
    const error = { kind: 'http', path: request?.path ?? new URL(response.url).pathname, status: response.status, resourceType: type, owned: request?.owned ?? false };
    if (error.path.endsWith('/favicon.ico') && response.status === 404) report.incidentalErrors.push({ ...error, reason: 'The local app has no favicon asset.' });
    else report.errors.push(error);
  });
  cdp.on('Network.loadingFinished', ({ requestId }) => {
    const entry = protocolRequests.get(requestId);
    if (!entry) return;
    responseTasks.push(cdp.send('Network.getResponseBody', { requestId }).then(({ body, base64Encoded }) => {
      const text = base64Encoded ? Buffer.from(body, 'base64').toString('utf8') : body;
      try { entry.response = JSON.parse(text); }
      catch { entry.response = text; }
    }).catch((error) => { entry.responseBodyError = String(error); }));
  });
  cdp.on('Network.loadingFailed', ({ requestId, errorText, type }) => {
    const entry = protocolRequests.get(requestId);
    if (entry) entry.loadingError = errorText;
    const request = allNetworkRequests.get(requestId);
    if (request?.url && new URL(request.url).hostname === 'www.google.com' && request.path === '/one-google-bar' && errorText === 'net::ERR_ABORTED' && type === 'Document') {
      report.incidentalErrors.push({ kind: 'network', path: request.path, origin: new URL(request.url).origin, error: errorText, reason: 'Chrome new-tab document aborted when navigating to the local application.' });
      return;
    }
    report.errors.push({ kind: 'network', path: request?.path, resourceType: type, owned: request?.owned ?? false, error: errorText });
  });
};

const waitForProtocolResponse = async (pathSuffix, timeoutMs) => {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const entry = report.protocol.find((candidate) => candidate.path.endsWith(pathSuffix) && candidate.status !== undefined);
    if (entry) {
      await Promise.all(responseTasks);
      if (entry.response === undefined) await new Promise((resolve) => setTimeout(resolve, 50));
      if (entry.response !== undefined) return entry;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out waiting for native protocol response ${pathSuffix}`);
};

const previewSnapshot = async () => browserEval(browser.cdp, `const panel=document.querySelector('[data-testid="construction-preview"]');const table=document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');const headers=table?[...table.querySelectorAll('[role="columnheader"]')].map(cell=>cell.innerText.trim()):[];const rows=table?[...table.querySelectorAll('[role="row"]')].slice(1).map(row=>[...row.querySelectorAll('[role="cell"]')].map(cell=>cell.innerText.trim())):[];const idIndex=headers.findIndex(header=>header.toUpperCase()==='SPECIMEN ID');return {status:panel?.dataset.previewStatus,outputId:panel?.dataset.previewOutputId,currentDraftVersion:panel?.dataset.currentDraftVersion,currentDraftDigest:panel?.dataset.currentDraftDigest,receiptId:panel?.dataset.previewReceiptId,headers,rows,specimenIds:idIndex<0?[]:rows.map(row=>row[idIndex]).filter(Boolean),ariaRowCount:table?.getAttribute('aria-rowcount')};`);

const viewerSnapshot = async () => browserEval(browser.cdp, `const normalize=value=>String(value??'').replace(/\\s+/g,' ').trim();const tables=[...document.querySelectorAll('[role="table"],table')];const readTable=table=>{const headerNodes=[...table.querySelectorAll('[role="columnheader"],thead th')];const headers=headerNodes.map(cell=>normalize(cell.innerText||cell.textContent));const idIndex=headers.findIndex(header=>header.toUpperCase()==='SPECIMEN ID');const rowNodes=[...table.querySelectorAll('[role="row"],tbody tr')].filter(row=>!row.querySelector('[role="columnheader"],th'));const rows=rowNodes.map(row=>[...row.querySelectorAll('[role="cell"],td')].map(cell=>normalize(cell.innerText||cell.textContent)));return {headers,idIndex,rows,specimenIds:idIndex<0?[]:rows.map(row=>row[idIndex]).filter(Boolean),ariaRowCount:table.getAttribute('aria-rowcount')};};return {url:location.href,body:document.body.innerText.slice(0,1800),tables:tables.map(readTable)};`);

const assertExactIds = (actualIds, expectedIds, label) => {
  assert(actualIds.length > 0, `${label} rendered no source IDs`);
  assert.equal(new Set(actualIds).size, actualIds.length, `${label} rendered duplicate source IDs`);
  assert.deepEqual([...actualIds].sort(), [...expectedIds].sort(), `${label} source ID membership differs from the scoped CDA oracle`);
};

const readPublishedMaterialization = (publication) => {
  assert.equal(publication.state, 'ACTIVE', `Native Publish did not activate the revision: ${JSON.stringify(publication)}`);
  assert(publication.revisionId, 'Native Publish omitted its revision ID');
  assert.equal(publication.receiptId, report.preview.receiptId, 'Native Publish did not consume the current automatic preview receipt');
  assert.equal(publication.outputs?.length, 1, 'Native Publish returned an unexpected output set');
  const output = publication.outputs[0];
  assert.equal(output.outputId, report.seed.outputId, 'Published output identity differs from the authored table');
  assert.equal(output.state, 'READY', 'Published output is not materialized and ready');
  assert.match(output.materializationId, /^[A-Za-z0-9_-]{1,128}$/, 'Materialization identifier is not safe for a ClickHouse table name');
  assert.match(output.outputId, /^[A-Za-z0-9_-]{1,128}$/, 'Output identifier is not safe for a ClickHouse table name');

  const tableName = `loom_bundle_${output.materializationId.replaceAll('-', '')}_${output.outputId}`;
  assert.match(tableName, /^[A-Za-z0-9_]+$/, 'Generated ClickHouse table identifier contains unsafe characters');
  assert.match(clickhouseContainer, /^[A-Za-z0-9_.-]+$/, 'ClickHouse container name contains unsafe characters');
  const query = `SELECT * FROM \`loom_dev\`.\`${tableName}\` LIMIT 4 FORMAT JSONEachRow`;
  const raw = spawnSync('rtk', [
    'proxy', 'docker', 'exec', clickhouseContainer, 'clickhouse-client', '--query', query,
  ], { encoding: 'utf8', timeout: 30000, maxBuffer: 2_000_000 });
  assert.equal(raw.status, 0, raw.stderr || raw.stdout);
  const rows = raw.stdout.trim() ? raw.stdout.trim().split('\n').map((line) => JSON.parse(line)) : [];
  assert(rows.length > 0 && rows.length <= 3, `Bounded ClickHouse materialization has unexpected row count ${rows.length}`);
  const expected = new Set(sourceIds);
  const observed = [];
  for (const row of rows) {
    const matchingValues = Object.values(row).filter((value) => typeof value === 'string' && expected.has(value));
    assert.equal(matchingValues.length, 1, `Materialized row does not have exactly one expected Specimen ID: ${JSON.stringify(row)}`);
    observed.push(matchingValues[0]);
  }
  assertExactIds(observed, sourceIds, 'Independent ClickHouse read');
  report.materialization = {
    outputId: output.outputId,
    materializationId: output.materializationId,
    table: `loom_dev.${tableName}`,
    query,
    rows,
    sourceIds: observed,
  };
};

const verifyViewerAndReload = async () => {
  const viewerRowsReady = `([...document.querySelectorAll('[role="table"],table')].some(table=>{const headers=[...table.querySelectorAll('[role="columnheader"],thead th')];const index=headers.findIndex(cell=>cell.innerText.trim().toUpperCase()==='SPECIMEN ID');if(index<0)return false;const rows=[...table.querySelectorAll('[role="row"],tbody tr')].filter(row=>!row.querySelector('[role="columnheader"],th'));const ids=rows.map(row=>[...row.querySelectorAll('[role="cell"],td')][index]?.innerText.trim()).filter(Boolean);return JSON.stringify(ids.sort())===${JSON.stringify(JSON.stringify([...sourceIds].sort()))};}))`;
  const openedAt = Date.now();
  const viewerControl = await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(candidate=>candidate.textContent?.trim()==='Viewer');return {visible:Boolean(button&&button.offsetParent!==null),disabled:button?.disabled};`);
  assert(viewerControl.visible && !viewerControl.disabled, 'Native Viewer control is missing or disabled after publication');
  report.viewerControl = await click(browser.cdp, 'button', { name: 'Viewer' }, 1500);
  await waitForBrowser(browser.cdp, `new URL(location.href).searchParams.get('mode')==='viewer'&&new URL(location.href).searchParams.get('project')===${JSON.stringify(project)}&&new URL(location.href).searchParams.get('explorer')===${JSON.stringify(explorerId)}`, 5000);
  const currentViewerURL = await browserEval(browser.cdp, 'return location.href;');
  await waitForBrowser(browser.cdp, viewerRowsReady, 5000);
  const first = await viewerSnapshot();
  const table = first.tables.find((candidate) => candidate.idIndex >= 0);
  assert(table, 'Viewer has no published Specimen ID output table');
  assert.equal(first.tables.filter((candidate) => candidate.idIndex >= 0).length, 1, 'Viewer exposed unexpected extra Specimen ID tables');
  assertExactIds(table.specimenIds, sourceIds, 'Viewer');
  assert.equal(table.rows.length, sourceIds.length, 'Viewer contains extra or missing data rows');
  report.viewerURL = currentViewerURL;
  report.viewer = { first, exactMembership: true, reload: undefined };
  report.timingsMs.viewerOpen = Date.now() - openedAt;
  assert(report.timingsMs.viewerOpen <= 5000, `Native Viewer open and exact membership exceeded 5000 ms (${report.timingsMs.viewerOpen} ms)`);

  const reloadStarted = Date.now();
  await navigate(browser.cdp, currentViewerURL);
  await waitForBrowser(browser.cdp, viewerRowsReady, 5000);
  const reloaded = await viewerSnapshot();
  const reloadedTable = reloaded.tables.find((candidate) => candidate.idIndex >= 0);
  assert(reloadedTable, 'Reloaded Viewer has no published Specimen ID output table');
  assert.equal(reloaded.tables.filter((candidate) => candidate.idIndex >= 0).length, 1, 'Reloaded Viewer exposed unexpected extra Specimen ID tables');
  assertExactIds(reloadedTable.specimenIds, sourceIds, 'Reloaded Viewer');
  assert.equal(reloadedTable.rows.length, sourceIds.length, 'Reloaded Viewer contains extra or missing data rows');
  report.viewer.reload = { ...reloaded, exactMembership: true };
  report.timingsMs.viewerReload = Date.now() - reloadStarted;
  assert(report.timingsMs.viewerReload <= 5000, `Viewer reload and exact membership exceeded 5000 ms (${report.timingsMs.viewerReload} ms)`);
};

const main = async () => {
  await mkdir(evidenceDirectory, { recursive: true });
  await seedOwnedExplorer();
  browser = await launchBrowser(evidenceDirectory);
  await navigate(browser.cdp, 'about:blank');
  captureNativeProtocol(browser.cdp);
  if (apiToken) await browser.cdp.send('Network.setExtraHTTPHeaders', { headers: { Authorization: `Bearer ${apiToken}` } });

  const previewStarted = Date.now();
  await navigate(browser.cdp, pageURL);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[data-testid="construction-workspace"]'))`, 30000);
  await waitForBrowser(browser.cdp, `(()=>{const p=document.querySelector('[data-testid="construction-preview"]');return Boolean(p&&p.dataset.previewStatus==='ready'&&p.dataset.previewReceiptId&&p.dataset.previewOutputId===${JSON.stringify(outputId)}&&p.dataset.currentDraftVersion&&p.dataset.currentDraftDigest);})()`, 30000);
  report.preview = await previewSnapshot();
  report.timingsMs.automaticPreviewRender = Date.now() - previewStarted;
  assert(report.timingsMs.automaticPreviewRender <= 5000, `Automatic preview render exceeded 5000 ms (${report.timingsMs.automaticPreviewRender} ms)`);
  assert.equal(report.preview.status, 'ready', 'Native automatic preview is not ready');
  assert.equal(report.preview.outputId, outputId, 'Automatic preview targets a different output');
  assert(report.preview.receiptId, 'Automatic preview has no receipt');
  assert(report.preview.currentDraftDigest, 'Automatic preview has no current draft digest');
  assert(report.preview.headers.includes('SPECIMEN ID'), 'Automatic preview omitted the authored direct ID column');
  assert.equal(report.preview.rows.length, sourceIds.length, 'Automatic preview contains extra or missing rows');
  assertExactIds(report.preview.specimenIds, sourceIds, 'Native automatic preview');
  const previewProtocol = await waitForProtocolResponse('/preview', 5000);
  assert.equal(previewProtocol.status, 200, `Native automatic preview request failed: ${JSON.stringify(previewProtocol)}`);
  report.nativePreviewProtocol = previewProtocol;

  const publishButton = await browserEval(browser.cdp, `const button=[...document.querySelectorAll('button')].find(candidate=>candidate.textContent?.trim()==='Publish');return {disabled:button?.disabled,visible:Boolean(button)};`);
  assert(publishButton.visible && !publishButton.disabled, 'Publish is not enabled after the ready native preview');
  const publicationStarted = Date.now();
  report.publishClick = await click(browser.cdp, 'button', { name: 'Publish' }, 1500);
  const publishProtocol = await waitForProtocolResponse('/publish', 5000);
  assert.equal(publishProtocol.status, 200, `Native Publish request failed: ${JSON.stringify(publishProtocol)}`);
  const publication = publishProtocol.response;
  assert(publication && typeof publication === 'object', 'Native Publish response body is missing');
  report.publication = publication;
  await waitForBrowser(browser.cdp, `(()=>{const button=[...document.querySelectorAll('button')].find(candidate=>candidate.textContent?.trim()==='Publish');return Boolean(button&&button.getAttribute('aria-busy')!=='true'&&button.disabled);})()`, 5000);
  report.timingsMs.publicationFullAction = Date.now() - publicationStarted;
  assert(report.timingsMs.publicationFullAction <= 5000, `Publication full action exceeded 5000 ms (${report.timingsMs.publicationFullAction} ms)`);
  const materializationStarted = Date.now();
  readPublishedMaterialization(publication);
  report.timingsMs.independentClickHouseRead = Date.now() - materializationStarted;
  assert(report.timingsMs.independentClickHouseRead <= 5000, `Independent ClickHouse proof exceeded 5000 ms (${report.timingsMs.independentClickHouseRead} ms)`);

  await verifyViewerAndReload();
  await Promise.all(responseTasks);
  assert.equal(browser.dialogErrors.length, 0, `Unexpected browser dialogs: ${JSON.stringify(browser.dialogErrors)}`);
  assert.equal(report.errors.length, 0, `Unexpected browser protocol or runtime errors: ${JSON.stringify(report.errors)}`);
  report.dialogs = browser.dialogErrors;
  report.status = 'pass';
};

try {
  await main();
} catch (error) {
  fatal = error;
  report.status = 'fail';
  report.failure = { message: error instanceof Error ? error.message : String(error), stack: error instanceof Error ? error.stack : undefined };
  report.errors.push({ kind: 'fatal', message: report.failure.message });
} finally {
  if (browser) {
    await Promise.all(responseTasks);
    report.dialogs = browser.dialogErrors;
    await browser.close().catch((error) => report.errors.push({ kind: 'browser-close', message: String(error) }));
  }
  report.cleanup = {
    retainedOwnedExplorer: true,
    explorerId,
    originalExplorerTouched: false,
    reason: 'The verifier creates a unique Explorer and leaves it and its materialization available for evidence; no delete route is used.',
  };
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'cda-publication-browser.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({
    status: report.status,
    evidenceDirectory,
    explorerId,
    generation: datasetGeneration,
    sourceIds,
    outputId,
    timingsMs: report.timingsMs,
    failure: report.failure,
    originalExplorerTouched: false,
  }, null, 2));
}

if (fatal) process.exitCode = 1;
