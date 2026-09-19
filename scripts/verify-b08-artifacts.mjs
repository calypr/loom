#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createDevSession, commandEnvironment, parseCSV, readStoredZip } from './loom-dev.mjs';
import { parseVerifyFastReport } from './measure-b07-evidence.mjs';

const FAULT_KEYS = [
  'LOOM_DEV_RECIPE_QUALITY_MAX_ROWS',
  'LOOM_DEV_RECIPE_QUALITY_MAX_DISTINCT_KEYS',
  'LOOM_DEV_ACTIVATION_CONFLICT_ONCE',
  'LOOM_DEV_ARTIFACT_MAX_ROWS',
  'LOOM_DEV_ARTIFACT_ROW_DELAY_MS',
];

const sleep = (milliseconds) => new Promise((resolvePromise) => setTimeout(resolvePromise, milliseconds));

const requestJSON = async (url, options = {}) => {
  const response = await fetch(url, { ...options, signal: options.signal ?? AbortSignal.timeout(options.timeoutMs ?? 120_000) });
  const text = await response.text();
  let value;
  try { value = text ? JSON.parse(text) : {}; } catch { value = { raw: text }; }
  return { response, value, text };
};

const postJSON = (url, body, timeoutMs = 120_000) => requestJSON(url, {
  method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), timeoutMs,
});

const authoringURL = (target, explorerId, suffix) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2${suffix}`;

const explorerURL = (target, explorerId) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}`;

const runCompose = (target, args, overrides = {}) => {
  const env = commandEnvironment(target);
  for (const key of FAULT_KEYS) env[key] = String(overrides[key] ?? '');
  try {
    return execFileSync('docker', ['compose', '--project-name', target.composeProject, '--file', target.composeFile, ...args], {
      cwd: target.sourceRoot, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    });
  } catch (error) {
    throw new Error(`docker compose ${args.join(' ')} failed: ${String(error.stderr ?? error.stdout ?? error.message)}`);
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
    } catch (error) { last = error instanceof Error ? error.message : String(error); }
    await sleep(500);
  }
  throw new Error(`timed out waiting for ${url}: ${last}`);
};

const restartAPI = async (target, overrides) => {
  runCompose(target, ['up', '-d', '--no-deps', '--force-recreate', 'loom-api'], overrides);
  await waitForHTTP(`${target.apiUrl}/readyz`);
};

const readState = async (target, explorerId) => {
  const result = await requestJSON(explorerURL(target, explorerId));
  if (!result.response.ok) throw new Error(`Explorer read failed: HTTP ${result.response.status}`);
  return result.value;
};

const publicationIdentity = (state) => {
  const publication = state?.runtime?.publication ?? state?.publication ?? {};
  const quality = state?.runtime?.qualityReports ?? state?.generated?.qualityReports ?? [];
  const output = state?.runtime?.outputs?.[0];
  return {
    revisionId: String(state?.active?.revisionId ?? state?.activeRevisionId ?? publication.revisionId ?? ''),
    executionId: String(publication.executionId ?? ''),
    receiptId: String(quality?.[0]?.receiptId ?? ''),
    outputId: String(output?.outputId ?? output?.name ?? ''),
  };
};

const freshReceipt = async (target, explorerId, label) => {
  const builderResult = await requestJSON(authoringURL(target, explorerId, '/builder'));
  if (!builderResult.response.ok) throw new Error(`Builder read failed: HTTP ${builderResult.response.status}`);
  const builder = builderResult.value;
  const output = builder?.workspace?.documents?.[0]?.output;
  assert.ok(output?.id, 'Builder has no output to republish');
  const commandResult = await postJSON(authoringURL(target, explorerId, '/commands'), {
    commandId: `b08-${label}-${Date.now().toString(36)}`,
    semanticsVersion: builder.workspace?.semanticsVersion ?? 4,
    snapshotToken: builder.catalog?.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands: [{ type: 'RENAME_TABLE', outputId: output.id, title: `${output.title ?? 'B08 export'} · ${label}` }],
  });
  if (!commandResult.response.ok) throw new Error(`artifact race command failed: HTTP ${commandResult.response.status} ${commandResult.text.slice(0, 500)}`);
  const reconcileResult = await postJSON(authoringURL(target, explorerId, '/reconcile'), {
    snapshotToken: builder.catalog?.snapshotToken,
    draftVersion: commandResult.value?.draftVersion,
    draftDigest: commandResult.value?.draftDigest,
  });
  if (!reconcileResult.response.ok || !reconcileResult.value?.receiptId) {
    throw new Error(`artifact race reconcile failed: HTTP ${reconcileResult.response.status} ${reconcileResult.text.slice(0, 500)}`);
  }
  return String(reconcileResult.value.receiptId);
};

