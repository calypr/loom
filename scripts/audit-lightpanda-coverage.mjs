#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { readFileSync, existsSync } from 'node:fs';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const recovery = '5646633e0e5c7ff8de568a4f111478d121e7f75f';
const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
const paths = git('ls-tree', '-r', '--name-only', recovery, 'scripts').trim().split('\n').filter(path => path.endsWith('.spec.mjs'));

const families = [
  { id: 'small-preview', names: ['builder-preview-read-only', 'load-preview', 'builder-load'],
    decision: 'Consolidate into one exact-row load/reload candidate; Lightpanda timing pilot only.',
    evidence: ['scripts/browser/lightpanda-cases.mjs'] },
  { id: 'saved-route', names: ['builder-saved-route-edit', 'cda-saved-related-route-edit-epoch126'],
    decision: 'Keep one small-fixture lifecycle candidate. Retain CDA coverage as an external gap. Lightpanda must preserve native disclosure, Cancel, Apply and reload.',
    evidence: ['scripts/browser/run.mjs', 'scripts/browser/lightpanda-cases.mjs', 'docs/verification/RETAINED_COVERAGE_DECISIONS.md'] },
  { id: 'native-input', names: ['viewer-row-file-download'],
    decision: 'Keep existing Chromium attachment check with exact filename/bytes/hash. Do not replace browser downloads with fetch.',
    evidence: ['scripts/browser/run.mjs'] },
  { id: 'legacy-harness', names: ['legacy-v9-workspace', 'native-action-readiness', 'native-authoring-oracles', 'native-failure-evidence', 'native-raw-fields-locator', 'construction-preview-bench'],
    decision: 'Do not restore retired driver/report machinery. Preserve actionable engine semantics in the unchanged disclosure check and fail-closed comparison tests. Legacy product compatibility is not newly promised.',
    evidence: ['scripts/verify-ui/helpers/verify-browser-disclosure.mjs', 'scripts/browser/engine-verdict.test.mjs'] },
  { id: 'viewer', match: /^(viewer-|cda-viewer-)/,
    decision: 'Preserve Viewer query/filter/export/identity promises. Prefer mounted reducer/client/modal checks; pagination, cross-output and published rendering still need one joined browser case when the engine qualifies. No equivalence claim.',
    evidence: ['ui/packages/loom-ui/src/features/ExplorerViewer/OutputTable.unit.test.tsx', 'ui/packages/loom-ui/src/features/ExplorerViewer/exportPopulation.unit.test.tsx', 'docs/verification/RETAINED_COVERAGE_DECISIONS.md'] },
  { id: 'publication', match: /publish|publication/,
    decision: 'Keep actual Arango/ClickHouse publication rows in integration. A small publication-to-Viewer browser journey remains valuable; reject large CDA replay as the default gate. Browser boundary remains open.',
    evidence: ['scripts/lib/formal-append-rows.mjs', 'docs/verification/RETAINED_COVERAGE_DECISIONS.md'] },
  { id: 'external-cda', match: /^(cda-|standalone-cda-|scope-001-)/,
    decision: 'Keep named external dataset/auth/oracle requirements as gaps, not default browser jobs. Consolidate operation semantics into bounded actual-engine and mounted checks. These files are not proved duplicates.',
    evidence: ['docs/verification/DELETED_GUARANTEES.md', '.codex/skills/builder-interaction-formal-verification/features/README.md'] },
  { id: 'workspace', names: ['builder-controls', 'builder-explorer-deletion', 'builder-explorer-deletion-unavailable', 'builder-authoring', 'standalone-builder', 'dev-journeys', 'builder-journey', 'read-only-capability-conflict', 'authoring-summary'],
    decision: 'Select by missing boundary: hydration, selection, lifecycle and reload. Retain mounted Workspace/reconciliation tests. Do not revive a multipurpose megasuite; native reorder remains Chromium-owned. Browser gaps are not declared closed.',
    evidence: ['ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/ConstructionWorkspace.unit.test.tsx', 'docs/verification/DELETED_GUARANTEES.md'] },
  { id: 'operation-lifecycle', match: /./,
    decision: 'Keep operation/type/row-oracle permutations in existing fast tests and actual service integration. Recover a distinct small UI lifecycle only when a missing browser guarantee is demonstrated and the engine meets the speed gate. No filename-based redundancy claim.',
    evidence: ['docs/verification/RETAINED_COVERAGE_DECISIONS.md', 'docs/verification/DELETED_GUARANTEES.md', 'scripts/verify-builder-formal-core.mjs'] },
];
for (const family of families) for (const path of family.evidence)
  if (!existsSync(resolve(root, path))) throw new Error(`Missing coverage evidence: ${path}`);

const specs = paths.map(path => {
  const source = git('show', `${recovery}:${path}`);
  const name = basename(path, '.spec.mjs');
  const family = families.find(family => family.names?.includes(name) || family.match?.test(name));
  const imports = [...source.matchAll(/^import\s+.*?from\s+['"]([^'"]+)['"]/gm)].map(match => match[1]);
  const literalTitles = [...source.matchAll(/\btest\s*\(\s*['"]([^'"\n]+)['"]/g)].map(match => match[1]);
  return { path, sha256: createHash('sha256').update(source).digest('hex'), family: family.id,
    decision: family.decision, evidence: family.evidence, literalTitles, imports,
    status: 'not-adopted-pending-engine-qualification',
    recovery: `https://github.com/calypr/loom/blob/${recovery}/${path}` };
});
const currentSpecs = git('ls-files', 'scripts', 'ui').trim().split('\n').filter(path => /\.spec\.mjs$/.test(path));
const counts = Object.fromEntries(families.map(family => [family.id, specs.filter(spec => spec.family === family.id).length]));
const retainedDisclosure = 'scripts/verify-ui/helpers/verify-browser-disclosure.mjs';
console.log(JSON.stringify({ recovery, retiredSpecs: specs.length, counts, currentSpecs,
  currentRetainedPlaywrightHelper: { path: retainedDisclosure,
    sha256: createHash('sha256').update(readFileSync(resolve(root, retainedDisclosure))).digest('hex'),
    execution: 'Unchanged helper now runs on either engine through lightpanda-cases.mjs.' },
  boundary: 'Every recovered spec receives a file-level disposition. Literal titles omit generated cases. Families guide consolidation; they do not prove duplicate assertions or close deleted guarantees.', specs }, null, 2));
