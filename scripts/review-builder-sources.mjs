#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { dirname, isAbsolute, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { reviewedSourceDrift, refreshReviewedSources } from './lib/reviewed-sources.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const DEFAULT_MANIFEST = 'verification/builder-interactions/reviewed-sources.json';
const usage = `Usage:
  node scripts/review-builder-sources.mjs --check [--manifest <path>]
  node scripts/review-builder-sources.mjs --refresh --note "<why>" [--manifest <path>]

--check    print every reviewed source that drifted from its pin; exit 1 on drift
--refresh  repin reviewed sources to committed git blobs; refuses a dirty working tree
--note     required with --refresh; records why the pins moved
--manifest manifest path (default ${DEFAULT_MANIFEST})`;

const options = { check: false, refresh: false, note: undefined, manifest: undefined };
for (let i = 2; i < process.argv.length; i++) {
    const argument = process.argv[i];
    if (argument === '--check') options.check = true;
    else if (argument === '--refresh') options.refresh = true;
    else if (argument === '--help' || argument === '-h') {
        console.log(usage);
        process.exit(0);
    }
    else if (argument === '--note' || argument === '--manifest') {
        const value = process.argv[i + 1];
        if (value === undefined || value.startsWith('--'))
            fail(`${argument} requires a value\n\n${usage}`);
        options[argument === '--note' ? 'note' : 'manifest'] = value;
        i++;
    }
    else fail(`Unknown option: ${argument}\n\n${usage}`);
}

function fail(message) {
    process.stderr.write(`${message}\n`);
    process.exit(2);
}

if (options.check === options.refresh)
    fail(`Pass exactly one of --check or --refresh\n\n${usage}`);
if (options.refresh && (options.note === undefined || options.note.trim() === ''))
    fail(`--refresh requires --note "<why>"\n\n${usage}`);
if (options.check && options.note !== undefined)
    fail(`--note is only valid with --refresh\n\n${usage}`);

const manifestPath = options.manifest
    ? (isAbsolute(options.manifest) ? options.manifest : resolve(root, options.manifest))
    : resolve(root, DEFAULT_MANIFEST);

if (options.refresh) {
    const refreshed = refreshReviewedSources({ root, manifestPath, note: options.note });
    console.log(`Refreshed ${Object.keys(refreshed.files).length} reviewed source pin(s) at ${refreshed.baselineCommit} (${refreshed.pinFormat}).`);
    process.exit(0);
}

const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
const report = reviewedSourceDrift(root, manifest);
if (report.clean) {
    console.log(`Reviewed sources match ${Object.keys(manifest.files ?? {}).length} ${report.pinFormat} pin(s)${report.baselineCommit ? ` from ${report.baselineCommit}` : ''}.`);
    process.exit(0);
}
for (const file of report.drift) {
    console.log(`Review required: ${file.path}`);
    console.log(`  expected ${file.expected}`);
    console.log(`  actual   ${file.actual ?? '<missing>'}`);
    if (file.error) console.log(`  error    ${file.error}`);
    if (file.diff) console.log(`  inspect  ${file.diff}`);
}
console.log(`${report.drift.length} reviewed source file(s) drifted from ${report.pinFormat} pin(s)${report.baselineCommit ? ` at baseline ${report.baselineCommit}` : ''}.`);
process.exit(1);
