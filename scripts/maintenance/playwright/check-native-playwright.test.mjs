import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { findRemovedNativePlaywrightApiReferences } from './removed-native-playwright-apis.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const requireUI = createRequire(join(repositoryRoot, 'ui/packages/loom-ui/package.json'));
const ts = requireUI('typescript');
const checkedCodes = new Set([2304, 2552, 18004, 2448]);
const nodeGlobals = new Set(['process', 'Buffer', 'setImmediate', 'clearImmediate', 'global']);
const checkerSource = readFileSync(new URL('./check-native-playwright.mjs', import.meta.url), 'utf8');

function diagnostics(source) {
  const directory = mkdtempSync(join(tmpdir(), 'loom-native-binding-'));
  try {
    const file = join(directory, 'fixture.mjs');
    writeFileSync(file, source);
    const program = ts.createProgram([file], {
      allowJs: true,
      checkJs: true,
      noEmit: true,
      noResolve: true,
      skipLibCheck: true,
      target: ts.ScriptTarget.ESNext,
      module: ts.ModuleKind.NodeNext,
      moduleResolution: ts.ModuleResolutionKind.NodeNext,
    });
    return program.getSemanticDiagnostics().map(diagnostic => ({
      code: diagnostic.code,
      message: ts.flattenDiagnosticMessageText(diagnostic.messageText, ' '),
    }));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function unresolvedName(message) {
  return message.match(/Cannot find name '([^']+)'/)?.[1]
    ?? message.match(/shorthand property '([^']+)'/)?.[1];
}

test('the saved-scope typo emits a gate-covered TS2552, while its explicit binding passes', () => {
  assert.match(checkerSource, /if \(!\[2304, 2552, 18004, 2448\]\.includes\(diagnostic\.code\)\) continue;/);
  const broken = diagnostics(`async function inspect() {
  const rootReread = [];
  return { rawRootReread };
}`);
  const typo = broken.find(item => item.code === 2552);
  assert.ok(typo, JSON.stringify(broken));
  assert.match(typo.message, /Cannot find name 'rawRootReread'/);
  assert.equal(unresolvedName(typo.message), 'rawRootReread');
  assert.ok(checkedCodes.has(typo.code), `checker must include TS${typo.code}`);

  const fixed = diagnostics(`async function inspect() {
  const rootReread = [];
  return { rawRootReread: rootReread };
}`);
  assert.deepEqual(fixed.filter(item => checkedCodes.has(item.code)), []);
});

test('the RelatedExpand removal branch rejects the retained directSaved TDZ and accepts its fixed order', () => {
  assert.match(checkerSource, /if \(!\[2304, 2552, 18004, 2448\]\.includes\(diagnostic\.code\)\) continue;/);
  const retainedPreimage = diagnostics(`async function editSavedRoute(includeRemovalLifecycle) {
  if (includeRemovalLifecycle) {
    const removalBaseline = {
      ...directSaved,
      document: structuredClone(directSaved.document),
    };
    await previewRelatedRemoval('RelatedExpand removal Cancel', directSaved);
    return removalBaseline;
  }
  const directSaved = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
  return directSaved;
}`);
  const tdz = retainedPreimage.filter(item => item.code === 2448);
  assert.ok(tdz.length > 0, JSON.stringify(retainedPreimage));
  assert.ok(tdz.every(item => /directSaved/.test(item.message)), JSON.stringify(tdz));
  assert.ok(checkedCodes.has(2448), 'the gate must report the retained TS2448 declaration-order failure');

  const fixed = diagnostics(`async function editSavedRoute(includeRemovalLifecycle) {
  const directSaved = savedRelatedState(await apiJSON(target, routeApiRoot, '/builder'), outputId);
  if (includeRemovalLifecycle) {
    const removalBaseline = {
      ...directSaved,
      document: structuredClone(directSaved.document),
    };
    await previewRelatedRemoval('RelatedExpand removal Cancel', directSaved);
    return removalBaseline;
  }
  return directSaved;
}`);
  assert.deepEqual(fixed.filter(item => item.code === 2448), []);
});

