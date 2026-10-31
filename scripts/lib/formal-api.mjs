import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve, delimiter } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const boundary = 'production HTTP routes, services and compiler; test-owned in-memory repository and external execution fixtures; not browser or durable database proof';
export const apiChecks = [
    { id: 'route-rows', title: 'Actual Arango route rows, persisted history and fresh repository read-back', package: './internal/server', tests: ['TestSavedRelatedRouteRowsAgainstArango'], integration: true, requiredEnvironment: ['LOOM_TEST_ARANGO_URL'], boundary: 'production public HTTP/service/compiler/query execution and actual Arango repository; isolated test-owned database; no browser or database-process-restart claim' },
    { id: 'route-roundtrip', title: 'Saved Related route proposal, Apply, restore and fresh-read identity', package: './internal/server', tests: ['TestSavedRelatedRouteProposalApplyRestoreAndFreshRead'], boundary },
    { id: 'publication', title: 'Publish receipt, stored revision and fresh Viewer agreement', package: './internal/server', tests: ['TestPublicationHTTPReceiptRevisionAndViewerAgreeAfterFreshRead'], boundary },
    { id: 'restore', title: 'Restore exact draft, fresh read-back, idempotency and rejected CAS/scope', package: './internal/server', tests: ['TestRestoreDraftRevisionPublicAPIIsScopedAndReloadVisible'], boundary },
    { id: 'apply', title: 'Save before accepted preview, capture failure and empty preview', package: './internal/server', tests: [
        'TestApplyConstructionProposalReturnsExactPreviewOnlyAfterSave',
        'TestApplyConstructionProposalCaptureFailureKeepsSaveAndOmitsAcceptedPreview',
        'TestApplyConstructionProposalCapturesEmptyPreviewAsEmptyRowsArray',
    ], boundary },
    { id: 'receipts', title: 'Current construction proposal and emitted receipt/output metadata', package: './internal/server', tests: [
        'TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly',
        'TestConstructionProposalCurrentCodingChoiceReconcilesAndPreviewsHTTP',
        'TestConstructionProposalRelatedCodingGroupReconcilesReceiptMetadataHTTP',
    ], boundary },
];
export function selectApiChecks(feature = 'all') {
    const checks = apiChecks.filter(check => feature === 'all' ? !check.integration : check.id === feature);
    assert.ok(checks.length, `Unknown API feature: ${feature}`);
    return checks;
}
export function apiSourceFingerprint() {
    const hash = createHash('sha256');
    const visit = relative => {
        for (const entry of readdirSync(join(root, relative), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
            const path = `${relative}/${entry.name}`;
            if (entry.isDirectory()) visit(path);
            else if (entry.isFile()) hash.update(path).update('\0').update(readFileSync(join(root, path))).update('\0');
        }
    };
    for (const directory of ['internal', 'generated', 'schemas']) visit(directory);
    for (const file of ['go.mod', 'go.sum', 'scripts/lib/formal-api.mjs']) hash.update(file).update('\0').update(readFileSync(join(root, file))).update('\0');
    return hash.digest('hex');
}
export function apiDoctor() {
    const declared = /^go (\d+)\.(\d+)\.(\d+)$/m.exec(readFileSync(join(root, 'go.mod'), 'utf8'));
    assert.ok(declared, 'Missing repository Go version');
    const candidates = process.env.GO ? (process.env.GO.includes('/') ? [process.env.GO] : (process.env.PATH || '').split(delimiter).filter(Boolean).map(path => join(path, process.env.GO))) : [
        ...(process.env.PATH || '').split(delimiter).filter(Boolean).map(path => join(path, 'go')),
        '/workspace/.loom-setup/go/bin/go',
    ];
    const attempts = [];
    for (const executable of new Set(candidates)) {
        if (!existsSync(executable)) continue;
        const result = spawnSync(executable, ['version'], { cwd: root, encoding: 'utf8', timeout: 3000 });
        const version = /^go version go(\d+)\.(\d+)\.(\d+)\b/.exec(result.stdout || '');
        if (!result.error && result.status === 0 && version && version.slice(1).every((part, index) => +part === +declared[index + 1])) {
            return { executable: resolve(executable), version: result.stdout.trim() };
        }
        attempts.push(executable);
    }
    throw new Error(`Install Go ${declared.slice(1).join('.')} or set GO to its executable; no compatible Go found (${attempts.join(', ')})`);
}
/** A successful package with no matching tests is not a successful API check. */
export function checkApiEvents(stdout, check) {
    assert.ok(Array.isArray(check.tests) && check.tests.length > 0, 'API check selects no named tests');
    assert.ok(check.tests.every(name => /^Test\w+$/.test(name)), 'Invalid API test name');
    const expected = new Set(check.tests);
    assert.equal(expected.size, check.tests.length, 'Duplicate API test names');
    const events = stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
    const tests = events.filter(event => event.Test && expected.has(event.Test));
    for (const name of expected) {
        assert.ok(tests.some(event => event.Test === name && event.Action === 'run'), `API test did not run: ${name}`);
        assert.ok(tests.some(event => event.Test === name && event.Action === 'pass'), `API test did not pass: ${name}`);
        assert.ok(!tests.some(event => event.Test === name && ['fail', 'skip'].includes(event.Action)), `API test failed or skipped: ${name}`);
    }
    assert.ok(!events.some(event => event.Action === 'fail'), 'API package or subtest failed');
    assert.ok(!events.some(event => event.Action === 'skip'), 'API test or subtest skipped');
    assert.ok(events.some(event => !event.Test && event.Action === 'pass'), 'API package did not pass');
    return { tests: [...expected], subtestsPassed: events.filter(event => event.Test && event.Action === 'pass' && !expected.has(event.Test)).length };
}
export function runApi({ feature = 'all', output }) {
    const checks = selectApiChecks(feature), tool = apiDoctor();
    for (const check of checks) for (const key of check.requiredEnvironment || []) assert.ok(process.env[key], `Set ${key} to an owned integration instance for ${check.id}`);
    const before = apiSourceFingerprint();
    const directory = resolve(output);
    assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Refusing to overwrite nonempty API evidence');
    mkdirSync(directory, { recursive: true });
    const evidence = { feature, notRun: apiChecks.filter(check => !checks.includes(check)).map(({ id, integration }) => ({ id, reason: integration ? 'explicit-integration-selection-required' : 'outside-selected-feature' })), tool, sourceFingerprint: before, boundary, status: 'running', checks: [] };
    const env = { ...process.env, GOTOOLCHAIN: 'local' };
    if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
        if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
        if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
    }
    try {
        for (const check of checks) {
            const args = ['test', '-json', '-timeout=30s', '-count=1', '-run', `^(${check.tests.join('|')})$`, check.package];
            const started = performance.now();
            const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024, detached: true });
            if (result.error && result.pid) {
                try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
            }
            writeFileSync(join(directory, `${check.id}.jsonl`), result.stdout || '');
            writeFileSync(join(directory, `${check.id}-process.json`), JSON.stringify({ executable: tool.executable, args, exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2));
            assert.ifError(result.error);
            assert.equal(result.status, 0, `API ${check.id} failed; see ${directory}/${check.id}.jsonl and ${check.id}-process.json`);
            const checked = checkApiEvents(result.stdout, check);
            evidence.checks.push({ id: check.id, ...checked, seconds: (performance.now() - started) / 1000 });
            console.log(`API ${check.id}: ${checked.tests.length} named tests passed (${evidence.checks.at(-1).seconds.toFixed(2)}s)`);
        }
        assert.equal(apiSourceFingerprint(), before, 'API verification sources changed during the run');
        evidence.status = 'passed';
        return evidence;
    } catch (error) {
        evidence.status = 'failed';
        evidence.error = error.message;
        throw error;
    } finally {
        writeFileSync(join(directory, 'api.json'), JSON.stringify(evidence, null, 2));
    }
}
