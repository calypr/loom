import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { browserEval, click, inspectAction, launchBrowser } from './lib/browser.mjs';

const evidence = process.argv[2] ?? '/tmp/loom-browser-disclosure';
await mkdir(evidence, { recursive: true });
const browser = await launchBrowser(evidence);
try {
  const { frameTree } = await browser.cdp.send('Page.getFrameTree');
  await browser.cdp.send('Page.setDocumentContent', {
    frameId: frameTree.frame.id,
    html: '<!doctype html><details id="advanced" open><summary>Advanced</summary><fieldset><input aria-label="Column name" value="Original"></fieldset></details>',
  });
  await browserEval(browser.cdp, `await new Promise(resolve=>requestAnimationFrame(resolve));document.querySelector('#advanced').open=false;`);
  const hidden = await inspectAction(browser.cdp, 'input');
  assert.equal(hidden.visible, false, 'A control inside collapsed details must be identified as hidden');
  assert.equal(hidden.closedDisclosure?.summary, 'Advanced');
  await assert.rejects(click(browser.cdp, 'input'), /closedDisclosure/);
  await click(browser.cdp, 'summary', { name: 'Advanced' });
  const visible = await inspectAction(browser.cdp, 'input');
  assert.equal(visible.visible, true);
  assert.equal(visible.closedDisclosure, null);
  await click(browser.cdp, 'input');
  await browserEval(browser.cdp, 'document.activeElement.select();');
  await browser.cdp.send('Input.insertText', { text: 'Renamed' });
  assert.equal(await browserEval(browser.cdp, 'return document.querySelector("input").value;'), 'Renamed');
  console.log(JSON.stringify({ status: 'passed', evidence, checks: ['collapsed control hidden', 'hidden click rejected', 'summary actionable', 'opened control native typing'] }));
} finally {
  await browser.close();
}
