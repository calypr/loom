import { createRequire } from 'node:module';
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs';
import { join, dirname, resolve, relative } from 'node:path';

const pkg = resolve(process.argv[2] ?? new URL('../', import.meta.url).pathname);
const json = process.argv.includes('--json');
const require = createRequire(join(pkg, 'package.json'));
const ts = require('typescript');
const src = join(pkg, 'src');
const testSupport = join(pkg, 'testSupport');
const tsconfig = ts.readConfigFile(join(pkg, 'tsconfig.json'), ts.sys.readFile);
const parsedTsconfig = ts.parseJsonConfigFileContent(tsconfig.config, ts.sys, pkg);
const typeProgram = ts.createProgram(parsedTsconfig.fileNames, parsedTsconfig.options);
const checker = typeProgram.getTypeChecker();
const filePaths = [];

function walk(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) walk(path);
    else if (/\.(?:ts|tsx|mts|cts)$/.test(entry.name) && !/\.d\.(?:ts|mts|cts)$/.test(entry.name)) filePaths.push(path);
  }
}

walk(src);
if (existsSync(testSupport)) walk(testSupport);
filePaths.sort();

const isTest = (path) => /\.test\.(?:ts|tsx|mts|cts)$/.test(path);
const isTestImporter = (path) => isTest(path) || path.includes(`${join(pkg, 'testSupport')}/`);
const isProductionSource = (path) => path.startsWith(`${src}/`) && !isTest(path);
const resolveModule = (from, specifier) => {
  if (!specifier.startsWith('.')) return null;
  const base = resolve(dirname(from), specifier);
  for (const candidate of [base, ...['.ts', '.tsx', '.mts', '.cts'].map((ext) => base + ext), ...['index.ts', 'index.tsx', 'index.mts', 'index.cts'].map((name) => join(base, name))]) {
    if (existsSync(candidate) && statSync(candidate).isFile()) return candidate;
  }
  return null;
};