const prepareArtifact = (target, explorerId, identity, idempotencyKey) =>
  postJSON(authoringURL(target, explorerId, '/artifacts'), {
    revisionId: identity.revisionId, outputId: identity.outputId, idempotencyKey,
  }, 180_000);

export const artifactID = ({ project, explorerId, revisionId, outputId, receiptId, executionId, idempotencyKey }) => {
  const encoded = JSON.stringify({
    Project: project, ExplorerID: explorerId, RevisionID: revisionId, OutputID: outputId,
    ReceiptID: receiptId, ExecutionID: executionId, IdempotencyKey: idempotencyKey,
  });
  return `artifact_${createHash('sha256').update(encoded).digest('hex')}`;
};

export const verifyMemberChecksums = (archive, manifest) => manifest.members.every((member) => {
  const contents = archive.get(member.name);
  return contents && contents.length === member.bytes && createHash('sha256').update(contents).digest('hex') === member.sha256;
});

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
  return { parsed, target: createDevSession(env, resolve(scriptDirectory, '..')) };
};

const run = async (reportPath, outputPath) => {
  const absoluteReportPath = resolve(reportPath);
  const { parsed, target } = targetFromReport(absoluteReportPath);
  const explorerId = parsed.explorerId;
  const evidenceDirectory = dirname(absoluteReportPath);
  const baselineState = await readState(target, explorerId);
  const baseline = publicationIdentity(baselineState);
  assert.ok(Object.values(baseline).every(Boolean), `baseline publication identity is incomplete: ${JSON.stringify(baseline)}`);
  const result = {
    status: 'passed', generated_at: new Date().toISOString(),
    source: { report: absoluteReportPath, compose_project: target.composeProject, api_url: target.apiUrl, project: target.fixtureProject, generation: target.fixtureGeneration, explorer_id: explorerId },
    scenarios: {},
  };

  try {
    await restartAPI(target, { LOOM_DEV_ARTIFACT_ROW_DELAY_MS: '2500' });
    const idempotencyKey = `b08-race-${Date.now().toString(36)}`;
    const artifactStartedAt = Date.now();
    const artifactPromise = prepareArtifact(target, explorerId, baseline, idempotencyKey);
    await sleep(250);
    const receiptB = await freshReceipt(target, explorerId, 'CONCURRENT_PUBLICATION');
    const publishedB = await postJSON(authoringURL(target, explorerId, '/publish'), { receiptId: receiptB }, 180_000);
    const publicationCompletedAt = Date.now();
    assert.equal(publishedB.response.status, 200, `concurrent publication failed: ${publishedB.text.slice(0, 500)}`);
    const artifact = await artifactPromise;
    const artifactCompletedAt = Date.now();
    assert.equal(artifact.response.status, 200, `pinned artifact failed: ${artifact.text.slice(0, 500)}`);
    assert.ok(publicationCompletedAt < artifactCompletedAt, 'publication B did not complete while artifact A was still preparing');

    const activeB = publicationIdentity(await readState(target, explorerId));
    assert.notEqual(activeB.executionId, baseline.executionId, 'concurrent publication did not advance the active execution');
    assert.equal(artifact.value.executionId, baseline.executionId, 'artifact followed publication B instead of pinned execution A');
    assert.equal(artifact.value.revisionId, baseline.revisionId, 'artifact followed publication B instead of pinned revision A');

    const downloadURL = `${authoringURL(target, explorerId, '/artifacts')}/${encodeURIComponent(artifact.value.id)}`;
    const download = await fetch(downloadURL, { signal: AbortSignal.timeout(120_000) });
    assert.equal(download.status, 200, `artifact download failed with HTTP ${download.status}`);
    const archiveBytes = Buffer.from(await download.arrayBuffer());
    const archivePath = join(evidenceDirectory, 'b08-concurrent-artifact.zip');
    writeFileSync(archivePath, archiveBytes, { mode: 0o600 });
    const archive = readStoredZip(archivePath);
    const manifest = JSON.parse(archive.get('manifest.json').toString('utf8'));
    const csvRows = parseCSV(archive.get('data.csv').toString('utf8'));
    assert.equal(manifest.identity.executionId, baseline.executionId, 'manifest execution changed during concurrent publication');
    assert.equal(manifest.identity.revisionId, baseline.revisionId, 'manifest revision changed during concurrent publication');
    assert.equal(manifest.rows, csvRows.length - 1, 'manifest row count does not match data.csv');
    assert.equal(verifyMemberChecksums(archive, manifest), true, 'artifact member checksum mismatch');
    const archiveDigest = createHash('sha256').update(archiveBytes).digest('hex');
    assert.equal(archiveDigest, artifact.value.archiveSha256, 'downloaded archive digest does not match preparation response');
    assert.equal(download.headers.get('x-loom-artifact-sha256'), archiveDigest, 'download digest header does not match archive');
    result.scenarios.concurrent_republish = {
      artifact_started_at: new Date(artifactStartedAt).toISOString(), publication_completed_at: new Date(publicationCompletedAt).toISOString(), artifact_completed_at: new Date(artifactCompletedAt).toISOString(),
      source_execution_id: baseline.executionId, active_execution_id_after_publish: activeB.executionId,
      artifact_id: artifact.value.id, archive_sha256: archiveDigest, archive_bytes: archiveBytes.length,
      rows: manifest.rows, features: manifest.features, member_checksums_match: true, archive_path: archivePath,
    };

    await restartAPI(target, { LOOM_DEV_ARTIFACT_MAX_ROWS: '1' });
    const activeState = await readState(target, explorerId);
    const active = publicationIdentity(activeState);
    const failedKey = `b08-failed-${Date.now().toString(36)}`;
    const failed = await prepareArtifact(target, explorerId, active, failedKey);
    assert.equal(failed.response.status, 503, `row-limited artifact returned HTTP ${failed.response.status}`);
    assert.equal(String(failed.value?.error?.code ?? failed.value?.code ?? ''), 'ARTIFACT_PREPARATION_FAILED', 'row-limited artifact returned the wrong error code');
    const failedArtifactID = artifactID({
      project: artifact.value.project, explorerId, revisionId: active.revisionId, outputId: active.outputId,
      receiptId: active.receiptId, executionId: active.executionId, idempotencyKey: failedKey,
    });
    const failedDownload = await requestJSON(`${authoringURL(target, explorerId, '/artifacts')}/${failedArtifactID}`);
    assert.equal(failedDownload.response.status, 503, `failed artifact download returned HTTP ${failedDownload.response.status}`);
    assert.equal(String(failedDownload.value?.error?.code ?? failedDownload.value?.code ?? ''), 'ARTIFACT_NOT_COMPLETE', 'failed artifact was not retained as incomplete');
    const failedRetry = await prepareArtifact(target, explorerId, active, failedKey);
    assert.equal(failedRetry.response.status, 503, `failed artifact retry returned HTTP ${failedRetry.response.status}`);
    assert.equal(String(failedRetry.value?.error?.code ?? failedRetry.value?.code ?? ''), 'ARTIFACT_FAILED_RETRYABLE', 'failed artifact retry did not preserve failed state');
    result.scenarios.failed_preparation = {
      artifact_id: failedArtifactID, max_rows: 1, http_status: failed.response.status,
      error_code: 'ARTIFACT_PREPARATION_FAILED', download_status: failedDownload.response.status,
      download_error_code: 'ARTIFACT_NOT_COMPLETE', retry_error_code: 'ARTIFACT_FAILED_RETRYABLE', complete_archive_visible: false,
    };
  } finally {
    await restartAPI(target, {});
  }

  const destination = resolve(outputPath || join(evidenceDirectory, 'b08-artifact-verification.json'));
  mkdirSync(dirname(destination), { recursive: true, mode: 0o700 });
  writeFileSync(destination, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
  return { ...result, outputPath: destination };
};

const main = async () => {
  const reportPath = process.argv[2];
  if (!reportPath) throw new Error('usage: node scripts/verify-b08-artifacts.mjs VERIFY_FAST_REPORT [OUTPUT_PATH]');
  const result = await run(reportPath, process.argv[3]);
  console.log(JSON.stringify({ output: result.outputPath, scenarios: result.scenarios }, null, 2));
};

export { publicationIdentity, run };

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => { console.error(error.stack ?? error.message ?? error); process.exitCode = 1; });
}
