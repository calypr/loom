#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createDevSession, commandEnvironment } from './loom-dev.mjs';
import { parseVerifyFastReport } from './measure-b07-evidence.mjs';

const QUALITY_LIMIT_ENV = 'LOOM_DEV_RECIPE_QUALITY_MAX_ROWS';
const QUALITY_KEYS_LIMIT_ENV = 'LOOM_DEV_RECIPE_QUALITY_MAX_DISTINCT_KEYS';
const CONFLICT_ENV = 'LOOM_DEV_ACTIVATION_CONFLICT_ONCE';
const FAULT_ENVIRONMENT_KEYS = [QUALITY_LIMIT_ENV, QUALITY_KEYS_LIMIT_ENV, CONFLICT_ENV];

const requestJSON = async (url, options = {}) => {
  const response = await fetch(url, {
    ...options,
    signal: options.signal ?? AbortSignal.timeout(options.timeoutMs ?? 120_000),
  });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : {}; } catch { value = { raw: text }; }
  return { response, value, text };
};

const postJSON = (url, body, timeoutMs = 120_000) => requestJSON(url, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
  timeoutMs,
});

export const authoringURL = (target, explorerId, suffix) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2${suffix}`;

export const explorerURL = (target, explorerId) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}`;

const readinessURL = (target) => `${target.apiUrl}/readyz`;

const errorCode = (value) => String(value?.error?.code ?? value?.code ?? '').trim();

export const publicationStateSignature = (state) => {
  const runtime = state?.runtime ?? {};
  const publication = runtime.publication ?? state?.publication ?? {};
  const reports = runtime.qualityReports ?? state?.generated?.qualityReports ?? [];
  const outputs = runtime.outputs ?? [];
  const rowCounts = outputs.map((output) => Number(output?.rowCount)).filter((value) => Number.isFinite(value));
  return {
    revisionId: String(state?.active?.revisionId ?? state?.activeRevisionId ?? publication.revisionId ?? ''),
    executionId: String(publication.executionId ?? ''),
    receiptIds: Array.isArray(reports) ? reports.map((report) => String(report?.receiptId ?? '')).filter(Boolean).sort() : [],
    rowCount: rowCounts.length > 0 ? rowCounts.reduce((sum, value) => sum + value, 0) : null,
    outputCount: outputs.length,
  };
};

export const assertNoPublicationLeak = (failure, candidateReceiptId, restrictedMarkers = []) => {
  const encoded = JSON.stringify(failure?.value ?? failure ?? {});
  assert.equal(encoded.includes(candidateReceiptId), false, 'fault response leaked the candidate receipt identity');
  for (const marker of restrictedMarkers) {
    assert.equal(encoded.includes(String(marker)), false, `fault response leaked restricted marker ${marker}`);
  }
  const lower = encoded.toLowerCase();
  for (const field of ['qualityreports', 'rowcount', 'totalcount', 'rows', 'contributions']) {
    assert.equal(lower.includes(`"${field}"`), false, `fault response leaked publication field ${field}`);
  }
};

const runCompose = (target, args, overrides = {}) => {
  const env = commandEnvironment(target);
  for (const key of FAULT_ENVIRONMENT_KEYS) env[key] = String(overrides[key] ?? '');
  try {
    return execFileSync('docker', [
      'compose', '--project-name', target.composeProject, '--file', target.composeFile,
      ...args,
    ], { cwd: target.sourceRoot, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  } catch (error) {
    const stdout = String(error.stdout ?? '');
    const stderr = String(error.stderr ?? '');
    throw new Error(`docker compose ${args.join(' ')} failed: ${stderr || stdout || error.message}`);
  }
};

const waitForHTTP = async (url, timeoutMs = 120_000) => {
  const started = Date.now();
  let last = 'no response';
  while (Date.now() - started < timeoutMs) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(5_000) });
      if (response.ok) return;
      last = `HTTP ${response.status}`;
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 500));
  }
  throw new Error(`timed out waiting for ${url}: ${last}`);
};

const restartAPI = async (target, overrides) => {
  runCompose(target, ['up', '-d', '--no-deps', '--force-recreate', 'loom-api'], overrides);
  await waitForHTTP(readinessURL(target));
};

const readState = async (target, explorerId) => {
  const result = await requestJSON(explorerURL(target, explorerId));
  if (!result.response.ok) throw new Error(`Explorer read failed: HTTP ${result.response.status} ${result.text.slice(0, 500)}`);
  return result.value;
};

const qualityReports = (state) => {
  const reports = state?.runtime?.qualityReports ?? state?.generated?.qualityReports;
  assert.ok(Array.isArray(reports) && reports.length > 0, 'published state has no quality reports');
  return reports;
};

