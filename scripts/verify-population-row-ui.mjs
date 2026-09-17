import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';
import { browserEval, launchBrowser, navigate, snapshot, waitForBrowser } from './loom-dev.mjs';

assert.equal(process.argv.length, 3, 'usage: node scripts/verify-population-row-ui.mjs SELECTION_EVIDENCE');
const evidence = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const selection = evidence.selections?.explicit;
assert.ok(selection?.id, 'selection evidence has no explicit revision');
assert.ok(evidence.target?.uiUrl && evidence.target?.project && evidence.explorerId, 'selection evidence has no browser target');

const query = new URLSearchParams({
  project: evidence.target.project,
  explorer: evidence.explorerId,
  mode: 'builder',
  selection: selection.id,
});
const url = `${evidence.target.uiUrl}/?${query}`;
const artifact = join(process.cwd(), '.artifacts/loom-dev', `population-row-ui-${Date.now()}.html`);
mkdirSync(dirname(artifact), { recursive: true });
const downloads = mkdtempSync(join(tmpdir(), 'loom-population-ui-'));
const browser = await launchBrowser(downloads);
const browserFailures = [];
const requestURLs = new Map();
browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
  browserFailures.push(exceptionDetails.exception?.description ?? exceptionDetails.text ?? 'browser exception');
});
browser.cdp.on('Network.requestWillBeSent', ({ requestId, request }) => {
  requestURLs.set(requestId, request.url);
});
browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.status >= 400) browserFailures.push(`${response.status} ${response.url}`);
});
browser.cdp.on('Network.loadingFailed', ({ requestId, errorText, blockedReason }) => {
  const detail = blockedReason ? `${errorText} (${blockedReason})` : errorText;
  browserFailures.push(`${detail} ${requestURLs.get(requestId) ?? 'unknown request'}`);
});

try {
  await navigate(browser.cdp, url);
  await snapshot(browser.cdp, artifact);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('3 selected DocumentReference resources are ready to constrain this table.') || [...document.querySelectorAll('button')].some((button) => button.textContent.trim() === 'Use all authorized rows')`);
  const alreadyAttached = await browserEval(browser.cdp, `return Boolean(buttonByName('Use all authorized rows'));`);
  if (alreadyAttached) {
    await browserEval(browser.cdp, `clickButton('Use all authorized rows');`);
    await waitForBrowser(browser.cdp, `document.body.innerText.includes('3 selected DocumentReference resources are ready to constrain this table.')`);
  }
  await browserEval(browser.cdp, `clickButton('Use selected resources');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('3 DocumentReference resources constrain one row per DocumentReference.')`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
  await browserEval(browser.cdp, `clickButton('Preview');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('dev-file-001') && document.body.innerText.includes('dev-file-002')`, 60_000);
  const previewText = await browserEval(browser.cdp, `return document.body.innerText;`);
  assert.equal(previewText.includes('dev-file-003'), false, 'preview included a file outside the attached selection');
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Check selected-resource coverage' && !button.disabled))`);
  await browserEval(browser.cdp, `clickButton('Check selected-resource coverage');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('3 selected · 2 produce rows · 1 needs attention')`, 60_000);
  const reportText = await browserEval(browser.cdp, `return document.body.innerText;`);
  assert.equal(reportText.includes('DocumentReference/dev-file-004'), true, 'coverage report omitted the bounded unmatched resource');
  await browser.cdp.send('Page.reload', { ignoreCache: true });
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('3 DocumentReference resources constrain one row per DocumentReference.')`, 60_000);
  const reloadedText = await browserEval(browser.cdp, `return document.body.innerText;`);
  assert.equal(reloadedText.includes('3 selected · 2 produce rows · 1 needs attention'), false, 'reload displayed stale coverage evidence');
  await snapshot(browser.cdp, artifact);
  console.log(JSON.stringify({
    evidence: artifact,
    assertions: [
      'Builder loads the immutable selection handoff',
      'the DOM control attaches the selection',
      'Preview contains only selected file rows',
      'coverage report maps two files and returns only unlinked file 004',
      'reload clears stale coverage evidence',
      'the attached population survives a full reload',
    ],
  }, null, 2));
} catch (error) {
  await snapshot(browser.cdp, artifact);
  const reason = error instanceof Error ? error.message : String(error);
  throw new Error(`${reason}; browser failures: ${browserFailures.slice(-5).join(' | ') || 'none'}`);
} finally {
  await browser.close();
  rmSync(downloads, { recursive: true, force: true });
}
