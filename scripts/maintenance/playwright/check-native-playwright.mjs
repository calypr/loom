import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { caseNamesFor, coverageDrift, registry, scenarioCaseFor } from '../../verify-ui/registry.mjs';
import { createBrowserCallbackScopeChecker } from './browser-callback-scope.mjs';

const scriptsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const root = resolve(scriptsRoot, '..');
const problems = [];
problems.push(...coverageDrift());
let mappedCases = 0;
for (const scenario of registry) {
  for (const caseName of caseNamesFor(scenario)) {
    const caseDefinition = scenarioCaseFor(scenario, caseName);
    const spec = caseDefinition?.playwrightTest;
    if (!spec || !existsSync(join(root, spec))) {
      problems.push(`${scenario.id}/${caseName}: missing native spec mapping`);
      continue;
    }
    const source = readFileSync(join(root, spec), 'utf8');
    if (!/\b(?:test|[A-Za-z_$][\w$]*Test)\s*\(/.test(source) || !source.includes(scenario.id) || !source.includes(caseName)) {
      problems.push(`${scenario.id}/${caseName}: mapped spec does not declare the case`);
      continue;
    }
    mappedCases += 1;
  }
}

function* files(directory) {
  if (!existsSync(directory)) return;
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === '.artifacts' || entry.name === 'tests') continue;
    const path = join(directory, entry.name);
    if (entry.isDirectory()) yield* files(path);
    else if (entry.name.endsWith('.mjs') && !entry.name.endsWith('.test.mjs')) yield path;
  }
}

const checkedPaths = new Set();
const browserOwnerNames = 'launchBrowser|launchCdaBrowser|launchPlaywrightBrowser|launchPlaywrightEvidenceBrowser';
const browserOwnership = new RegExp(
  `\\b(?:${browserOwnerNames})\\s*\\(|\\bimport\\s*\\{[^}]*\\b(?:${browserOwnerNames})\\b[^}]*\\}|` +
  `\\bexport\\s+(?:async\\s+)?(?:function|const|let)\\s+(?:${browserOwnerNames})\\b|` +
  `\\b(?:chromium|firefox|webkit)\\.(?:launch(?:PersistentContext)?|connect(?:OverCDP)?)\\s*\\(`,
  's',
);

function checkBrowserSource(path) {
  if (checkedPaths.has(path)) return;
  checkedPaths.add(path);
  const source = readFileSync(path, 'utf8');
  const relativePath = path.slice(root.length + 1);
  if (/\brunPlaywrightCase\b|\bexecuteScenario\b/.test(source)) {
    problems.push(`${relativePath}: custom runner remains`);
  }
  if (browserOwnership.test(source)) {
    problems.push(`${relativePath}: verifier still owns its browser`);
  }
  if (/\b__loomSession\b/.test(source)) {
    problems.push(`${relativePath}: legacy browser session facade remains`);
  }
}

for (const directory of ['scripts/verify-ui', 'scripts/lib']) {
  for (const path of files(join(root, directory))) checkBrowserSource(path);
}
checkBrowserSource(join(root, 'scripts/loom-dev.mjs'));
checkBrowserSource(join(root, 'scripts/measurements/construction-preview/construction_preview_bench.mjs'));
for (const entry of readdirSync(join(root, 'scripts'))) {
  if (/^verify(?:-|_).*\.mjs$/.test(entry) && !entry.endsWith('.test.mjs')) {
    checkBrowserSource(join(root, 'scripts', entry));
  }
}
for (const path of files(join(root, 'ui/packages/loom-ui/scripts'))) {
  if (/^verify.*\.mjs$/.test(path.split('/').at(-1))) checkBrowserSource(path);
}

try {
  const requireUI = createRequire(join(root, 'ui/packages/loom-ui/package.json'));
  const ts = requireUI('typescript');
  const program = ts.createProgram([...checkedPaths], {
    allowJs: true,
    checkJs: true,
    noEmit: true,
    noResolve: true,
    skipLibCheck: true,
    target: ts.ScriptTarget.ESNext,
    module: ts.ModuleKind.NodeNext,
    moduleResolution: ts.ModuleResolutionKind.NodeNext,
  });
  const nodeGlobals = new Set(['process', 'Buffer', 'setImmediate', 'clearImmediate', 'global']);
  const checkBrowserCallbackScope = createBrowserCallbackScopeChecker(ts, program);
  for (const diagnostic of program.getSemanticDiagnostics()) {
    if (![2304, 2552, 18004].includes(diagnostic.code)) continue;
    const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, ' ');
    const missingName = message.match(/Cannot find name '([^']+)'/)?.[1]
      ?? message.match(/shorthand property '([^']+)'/)?.[1];
    if (missingName && nodeGlobals.has(missingName)) continue;
    const { line } = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start);
    problems.push(`${diagnostic.file.fileName.slice(root.length + 1)}:${line + 1}: ${message} Restore its import or use the native fixture API.`);
  }
  for (const path of checkedPaths) {
    const sourceFile = program.getSourceFile(path);
    if (!sourceFile) continue;
    for (const issue of checkBrowserCallbackScope(sourceFile)) {
      problems.push(`${path.slice(root.length + 1)}:${issue.line}: Browser callback captures Node-scope name "${issue.name}". Pass it through serialized arguments or keep the browser logic self-contained.`);
    }
  }
} catch (error) {
  problems.push(`Native binding check could not run: ${error.message}. Install the UI package dependencies before running this gate.`);
}

console.log(`${mappedCases}/${registry.reduce((total, scenario) => total + caseNamesFor(scenario).length, 0)} registered cases have native spec mappings`);
for (const problem of problems) console.error(problem);
if (problems.length) process.exitCode = 1;
