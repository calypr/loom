import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const sourcePath = new URL('../../scripts/demo-builder-resource-smoke.mjs', import.meta.url);
const source = await readFile(sourcePath, 'utf8');

const runWithFakes = async ({
  failInitialEvidence = false,
  rejectServerClose = false,
  hangDOM = '',
  verificationFailure = '',
} = {}) => {
  const oldArgv = process.argv;
  const state = { browserClosed: false, serverCloseCalled: false, kills: [], writes: [], contentCalls: 0, evidenceAfterTeardown: true };
  const child = {
    exitCode: null,
    signalCode: null,
    once(event, listener) { this.listener = listener; return this; },
    kill(signal) { state.kills.push(signal); this.signalCode = signal; this.listener?.(); return true; },
  };
  globalThis.__smokeProbeState = state;
  globalThis.__smokeProbeChild = child;
  globalThis.__smokeProbeOptions = { failInitialEvidence, rejectServerClose, hangDOM, verificationFailure };
  const fakes = source
    .replace("import { mkdir, writeFile } from 'node:fs/promises';", `const state = globalThis.__smokeProbeState; const child = globalThis.__smokeProbeChild; const options = globalThis.__smokeProbeOptions; const mkdir = async () => {}; const writeFile = async (path, value) => { if (path.endsWith('.html') && (!state.browserClosed || !state.serverCloseCalled)) state.evidenceAfterTeardown = false; state.writes.push([path, value]); if (options.failInitialEvidence && path.endsWith('/builder-initial.html')) throw new Error('simulated initial evidence failure'); };`)
    .replace("import { chromium } from 'playwright';", `const chromium = {
      launchServer: async () => ({ process: () => child, wsEndpoint: () => 'ws://test', close: async () => { state.serverCloseCalled = true; if (options.rejectServerClose) throw new Error('simulated server close failure'); child.exitCode = 0; } }),
      connect: async () => ({ newPage: async () => ({ on() {}, goto: async () => {}, content: async () => { state.contentCalls += 1; if ((options.hangDOM === 'initial' && state.contentCalls === 1) || (options.hangDOM === 'final' && state.contentCalls === 2)) return new Promise(() => {}); return '<main>Builder</main>'; } }), close: async () => { state.browserClosed = true; } }),
    };`)
    .replace("import { verifyBuilderResourceChoices } from './demo-builder-resource-check.mjs';", `const verifyBuilderResourceChoices = async () => { if (globalThis.__smokeProbeOptions.verificationFailure) throw new Error(globalThis.__smokeProbeOptions.verificationFailure); };`)
    .replace('const DOM_CAPTURE_TIMEOUT_MS = 5_000;', 'const DOM_CAPTURE_TIMEOUT_MS = 0;');
  process.argv = ['node', 'probe', 'http://example.invalid', 'chrome', '/evidence', 'Patient'];
  try {
    await import(`data:text/javascript;base64,${Buffer.from(fakes).toString('base64')}#${Math.random()}`);
    return { state };
  } catch (error) {
    return { state, error };
  } finally {
    process.argv = oldArgv;
    delete globalThis.__smokeProbeState;
    delete globalThis.__smokeProbeChild;
    delete globalThis.__smokeProbeOptions;
  }
};

test('server close rejection falls back to terminating the owned browser process', async () => {
  const { state, error } = await runWithFakes({ rejectServerClose: true });
  assert.equal(error, undefined);
  assert.equal(state.browserClosed, true);
  assert.equal(state.serverCloseCalled, true);
  assert.deepEqual(state.kills, ['SIGTERM']);
});

test('evidence write failure remains primary while browser and server teardown still run', async () => {
  const { state, error } = await runWithFakes({ failInitialEvidence: true, rejectServerClose: true });
  assert.match(error.message, /simulated initial evidence failure/);
  assert.equal(state.browserClosed, true);
  assert.equal(state.serverCloseCalled, true);
  assert.deepEqual(state.kills, ['SIGTERM']);
  const report = state.writes.find(([path]) => path.endsWith('builder-smoke.json'))?.[1];
  assert.match(report, /simulated initial evidence failure/);
});

test('hanging initial DOM capture is bounded and browser teardown still completes', async () => {
  const { state, error } = await runWithFakes({ hangDOM: 'initial' });
  assert.match(error.message, /Capture initial Builder DOM timed out/);
  assert.equal(state.browserClosed, true);
  assert.equal(state.serverCloseCalled, true);
});

test('hanging final DOM capture is bounded, teardown precedes evidence writes, and primary failure stays primary', async () => {
  const { state, error } = await runWithFakes({ hangDOM: 'final', verificationFailure: 'primary verification failure' });
  assert.match(error.message, /primary verification failure/);
  assert.equal(state.browserClosed, true);
  assert.equal(state.serverCloseCalled, true);
  assert.equal(state.evidenceAfterTeardown, true);
  const report = state.writes.find(([path]) => path.endsWith('builder-smoke.json'))?.[1];
  assert.match(report, /Capture final Builder DOM timed out/);
  const lastDOMCapture = state.contentCalls;
  const browserCloseWriteIndex = state.writes.findIndex(([path]) => path.endsWith('builder.html'));
  assert.equal(browserCloseWriteIndex >= 0, true);
  assert.equal(lastDOMCapture, 2);
});
