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

export function routeSuffix(path) {
  const segments = String(path).split('/').filter(Boolean);
  const suffix = segments.at(-1) ?? '';
  return /^\{[^}]+\}$/.test(suffix) ? null : suffix;
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

export function testNameFromFile(text) {
  const names = [];
  const pattern = /\b(?:it|test)(?:\.each\s*\([^)]*\))?\s*\(\s*(['"`])([\s\S]*?)\1/g;
  for (const match of text.matchAll(pattern)) {
    const name = match[2].replace(/\s+/g, ' ').trim();
    if (name) names.push(name);
  }
  return names;
}

const routePattern = (route) => {
  const suffix = routeSuffix(route.path);
  if (!suffix) return null;
  const escaped = suffix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp('["\'`]\\/' + escaped + '["\'`]');
};

export function evidenceForRoute(route, tests, browserCases) {
  const key = routeKey(route);
  const browser = (browserCases ?? [])
    .filter((entry) => (entry.coveredRoutes ?? []).includes(key))
    .map((entry) => ({ kind: 'browser', id: entry.id, file: entry.sourceReport ?? null }));
  const strongMappings = new Set(['go-route', 'go-route-suffix', 'browser-evidence', 'route-proof']);
  const mappedTests = (tests ?? [])
    .filter((test) => (test.routes ?? []).includes(key) && strongMappings.has(test.mapping))
    .map((test) => ({ kind: test.kind, name: test.name, file: test.file, mapping: test.mapping }));
  const suffix = routeSuffix(route.path);
  const scanned = [];
  if (suffix) {
    const pattern = routePattern(route);
    for (const file of walkFiles('internal', (path) => path.endsWith('_test.go'))) {
      const text = readFileSync(file, 'utf8');
      if (!pattern.test(text)) continue;
      const names = [...text.matchAll(/^func (Test[A-Za-z0-9_]+)\(/gm)].map((match) => match[1]);
      scanned.push({ kind: 'go', name: names[0] ?? null, file: relative(ROOT, file), mapping: 'route-suffix' });
    }
    for (const file of walkFiles('ui/packages/loom-ui/src/features/ExplorerBuilder', (path) => /\.(test|spec)\.(ts|tsx)$/.test(path))) {
      const text = readFileSync(file, 'utf8');
      if (!pattern.test(text)) continue;
      scanned.push({ kind: 'ui', name: testNameFromFile(text)[0] ?? null, file: relative(ROOT, file), mapping: 'route-suffix' });
    }
  }
  const seen = new Set();
  const evidence = [...browser, ...mappedTests, ...scanned].filter((entry) => {
    const id = [entry.kind, entry.id ?? entry.name ?? '', entry.file ?? ''].join('|');
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
  return { browser, tests: evidence.filter((entry) => entry.kind !== 'browser'), evidence };
}

export function displayPath(path) {
  return path ? relative(ROOT, path) : basename(path ?? '');
}
