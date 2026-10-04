import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { performAction, requireUnique } from './lib/playwright-actions.mjs';
import { captureCDARequests } from './lib/cda-playwright-requests.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { waitForCondition } from './lib/playwright-observations.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';

// Replay an owned, pre-direction-field QA Explorer without changing its saved draft.
const seedPath = process.env.LOOM_LEGACY_COLLECTION_SEED_REPORT;
assert(seedPath, 'Set LOOM_LEGACY_COLLECTION_SEED_REPORT to a retained owned legacy fixture report');
const seed = JSON.parse(await readFile(seedPath, 'utf8'));
assert(/^collection-repair-\d+$/.test(seed.explorer), 'Use an owned collection-repair QA seed');
const project = process.env.LOOM_CDA_PROJECT;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const arangoContainer = process.env.LOOM_ARANGO_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const evidence = process.argv[2] ?? `/tmp/loom-legacy-collection-${Date.now()}`;
const sourceRoot = process.env.LOOM_SOURCE_FREEZE_ROOT ?? fileURLToPath(new URL('..', import.meta.url));
await assertOwnedCdaTarget({ project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot, arangoContainer });
const base = `/api/v1/projects/${project}/explorers/${seed.explorer}/authoring/v2`;
const report = { seedPath, explorer: seed.explorer, cases: [], nativeRequests: [], errors: [], started: new Date().toISOString() };
const sourceFreeze = await captureSourceFreeze(sourceRoot);
const sourceBefore = sourceFingerprint(sourceRoot);
const frozenApiBuild = await captureApiBuildFreeze(() => checkContainerApiBuildStamp(apiContainer));
report.sourceFreeze = { root: sourceRoot, watchedFileCount: sourceFreeze.watchedFileCount };
report.sourceFingerprint = { root: sourceRoot, before: sourceBefore };
report.apiBuildFreeze = { container: apiContainer, initial: frozenApiBuild.initial };
await mkdir(evidence, { recursive: true });
let browser;
let actionTracker = {};
const inspectPage = (page, inspect, argument) => page.evaluate(inspect, argument);
const waitForBrowser = (page, condition, timeout = 30000) => waitForCondition(page, condition, timeout);
const resolveActionLocator = async (page, selector, identity = {}) => {
  const candidates = page.locator(selector);
  if (identity.name === undefined) return requireUnique(candidates, selector);
  const matches = await candidates.evaluateAll((nodes, name) => nodes.flatMap((node, index) =>
    String(node.getAttribute('aria-label') || node.innerText || node.textContent || '').replace(/\s+/g, ' ').trim() === name ? [index] : []), identity.name);
  assert.equal(matches.length, 1, `${selector}: expected one target named ${identity.name}, found ${matches.length}`);
  return requireUnique(candidates.nth(matches[0]), `${selector} ${identity.name}`);
};
const click = async (page, selector, identity = {}, timeout = 5000) => {
  const label = `Click ${identity.name ?? selector}`;
  actionTracker.activeAction = { label, locator: selector, targetLocator: page.locator(selector), startedAt: Date.now() };
  const locator = await resolveActionLocator(page, selector, identity);
  actionTracker.activeAction.targetLocator = locator;
  const elapsedMs = await performAction(actionTracker, label, locator, (target, options) => target.click(options), { timeout });
  actionTracker.lastAction = { label, locator: locator.toString(), targetLocator: locator, elapsedMs };
  return elapsedMs;
};
const navigate = (page, url) => page.goto(url, { waitUntil: 'load', timeout: 30000 });
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
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', arangoContainer, 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000 });
  assert.equal(raw.status, 0, raw.stderr);
  const [source] = JSON.parse(raw.stdout.slice(raw.stdout.indexOf('[')));
  assert(source);
  assert.equal(source.parents.length, 0);
  assert(source.children.length > 0, 'The source must distinguish parent from child traversal');
  report.oracle = { query, source };
  browser = await launchBrowser({ evidence, appOrigins: [apiOrigin, uiOrigin], noAuth: process.env.LOOM_CDA_NO_AUTH === '1' });
  const requestMonitor = captureCDARequests(browser.page, {
    apiOrigin: uiOrigin,
    appOrigins: [apiOrigin, uiOrigin],
    ownedPathPrefix: `/api/v1/projects/${project}/explorers/${seed.explorer}`,
    responsePaths: /./,
    report: { nativeRequests: report.nativeRequests, errors: report.errors },
  });
  browser.page.on('response', response => {
    if (response.status() < 400 || response.url().endsWith('/favicon.ico')) return;
    const url = new URL(response.url());
    if ([new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin)) report.errors.push({ kind: 'http', path: url.pathname, status: response.status() });
  });
  browser.page.on('requestfailed', request => {
    const url = new URL(request.url());
    if ([new URL(apiOrigin).origin, new URL(uiOrigin).origin].includes(url.origin) && request.resourceType() === 'script') {
      report.errors.push({ kind: 'module', path: url.pathname, error: sanitizeText(request.failure()?.errorText) });
    }
  });
  const start = Date.now();
  await navigate(browser.page, `${uiOrigin}/?project=${project}&explorer=${seed.explorer}&mode=builder`);
  await waitForBrowser(browser.page, { kind: 'present', selector: `[data-testid="construction-table-${document.output.id}"]` });
  await click(browser.page, `[data-testid="construction-table-${document.output.id}"]`);
  await waitForBrowser(browser.page, { kind: 'enabled', selector: '[data-testid="construction-rows-settings-trigger"]' });
  await click(browser.page, '[data-testid="construction-rows-settings-trigger"]');
  await waitForBrowser(browser.page, { kind: 'present', selector: 'section[aria-label="Starting collection"]' });
  const coverageStart = Date.now();
  await click(browser.page, 'section[aria-label="Starting collection"] button', { name: 'Check selected-resource coverage' });
  await waitForBrowser(browser.page, { kind: 'any', conditions: [
    { kind: 'present', selector: '[data-testid="population-coverage-report"]' },
    { kind: 'present', selector: 'section[aria-label="Starting collection"] [role="alert"]' },
  ] });
  const text = await inspectPage(browser.page, () => document.querySelector('[data-testid="population-coverage-report"]')?.innerText);
  report.coverage = text;
  report.cases.push({ name: 'legacy-coverage', durationMs: Date.now() - coverageStart, loadAndCoverageMs: Date.now() - start });
  assert(report.cases[0].loadAndCoverageMs <= 5000, 'Legacy table load and coverage must finish within five seconds');
  assert((text ?? '').includes('1 selected · 0 produce rows · 1 needs attention'), `Legacy connection must use the exact parent direction: ${text}`);
  assert(text.includes(source.id), 'Coverage must name the independently checked CDA member');
  assert(Date.now() - coverageStart <= 5000);
  const rowCount = await browser.page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
  assert.equal(rowCount, '1', 'The parentless selected resource must yield zero data rows');
  report.after = await builder();
  assert.equal(report.after.draftDigest, report.before.draftDigest);
  assert.equal(report.after.draftVersion, report.before.draftVersion);
  assert.deepEqual(report.after.workspace, report.before.workspace, 'Direction resolution must not rewrite the authored draft');
  await requestMonitor.flush();
  assert.deepEqual(report.errors, []);
  report.status = 'passed';
} catch (error) {
  report.status = 'failed'; report.error = String(error.stack ?? error); process.exitCode = 1;
  report.failureUI = browser ? await inspectPage(browser.page, () => document.body.innerText).catch(String) : undefined;
  if (browser) report.failureTrace = await browser.captureFailure(error, { phase: report.cases.length,
    action: actionTracker.activeAction ?? actionTracker.lastAction,
    elapsedMs: actionTracker.activeAction?.startedAt ? Date.now() - actionTracker.activeAction.startedAt : actionTracker.lastAction?.elapsedMs,
    requestIdentity: report.nativeRequests.at(-1) && (({ requestId, path, method }) => ({ requestId, path, method }))(report.nativeRequests.at(-1)),
  }).catch(String);
} finally {
  try {
    const after = sourceFingerprint(sourceRoot);
    const unchanged = sourceBefore.sha256 === after.sha256 && sourceBefore.files === after.files;
    report.sourceFingerprint = { ...report.sourceFingerprint, after, unchanged, invalidatesRun: !unchanged };
    assert(unchanged, 'Watched source fingerprint changed during verification');
    report.sourceFreeze = { ...report.sourceFreeze, ...(await sourceFreeze.assertUnchanged()) };
    report.apiBuildFreeze = { ...report.apiBuildFreeze, ...(await frozenApiBuild.assertUnchanged()) };
  } catch (error) {
    report.priorStatus = report.status;
    report.status = 'invalidated';
    report.invalidations = [{ kind: 'verification-freeze', reason: sanitizeText(error) }];
    process.exitCode = 1;
  }
  report.finished = new Date().toISOString();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await browser?.close();
}
console.log(JSON.stringify({ status: report.status, evidence, coverage: report.coverage, error: report.error }));
