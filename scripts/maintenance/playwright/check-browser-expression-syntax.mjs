#!/usr/bin/env node

import { access, readFile } from 'node:fs/promises';
import { constants } from 'node:fs';
import { resolve, dirname } from 'node:path';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const driverPath = process.argv[2];
if (!driverPath) {
  console.error('Usage: node scripts/maintenance/playwright/check-browser-expression-syntax.mjs <driver.mjs>');
  process.exit(2);
}

const absoluteDriverPath = resolve(driverPath);
const source = await readFile(absoluteDriverPath, 'utf8');

const existingFile = async path => {
  try {
    await access(path, constants.R_OK);
    return true;
  } catch {
    return false;
  }
};

const packageCandidates = [
  resolve(dirname(absoluteDriverPath), '../ui/package.json'),
  resolve(process.cwd(), 'ui/package.json'),
  resolve(dirname(fileURLToPath(import.meta.url)), '../../../ui/package.json'),
];
let packagePath;
for (const candidate of packageCandidates) {
  if (await existingFile(candidate)) {
    packagePath = candidate;
    break;
  }
}
if (!packagePath) {
  console.error('Could not find an existing ui/package.json to resolve @babel/parser.');
  process.exit(2);
}

const require = createRequire(packagePath);
let parse;
try {
  ({ parse } = require('@babel/parser'));
} catch (error) {
  console.error(`Could not load existing @babel/parser dependency: ${error.message}`);
  process.exit(2);
}

const ast = parse(source, { sourceType: 'module' });
const expressions = [];
const unsupported = [];
const constantsByName = new Map();

const collectConstants = node => {
  if (!node || typeof node !== 'object') return;
  if (node.type === 'VariableDeclaration') {
    for (const declaration of node.declarations) {
      if (declaration.id.type !== 'Identifier') continue;
      const declarations = constantsByName.get(declaration.id.name) ?? [];
      declarations.push({ kind: node.kind, init: declaration.init });
      constantsByName.set(declaration.id.name, declarations);
    }
  }
  for (const [key, value] of Object.entries(node)) {
    if (key === 'loc' || key === 'start' || key === 'end' || key === 'extra') continue;
    if (Array.isArray(value)) value.forEach(collectConstants);
    else if (value && typeof value === 'object') collectConstants(value);
  }
};
collectConstants(ast);

const constantStringFragments = (node, resolving = new Set()) => {
  if (!node) return undefined;
  if (node.type === 'StringLiteral') return [node.value];
  if (node.type === 'TemplateLiteral') {
    let variants = [''];
    for (let index = 0; index < node.quasis.length; index += 1) {
      const quasi = node.quasis[index];
      if (quasi.value.cooked === null) return undefined;
      variants = variants.map(value => value + quasi.value.cooked);
      const interpolation = node.expressions[index];
      if (!interpolation) continue;
      // Dynamic values inside a fragment are syntax-safe source placeholders.
      const inserted = constantStringFragments(interpolation, resolving) ?? ['0'];
      variants = variants.flatMap(value => inserted.map(part => value + part));
    }
    return variants;
  }
  if (node.type === 'ConditionalExpression') {
    const consequent = constantStringFragments(node.consequent, resolving);
    const alternate = constantStringFragments(node.alternate, resolving);
    return consequent && alternate ? [...consequent, ...alternate] : undefined;
  }
  if (node.type === 'Identifier' && !resolving.has(node.name)) {
    const declarations = constantsByName.get(node.name);
    if (declarations?.length !== 1 || declarations[0].kind !== 'const' || !declarations[0].init) return undefined;
    const next = new Set(resolving);
    next.add(node.name);
    return constantStringFragments(declarations[0].init, next);
  }
  return undefined;
};

const sourceFromArgument = argument => {
  if (argument.type === 'StringLiteral') return [argument.value];
  if (argument.type !== 'TemplateLiteral') return undefined;

  let variants = [''];
  for (let index = 0; index < argument.quasis.length; index += 1) {
    const quasi = argument.quasis[index];
    if (quasi.value.cooked === null) {
      throw new Error(`Template literal at line ${argument.loc.start.line} has an invalid escape.`);
    }
    variants = variants.map(value => value + quasi.value.cooked);

    const interpolation = argument.expressions[index];
    if (!interpolation) continue;
    // Resolve const string fragments structurally so their own source is also
    // syntax-checked. Ordinary runtime values use a syntax-safe placeholder.
    const inserted = constantStringFragments(interpolation) ?? ['0'];
    variants = variants.flatMap(value => inserted.map(part => value + part));
  }
  return variants;
};

const visit = node => {
  if (!node || typeof node !== 'object') return;
  if (node.type === 'CallExpression' && node.callee.type === 'Identifier' &&
      ['browserEval', 'waitForBrowser'].includes(node.callee.name)) {
    const name = node.callee.name;
    const argument = node.arguments[1];
    if (!argument) {
      unsupported.push({ name, line: node.loc?.start.line ?? 0, reason: 'missing JavaScript expression argument' });
    } else {
      const bodies = sourceFromArgument(argument);
      if (bodies === undefined) {
        unsupported.push({ name, line: node.loc?.start.line ?? 0, reason: `expression is ${argument.type}, not a static string or template` });
      } else {
        for (const body of bodies) {
          const expression = name === 'browserEval'
            ? `(async()=>{${body}})()`
            : `Boolean((${body}))`;
          expressions.push({ name, line: node.loc?.start.line ?? 0, expression });
        }
      }
    }
  }

  for (const [key, value] of Object.entries(node)) {
    if (key === 'loc' || key === 'start' || key === 'end' || key === 'extra') continue;
    if (Array.isArray(value)) value.forEach(visit);
    else if (value && typeof value === 'object') visit(value);
  }
};

visit(ast);
const errors = [];
for (const item of expressions) {
  try {
    new vm.Script(item.expression, { filename: `${absoluteDriverPath}:${item.line}` });
  } catch (error) {
    errors.push({ line: item.line, name: item.name, message: error.message });
  }
}

const callSites = new Set(expressions.map(item => `${item.name}:${item.line}`)).size;
console.log(`${callSites} browserEval/waitForBrowser call sites; ${expressions.length} generated expressions compiled from ${absoluteDriverPath}`);
for (const item of unsupported) console.error(`${absoluteDriverPath}:${item.line}: ${item.name}: ${item.reason}`);
for (const item of errors) console.error(`${absoluteDriverPath}:${item.line}: ${item.name}: ${item.message}`);
if (unsupported.length || errors.length || expressions.length === 0) process.exitCode = 1;
