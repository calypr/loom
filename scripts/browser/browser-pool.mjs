import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

export async function bounded(promise, label, ms = 5000) {
  const controller = new AbortController();
  try {
    return await Promise.race([promise, delay(ms, undefined, { signal: controller.signal }).then(() => { throw new Error(`${label} timed out after ${ms}ms`); })]);
  } finally { controller.abort(); }
}

export async function openBrowserPool({ engine, concurrency, chrome, lightpanda, nativeInput = false }) {
  const { chromium } = await import('playwright');
  const resources = [], workers = [];
  const started = performance.now();
  async function openBrowser() {
    const resource = { stderr: '' };
    resources.push(resource);
    if (engine === 'chromium') {
      resource.browser = await chromium.launch({ headless: true, ...(chrome ? { executablePath: chrome } : {}), timeout: 10000 });
    } else {
      const reservation = createServer();
      await new Promise((resolve, reject) => { reservation.once('error', reject); reservation.listen(0, '127.0.0.1', resolve); });
      const port = reservation.address().port;
      await new Promise(resolve => reservation.close(resolve));
      resource.cache = await mkdtemp(join(tmpdir(), 'loom-lightpanda-cache-'));
      const child = resource.child = spawn(lightpanda, ['serve', '--host', '127.0.0.1', '--port', String(port),
        '--http-cache-dir', resource.cache, ...(nativeInput ? ['--load-resources', 'stylesheet'] : [])], { stdio: ['ignore', 'ignore', 'pipe'] });
      let spawnError;
      child.on('error', error => { spawnError = error; });
      child.stderr.on('data', bytes => { resource.stderr += bytes; });
      const deadline = performance.now() + 10000;
      let ready = false;
      while (performance.now() < deadline) {
        if (spawnError) throw spawnError;
        assert.equal(child.exitCode, null, `Lightpanda exited: ${resource.stderr}`);
        try { ready = (await fetch(`http://127.0.0.1:${port}/json/version`, { signal: AbortSignal.timeout(500) })).ok; } catch { }
        if (ready) break;
        await delay(10);
      }
      assert.ok(ready, `Lightpanda startup timed out: ${resource.stderr}`);
      resource.browser = await chromium.connectOverCDP(`ws://127.0.0.1:${port}`, { timeout: 10000 });
    }
    return resource.browser;
  }
  async function close() {
    const errors = [];
    const closed = await Promise.allSettled(resources.map(async resource => {
      try { if (resource.browser) await bounded(resource.browser.close(), 'browser cleanup'); }
      catch (error) { errors.push(error.message); }
      const child = resource.child;
      if (child && child.pid && child.exitCode === null && child.signalCode === null) {
        const exited = new Promise(resolve => child.once('exit', resolve));
        child.kill('SIGTERM');
        try { await bounded(exited, 'Lightpanda shutdown', 2000); }
        catch {
          child.kill('SIGKILL');
          try { await bounded(exited, 'Lightpanda forced shutdown'); } catch (error) { errors.push(error.message); }
        }
      }
      if (resource.cache) {
        try { await rm(resource.cache, { recursive: true, force: true }); } catch (error) { errors.push(error.message); }
      }
    }));
    for (const result of closed) if (result.status === 'rejected') errors.push(String(result.reason));
    return { browserConnected: resources.some(resource => resource.browser?.isConnected()),
      lightpandaStopped: resources.every(({ child }) => !child?.pid || child.exitCode !== null || child.signalCode !== null), errors };
  }
  try {
    const chromiumBrowser = engine === 'chromium' ? await openBrowser() : undefined;
    const ready = await Promise.allSettled(Array.from({ length: concurrency }, async (_, id) => {
      const browser = chromiumBrowser ?? await openBrowser();
      const context = await bounded(browser.newContext({ viewport: { width: 1440, height: 1000 } }), 'new worker context');
      const page = await bounded(context.newPage(), 'new worker page');
      page.setDefaultTimeout(5000); page.setDefaultNavigationTimeout(10000);
      workers[id] = { id, engine, browser, context, page };
    }));
    const failure = ready.find(result => result.status === 'rejected');
    if (failure) throw failure.reason;
    return { workers, close, startupMs: performance.now() - started,
      version: workers[0].browser.version(), topology: engine === 'chromium' ? 'one-browser-isolated-contexts' : 'independent-processes',
      logs: () => resources.map(resource => resource.stderr).join('\n') };
  } catch (error) { await close(); throw error; }
}
