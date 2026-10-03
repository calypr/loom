import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { browserEval, launchBrowser, navigate, snapshot, waitForBrowser } from './loom-dev.mjs';

assert.equal(process.argv.length, 3, 'usage: node scripts/verify-row-rebase-ui.mjs DEV_REPORT');
const report = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const target = report.target;
assert.ok(target?.apiUrl && target?.uiUrl && target?.project && target?.explorerId, 'development report has no browser target');

const authoring = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`;
const json = async (url, body) => {
  const response = await fetch(url, {
    ...(body === undefined ? {} : {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    }),
    signal: AbortSignal.timeout(30_000),
  });
  const value = await response.json();
  assert.ok(response.ok, `${url}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};

const before = await json(`${authoring}/builder`);
const document = before.workspace.documents[0];
assert.equal(document.rootResourceType, 'Patient', 'fixture journey must start with Patient rows');
assert.equal(document.route.children?.length, 1, 'fixture journey must have one direct child');
assert.equal(document.route.children[0].resourceType, 'Observation');
const columnKeys = document.columns.map((column) => column.column);
const filterKeys = document.columns.filter((column) => column.filter).map((column) => column.column);

const query = new URLSearchParams({
  project: target.project,
  explorer: target.explorerId,
  mode: 'builder',
});
const artifactRoot = join(process.cwd(), '.artifacts/loom-dev', `row-rebase-ui-${Date.now()}`);
const htmlPath = `${artifactRoot}.html`;
const reportPath = `${artifactRoot}.json`;
mkdirSync(dirname(htmlPath), { recursive: true });
const downloads = mkdtempSync(join(tmpdir(), 'loom-row-rebase-ui-'));
const browser = await launchBrowser(downloads);
const authoringResponses = [];
const browserFailures = [];
browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
  browserFailures.push(exceptionDetails.exception?.description ?? exceptionDetails.text ?? 'browser exception');
});
browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.url.includes('/authoring/v2/')) {
    authoringResponses.push(`${response.status} ${new URL(response.url).pathname}`);
  }
  const url = new URL(response.url);
  if (response.status >= 400 && url.pathname !== '/favicon.ico') {
    browserFailures.push(`${response.status} ${response.url}`);
  }
});

try {
  await navigate(browser.cdp, `${target.uiUrl}/?${query}`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[aria-label="Make each Observation one row"]'))`, 60_000);
  await browserEval(browser.cdp, `clickButton('Make each Observation one row');`);
  await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[aria-label="Make each Patient one row"]'))`, 60_000);

  const after = await json(`${authoring}/builder`);
  const rebased = after.workspace.documents.find((candidate) => candidate.output.id === document.output.id);
  assert.ok(rebased, 'rebased table disappeared');
  assert.equal(after.draftVersion, before.draftVersion + 1, 'row change must create exactly one draft version');
  assert.equal(rebased.rootResourceType, 'Observation');
  assert.equal(rebased.route.resourceType, 'Observation');
  assert.equal(rebased.route.children?.[0]?.resourceType, 'Patient');
  assert.deepEqual(rebased.columns.map((column) => column.column), columnKeys, 'row change replaced stable feature keys');
  assert.deepEqual(
    rebased.columns.filter((column) => column.filter).map((column) => column.column),
    filterKeys,
    'row change lost configured filters',
  );

  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
  await browserEval(browser.cdp, `clickButton('Preview');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('dev-patient-001')`, 60_000);
  assert.equal(browserFailures.length, 0, `browser failures: ${browserFailures.join(' | ')}`);
  await snapshot(browser.cdp, htmlPath);

  const evidence = {
    status: 'passed',
    scenario: 'builder-row-rebase',
    target: {
      composeProject: target.composeProject,
      apiUrl: target.apiUrl,
      uiUrl: target.uiUrl,
      project: target.project,
      explorerId: target.explorerId,
    },
    before: { draftVersion: before.draftVersion, rootResourceType: document.rootResourceType, columnKeys, filterKeys },
    after: { draftVersion: after.draftVersion, rootResourceType: rebased.rootResourceType, columnKeys: rebased.columns.map((column) => column.column), filterKeys: rebased.columns.filter((column) => column.filter).map((column) => column.column) },
    assertions: [
      'Builder exposes an existing direct child as a row-start choice',
      'the browser assessment and apply requests both succeed',
      'Observation becomes the row root with Patient rebased beneath it',
      'stable feature keys and the configured filter survive the rebase',
      'Preview succeeds after the row change',
    ],
    authoringResponses,
    evidencePaths: [htmlPath],
  };
  writeFileSync(reportPath, JSON.stringify(evidence, null, 2), { mode: 0o600 });
  console.log(JSON.stringify({ evidence: reportPath, assertions: evidence.assertions }, null, 2));
} catch (error) {
  await snapshot(browser.cdp, htmlPath);
  const reason = error instanceof Error ? error.message : String(error);
  throw new Error(`${reason}; authoring responses: ${authoringResponses.slice(-12).join(' | ') || 'none'}; browser failures: ${browserFailures.slice(-5).join(' | ') || 'none'}`);
} finally {
  await browser.close();
  rmSync(downloads, { recursive: true, force: true });
}
