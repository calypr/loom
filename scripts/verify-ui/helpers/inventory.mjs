import { readdirSync, readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { registry, coverageDrift as registryDrift } from '../registry.mjs';

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
export const repositoryRoot = resolve(scriptDirectory, '../..');
const localRequire = createRequire(join(repositoryRoot, 'ui/package.json'));
const ts = localRequire('typescript');
let postcss;
try { postcss = localRequire('postcss'); } catch { postcss = undefined; }

const sourceRoots = ['ui/packages/loom-ui/src', 'ui/apps/demo/src'];
const excludedSource = (path) => /\.d\.ts$|\.unit\.test\.|\.test\.|\.spec\.|\.stories\.|\/__tests__\//i.test(path);

const listFiles = (directory) => {
  if (!existsSync(directory)) return [];
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? listFiles(path) : [path];
  });
};

const sourceFiles = () => sourceRoots.flatMap((root) => listFiles(join(repositoryRoot, root)))
  .filter((path) => /\.(?:ts|tsx|js|jsx|css)$/.test(path) && !excludedSource(path))
  .sort();

const textForNode = (sourceFile, node) => sourceFile.text.slice(node.getStart(sourceFile), node.getEnd()).replace(/\s+/g, ' ').trim().slice(0, 360);
const nodeLocation = (sourceFile, node) => {
  const position = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
  return { line: position.line + 1, column: position.character + 1 };
};
const propertyName = (node) => ts.isIdentifier(node) || ts.isStringLiteral(node) || ts.isNumericLiteral(node) ? node.text : node.getText();
const expressionName = (node) => ts.isIdentifier(node) ? node.text : ts.isPropertyAccessExpression(node) ? node.name.text : '';

const componentName = (node) => {
  for (let current = node.parent; current; current = current.parent) {
    if ((ts.isFunctionDeclaration(current) || ts.isMethodDeclaration(current)) && current.name) {
      const name = propertyName(current.name);
      if (/^[A-Z]/.test(name)) return name;
    }
    if (ts.isVariableDeclaration(current) && current.initializer && ts.isIdentifier(current.name) && /^[A-Z]/.test(current.name.text) && (ts.isArrowFunction(current.initializer) || ts.isFunctionExpression(current.initializer))) return current.name.text;
  }
  return null;
};

const containsJSX = (node) => {
  if (ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node) || ts.isJsxFragment(node)) return true;
  let found = false;
  ts.forEachChild(node, (child) => { if (!found && containsJSX(child)) found = true; });
  return found;
};

const branchReturnsEarly = (node) => {
  if (ts.isReturnStatement(node) || ts.isThrowStatement(node)) return true;
  if (ts.isFunctionLike(node)) return false;
  let found = false;
  ts.forEachChild(node, (child) => { if (!found && branchReturnsEarly(child)) found = true; });
  return found;
};

const hasEmptyRenderReturn = (node) => {
  if (ts.isReturnStatement(node)) return !node.expression || node.expression.kind === ts.SyntaxKind.NullKeyword || (ts.isIdentifier(node.expression) && node.expression.text === 'false');
  if (ts.isFunctionLike(node)) return false;
  let found = false;
  ts.forEachChild(node, (child) => { if (!found && hasEmptyRenderReturn(child)) found = true; });
  return found;
};

const walk = (node, visit) => {
  visit(node);
  ts.forEachChild(node, (child) => walk(child, visit));
};

const recordFor = (sourceFile, relativePath, node, kind, fields = {}) => ({
  kind, file: relativePath, ...nodeLocation(sourceFile, node), component: componentName(node),
  expression: textForNode(sourceFile, node), ...fields,
});

