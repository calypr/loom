import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import {
  assertOwnedTarget, captureRequests, click, includeBrowserDiagnostics, launchBrowser, navigate, waitForBrowser,
} from './lib/cda-playwright.mjs';

assert.equal(process.argv.length, 3, 'usage: node scripts/verify-row-rebase-ui.mjs DEV_REPORT');
const devReportPath = process.argv[2];
const devReport = JSON.parse(await readFile(devReportPath, 'utf8'));
const target = devReport.target;
assert.ok(target?.apiUrl && target?.uiUrl && target?.project && target?.explorerId, 'development report has no browser target');
const apiOrigin = new URL(target.apiUrl).origin;
const uiOrigin = new URL(target.uiUrl).origin;
const ownership = await assertOwnedTarget({ project: target.project, apiOrigin, uiOrigin, arangoContainer: process.env.LOOM_ARANGO_CONTAINER });
assert.equal(target.composeProject, ownership.composeProject, 'development report targets a different Compose project');
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
const artifactRoot = join(process.cwd(), '.artifacts/loom-dev', `row-rebase-ui-${Date.now()}`);
const evidenceDirectory = artifactRoot;
const htmlPath = `${artifactRoot}.html`;
const screenshotPath = `${artifactRoot}.png`;
const reportPath = `${artifactRoot}.json`;
await mkdir(dirname(htmlPath), { recursive: true });
const browser = await launchBrowser(evidenceDirectory, undefined, { noAuth: true, apiOrigin, uiOrigin });
const evidence = { nativeRequests: [], errors: [], target: ownership };
const requestPrefix = `/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`;
const browserEvents = captureRequests(browser, evidence, requestPrefix, { apiOrigin, uiOrigin, responsePaths: /commands|row-change|preview/ });
const page = browser.page;
const authoringResponses = () => evidence.nativeRequests.filter(request => request.path.startsWith(requestPrefix)).map(request => `${request.status ?? 'pending'} ${request.path}`);
const action = async (selector, name) => click(page, selector, { name });

try {
  await navigate(page, `${uiOrigin}/?${query}`);
  await waitForBrowser(page, () => Boolean(document.querySelector('[aria-label="Make each Observation one row"]')), [], 60_000);
  const rebaseRequestStart = evidence.nativeRequests.length;
  const rebaseStarted = Date.now();
  await action('[aria-label="Make each Observation one row"]', 'Make each Observation one row');
  await waitForBrowser(page, () => Boolean(document.querySelector('[aria-label="Make each Patient one row"]')), [], 60_000);
  const rebaseDurationMs = Date.now() - rebaseStarted;
  assert(rebaseDurationMs <= 5000, `Row rebase took ${rebaseDurationMs} ms to render the restored Patient choice`);
  await browserEvents.flush();
  const rebaseRequests = evidence.nativeRequests.slice(rebaseRequestStart);
  const assessment = rebaseRequests.find(request => request.path.endsWith('/row-change') && request.status === 200);
  assert(assessment, 'row change assessment request did not succeed');
  const applyRequest = rebaseRequests.find(request => request.path.endsWith('/commands') && request.status === 200);
  assert(applyRequest, 'row change Apply request did not succeed');

  const after = await json(`${authoring}/builder`);
  const rebased = after.workspace.documents.find(candidate => candidate.output.id === document.output.id);
  assert.ok(rebased, 'rebased table disappeared');
  assert.equal(after.draftVersion, before.draftVersion + 1, 'row change must create exactly one draft version');
  assert.equal(rebased.rootResourceType, 'Observation');
  assert.equal(rebased.route.resourceType, 'Observation');
  assert.equal(rebased.route.children?.[0]?.resourceType, 'Patient');
  assert.deepEqual(rebased.columns.map(column => column.column), columnKeys, 'row change replaced stable feature keys');
  assert.deepEqual(rebased.columns.filter(column => column.filter).map(column => column.column), filterKeys, 'row change lost configured filters');

  await waitForBrowser(page, () => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === 'Preview' && !button.disabled), [], 60_000);
  const previewStart = evidence.nativeRequests.length;
  const previewStarted = Date.now();
  await action('button', 'Preview');
  await waitForBrowser(page, () => document.body.innerText.includes('dev-patient-001'), [], 60_000);
  const previewDurationMs = Date.now() - previewStarted;
  assert(previewDurationMs <= 5000, `Preview took ${previewDurationMs} ms to render the CDA Patient`);
  await browserEvents.flush();
  assert(evidence.nativeRequests.slice(previewStart).some(request => request.path.endsWith('/preview') && request.status === 200), 'Preview request did not succeed after the row change');
  includeBrowserDiagnostics(browser, evidence);
  assert.equal(evidence.errors.length, 0, `browser failures: ${JSON.stringify(evidence.errors)}`);
  assert.deepEqual(browser.diagnostics.pageErrors, [], 'browser exceptions occurred');
  assert.deepEqual(browser.diagnostics.console, [], 'browser console errors occurred');
  assert.deepEqual(browser.diagnostics.networkFailures, [], 'browser network requests failed');
  assert.deepEqual(browser.diagnostics.httpFailures.filter(failure => new URL(failure.url).pathname !== '/favicon.ico'), [], 'browser HTTP request failed');
  await page.screenshot({ path: screenshotPath, fullPage: true });
  await writeFile(htmlPath, await page.content(), { mode: 0o600 });

  const output = {
    status: 'passed',
    scenario: 'builder-row-rebase',
    target: {
      composeProject: target.composeProject,
      apiUrl: apiOrigin,
      uiUrl: uiOrigin,
      project: target.project,
      explorerId: target.explorerId,
    },
    before: { draftVersion: before.draftVersion, rootResourceType: document.rootResourceType, columnKeys, filterKeys },
    after: { draftVersion: after.draftVersion, rootResourceType: rebased.rootResourceType, columnKeys: rebased.columns.map(column => column.column), filterKeys: rebased.columns.filter(column => column.filter).map(column => column.column) },
    timingsMs: { rowRebase: rebaseDurationMs, preview: previewDurationMs },
    assertions: [
      'Builder exposes an existing direct child as a row-start choice',
      'the browser assessment and apply requests both succeed',
      'Observation becomes the row root with Patient rebased beneath it',
      'stable feature keys and the configured filter survive the rebase',
      'Preview succeeds after the row change',
    ],
    authoringResponses: authoringResponses(),
    evidencePaths: [htmlPath, screenshotPath],
  };
  await writeFile(reportPath, JSON.stringify(output, null, 2), { mode: 0o600 });
  console.log(JSON.stringify({ evidence: reportPath, assertions: output.assertions }, null, 2));
} catch (error) {
  includeBrowserDiagnostics(browser, evidence);
  await browser.captureFailure(error, { phase: 'row-rebase-ui', project: target.project, explorer: target.explorerId, draftVersion: before.draftVersion });
  const reason = error instanceof Error ? error.message : String(error);
  throw new Error(`${reason}; authoring responses: ${authoringResponses().slice(-12).join(' | ') || 'none'}; browser failures: ${JSON.stringify(evidence.errors.slice(-5))}`);
} finally {
  await browserEvents.flush();
  includeBrowserDiagnostics(browser, evidence);
  await writeFile(`${artifactRoot}-failure.json`, JSON.stringify({ evidence: evidenceDirectory, diagnostics: browser.diagnostics, requests: evidence.nativeRequests }, null, 2), { mode: 0o600 }).catch(() => undefined);
  await browser.close();
}
