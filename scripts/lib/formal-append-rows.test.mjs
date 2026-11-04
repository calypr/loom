import test from 'node:test';
import assert from 'node:assert/strict';
import { appendRowsTest, appendRowsFaults, validateAppendRowsPositive, validateAppendRowsFault } from './formal-append-rows.mjs';

const pkg = 'github.com/calypr/loom/internal/server';
const copy = value => structuredClone(value);
const expected = [{ label: 'F', value: 3 }, { label: 'F', value: 3 }, { label: 'M', value: 2 }, { label: 'M', value: 2 }, { label: null, value: 2 }, { label: null, value: 3 }];
const report = actual => ({ phase: 'before', expected: copy(expected), actual });
const serialize = events => events.map(event => JSON.stringify({ Package: pkg, ...event })).join('\n');
function failed(value) { return [{ Test: appendRowsTest, Action: 'run' }, { Test: appendRowsTest, Action: 'output', Output: `APPEND_CURRENT_DRAFT_ROWS_JSON ${JSON.stringify(value)}\n` }, { Test: appendRowsTest, Action: 'fail' }, { Action: 'fail' }]; }
function positive() { return [{ Test: appendRowsTest, Action: 'run' }, { Test: appendRowsTest, Action: 'output', Output: `APPEND_CURRENT_DRAFT_SUMMARY_JSON ${JSON.stringify({ phases: ['before', 'edited', 'filtered', 'restored'], rowCounts: [6, 6, 5, 6], artifactManifests: [0, 0, 0, 0] })}\n` }, { Test: appendRowsTest, Action: 'pass' }, { Action: 'pass' }]; }
const faults = [report([expected[0], expected[2], expected[4], expected[5]]), report(expected.map(row => ({ ...row, label: row.label === null ? 'wrong' : row.label }))), report(expected.map(row => ({ ...row, value: row.value - 2 }))), { phase: 'filtered', expected: expected.filter((row, index) => index !== 3), actual: copy(expected) }];

test('positive actual-row proof requires exact named execution and all four completed phases', () => {
    assert.deepEqual(validateAppendRowsPositive(serialize(positive())).rowCounts, [6, 6, 5, 6]);
    for (const change of [value => value.pop(), value => { value[0].Test = 'TestOther'; }, value => { value[2].Action = 'skip'; }, value => { value[1].Output = value[1].Output.replace('[6,6,5,6]', '[6,6,6]'); }, value => { value[1].Output = value[1].Output.replace('[0,0,0,0]', '[0,1,0,0]'); }, value => value.push(copy(value[1]))]) {
        const events = positive(); change(events); assert.throws(() => validateAppendRowsPositive(serialize(events)));
    }
    assert.throws(() => validateAppendRowsPositive(serialize(positive()), 1));
});
test('each source fault requires its intended typed row difference', () => {
    for (let index = 0; index < faults.length; index++) {
        const evidence = validateAppendRowsFault(serialize(failed(faults[index])), 1, appendRowsFaults[index]);
        assert.equal(evidence.property, appendRowsFaults[index].property);
        const reordered = copy(faults[index]);
        for (const name of ['expected', 'actual']) reordered[name] = reordered[name].map(({ label, value }) => ({ value, label }));
        assert.equal(validateAppendRowsFault(serialize(failed(reordered)), 1, appendRowsFaults[index]).property, evidence.property);
        for (let wrong = 0; wrong < faults.length; wrong++) if (index !== wrong) assert.throws(() => validateAppendRowsFault(serialize(failed(faults[index])), 1, appendRowsFaults[wrong]));
    }
});
test('source fault cannot receive compiler, query, skipped, foreign or ambiguous evidence credit', () => {
    for (const change of [
        value => value.push({ Action: 'output', Output: 'compiler: undefined: missing\n' }),
        value => value.push({ Test: appendRowsTest, Action: 'output', Output: 'panic: execution failed\n' }),
        value => value.push({ Test: appendRowsTest, Action: 'output', Output: 'fixture.go:123: owned database cleanup failed\n' }),
        value => { value[1].Output = 'QueryRows: actual ClickHouse query failed\n'; },
        value => { value[0].Test = 'TestOther'; },
        value => { value[1].Package = 'other/package'; },
        value => value.push({ Test: appendRowsTest, Action: 'skip' }),
        value => value.push({ Test: appendRowsTest, Action: 'pass' }),
        value => value.push(copy(value[1])),
        value => value.push({ Action: 'output', Output: value[1].Output }),
        value => value.pop(),
    ]) {
        const events = failed(faults[0]); change(events); assert.throws(() => validateAppendRowsFault(serialize(events), 1, appendRowsFaults[0]));
    }
    assert.throws(() => validateAppendRowsFault(serialize(failed(faults[0])), 0, appendRowsFaults[0]));
    assert.throws(() => validateAppendRowsFault(serialize(failed(faults[0])), 1, { ...appendRowsFaults[0], property: 'Inv_Unknown' }));
});
test('row oracle substitution and malformed or harmless observations fail closed', () => {
    for (const change of [value => { value.phase = 'foreign'; }, value => { value.expected[0].value = 10; }, value => { value.actual = copy(expected); }, value => { value.actual[0].value = '3'; }, value => { value.actual[0].other = true; }, value => { value.extra = true; }, value => { value.actual = []; }, value => { value.actual[0].label = 1; }]) {
        const value = copy(faults[0]); change(value); assert.throws(() => validateAppendRowsFault(serialize(failed(value)), 1, appendRowsFaults[0]));
    }
});

test('filtered upstream fault requires exact cardinality phase and excluded row', () => {
    const proof = appendRowsFaults.find(value => value.branch === 'filtered-upstream');
    const value = copy(faults[3]);
    for (const change of [data => { data.phase = 'before'; }, data => { data.phase = 'restored'; }, data => { data.actual[0].value = 7; }, data => { data.actual.pop(); }, data => { data.expected = copy(expected); }]) {
        const changed = copy(value); change(changed); assert.throws(() => validateAppendRowsFault(serialize(failed(changed)), 1, proof));
    }
});
