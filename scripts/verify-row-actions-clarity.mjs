import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';

const project = 'loom_dev_cda_fhir';
const explorer = process.env.LOOM_QA_EXPLORER;
assert(explorer && ['group-related-summary-browser-', 'cohort-add-fields-browser-'].some(prefix => explorer.startsWith(prefix)), 'Supply an owned group-related-summary-browser or cohort-add-fields-browser QA Explorer');
const evidence = process.argv[2] ?? `/tmp/loom-row-actions-clarity-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
await mkdir(evidence, { recursive: true });
const sourceFreezeStartedAt = new Date().toISOString();
const sourceFreeze = await captureSourceFreeze(fileURLToPath(new URL('..', import.meta.url)));
const read = async () => {
  const response = await fetch(`${apiOrigin}/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/builder`);
  assert(response.ok, `Builder read returned ${response.status}`);
  return response.json();
};
const before = await read();
const outputId = before.workspace.documents[0].output.id;
const report = {
  explorer,
  outputId,
  cases: [],
  transitions: [],
  workspaceChecks: [],
  errors: [],
  sourceFreeze: { startedAt: sourceFreezeStartedAt, watchedFileCount: sourceFreeze.watchedFileCount },
};
let browser;
try {
  browser = await launchBrowser(evidence);
  await browser.cdp.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false });
  browser.cdp.on('Runtime.exceptionThrown', event => report.errors.push({ kind: 'runtime', details: event.exceptionDetails }));
  browser.cdp.on('Network.responseReceived', ({ response }) => { if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) report.errors.push({ kind: 'http', status: response.status, url: response.url }); });
  const timedTransition = async (name, action, settledWhen) => {
    const started = Date.now();
    try {
      await action();
      await waitForBrowser(browser.cdp, settledWhen, 5000);
    } catch (error) {
      const durationMs = Date.now() - started;
      report.transitions.push({ name, durationMs, limitMs: 5000, status: 'failed', error: String(error) });
      throw new Error(`${name} did not render within 5000 ms (observed ${durationMs} ms): ${error.message}`);
    }
    const durationMs = Date.now() - started;
    const status = durationMs <= 5000 ? 'passed' : 'failed';
    report.transitions.push({ name, durationMs, limitMs: 5000, status });
    assert.equal(status, 'passed', `${name} took ${durationMs} ms to render (limit 5000 ms)`);
  };
  const assertWorkspaceUnchanged = async phase => {
    const current = await read();
    assert.deepEqual(current.workspace, before.workspace, `${phase}: opening and closing row actions must not save workspace changes`);
    assert.equal(current.draftVersion, before.draftVersion, `${phase}: opening and closing row actions must not advance the draft version`);
    assert.equal(current.draftDigest, before.draftDigest, `${phase}: opening and closing row actions must not change the draft digest`);
    report.workspaceChecks.push({
      phase,
      unchanged: true,
      draftVersion: current.draftVersion,
      draftDigest: current.draftDigest,
    });
  };
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${outputId}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${outputId}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`);
  const inspectCards = async name => {
    const device = name.startsWith('mobile') ? 'mobile' : 'desktop';
    await timedTransition(
      `${device}-row-actions-open`,
      () => click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]'),
      `document.querySelector('[data-testid="construction-action-related-rows"]')?.getClientRects().length>0`,
    );
    const relatedState = await browserEval(browser.cdp, `const button=document.querySelector('[data-testid="construction-action-related-rows"]');return {disabled:button.disabled,text:button.innerText};`);
    assert(!relatedState.disabled, `Related row action is disabled: ${relatedState.text}`);
    const cards = await browserEval(browser.cdp, `return ['group','pivot','related','unpivot'].map(kind=>{ const button=document.querySelector('[data-testid="construction-action-'+kind+'-rows"]'); const bounds=button.getBoundingClientRect(); return {kind,text:button.innerText,visible:bounds.width>0&&bounds.left>=0&&bounds.right<=innerWidth}; });`);
    assert(cards.every(card => card.visible), JSON.stringify(cards));
    const related = cards.find(card => card.kind === 'related');
    assert(related.text.includes('Make a row for each related record'));
    assert(related.text.toLowerCase().includes('existing values') || related.text.toLowerCase().includes('original values'));
    assert(related.text.includes('keep a current row once when no records match'));
    assert(related.text.includes('By default'));
    assert(cards.find(card => card.kind === 'group').text.includes('Combine rows into groups'));
    assert(cards.find(card => card.kind === 'unpivot').text.includes('Other columns repeat on each new row'));
    report.dialogLayout = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"][aria-label="Row definition settings"]');return [dialog,dialog.parentElement,...Array.from((function*(){let node=dialog.parentElement.parentElement;while(node){yield node;node=node.parentElement;}})())].map(node=>({tag:node.tagName,className:node.className,position:getComputedStyle(node).position,zIndex:getComputedStyle(node).zIndex,transform:getComputedStyle(node).transform,overflow:getComputedStyle(node).overflow,top:node.getBoundingClientRect().top}));`);
    const shot = await browser.cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
    await writeFile(join(evidence, `${name}.png`), Buffer.from(shot.data, 'base64'));
    report.cases.push({ name, cards });
  };
  await inspectCards('desktop-actions');
  await timedTransition(
    'desktop-related-editor-open',
    () => click(browser.cdp, '[data-testid="construction-action-related-rows"]'),
    `document.querySelector('[data-testid="construction-related-expand-editor"]')?.getClientRects().length>0`,
  );
  const editor = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-related-expand-editor"]').innerText;`);
  assert(editor.includes('Make a row for each related record'));
  assert(editor.includes('Start from'));
  const startControl = await browserEval(browser.cdp, `const editor=document.querySelector('[data-testid="construction-related-expand-editor"]');const label=[...editor.querySelectorAll('label,div')].find(node=>node.innerText.trim().startsWith('Start from'));return {present:!!label,hiddenInDetails:!!label?.closest('details'),text:label?.innerText};`);
  assert(startControl.present, 'Starting records must be explained in the main form');
  assert(!startControl.hiddenInDetails, 'Starting-record choice must not be hidden in Advanced options');
  report.startControl = startControl;
  const noMatchControl = await browserEval(browser.cdp, `const editor=document.querySelector('[data-testid="construction-related-expand-editor"]');const select=[...editor.querySelectorAll('select')].find(node=>[...node.options].some(option=>option.value==='PRESERVE_PARENT'));return {present:!!select,hiddenInDetails:!!select?.closest('details'),value:select?.value};`);
  assert(noMatchControl.present, 'The no-match choice must be available');
  assert(!noMatchControl.hiddenInDetails, 'The no-match outcome must not require opening Advanced options');
  assert.equal(noMatchControl.value, 'PRESERVE_PARENT');
  report.noMatchControl = noMatchControl;
  assert(editor.includes('What will change'));
  assert(editor.includes('Rows before and after'));
  assert(editor.includes('Patient A + Observation 1'));
  assert(editor.includes('Patient A + Observation 2'));
  assert(editor.includes('Fields you can add'));
  assert(editor.includes('changes which records those fields come from'));
  report.cases.push({ name: 'related-editor-explanation', editor });
  await assertWorkspaceUnchanged('desktop editor open');
  await timedTransition(
    'desktop-related-editor-close',
    () => click(browser.cdp, '[data-testid="construction-close-operation-editor"]'),
    `(!document.querySelector('[data-testid="construction-related-expand-editor"]')||document.querySelector('[data-testid="construction-related-expand-editor"]').getClientRects().length===0)&&document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`,
  );
  await browser.cdp.send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true });
  await inspectCards('mobile-actions');
  await timedTransition(
    'mobile-related-editor-open',
    () => click(browser.cdp, '[data-testid="construction-action-related-rows"]'),
    `document.querySelector('[data-testid="construction-related-expand-editor"]')?.getClientRects().length>0`,
  );
  const mobileEditor = await browserEval(browser.cdp, `return document.querySelector('[data-testid="construction-related-expand-editor"]').innerText;`);
  assert(mobileEditor.includes('What will change'));
  assert(mobileEditor.includes('Rows before and after'));
  assert(mobileEditor.includes('Fields you can add'));
  report.cases.push({ name: 'mobile-related-action', editor: mobileEditor });
  await assertWorkspaceUnchanged('mobile editor open');
  await timedTransition(
    'mobile-related-editor-close',
    () => click(browser.cdp, '[data-testid="construction-close-operation-editor"]'),
    `(!document.querySelector('[data-testid="construction-related-expand-editor"]')||document.querySelector('[data-testid="construction-related-expand-editor"]').getClientRects().length===0)&&document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled===false`,
  );
  await assertWorkspaceUnchanged('mobile editor closed');
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  await browser?.close();
  const sourceFreezeFinishedAt = new Date().toISOString();
  try {
    report.sourceFreeze = {
      ...report.sourceFreeze,
      ...(await sourceFreeze.assertUnchanged()),
      finishedAt: sourceFreezeFinishedAt,
    };
  } catch (error) {
    if (!error.invalidatesRun) throw error;
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.sourceFreeze = {
      ...report.sourceFreeze,
      unchanged: false,
      changedPaths: error.changedPaths ?? [],
      invalidatesRun: true,
      productFailure: false,
      finishedAt: sourceFreezeFinishedAt,
    };
    process.exitCode = 1;
  }
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
console.log(JSON.stringify({ status: report.status, evidence, cases: report.cases.map(value => value.name), transitions: report.transitions, error: report.error }));
