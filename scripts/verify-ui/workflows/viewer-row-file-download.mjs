import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { isDeepStrictEqual } from 'node:util';
import { canonicalProjectID } from '../../loom-dev.mjs';
import { readViewerRowFileDownloadOracle } from '../helpers/viewer-row-file-download-oracle.mjs';
import { browserURL } from './builder-url.mjs';

const explorerID = 'default';
const outputID = 'files';
const fileColumn = 'file_id';
const downloadPath = '/__fixture-file-download';
const repositoryConfigPath = (project, generation) =>
  `/api/v1/projects/${encodeURIComponent(project)}/generations/${encodeURIComponent(generation)}/explorer-config`;
const runtimePath = project => `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorerID)}`;
const workspaceForFileDownload = () => ({
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderWorkspace',
  semanticsVersion: 10,
  explorer: { title: 'Basic Viewer per-row FileCell download' },
  documents: [{
    kind: 'ExplorerBuilderDocument',
    output: { id: outputID, title: 'File attachments', rowLabel: 'File' },
    rootResourceType: 'DocumentReference',
    route: { occurrenceId: 'base', resourceType: 'DocumentReference' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [{
      columnId: 'document-reference-file-id',
      column: fileColumn,
      label: 'File ID',
      logicalType: 'string',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
      table: { visible: true, order: 0, cellRenderer: 'fileActions' },
    }],
  }],
  tabs: [{ id: 'files-tab', title: 'Files', outputId: outputID, order: 0, visible: true }],
  fileActions: {
    extensions: { default: ['file_download'] },
    actions: { file_download: downloadPath },
  },
});

const matchesSelector = (actual, expected) => actual?.recipe === expected?.recipe &&
  actual?.translationVersion === expected?.translationVersion && actual?.output === expected?.output;

const matchesGraphRequest = (request, { origin, project, selector }) => {
  try {
    const url = new URL(request.url());
    const input = request.postDataJSON()?.variables?.input;
    return request.method() === 'POST' && url.origin === origin && url.pathname === '/graphql/graph' &&
      input?.projectId === project && input.selector?.output === outputID &&
      (!selector || matchesSelector(input.selector, selector)) &&
      Array.isArray(input.columns) && input.columns.join('\u0000') === fileColumn;
  } catch { return false; }
};

const assertOutputQuery = async (response, { project, origin, selector, oracle, check, phase }) => {
  const status = response.status();
  const request = response.request();
  const body = await response.json();
  const errors = Array.isArray(body.errors) ? body.errors : [];
  const connection = body.data?.dataframeRows;
  const columns = Array.isArray(connection?.columns) ? connection.columns : [];
  const rows = Array.isArray(connection?.rows) ? connection.rows : [];
  const rowIDs = Array.isArray(connection?.rowIds) ? connection.rowIds : [];
  const fileIDs = rows.map(row => Array.isArray(row) ? row[columns.indexOf(fileColumn)] : row?.[fileColumn]);
  const queryPassed = status >= 200 && status < 300 && errors.length === 0 &&
    matchesGraphRequest(request, { origin, project, selector }) &&
    isDeepStrictEqual(columns, [fileColumn]) && rowIDs.length === 1 &&
    rowIDs.every(rowID => typeof rowID === 'string' && rowID.length > 0) &&
    new Set(rowIDs).size === 1 && isDeepStrictEqual(fileIDs, oracle.fileIDs);
  check('correctness', `${phase} Viewer query returns exactly the independent DocumentReference UUID`, queryPassed,
    { status, errors, origin: new URL(request.url()).origin, projectId: request.postDataJSON()?.variables?.input?.projectId,
      selector: request.postDataJSON()?.variables?.input?.selector, columns, rowIDs, fileIDs, expectedFileIDs: oracle.fileIDs });
  assert(queryPassed, `${phase} Viewer output query must return the exact source DocumentReference UUID`);
};

const assertRenderedFileCell = async ({ page, href, check, phase }) => {
  await page.waitForFunction(({ expectedHref }) => {
    const tables = [...document.querySelectorAll('table[aria-label="Files results"]')];
    if (tables.length !== 1) return false;
    const rows = [...tables[0].querySelectorAll('tbody tr')];
    const links = [...tables[0].querySelectorAll('tbody a[aria-label="file_download"]')];
    return rows.length === 1 && links.length === 1 && links[0].href === expectedHref;
  }, { expectedHref: href }, { timeout: 5_000 });
  const table = page.getByRole('table', { name: 'Files results', exact: true });
  const rows = await table.locator('tbody tr').count();
  const links = table.locator('tbody a[aria-label="file_download"]');
  const linkCount = await links.count();
  const actualHref = linkCount === 1 ? await links.first().evaluate(anchor => anchor.href) : null;
  const passed = rows === 1 && linkCount === 1 && actualHref === href;
  check('correctness', `${phase} FileCell renders one link bound to the exact source identity`, passed,
    { rows, links: linkCount, href: actualHref, expectedHref: href });
  assert(passed, `${phase} FileCell must render one source-identity-bound download link`);
  return links.first();
};

const assertFileDownload = async ({ download, routeRequests, testInfo, check, oracle, phase }) => {
  const request = routeRequests.at(-1);
  const downloadFailure = await download.failure();
  const requestURL = request ? new URL(request.url) : undefined;
  const exactRequest = request?.method === 'GET' && requestURL?.origin === request.expectedOrigin &&
    requestURL.pathname === `${downloadPath}/${oracle.fileID}` && requestURL.search === '?redirect=true' && request.status === 200;
  check('correctness', `${phase} route request uses the exact same-origin DocumentReference download URL`, exactRequest,
    { request, expectedOrigin: request?.expectedOrigin, expectedPath: `${downloadPath}/${oracle.fileID}`,
      expectedQuery: '?redirect=true', downloadFailure });
  check('correctness', `${phase} per-row download uses the literal filename`,
    download.suggestedFilename() === oracle.downloadName,
    { expected: oracle.downloadName, actual: download.suggestedFilename() });

  const path = testInfo.outputPath(`${phase}.bin`);
  await download.saveAs(path);
  const bytes = readFileSync(path);
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  const exactBytes = downloadFailure === null && bytes.equals(oracle.payloadBytes) &&
    bytes.byteLength === oracle.payloadByteLength && sha256 === oracle.payloadSHA256;
  check('correctness', `${phase} per-row download bytes and SHA-256 match the literal fixture payload`, exactBytes,
    { expectedBytes: oracle.payloadByteLength, actualBytes: bytes.byteLength,
      expectedSHA256: oracle.payloadSHA256, actualSHA256: sha256, downloadFailure });
  assert(exactBytes, `${phase} FileCell download must preserve the exact literal fixture payload`);
};

export const viewerRowFileDownloadWorkflow = async ({ page, report, action, check, networkObserver }, context, testInfo) => {
  const target = context.target;
  const project = target.fixtureProject;
  const canonicalProject = canonicalProjectID(project);
  const generation = target.fixtureGeneration;
  const oracle = readViewerRowFileDownloadOracle(target.fixtureDir);
  const origin = new URL(target.uiUrl).origin;
  const expectedHref = new URL(`${downloadPath}/${encodeURIComponent(oracle.fileID)}?redirect=true`, origin).href;
  const workspace = workspaceForFileDownload();
  const sourceCommit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: target.sourceRoot, encoding: 'utf8' }).trim();
  const sourceHashBefore = oracle.documentReferenceSHA256;
  report.target.project = project;
  report.target.fixtureProjectAlias = project;
  report.target.canonicalProject = canonicalProject;
  report.target.explorer = explorerID;
  report.target.fixtureOracle = {
    documentReferencePath: oracle.documentReferencePath,
    documentReferenceSHA256: oracle.documentReferenceSHA256,
    documentReferenceIDs: oracle.fileIDs,
    payloadPath: oracle.payloadPath,
    payloadBytes: oracle.payloadByteLength,
    payloadSHA256: oracle.payloadSHA256,
    downloadName: oracle.downloadName,
  };
  check('correctness', 'exact source DocumentReference identity and literal fixture bytes are independently pinned',
    isDeepStrictEqual(oracle.fileIDs, ['7f6a8e2d-4c91-4b30-8a72-6e59d03c1f84']) &&
      oracle.fileID === '7f6a8e2d-4c91-4b30-8a72-6e59d03c1f84' && oracle.payloadByteLength === 41 &&
      oracle.payloadSHA256 === '7b50a05814262850f1356dd2ff00a8ddf628f916869ff53bcd5cfc6974f49726',
    { fileIDs: oracle.fileIDs, expectedFileIDs: [oracle.fileID], payloadBytes: oracle.payloadByteLength,
      expectedPayloadBytes: 41, payloadSHA256: oracle.payloadSHA256 });

  const configured = await fetch(`${target.apiUrl}${repositoryConfigPath(project, generation)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Loom-Source-Commit': sourceCommit },
    body: JSON.stringify(workspace),
    signal: AbortSignal.timeout(30_000),
  });
  const configuredText = await configured.text();
  let configuredBody;
  try { configuredBody = JSON.parse(configuredText); }
  catch { configuredBody = { body: configuredText.slice(0, 1_000) }; }
  const configuredPassed = configured.ok && configuredBody.project === canonicalProject &&
    configuredBody.generation === generation && configuredBody.explorerId === explorerID && configuredBody.activated === true;
  check('correctness', 'repository config publishes the FileCell Viewer to the exact fixture generation', configuredPassed,
    { status: configured.status, project: configuredBody.project, expectedProject: canonicalProject,
      generation: configuredBody.generation, expectedGeneration: generation, explorer: configuredBody.explorerId,
      sourceCommit });
  assert(configuredPassed, 'repository config must activate the FileCell output for the exact fixture generation');
  report.target.repositoryConfigSeed = { project, canonicalProject, generation, explorer: explorerID,
    sourceCommit, receiptId: configuredBody.receiptId, revisionId: configuredBody.revisionId };

  const routeRequests = [];
  await page.context().route('**/__fixture-file-download/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const exact = request.method() === 'GET' && url.origin === origin &&
      url.pathname === `${downloadPath}/${oracle.fileID}` && url.search === '?redirect=true';
    const evidence = { method: request.method(), url: request.url(), expectedOrigin: origin, status: exact ? 200 : 404 };
    routeRequests.push(evidence);
    await route.fulfill(exact ? {
      status: 200,
      headers: {
        'content-type': 'application/octet-stream',
        'content-disposition': `attachment; filename="${oracle.downloadName}"`,
      },
      body: oracle.payloadBytes,
    } : { status: 404, body: 'unexpected FileCell download identity' });
  });

  const runtimeAPIPath = runtimePath(project);
  const runtimeStart = networkObserver.requests.length;
  const runtimePromise = networkObserver.waitForResponse(entry => entry.method === 'GET' && entry.path === runtimeAPIPath,
    { fromIndex: runtimeStart, timeoutMs: 5_000 });
  const initialGraphPromise = page.waitForResponse(response => matchesGraphRequest(response.request(), { origin, project }),
    { timeout: 5_000 });
  await page.goto(browserURL(target, project, explorerID, 'viewer'), { waitUntil: 'domcontentloaded', timeout: 5_000 });
  const [runtimeEntry, initialGraphResponse] = await Promise.all([runtimePromise, initialGraphPromise]);
  const envelope = runtimeEntry.response;
  const runtime = envelope?.runtime;
  const output = runtime?.outputs?.find(candidate => candidate.outputId === outputID);
  const tableColumns = output?.table?.columns ?? [];
  const expectedFileAction = {
    extensions: { default: ['file_download'] },
    actions: { file_download: downloadPath },
  };
  const runtimePassed = runtimeEntry.status >= 200 && runtimeEntry.status < 300 &&
    envelope?.project === canonicalProject && envelope?.explorerId === explorerID &&
    (runtime?.generation ?? runtime?.publication?.generation) === generation &&
    runtime?.outputs?.length === 1 && output?.selector?.output === outputID &&
    isDeepStrictEqual(tableColumns.map(({ column, visible, cellRenderer }) => ({ column, visible, cellRenderer })),
      [{ column: fileColumn, visible: true, cellRenderer: 'fileActions' }]) &&
    isDeepStrictEqual(runtime?.fileActions, expectedFileAction);
  check('correctness', 'Viewer runtime retains the exact project, generation, output FileCell binding, and fileActions URL', runtimePassed,
    { status: runtimeEntry.status, project: envelope?.project, expectedProject: canonicalProject,
      explorer: envelope?.explorerId, generation: runtime?.generation ?? runtime?.publication?.generation,
      expectedGeneration: generation, outputId: output?.outputId, selector: output?.selector,
      tableColumns, fileActions: runtime?.fileActions, expectedFileActions: expectedFileAction });
  assert(runtimePassed, 'Viewer runtime must retain the exact FileCell binding and configured download URL');
  await assertOutputQuery(initialGraphResponse, { project, origin, selector: output.selector, oracle, check, phase: 'initial' });

  const link = await assertRenderedFileCell({ page, href: expectedHref, check, phase: 'initial' });
  const performDownload = async (phase, locator, label) => {
    const fromRouteIndex = routeRequests.length;
    const downloadPromise = page.waitForEvent('download', { timeout: 5_000 });
    await action(label, locator, () => locator.click(), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: `${phase} FileCell download action-to-render within five seconds`,
      after: async () => {
        const download = await downloadPromise;
        const currentRequests = routeRequests.slice(fromRouteIndex);
        const exactOneRequest = currentRequests.length === 1 && currentRequests[0].status === 200;
        check('correctness', `${phase} FileCell browser request resolves exactly one source file`, exactOneRequest,
          { requests: currentRequests, expectedCount: 1 });
        assert(exactOneRequest, `${phase} FileCell must request exactly one source file`);
        await assertFileDownload({ download, routeRequests: currentRequests, testInfo, check, oracle, phase });
      },
    });
  };
  await performDownload('first', link, 'download first per-row FileCell file');

  const reloadRuntimeStart = networkObserver.requests.length;
  const reloadRuntimePromise = networkObserver.waitForResponse(entry => entry.method === 'GET' && entry.path === runtimeAPIPath,
    { fromIndex: reloadRuntimeStart, timeoutMs: 5_000 });
  const reloadGraphPromise = page.waitForResponse(response => matchesGraphRequest(response.request(), {
    origin, project, selector: output.selector,
  }), { timeout: 5_000 });
  const outputTable = page.getByRole('table', { name: 'Files results', exact: true });
  let reloadedLink;
  await action('reload Viewer and restore exact FileCell row', outputTable,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: 5_000 }), {
      timeout: 5_000,
      budget: 5_000,
      requiredCheck: 'FileCell Viewer reload restores runtime, query, and visible link within five seconds',
      after: async () => {
        const [reloadRuntimeEntry, reloadGraphResponse] = await Promise.all([reloadRuntimePromise, reloadGraphPromise]);
        const reloadEnvelope = reloadRuntimeEntry.response;
        const reloadRuntime = reloadEnvelope?.runtime;
        const reloadOutput = reloadRuntime?.outputs?.find(candidate => candidate.outputId === outputID);
        const reloadBinding = reloadOutput?.table?.columns ?? [];
        const reloadPassed = reloadRuntimeEntry.status >= 200 && reloadRuntimeEntry.status < 300 &&
          reloadEnvelope?.project === canonicalProject && reloadEnvelope?.explorerId === explorerID &&
          (reloadRuntime?.generation ?? reloadRuntime?.publication?.generation) === generation &&
          matchesSelector(reloadOutput?.selector, output.selector) &&
          isDeepStrictEqual(reloadBinding.map(({ column, visible, cellRenderer }) => ({ column, visible, cellRenderer })),
            [{ column: fileColumn, visible: true, cellRenderer: 'fileActions' }]) &&
          isDeepStrictEqual(reloadRuntime?.fileActions, expectedFileAction);
        check('persistence', 'full Viewer reload retains the exact FileCell binding and fileActions URL', reloadPassed,
          { status: reloadRuntimeEntry.status, project: reloadEnvelope?.project,
            generation: reloadRuntime?.generation ?? reloadRuntime?.publication?.generation,
            outputId: reloadOutput?.outputId, selector: reloadOutput?.selector, tableColumns: reloadBinding,
            fileActions: reloadRuntime?.fileActions });
        assert(reloadPassed, 'full Viewer reload must restore the exact FileCell configuration');
        await assertOutputQuery(reloadGraphResponse, {
          project, origin, selector: output.selector, oracle, check, phase: 'reloaded',
        });
        reloadedLink = await assertRenderedFileCell({ page, href: expectedHref, check, phase: 'reloaded' });
      },
    });
  await performDownload('after-reload', reloadedLink, 'repeat per-row FileCell file download after reload');

  const finalOracle = readViewerRowFileDownloadOracle(target.fixtureDir);
  const unchanged = finalOracle.documentReferenceSHA256 === sourceHashBefore &&
    finalOracle.payloadSHA256 === oracle.payloadSHA256;
  check('correctness', 'independent DocumentReference and payload fixtures stay unchanged across reload and downloads', unchanged,
    { documentReferenceBefore: sourceHashBefore, documentReferenceAfter: finalOracle.documentReferenceSHA256,
      payloadBefore: oracle.payloadSHA256, payloadAfter: finalOracle.payloadSHA256 });
  assert(unchanged, 'FileCell lifecycle must not modify the independent source fixture');
};