const outputID = (report, state) => String(
  report.target.materialization?.selector?.output
    ?? state?.runtime?.outputs?.[0]?.outputId
    ?? state?.runtime?.outputs?.[0]?.name
    ?? '',
).trim();

const readPublishedPreview = async (target, explorerId, receiptId, outputId) => {
  const result = await postJSON(authoringURL(target, explorerId, '/preview'), { receiptId, outputId, limit: 25 });
  if (!result.response.ok) throw new Error(`published preview failed: HTTP ${result.response.status} ${result.text.slice(0, 500)}`);
  return {
    rows: Array.isArray(result.value?.rows) ? result.value.rows : [],
    totalCount: Number(result.value?.totalCount ?? result.value?.count ?? result.value?.rows?.length ?? 0),
    columns: Array.isArray(result.value?.columns) ? result.value.columns : [],
  };
};

const freshReceipt = async (target, explorerId, label) => {
  const builderResult = await requestJSON(authoringURL(target, explorerId, '/builder'));
  if (!builderResult.response.ok) throw new Error(`Builder read failed: HTTP ${builderResult.response.status}`);
  const builder = builderResult.value;
  const output = builder?.workspace?.documents?.[0]?.output;
  assert.ok(output?.id, 'Builder has no output to publish');
  const commandResult = await postJSON(authoringURL(target, explorerId, '/commands'), {
    commandId: `b07-fault-${label}-${Date.now().toString(36)}`,
    semanticsVersion: builder.workspace?.semanticsVersion ?? 4,
    snapshotToken: builder.catalog?.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands: [{ type: 'RENAME_TABLE', outputId: output.id, title: `${output.title ?? 'B07 fault'} · ${label}` }],
  });
  if (!commandResult.response.ok) throw new Error(`fault authoring command failed: HTTP ${commandResult.response.status} ${commandResult.text.slice(0, 500)}`);
  const reconcileResult = await postJSON(authoringURL(target, explorerId, '/reconcile'), {
    snapshotToken: builder.catalog?.snapshotToken,
    draftVersion: commandResult.value?.draftVersion,
    draftDigest: commandResult.value?.draftDigest,
  });
  if (!reconcileResult.response.ok || !reconcileResult.value?.receiptId) {
    throw new Error(`fault reconcile failed: HTTP ${reconcileResult.response.status} ${reconcileResult.text.slice(0, 500)}`);
  }
  return String(reconcileResult.value.receiptId);
};

const publishReceipt = (target, explorerId, receiptId) =>
  postJSON(authoringURL(target, explorerId, '/publish'), { receiptId });

const logsFor = (target) => {
  try {
    return runCompose(target, ['logs', '--no-color', '--since', '2m', 'loom-api']);
  } catch (error) {
    return String(error?.message ?? error);
  }
};

const targetFromReport = (reportPath) => {
  const report = JSON.parse(readFileSync(reportPath, 'utf8'));
  const parsed = parseVerifyFastReport(report, reportPath);
  const api = new URL(parsed.apiUrl);
  const env = {
    ...process.env,
    LOOM_DEV_COMPOSE_PROJECT: parsed.composeProject,
    LOOM_DEV_PROJECT: parsed.project,
    LOOM_DEV_GENERATION: parsed.generation,
    LOOM_DEV_API_PORT: api.port,
    LOOM_DEV_API_URL: parsed.apiUrl,
  };
  const scriptDirectory = dirname(fileURLToPath(import.meta.url));
  return { report, parsed, target: createDevSession(env, resolve(scriptDirectory, '..')) };
};

