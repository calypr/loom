import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

export async function verifyRowRebaseUI({ page, cda, devReport }) {
  const target = devReport?.target ?? {
    apiUrl: cda.apiUrl,
    uiUrl: cda.uiUrl,
    project: cda.project,
    explorerId: cda.explorer,
    composeProject: cda.target.composeProject,
  };
  assert.ok(target?.apiUrl && target?.uiUrl && target?.project && target?.explorerId,
    'development report or owned CDA fixture has no browser target');
  const apiOrigin = new URL(target.apiUrl).origin;
  const uiOrigin = new URL(target.uiUrl).origin;
  assert.equal(target.project, cda.project, 'row rebase report must target the explicitly owned project');
  assert.equal(target.composeProject, cda.target.composeProject, 'row rebase report targets a different Compose project');
  const authoring = `${apiOrigin}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`;
  const json = async url => {
    const response = await fetch(url, { signal: AbortSignal.timeout(30_000) });
    const value = await response.json();
    assert.ok(response.ok, `${url}: ${response.status} ${JSON.stringify(value)}`);
    return value;
  };

  const before = await json(`${authoring}/builder`);
  const document = before.workspace.documents[0];
  assert.equal(document.rootResourceType, 'Patient', 'fixture journey must start with Patient rows');
  assert.equal(document.route.children?.length, 1, 'fixture journey must have one direct child');
  assert.equal(document.route.children[0].resourceType, 'Observation');
  const columnKeys = document.columns.map(column => column.column);
  const filterKeys = document.columns.filter(column => column.filter).map(column => column.column);
  const query = new URLSearchParams({ project: target.project, explorer: target.explorerId, mode: 'builder' });
  const artifactRoot = join(cda.evidence, `row-rebase-ui-${Date.now()}`);
  const htmlPath = `${artifactRoot}.html`;
  const screenshotPath = process.env.LOOM_VERIFY_SCREENSHOTS === '1' ? `${artifactRoot}.png` : undefined;
  await mkdir(cda.evidence, { recursive: true });
  const evidence = { nativeRequests: [], errors: [], target: cda.target };
  const requestByPlaywrightRequest = new Map();
  const requestPrefix = `/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`;
  page.on('request', request => {
    const url = new URL(request.url());
    if (url.origin !== apiOrigin && url.origin !== uiOrigin) return;
    if (!url.pathname.startsWith(requestPrefix)) return;
    let body;
    try { body = request.postDataJSON(); } catch { body = undefined; }
    const entry = { path: url.pathname, method: request.method(), body, startedAt: Date.now() };
    evidence.nativeRequests.push(entry);
    requestByPlaywrightRequest.set(request, entry);
  });
  page.on('response', response => {
    const entry = requestByPlaywrightRequest.get(response.request());
    if (entry) entry.status = response.status();
  });
  page.on('pageerror', error => evidence.errors.push({ kind: 'page-error', message: error.message }));
  page.on('console', message => {
    if (message.type() === 'error') evidence.errors.push({ kind: 'console', message: message.text() });
  });
  page.on('requestfailed', request => evidence.errors.push({ kind: 'network', url: request.url(), error: request.failure()?.errorText }));
  const authoringResponses = () => evidence.nativeRequests.filter(request => request.path.startsWith(requestPrefix))
    .map(request => `${request.status ?? 'pending'} ${request.path}`);
  const action = async (selector, name) => cda.action(name, page.locator(selector), target => target.click());
  const output = {
    status: 'running', scenario: 'builder-row-rebase', target: {
      composeProject: target.composeProject, apiUrl: apiOrigin, uiUrl: uiOrigin,
      project: target.project, explorerId: target.explorerId,
    }, assertions: [],
  };
  try {
    await cda.navigate(`${uiOrigin}/?${query}`);
    await cda.wait(() => Boolean(document.querySelector('[aria-label="Make each Observation one row"]')), [], 5_000);
    const rebaseRequestStart = evidence.nativeRequests.length;
    const rebaseStarted = Date.now();
    await action('[aria-label="Make each Observation one row"]', 'Make each Observation one row');
    await cda.wait(() => Boolean(document.querySelector('[aria-label="Make each Patient one row"]')), [], 5_000);
    const rebaseDurationMs = Date.now() - rebaseStarted;
    assert(rebaseDurationMs <= 5000, `Row rebase took ${rebaseDurationMs} ms to render the restored Patient choice`);
    const rebaseRequests = evidence.nativeRequests.slice(rebaseRequestStart);
    assert(rebaseRequests.some(request => request.path.endsWith('/row-change') && request.status === 200),
      'row change assessment request did not succeed');
    assert(rebaseRequests.some(request => request.path.endsWith('/commands') && request.status === 200),
      'row change Apply request did not succeed');

    const after = await json(`${authoring}/builder`);
    const rebased = after.workspace.documents.find(candidate => candidate.output.id === document.output.id);
    assert.ok(rebased, 'rebased table disappeared');
    assert.equal(after.draftVersion, before.draftVersion + 1, 'row change must create exactly one draft version');
    assert.equal(rebased.rootResourceType, 'Observation');
    assert.equal(rebased.route.resourceType, 'Observation');
    assert.equal(rebased.route.children?.[0]?.resourceType, 'Patient');
    assert.deepEqual(rebased.columns.map(column => column.column), columnKeys, 'row change replaced stable feature keys');
    assert.deepEqual(rebased.columns.filter(column => column.filter).map(column => column.column), filterKeys,
      'row change lost configured filters');

    await cda.wait(() => [...document.querySelectorAll('button')]
      .some(button => button.textContent.trim() === 'Preview' && !button.disabled), [], 5_000);
    const previewStart = evidence.nativeRequests.length;
    const previewStarted = Date.now();
    await action('button', 'Preview');
    await cda.wait(() => document.body.innerText.includes('dev-patient-001'), [], 5_000);
    const previewDurationMs = Date.now() - previewStarted;
    assert(previewDurationMs <= 5000, `Preview took ${previewDurationMs} ms to render the CDA Patient`);
    assert(evidence.nativeRequests.slice(previewStart)
      .some(request => request.path.endsWith('/preview') && request.status === 200),
    'Preview request did not succeed after the row change');
    assert.deepEqual(evidence.errors, [], `browser failures: ${JSON.stringify(evidence.errors)}`);
    if (screenshotPath) await page.screenshot({ path: screenshotPath, fullPage: true });
    await writeFile(htmlPath, await page.content(), { mode: 0o600 });

    output.status = 'passed';
    output.before = { draftVersion: before.draftVersion, rootResourceType: document.rootResourceType, columnKeys, filterKeys };
    output.after = { draftVersion: after.draftVersion, rootResourceType: rebased.rootResourceType,
      columnKeys: rebased.columns.map(column => column.column),
      filterKeys: rebased.columns.filter(column => column.filter).map(column => column.column) };
    output.timingsMs = { rowRebase: rebaseDurationMs, preview: previewDurationMs };
    output.assertions = [
      'Builder exposes an existing direct child as a row-start choice',
      'the browser assessment and apply requests both succeed',
      'Observation becomes the row root with Patient rebased beneath it',
      'stable feature keys and the configured filter survive the rebase',
      'Preview succeeds after the row change',
    ];
    output.authoringResponses = authoringResponses();
    output.evidencePaths = [htmlPath, ...(screenshotPath ? [screenshotPath] : [])];
    cda.check('correctness', 'row rebase preserves stable feature keys and filters', true,
      { columnKeys, filterKeys, draftVersion: after.draftVersion });
    return output;
  } catch (error) {
    output.status = 'failed';
    output.error = String(error.stack ?? error);
    output.authoringResponses = authoringResponses();
    output.browserErrors = evidence.errors;
    throw new Error(`${error instanceof Error ? error.message : String(error)}; authoring responses: ${authoringResponses().slice(-12).join(' | ') || 'none'}; browser failures: ${JSON.stringify(evidence.errors.slice(-5))}`);
  } finally {
    await writeFile(`${artifactRoot}-failure.json`, JSON.stringify({ evidence, requests: evidence.nativeRequests }, null, 2), { mode: 0o600 }).catch(() => undefined);
    await cda.attachReport('row-rebase-ui', output);
  }
}
