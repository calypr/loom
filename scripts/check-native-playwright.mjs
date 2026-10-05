import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { registry } from './verify-ui/registry.mjs';

const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
const problems = [];
let mappedCases = 0;
for (const scenario of registry) {
  for (const caseName of scenario.cases) {
    const spec = scenario.playwrightTests?.[caseName];
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
  `\\b(?:chromium|firefox|webkit)\\.launch(?:PersistentContext)?\\s*\\(`,
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

for (const directory of ['scripts/verify-ui', 'scripts/playwright', 'scripts/lib']) {
  for (const path of files(join(root, directory))) checkBrowserSource(path);
}
checkBrowserSource(join(root, 'scripts/loom-dev.mjs'));
for (const entry of readdirSync(join(root, 'scripts'))) {
  if (/^verify(?:-|_).*\.mjs$/.test(entry) && !entry.endsWith('.test.mjs')) {
    checkBrowserSource(join(root, 'scripts', entry));
  }
}
for (const path of files(join(root, 'ui/packages/loom-ui/scripts'))) {
  if (/^verify.*\.mjs$/.test(path.split('/').at(-1))) checkBrowserSource(path);
}

console.log(`${mappedCases}/${registry.reduce((total, scenario) => total + scenario.cases.length, 0)} registered cases have native spec mappings`);
for (const problem of problems) console.error(problem);
if (problems.length) process.exitCode = 1;
