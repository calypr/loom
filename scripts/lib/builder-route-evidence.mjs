import { readFileSync, readdirSync, statSync } from 'node:fs';
import { basename, dirname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

export function readJson(path) {
  return JSON.parse(readFileSync(resolve(ROOT, path), 'utf8'));
}

export function uniqueSorted(values) {
  return [...new Set(values.filter(Boolean))].sort();
}

export function routeKey(route) {
  return String(route.method).toUpperCase() + ' ' + String(route.path);
}

export function isCurrentBrowserEvidence(entry) {
  return entry?.status === 'current' && entry?.sourceStatus === 'current';
}

export function routeSuffix(path) {
  const segments = String(path).split('/').filter(Boolean);
  const suffix = segments.at(-1) ?? '';
  return /^\{[^}]+\}$/.test(suffix) ? null : suffix;
}

export function routePathPattern(path) {
  return String(path)
    .split('/')
    .map((segment) => (/^\{[^}]+\}$/.test(segment) ? '[^/]+' : segment.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    .join('/');
}

const READ_LIKE_POSTS = [
  /\/builder$/,
  /\/capability$/,
  /\/capabilities$/,
  /\/column-source$/,
  /\/configured-column-context$/,
  /\/construction-capabilities$/,
  /\/construction-choices$/,
  /\/construction-inputs$/,
  /\/construction-proposals$/,
  /\/construction-category-discoveries$/,
  /\/construction-choice-proposals$/,
  /\/frame-source-options$/,
  /\/interpretation-preview$/,
  /\/population-member-proposals$/,
  /\/population-routes$/,
  /\/related-expand-choices$/,
  /\/related-expand-contributors$/,
  /\/related-field-choices$/,
  /\/row-definition-choices$/,
  /\/row-definition-proposals$/,
  /\/row-lineage$/,
  /\/semantic-inventory$/,
  /\/suggestions$/,
  /\/table-shape-capabilities$/,
  /\/table-shape-proposals$/,
  /\/table-shape-category-discoveries$/,
];

let operationCache = null;

function operationsByRoute() {
  if (operationCache) return operationCache;
  operationCache = new Map();
  const lines = readFileSync(resolve(ROOT, 'openapi/openapi.yaml'), 'utf8').split('\n');
  let currentPath = null;
  for (let index = 0; index < lines.length; index += 1) {
    const pathMatch = lines[index].match(/^  (\/[^:]+):\s*$/);
    if (pathMatch) {
      currentPath = pathMatch[1];
      continue;
    }
    const methodMatch = lines[index].match(/^    (get|post|put|delete|patch):\s*$/);
    if (!methodMatch || !currentPath) continue;
    const key = methodMatch[1].toUpperCase() + ' ' + currentPath;
    for (let inner = index + 1; inner < lines.length; inner += 1) {
      if (/^    (get|post|put|delete|patch):\s*$/.test(lines[inner]) || /^  \//.test(lines[inner])) break;
      const operation = lines[inner].match(/^      operationId:\s*(\S+)/);
      if (operation) {
        operationCache.set(key, operation[1]);
        break;
      }
    }
  }
  return operationCache;
}

export function operationIdForRoute(route) {
  return operationsByRoute().get(routeKey(route)) ?? null;
}

export function routeMode(route) {
  if (route.path.endsWith('/builder')) return 'stateful';
  const operation = operationIdForRoute(route);
  if (operation && /^(get|list|browse|search|discover|propose|trace|download)/i.test(operation)) return 'read';
  const method = String(route.method).toUpperCase();
  if (method === 'GET') return 'read';
  if (method !== 'POST') return 'stateful';
  return READ_LIKE_POSTS.some((pattern) => pattern.test(route.path)) ? 'read' : 'stateful';
}

export function walkFiles(root, predicate) {
  const absolute = resolve(ROOT, root);
  const files = [];
  const walk = (directory) => {
    for (const entry of readdirSync(directory)) {
      const path = resolve(directory, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (predicate(path)) files.push(path);
    }
  };
  walk(absolute);
  return files.sort();
}

function quotedPathMatches(text) {
  const matches = [];
  const pattern = /(['"`])(\/(?:[^'"`\\]|\\.)*?)\1/g;
  for (const match of text.matchAll(pattern)) {
    const value = match[2]
      .replace(/\\\//g, '/')
      .replace(/\?.*$/, '')
      .replace(/\[.*$/, '');
    if (value.includes('/')) matches.push({ value, index: match.index });
  }
  return matches;
}

function routeMatch(route, value) {
  if (new RegExp('^' + routePathPattern(route.path) + '$').test(value)) return 'route-pattern';
  const suffix = routeSuffix(route.path);
  if (!suffix) return null;
  const ambiguous = new Set(['builder', 'commands', 'discoveries', 'options', 'preview', 'proposals', 'publish', 'reconcile', 'routes', 'selections']);
  if (ambiguous.has(suffix)) return null;
  return value === '/' + suffix || value.endsWith('/' + suffix) ? 'route-suffix' : null;
}

function testEntriesFromFile(text) {
  const entries = [];
  const pattern = /\b(?:it|test)(?:\.each\s*\([^)]*\))?\s*\(\s*(['"`])([\s\S]*?)\1/g;
  for (const match of text.matchAll(pattern)) {
    const name = match[2].replace(/\s+/g, ' ').trim();
    if (name) entries.push({ name, index: match.index });
  }
  return entries;
}

export function testNameFromFile(text) {
  return testEntriesFromFile(text).map((entry) => entry.name);
}

function nearestBefore(entries, index) {
  const before = entries.filter((entry) => entry.index <= index);
  if (before.length > 0) return before[before.length - 1];
  return entries[0] ?? null;
}

function scannedEvidence(route, file, text) {
  const matches = quotedPathMatches(text)
    .map((entry) => ({ ...entry, mapping: routeMatch(route, entry.value) }))
    .filter((entry) => entry.mapping);
  if (matches.length === 0) return [];
  const relativeFile = relative(ROOT, file);
  if (file.endsWith('_test.go')) {
    const functions = [...text.matchAll(/^func (Test[A-Za-z0-9_]+)\(/gm)]
      .map((match) => ({ name: match[1], index: match.index }));
    return matches.map((match) => ({
      kind: 'go',
      name: nearestBefore(functions, match.index)?.name ?? null,
      file: relativeFile,
      mapping: match.mapping,
    }));
  }
  const tests = testEntriesFromFile(text);
  return matches.map((match) => ({
    kind: 'ui',
    name: nearestBefore(tests, match.index)?.name ?? null,
    file: relativeFile,
    mapping: match.mapping,
  }));
}

export function evidenceForRoute(route, tests, browserCases) {
  const key = routeKey(route);
  const browser = (browserCases ?? [])
    .filter((entry) => isCurrentBrowserEvidence(entry) && (entry.coveredRoutes ?? []).includes(key))
    .map((entry) => ({ kind: 'browser', id: entry.id, file: entry.sourceReport ?? null, mapping: 'browser-evidence' }));
  const strongMappings = new Set(['go-route', 'go-route-suffix', 'browser-evidence', 'route-proof']);
  const mappedTests = (tests ?? [])
    .filter((test) => (test.routes ?? []).includes(key) && strongMappings.has(test.mapping))
    .map((test) => ({ kind: test.kind, name: test.name, file: test.file, mapping: test.mapping }));
  const scanned = [];
  for (const file of walkFiles('internal', (path) => path.endsWith('_test.go'))) {
    scanned.push(...scannedEvidence(route, file, readFileSync(file, 'utf8')));
  }
  for (const file of walkFiles('ui/packages/loom-ui/src/features/ExplorerBuilder', (path) => /\.(test|spec)\.(ts|tsx)$/.test(path))) {
    scanned.push(...scannedEvidence(route, file, readFileSync(file, 'utf8')));
  }
  const rank = {
    'browser-evidence': 6,
    'route-proof': 5,
    'go-route': 4,
    'go-route-suffix': 3,
    'route-pattern': 2,
    'route-suffix': 1,
  };
  const byIdentity = new Map();
  for (const entry of [...browser, ...mappedTests, ...scanned]) {
    const id = [entry.kind, entry.id ?? entry.name ?? '', entry.file ?? ''].join('|');
    const current = byIdentity.get(id);
    if (!current || (rank[entry.mapping] ?? 0) > (rank[current.mapping] ?? 0)) byIdentity.set(id, entry);
  }
  const evidence = [...byIdentity.values()].sort((left, right) =>
    (rank[right.mapping] ?? 0) - (rank[left.mapping] ?? 0)
    || (left.name ?? left.id ?? '').localeCompare(right.name ?? right.id ?? '')
    || (left.file ?? '').localeCompare(right.file ?? ''));
  return { browser, tests: evidence.filter((entry) => entry.kind !== 'browser'), evidence };
}

export function displayPath(path) {
  return path ? relative(ROOT, path) : basename(path ?? '');
}
