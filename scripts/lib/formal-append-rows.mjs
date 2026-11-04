import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root } from './formal-tools.mjs';
import { apiDoctor, apiSourceFingerprint, checkApiEvents } from './formal-api.mjs';
import { goTestTimeout, verificationTimeoutMs } from './verification-budget.mjs';

export const appendRowsTest = 'TestCurrentDraftAppendRowsAgainstArangoClickHouse';
export const appendRowsFile = 'internal/server/append_workspace_rows_integration_test.go';
export const appendRowsFaults = [
    { branch: 'drop-input', property: 'Inv_AppendRetainsEveryInputRow', file: 'internal/dataframe/compiler/render/clickhouse/workspace_combine.go', before: '\tfor inputIndex, input := range inputs {\n\t\talias := fmt.Sprintf("__loom_input_%d", inputIndex)', after: '\tfor inputIndex, input := range inputs {\n\t\tif inputIndex == 1 { continue }\n\t\talias := fmt.Sprintf("__loom_input_%d", inputIndex)' },
    { branch: 'null-padding', property: 'Inv_AppendPadsMissingNullableColumns', file: 'internal/dataframe/compiler/render/clickhouse/workspace_combine.go', before: 'selects = append(selects, fmt.Sprintf("CAST(NULL, \'%s\') AS `%s`", output.ClickHouseType, output.Name))', after: 'selects = append(selects, fmt.Sprintf("CAST(\'wrong\', \'%s\') AS `%s`", output.ClickHouseType, output.Name))' },
    { branch: 'derive-operand', property: 'Inv_CurrentDraftDeriveUsesAuthoredExpression', file: 'internal/dataframe/compiler/lower/recipe_derived_columns.go', before: 'recipe.DerivedAdd:      "add",', after: 'recipe.DerivedAdd:      "subtract",' },
    { branch: 'filtered-upstream', property: 'Inv_CurrentDraftAppendUsesFilteredUpstreamRows', file: 'internal/dataframe/compiler/render/aql/stage_sequence.go', before: '\t\t\tif stage.Filter != nil {', after: '\t\t\tif stage.Filter != nil && stage.Kind != ir.PhysicalStageFilterOp {' },
];
const phases = ['before', 'edited', 'filtered', 'restored'];
const row = (label, value) => ({ label, value });
const beforeRows = [row('F', 3), row('F', 3), row('M', 2), row('M', 2), row(null, 2), row(null, 3)];
const editedRows = [row('F', 4), row('F', 3), row('M', 3), row('M', 2), row(null, 2), row(null, 3)];
const filteredRows = [row('F', 3), row('F', 3), row('M', 2), row(null, 2), row(null, 3)];
const sorted = rows => rows.map(value => JSON.stringify(row(value.label, value.value))).sort();
function typedRows(rows) {
    assert.ok(Array.isArray(rows) && rows.length > 0, 'Missing row observations');
    for (const value of rows) {
        assert.deepEqual(Object.keys(value).sort(), ['label', 'value']);
        assert.ok(value.label === null || typeof value.label === 'string');
        assert.ok(Number.isSafeInteger(value.value), 'Non-integer row observation');
    }
    return rows;
}
function events(stdout) {
    const result = stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
    assert.ok(result.length, 'Missing Go events');
    assert.ok(result.every(event => event.Package === 'github.com/calypr/loom/internal/server'), 'Foreign Go package');
    assert.ok(!result.some(event => event.Action === 'skip'), 'Skipped Go case');
    assert.ok(result.every(event => !event.Test || event.Test === appendRowsTest), 'Foreign Go test');
    return result;
}
function marker(stream, name) {
    const text = stream.filter(event => event.Test === appendRowsTest && event.Action === 'output').map(event => event.Output || '').join('');
    const allText = stream.filter(event => event.Action === 'output').map(event => event.Output || '').join('');
    assert.doesNotMatch(allText, /panic:|fatal error:|test timed out|\[build failed\]|undefined:|syntax error:|no required module/i);
    const matches = [...text.matchAll(new RegExp(`${name}\\s+([^\\n]+)`, 'g'))];
    assert.equal(matches.length, 1, `Missing or duplicate ${name}`);
    assert.equal([...allText.matchAll(new RegExp(`${name}\\s+`, 'g'))].length, 1, `Foreign or duplicate ${name}`);
    return JSON.parse(matches[0][1]);
}
export function validateAppendRowsPositive(stdout, exit = 0) {
    assert.equal(exit, 0, 'Actual row check failed');
    checkApiEvents(stdout, { tests: [appendRowsTest] });
    const stream = events(stdout), summary = marker(stream, 'APPEND_CURRENT_DRAFT_SUMMARY_JSON');
    assert.deepEqual(summary, { phases, rowCounts: [6, 6, 5, 6], artifactManifests: [0, 0, 0, 0] });
    return summary;
}
export function validateAppendRowsFault(stdout, exit, proof) {
    assert.deepEqual(proof, appendRowsFaults.find(value => value.branch === proof.branch), 'Unknown row source fault');
    assert.equal(exit, 1, 'Fault must reach a semantic Go failure');
    const stream = events(stdout);
    assert.ok(stream.some(event => event.Test === appendRowsTest && event.Action === 'run'), 'Named test did not run');
    assert.ok(stream.some(event => event.Test === appendRowsTest && event.Action === 'fail'), 'Named test did not fail');
    assert.ok(stream.some(event => !event.Test && event.Action === 'fail'), 'Package did not fail');
    assert.ok(!stream.some(event => event.Action === 'pass'), 'Mixed pass/fail row evidence');
    const report = marker(stream, 'APPEND_CURRENT_DRAFT_ROWS_JSON');
    const lines = stream.filter(event => event.Test === appendRowsTest && event.Action === 'output').flatMap(event => (event.Output || '').split('\n')).map(line => line.trim()).filter(Boolean);
    assert.ok(lines.every(line => line.startsWith(`=== RUN   ${appendRowsTest}`) || line.startsWith(`--- FAIL: ${appendRowsTest} `) || /^(?:[^\s]+\.go:\d+: )?APPEND_CURRENT_DRAFT_ROWS_JSON /.test(line)), 'Unexpected failure output alongside row counterexample');
    assert.deepEqual(Object.keys(report).sort(), ['actual', 'expected', 'phase']);
    assert.ok(phases.includes(report.phase), 'Unknown row phase');
    const expected = typedRows(report.expected), actual = typedRows(report.actual);
    assert.deepEqual(sorted(expected), sorted(report.phase === 'edited' ? editedRows : report.phase === 'filtered' ? filteredRows : beforeRows), 'Changed literal row oracle');
    assert.notDeepEqual(sorted(actual), sorted(expected), 'No observed row difference');
    if (proof.branch === 'filtered-upstream') {
        assert.equal(report.phase, 'filtered', 'Filter fault must reach the filtered upstream phase');
        assert.deepEqual(sorted(actual), sorted(beforeRows), 'Filter bypass must retain the one excluded M2 row');
    } else if (proof.branch === 'drop-input') {
        assert.equal(report.phase, 'before', 'Input loss fault must reach the initial witness');
        assert.equal(actual.length, 4, 'Not the missing-input row count');
        const available = sorted(expected);
        for (const value of sorted(actual)) { const index = available.indexOf(value); assert.ok(index >= 0, 'Row loss also changed values'); available.splice(index, 1); }
    } else if (proof.branch === 'null-padding') {
        assert.equal(report.phase, 'before', 'Null padding fault must reach the initial witness');
        assert.equal(actual.length, expected.length);
        assert.deepEqual(sorted(actual.filter(value => value.label !== 'wrong')), sorted(expected.filter(value => value.label !== null)), 'Null fault also changed present rows');
        assert.deepEqual(actual.filter(value => value.label === 'wrong').map(value => value.value).sort(), expected.filter(value => value.label === null).map(value => value.value).sort(), 'Null fault changed numeric values');
    } else {
        assert.equal(report.phase, 'before', 'Derive fault must reach the initial witness');
        assert.equal(actual.length, expected.length);
        assert.deepEqual(actual.map(value => value.label).sort(), expected.map(value => value.label).sort(), 'Derive fault changed row identities');
        assert.notDeepEqual(actual.map(value => value.value).sort(), expected.map(value => value.value).sort(), 'Derive values did not change');
    }
    return { property: proof.property, ...report };
}
const sha = file => createHash('sha256').update(readFileSync(file)).digest('hex');
export function runAppendRows(output) {
    for (const name of ['LOOM_TEST_ARANGO_URL', 'LOOM_CLICKHOUSE_URL']) assert.ok(process.env[name], `Set ${name} to an owned integration instance`);
    const directory = resolve(output);
    assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Output must be empty');
    mkdirSync(directory, { recursive: true });
    const tool = apiDoctor(), before = apiSourceFingerprint(), adapterHash = sha(join(root, appendRowsFile)), wrapperHash = sha(new URL(import.meta.url));
    const evidence = { status: 'running', boundary: 'fixed current-workspace Group/Derive/Filter/APPEND edit and restore witness using actual Arango and ClickHouse; controlled recipe authoring, not TLA, HTTP, browser, exhaustive inputs or process restart', sourceBefore: before, adapterHash, wrapperHash, tool, positive: undefined, sourceControls: [], restored: undefined };
    const run = (label, proof, setup = false) => {
        const scratch = mkdtempSync(join(tmpdir(), 'loom-append-rows-')), target = join(directory, label);
        mkdirSync(target);
        try {
            const args = ['test', '-json', '-count=1', `-timeout=${goTestTimeout}`, '-run', setup ? '^$' : `^${appendRowsTest}$`];
            if (proof) {
                const original = readFileSync(join(root, proof.file), 'utf8');
                assert.equal(original.split(proof.before).length, 2, 'Nonunique source fault anchor');
                const changed = join(scratch, 'mutant.go'), overlay = join(scratch, 'overlay.json');
                writeFileSync(changed, original.replace(proof.before, proof.after));
                writeFileSync(overlay, JSON.stringify({ Replace: { [join(root, proof.file)]: changed } }));
                writeFileSync(join(target, 'source.json'), JSON.stringify({ file: proof.file, before: sha(join(root, proof.file)), after: sha(changed), property: proof.property }));
                args.push('-overlay', overlay);
            }
            args.push('./internal/server');
            const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', GOMAXPROCS: '2' };
            if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
                if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
                if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
            }
            const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: setup ? Math.max(60_000, verificationTimeoutMs) : verificationTimeoutMs, maxBuffer: 16 * 1024 * 1024, detached: true });
            if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
            writeFileSync(join(target, 'go.jsonl'), result.stdout || '');
            writeFileSync(join(target, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2));
            assert.ifError(result.error);
            assert.equal(result.stderr || '', '', 'Unexpected Go stderr');
            if (setup) {
                assert.equal(result.status, 0, 'Package compile setup failed');
                const stream = events(result.stdout);
                assert.ok(stream.some(event => !event.Test && event.Action === 'pass'));
                assert.ok(stream.every(event => !event.Test && event.Action !== 'fail'));
                return { status: 'compiled-no-proof', namedCasesExecuted: 0 };
            }
            return proof ? validateAppendRowsFault(result.stdout, result.status, proof) : validateAppendRowsPositive(result.stdout, result.status);
        } finally { rmSync(scratch, { recursive: true, force: true }); }
    };
    try {
        evidence.setup = run('setup', undefined, true);
        evidence.positive = run('positive');
        for (const proof of appendRowsFaults) evidence.sourceControls.push({ branch: proof.branch, ...run(proof.branch, proof) });
        evidence.restored = run('restored');
        evidence.sourceVariantCount = evidence.sourceControls.length;
        evidence.properties = evidence.sourceControls.map(control => control.property);
        evidence.phaseCounts = { positive: evidence.positive.phases.length, sourceFaults: evidence.sourceControls.length, restored: evidence.restored.phases.length };
        assert.equal(apiSourceFingerprint(), before, 'Integration sources changed during replay');
        assert.equal(sha(join(root, appendRowsFile)), adapterHash); assert.equal(sha(new URL(import.meta.url)), wrapperHash);
        evidence.status = 'passed';
        return evidence;
    } catch (error) { evidence.status = 'failed'; evidence.error = error.message; throw error; }
    finally { evidence.sourceAfter = apiSourceFingerprint(); writeFileSync(join(directory, 'append-rows.json'), JSON.stringify(evidence, null, 2)); }
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    assert.equal(process.argv.length, 4); assert.equal(process.argv[2], '--out');
    console.log(JSON.stringify(runAppendRows(process.argv[3])));
}
