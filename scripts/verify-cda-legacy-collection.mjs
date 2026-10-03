import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { browserEval, click, launchBrowser, navigate, waitForBrowser } from './lib/browser.mjs';

// Replay an owned, pre-direction-field QA Explorer without changing its saved draft.
const seedPath = process.env.LOOM_LEGACY_COLLECTION_SEED_REPORT ?? '/tmp/loom-collection-repair-run-2/report.json';
const seed = JSON.parse(await readFile(seedPath, 'utf8'));
assert(/^collection-repair-\d+$/.test(seed.explorer), 'Use an owned collection-repair QA seed');
const project = 'loom_dev_cda_fhir';
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN ?? 'http://127.0.0.1:8188';
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN ?? 'http://127.0.0.1:30008';
const evidence = process.argv[2] ?? `/tmp/loom-legacy-collection-${Date.now()}`;
const base = `/api/v1/projects/${project}/explorers/${seed.explorer}/authoring/v2`;
const report = { seedPath, explorer: seed.explorer, cases: [], errors: [], started: new Date().toISOString() };
await mkdir(evidence, { recursive: true });
let browser;
const builder = async () => {
  const response = await fetch(apiOrigin + base + '/builder', { signal: AbortSignal.timeout(30000) });
  assert.equal(response.status, 200);
  return response.json();
};
try {
  report.before = await builder();
  const document = report.before.workspace.documents[0];
  assert.equal(document.rootResourceType, 'Specimen');
  assert.equal(document.population.route.length, 1);
  assert.equal(document.population.route[0].relationship, 'parent');
  assert(document.population.route[0].catalogEdgeId);
  assert(!document.population.route[0].storageDirection, 'The seed must still be saved in the legacy shape');
  const query = `FOR s IN Specimen FILTER s.id == ${JSON.stringify(seed.oracle.id)} AND s.project == "${project}" AND s.dataset_generation == "cda-fhir-v1" LET parents = (FOR e IN fhir_edge FILTER e._from == s._id AND e.label == "parent" AND e.project == s.project AND e.dataset_generation == s.dataset_generation RETURN e._to) LET children = (FOR e IN fhir_edge FILTER e._to == s._id AND e.label == "parent" AND e.project == s.project AND e.dataset_generation == s.dataset_generation RETURN e._from) RETURN {id:s.id, parents, children}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', process.env.LOOM_ARANGO_CONTAINER ?? 'loom-dev-6d7df93d6a37-arangodb-1', 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source);
  assert.equal(source.parents.length, 0);
  assert(source.children.length > 0, 'The source must distinguish parent from child traversal');
  report.oracle = { query, source };
  browser = await launchBrowser(evidence);
  browser.cdp.on('Runtime.exceptionThrown', e => report.errors.push({ kind: 'runtime', details: e.exceptionDetails }));
  browser.cdp.on('Runtime.consoleAPICalled', e => { if (e.type === 'error') report.errors.push({ kind: 'console', args: e.args }); });
  browser.cdp.on('Network.responseReceived', ({ response }) => { if (response.status >= 400 && !response.url.endsWith('/favicon.ico')) report.errors.push({ kind: 'http', status: response.status, url: response.url }); });
  browser.cdp.on('Network.loadingFailed', e => { if (e.type === 'Script' && e.errorText !== 'net::ERR_ABORTED') report.errors.push({ kind: 'module', error: e.errorText }); });
  const start = Date.now();
  await navigate(browser.cdp, `${uiOrigin}/?project=${project}&explorer=${seed.explorer}&mode=builder`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-table-${document.output.id}"]')`);
  await click(browser.cdp, `[data-testid="construction-table-${document.output.id}"]`);
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false`);
  await click(browser.cdp, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.cdp, `document.querySelector('section[aria-label="Starting collection"]')`);
  const coverageStart = Date.now();
  await click(browser.cdp, 'section[aria-label="Starting collection"] button', { name: 'Check selected-resource coverage' });
  await waitForBrowser(browser.cdp, `document.querySelector('[data-testid="population-coverage-report"]') || document.querySelector('section[aria-label="Starting collection"] [role="alert"]')`);
  const text = await browserEval(browser.cdp, `return document.querySelector('[data-testid="population-coverage-report"]')?.innerText;`);
  report.coverage = text;
  report.cases.push({ name: 'legacy-coverage', durationMs: Date.now() - coverageStart, loadAndCoverageMs: Date.now() - start });
  assert(report.cases[0].loadAndCoverageMs <= 5000, 'Legacy table load and coverage must finish within five seconds');
  assert((text ?? '').includes('1 selected · 0 produce rows · 1 needs attention'), `Legacy connection must use the exact parent direction: ${text}`);
  assert(text.includes(source.id), 'Coverage must name the independently checked CDA member');
  assert(Date.now() - coverageStart <= 5000);
  const rowCount = await browserEval(browser.cdp, `return document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount');`);
  assert.equal(rowCount, '1', 'The parentless selected resource must yield zero data rows');
  report.after = await builder();
  assert.equal(report.after.draftDigest, report.before.draftDigest);
  assert.equal(report.after.draftVersion, report.before.draftVersion);
  assert.deepEqual(report.after.workspace, report.before.workspace, 'Direction resolution must not rewrite the authored draft');
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.failureUI = browser ? await browserEval(browser.cdp, 'return document.body.innerText;').catch(String) : undefined;
} finally {
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, coverage: report.coverage, error: report.error }));
