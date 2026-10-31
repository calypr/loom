import assert from 'node:assert/strict';

/** Validate the pinned statechart worker's structured verdict, never stdout text.
 * A named negative control may fail in Init before TLC records an initial state.
 */
export function validateModelVerdict(checked, processExit, expectedInvariant) {
    assert.ok(checked && typeof checked === 'object' && !Array.isArray(checked), 'Missing structured statechart verdict');
    assert.ok(expectedInvariant === undefined || typeof expectedInvariant === 'string' && expectedInvariant.trim().length > 0, 'Invalid expected invariant');
    assert.ok(Array.isArray(checked.rules) && checked.rules.length > 0, 'Statechart verdict contains no checked rules');
    const names = new Set();
    const broken = [];
    for (const rule of checked.rules) {
        assert.ok(rule && typeof rule.name === 'string' && rule.name.trim().length > 0, 'Malformed statechart rule');
        assert.ok(!names.has(rule.name), `Duplicate statechart rule: ${rule.name}`);
        names.add(rule.name);
        assert.ok(['holds', 'broken', 'not checked'].includes(rule.outcome), `Unknown rule outcome: ${rule.name}`);
        assert.notEqual(rule.outcome, 'not checked', `Rule was not checked: ${rule.name}`);
        if (rule.outcome === 'broken') broken.push(rule.name);
    }
    const states = checked.states;
    assert.ok(states && typeof states === 'object' && !Array.isArray(states), 'Missing state-space result');
    for (const name of ['distinct', 'initial'])
        assert.ok(Number.isSafeInteger(states[name]) && states[name] >= 0, `Invalid state count: ${name}`);
    assert.ok(states.initial <= states.distinct, 'Initial state count exceeds distinct states');
    assert.equal(typeof states.complete, 'boolean', 'Missing state-space completion flag');
    assert.equal(typeof states.cut, 'boolean', 'Missing state-space cut flag');
    assert.equal(states.cut, false, 'State-space exploration was cut');
    if (expectedInvariant !== undefined) {
        assert.equal(processExit, 1, 'Negative control did not exit with a property violation');
        assert.equal(checked.exit, 1, 'Compiled verdict did not report a property violation');
        assert.deepEqual(broken, [expectedInvariant], `Expected only ${expectedInvariant}`);
    } else {
        assert.equal(processExit, 0, 'Model-checking process failed');
        assert.equal(checked.exit, 0, 'Compiled statechart verdict failed');
        assert.equal(states.complete, true, 'State-space exploration is incomplete');
        assert.ok(states.initial > 0 && states.distinct > 0, 'Model explored no initial states');
        assert.deepEqual(broken, [], 'Current code/model violates a property');
    }
    return checked;
}
