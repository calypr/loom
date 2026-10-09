import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import test from 'node:test';
import { builderAuthoringWorkflow } from '../playwright-authoring.mjs';
import { installNativeAbortProbe } from '../native-abort-probe.mjs';

test('Builder authoring caller installs its exact fixture scope before the first navigation', async () => {
  const calls = [];
  const browserContext = {
    async exposeBinding(name, callback) {
      calls.push({ kind: 'binding', name, callback });
    },
    async addInitScript(script) {
      calls.push({ kind: 'init-script', script });
    },
  };
  const sentinel = new Error('stop at the first navigation');
  const page = {
    setDefaultTimeout(timeout) { calls.push({ kind: 'default-timeout', timeout }); },
    setDefaultNavigationTimeout(timeout) { calls.push({ kind: 'navigation-timeout', timeout }); },
    context: () => browserContext,
    async evaluate(script) {
      calls.push({ kind: 'current-document', script });
    },
    async goto(url, options) {
      calls.push({ kind: 'goto', url, options });
      throw sentinel;
    },
  };
  const project = 'loom_dev_verify_authoring_caller';
  const origin = 'http://127.0.0.1:30008';
  const target = {
    uiUrl: origin,
    fixtureProject: project,
    fixtureGeneration: 'fixture-v1',
    fixtureDir: resolve(dirname(fileURLToPath(import.meta.url)), '../../../../testdata/verify-combine'),
    bootstrapExplorerId: 'loom-dev-bootstrap',
  };
  const report = { target: {}, requiredChecks: [], timings: {}, network: [], errors: [], assertions: [] };

  await assert.rejects(builderAuthoringWorkflow({
    page,
    report,
    action: async () => { throw new Error('no user action should run before first navigation'); },
    check: () => { throw new Error('no check should run before first navigation'); },
  }, { target, runID: 'caller-regression-0123456789' }), error => error === sentinel);

  const setup = calls.filter(({ kind }) => ['binding', 'init-script', 'current-document'].includes(kind));
  const navigation = calls.find(({ kind }) => kind === 'goto');
  assert.deepEqual(setup.map(({ kind }) => kind), ['binding', 'init-script', 'current-document']);
  assert(navigation, 'the real caller must reach its first page navigation');
  assert(calls.indexOf(navigation) > calls.indexOf(setup.at(-1)),
    'the actual goto must happen after binding, future-document script, and current-document install');
  assert.equal(setup[0].name, '__loomNativeAbortProbeBinding');
  assert(setup[1].script.includes(`project: "${project}"`));
  assert(setup[1].script.includes('explorerScope: "project-routes"'));
  assert(setup[1].script.includes(`apiOrigin: "${origin}"`));
  assert.doesNotMatch(setup[1].script, /bootstrapExplorerId/,
    'the probe must capture the later selected Explorer rather than claim the bootstrap Explorer');
  const navigationURL = new URL(navigation.url);
  assert.equal(navigationURL.origin, origin);
  assert.equal(navigationURL.searchParams.get('project'), project);
  assert.equal(navigationURL.searchParams.get('explorer'), target.bootstrapExplorerId);
  assert.equal(report.target.fixtureOracle.project, project);
  assert.equal(report.nativeAbortProbeEvents.length, 0);
});

test('shared installer attaches to current and future documents in exact-scope order', async () => {
  const calls = [];
  const browserContext = {
    async exposeBinding(name, callback) {
      calls.push({ kind: 'binding', name, callback });
    },
    async addInitScript(script) {
      calls.push({ kind: 'init-script', script });
    },
  };
  const page = {
    context: () => browserContext,
    async evaluate(script) {
      calls.push({ kind: 'current-document', script });
    },
  };
  const report = {};
  const project = 'loom_dev_verify_installer';
  const origin = 'http://127.0.0.1:30008';

  await installNativeAbortProbe({ page, report, project, explorer: { mode: 'project-routes' }, apiOrigin: origin });

  assert.deepEqual(calls.map(({ kind }) => kind), ['binding', 'init-script', 'current-document']);
  assert.equal(calls[0].name, '__loomNativeAbortProbeBinding');
  assert(calls[1].script.includes(`project: "${project}"`));
  assert(calls[1].script.includes('explorerScope: "project-routes"'));
  assert(calls[1].script.includes(`apiOrigin: "${origin}"`));
  assert.doesNotMatch(calls[1].script, /bootstrapExplorerId/);
  assert.deepEqual(report.nativeAbortProbeEvents, []);
});
