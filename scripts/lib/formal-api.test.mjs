import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync, readdirSync } from 'node:fs';
import { apiChecks, checkApiEvents, selectApiChecks, serviceProfileEvidence } from './formal-api.mjs';

const check = { tests: ['TestReadBack'] };
const line = event => JSON.stringify(event);
const valid = [
    { Action: 'run', Test: 'TestReadBack' },
    { Action: 'pass', Test: 'TestReadBack' },
    { Action: 'pass' },
].map(line).join('\n');

test('API checks select real named public handler tests', () => {
    assert.equal(selectApiChecks().length, apiChecks.filter(check => !check.integration).length);
    assert.equal(selectApiChecks('restore').length, 1);
    assert.throws(() => selectApiChecks('missing'), /Unknown API feature/);
    for (const check of apiChecks) {
        const directory = new URL(`../../${check.package.replace(/^\.\//, '')}/`, import.meta.url);
        const source = readdirSync(directory).filter(file => file.endsWith('_test.go'))
            .map(file => readFileSync(new URL(file, directory), 'utf8')).join('\n');
        for (const name of check.tests) assert.match(source, new RegExp(`func ${name}\\(t \\*testing\\.T\\)`));
    }
    const reconcile = selectApiChecks('release-dangle-reconcile')[0];
    assert.deepEqual(reconcile.tests, ['TestReconcileDanglesActiveReleaseAgainstArangoClickHouse']);
    assert.deepEqual(reconcile.requiredEnvironment, ['LOOM_TEST_ARANGO_URL', 'LOOM_CLICKHOUSE_URL']);
});
test('API evidence requires actual named execution and successful completion', () => {
    assert.deepEqual(checkApiEvents(valid, check).tests, ['TestReadBack']);
    assert.throws(() => checkApiEvents(valid, { tests: [] }), /no named tests/);
    assert.throws(() => checkApiEvents(valid, { tests: ['TestReadBack', 'TestReadBack'] }), /Duplicate/);
    assert.throws(() => checkApiEvents(line({ Action: 'pass' }), check), /did not run/);
    assert.throws(() => checkApiEvents(valid.replace('"Action":"run"', '"Action":"output"'), check), /did not run/);
    assert.throws(() => checkApiEvents(valid.replace('"Action":"pass","Test"', '"Action":"skip","Test"'), check), /did not pass/);
    assert.throws(() => checkApiEvents(valid + '\n' + line({ Action: 'fail', Test: 'TestReadBack/subcase' }), check), /subtest failed/);
    assert.throws(() => checkApiEvents(valid + '\n' + line({ Action: 'skip', Test: 'TestReadBack/subcase' }), check), /subtest skipped/);
    assert.throws(() => checkApiEvents(valid.split('\n').slice(0, 2).join('\n'), check), /package did not pass/);
    assert.throws(() => checkApiEvents('not json', check));
});

test('service evidence separates failed selection from unrequested and passed profiles', () => {
    const status = (options, id) => serviceProfileEvidence(options).find(row => row.id === id).status;
    assert.equal(status({ phase: 'all' }, 'append-rows'), 'not-run-in-this-command');
    assert.equal(status({ phase: 'api', feature: 'append-rows' }, 'append-rows'), 'failed-or-incomplete');
    assert.equal(status({ phase: 'api', feature: 'append-rows', checks: [{ id: 'append-rows' }] }, 'append-rows'), 'passed');
    assert.equal(status({ phase: 'api', feature: 'route-rows', checks: [{ id: 'append-rows' }] }, 'append-rows'), 'not-run-in-this-command');
    assert.equal(status({ phase: 'api', feature: 'route-rows', checks: [{ id: 'append-rows' }] }, 'route-rows'), 'failed-or-incomplete');
});
