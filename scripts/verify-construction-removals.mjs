import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseArgs } from 'node:util';

const { values } = parseArgs({ options: {
  project: { type: 'string', default: 'loom_dev_cda_fhir' },
  explorer: { type: 'string' }, output: { type: 'string' },
  origin: { type: 'string', default: 'http://127.0.0.1:8188' },
  evidence: { type: 'string', default: `/tmp/loom-removal-verification-${Date.now()}` },
  'expect-cascade-step': { type: 'string' },
  'api-container': { type: 'string', default: 'loom-dev-6d7df93d6a37-loom-api-1' },
} });
assert(values.explorer && values.output, 'Pass --explorer <id> --output <id>. This script proposes changes; it never applies them.');
const base = `${values.origin}/api/v1/projects/${encodeURIComponent(values.project)}/explorers/${encodeURIComponent(values.explorer)}/authoring/v2`;
const started = new Date().toISOString();
const report = { started, project: values.project, explorer: values.explorer, output: values.output, cases: [], failures: [] };
report.environment = {
  node: process.version, origin: values.origin,
  commit: spawnSync('rtk', ['proxy', 'git', 'rev-parse', 'HEAD'], { encoding: 'utf8' }).stdout?.trim(),
  workingTree: spawnSync('rtk', ['proxy', 'git', 'status', '--short'], { encoding: 'utf8' }).stdout?.trim(),
};
const readBuilder = async () => {
  const response = await fetch(base + '/builder', { signal: AbortSignal.timeout(30000) });
  assert(response.ok, `Builder read failed: ${response.status}`);
  return response.json();
};
await mkdir(values.evidence, { recursive: true });
try {
  const baseline = await readBuilder();
  await writeFile(join(values.evidence, 'baseline.json'), JSON.stringify(baseline, null, 2));
  const document = baseline.workspace.documents.find(item => item.output.id === values.output);
  assert(document?.construction?.steps.length, 'The selected table must have saved construction steps');
  const steps = document.construction.steps;
  if (values['expect-cascade-step']) assert(steps.some(step => step.id === values['expect-cascade-step']), 'Expected cascade step is absent from the saved construction');
  for (const [index, step] of steps.entries()) {
    if (step.ownerStepId) continue; // Owned prerequisites are edited through their visible owner.
    for (const mode of ['single', 'suffix']) {
      if (mode === 'suffix' && index === steps.length - 1) continue;
      const removed = mode === 'single' ? [step.id] : steps.slice(index).map(item => item.id);
      const requestId = `construction-removal-${randomUUID()}`;
      const request = {
        snapshotToken: baseline.catalog.snapshotToken,
        expectedDraftVersion: baseline.draftVersion,
        expectedDraftDigest: baseline.draftDigest,
        outputId: values.output, removeStepIds: removed, limit: 25,
        candidateConstruction: { ...document.construction, steps: steps.filter(item => !removed.includes(item.id)) },
      };
      const record = { stepId: step.id, kind: step.operation.kind, mode, requestId, request };
      report.cases.push(record);
      const actionStarted = Date.now();
      try {
        const response = await fetch(base + '/construction-proposals', {
          method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestId },
          body: JSON.stringify(request), signal: AbortSignal.timeout(30000),
        });
        record.status = response.status;
        record.response = await response.json();
        record.durationMs = Date.now() - actionStarted;
        assert.equal(response.status, 200, JSON.stringify(record.response));
        assert.equal(record.response.previewStatus, 'READY', 'A removal must include its dependent steps and produce a valid preview');
        assert.equal(record.response.dependencyImpact.missingInputs?.length ?? 0, 0);
        const removedIds = record.response.dependencyImpact.removedStepIds ?? [];
        assert(removed.every(id => removedIds.includes(id)), 'The proposal must remove every requested step');
        assert(record.response.candidateConstruction.steps.every(item => !removedIds.includes(item.id)), 'Removed steps must be absent from the valid candidate');
        if (mode === 'single' && step.id === values['expect-cascade-step']) {
          assert(removedIds.length > 1, 'The proposal must report its dependent removals');
        }
        assert(record.durationMs <= 5000, `Proposal took ${record.durationMs} ms`);
      } catch (error) {
        record.failure = String(error.stack ?? error);
        report.failures.push({ stepId: step.id, mode, error: record.failure });
      }
      await writeFile(join(values.evidence, `${index}-${mode}.json`), JSON.stringify(record, null, 2));
      const after = await readBuilder();
      assert.equal(after.draftDigest, baseline.draftDigest, 'A proposal must leave the saved draft unchanged');
      assert.equal(after.draftVersion, baseline.draftVersion);
    }
  }
} catch (error) {
  report.failures.push({ error: String(error.stack ?? error) });
} finally {
  try {
    const result = spawnSync('rtk', ['proxy', 'docker', 'logs', '--since', started, values['api-container']], { encoding: 'utf8', maxBuffer: 10000000, timeout: 30000 });
    if (result.error) throw result.error;
    const logs = String(result.stdout ?? '') + String(result.stderr ?? '');
    const ids = report.cases.map(record => record.requestId);
    report.serverLogs = logs.split('\n').filter(line => ids.some(id => line.includes(id)));
    if (result.status !== 0) report.logCaptureError = `Docker logs exited ${result.status}`;
  } catch (error) {
    const logs = String(error.stderr ?? '');
    const ids = report.cases.map(record => record.requestId);
    report.serverLogs = logs.split('\n').filter(line => ids.some(id => line.includes(id)));
    report.logCaptureError = String(error.message);
  }
  await writeFile(join(values.evidence, 'report.json'), JSON.stringify(report, null, 2));
}
console.log(JSON.stringify({ evidence: values.evidence, cases: report.cases.map(({ stepId, mode, status, durationMs, response }) => ({ stepId, mode, status, durationMs, previewStatus: response?.previewStatus })), failures: report.failures }, null, 2));
if (report.failures.length) process.exitCode = 1;
