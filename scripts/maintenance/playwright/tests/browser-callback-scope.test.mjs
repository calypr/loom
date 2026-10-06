import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { createBrowserCallbackScopeChecker } from '../browser-callback-scope.mjs';

const testDirectory = dirname(fileURLToPath(import.meta.url));
const sourceRoot = process.env.LOOM_BROWSER_CALLBACK_SCOPE_ROOT ?? resolve(testDirectory, '../../../..');
const requireUI = createRequire(join(sourceRoot, 'ui/packages/loom-ui/package.json'));
const ts = requireUI('typescript');
const compilerOptions = {
  allowJs: true,
  checkJs: true,
  noEmit: true,
  noResolve: true,
  skipLibCheck: true,
  target: ts.ScriptTarget.ESNext,
  module: ts.ModuleKind.NodeNext,
  moduleResolution: ts.ModuleResolutionKind.NodeNext,
};

function inspectSources(sources) {
  const virtualFiles = new Map(Object.entries(sources).map(([path, content]) => [resolve(path), content]));
  const host = ts.createCompilerHost(compilerOptions);
  const readFile = host.readFile.bind(host);
  const fileExists = host.fileExists.bind(host);
  const getSourceFile = host.getSourceFile.bind(host);
  host.readFile = path => virtualFiles.get(resolve(path)) ?? readFile(path);
  host.fileExists = path => virtualFiles.has(resolve(path)) || fileExists(path);
  host.getSourceFile = (path, languageVersion, onError, shouldCreateNewSourceFile) => {
    const source = virtualFiles.get(resolve(path));
    return source === undefined
      ? getSourceFile(path, languageVersion, onError, shouldCreateNewSourceFile)
      : ts.createSourceFile(path, source, languageVersion, true, ts.ScriptKind.JS);
  };

  const program = ts.createProgram([...virtualFiles.keys()], compilerOptions, host);
  const check = createBrowserCallbackScopeChecker(ts, program);
  return [...virtualFiles.keys()].flatMap(path => {
    const sourceFile = program.getSourceFile(path);
    return sourceFile ? check(sourceFile).map(issue => ({ path, ...issue })) : [];
  });
}

test('native browser callback guard rejects a module helper capture and allows serialized args', () => {
  const fixturePath = resolve(testDirectory, 'callback-scope-fixture.mjs');
  const issues = inspectSources({
    [fixturePath]: `
      const startingCollectionPreviewHeaderText = header => header.textContent.trim();
      const leaked = () => cda.inspect(() => [...document.querySelectorAll('th')].map(startingCollectionPreviewHeaderText));
      const serialized = () => cda.inspect(([expectedHeaders]) =>
        [...document.querySelectorAll('th')].map(header => header.textContent.trim()).includes(expectedHeaders[0]),
      [['Patient ID']]);
    `,
  });

  assert.deepEqual(issues.map(({ name }) => name), ['startingCollectionPreviewHeaderText']);
});

test('native browser callback guard follows a direct wait alias to its call sites', () => {
  const fixturePath = resolve(testDirectory, 'callback-alias-fixture.mjs');
  const issues = inspectSources({
    [fixturePath]: `
      const startingCollectionPreviewHeaderText = header => header.textContent.trim();
      const wait = (callback, args = [], timeout = 5000) => cda.wait(callback, args, timeout);
      const leaked = () => wait(() => [...document.querySelectorAll('th')].map(startingCollectionPreviewHeaderText));
      const serialized = () => wait(([expected]) => document.body.innerText.includes(expected), ['Patient ID']);
    `,
  });

  assert.deepEqual(issues.map(({ name }) => name), ['startingCollectionPreviewHeaderText']);
});

test('serialized callbacks resolve known browser globals in the target page despite Node shadows', () => {
  const fixturePath = resolve(testDirectory, 'callback-global-shadow-fixture.mjs');
  const issues = inspectSources({
    [fixturePath]: `
      const document = { body: 'node-side' };
      const URL = function NodeURL() {};
      const leaked = () => cda.inspect(() => [document.body, URL]);
    `,
  });

  assert.deepEqual(issues, []);
});

test('the corrected exported starting-collection preview callbacks are self-contained', () => {
  const workflowPath = resolve(sourceRoot, 'scripts/verify-ui/workflows/verify-cda-starting-collection-handoff.mjs');
  const issues = inspectSources({ [workflowPath]: readFileSync(workflowPath, 'utf8') });
  assert.deepEqual(issues, []);
});

test('waitForAddColumnsAction serializes its selector instead of capturing it', () => {
  const loomDevPath = resolve(sourceRoot, 'scripts/loom-dev.mjs');
  const issues = inspectSources({ [loomDevPath]: readFileSync(loomDevPath, 'utf8') });
  assert.deepEqual(issues, []);
});

test('existing serialized wait callbacks pass all Node values as arguments', () => {
  const paths = [
    'scripts/verify-ui/workflows/verify-cda-coded-pivot.mjs',
    'scripts/verify-ui/workflows/verify-cda-implicit-pivot.mjs',
    'scripts/verify-ui/workflows/verify-cda-unpivot.mjs',
    'ui/packages/loom-ui/scripts/verify-cda-contributor-exists-browser.mjs',
  ];
  const sources = Object.fromEntries(paths.map(path => {
    const absolute = resolve(sourceRoot, path);
    return [absolute, readFileSync(absolute, 'utf8')];
  }));

  assert.deepEqual(inspectSources(sources), []);
});
