import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';

export async function legacyCollectionWorkflow({ page, cda }) {
  // Replay an owned, pre-direction-field QA Explorer without changing its saved draft.
  const seedPath = cda.env.LOOM_LEGACY_COLLECTION_SEED_REPORT ?? process.env.LOOM_LEGACY_COLLECTION_SEED_REPORT;
  assert(seedPath, 'Set LOOM_LEGACY_COLLECTION_SEED_REPORT to a retained owned legacy fixture report');
  const seed = JSON.parse(await readFile(seedPath, 'utf8'));
  assert(/^collection-repair-\d+$/.test(seed.explorer), 'Use an owned collection-repair QA seed');
  const project = cda.project;
  const apiOrigin = cda.apiOrigin;
  const uiOrigin = cda.uiOrigin;
  const arangoContainer = cda.target.arangoContainer;
  const base = `/api/v1/projects/${project}/explorers/${seed.explorer}/authoring/v2`;
  const report = cda.report;
  Object.assign(report, { seedPath, explorer: seed.explorer, cases: [], errors: report.errors ?? [], started: new Date().toISOString(), target: cda.target });
  let requestMonitor;
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
    requestMonitor = cda.captureRequests(`/api/v1/projects/${project}/explorers/${seed.explorer}`, { responsePaths: /./ });

    const start = Date.now();
    await cda.navigate( `${uiOrigin}/?project=${project}&explorer=${seed.explorer}&mode=builder`);
    await cda.wait(([selector]) => Boolean(document.querySelector(selector)), [`[data-testid="construction-table-${document.output.id}"]`], 5000);
    await cda.click( `[data-testid="construction-table-${document.output.id}"]`);
    await cda.wait(() => document.querySelector('[data-testid="construction-rows-settings-trigger"]')?.disabled === false, [], 5000);
    await cda.click( '[data-testid="construction-rows-settings-trigger"]');
    await cda.wait(() => Boolean(document.querySelector('section[aria-label="Starting collection"]')), [], 5000);
    const coverageStart = Date.now();
    await cda.click( 'section[aria-label="Starting collection"] button', { name: 'Check selected-resource coverage' });
    await cda.wait(() => Boolean(document.querySelector('[data-testid="population-coverage-report"]') || document.querySelector('section[aria-label="Starting collection"] [role="alert"]')), [], 5000);
    const text = await cda.inspect( () => document.querySelector('[data-testid="population-coverage-report"]')?.innerText);
    report.coverage = text;
    report.cases.push({ name: 'legacy-coverage', durationMs: Date.now() - coverageStart, loadAndCoverageMs: Date.now() - start });
    assert(report.cases[0].loadAndCoverageMs <= 5000, 'Legacy table load and coverage must finish within five seconds');
    assert((text ?? '').includes('1 selected · 0 produce rows · 1 needs attention'), `Legacy connection must use the exact parent direction: ${text}`);
    assert(text.includes(source.id), 'Coverage must name the independently checked CDA member');
    assert(Date.now() - coverageStart <= 5000);
    const rowCount = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
    assert.equal(rowCount, '1', 'The parentless selected resource must yield zero data rows');
    report.after = await builder();
    assert.equal(report.after.draftDigest, report.before.draftDigest);
    assert.equal(report.after.draftVersion, report.before.draftVersion);
    assert.deepEqual(report.after.workspace, report.before.workspace, 'Direction resolution must not rewrite the authored draft');
    cda.includeBrowserDiagnostics();
    assert.deepEqual(report.errors, []);
    assert.deepEqual(cda.diagnostics.pageErrors, [], 'Unexpected browser exceptions');
    assert.deepEqual(cda.diagnostics.console, [], 'Unexpected browser console errors');
    assert.deepEqual(cda.diagnostics.networkFailures, [], 'Unexpected browser request failures');
    assert.deepEqual(cda.diagnostics.httpFailures, [], 'Unexpected browser HTTP failures');
    report.status = 'passed';
  } catch (error) {
    report.status = 'failed'; report.error = String(error.stack ?? error);
    report.failureUI = await cda.inspect( () => document.body.innerText).catch(String);
  } finally {
    await requestMonitor?.flush();
    report.finished = new Date().toISOString();
    cda.includeBrowserDiagnostics();
    await cda.attachReport('legacy-collection', report);
  }
  if (report.status === 'failed') throw new Error(report.error);
}