test('unresolved shorthand is caught and known Node globals stay exempt', () => {
  const missing = diagnostics('const payload = { unboundShorthand };');
  const shorthand = missing.find(item => item.code === 18004);
  assert.ok(shorthand, JSON.stringify(missing));
  assert.equal(unresolvedName(shorthand.message), 'unboundShorthand');
  assert.ok(checkedCodes.has(shorthand.code));

  const nodeGlobal = diagnostics('const payload = { setImmediate };');
  const globalDiagnostic = nodeGlobal.find(item => item.code === 18004);
  assert.ok(globalDiagnostic, JSON.stringify(nodeGlobal));
  assert.equal(unresolvedName(globalDiagnostic.message), 'setImmediate');
  assert.ok(nodeGlobals.has(unresolvedName(globalDiagnostic.message)));
  assert.match(checkerSource, /nodeGlobals\.has\(missingName\)/);
});

test('native Playwright source gate rejects imports, fixture members, and removed request drains', () => {
  assert.match(checkerSource, /findRemovedNativePlaywrightApiReferences\(source, ts\)/);
  assert.match(checkerSource, /scripts\/verify-ui\/helpers\/tests/);
  const source = `import { installNativeAbortProbe } from '../helpers/native-abort-probe.mjs';
cda.includeBrowserDiagnostics();
await cda.waitForCapturedResponse(predicate);
await cda.flushHttpDiagnostics();
const ledger = workflow.nativeRequestLedger;
await tracker.flush();
await requestCapture.waitFor(predicate);
void tracker.pendingReads;
void observer.byRequest.entries();
await drainPendingResponseReads();
await captureOwnedPreviewLifecycle();`;
  const issues = findRemovedNativePlaywrightApiReferences(source, ts);
  assert.deepEqual([...new Set(issues.map(({ line }) => line))], [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11]);
  assert.ok(issues.some(issue => issue.name.startsWith('deleted helper module') && issue.line === 1));
  assert.ok(issues.some(issue => issue.name === 'removed CDA fixture member' && issue.line === 2));
  assert.ok(issues.some(issue => issue.reference === 'tracker.flush' && issue.line === 6));
});

test('native source gate permits the shared observer and specific response accessors', () => {
  const source = `import { captureCDARequests, findCompletedNativeResponse } from '../helpers/cda-playwright-requests.mjs';
const capture = cda.captureRequests('/api/v1/projects/p/explorers/e');
await capture.waitForResponse(entry => entry.status === 200, { fromIndex: 0, timeoutMs: 5000 });
await page.waitForResponse(response => response.ok());
const request = await capture.requestBody(entry);
const response = await capture.responseBody(entry);
observer.markExpectedValidation(entry, { status: 422, reason, proof });
observer.markExpectedTransportFailure(request, { reason, action: 'abort' });
const body = response.body ?? document.body;
void captureCDARequests;
void findCompletedNativeResponse;`;
  assert.deepEqual(findRemovedNativePlaywrightApiReferences(source, ts), []);
  assert.deepEqual(findRemovedNativePlaywrightApiReferences(`
// cda.includeBrowserDiagnostics() was removed.
const note = 'nativeRequestLedger';
`, ts), [], 'comments and string data are not executable API references');
});

test('native source gate catches fixture-view stops after assignments and local wrappers', () => {
  const source = `const fromHelper = captureCDARequests(page, options);
fromHelper.stop();
const fromFixture = cda.captureRequests('/api/v1/projects/p/explorers/e');
fromFixture.stop();
const fromAdapter = createCDARequestView(observer, options);
fromAdapter.stop();
const observer = createPlaywrightNetworkObserver(page, options);
observer.stop();
async function installGroupJoinNativeCapture() {
  return cda.captureRequests('/api/v1/projects/p/explorers/e');
}
let fromGroupJoin;
fromGroupJoin = await installGroupJoinNativeCapture();
fromGroupJoin.stop();
let fromLaterAssignment;
fromLaterAssignment = await cda.captureRequests('/api/v1/projects/p/explorers/e');
fromLaterAssignment.stop();
let browserRequestCapture;
browserRequestCapture = isFixture ? context.networkObserver : context.captureRequests('/api/v1/projects/p/explorers/e');
browserRequestCapture.stop();`;
  const issues = findRemovedNativePlaywrightApiReferences(source, ts);
  assert.deepEqual(issues.map(issue => issue.reference), [
    'fromFixture.stop()',
    'fromAdapter.stop()',
    'fromGroupJoin.stop()',
    'fromLaterAssignment.stop()',
    'browserRequestCapture.stop()',
  ]);
  assert.equal(issues.some(issue => issue.reference === 'fromHelper.stop()'), false,
    'the directly owned captureCDARequests observer can be stopped by its caller');
  assert.equal(issues.some(issue => issue.reference === 'observer.stop()'), false,
    'the directly created observer can be stopped by its caller');
});