const run = async (reportPath, outputPath) => {
  const absoluteReportPath = resolve(reportPath);
  const { report, parsed, target } = targetFromReport(absoluteReportPath);
  const evidenceDirectory = dirname(absoluteReportPath);
  const explorerId = parsed.explorerId;
  const baselineState = await readState(target, explorerId);
  const baselineSignature = publicationStateSignature(baselineState);
  const baselineReports = qualityReports(baselineState);
  const baselineReceiptId = String(baselineReports[0].receiptId ?? '');
  const baselineOutputId = outputID(report, baselineState);
  assert.ok(baselineReceiptId && baselineOutputId, 'baseline publication identity is incomplete');
  const baselinePreview = await readPublishedPreview(target, explorerId, baselineReceiptId, baselineOutputId);
  assert.ok(baselinePreview.rows.length > 0, 'baseline publication is not readable');

  const result = {
    status: 'passed',
    generated_at: new Date().toISOString(),
    source: { report: absoluteReportPath, compose_project: target.composeProject, api_url: target.apiUrl, project: target.fixtureProject, generation: target.fixtureGeneration, explorer_id: explorerId, output_id: baselineOutputId },
    baseline: { signature: baselineSignature, receipt_id: baselineReceiptId, row_count: baselinePreview.rows.length, total_count: baselinePreview.totalCount },
    scenarios: {},
    limitations: ['The isolated loom-dev Compose server uses --no-auth; this proves publication fault isolation and response redaction, not an independent 401/403 authorization denial.'],
  };

  try {
    await restartAPI(target, { [QUALITY_LIMIT_ENV]: '1' });
    const incompleteReceiptId = await freshReceipt(target, explorerId, 'INCOMPLETE_FULL_QUALITY_SCAN');
    const incomplete = await publishReceipt(target, explorerId, incompleteReceiptId);
    const incompleteState = await readState(target, explorerId);
    const incompleteSignature = publicationStateSignature(incompleteState);
    assert.equal(incomplete.response.ok, false, 'incomplete quality scan unexpectedly published');
    assert.ok([500, 503].includes(incomplete.response.status), `incomplete quality scan returned HTTP ${incomplete.response.status}`);
    assert.equal(incompleteSignature.revisionId, baselineSignature.revisionId, 'incomplete quality scan moved active revision');
    assert.equal(incompleteSignature.executionId, baselineSignature.executionId, 'incomplete quality scan moved active execution');
    assert.deepEqual(await readPublishedPreview(target, explorerId, baselineReceiptId, baselineOutputId), baselinePreview, 'prior publication changed after incomplete scan');
    assertNoPublicationLeak(incomplete, incompleteReceiptId, ['INCOMPLETE_FULL_QUALITY_SCAN']);
    result.scenarios.incomplete_full_quality_scan = {
      receipt_id: incompleteReceiptId,
      quality_max_rows: 1,
      http_status: incomplete.response.status,
      error_code: errorCode(incomplete.value),
      retained_signature: incompleteSignature,
      retained_preview: { row_count: baselinePreview.rows.length, total_count: baselinePreview.totalCount },
      prior_readable: true,
      restricted_data_or_counts_leaked: false,
    };

    await restartAPI(target, {});
    await restartAPI(target, { [CONFLICT_ENV]: 'true' });
    const conflictReceiptId = await freshReceipt(target, explorerId, 'ACTIVATION_CONFLICT');
    const conflict = await publishReceipt(target, explorerId, conflictReceiptId);
    const conflictState = await readState(target, explorerId);
    const conflictSignature = publicationStateSignature(conflictState);
    assert.equal(conflict.response.ok, false, 'activation conflict unexpectedly published');
    assert.ok([409, 500].includes(conflict.response.status), `activation conflict returned HTTP ${conflict.response.status}`);
    assert.equal(conflictSignature.revisionId, baselineSignature.revisionId, 'activation conflict moved active revision');
    assert.equal(conflictSignature.executionId, baselineSignature.executionId, 'activation conflict moved active execution');
    assert.deepEqual(await readPublishedPreview(target, explorerId, baselineReceiptId, baselineOutputId), baselinePreview, 'prior publication changed after activation conflict');
    assertNoPublicationLeak(conflict, conflictReceiptId, ['ACTIVATION_CONFLICT']);
    const conflictLogs = logsFor(target);
    const conflictProven = conflictLogs.includes('project release activation compare-and-swap conflict');
    assert.equal(conflictProven, true, 'API logs did not prove the activation CAS conflict');
    const logPath = join(evidenceDirectory, 'b07-activation-conflict.log');
    writeFileSync(logPath, conflictLogs, { mode: 0o600 });
    result.scenarios.activation_conflict = {
      receipt_id: conflictReceiptId,
      http_status: conflict.response.status,
      error_code: errorCode(conflict.value),
      retained_signature: conflictSignature,
      retained_preview: { row_count: baselinePreview.rows.length, total_count: baselinePreview.totalCount },
      prior_readable: true,
      restricted_data_or_counts_leaked: false,
      conflict_log_proven: conflictProven,
      log_path: logPath,
    };
  } finally {
    await restartAPI(target, {});
  }

  const destination = resolve(outputPath || join(evidenceDirectory, 'b07-fault-verification.json'));
  mkdirSync(dirname(destination), { recursive: true, mode: 0o700 });
  writeFileSync(destination, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
  return { ...result, outputPath: destination };
};

const main = async () => {
  const reportPath = process.argv[2];
  const outputPath = process.argv[3];
  if (!reportPath) throw new Error('usage: node scripts/verify-b07-faults.mjs VERIFY_FAST_REPORT [OUTPUT_PATH]');
  const result = await run(reportPath, outputPath);
  console.log(JSON.stringify({ output: result.outputPath, scenarios: result.scenarios }, null, 2));
};

export { run };

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => {
    console.error(error.stack ?? error.message ?? error);
    process.exitCode = 1;
  });
}
