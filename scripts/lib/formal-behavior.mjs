import assert from 'node:assert/strict';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { root } from './formal-tools.mjs';
// A partition of existing suites, not a claim that every test is a formal proof.
export const features = [
    { id: 'ownership', model: ['BuilderLifecycle', 'ApplySettlementRace'], pattern: /^src\/features\/ExplorerBuilder\/constructionWorkspace\/useConstructionLifecycle\./ },
    { id: 'drafts', model: ['ReconcileLifecycle', 'ReconcileAcceptance'], pattern: /^src\/features\/ExplorerBuilder\/(authoring\/|BuilderWorkspace\.)/ },
    { id: 'construction', model: [], pattern: /^(src\/features\/ExplorerBuilder\/(constructionOperations\/|constructionWorkspace\/(?!useConstructionLifecycle\.))|testSupport\/)/ },
    { id: 'tables', model: [], pattern: /^src\/features\/(ExplorerViewer\/|ExplorerBuilder\/(?!authoring\/|BuilderWorkspace\.|constructionOperations\/|constructionWorkspace\/))/ },
    { id: 'contracts', model: [], pattern: /^src\/(?!features\/)/ },
];
const directory = join(root, 'ui/packages/loom-ui');
export function behaviorInventory() {
    const suites = [];
    const walk = relative => {
        for (const entry of readdirSync(join(directory, relative), { withFileTypes: true })) {
            const path = `${relative}/${entry.name}`;
            if (entry.isDirectory())
                walk(path);
            else if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(path)) {
                const owners = features.filter(feature => feature.pattern.test(path));
                assert.equal(owners.length, 1, `Suite must have exactly one feature: ${path}`);
                suites.push({ file: path, feature: owners[0].id });
            }
        }
    };
    for (const location of ['src', 'testSupport'])
        walk(location);
    assert.ok(suites.length, 'No behavioral suites found');
    return suites.sort((a, b) => a.file.localeCompare(b.file));
}
export function behaviorDoctor() {
    assert.ok(existsSync(join(root, 'ui/node_modules/vitest/vitest.mjs')), 'Install UI dependencies with npm ci from ui');
    return behaviorInventory();
}
export function runBehavior({ feature = 'all', shard, pattern, output }) {
    assert.ok(feature === 'all' || features.some(row => row.id === feature), `Unknown feature: ${feature}`);
    let suites = behaviorDoctor().filter(row => feature === 'all' || row.feature === feature);
    if (shard)
        suites = suites.filter((_, index) => index % shard[1] === shard[0] - 1);
    assert.ok(suites.length, 'Empty behavior selection');
    mkdirSync(output, { recursive: true });
    const reportFile = join(output, 'vitest.json');
    const args = [join(root, 'ui/node_modules/vitest/vitest.mjs'), 'run', ...suites.map(row => row.file), '--maxWorkers=2', '--reporter=json', `--outputFile=${reportFile}`];
    if (pattern)
        args.push('--testNamePattern', pattern);
    const env = { ...process.env, LOOM_MUTATION_TARGET: '', LOOM_MUTATION_SOURCE: '', LOOM_MUTATION_OVERRIDES: '', LOOM_BUILDER_TRACE_OUTPUT: '', LOOM_VITEST_CACHE_DIR: join(output, 'cache') };
    // These optional capture/replay modes belong to retired external generators.
    // Ambient settings must not turn a normal behavioral check into a fixture replay.
    for (const key of Object.keys(env))
        if (key.startsWith('BUILDER_'))
            delete env[key];
    const started = performance.now();
    const result = spawnSync(process.execPath, args, { cwd: directory, env, encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024, detached: true });
    if (result.error && result.pid) {
        try {
            process.kill(-result.pid, 'SIGKILL');
        }
        catch (error) {
            if (error.code !== 'ESRCH')
                throw error;
        }
    }
    writeFileSync(join(output, 'process.json'), JSON.stringify({ exit: result.status, error: result.error?.message, stdout: result.stdout, stderr: result.stderr }, null, 2));
    assert.ifError(result.error);
    assert.ok(existsSync(reportFile), 'Vitest did not produce evidence');
    const report = JSON.parse(readFileSync(reportFile, 'utf8'));
    const selected = new Map(suites.map(row => [resolve(directory, row.file), row]));
    const results = report.testResults.flatMap(suite => {
        const owner = selected.get(resolve(suite.name));
        assert.ok(owner, `Unexpected suite: ${suite.name}`);
        return suite.assertionResults.map(test => ({ file: owner.file, feature: owner.feature, name: test.fullName, status: test.status }));
    });
    assert.equal(new Set(report.testResults.map(suite => resolve(suite.name))).size, suites.length, 'A selected suite did not execute');
    const passed = results.filter(test => test.status === 'passed');
    const failed = results.filter(test => test.status === 'failed');
    const skipped = results.filter(test => !['passed', 'failed'].includes(test.status));
    const evidence = { feature, suites, passed: passed.length, failed, skipped,
        status: result.status !== 0 || failed.length ? 'failed' : passed.length ? skipped.length ? 'partial' : 'passed' : 'blocked',
        seconds: (performance.now() - started) / 1000, boundary: 'production components and functions; jsdom and mocked external services; no live browser or durable storage proof' };
    writeFileSync(join(output, 'behavior.json'), JSON.stringify(evidence, null, 2));
    assert.equal(result.status, 0, `Behavior tests failed; see ${reportFile}`);
    assert.equal(failed.length, 0, 'Behavior assertions failed');
    assert.ok(passed.length > 0, 'Selection ran no behavior assertions');
    console.log(`Behavior ${feature}: ${passed.length} passed, ${skipped.length} skipped (${evidence.seconds.toFixed(2)}s); coverage ${evidence.status}`);
    return evidence;
}
