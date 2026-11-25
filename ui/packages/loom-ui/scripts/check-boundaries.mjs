import { execFileSync } from 'node:child_process';
import { readFile, readdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join, relative } from 'node:path';
import ts from 'typescript';
import { checkPublicApi } from './public-api.mjs';

const scriptDirectory = new URL('.', import.meta.url);
const packageRoot = fileURLToPath(new URL('../', scriptDirectory));
const sourceRoot = new URL('../src/', scriptDirectory);
const forbidden = /(?:@gen3\/core|@gen3\/frontend|from ['"]next(?:\/|['"])|ProtectedContent|CalyprAuth)/;
const effectNames = /\b(?:useEffect|useLayoutEffect|useInsertionEffect|useDeepCompareEffect)\b/;
const effectCalls = /\b(?:useEffect|useLayoutEffect|useInsertionEffect|useDeepCompareEffect)\s*\(/g;
const effectBaseline = JSON.parse(await readFile(new URL('./useeffect-baseline.json', scriptDirectory), 'utf8'));
const graphBaseline = JSON.parse(await readFile(new URL('./graph-ratchet-baseline.json', scriptDirectory), 'utf8'));
const graphPath = join(packageRoot, 'scripts', 'graph-audit.mjs');
const graph = JSON.parse(execFileSync(process.execPath, [graphPath, packageRoot, '--json'], { encoding: 'utf8' }));
const files = [];

const walk = async (url) => {
  for (const entry of await readdir(url, { withFileTypes: true })) {
    const child = new URL(entry.name + (entry.isDirectory() ? '/' : ''), url);
    if (entry.isDirectory()) await walk(child);
    else if (/\.(ts|tsx)$/.test(entry.name)) files.push(child);
  }
};
await walk(sourceRoot);

const violations = [];
for (const file of files) {
  const source = await readFile(file, 'utf8');
  const syntax = ts.createSourceFile(file.pathname, source, ts.ScriptTarget.Latest, false);
  for (const statement of syntax.statements) {
    if (!ts.isImportDeclaration(statement) || statement.moduleSpecifier.text !== 'react') continue;
    const bindings = statement.importClause?.namedBindings;
    if (!bindings || !ts.isNamedImports(bindings)) continue;
    const importedEffects = bindings.elements
      .map((binding) => (binding.propertyName ?? binding.name).text)
      .filter((name) => effectNames.test(name));
    if (importedEffects.length) {
      violations.push(`${file.pathname}: React effect import(s) ${importedEffects.join(', ')} are forbidden. Derive state during rendering or update it through its owning action.`);
    }
  }
  if (forbidden.test(source)) violations.push(`${file.pathname}: forbidden Calypr dependency`);
  if (!/\.test\.tsx?$/.test(file.pathname) && /\bimport\s*\.\s*meta\s*\.\s*env\b/.test(source)) {
    violations.push(`${file.pathname}: Vite environment access belongs in the host app; pass configuration through props`);
  }
  const isViewerFeature = file.pathname.includes('/features/ExplorerViewer/') || /\/Viewer\.tsx$/.test(file.pathname);
  if (isViewerFeature && effectNames.test(source)) violations.push(`${file.pathname}: Viewer code must not use React effects`);
  if (!isViewerFeature && !/\.test\.tsx?$/.test(file.pathname)) {
    const key = relative(packageRoot, file.pathname);
    const found = (source.match(effectCalls) ?? []).length;
    const allowed = effectBaseline[key] ?? 0;
    if (found !== allowed) {
      violations.push(`${key}: ${found} React effect call(s), baseline is ${allowed}. Keep useeffect-baseline.json aligned with actual reductions.`);
    }
  }
}

const ratchets = [
  ['unreachableProductionFiles', 'Unreachable production files'],
  ['unusedExports', 'Unused exports'],
  ['importCycles', 'Production import cycles'],
  ['effectLikeSites', 'React effect and external-subscription call sites'],
  ['stalenessComparisons', 'Identity comparison lines'],
  ['identityComparisonsOutsideOwners', 'Identity comparisons outside contracts, data, and model'],
  ['abortControllerSites', 'AbortController sites'],
  ['abortControllersOutsideClientData', 'AbortController sites outside client and data'],
  ['uiServerAccessSites', 'Builder client-access sites'],
  ['widestPropBag', 'Widest component prop bag'],
  ['widestOptionBag', 'Widest hook option bag'],
];
for (const [key, label] of ratchets) {
  const current = graph.metrics[key];
  const maximum = graphBaseline[key];
  if (current > maximum) violations.push(`${label}: ${current}, ratchet allows at most ${maximum}.`);
}
for (const [metric, label] of [['widePropBags', 'JSX attribute bag'], ['wideOptionBags', 'Hook option bag']]) {
  const exceptions = graphBaseline[metric] ?? {};
  const limit = metric === 'widePropBags' ? 10 : 8;
  for (const { name, count } of graph.metrics[metric]) {
    const maximum = Object.hasOwn(exceptions, name) ? exceptions[name] : limit;
    if (count > maximum) violations.push(`${label} ${name}: ${count}, ratchet allows at most ${maximum}.`);
  }
}
if (graphBaseline.enforceLayers && graph.metrics.layerViolations > graphBaseline.layerViolations) {
  violations.push(`Layer violations: ${graph.metrics.layerViolations}, ratchet allows at most ${graphBaseline.layerViolations}.`);
}
if (graphBaseline.enforceUiServerAccess && graph.metrics.uiServerAccessSites > 0) {
  violations.push(`Builder client-access sites: ${graph.metrics.uiServerAccessSites}; Builder UI must not import client/, LoomClient, or useLoomClient.`);
}
if (graphBaseline.enforceTestOnlyExportLayers) {
  const invalidExports = graph.metrics.testOnlyExportSites.filter((site) => !/\/features\/ExplorerBuilder\/(?:model|session)\//.test(site.path));
  if (invalidExports.length) {
    violations.push(`Test-only exports must live in Builder model or session modules: ${invalidExports.map(({ path, name }) => `${path}#${name}`).join(', ')}`);
  }
}
if (graphBaseline.enforceDomSubscriptionAllowlist) {
  const subscriptionSites = graph.metrics.effectSites.filter((site) => site.name === 'useDomSubscription');
  const actual = subscriptionSites.map(site => site.path).sort();
  const allowed = [...graphBaseline.domSubscriptionAllowlist].sort();
  const invalidSites = graph.metrics.effectSites.filter(site =>
    site.name !== 'useDomSubscription' || !site.path.startsWith('src/features/ExplorerBuilder/dom/'));
  if (invalidSites.length || JSON.stringify(actual) !== JSON.stringify(allowed)) {
    violations.push(`DOM subscription allowlist differs. Expected ${allowed.join(', ')}. Found ${graph.metrics.effectSites.map(({ path, line, name }) => `${path}:${line} ${name}`).join(', ')}.`);
  }
}

try {
  await checkPublicApi(packageRoot);
} catch (error) {
  violations.push(error instanceof Error ? error.message : String(error));
}

if (violations.length) {
  console.error(violations.join('\n'));
  process.exit(1);
}
console.log(`Loom UI boundary check passed (${files.length} TypeScript files).`);
