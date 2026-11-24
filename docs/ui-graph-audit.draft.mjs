// Usage: node deadscan.mjs <loom-ui package dir>
import { createRequire } from 'node:module';
import { readdirSync, readFileSync, existsSync, statSync } from 'node:fs';
import { join, dirname, resolve, relative } from 'node:path';
const pkg = resolve(process.argv[2]);
const require = createRequire(join(pkg, 'package.json'));
const ts = require('typescript');
const src = join(pkg, 'src');
const files = [];
const walk = (d) => { for (const e of readdirSync(d, { withFileTypes: true })) { const p = join(d, e.name); if (e.isDirectory()) walk(p); else if (/\.(ts|tsx)$/.test(e.name) && !e.name.endsWith('.d.ts')) files.push(p); } };
walk(src);
const isTest = (f) => /\.test\.tsx?$/.test(f);
const resolveMod = (from, spec) => {
  if (!spec.startsWith('.')) return null;
  const base = resolve(dirname(from), spec);
  for (const c of [base, base + '.ts', base + '.tsx', join(base, 'index.ts'), join(base, 'index.tsx')]) if (existsSync(c) && statSync(c).isFile()) return c;
  return null;
};
const info = new Map();
for (const f of files) {
  const sf = ts.createSourceFile(f, readFileSync(f, 'utf8'), ts.ScriptTarget.Latest, true);
  const exports = new Set(); const imports = []; // {target, names:Set|'*'}
  const reexports = []; // {target, names: Map(exported->local) | '*'}
  for (const st of sf.statements) {
    const mods = ts.canHaveModifiers(st) ? ts.getModifiers(st) ?? [] : [];
    const exported = mods.some((m) => m.kind === ts.SyntaxKind.ExportKeyword);
    const isDefault = mods.some((m) => m.kind === ts.SyntaxKind.DefaultKeyword);
    if (ts.isImportDeclaration(st)) {
      const t = resolveMod(f, st.moduleSpecifier.text); if (!t) continue;
      const names = new Set(); const c = st.importClause;
      if (c?.name) names.add('default');
      if (c?.namedBindings) { if (ts.isNamespaceImport(c.namedBindings)) names.add('*'); else for (const el of c.namedBindings.elements) names.add((el.propertyName ?? el.name).text); }
      imports.push({ target: t, names });
    } else if (ts.isExportDeclaration(st)) {
      if (st.moduleSpecifier) {
        const t = resolveMod(f, st.moduleSpecifier.text); if (!t) continue;
        if (!st.exportClause) reexports.push({ target: t, names: '*' });
        else if (ts.isNamedExports(st.exportClause)) { const m = new Map(); for (const el of st.exportClause.elements) { m.set(el.name.text, (el.propertyName ?? el.name).text); exports.add(el.name.text); } reexports.push({ target: t, names: m }); }
      } else if (st.exportClause && ts.isNamedExports(st.exportClause)) for (const el of st.exportClause.elements) exports.add(el.name.text);
    } else if (ts.isExportAssignment(st)) exports.add('default');
    else if (exported) {
      if (isDefault) exports.add('default');
      else if (ts.isVariableStatement(st)) for (const d of st.declarationList.declarations) { if (ts.isIdentifier(d.name)) exports.add(d.name.text); }
      else if (st.name) exports.add(st.name.text);
    }
  }
  // dynamic imports / vi.mock
  const dyn = [];
  const visit = (n) => { if (ts.isCallExpression(n) && n.arguments.length && ts.isStringLiteral(n.arguments[0]) && (n.expression.kind === ts.SyntaxKind.ImportKeyword || /vi\.(mock|importActual)/.test(n.expression.getText(sf)))) { const t = resolveMod(f, n.arguments[0].text); if (t) dyn.push(t); } ts.forEachChild(n, visit); };
  visit(sf);
  info.set(f, { exports, imports, reexports, dyn });
}
const entries = ['index.ts', 'viewer-entry.ts', 'builder-entry.ts', 'saved-views-entry.ts'].map((e) => join(src, e));
// reachability
const reach = (roots, includeTests) => { const seen = new Set(); const st = [...roots]; while (st.length) { const f = st.pop(); if (seen.has(f) || !info.has(f)) continue; seen.add(f); const i = info.get(f); for (const x of [...i.imports.map((m) => m.target), ...i.reexports.map((r) => r.target), ...i.dyn]) st.push(x); } return seen; };
const prodReach = reach(entries);
const allReach = reach([...entries, ...files.filter(isTest)]);
const rel = (f) => relative(pkg, f);
console.log('## Non-test files unreachable from package entries');
for (const f of files) if (!isTest(f) && !prodReach.has(f)) console.log((allReach.has(f) ? '  [test-only] ' : '  [orphan]    ') + rel(f));
// export usage: mark used(file,name) with propagation through reexports
const used = new Map(); const usedBy = new Map(); // key -> set of 'prod'|'test'|'public'
const key = (f, n) => f + '#' + n;
const mark = (f, n, kind, depth = 0) => {
  if (depth > 30 || !info.has(f)) return; const k = key(f, n);
  const s = usedBy.get(k) ?? new Set(); if (s.has(kind)) return; s.add(kind); usedBy.set(k, s);
  const i = info.get(f);
  for (const r of i.reexports) {
    if (r.names === '*') { if (n === '*') mark(r.target, '*', kind, depth + 1); else mark(r.target, n, kind, depth + 1); }
    else if (n === '*') { for (const [, local] of r.names) mark(r.target, local, kind, depth + 1); }
    else if (r.names.has(n)) mark(r.target, r.names.get(n), kind, depth + 1);
  }
  if (n === '*') for (const e of i.exports) mark(f, e, kind, depth + 1);
};
for (const e of entries) mark(e, '*', 'public');
for (const f of files) { const i = info.get(f); const kind = isTest(f) ? 'test' : 'prod'; for (const im of i.imports) for (const n of im.names) mark(im.target, n, kind); for (const d of i.dyn) mark(d, '*', kind); }
console.log('\n## Exports with no non-test importer (not public)');
let testOnly = 0, unused = 0; const byFile = new Map();
for (const f of files) {
  if (isTest(f) || entries.includes(f)) continue;
  for (const n of info.get(f).exports) {
    const s = usedBy.get(key(f, n)) ?? new Set();
    if (s.has('prod') || s.has('public')) continue;
    // used internally in same file?
    const body = readFileSync(f, 'utf8'); const internal = (body.match(new RegExp('\\b' + n + '\\b', 'g')) ?? []).length > 1;
    const tag = s.has('test') ? 'test-only' : 'unused';
    if (tag === 'test-only') testOnly++; else unused++;
    const arr = byFile.get(f) ?? []; arr.push(`${n}${internal ? '' : ' (no local use)'}[${tag}]`); byFile.set(f, arr);
  }
}
for (const [f, arr] of byFile) console.log('  ' + rel(f) + ': ' + arr.join(', '));
console.log(`\nTotals: ${unused} unused exports, ${testOnly} test-only exports`);
// public surface size
let pub = 0; for (const [k, s] of usedBy) if (s.has('public') && !k.endsWith('#*')) pub++;
console.log(`Public-reachable export bindings: ${pub}`);
// fan-in per file
console.log('\n## Production fan-in (importers) / fan-out by file, top 40 by lines');
const fanIn = new Map(); for (const f of files) if (!isTest(f)) for (const im of [...info.get(f).imports, ...info.get(f).reexports]) fanIn.set(im.target, (fanIn.get(im.target) ?? 0) + 1);
const rows = files.filter((f) => !isTest(f)).map((f) => [readFileSync(f, 'utf8').split('\n').length, fanIn.get(f) ?? 0, info.get(f).imports.length, rel(f)]).sort((a, b) => b[0] - a[0]);
for (const r of rows.slice(0, 40)) console.log(`  ${String(r[0]).padStart(5)} in=${String(r[1]).padStart(3)} out=${String(r[2]).padStart(3)} ${r[3]}`);
// cycles (prod only) via Tarjan
const idx = new Map(), low = new Map(), on = new Set(), stack = []; let i0 = 0; const sccs = [];
const prodFiles = files.filter((f) => !isTest(f));
const edges = (f) => [...info.get(f).imports, ...info.get(f).reexports].map((x) => x.target).filter((t) => info.has(t) && !isTest(t));
const strong = (v) => { idx.set(v, i0); low.set(v, i0); i0++; stack.push(v); on.add(v); for (const w of edges(v)) { if (!idx.has(w)) { strong(w); low.set(v, Math.min(low.get(v), low.get(w))); } else if (on.has(w)) low.set(v, Math.min(low.get(v), idx.get(w))); } if (low.get(v) === idx.get(v)) { const c = []; let w; do { w = stack.pop(); on.delete(w); c.push(w); } while (w !== v); if (c.length > 1) sccs.push(c); } };
for (const f of prodFiles) if (!idx.has(f)) strong(f);
console.log('\n## Import cycles (production)');
for (const c of sccs) console.log('  [' + c.length + '] ' + c.map(rel).join(' , '));
console.log('\n## Directory edges (production imports, cross-dir)');
const dcount = new Map();
for (const f of prodFiles) for (const t of edges(f)) { const a = relative(src, dirname(f)) || '.', b = relative(src, dirname(t)) || '.'; if (a === b) continue; const k = a + ' -> ' + b; dcount.set(k, (dcount.get(k) ?? 0) + 1); }
for (const [k, v] of [...dcount].sort((x, y) => y[1] - x[1])) console.log(`  ${String(v).padStart(3)} ${k.replaceAll('features/ExplorerBuilder', 'EB').replaceAll('features/', 'F/')}`);
