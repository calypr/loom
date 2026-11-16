import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

export const GIT_BLOB_PIN_FORMAT = 'git-blob-sha1';
const LEGACY_PIN_FORMAT = 'sha256';

function git(root, args, { allowFailure = false } = {}) {
    const result = spawnSync('git', args, { cwd: root, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
    if (result.error) throw result.error;
    if (result.status !== 0) {
        if (allowFailure) return { ok: false, stdout: result.stdout ?? '', stderr: result.stderr ?? '', status: result.status };
        throw new Error(`git ${args.join(' ')} failed (exit ${result.status}): ${(result.stderr || result.stdout || '').trim()}`);
    }
    return { ok: true, stdout: (result.stdout ?? '').trimEnd(), stderr: result.stderr ?? '', status: 0 };
}

export function reviewedSourcePinFormat(manifest) {
    if (manifest.pinFormat === undefined) return LEGACY_PIN_FORMAT;
    if (manifest.pinFormat === GIT_BLOB_PIN_FORMAT) return GIT_BLOB_PIN_FORMAT;
    throw new Error(`Unsupported reviewed-source pinFormat: ${JSON.stringify(manifest.pinFormat)}`);
}

function workingPin(root, path, pinFormat) {
    if (pinFormat === GIT_BLOB_PIN_FORMAT) {
        const result = git(root, ['hash-object', '--', path], { allowFailure: true });
        if (result.ok) return { actual: result.stdout, error: undefined };
        return { actual: null, error: (result.stderr || result.stdout || `git hash-object failed`).trim() };
    }
    try {
        return { actual: createHash('sha256').update(readFileSync(join(root, path))).digest('hex'), error: undefined };
    }
    catch (error) {
        return { actual: null, error: error.message };
    }
}

function pinnedFiles(manifest) {
    if (manifest === null || typeof manifest !== 'object' || manifest.files === null || typeof manifest.files !== 'object' || Array.isArray(manifest.files))
        throw new Error('Reviewed-source manifest must contain a files object');
    const entries = Object.entries(manifest.files);
    if (entries.length === 0)
        throw new Error('Reviewed-source manifest must pin at least one file');
    return entries;
}

export function reviewedSourceDrift(root, manifest) {
    const pinFormat = reviewedSourcePinFormat(manifest);
    const entries = pinnedFiles(manifest);
    const baselineCommit = manifest.baselineCommit;
    const files = entries.map(([path, expected]) => {
        const { actual, error } = workingPin(root, path, pinFormat);
        return {
            path,
            expected,
            actual,
            matches: actual === expected,
            ...(error ? { error } : {}),
            ...(baselineCommit ? { diff: `git diff ${baselineCommit}..HEAD -- ${path}` } : {}),
        };
    });
    const drift = files.filter((file) => !file.matches);
    return { pinFormat, baselineCommit, files, drift, clean: drift.length === 0 };
}

export function refreshReviewedSources({ root, manifestPath, commit = 'HEAD', note }) {
    if (typeof note !== 'string' || note.trim() === '')
        throw new Error('refreshReviewedSources requires a non-empty note');
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
    const paths = pinnedFiles(manifest).map(([path]) => path);
    const dirty = git(root, ['status', '--porcelain', '--', ...paths]);
    if (dirty.stdout !== '')
        throw new Error(`Refusing to refresh while pinned files are dirty in the working tree:\n${dirty.stdout}`);
    const baselineCommit = git(root, ['rev-parse', '--verify', `${commit}^{commit}`]).stdout;
    if (!/^[0-9a-f]{40,64}$/.test(baselineCommit))
        throw new Error(`Could not resolve ${commit} to a full commit: ${baselineCommit}`);
    const files = {};
    for (const path of paths)
        files[path] = git(root, ['rev-parse', '--verify', `${baselineCommit}:${path}`]).stdout;
    const { pinFormat: _pinFormat, baselineCommit: _baselineCommit, files: _files, repin: _repin, ...retained } = manifest;
    const repin = { commit: baselineCommit, note, at: new Date().toISOString() };
    const updated = { baselineCommit, pinFormat: GIT_BLOB_PIN_FORMAT, ...retained, files, repin };
    writeFileSync(manifestPath, `${JSON.stringify(updated, null, 2)}\n`);
    return { manifestPath, baselineCommit, pinFormat: GIT_BLOB_PIN_FORMAT, files, repin };
}