const files = new Map();
for (const path of filePaths) {
  const text = readFileSync(path, 'utf8');
  const source = typeProgram.getSourceFile(path) ?? ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true);
  const exports = new Set();
  const imports = [];
  const reexports = [];
  const dynamicImports = [];
  for (const statement of source.statements) {
    if (ts.isImportDeclaration(statement)) {
      const target = resolveModule(path, statement.moduleSpecifier.text);
      if (!target) continue;
      const names = new Set();
      const clause = statement.importClause;
      if (clause?.name) names.add('default');
      if (clause?.namedBindings) {
        if (ts.isNamespaceImport(clause.namedBindings)) names.add('*');
        else for (const element of clause.namedBindings.elements) names.add((element.propertyName ?? element.name).text);
      }
      imports.push({ target, names });
    } else if (ts.isExportDeclaration(statement)) {
      const target = statement.moduleSpecifier && resolveModule(path, statement.moduleSpecifier.text);
      if (target) {
        if (!statement.exportClause) reexports.push({ target, names: '*' });
        else if (ts.isNamedExports(statement.exportClause)) {
          const names = new Map();
          for (const element of statement.exportClause.elements) {
            names.set(element.name.text, (element.propertyName ?? element.name).text);
            exports.add(element.name.text);
          }
          reexports.push({ target, names });
        }
      } else if (statement.exportClause && ts.isNamedExports(statement.exportClause)) {
        for (const element of statement.exportClause.elements) exports.add(element.name.text);
      }
    } else if (ts.isExportAssignment(statement)) {
      exports.add('default');
    } else {
      const modifiers = ts.canHaveModifiers(statement) ? ts.getModifiers(statement) ?? [] : [];
      if (!modifiers.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword)) continue;
      if (modifiers.some((modifier) => modifier.kind === ts.SyntaxKind.DefaultKeyword)) exports.add('default');
      else if (ts.isVariableStatement(statement)) {
        for (const declaration of statement.declarationList.declarations) if (ts.isIdentifier(declaration.name)) exports.add(declaration.name.text);
      } else if (statement.name) exports.add(statement.name.text);
    }
  }
  const visit = (node) => {
    if (ts.isCallExpression(node) && node.arguments.length && ts.isStringLiteral(node.arguments[0])) {
      const expression = node.expression.getText(source);
      if (node.expression.kind === ts.SyntaxKind.ImportKeyword || /\bvi\.(?:mock|importActual)\b/.test(expression)) {
        const target = resolveModule(path, node.arguments[0].text);
        if (target) dynamicImports.push(target);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  files.set(path, { path, text, source, exports, imports, reexports, dynamicImports });
}

const entries = ['index.ts', 'viewer-entry.ts', 'builder-entry.ts', 'saved-views-entry.ts'].map((name) => join(src, name));
function reachable(roots) {
  const seen = new Set();
  const pending = [...roots];
  while (pending.length) {
    const path = pending.pop();
    if (seen.has(path) || !files.has(path)) continue;
    seen.add(path);
    const file = files.get(path);
    pending.push(...file.imports.map((item) => item.target), ...file.reexports.map((item) => item.target), ...file.dynamicImports);
  }
  return seen;
}

const productionReachable = reachable(entries);
const allReachable = reachable([...entries, ...filePaths.filter(isTestImporter)]);
const usedBy = new Map();
const exportKey = (path, name) => `${path}#${name}`;
function markExport(path, name, kind, depth = 0) {
  if (depth > 50 || !files.has(path)) return;
  const key = exportKey(path, name);
  const kinds = usedBy.get(key) ?? new Set();
  if (kinds.has(kind)) return;
  kinds.add(kind);
  usedBy.set(key, kinds);
  const file = files.get(path);
  for (const reexport of file.reexports) {
    if (reexport.names === '*') {
      if (name === '*') markExport(reexport.target, '*', kind, depth + 1);
      else markExport(reexport.target, name, kind, depth + 1);
    } else if (name === '*') {
      for (const localName of reexport.names.values()) markExport(reexport.target, localName, kind, depth + 1);
    } else if (reexport.names.has(name)) {
      markExport(reexport.target, reexport.names.get(name), kind, depth + 1);
    }
  }
  if (name === '*') for (const exportName of file.exports) markExport(path, exportName, kind, depth + 1);
}

for (const entry of entries) markExport(entry, '*', 'public');
for (const file of files.values()) {
  const kind = isTestImporter(file.path) ? 'test' : 'production';
  for (const item of file.imports) for (const name of item.names) markExport(item.target, name, kind);
  for (const path of file.dynamicImports) markExport(path, '*', kind);
}

const unusedExports = [];
const testOnlyExports = [];
for (const file of files.values()) {
  if (!isProductionSource(file.path) || entries.includes(file.path)) continue;
  for (const name of file.exports) {
    const uses = usedBy.get(exportKey(file.path, name)) ?? new Set();
    if (uses.has('production') || uses.has('public')) continue;
    const kind = uses.has('test') ? 'test-only' : 'unused';
    const row = { path: relative(pkg, file.path), name, kind };
    if (kind === 'test-only') testOnlyExports.push(row);
    else unusedExports.push(row);
  }
}

function findCycles() {
  let nextIndex = 0;
  const index = new Map();
  const low = new Map();
  const stack = [];
  const stacked = new Set();
  const cycles = [];
  const productionFiles = [...files.values()].filter((file) => isProductionSource(file.path));
  const edges = (path) => {
    const file = files.get(path);
    return [...file.imports, ...file.reexports].map((edge) => edge.target).filter((target) => files.has(target) && isProductionSource(target));
  };
  function visit(path) {
    index.set(path, nextIndex);
    low.set(path, nextIndex++);
    stack.push(path);
    stacked.add(path);
    for (const target of edges(path)) {
      if (!index.has(target)) {
        visit(target);
        low.set(path, Math.min(low.get(path), low.get(target)));
      } else if (stacked.has(target)) low.set(path, Math.min(low.get(path), index.get(target)));
    }
    if (low.get(path) !== index.get(path)) return;
    const component = [];
    let member;
    do {
      member = stack.pop();
      stacked.delete(member);
      component.push(relative(pkg, member));
    } while (member !== path);
    if (component.length > 1) cycles.push(component.sort());
  }
  for (const file of productionFiles) if (!index.has(file.path)) visit(file.path);
  return cycles;
}

const layerViolations = [];
const layerOrder = new Map([['contracts', 0], ['client', 1], ['data', 2], ['display', 3], ['features', 4]]);
function outerLayer(path) {
  const parts = relative(src, path).split('/');
  return layerOrder.has(parts[0]) ? parts[0] : null;
}
for (const file of files.values()) {
  if (!isProductionSource(file.path)) continue;
  const fromBuilder = relative(src, file.path).split('/');
  for (const edge of [...file.imports, ...file.reexports, ...file.dynamicImports.map(target => ({ target }))]) {
    const target = files.get(edge.target);
    if (!target || !isProductionSource(target.path)) continue;
    const toBuilder = relative(src, target.path).split('/');
    const fromLayer = outerLayer(file.path);
    const toLayer = outerLayer(target.path);
    if (fromLayer && toLayer && layerOrder.get(toLayer) > layerOrder.get(fromLayer)) {
      layerViolations.push({ from: relative(pkg, file.path), to: relative(pkg, target.path), rule: 'outer-layer direction' });
    }
    if (fromBuilder[0] === 'features' && fromBuilder[1] === 'ExplorerBuilder') {
      const fromPart = fromBuilder[2];
      const toPart = toBuilder[0] === 'features' && toBuilder[1] === 'ExplorerBuilder' ? toBuilder[2] : null;
      if (toPart === 'dom' && fromPart !== 'ui' && fromPart !== 'dom') layerViolations.push({ from: relative(pkg, file.path), to: relative(pkg, target.path), rule: 'Builder DOM imported only by UI' });
      const importedNames = edge.names instanceof Set
        ? [...edge.names]
        : edge.names instanceof Map ? [...edge.names.values()] : [];
      if (fromPart === 'ui' && (toLayer === 'client' || importedNames.some((name) => name === 'LoomClient' || name === 'useLoomClient'))) {
        layerViolations.push({ from: relative(pkg, file.path), to: relative(pkg, target.path), rule: 'UI server access' });
      }
      if (fromPart === 'model' && toPart !== 'model' && !['contracts', 'display'].includes(toLayer)) {
        layerViolations.push({ from: relative(pkg, file.path), to: relative(pkg, target.path), rule: 'Builder model imports only model, contracts, or display' });
      }
      if (fromPart === 'session' && toPart === 'ui') {
        layerViolations.push({ from: relative(pkg, file.path), to: relative(pkg, target.path), rule: 'Builder session cannot import UI' });
      }
    }
    if (fromLayer === 'features' && toLayer === 'features' && fromBuilder[1] !== toBuilder[1]) {
      layerViolations.push({ from: relative(pkg, file.path), to: relative(pkg, target.path), rule: 'feature isolation' });
    }
  }
}

const productionFiles = [...files.values()].filter((file) => isProductionSource(file.path));
const testFiles = [...files.values()].filter((file) => isTest(file.path) && file.path.startsWith(`${src}/`));
const lineCount = (rows) => rows.reduce((total, file) => total + [...file.text].filter((character) => character.charCodeAt(0) === 10).length, 0);
const unreachableFiles = productionFiles.filter((file) => !productionReachable.has(file.path)).map((file) => ({
  path: relative(pkg, file.path),
  reachability: allReachable.has(file.path) ? 'test-only' : 'orphan',
}));

const propBags = new Map();
const optionBags = new Map();
const stalenessSites = [];
const auditStalenessSites = [];
const abortControllerSites = [];
const abortControllerOutsideAllowedSites = [];
const effectSites = [];
const uiServerAccessSites = [];
function visitMetrics(file) {
  if (!isProductionSource(file.path)) return;
  const sourcePath = relative(src, file.path);
  const builderUiPath = sourcePath.startsWith('features/ExplorerBuilder/ui/');
  function visit(node) {
    if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
      const component = node.tagName.getText(file.source);
      const attributes = new Set();
      for (const attribute of node.attributes.properties) {
        if (ts.isJsxAttribute(attribute)) attributes.add(attribute.name.getText(file.source));
        else for (const property of checker.getTypeAtLocation(attribute.expression).getProperties()) attributes.add(property.getName());
      }
      const width = attributes.size;
      const previous = propBags.get(component) ?? 0;
      if (width > previous) propBags.set(component, width);
    }
    if (ts.isFunctionDeclaration(node) || ts.isArrowFunction(node) || ts.isFunctionExpression(node) || ts.isMethodDeclaration(node)) {
      const name = node.name?.getText(file.source)
        ?? (ts.isVariableDeclaration(node.parent) && ts.isIdentifier(node.parent.name) ? node.parent.name.text : '<anonymous>');
      if ((name.startsWith('use') || name === 'actionsFor') && node.parameters.length) {
        const parameter = node.parameters[0];
        const parameterType = checker.getNonNullableType(checker.getTypeAtLocation(parameter.name));
        const isObjectBag = (parameterType.flags & ts.TypeFlags.Object) !== 0
          && !checker.isArrayType(parameterType)
          && !checker.isTupleType(parameterType)
          && parameterType.getCallSignatures().length === 0;
        const argumentName = ts.isIdentifier(parameter.name) ? parameter.name.text : '';
        const width = ts.isObjectBindingPattern(parameter.name)
          ? parameter.name.elements.length
          : isObjectBag && /^(?:options|args|config|dependencies|deps|state)$/i.test(argumentName)
            ? parameterType.getProperties().length
            : 0;
        if (width > (optionBags.get(name) ?? 0)) optionBags.set(name, width);
      }
    }
    const isEquality = ts.isBinaryExpression(node) && [
      ts.SyntaxKind.EqualsEqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsEqualsToken,
      ts.SyntaxKind.EqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsToken,
    ].includes(node.operatorToken.kind);
    const hasIdentityField = (text) => /(?:snapshotToken|draftVersion|draftDigest)/.test(text);
    if (isEquality && (hasIdentityField(node.left.getText(file.source)) || hasIdentityField(node.right.getText(file.source)))) {
      stalenessSites.push({ path: relative(pkg, file.path), line: file.source.getLineAndCharacterOfPosition(node.getStart(file.source)).line + 1, expression: node.getText(file.source).slice(0, 180) });
    }
    if (ts.isNewExpression(node) && node.expression.getText(file.source) === 'AbortController') {
      const site = { path: relative(pkg, file.path), line: file.source.getLineAndCharacterOfPosition(node.getStart(file.source)).line + 1 };
      abortControllerSites.push(site);
      if (!/^src\/(?:client|data)\//.test(site.path)) abortControllerOutsideAllowedSites.push(site);
    }
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
      const name = node.expression.text;
      if (/^(?:useEffect|useLayoutEffect|useInsertionEffect|useDeepCompareEffect|useBrowserSubscription|useCommitLifecycle|useDomSubscription)$/.test(name)) {
        effectSites.push({ path: relative(pkg, file.path), line: file.source.getLineAndCharacterOfPosition(node.getStart(file.source)).line + 1, name });
      }
      if (builderUiPath && name === 'useLoomClient') {
        uiServerAccessSites.push({ path: relative(pkg, file.path), line: file.source.getLineAndCharacterOfPosition(node.getStart(file.source)).line + 1, name });
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(file.source);
  for (const [index, line] of file.text.split(String.fromCharCode(10)).entries()) {
    if (/(?:snapshotToken|draftVersion|draftDigest) *[!=]==/.test(line)) {
      auditStalenessSites.push({ path: relative(pkg, file.path), line: index + 1, expression: line.trim().slice(0, 180) });
    }
  }
  const relativePath = relative(pkg, file.path);
  for (const statement of file.source.statements) {
    if (!ts.isImportDeclaration(statement)) continue;
    const names = [];
    const clause = statement.importClause;
    if (clause?.name?.text === 'LoomClient') names.push('LoomClient');
    if (clause?.namedBindings && ts.isNamedImports(clause.namedBindings)) {
      for (const binding of clause.namedBindings.elements) {
        const name = (binding.propertyName ?? binding.name).text;
        if (name === 'LoomClient' || name === 'useLoomClient') names.push(name);
      }
    }
    const importedModule = resolveModule(file.path, statement.moduleSpecifier.text);
    if (builderUiPath && importedModule && outerLayer(importedModule) === 'client') names.push('client/');
    if (builderUiPath && names.length) {
      uiServerAccessSites.push({ path: relativePath, line: file.source.getLineAndCharacterOfPosition(statement.getStart(file.source)).line + 1, name: names.join(', ') });
    }
  }
}
for (const file of files.values()) visitMetrics(file);

const cycles = findCycles();
const directoryEdges = new Map();
for (const file of productionFiles) {
  const fromDirectory = relative(src, dirname(file.path)) || '.';
  for (const edge of [...file.imports, ...file.reexports]) {
    if (!files.has(edge.target) || !isProductionSource(edge.target)) continue;
    const toDirectory = relative(src, dirname(edge.target)) || '.';
    if (fromDirectory === toDirectory) continue;
    const key = `${fromDirectory} -> ${toDirectory}`;
    directoryEdges.set(key, (directoryEdges.get(key) ?? 0) + 1);
  }
}
const byWidth = (map) => [...map].map(([name, count]) => ({ name, count })).sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
const identityOwner = /^src\/(?:contracts\/identity\.ts$|data\/|features\/ExplorerBuilder\/model\/)/;
const identitySitesOutsideOwners = [...new Map(
  [...stalenessSites, ...auditStalenessSites].filter(({ path }) => !identityOwner.test(path))
    .map(site => [`${site.path}:${site.line}`, site]),
).values()];
const before = JSON.parse(readFileSync(join(pkg, 'scripts', 'graph-ratchet-baseline.json'), 'utf8'));
const report = {
  source: relative(process.cwd(), pkg),
  before,
  targets: {
    unreachableProductionFiles: 0, unusedExports: 0, importCycles: 0, layerViolations: 0,
    stalenessComparisons: 0, abortControllersOutsideClientData: 0, uiServerAccessSites: 0,
    effectLikeSites: 4, widestPropBag: 10, widestOptionBag: 8,
  },
  metrics: {
    productionLines: lineCount(productionFiles),
    testLines: lineCount(testFiles),
    unreachableProductionFiles: unreachableFiles.length,
    unreachableFiles,
    unusedExports: unusedExports.length,
    unusedExportSites: unusedExports.sort((a, b) => a.path.localeCompare(b.path) || a.name.localeCompare(b.name)),
    testOnlyExports: testOnlyExports.length,
    testOnlyExportSites: testOnlyExports.sort((a, b) => a.path.localeCompare(b.path) || a.name.localeCompare(b.name)),
    importCycles: cycles.length,
    cycles,
    layerViolations: layerViolations.length,
    layerViolationSites: layerViolations,
    widestPropBags: byWidth(propBags).slice(0, 10),
    widestOptionBags: byWidth(optionBags).slice(0, 10),
    widePropBags: byWidth(propBags).filter(({ count }) => count > 10),
    wideOptionBags: byWidth(optionBags).filter(({ count }) => count > 8),
    widestPropBag: Math.max(0, ...propBags.values()),
    widestOptionBag: Math.max(0, ...optionBags.values()),
    stalenessComparisons: auditStalenessSites.length,
    stalenessComparisonSites: auditStalenessSites,
    structuralIdentityComparisons: stalenessSites.length,
    stalenessSites,
    identityComparisonsOutsideOwners: identitySitesOutsideOwners.length,
    identityComparisonLocationsOutsideOwners: identitySitesOutsideOwners,
    abortControllerSites: abortControllerSites.length,
    abortControllerLocations: abortControllerSites,
    abortControllersOutsideClientData: abortControllerOutsideAllowedSites.length,
    abortControllerOutsideClientDataLocations: abortControllerOutsideAllowedSites,
    effectLikeSites: effectSites.length,
    effectSites,
    uiServerAccessSites: uiServerAccessSites.length,
    uiServerAccessLocations: uiServerAccessSites,
    directoryEdges: [...directoryEdges].map(([edge, count]) => ({ edge, count })).sort((a, b) => b.count - a.count || a.edge.localeCompare(b.edge)),
  },
};

if (json) {
  console.log(JSON.stringify(report, null, 2));
} else {
  const metrics = report.metrics;
  console.log(`Loom UI graph audit for ${pkg}`);
  console.log(`Production/test lines: ${metrics.productionLines} / ${metrics.testLines}`);
  console.log(`Unreachable production files: ${metrics.unreachableProductionFiles} (${metrics.unreachableFiles.filter((file) => file.reachability === 'orphan').length} orphan, ${metrics.unreachableFiles.filter((file) => file.reachability === 'test-only').length} test-only)`);
  console.log(`Unused exports: ${metrics.unusedExports}; test-only exports: ${metrics.testOnlyExports}`);
  console.log(`Production import cycles: ${metrics.importCycles}; layer violations: ${metrics.layerViolations}`);
  console.log(`Staleness comparisons: ${metrics.stalenessComparisons}; AbortController sites: ${metrics.abortControllerSites}; effect-like sites: ${metrics.effectLikeSites}; UI server-access sites: ${metrics.uiServerAccessSites}`);
  console.log(`Targets: unreachable/unused/cycles/layers/staleness/UI access=0; effect-like sites=4; widest props/options=10/8.`);
  console.log('Widest prop bags:', metrics.widestPropBags.map(({ name, count }) => `${name}=${count}`).join(', '));
  console.log('Widest option bags:', metrics.widestOptionBags.map(({ name, count }) => `${name}=${count}`).join(', '));
  console.log('Top directory edges:', metrics.directoryEdges.slice(0, 20).map(({ edge, count }) => `${count} ${edge}`).join(', '));
  console.log('Production cycles:', metrics.cycles);
  if (metrics.unreachableFiles.length) console.log('Unreachable files:', metrics.unreachableFiles);
  if (metrics.unusedExportSites.length) console.log('Unused exports:', metrics.unusedExportSites);
  if (metrics.layerViolationSites.length) console.log('Layer violations:', metrics.layerViolationSites);
}
