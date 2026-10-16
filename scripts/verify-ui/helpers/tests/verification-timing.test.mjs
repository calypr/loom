import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildReport, finishSpan, startSpan } from '../../../verification-timing.mjs';

const at = (minute) => new Date(Date.UTC(2026, 0, 1, 0, minute)).toISOString();
function temporary(t) {
  const root = mkdtempSync(join(tmpdir(), 'verification-timing-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return root;
}
function recordSpan(dir, owner, caseId, phase, from, to) {
  const span = startSpan({ spansDir: dir, owner, caseId, phase, now: () => new Date(at(from)) });
  if (to !== undefined) finishSpan({ spansDir: dir, spanId: span.spanId, now: () => new Date(at(to)) });
  return span;
}

test('span files stay minimal and immutable while report math separates brackets, phase overlap, and gaps', (t) => {
  const root = temporary(t); const spansDir = join(root, 'spans');
  process.env.VERIFICATION_TIMING_TEST_SECRET = 'environment-secret-must-not-be-recorded';
  t.after(() => { delete process.env.VERIFICATION_TIMING_TEST_SECRET; });
  const first = recordSpan(spansDir, 'owner-a', 'ISSUE-1', 'diagnosis', 10, 25);
  recordSpan(spansDir, 'owner-a', 'ISSUE-1', 'diagnosis', 20, 25);
  recordSpan(spansDir, 'owner-b', 'ISSUE-2', 'review', 40, 50);
  const open = recordSpan(spansDir, 'owner-c', 'CASE-3', 'integration', 55);

  const startFile = JSON.parse(readFileSync(first.path, 'utf8'));
  assert.deepEqual(Object.keys(startFile).sort(), ['caseId', 'owner', 'phase', 'startedAt']);
  const endPath = join(spansDir, `${first.spanId}.end.json`);
  const endBytes = readFileSync(endPath, 'utf8');
  assert.deepEqual(Object.keys(JSON.parse(endBytes)), ['finishedAt']);
  assert.doesNotMatch(readFileSync(first.path, 'utf8') + endBytes, /environment-secret-must-not-be-recorded/);
  assert.throws(() => finishSpan({ spansDir, spanId: first.spanId, now: () => new Date(at(26)) }), { code: 'EEXIST' });
  assert.equal(readFileSync(endPath, 'utf8'), endBytes, 'a duplicate finish leaves the original end file intact');

  const summaryOne = join(root, 'summary-one.json');
  const summaryTwo = join(root, 'summary-two.json');
  const missingSummary = join(root, 'summary-missing.json');
  writeFileSync(summaryOne, JSON.stringify({ startedAt: at(0), finishedAt: at(20), status: 'failed', secret: 'must not be reported' }));
  writeFileSync(summaryTwo, JSON.stringify({ startedAt: at(30), finishedAt: at(40), commands: { playwright: { durationMs: 8000 } } }));
  const report = buildReport({ summaryPaths: [summaryOne, summaryTwo, missingSummary], spansDir, generatedAt: at(60) });

  assert.deepEqual(report.summaryInputs.map(({ status }) => status), ['measured', 'measured', 'unknown']);
  assert.equal(report.bracket.measuredCount, 2);
  assert.equal(report.bracket.wallUnionMs, 30 * 60_000);
  assert.deepEqual(report.browserCommands, { measuredCount: 1, summedDurationMs: 8000 });
  assert.deepEqual(report.phaseDurations.diagnosis, { wallUnionMs: 15 * 60_000, summedDurationMs: 20 * 60_000, overlapMs: 5 * 60_000 });
  assert.deepEqual(report.phaseDurations.review, { wallUnionMs: 10 * 60_000, summedDurationMs: 10 * 60_000, overlapMs: 0 });
  assert.equal(report.observationWindow.elapsedMs, 50 * 60_000);
  assert.equal(report.observationWindow.unattributedGapMs, 5 * 60_000);
  assert.equal(report.openSpans[0].spanId, open.spanId);
  assert.match(report.interpretation, /do not estimate any phase or reasoning time/);
  assert.doesNotMatch(JSON.stringify(report), /must not be reported|environment-secret-must-not-be-recorded/);
});

test('CLI start and finish create separate files in the explicit temporary spans directory', (t) => {
  const root = temporary(t); const spansDir = join(root, 'cli-spans');
  const cli = fileURLToPath(new URL('../../../verification-timing.mjs', import.meta.url));
  const repoRoot = fileURLToPath(new URL('../../../../', import.meta.url));
  const rejectedDir = join(repoRoot, `.verification-timing-reject-${randomUUID()}`);
  assert.equal(existsSync(rejectedDir), false);
  assert.throws(() => execFileSync(process.execPath, [cli, 'start', '--spans', rejectedDir, '--owner', 'x', '--case-id', 'y', '--phase', 'review']));
  assert.equal(existsSync(rejectedDir), false, 'rejected paths are checked before directory creation');
  assert.throws(() => execFileSync(process.execPath, [cli, 'start', '--spans', spansDir, '--owner', 'x', '--case-id', 'y', '--phase', 'review', '--summary', 'ignored.json']));
  const started = JSON.parse(execFileSync(process.execPath, [cli, 'start', '--spans', spansDir, '--owner', 'cli-owner', '--case-id', 'CASE-CLI', '--phase', 'implementation'], { encoding: 'utf8' }));
  assert.equal(readFileSync(started.path, 'utf8').includes('CASE-CLI'), true);
  const finished = JSON.parse(execFileSync(process.execPath, [cli, 'finish', '--spans', spansDir, '--span-id', started.spanId], { encoding: 'utf8' }));
  assert.equal(finished.spanId, started.spanId);
  assert.equal(JSON.parse(readFileSync(finished.path, 'utf8')).finishedAt !== undefined, true);
  const summaryPath = join(root, 'summary.json');
  writeFileSync(summaryPath, JSON.stringify({ startedAt: new Date(Date.now() - 1000).toISOString(), finishedAt: new Date().toISOString() }));
  const report = JSON.parse(execFileSync(process.execPath, [cli, 'report', '--summary', summaryPath, '--spans', spansDir], { encoding: 'utf8' }));
  assert.equal(report.summaryInputs[0].status, 'measured');
  assert.equal(report.phaseSpans[0].caseId, 'CASE-CLI');
});

test('invalid and nonpositive timestamps stay unknown, and missing span history is explicit', (t) => {
  const root = temporary(t); const spansDir = join(root, 'spans');
  const invalidSummary = join(root, 'invalid-summary.json');
  writeFileSync(invalidSummary, JSON.stringify({ startedAt: at(3), finishedAt: at(3) }));
  const invalidId = '00000000-0000-4000-8000-000000000001';
  const reversedId = '00000000-0000-4000-8000-000000000002';
  mkdirSync(spansDir, { recursive: true });
  writeFileSync(join(spansDir, `${invalidId}.start.json`), JSON.stringify({ owner: 'a', caseId: 'X-1', phase: 'review', startedAt: 'not-a-date' }));
  writeFileSync(join(spansDir, `${invalidId}.end.json`), JSON.stringify({ finishedAt: at(5) }));
  writeFileSync(join(spansDir, `${reversedId}.start.json`), JSON.stringify({ owner: 'a', caseId: 'X-2', phase: 'review', startedAt: at(8) }));
  writeFileSync(join(spansDir, `${reversedId}.end.json`), JSON.stringify({ finishedAt: at(7) }));
  const report = buildReport({ summaryPaths: [invalidSummary], spansDir });
  assert.equal(report.summaryInputs[0].status, 'unknown');
  assert.equal(report.observationWindow, null);
  assert.equal(report.unknownSpans.length, 2);
  const noHistory = buildReport({ summaryPaths: [], spansDir: join(root, 'absent') });
  assert.equal(noHistory.observationWindow, null);
  assert.equal(noHistory.unknownSpans[0].reason, 'spans directory missing or unreadable');
});