export const scanTypeScriptSource = ({ path, root = repositoryRoot }) => {
  const absolutePath = resolve(path);
  const source = readFileSync(absolutePath, 'utf8');
  const scriptKind = /\.(?:tsx|jsx)$/.test(absolutePath) ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const sourceFile = ts.createSourceFile(absolutePath, source, ts.ScriptTarget.Latest, true, scriptKind);
  const relativePath = relative(root, absolutePath).split('\\').join('/');
  const records = [];
  const seenHandlers = new Set();

  const inspectHandler = (functionNode, handlerName, includeNestedCallbacks = false) => {
    if (!functionNode.body || seenHandlers.has(functionNode)) return;
    seenHandlers.add(functionNode);
    const visitHandler = (node) => {
      if (node !== functionNode.body && ts.isFunctionLike(node) && !includeNestedCallbacks) return;
      if (ts.isIfStatement(node) && (branchReturnsEarly(node.thenStatement) || (node.elseStatement && branchReturnsEarly(node.elseStatement)))) {
        records.push(recordFor(sourceFile, relativePath, node, 'imperative-event-guard', { handler: handlerName, gate: textForNode(sourceFile, node.expression) }));
      }
      ts.forEachChild(node, visitHandler);
    };
    visitHandler(functionNode.body);
  };

  walk(sourceFile, (node) => {
    if (ts.isJsxAttribute(node)) {
      const name = propertyName(node.name);
      if (name.startsWith('data-')) records.push(recordFor(sourceFile, relativePath, node, 'data-hook', { hook: name, value: node.initializer ? textForNode(sourceFile, node.initializer) : 'true' }));
      if (name === 'disabled' || name === 'aria-disabled') records.push(recordFor(sourceFile, relativePath, node, 'jsx-interaction-gate', { gate: name, value: node.initializer ? textForNode(sourceFile, node.initializer) : 'true' }));
      if (name === 'style' && node.initializer && /pointerEvents\s*:\s*['"]none['"]/.test(node.initializer.getText(sourceFile))) records.push(recordFor(sourceFile, relativePath, node, 'jsx-pointer-gate', { gate: 'pointerEvents:none' }));
      if (name === 'className' && node.initializer && /pointer-events-none|disabled:/.test(node.initializer.getText(sourceFile))) records.push(recordFor(sourceFile, relativePath, node, 'jsx-interaction-style', { gate: 'utility class' }));
      if (/^on[A-Z]/.test(name) && node.initializer && ts.isJsxExpression(node.initializer)) {
        const expression = node.initializer.expression;
        if (expression && (ts.isArrowFunction(expression) || ts.isFunctionExpression(expression))) inspectHandler(expression, name);
      }
    }
    if (ts.isPropertyAssignment(node) && propertyName(node.name) === 'pointerEvents' && /['"]none['"]/.test(node.initializer.getText(sourceFile))) records.push(recordFor(sourceFile, relativePath, node, 'pointer-style-gate', { gate: 'pointerEvents:none' }));
    if (ts.isCallExpression(node)) {
      const name = expressionName(node.expression);
      if (/^use[A-Za-z0-9_]*(?:Query|Mutation|Rows|Output|Runtime)$/.test(name)) records.push(recordFor(sourceFile, relativePath, node, 'data-hook-call', { hook: name }));
      if (ts.isPropertyAccessExpression(node.expression)) {
        const receiver = node.expression.expression;
        const owner = ts.isIdentifier(receiver) ? receiver.text : '';
        if (/client|api/i.test(owner)) records.push(recordFor(sourceFile, relativePath, node, 'client-api-call', { client: owner, method: name }));
      }
      if (ts.isIdentifier(node.expression) && ['fetch', 'axios'].includes(name)) records.push(recordFor(sourceFile, relativePath, node, 'direct-api-call', { method: name }));
    }
    if (ts.isConditionalExpression(node) && (containsJSX(node.whenTrue) || containsJSX(node.whenFalse))) records.push(recordFor(sourceFile, relativePath, node, 'conditional-render', { condition: textForNode(sourceFile, node.condition) }));
    if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken && containsJSX(node.right)) records.push(recordFor(sourceFile, relativePath, node, 'conditional-render', { condition: textForNode(sourceFile, node.left) }));
    if (ts.isIfStatement(node) && componentName(node) && (hasEmptyRenderReturn(node.thenStatement) || (node.elseStatement && hasEmptyRenderReturn(node.elseStatement)))) records.push(recordFor(sourceFile, relativePath, node, 'conditional-empty-render', { condition: textForNode(sourceFile, node.expression) }));
    if ((ts.isFunctionDeclaration(node) || ts.isMethodDeclaration(node)) && node.name) {
      const name = propertyName(node.name);
      if (/^(handle|on)[A-Z]/.test(name) || componentName(node)) inspectHandler(node, name);
    }
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer && (ts.isArrowFunction(node.initializer) || ts.isFunctionExpression(node.initializer)) && (/^(handle|on)[A-Z]/.test(node.name.text) || componentName(node))) inspectHandler(node.initializer, node.name.text);
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer && ts.isCallExpression(node.initializer) && expressionName(node.initializer.expression) === 'useCallback') {
      const callback = node.initializer.arguments[0];
      if (callback && (ts.isArrowFunction(callback) || ts.isFunctionExpression(callback))) inspectHandler(callback, node.name.text, true);
    }
  });

  return records;
};

const scanCss = (path) => {
  const source = readFileSync(path, 'utf8');
  const relativePath = relative(repositoryRoot, path).split('\\').join('/');
  const records = [];
  const add = (line, selector, property, value) => records.push({
    kind: 'css-interaction-gate', file: relativePath, line, column: 1, component: null,
    gate: property + ':' + value, selector, expression: selector + ' {' + property + ':' + value + '}',
  });
  if (!postcss) {
    for (const match of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      if (/pointer-events\s*:\s*none/i.test(match[2])) add(source.slice(0, match.index).split('\n').length, match[1].trim(), 'pointer-events', 'none');
    }
    return records;
  }
  postcss.parse(source, { from: path }).walkRules((rule) => {
    rule.walkDecls((declaration) => {
      if (declaration.prop.toLowerCase() === 'pointer-events' && declaration.value.toLowerCase() === 'none') add(declaration.source?.start?.line ?? rule.source?.start?.line ?? 1, rule.selector, declaration.prop, declaration.value);
    });
  });
  return records;
};

export const inventoryCoverageDrift = (records, scenarios = registry) => {
  const productionHooks = records.filter((record) => record.kind === 'data-hook-call' && !(record.file?.endsWith('/react.tsx') && ['useQuery', 'useMutation'].includes(record.hook)));
  const availableHooks = new Set(productionHooks.map((record) => record.hook));
  const registeredHooks = new Set(scenarios.flatMap((scenario) => scenario.hooks));
  return [
    ...registryDrift(scenarios),
    ...scenarios.flatMap((scenario) => scenario.hooks.filter((hook) => !availableHooks.has(hook)).map((hook) => scenario.id + ': missing source hook ' + hook)),
    ...[...availableHooks].filter((hook) => !registeredHooks.has(hook)).map((hook) => 'unregistered production data hook ' + hook),
  ].sort();
};

export const buildInventory = () => {
  const files = sourceFiles();
  const records = [];
  for (const path of files) {
    if (path.endsWith('.css')) records.push(...scanCss(path));
    else records.push(...scanTypeScriptSource({ path }));
  }
  records.sort((left, right) => left.file.localeCompare(right.file) || left.line - right.line || left.column - right.column || left.kind.localeCompare(right.kind) || String(left.expression).localeCompare(String(right.expression)));
  const counts = Object.fromEntries([...new Set(records.map((record) => record.kind))].sort().map((kind) => [kind, records.filter((record) => record.kind === kind).length]));
  return {
    schemaVersion: 1,
    analysis: 'TypeScript Compiler API plus PostCSS source scan. Static findings do not prove runtime reachability or completeness.',
    limitations: [
      'Dynamic component construction, runtime style injection, computed class names, and indirect event dispatch may not be found.',
      'Conditional render branches and early-return guards are syntactic candidates, not proof of runtime visibility or reachability.',
      'Every interaction requires separate browser evidence; source inventory status never implies a runtime pass.',
    ],
    sourceRoots, sourceFileCount: files.length, recordCount: records.length, counts,
    registryCoverageDrift: inventoryCoverageDrift(records), records,
  };
};

export const renderInventoryMarkdown = (inventory) => {
  const tick = String.fromCharCode(96);
  const lines = [
    '# Loom UI interaction inventory', '',
    'Generated from production TypeScript/TSX/JavaScript/CSS source. Static findings are navigation evidence, not proof of runtime reachability, completeness, or usable behavior.',
    '', 'No runtime pass is inferred from this inventory.', '', '## Counts', '',
    '| Source files | Records | Registry drift |', '| ---: | ---: | ---: |',
    '| ' + inventory.sourceFileCount + ' | ' + inventory.recordCount + ' | ' + inventory.registryCoverageDrift.length + ' |', '',
    '| Kind | Count |', '| --- | ---: |',
    ...Object.entries(inventory.counts).map(([kind, count]) => '| ' + kind + ' | ' + count + ' |'),
    '', '## Records', '', '| Kind | Source | Component | Subject | Expression |', '| --- | --- | --- | --- | --- |',
    ...inventory.records.map((record) => {
      const subject = record.hook ?? record.gate ?? record.method ?? record.selector ?? '';
      return '| ' + record.kind + ' | ' + tick + record.file + ':' + record.line + tick + ' | ' + (record.component ?? '') + ' | ' + String(subject).replace(/\|/g, '\\|') + ' | ' + tick + String(record.expression).replace(/\|/g, '\\|').replaceAll(tick, "'") + tick + ' |';
    }),
    '', '## Limitations', '', ...inventory.limitations.map((limitation) => '- ' + limitation), '',
  ];
  return lines.join('\n');
};

const defaultJSON = join(repositoryRoot, 'docs/UI_INTERACTION_INVENTORY.json');
const defaultMarkdown = join(repositoryRoot, 'docs/UI_INTERACTION_INVENTORY.md');

const cli = () => {
  const args = process.argv.slice(2);
  let check = false, jsonPath = defaultJSON, markdownPath = defaultMarkdown;
  for (let index = 0; index < args.length; index += 1) {
    const value = args[index];
    if (value === '--check') check = true;
    else if (value === '--json' || value === '--markdown') {
      const next = args[index + 1];
      if (!next || next.startsWith('--')) throw new Error(value + ' requires a path');
      index += 1;
      if (value === '--json') jsonPath = resolve(next);
      else markdownPath = resolve(next);
    } else throw new Error('unknown inventory option: ' + value);
  }
  const inventory = buildInventory();
  const json = JSON.stringify(inventory, null, 2) + '\n';
  const markdown = renderInventoryMarkdown(inventory);
  if (check) {
    const missing = [jsonPath, markdownPath].filter((path) => !existsSync(path));
    const drift = missing.length || readFileSync(jsonPath, 'utf8') !== json || readFileSync(markdownPath, 'utf8') !== markdown;
    if (drift) {
      console.error('UI inventory is missing or stale. Regenerate with: node scripts/verify-ui/helpers/inventory.mjs --json ' + jsonPath + ' --markdown ' + markdownPath);
      process.exitCode = 1;
      return;
    }
    if (inventory.registryCoverageDrift.length) {
      console.error(inventory.registryCoverageDrift.join('\n'));
      process.exitCode = 1;
      return;
    }
    console.log('UI interaction inventory is current (' + inventory.recordCount + ' records).');
    return;
  }
  for (const path of [jsonPath, markdownPath]) mkdirSync(dirname(path), { recursive: true });
  writeFileSync(jsonPath, json, { mode: 0o644 });
  writeFileSync(markdownPath, markdown, { mode: 0o644 });
  if (inventory.registryCoverageDrift.length) {
    console.error(inventory.registryCoverageDrift.join('\n'));
    process.exitCode = 1;
  }
  console.log('Wrote ' + inventory.recordCount + ' interaction records to ' + jsonPath + ' and ' + markdownPath + '.');
};

if (import.meta.url === new URL(process.argv[1] ?? '', 'file:').href) {
  try { cli(); } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
