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

try {
  await navigate(browser.cdp, url);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('2 selected DocumentReference resources are ready to constrain this table.')`);
  await browserEval(browser.cdp, `clickButton('Use selected resources');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('2 DocumentReference resources constrain one row per DocumentReference.')`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
  await browserEval(browser.cdp, `clickButton('Preview');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('dev-file-001') && document.body.innerText.includes('dev-file-002')`, 60_000);
  const previewText = await browserEval(browser.cdp, `return document.body.innerText;`);
  assert.equal(previewText.includes('dev-file-003'), false, 'preview included a file outside the attached selection');
  await browser.cdp.send('Page.reload', { ignoreCache: true });
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('2 DocumentReference resources constrain one row per DocumentReference.')`, 60_000);
  await snapshot(browser.cdp, artifact);
  console.log(JSON.stringify({
    evidence: artifact,
    assertions: [
      'Builder loads the immutable selection handoff',
      'the DOM control attaches the selection',
      'Preview contains only selected file rows',
      'the attached population survives a full reload',
    ],
  }, null, 2));
} finally {
  await browser.close();
  rmSync(downloads, { recursive: true, force: true });
}
