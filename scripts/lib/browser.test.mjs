import test from 'node:test';
import assert from 'node:assert/strict';
import { launchBrowser, evaluate, click, navigate, waitForBrowser } from './browser.mjs';

test('native clicks wait for moving controls and reject overlays and disabled fieldsets', async () => {
  const browser = await launchBrowser('/private/tmp');
  try {
    const html = '<html><body><button id="target">Action</button><fieldset disabled><button id="disabled">Disabled</button></fieldset><div id="overlay" style="position:fixed;inset:0;z-index:100"></div></body></html>';
    await browser.cdp.send('Page.navigate', { url: 'data:text/html;base64,' + Buffer.from(html).toString('base64') });
    await waitForBrowser(browser.cdp, "document.querySelector('#target')", 5000);
    await evaluate(browser.cdp, "document.querySelector('#target').addEventListener('click',()=>document.body.dataset.clicked='yes')");
    await assert.rejects(click(browser.cdp, '#target', {}, 100), /not actionable/);
    assert.equal(await evaluate(browser.cdp, "document.body.dataset.clicked"), undefined);
    await evaluate(browser.cdp, "document.querySelector('#overlay').remove()");
    await assert.rejects(click(browser.cdp, '#disabled'), /not actionable/);
    await evaluate(browser.cdp, "document.querySelector('#target').animate([{transform:'translateX(0px)'},{transform:'translateX(200px)'}],{duration:350,fill:'forwards'})");
    await click(browser.cdp, '#target');
    assert.equal(await evaluate(browser.cdp, "document.body.dataset.clicked"), 'yes');
    assert.ok(await evaluate(browser.cdp, "document.querySelector('#target').getBoundingClientRect().left >= 200"));
  } finally { await browser.close(); }
});

test('navigation waits for a new document when reloading the same URL', async () => {
  const browser = await launchBrowser('/private/tmp');
  try {
    const url = 'data:text/html;base64,' + Buffer.from('<html><body>Loaded</body></html>').toString('base64');
    await navigate(browser.cdp, url);
    await evaluate(browser.cdp, "document.body.dataset.stale='yes'");
    await navigate(browser.cdp, url);
    assert.equal(await evaluate(browser.cdp, 'document.body.dataset.stale'), undefined);
    assert.equal(await evaluate(browser.cdp, 'document.body.innerText'), 'Loaded');
  } finally { await browser.close(); }
});

test('native clicks scroll dialog controls nested inside graph nodes', async () => {
  const browser = await launchBrowser('/private/tmp');
  try {
    const html = '<html><body><div class="react-flow"><div class="react-flow__node"><div style="position:fixed;inset:0;overflow:auto"><div role="dialog" style="min-height:1600px"><button id="target" style="margin-top:1400px">Coverage</button></div></div></div></div></body></html>';
    await navigate(browser.cdp, 'data:text/html;base64,' + Buffer.from(html).toString('base64'));
    await evaluate(browser.cdp, "document.querySelector('#target').addEventListener('click',()=>document.body.dataset.clicked='yes')");
    await evaluate(browser.cdp, "setTimeout(()=>document.querySelector('[role=dialog]').parentElement.scrollTop=0,100)");
    await click(browser.cdp, '#target');
    assert.equal(await evaluate(browser.cdp, "document.body.dataset.clicked"), 'yes');
  } finally { await browser.close(); }
});

test('native clicks recover when dialog scrolling clips a control inside the viewport', async () => {
  const browser = await launchBrowser('/private/tmp');
  try {
    const html = '<html><body><section role="dialog" style="position:fixed;top:50px;width:500px;height:100px;overflow:hidden"><div id="scroll" style="height:100px;overflow:auto"><button id="target" style="margin-top:180px;margin-bottom:200px">Action</button></div></section></body></html>';
    await navigate(browser.cdp, 'data:text/html;base64,' + Buffer.from(html).toString('base64'));
    await evaluate(browser.cdp, "document.querySelector('#target').addEventListener('click',()=>document.body.dataset.clicked='yes');setTimeout(()=>document.querySelector('#scroll').scrollTop=0,100)");
    await click(browser.cdp, '#target', {}, 1000);
    assert.equal(await evaluate(browser.cdp, 'document.body.dataset.clicked'), 'yes');
  } finally { await browser.close(); }
});
