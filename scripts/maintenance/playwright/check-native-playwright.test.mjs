import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const requireUI = createRequire(join(repositoryRoot, 'ui/packages/loom-ui/package.json'));
const ts = requireUI('typescript');
const checkedCodes = new Set([2304, 2552, 18004]);
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
  assert.match(checkerSource, /if \(!\[2304, 2552, 18004\]\.includes\(diagnostic\.code\)\) continue;/);
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
