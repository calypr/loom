import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { browserEval, launchBrowser, navigate, snapshot, waitForBrowser } from './loom-dev.mjs';

assert.equal(process.argv.length, 3, 'usage: node scripts/verify-row-definition-ui.mjs SELECTION_EVIDENCE');
const source = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const target = { ...source.target, explorerId: source.explorerId };
assert.ok(target.apiUrl && target.uiUrl && target.project && target.explorerId, 'evidence has no browser target');
const apiURL = new URL(target.apiUrl);
const uiURL = new URL(target.uiUrl);
assert.equal(apiURL.hostname, '127.0.0.1', 'row-definition verification only operates on the isolated local API');
assert.equal(uiURL.hostname, '127.0.0.1', 'row-definition verification only operates on the isolated local UI');
assert.match(target.project, /^loom_dev_verify_[a-z0-9]+-[a-f0-9]+$/, 'row-definition verification requires a disposable verification project');
assert.match(target.explorerId, /^loom-dev-verification-selection-[a-f0-9-]+$/, 'row-definition verification requires its disposable selection explorer');

const authoring = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`;
const json = async (url, body) => {
  const response = await fetch(url, {
    ...(body === undefined ? {} : {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
    }),
    signal: AbortSignal.timeout(30_000),
  });
  const value = await response.json();
  assert.ok(response.ok, `${url}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};

