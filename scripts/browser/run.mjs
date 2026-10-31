#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:http';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { mkdir, mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { dirname, join, resolve, extname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { tmpdir } from 'node:os';
import { CDPClient } from './cdp.mjs';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const fixture = join(root, 'ui/packages/loom-ui/testSupport/browser/fixture.tsx');
const uuid = '11111111-2222-4333-8444-555555555555';
const downloadPath = `/download/${uuid}?redirect=true`;
const configuredDownloadPath = `/download/${uuid}?source=viewer&redirect=true`;
const payload = Buffer.from('Loom browser download fixture\npatient-A\n');
const options = { feature: 'all', chrome: process.env.LOOM_E2E_CHROME || '/usr/bin/chromium' };
for (let i = 2; i < process.argv.length; i++) {
    if (process.argv[i] === '--doctor') options.doctor = true;
    else {
        const key = { '--feature': 'feature', '--out': 'out', '--chrome': 'chrome' }[process.argv[i]];
        assert.ok(key && process.argv[i + 1], `Unknown/missing option ${process.argv[i]}`);
        options[key] = process.argv[++i];
    }
}
assert.ok(['all', 'drag', 'download'].includes(options.feature), 'Unknown browser feature');
for (const file of [fixture, options.chrome, 'ui/node_modules/esbuild/lib/main.js', 'ui/node_modules/postcss/lib/postcss.mjs', 'ui/node_modules/tailwindcss/lib/index.js'])
    assert.ok(existsSync(resolve(root, file)), `Missing browser prerequisite: ${file}`);
