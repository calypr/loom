import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  loadRetainedBrowserEvidenceArchive,
  resolveEvidenceReference,
} from '../../../generate-builder-browser-evidence.mjs';

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../');
const retainedEvidenceManifest = 'verification/builder-interactions/retained-browser-evidence/manifest.json';
const generatorScript = 'scripts/generate-builder-browser-evidence.mjs';

const runEvidenceCliWithoutPlaywright = (args) => {
  const directory = mkdtempSync(join(tmpdir(), 'loom-evidence-no-playwright-'));
  const loader = join(directory, 'reject-playwright.mjs');
  writeFileSync(loader, `export async function resolve(specifier, context, nextResolve) {
  if (specifier === '@playwright/test') {
    const error = new Error('static evidence checks must not load Playwright');
    error.code = 'ERR_PLAYWRIGHT_IMPORT_BLOCKED';
    throw error;
  }
  return nextResolve(specifier, context);
}`);
  try {
    return spawnSync(process.execPath, [
      '--no-warnings',
      '--experimental-loader', loader,
      generatorScript,
      ...args,
    ], {
      cwd: repositoryRoot,
      encoding: 'utf8',
      timeout: 120_000,
    });
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
};

test('Builder browser evidence self-test does not load Playwright', () => {
  const result = runEvidenceCliWithoutPlaywright(['--self-test']);

  assert.equal(result.status, 0, [result.stdout, result.stderr].filter(Boolean).join('\n'));
  assert.match(result.stdout, /PASS retained Builder journey evidence/);
});

test('Builder evidence generation and check use retained reports without loading Playwright', () => {
  const temporaryDirectory = mkdtempSync(join(tmpdir(), 'loom-browser-evidence-check-'));
  const temporaryOutput = join(temporaryDirectory, 'browser-evidence.json');
  try {
    writeFileSync(temporaryOutput,
      readFileSync(resolve(repositoryRoot, 'verification/builder-interactions/browser-evidence.json')));
    const generated = runEvidenceCliWithoutPlaywright(['--from-evidence', '--output', temporaryOutput]);
    assert.equal(generated.status, 0, [generated.stdout, generated.stderr].filter(Boolean).join('\n'));
    assert.match(generated.stdout, /wrote .*browser-evidence\.json with 3 browser case\(s\) and 4 retained journey run\(s\)/);

    const checked = runEvidenceCliWithoutPlaywright(['--from-evidence', '--output', temporaryOutput, '--check']);
    assert.equal(checked.status, 0, [checked.stdout, checked.stderr].filter(Boolean).join('\n'));
    assert.match(checked.stdout, /PASS .*browser-evidence\.json is current:/);
  } finally {
    rmSync(temporaryDirectory, { recursive: true, force: true });
  }
});

test('retained evidence archive rejects missing and corrupted payloads', () => {
  const retained = loadRetainedBrowserEvidenceArchive(repositoryRoot);
  const manifest = JSON.parse(readFileSync(resolve(repositoryRoot, retainedEvidenceManifest), 'utf8'));
  assert.equal(retained.size, 5);

  const entry = manifest.artifacts[0];
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'loom-retained-evidence-'));
  const manifestPath = resolve(temporaryRoot, retainedEvidenceManifest);
  const archivePath = resolve(temporaryRoot, entry.archivePath);
  try {
    mkdirSync(dirname(manifestPath), { recursive: true });
    writeFileSync(manifestPath, JSON.stringify({ schemaVersion: 1, artifacts: [entry] }, null, 2) + '\n');
    assert.throws(() => loadRetainedBrowserEvidenceArchive(temporaryRoot), /archive is missing or unreadable/);

    mkdirSync(dirname(archivePath), { recursive: true });
    writeFileSync(archivePath, Buffer.alloc(entry.archiveBytes));
    assert.throws(() => loadRetainedBrowserEvidenceArchive(temporaryRoot), /compressed SHA-256 mismatch/);
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});

test('retained evidence path aliases stay inside the archive and reject unmatched external paths', () => {
  const retained = loadRetainedBrowserEvidenceArchive(repositoryRoot);
  const nativeReport = [...retained.values()].find((entry) => entry.archivePath.endsWith('/native-report.json.gz'));
  const tlaReceipt = [...retained.values()].find((entry) => entry.archivePath.endsWith('/formal-tla-receipt.json.gz'));
  const report = JSON.parse(nativeReport.bytes.toString('utf8'));
  const replayReportPath = report.target?.builderJourneyTrace?.replayReportPath;
  assert.equal(typeof replayReportPath, 'string');
  assert.equal(
    resolveEvidenceReference(replayReportPath, repositoryRoot, retained, 'formal replay output'),
    resolve(repositoryRoot, tlaReceipt.path),
  );

  const logicalPath = retained.keys().next().value;
  const absoluteSourcePath = '/private/tmp/source-checkout/' + logicalPath;
  assert.equal(
    resolveEvidenceReference(absoluteSourcePath, repositoryRoot, retained, 'fixture artifact'),
    resolve(repositoryRoot, logicalPath),
  );
  assert.throws(() => resolveEvidenceReference(
    '/private/tmp/unrelated-output/report.json',
    repositoryRoot,
    retained,
    'unrelated artifact',
  ), /outside the checkout and does not match one retained evidence manifest entry/);
});
