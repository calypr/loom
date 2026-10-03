#!/usr/bin/env node

import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dataframeOutputQuery } from '../ui/packages/loom-ui/src/dataframeOutputQuery.mjs';

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(SCRIPT_DIR, '..');
const DEFAULT_SAMPLE_COUNT = 5;
const DEFAULT_TRACE_LIMIT = 25;
const DEFAULT_PREVIEW_LIMIT = 25;
const TARGETED_LOOP_LIMIT_MS = 30_000;
const LOOPBACK_HOSTS = new Set(['127.0.0.1', 'localhost', '::1']);

const elapsedMilliseconds = (started) => performance.now() - started;

export const median = (values) => {
  assert.ok(Array.isArray(values) && values.length > 0, 'median requires at least one sample');
  assert.ok(values.every((value) => Number.isFinite(value)), 'median requires finite samples');
  const sorted = [...values].sort((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 1
    ? sorted[middle]
    : (sorted[middle - 1] + sorted[middle]) / 2;
};

const parseUnitBytes = (value) => {
  const match = String(value).trim().match(/^([0-9]+(?:\.[0-9]+)?)\s*(B|KiB|MiB|GiB|TiB|KB|MB|GB|TB)?$/i);
  if (!match) throw new Error(`invalid byte size: ${value}`);
  const unit = String(match[2] ?? 'B').toUpperCase();
  const multipliers = {
    B: 1,
    KB: 1_000,
    MB: 1_000_000,
    GB: 1_000_000_000,
    TB: 1_000_000_000_000,
    KIB: 1024,
    MIB: 1024 ** 2,
    GIB: 1024 ** 3,
    TIB: 1024 ** 4,
  };
  return Math.round(Number(match[1]) * multipliers[unit]);
};

export const parseDockerMemoryBytes = (value) => {
  const used = String(value).split('/')[0]?.trim();
  if (!used) throw new Error(`invalid Docker memory usage: ${value}`);
  return parseUnitBytes(used);
};

export const parseProcStatusRSSBytes = (value) => {
  const match = String(value).match(/(?:^|\n)VmRSS:\s*([0-9]+)\s*kB(?:\s|$)/i);
  if (!match) throw new Error('VmRSS is missing from /proc status');
  return Number(match[1]) * 1024;
};

export const qualityEvidenceJSONBytes = (qualityReports) =>
  Buffer.byteLength(JSON.stringify(Array.isArray(qualityReports) ? qualityReports : []), 'utf8');

export const tracePageSummary = (trace, limit, offset = 0) => {
  assert.ok(trace && typeof trace === 'object', 'cell trace response is missing');
  const contributions = Array.isArray(trace.contributions) ? trace.contributions : [];
  assert.ok(Number.isInteger(limit) && limit > 0, 'trace limit must be positive');
  assert.ok(contributions.length <= limit, `trace returned ${contributions.length} contributions for limit ${limit}`);
  if (trace.hasMore) {
    assert.equal(trace.nextOffset, offset + contributions.length, 'trace nextOffset must advance by the returned page size');
  } else {
    assert.ok(trace.nextOffset === undefined || trace.nextOffset === 0 || trace.nextOffset >= offset, 'trace nextOffset cannot move backwards');
  }
  return {
    status: trace.status,
    contributionCount: contributions.length,
    hasMore: Boolean(trace.hasMore),
    nextOffset: Number(trace.nextOffset ?? 0),
    complete: Boolean(trace.complete),
  };
};

const runID = (value) => String(value).replace(/[^a-z0-9_-]/gi, '-').slice(-48);

export const parseVerifyFastReport = (report, reportPath = '<report>') => {
  assert.equal(report?.status, 'passed', `${reportPath} must be a passed verify-fast report`);
  const target = report.target;
  assert.ok(target && typeof target === 'object', `${reportPath} has no target`);
  const api = new URL(String(target.apiUrl ?? ''));
  assert.equal(api.protocol, 'http:', 'performance probe requires HTTP loopback API');
  assert.ok(LOOPBACK_HOSTS.has(api.hostname), 'performance probe requires a loopback API');
  assert.match(String(target.composeProject ?? ''), /^loom-dev-/);
  assert.match(String(target.project ?? ''), /^loom_dev_verify_[a-z0-9_-]+$/);
  assert.match(String(target.explorerId ?? ''), /^loom-dev-verification-[a-z0-9_-]+$/);
  const materialization = target.materialization;
  assert.ok(materialization && typeof materialization === 'object', `${reportPath} has no retained materialization`);
  const outputId = String(materialization.selector?.output ?? materialization.name ?? '').trim();
  assert.ok(outputId, `${reportPath} has no materialized output selector`);
  return Object.freeze({
    reportPath: resolve(reportPath),
    reportStatus: report.status,
    apiUrl: api.origin,
    composeProject: String(target.composeProject),
    project: String(target.project),
    generation: String(target.generation ?? materialization.datasetGeneration ?? ''),
    explorerId: String(target.explorerId),
    outputId,
    selector: materialization.selector ?? { output: outputId },
    materializationId: String(materialization.id ?? ''),
  });
};

export const summarizeLatency = (samples, key) => {
  const values = samples.map((sample) => Number(sample[key]));
  return {
    samples: values,
    median_ms: median(values),
    min_ms: Math.min(...values),
    max_ms: Math.max(...values),
  };
};

const authoringURL = (target, suffix) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}/authoring/v2${suffix}`;

const explorerURL = (target) =>
  `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers/${encodeURIComponent(target.explorerId)}`;

const requestJSON = async (url, options = {}) => {
  const started = performance.now();
  const response = await fetch(url, {
    ...options,
    signal: options.signal ?? AbortSignal.timeout(options.timeoutMs ?? 120_000),
  });
  const text = await response.text();
  let value = null;
  try {
    value = text ? JSON.parse(text) : null;
  } catch {
    value = { raw: text };
  }
  const result = {
    response,
    value,
    status: response.status,
    elapsedMs: elapsedMilliseconds(started),
    responseBytes: Buffer.byteLength(text, 'utf8'),
  };
  if (!response.ok) {
    const detail = JSON.stringify(value?.error ?? value).slice(0, 1000);
    throw new Error(`${options.method ?? 'GET'} ${url} failed: HTTP ${response.status} ${detail}`);
  }
  return result;
};

const postJSON = (url, body, timeoutMs = 120_000) => requestJSON(url, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
  timeoutMs,
});

const getState = async (target) => (await requestJSON(explorerURL(target))).value;

const outputFromState = (state, outputId) => {
  const runtime = state?.runtime;
  const output = runtime?.outputs?.find((candidate) => candidate.outputId === outputId || candidate.name === outputId)
    ?? runtime?.outputs?.[0];
  assert.ok(output, `published Explorer has no runtime output ${outputId}`);
  return output;
};

const qualityReportsFromState = (state) => {
  const reports = state?.runtime?.qualityReports ?? state?.generated?.qualityReports;
  assert.ok(Array.isArray(reports) && reports.length > 0, 'published Explorer has no quality reports');
  return reports;
};

const receiptFromState = (state, outputId) => {
  const report = qualityReportsFromState(state).find((candidate) => candidate.output === outputId)
    ?? qualityReportsFromState(state)[0];
  assert.ok(String(report.receiptId ?? '').trim(), 'quality report has no receiptId');
  return String(report.receiptId);
};

const getBuilder = async (target) => (await requestJSON(authoringURL(target, '/builder'))).value;

const publishedRows = async (target, output, selector) => {
  const result = await postJSON(`${target.apiUrl}/graphql/graph`, {
    query: dataframeOutputQuery('B07TraceCoordinate'),
    variables: {
      input: {
        projectId: target.project,
        selector,
        columns: output.columns.map((column) => column.column),
        first: 25,
      },
    },
  });
  const page = result.value?.data?.dataframeRows;
  assert.ok(page && Array.isArray(page.rowIds) && page.rowIds.length > 0, 'published GraphQL rows did not expose an opaque row identity');
  return page;
};

const preview = async (target, receiptId, outputId) => {
  const result = await postJSON(authoringURL(target, '/preview'), {
    receiptId,
    outputId,
    limit: DEFAULT_PREVIEW_LIMIT,
  });
  assert.ok(Array.isArray(result.value?.rows) && result.value.rows.length > 0, 'preview returned no rows');
  assert.ok(Array.isArray(result.value?.columns) && result.value.columns.length > 0, 'preview returned no columns');
  return result;
};

const traceCoordinate = (previewResult) => {
  const column = previewResult.value.columns.find((candidate) => candidate.column && candidate.column !== '__loom_row_id')?.column;
  assert.ok(column, 'preview did not expose a traceable output column');
  return { column };
};

const trace = async (target, receiptId, outputId, coordinate, offset = 0) => {
  const result = await postJSON(authoringURL(target, '/cell-trace'), {
    receiptId,
    outputId,
    rowId: coordinate.rowId,
    column: coordinate.column,
    offset,
    limit: DEFAULT_TRACE_LIMIT,
  });
  const summary = tracePageSummary(result.value?.trace, DEFAULT_TRACE_LIMIT, offset);
  return { ...result, summary };
};

const freshReceipt = async (target, sampleIndex) => {
  const builder = await getBuilder(target);
  const output = builder?.workspace?.documents?.[0]?.output;
  assert.ok(output?.id, 'Builder has no output to publish');
  const title = `${output.title ?? 'B07 performance'} · B07-${runID(`${Date.now()}-${sampleIndex}`)}`;
  const command = await postJSON(authoringURL(target, '/commands'), {
    commandId: `b07-performance-${runID(`${Date.now()}-${sampleIndex}`)}`,
    semanticsVersion: builder.workspace?.semanticsVersion ?? 4,
    snapshotToken: builder.catalog?.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands: [{ type: 'RENAME_TABLE', outputId: output.id, title }],
  });
  const receipt = await postJSON(authoringURL(target, '/reconcile'), {
    snapshotToken: builder.catalog?.snapshotToken,
    draftVersion: command.value?.draftVersion,
    draftDigest: command.value?.draftDigest,
  });
  assert.ok(receipt.value?.receiptId, 'reconcile returned no receiptId');
  return receipt.value;
};

const publishFreshReceipt = async (target, sampleIndex) => {
  const receipt = await freshReceipt(target, sampleIndex);
  const result = await postJSON(authoringURL(target, '/publish'), { receiptId: receipt.receiptId });
  assert.equal(result.status, 200);
  assert.equal(result.value?.receiptId, receipt.receiptId);
  assert.ok(result.value?.revisionId, 'publish returned no revisionId');
  assert.ok(result.value?.outputs?.some((output) => output.state === 'READY'), 'publish did not return a READY output');
  return { ...result, receiptId: receipt.receiptId, revisionId: result.value.revisionId };
};

const dockerContainerID = (target) => {
  const result = execFileSync('docker', [
    'compose', '--project-name', target.composeProject, '--file', join(REPO_ROOT, 'compose.dev.yaml'), 'ps', '-q', 'loom-api',
  ], { cwd: REPO_ROOT, encoding: 'utf8' }).trim();
  if (!result) throw new Error(`Docker Compose has no loom-api container for ${target.composeProject}`);
  return result.split(/\s+/)[0];
};

export const containerRSS = (target) => {
  const container = dockerContainerID(target);
  try {
    const status = execFileSync('docker', ['exec', container, 'sh', '-c', "awk '/VmRSS:/ {print; exit}' /proc/1/status"], { encoding: 'utf8' });
    return { container, rssBytes: parseProcStatusRSSBytes(status), source: 'proc-vmrss' };
  } catch (error) {
    const stats = execFileSync('docker', ['stats', '--no-stream', '--format', '{{json .}}', container], { encoding: 'utf8' }).trim();
    const value = JSON.parse(stats);
    return {
      container,
      rssBytes: parseDockerMemoryBytes(value.MemUsage),
      source: 'docker-stats-memory-usage',
      memoryUsage: value.MemUsage,
      fallbackReason: String(error?.message ?? error).slice(0, 300),
    };
  }
};

const options = (argv) => {
  const values = { reportPath: '', outputPath: '', samples: DEFAULT_SAMPLE_COUNT, publicationSamples: 1 };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === '--samples') values.samples = Number(argv[++index]);
    else if (argument === '--publication-samples') values.publicationSamples = Number(argv[++index]);
    else if (argument === '--output') values.outputPath = argv[++index];
    else if (!values.reportPath) values.reportPath = argument;
    else throw new Error(`unknown argument: ${argument}`);
  }
  assert.ok(values.reportPath, 'usage: node scripts/measure-b07-evidence.mjs VERIFY_FAST_REPORT [--output PATH] [--samples N] [--publication-samples N]');
  assert.ok(Number.isInteger(values.samples) && values.samples > 0 && values.samples <= 20, '--samples must be an integer from 1 to 20');
  assert.ok(Number.isInteger(values.publicationSamples) && values.publicationSamples > 0 && values.publicationSamples <= 5, '--publication-samples must be an integer from 1 to 5');
  return values;
};

export async function measure(reportPath, { samples = DEFAULT_SAMPLE_COUNT, publicationSamples = 1, outputPath } = {}) {
  const report = JSON.parse(readFileSync(reportPath, 'utf8'));
  const target = parseVerifyFastReport(report, reportPath);
  const initialState = await getState(target);
  const initialQualityReports = qualityReportsFromState(initialState);
  const initialReceiptId = receiptFromState(initialState, target.outputId);
  const output = outputFromState(initialState, target.outputId);
  const previewSamples = [];
  const traceSamples = [];
  const rssSamples = [];
  const page = await publishedRows(target, output, target.selector);
  let coordinate;
  for (let index = 0; index < samples; index += 1) {
    const previewResult = await preview(target, initialReceiptId, target.outputId);
    coordinate ??= { ...traceCoordinate(previewResult), rowId: page.rowIds[0] };
    const traceResult = await trace(target, initialReceiptId, target.outputId, coordinate);
    previewSamples.push({
      sample: index + 1,
      latency_ms: previewResult.elapsedMs,
      response_bytes: previewResult.responseBytes,
      row_count: previewResult.value.rows.length,
      receipt_id: previewResult.value.receiptId,
    });
    traceSamples.push({
      sample: index + 1,
      latency_ms: traceResult.elapsedMs,
      response_bytes: traceResult.responseBytes,
      ...traceResult.summary,
    });
    rssSamples.push({ sample: index + 1, ...containerRSS(target) });
  }
  const publicationSamplesMeasured = [];
  for (let index = 0; index < publicationSamples; index += 1) {
    const publication = await publishFreshReceipt(target, index);
    const state = await getState(target);
    const qualityReports = qualityReportsFromState(state);
    publicationSamplesMeasured.push({
      sample: index + 1,
      latency_ms: publication.elapsedMs,
      response_bytes: publication.responseBytes,
      receipt_id: publication.receiptId,
      revision_id: publication.revisionId,
      quality_report_count: qualityReports.length,
      quality_evidence_json_bytes: qualityEvidenceJSONBytes(qualityReports),
    });
    rssSamples.push({ sample: `${index + 1}-publication`, ...containerRSS(target) });
  }
  const finalState = await getState(target);
  const finalQualityReports = qualityReportsFromState(finalState);
  const targetedLoopSamples = previewSamples.map((sample, index) => sample.latency_ms + traceSamples[index].latency_ms);
  const result = {
    status: 'PASSED',
    generated_at: new Date().toISOString(),
    source: {
      report: target.reportPath,
      report_status: target.reportStatus,
      compose_project: target.composeProject,
      api_url: target.apiUrl,
      project: target.project,
      generation: target.generation,
      explorer_id: target.explorerId,
      output_id: target.outputId,
      retained_materialization_id: target.materializationId,
      initial_receipt_id: initialReceiptId,
      trace_coordinate: coordinate,
      runtime_output: { output_id: output.outputId, name: output.name, row_label: output.rowLabel },
    },
    configuration: {
      samples,
      publication_samples: publicationSamples,
      preview_limit: DEFAULT_PREVIEW_LIMIT,
      trace_limit: DEFAULT_TRACE_LIMIT,
      targeted_loop_limit_ms: TARGETED_LOOP_LIMIT_MS,
    },
    measurements: {
      tracing_disabled_preview: {
        latency: summarizeLatency(previewSamples, 'latency_ms'),
        response_bytes: summarizeLatency(previewSamples, 'response_bytes'),
        rows: previewSamples.map(({ sample, row_count }) => ({ sample, row_count })),
      },
      targeted_cell_trace: {
        latency: summarizeLatency(traceSamples, 'latency_ms'),
        response_bytes: summarizeLatency(traceSamples, 'response_bytes'),
        pages: traceSamples,
      },
      non_idempotent_full_publication_quality_scan: {
        latency: summarizeLatency(publicationSamplesMeasured, 'latency_ms'),
        response_bytes: summarizeLatency(publicationSamplesMeasured, 'response_bytes'),
        samples: publicationSamplesMeasured,
      },
      quality_evidence: {
        initial_json_bytes: qualityEvidenceJSONBytes(initialQualityReports),
        final_json_bytes: qualityEvidenceJSONBytes(finalQualityReports),
        initial_report_count: initialQualityReports.length,
        final_report_count: finalQualityReports.length,
      },
      api_container_rss: {
        samples: rssSamples,
        max_bytes: Math.max(...rssSamples.map((sample) => sample.rssBytes)),
        median_bytes: median(rssSamples.map((sample) => sample.rssBytes)),
      },
      targeted_loop: {
        samples_ms: targetedLoopSamples,
        median_ms: median(targetedLoopSamples),
        max_ms: Math.max(...targetedLoopSamples),
      },
    },
    assertions: {
      trace_pages_bounded: traceSamples.every((sample) => sample.contributionCount <= DEFAULT_TRACE_LIMIT),
      targeted_loop_under_30_seconds: Math.max(...targetedLoopSamples) < TARGETED_LOOP_LIMIT_MS,
      fresh_publication_receipts: new Set(publicationSamplesMeasured.map((sample) => sample.receipt_id)).size === publicationSamplesMeasured.length,
      quality_evidence_present: finalQualityReports.length > 0,
    },
  };
  assert.ok(result.assertions.trace_pages_bounded, 'cell trace returned an unbounded page');
  assert.ok(result.assertions.targeted_loop_under_30_seconds, 'preview plus targeted trace exceeded 30 seconds');
  assert.ok(result.assertions.fresh_publication_receipts, 'publication samples reused a receipt and were not non-idempotent');
  assert.ok(result.assertions.quality_evidence_present, 'publication produced no quality evidence');
  const destination = resolve(outputPath || join(dirname(reportPath), 'b07-performance.json'));
  mkdirSync(dirname(destination), { recursive: true });
  writeFileSync(destination, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
  return { ...result, outputPath: destination };
}

async function main() {
  const values = options(process.argv.slice(2));
  const result = await measure(resolve(values.reportPath), values);
  console.log(JSON.stringify({ output: result.outputPath, assertions: result.assertions, targeted_loop: result.measurements.targeted_loop }, null, 2));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => {
    console.error(error.stack ?? error.message ?? error);
    process.exitCode = 1;
  });
}