const version = spawnSync(options.chrome, ['--version'], { encoding: 'utf8', timeout: 3000 });
assert.equal(version.status, 0, version.stderr);
if (options.doctor) {
    console.log(JSON.stringify({ status: 'ready-for-browser-component-checks', chrome: version.stdout.trim(), features: ['drag', 'download'], boundary: 'real production components in real Chromium; test-owned caller/data/download HTTP response, not deployed Loom/backend or disk durability' }));
    process.exit(0);
}
assert.ok(options.out, '--out is required');
const out = resolve(options.out);
assert.ok(!existsSync(out) || readdirSync(out).length === 0, 'Refusing to overwrite browser evidence');
await mkdir(out, { recursive: true });
function fingerprint() {
    const hash = createHash('sha256');
    function visit(directory) {
        for (const entry of readdirSync(join(root, directory), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
            const file = `${directory}/${entry.name}`;
            if (entry.isDirectory()) visit(file);
            else if (entry.isFile()) hash.update(file).update('\0').update(readFileSync(join(root, file))).update('\0');
        }
    }
    for (const path of ['ui/packages/loom-ui/src', 'ui/packages/loom-ui/testSupport/browser', 'scripts/browser']) visit(path);
    hash.update(readFileSync(join(root, 'ui/package-lock.json')));
    return hash.digest('hex');
}
const inputFingerprint = fingerprint();
const temp = await mkdtemp(join(tmpdir(), 'loom-browser-component-'));
const assets = join(temp, 'assets'), profile = join(temp, 'profile'), downloads = join(temp, 'downloads');
await mkdir(downloads);
let server, chrome, client, browser, stopBuild;
const started = performance.now(), requests = [], browserErrors = [];
const report = { schemaVersion: 1, status: 'running', feature: options.feature, sourceFingerprint: inputFingerprint, chrome: version.stdout.trim(), checks: [], boundary: 'Actual PreviewTable/OutputTable in Chromium with native input. Test-owned caller persists column settings in sessionStorage; controlled attachment endpoint. No BuilderWorkspace, real API/store, published Viewer integration or disk durability claim.' };
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(probe, message, milliseconds = 5000) {
    const deadline = performance.now() + milliseconds;
    while (performance.now() < deadline) {
        const value = await probe();
        if (value) return value;
        await wait(40);
    }
    assert.fail(`Browser condition failed before deadline: ${message}`);
}
async function evaluate(expression) {
    const result = await client.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    assert.ok(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
    return result.result.value;
}
async function position(selector) {
    return evaluate(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if(!e) return null; const b=e.getBoundingClientRect(); return {x:b.x+b.width/2,y:b.y+b.height/2,top:b.top,height:b.height,width:b.width}; })()`);
}
async function click(selector) {
    const point = await until(() => position(selector), `visible control ${selector}`);
    assert.ok(point.width > 0 && point.height > 0, `Invisible target ${selector}`);
    await client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
}
async function navigate(feature) {
    await client.send('Page.navigate', { url: `${report.origin}/?feature=${feature}` });
    await until(() => evaluate(`Boolean(document.querySelector(${JSON.stringify(feature === 'drag' ? '[data-testid="preview-table-scroll"]' : 'a[aria-label="file_download"]')}))`), 'production component mounted');
}
async function identities() {
    const result = [];
    for (const index of [1, 2]) {
        await click(`[aria-label="Inspect row ${index} identity"]`);
        result.push(await until(() => evaluate(`document.querySelector('[role="dialog"]')?.textContent`), 'row identity dialog'));
        await evaluate(`document.querySelector('[role="dialog"] button')?.click()`);
        await until(() => evaluate(`!document.querySelector('[role="dialog"]')`), 'identity dialog closed');
    }
    return result;
}
try {
    const { build, stop } = await import(pathToFileURL(join(root, 'ui/node_modules/esbuild/lib/main.js')));
    stopBuild = stop;
    await build({ absWorkingDir: root, entryPoints: [fixture], bundle: true, format: 'esm', splitting: true, outdir: assets, entryNames: 'app', jsx: 'automatic', logLevel: 'silent', define: { 'process.env.NODE_ENV': '"production"' }, nodePaths: [join(root, 'ui/node_modules')] });
    stop();
    stopBuild = undefined;
    const [{ default: postcss }, { default: tailwind }] = await Promise.all([
        import(pathToFileURL(join(root, 'ui/node_modules/postcss/lib/postcss.mjs'))),
        import(pathToFileURL(join(root, 'ui/node_modules/tailwindcss/lib/index.js'))),
    ]);
    const css = await postcss([tailwind({ content: [join(root, 'ui/packages/loom-ui/src/**/*.tsx'), fixture], theme: { extend: {} }, plugins: [] })]).process(await readFile(join(assets, 'app.css'), 'utf8'), { from: join(assets, 'app.css') });
    await writeFile(join(assets, 'app.css'), css.css);
    server = createServer(async (req, res) => {
        try {
            if (req.url.startsWith('/download/')) {
                requests.push({ method: req.method, url: req.url });
                if (![downloadPath, configuredDownloadPath].includes(req.url)) { res.writeHead(404); res.end('wrong attachment identity'); return; }
                res.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Disposition': 'attachment; filename="loom-browser-fixture.txt"', 'Content-Length': payload.length }); res.end(payload); return;
            }
            if (req.url.startsWith('/assets/')) {
                const relative = new URL(req.url, 'http://localhost').pathname.slice('/assets/'.length);
                assert.ok(relative && !relative.includes('..'));
                const bytes = await readFile(join(assets, relative));
                res.writeHead(200, { 'Content-Type': extname(relative) === '.css' ? 'text/css' : 'application/javascript' }); res.end(bytes); return;
            }
            res.writeHead(200, { 'Content-Type': 'text/html' });
            res.end('<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/assets/app.css"></head><body><div id="root"></div><script type="module" src="/assets/app.js"></script></body></html>');
        } catch (error) { res.writeHead(500); res.end(error.message); }
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    report.origin = `http://127.0.0.1:${server.address().port}`;
    chrome = spawn(options.chrome, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--window-size=1600,1000', '--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
    let chromeErrors = '';
    chrome.stderr.on('data', chunk => { chromeErrors += chunk; });
    const portFile = join(profile, 'DevToolsActivePort');
    await until(() => existsSync(portFile), 'Chromium DevTools startup', 8000);
    const [port, browserPath] = (await readFile(portFile, 'utf8')).trim().split('\n');
    browser = await CDPClient.connect(`ws://127.0.0.1:${port}${browserPath}`);
    await browser.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: downloads, eventsEnabled: true });
    const target = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' }).then(response => response.json());
    client = await CDPClient.connect(target.webSocketDebuggerUrl);
    client.on('Runtime.exceptionThrown', event => browserErrors.push(event.exceptionDetails?.exception?.description || event.exceptionDetails?.text));
    await client.send('Runtime.enable'); await client.send('Page.enable');
    if (['all', 'drag'].includes(options.feature)) {
        const before = performance.now(); await navigate('drag');
        const headers = () => evaluate(`Array.from(document.querySelectorAll('[role="columnheader"]'),e=>e.textContent.trim())`);
        assert.deepEqual(await headers(), ['First', 'Second']);
        const beforeHeaders = await headers();
        const originalIdentities = await identities();
        assert.ok(originalIdentities[0].includes('canonical-row-A') && originalIdentities[1].includes('canonical-row-B'));
        await click('[data-testid="preview-table-header-controls"] button');
        await evaluate(`window.__nativeDragEvents = []; for (const type of ['dragstart','dragover','drop','dragend']) document.addEventListener(type, event => window.__nativeDragEvents.push({type:event.type,trusted:event.isTrusted}), true);`);
        const source = await until(() => position('[aria-label="Drag Second"]'), 'drag handle');
        const destination = await position('[data-column-name="first"]');
        assert.ok(source.width > 0 && destination.height > 0);
        await client.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: source.x, y: source.y });
        await client.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: source.x, y: source.y, button: 'left', clickCount: 1 });
        for (let step = 1; step <= 15; step++) {
            await client.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: source.x + (destination.x - source.x) * step / 15, y: source.y + (destination.top + 3 - source.y) * step / 15, button: 'left', buttons: 1 }); await wait(15);
        }
        await client.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: destination.x, y: destination.top + 3, button: 'left', clickCount: 1 });
        await until(async () => JSON.stringify(await headers()) === '["Second","First"]', 'native drag updated actual headers');
        await click('[data-testid="preview-table-header-controls"] button');
        const afterHeaders = await headers();
        const afterIdentities = await identities();
        assert.deepEqual(afterIdentities, originalIdentities);
        const nativeEvents = await evaluate('window.__nativeDragEvents');
        assert.ok(nativeEvents.some(event => event.type === 'dragstart' && event.trusted), 'Native browser dragstart observed');
        assert.ok(nativeEvents.some(event => event.type === 'drop' && event.trusted), 'Native browser drop observed');
        await navigate('drag');
        assert.deepEqual(await headers(), ['Second', 'First']);
        const reloadedHeaders = await headers(), reloadedIdentities = await identities();
        assert.deepEqual(reloadedIdentities, originalIdentities);
        const screenshot = await client.send('Page.captureScreenshot', { format: 'png' });
        await writeFile(join(out, 'drag.png'), Buffer.from(screenshot.data, 'base64'));
        report.checks.push({ feature: 'drag', status: 'passed', beforeHeaders, afterHeaders, reloadedHeaders, originalIdentities, afterIdentities, reloadedIdentities, nativeEvents, persistence: 'test-owned sessionStorage presentation caller across component reload; not Loom API/store', seconds: (performance.now() - before) / 1000 });
    }
    if (['all', 'download'].includes(options.feature)) {
        const before = performance.now();
        const observedCases = [];
        for (const variant of [
            { id: 'default', query: '', href: downloadPath, request: downloadPath },
            { id: 'configured-query', query: '&downloadBase=query', href: `${configuredDownloadPath}#details`, request: configuredDownloadPath },
        ]) {
            for (const pass of [1, 2]) {
                await navigate(`download${variant.query}`);
                const actualHref = await evaluate(`document.querySelector('a[aria-label="file_download"]').getAttribute('href')`);
                assert.equal(actualHref, variant.href, `Exact ${variant.id} download URL`);
                await click('a[aria-label="file_download"]');
                const file = join(downloads, 'loom-browser-fixture.txt');
                await until(() => existsSync(file), 'exact browser attachment filename');
                const actual = await readFile(file);
                assert.equal(actual.length, 40, 'Exact literal download byte count');
                assert.equal(createHash('sha256').update(actual).digest('hex'), '9be39116627269ce2c87e8255cab99c7000a50a439e5533daa4302ca592407dc', 'Exact literal attachment SHA-256');
                await rm(file);
                observedCases.push({ variant: variant.id, afterReload: pass === 2, href: actualHref, bytes: actual.length });
                assert.equal(requests.length, observedCases.length, 'One owned request per download, including after reload');
                assert.deepEqual(requests.at(-1), { method: 'GET', url: variant.request });
            }
        }
        assert.deepEqual(requests, [downloadPath, downloadPath, configuredDownloadPath, configuredDownloadPath].map(url => ({ method: 'GET', url })));
        report.checks.push({ feature: 'download', status: 'passed', cases: observedCases, filename: 'loom-browser-fixture.txt', bytes: payload.length, sha256: createHash('sha256').update(payload).digest('hex'), requests: [...requests], seconds: (performance.now() - before) / 1000 });
    }
    assert.deepEqual(browserErrors, [], 'Browser runtime exceptions');
    assert.equal(fingerprint(), inputFingerprint, 'Browser verification sources changed during run');
    const screenshot = await client.send('Page.captureScreenshot', { format: 'png' });
    await writeFile(join(out, 'page.png'), Buffer.from(screenshot.data, 'base64'));
    report.status = 'passed';
} catch (error) {
    report.status = 'failed'; report.error = { message: error.message, stack: error.stack };
    process.exitCode = 1;
} finally {
    stopBuild?.();
    client?.close(); browser?.close();
    if (chrome && chrome.exitCode === null && chrome.signalCode === null) {
        chrome.kill('SIGTERM');
        await Promise.race([new Promise(resolve => chrome.once('exit', resolve)), wait(1000)]);
        if (chrome.exitCode === null && chrome.signalCode === null) {
            const stopped = new Promise(resolve => chrome.once('exit', resolve));
            chrome.kill('SIGKILL');
            await stopped;
        }
    }
    if (server) await new Promise(resolve => { server.closeAllConnections(); server.close(resolve); });
    try {
        await rm(temp, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
    } catch (error) {
        report.status = 'failed';
        report.cleanupError = error.message;
        process.exitCode = 1;
    }
    report.seconds = (performance.now() - started) / 1000; report.browserErrors = browserErrors;
    report.cleanup = { chromiumStopped: !chrome || chrome.exitCode !== null || chrome.signalCode !== null, fixtureServerStopped: !server || !server.listening, temporaryProfileRemoved: !existsSync(temp) };
    await writeFile(join(out, 'browser.json'), JSON.stringify(report, null, 2));
    console.log(JSON.stringify(report));
}