let builder = await json(`${authoring}/builder`);
const command = async (commands) => {
  const commandId = crypto.randomUUID();
  const result = await json(`${authoring}/commands`, {
    commandId,
    semanticsVersion: 4,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await json(`${authoring}/builder`);
  return result;
};

for (const document of builder.workspace?.documents ?? []) {
  await command([{ type: 'DELETE_TABLE', outputId: document.output.id }]);
}
const specimenNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen' && node.rowRootEligible);
const observationNode = builder.catalog.nodes.find((node) => node.resourceType === 'Observation' && node.rowRootEligible);
assert.ok(specimenNode && observationNode, 'fixture lacks row-eligible Specimen or Observation resources');
const forwardEdge = builder.catalog.edges.find((edge) => edge.fromNodeId === specimenNode.nodeId && edge.toNodeId === observationNode.nodeId && edge.populated !== false);
const reverseEdge = builder.catalog.edges.find((edge) => edge.fromNodeId === observationNode.nodeId && edge.toNodeId === specimenNode.nodeId && edge.populated !== false);
assert.ok(forwardEdge && reverseEdge, 'fixture lacks a bidirectional Specimen/Observation route');

const created = await command([{ type: 'CREATE_TABLE', title: 'Laboratory observations', rootNodeId: specimenNode.nodeId }]);
const outputId = created.results.find((result) => result.type === 'TABLE_CREATED')?.outputId;
assert.ok(outputId, 'CREATE_TABLE returned no output ID');
const route = await command([{ type: 'ADD_ROUTE', outputId, parentOccurrenceId: 'base', edgeId: forwardEdge.edgeId }]);
const observationOccurrenceId = route.results.find((result) => result.type === 'ROUTE_ADDED')?.occurrenceId;
assert.ok(observationOccurrenceId, 'ADD_ROUTE returned no Observation occurrence ID');
await command([
  { type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title: 'Specimen ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
  { type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: observationOccurrenceId, title: 'Observation ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
]);
const before = builder;
const original = before.workspace.documents.find((document) => document.output.id === outputId);
assert.ok(original && !original.population, 'row-definition journey must start without a file population');
const featureKeys = original.columns.map((column) => column.column);

const query = new URLSearchParams({ project: target.project, explorer: target.explorerId, mode: 'builder' });
const artifactRoot = join(process.cwd(), '.artifacts/loom-dev', `row-definition-ui-${Date.now()}`);
const htmlPath = `${artifactRoot}.html`;
const reportPath = `${artifactRoot}.json`;
mkdirSync(dirname(htmlPath), { recursive: true });
const downloads = mkdtempSync(join(tmpdir(), 'loom-row-definition-ui-'));
const browser = await launchBrowser(downloads);
const browserFailures = [];
const authoringResponses = [];
browser.cdp.on('Runtime.exceptionThrown', ({ exceptionDetails }) => browserFailures.push(exceptionDetails.exception?.description ?? exceptionDetails.text ?? 'browser exception'));
browser.cdp.on('Network.responseReceived', ({ response }) => {
  const responseURL = new URL(response.url);
  if (response.url.includes('/authoring/v2/')) authoringResponses.push(`${response.status} ${responseURL.pathname}`);
  if (response.status >= 400 && responseURL.pathname !== '/favicon.ico') browserFailures.push(`${response.status} ${response.url}`);
});

try {
  await navigate(browser.cdp, `${target.uiUrl}/?${query}`);
  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('select[aria-label="One row per"] option')].find((option) => option.textContent.includes('Observation')))`, 60_000);
  await browserEval(browser.cdp, `
    const select = document.querySelector('select[aria-label="One row per"]');
    const option = [...select.options].find((candidate) => candidate.textContent.includes('Observation'));
    select.value = option.value;
    select.dispatchEvent(new Event('change', { bubbles: true }));
  `);
  await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="One row per"] option:checked')?.textContent.trim() === 'Observation'`, 60_000);

  const after = await json(`${authoring}/builder`);
  const rebased = after.workspace.documents.find((document) => document.output.id === outputId);
  assert.ok(rebased, 'row-defined table disappeared');
  assert.equal(rebased.rootResourceType, 'Observation');
  assert.equal(rebased.route.children?.[0]?.resourceType, 'Specimen');
  assert.equal(rebased.population, undefined, 'row change introduced a file population');
  assert.deepEqual(rebased.columns.map((column) => column.column), featureKeys, 'row change replaced stable feature keys');
  assert.equal(after.draftVersion, before.draftVersion + 1, 'row definition must create exactly one draft version');

  await waitForBrowser(browser.cdp, `Boolean([...document.querySelectorAll('button')].find((button) => button.textContent.trim() === 'Preview' && !button.disabled))`);
  await browserEval(browser.cdp, `clickButton('Preview');`);
  await waitForBrowser(browser.cdp, `document.body.innerText.includes('dev-pair-001')`, 60_000);
  await browser.cdp.send('Page.reload', { ignoreCache: true });
  await waitForBrowser(browser.cdp, `document.querySelector('select[aria-label="One row per"] option:checked')?.textContent.trim() === 'Observation'`, 60_000);
  assert.equal(browserFailures.length, 0, `browser failures: ${browserFailures.join(' | ')}`);
  await snapshot(browser.cdp, htmlPath);

  const evidence = {
    status: 'passed', scenario: 'explicit-row-definition-without-patient-or-files',
    target, outputId,
    before: { draftVersion: before.draftVersion, rootResourceType: original.rootResourceType, featureKeys },
    after: { draftVersion: after.draftVersion, rootResourceType: rebased.rootResourceType, featureKeys: rebased.columns.map((column) => column.column) },
    assertions: [
      'Builder exposes an explicit One row per control outside the graph',
      'Specimen rows change to Observation rows without Patient or DocumentReference input',
      'stable Specimen and Observation feature keys survive the row change',
      'Preview returns fixture Observation rows',
      'the Observation row definition survives a full reload',
    ],
    authoringResponses, evidencePaths: [htmlPath],
  };
  writeFileSync(reportPath, JSON.stringify(evidence, null, 2), { mode: 0o600 });
  console.log(JSON.stringify({ evidence: reportPath, assertions: evidence.assertions }, null, 2));
} catch (error) {
  await snapshot(browser.cdp, htmlPath);
  const reason = error instanceof Error ? error.message : String(error);
  throw new Error(`${reason}; authoring responses: ${authoringResponses.slice(-12).join(' | ') || 'none'}; browser failures: ${browserFailures.slice(-5).join(' | ') || 'none'}`);
} finally {
  await browser.close();
  rmSync(downloads, { recursive: true, force: true });
}
