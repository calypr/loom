import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export function timingTarget(report) {
  const target = report.target;
  const url = new URL(target.apiUrl);
  assert.equal(url.protocol, 'http:');
  assert.equal(url.hostname, '127.0.0.1');
  assert.ok(url.port && url.port !== '8080', 'never benchmark the canonical demo');
  assert.match(target.composeProject, /^loom-dev-/);
  assert.match(target.project, /^loom_dev_verify_[a-z0-9_-]+$/);
  assert.match(target.explorerId, /^loom-dev-verification-[a-z0-9-]+$/);
  return {
    ...target,
    base: `${url.origin}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2`,
  };
}

async function json(url, body) {
  const response = await fetch(url, {
    ...(body === undefined ? {} : {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert.ok(response.ok, `${url}: ${response.status} ${JSON.stringify(value.error ?? value)}`);
  return value;
}

export const median = (values) => {
  assert.ok(values.length > 0 && values.every(Number.isFinite));
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
};

async function sample(target, index) {
  const builder = await json(`${target.base}/builder`);
  const outputId = builder.workspace.documents[0].output.id;
  const start = performance.now();
  const command = await json(`${target.base}/commands`, {
    commandId: `timing-${randomUUID()}`,
    ...(builder.workspace.semanticsVersion >= 4 ? { semanticsVersion: 4 } : {}),
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands: [{ type: 'RENAME_TABLE', outputId, title: `Timing sample ${index}` }],
  });
  const saved = performance.now();
  const receipt = await json(`${target.base}/reconcile`, {
    snapshotToken: builder.catalog.snapshotToken,
    draftVersion: command.draftVersion,
    draftDigest: command.draftDigest,
  });
  const reconciled = performance.now();
  const preview = await json(`${target.base}/preview`, { receiptId: receipt.receiptId, outputId, limit: 25 });
  const finished = performance.now();
  const rows = preview.rows.map((row) => Object.fromEntries(
    preview.columns.map((column, i) => [column.label, Array.isArray(row) ? row[i] : row[column.column]]),
  )).sort((a, b) => String(a.id).localeCompare(String(b.id)));
  assert.deepEqual(rows.map((row) => [row.id, row['valueQuantity.value']]), [
    ['dev-patient-001', 180], ['dev-patient-002', 68],
  ]);
  return {
    save_ms: saved - start, reconcile_ms: reconciled - saved,
    preview_ms: finished - reconciled, total_ms: finished - start,
    rows,
    populations: builder.catalog.nodes.map((node) => [node.resourceType, node.documentCount]).sort(),
  };
}

function sourceIdentity(target) {
  const cwd = resolve(target.fixture, '../..');
  const head = execFileSync('git', ['rev-parse', 'HEAD'], { cwd, encoding: 'utf8' }).trim();
  const diff = execFileSync('git', ['diff', 'HEAD', '--', 'internal', 'generated', 'openapi', 'ui', 'testdata'], { cwd });
  const digest = createHash('sha256').update(diff);
  const untracked = execFileSync('git', ['ls-files', '--others', '--exclude-standard', '-z', '--', 'internal', 'generated', 'openapi', 'ui', 'testdata'], { cwd, encoding: 'utf8' });
  for (const path of untracked.split('\0').filter(Boolean).sort()) {
    digest.update(path).update('\0').update(readFileSync(join(cwd, path))).update('\0');
  }
  return { head, workingChangesSha256: digest.digest('hex') };
}

async function main() {
  assert.equal(process.argv.length, 4, 'usage: node scripts/measure-authoring-loop.mjs BASELINE_REPORT CANDIDATE_REPORT');
  const targets = process.argv.slice(2).map((path) => timingTarget(JSON.parse(readFileSync(path, 'utf8'))));
  assert.notEqual(targets[0].apiUrl, targets[1].apiUrl);
  const identities = targets.map(sourceIdentity);
  const results = [[], []];
  for (let i = -1; i < 5; i++) {
    const pair = [];
    for (const side of i % 2 === 0 ? [1, 0] : [0, 1]) {
      pair[side] = await sample(targets[side], `${randomUUID()}-${i}`);
    }
    assert.deepEqual(pair[0].populations, pair[1].populations, 'baseline and candidate fixtures differ');
    assert.deepEqual(pair[0].rows, pair[1].rows, 'baseline and candidate preview values differ');
    if (i >= 0) pair.forEach((value, side) => results[side].push(value));
  }
  assert.deepEqual(targets.map(sourceIdentity), identities, 'source changed during timing run');
  const metrics = Object.fromEntries(['save_ms', 'reconcile_ms', 'preview_ms', 'total_ms'].map((key) => {
    const [baseline, candidate] = results.map((samples) => median(samples.map((value) => value[key])));
    return [key, { baseline, candidate, ratio: candidate / baseline }];
  }));
  const output = join(process.cwd(), '.artifacts/loom-dev', `authoring-timing-${Date.now()}.json`);
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, JSON.stringify({ targets, identities, samples: results, metrics }, null, 2));
  console.log(JSON.stringify({ evidence: output, metrics }, null, 2));
  assert.ok(metrics.total_ms.candidate < 30000, 'warm authoring loop exceeded 30 seconds');
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
