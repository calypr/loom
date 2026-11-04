import assert from 'node:assert/strict';

/** Read the repository's explicit one-rule-per-line TLC configuration format.
 * Unsupported rule syntax blocks instead of silently omitting a declaration.
 */
export function configuredModelRules(config) {
    assert.equal(typeof config, 'string', 'Missing model configuration');
    const rules = [];
    for (const line of config.split(/\r?\n/)) {
        const declaration = line.replace(/\\\*.*/, '').trim();
        if (!/^(?:INVARIANTS?|PROPERT(?:Y|IES))\b/.test(declaration)) continue;
        const match = /^(?:INVARIANT|PROPERTY)\s+([A-Za-z_]\w*)$/.exec(declaration);
        assert.ok(match, `Unsupported model rule declaration: ${declaration}`);
        rules.push(match[1]);
    }
    assert.ok(rules.length > 0, 'Configuration declares no model rules');
    assert.equal(new Set(rules).size, rules.length, 'Duplicate configured model rule');
    return rules;
}

/** Validate the pinned statechart worker's structured verdict, never stdout text.
 * A named negative control may fail in Init before TLC records an initial state.
 */
export function validateModelVerdict(checked, processExit, expectedInvariant, expectedRules) {
    assert.ok(checked && typeof checked === 'object' && !Array.isArray(checked), 'Missing structured statechart verdict');
    assert.ok(expectedInvariant === undefined || typeof expectedInvariant === 'string' && expectedInvariant.trim().length > 0, 'Invalid expected invariant');
    assert.ok(Array.isArray(checked.rules) && checked.rules.length > 0, 'Statechart verdict contains no checked rules');
    assert.ok(Array.isArray(expectedRules) && expectedRules.length > 0, 'Missing configured rule inventory');
    assert.ok(expectedRules.every(name => typeof name === 'string' && /^[A-Za-z_]\w*$/.test(name)), 'Malformed configured rule inventory');
    assert.equal(new Set(expectedRules).size, expectedRules.length, 'Duplicate configured rule inventory');
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
    assert.deepEqual([...names].sort(), [...expectedRules].sort(), 'Statechart verdict does not match configured model rules');
    assert.ok(Array.isArray(checked.counterexamples), 'Missing decoded counterexample inventory');
    if (expectedInvariant === undefined) {
        assert.deepEqual(checked.counterexamples, [], 'Successful verdict contains counterexamples');
    } else {
        assert.equal(checked.counterexamples.length, 1, 'Negative control must retain one decoded counterexample');
        const counterexample = checked.counterexamples[0];
        assert.ok(counterexample && typeof counterexample === 'object' && !Array.isArray(counterexample), 'Malformed decoded counterexample');
        assert.equal(counterexample.property, expectedInvariant, 'Counterexample does not witness the expected invariant');
        assert.ok(Array.isArray(counterexample.states) && counterexample.states.length > 0, 'Counterexample contains no decoded states');
        for (const state of counterexample.states) {
            // TLC can use an empty action label for a violation in the initial state.
            assert.ok(state && typeof state === 'object' && !Array.isArray(state) && typeof state.action === 'string', 'Malformed decoded counterexample state');
            assert.ok(state.vars && typeof state.vars === 'object' && !Array.isArray(state.vars) && Object.keys(state.vars).length > 0, 'Missing decoded counterexample variables');
        }
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
