import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { join } from 'node:path';
// Bounded implementation checks, not a proof of the React caller or transport.
export async function checkReconcileImplementation(root) {
    const source = readFileSync(join(root, 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileRecovery.ts'), 'utf8');
    const load = (text) => import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(text)).toString('base64')}`);
    function check(api) {
        let cases = 0;
        for (const generation of [0, 1, 2])
            for (const currentGeneration of [0, 1, 2]) {
                for (const submittedExplorerId of ['A', 'B'])
                    for (const currentExplorerId of ['A', 'B']) {
                        for (const submittedFingerprint of ['F0', 'F1'])
                            for (const currentFingerprint of ['F0', 'F1']) {
                                assert.equal(api.reconcileResultIsCurrent({ generation, currentGeneration,
                                    submittedExplorerId, currentExplorerId, submittedFingerprint, currentFingerprint }), [generation, submittedExplorerId, submittedFingerprint].join('|') ===
                                    [currentGeneration, currentExplorerId, currentFingerprint].join('|'));
                                cases++;
                            }
                    }
            }
        for (const snapshotToken of ['S0', 'S1'])
            for (const requestedSnapshot of ['S0', 'S1']) {
                for (const outputId of [undefined, 'A', 'B'])
                    for (const ids of [[], ['A'], ['B'], ['A', 'B']]) {
                        assert.equal(api.reconcileReceiptMatchesRequest({ snapshotToken,
                            outputs: ids.map((id) => ({ outputId: id })) }, { snapshotToken: requestedSnapshot, outputId }), snapshotToken === requestedSnapshot && (outputId === undefined || ids.includes(outputId)));
                        cases++;
                    }
            }
        for (const attempt of [1, 2, 3, 4])
            for (const staleRefreshes of [0, 1, 2, 3]) {
                for (const code of ['STALE_SNAPSHOT', 'DRAFT_CONFLICT', 'BACKEND_UNAVAILABLE']) {
                    for (const retryable of [false, true]) {
                        const expected = code === 'STALE_SNAPSHOT'
                            ? (staleRefreshes < 2 ? 'refresh-and-retry' : 'repair')
                            : code === 'DRAFT_CONFLICT' ? 'refresh-and-stop'
                                : retryable && attempt < 3 ? 'retry' : 'repair';
                        assert.equal(api.reconcileRecoveryAction({ code, retryable }, {
                            attempt, maxAttempts: 3, staleRefreshes, maxStaleRefreshes: 2,
                        }), expected);
                        cases++;
                    }
                }
            }
        return cases;
    }
    console.log(`Reconcile implementation: PASS (${check(await load(source))} bounded input combinations)`);
    for (const [label, before, after] of [
        ['generation ownership', 'identity.generation === identity.currentGeneration &&', ''],
        ['snapshot receipt match', 'value.snapshotToken === request.snapshotToken &&', ''],
        ['ordinary retry limit', 'options.attempt < options.maxAttempts', 'options.attempt <= options.maxAttempts'],
        ['stale refresh limit', 'options.staleRefreshes < options.maxStaleRefreshes', 'options.staleRefreshes <= options.maxStaleRefreshes'],
    ]) {
        assert.equal(source.split(before).length, 2, `unique source anchor: ${label}`);
        const mutant = await load(source.replace(before, after));
        assert.throws(() => check(mutant), { code: 'ERR_ASSERTION' }, `surviving implementation mutant: ${label}`);
        console.log(`Reconcile implementation control caught: ${label}`);
    }
    check(await load(source));
}
