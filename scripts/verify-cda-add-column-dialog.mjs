import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, launchBrowser, navigate, waitForBrowser } from './loom-dev.mjs';

const explorerId = process.argv[2] ?? 'cda-builder-full-qa-1790440983382';
const origin = (process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008').replace(/\/$/, '');
const url = `${origin}/?project=loom_dev_cda_fhir&explorer=${explorerId}&mode=builder`;
const evidenceDirectory = join('.artifacts', 'cda-builder', new Date().toISOString().replaceAll(':', '-'));
const browser = await launchBrowser('/private/tmp');
const evidence = { url, clicks: [], dialogs: [], errors: [], responses: [] };

browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => {
  evidence.errors.push(exceptionDetails?.text ?? 'Browser exception');
});
browser.cdp.on('Network.responseReceived', ({ response }) => {
  if (response.url.includes('/authoring/v2/')) {
    evidence.responses.push({ path: new URL(response.url).pathname, status: response.status });
  }
});

const pointerClick = async (selector, label) => {
  const point = await browserEval(browser.cdp, `const button=document.querySelector(${JSON.stringify(selector)});if(!button)throw new Error('Button missing');button.scrollIntoView({block:'center'});const rect=button.getBoundingClientRect();return {x:rect.x+rect.width/2,y:rect.y+rect.height/2,disabled:button.disabled};`);
  assert.equal(point.disabled, false, `${label} is disabled`);
  await browser.cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  await browser.cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  evidence.clicks.push(label);
};

try {
  await navigate(browser.cdp, url);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('DATASET WORKSPACE')`, 30_000);
  await waitForBrowser(browser.cdp, `document.querySelectorAll('[data-testid^="paired-column-suggestion-"]').length>=3`, 30_000);
  const suggestions = await browserEval(browser.cdp, `return [...document.querySelectorAll('[data-testid^="paired-column-suggestion-"]')].map(button=>({testId:button.dataset.testid,label:button.getAttribute('aria-label'),disabled:button.disabled}));`);
  assert(suggestions.length >= 3, 'The current CDA table has fewer than three ready coded values');

  for (const suggestion of suggestions) {
    assert.equal(suggestion.disabled, false, `${suggestion.label} is disabled`);
    await pointerClick(`[data-testid="${suggestion.testId}"]`, suggestion.label);
    await waitForBrowser(browser.cdp, `Boolean(document.querySelector('[role="dialog"]'))`, 10_000);
    const dialog = await browserEval(browser.cdp, `const dialog=document.querySelector('[role="dialog"]');const rect=dialog.getBoundingClientRect();return {text:dialog.innerText.slice(0,240),parent:dialog.parentElement?.parentElement?.tagName,rect:{x:rect.x,y:rect.y,width:rect.width,height:rect.height},viewport:{width:innerWidth,height:innerHeight},routeChoices:dialog.querySelectorAll('input[type=radio]').length};`);
    assert(dialog.text.includes('Choose how to add these fields'), `${suggestion.label} opened an invisible dialog`);
    assert.equal(dialog.parent, 'BODY', `${suggestion.label} dialog is trapped in a disclosure`);
    assert(dialog.rect.width > 0 && dialog.rect.height > 0 && dialog.rect.y >= 0 && dialog.rect.y < dialog.viewport.height, `${suggestion.label} dialog is outside the viewport`);
    assert(dialog.routeChoices > 0, `${suggestion.label} has no selectable route`);
    evidence.dialogs.push({ suggestion: suggestion.label, ...dialog });
    await browserEval(browser.cdp, `const cancel=[...document.querySelectorAll('[role="dialog"] button')].find(button=>button.textContent?.trim()==='Cancel');cancel.click();return true;`);
    await waitForBrowser(browser.cdp, `!document.querySelector('[role="dialog"]')`, 10_000);
    evidence.clicks.push('Cancel dialog');
  }
  assert.deepEqual(evidence.errors, [], 'Browser exceptions occurred');
  assert(evidence.responses.every(({ status }) => status < 400), 'An authoring request failed');
} finally {
  await mkdir(evidenceDirectory, { recursive: true });
  await writeFile(join(evidenceDirectory, 'add-column-dialogs.json'), JSON.stringify(evidence, null, 2));
  await browser.close();
  console.log(JSON.stringify({ evidenceDirectory, clicks: evidence.clicks, dialogs: evidence.dialogs.length, errors: evidence.errors }, null, 2));
}
