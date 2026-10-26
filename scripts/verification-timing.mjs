#!/usr/bin/env node
import { randomUUID } from 'node:crypto';
import { closeSync, existsSync, lstatSync, mkdirSync, openSync, readFileSync, readdirSync, realpathSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const phases = new Set(['diagnosis', 'implementation', 'review', 'integration', 'harness-repair']);
const repoRoot = realpathSync(resolve(dirname(fileURLToPath(import.meta.url)), '..'));
const outside = (path, root) => {
  const rel = relative(root, path);
  return rel === '..' || rel.startsWith(`..${sep}`) || isAbsolute(rel);
};

function timestamp(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(value)) return null;
  const millis = Date.parse(value);
  return Number.isFinite(millis) ? millis : null;
}

function spanDirectory(path, create = false) {
  const requested = resolve(path);
  if (!outside(requested, repoRoot)) throw new Error('spans directory must resolve outside the repository');
  if (create) mkdirSync(requested, { recursive: true, mode: 0o700 });
  if (!existsSync(requested) || !lstatSync(requested).isDirectory()) throw new Error('spans directory is missing or is not a directory');
  const actual = realpathSync(requested);
  if (!outside(actual, repoRoot)) throw new Error('spans directory must resolve outside the repository');
  return actual;
}

function writeExclusive(path, value) {
  const fd = openSync(path, 'wx', 0o600);
  try { writeFileSync(fd, JSON.stringify(value) + '\n'); } finally { closeSync(fd); }
}

export function startSpan({ spansDir, owner, caseId, phase, now = () => new Date() }) {
  if (typeof owner !== 'string' || !owner.trim() || typeof caseId !== 'string' || !caseId.trim() || !phases.has(phase)) {
    throw new Error('owner, case ID, and a supported phase are required');
  }
  const dir = spanDirectory(spansDir, true);
  const spanId = randomUUID();
  const path = resolve(dir, `${spanId}.start.json`);
  writeExclusive(path, { owner: owner.trim(), caseId: caseId.trim(), phase, startedAt: now().toISOString() });
  return { spanId, path };
}

export function finishSpan({ spansDir, spanId, now = () => new Date() }) {
  if (!/^[0-9a-f-]{36}$/i.test(spanId ?? '')) throw new Error('invalid span ID');
  const dir = spanDirectory(spansDir);
  const startPath = resolve(dir, `${spanId}.start.json`);
  if (!existsSync(startPath) || !lstatSync(startPath).isFile()) throw new Error('span start does not exist');
  const start = JSON.parse(readFileSync(startPath, 'utf8'));
  if (!timestamp(start.startedAt)) throw new Error('span start timestamp is invalid');
  const finishedAt = now().toISOString();
  if (timestamp(finishedAt) <= timestamp(start.startedAt)) throw new Error('span end must follow its start');
  const path = resolve(dir, `${spanId}.end.json`);
  writeExclusive(path, { finishedAt });
  return { spanId, path };
}

function mergeIntervals(intervals) {
  const sorted = [...intervals].sort((a, b) => a.start - b.start || a.end - b.end);
  const merged = [];
  for (const interval of sorted) {
    const last = merged.at(-1);
    if (last && interval.start <= last.end) last.end = Math.max(last.end, interval.end);
    else merged.push({ ...interval });
  }
  return merged;
}

function total(intervals) { return intervals.reduce((sum, item) => sum + item.end - item.start, 0); }
function validInterval(start, end) {
  const a = timestamp(start); const b = timestamp(end);
  return a !== null && b !== null && b > a ? { start: a, end: b } : null;
}

export function buildReport({ summaryPaths, spansDir, generatedAt = new Date().toISOString() }) {
  const summaries = summaryPaths.map((path) => {
    try {
      const summary = JSON.parse(readFileSync(path, 'utf8'));
      const interval = validInterval(summary.startedAt, summary.finishedAt);
      const commandDuration = summary.commands?.playwright?.durationMs;
      return {
        path, status: interval ? 'measured' : 'unknown', interval,
        browserCommandDurationMs: Number.isFinite(commandDuration) && commandDuration >= 0 ? commandDuration : null,
        reason: interval ? undefined : 'missing, invalid, or nonpositive bracket timestamps',
      };
    } catch (error) { return { path, status: 'unknown', interval: null, reason: error.code === 'ENOENT' ? 'summary missing' : 'summary unreadable or malformed' }; }
  });
  const spans = []; const openSpans = []; const unknownSpans = [];
  let spanHistoryStatus = spansDir ? 'unknown' : 'not_supplied';
  if (spansDir) {
    try {
      const dir = spanDirectory(spansDir);
      const files = readdirSync(dir).filter((name) => /^[0-9a-f-]{36}\.(?:start|end)\.json$/i.test(name));
      if (files.length) spanHistoryStatus = 'available';
      const starts = new Map(); const ends = new Map();
      for (const name of files) {
        const id = name.replace(/\.(?:start|end)\.json$/i, '');
        try {
          const fullPath = resolve(dir, name);
          if (!lstatSync(fullPath).isFile()) throw new Error('not a regular file');
          const value = JSON.parse(readFileSync(fullPath, 'utf8'));
          (name.endsWith('.start.json') ? starts : ends).set(id, value);
        } catch { unknownSpans.push({ spanId: id, reason: 'span file unreadable or malformed' }); }
      }
      for (const [id, start] of starts) {
        const end = ends.get(id);
        if (!phases.has(start.phase) || typeof start.owner !== 'string' || typeof start.caseId !== 'string') {
          unknownSpans.push({ spanId: id, reason: 'span metadata invalid' }); continue;
        }
        if (!end) { openSpans.push({ spanId: id, owner: start.owner, caseId: start.caseId, phase: start.phase }); continue; }
        const interval = validInterval(start.startedAt, end.finishedAt);
        if (!interval) { unknownSpans.push({ spanId: id, reason: 'missing, invalid, or nonpositive span timestamps' }); continue; }
        spans.push({ ...interval, owner: start.owner, caseId: start.caseId, phase: start.phase });
      }
      for (const id of ends.keys()) if (!starts.has(id)) unknownSpans.push({ spanId: id, reason: 'span start missing' });
    } catch { spanHistoryStatus = 'unknown'; unknownSpans.push({ reason: 'spans directory missing or unreadable' }); }
  }
  const brackets = summaries.flatMap((item) => item.interval ? [item.interval] : []);
  const phasesReport = Object.fromEntries([...phases].map((phase) => {
    const items = spans.filter((span) => span.phase === phase);
    const wallMs = total(mergeIntervals(items)); const summedMs = total(items);
    return [phase, { wallUnionMs: wallMs, summedDurationMs: summedMs, overlapMs: summedMs - wallMs }];
  }));
  const browserDurations = summaries.flatMap((item) => item.browserCommandDurationMs === null || item.browserCommandDurationMs === undefined
    ? [] : [item.browserCommandDurationMs]);
  const observed = [...brackets, ...spans];
  const window = observed.length ? { start: new Date(Math.min(...observed.map((x) => x.start))).toISOString(), end: new Date(Math.max(...observed.map((x) => x.end))).toISOString() } : null;
  const windowMs = window ? timestamp(window.end) - timestamp(window.start) : null;
  const attributedMs = total(mergeIntervals(observed));
  return {
    generatedAt,
    summaryInputs: summaries.map(({ path, status, reason }) => ({ path, status, ...(reason ? { reason } : {}) })),
    bracket: { measuredCount: brackets.length, wallUnionMs: total(mergeIntervals(brackets)) },
    browserCommands: { measuredCount: browserDurations.length, summedDurationMs: browserDurations.reduce((sum, ms) => sum + ms, 0) },
    spansDirectory: spansDir ?? null,
    spanHistoryStatus,
    phaseSpans: spans.map((span) => ({
      owner: span.owner, caseId: span.caseId, phase: span.phase,
      startedAt: new Date(span.start).toISOString(), finishedAt: new Date(span.end).toISOString(),
      durationMs: span.end - span.start,
    })),
    phaseDurations: phasesReport,
    openSpans,
    unknownSpans,
    observationWindow: window ? { ...window, elapsedMs: windowMs, unattributedGapMs: windowMs - attributedMs } : null,
    interpretation: 'Bracket intervals use summary startedAt/finishedAt; browser command durations are summed separately when present. Windows and gaps use only closed intervals. Gaps do not estimate any phase or reasoning time. Supplied summaries are not a closed-workflow count; retained-history freshness is not assessed.',
  };
}

function parseArgs(args) {
  const [command, ...rest] = args; const options = new Map(); const summaries = [];
  for (let i = 0; i < rest.length; i += 1) {
    const key = rest[i]; const value = rest[i + 1];
    if (!key.startsWith('--') || !value || value.startsWith('--')) throw new Error(`invalid argument: ${key}`);
    if (key === '--summary') { summaries.push(value); i += 1; continue; }
    if (options.has(key)) throw new Error(`duplicate option: ${key}`);
    options.set(key, value); i += 1;
  }
  const allowed = command === 'start' ? ['--spans', '--owner', '--case-id', '--phase']
    : command === 'finish' ? ['--spans', '--span-id']
      : command === 'report' ? ['--spans'] : [];
  if ([...options.keys()].some((key) => !allowed.includes(key))) throw new Error('unsupported option for command');
  if (command !== 'report' && summaries.length) throw new Error('--summary is supported only by report');
  if (command === 'report' && summaries.length === 0) throw new Error('report requires at least one --summary path');
  return { command, options, summaries };
}

async function main() {
  const { command, options, summaries } = parseArgs(process.argv.slice(2));
  let result;
  if (command === 'start') result = startSpan({ spansDir: options.get('--spans'), owner: options.get('--owner'), caseId: options.get('--case-id'), phase: options.get('--phase') });
  else if (command === 'finish') result = finishSpan({ spansDir: options.get('--spans'), spanId: options.get('--span-id') });
  else if (command === 'report' && summaries.length) result = buildReport({ summaryPaths: summaries, spansDir: options.get('--spans') });
  else throw new Error('usage: verification-timing.mjs start|finish|report [options]');
  process.stdout.write(JSON.stringify(result, null, 2) + '\n');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => { process.stderr.write(`verification timing: ${error.message}\n`); process.exitCode = 1; });
}
