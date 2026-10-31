import { acceptanceSourceProofs, checkReconcileSourceProof } from './formal-reconcile-replay.mjs';
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdtempSync, rmSync, mkdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawnSync } from 'node:child_process';
import { stripTypeScriptTypes } from 'node:module';
import { root } from './formal-tools.mjs';
import { checkApplyGraph, checkApplyIdentityInputs } from './formal-apply-replay.mjs';
const base = 'ui/packages/loom-ui/src/features/ExplorerBuilder/';
const hook = base + 'constructionWorkspace/useConstructionLifecycle.ts';
const core = base + 'constructionWorkspace/constructionApplyOwnership.ts';
const recovery = base + 'authoring/reconcileRecovery.ts';
const panel = base + 'constructionWorkspace/ConstructionProposalPanel.tsx';
const hookTest = 'src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx';
const workspaceTest = 'src/features/ExplorerBuilder/BuilderWorkspace.reconciliation.unit.test.tsx';
const ownership = (property, edit, after) => ({ model: 'ApplySettlementRace', property, kind: 'ownership', edits: [[core, edit, after]] });
const predicate = (property, check, edit, after = '') => ({ model: 'ReconcileLifecycle', property, kind: 'predicate', check, edits: [[recovery, edit, after]] });
export const sourceProofs = [
    { model: 'ImplementationConformance', property: 'Inv_LateResponseDoesNotReplaceOwner', kind: 'vitest', suite: hookTest, pattern: 'formal property Inv_LateResponseDoesNotReplaceOwner', edits: [['ui/packages/loom-ui/src/react.tsx', 'if (controller !== requestController || epoch !== requestEpoch) return { data };', '']] },
    { model: 'BuilderLifecycle', property: 'Inv_ReadyProposalIsCurrent', kind: 'vitest', suite: hookTest, pattern: 'resolve A with snapshotToken mismatch', edits: [[hook, 'if (!matchesBase) throw new Error', 'if (false) throw new Error']] },
    { model: 'BuilderLifecycle', property: 'Inv_ReadyPreviewMatchesProposal', kind: 'vitest', suite: hookTest, pattern: 'fallback preview schedule:.*wrong receipt', edits: [[hook, 'preview.receiptId === proposalId &&', '']] },
    { model: 'BuilderLifecycle', property: 'Inv_ApplyStartedFromReviewedCurrentPreview', kind: 'vitest', suite: hookTest, pattern: 'formal property Inv_ApplyStartedFromReviewedCurrentPreview', edits: [[hook, 'preview.rows !== null,', 'true,'], [panel, 'state.preview.rows !== null;', 'true;']] },
    { ...ownership('Inv_ApplySettlementTargetsInitiatingOwner', 'if (slot.current !== attempt) return false;', 'const identity = slot.current?.identity; if (!identity) return false;'), model: 'BuilderLifecycle' },
    { model: 'BuilderLifecycle', property: 'Inv_DiscoveryAdoptedOnlyForCurrentIdentity', kind: 'vitest', suite: hookTest, pattern: 'formal property Inv_DiscoveryAdoptedOnlyForCurrentIdentity', edits: [[hook,
                'discoveryRequest.then(\n      (response: ConstructionCategoryDiscoveryResponse) => {\n        if (controller.signal.aborted || pivotDiscoveryGeneration.current !== generation) return;',
                'discoveryRequest.then(\n      (response: ConstructionCategoryDiscoveryResponse) => {']] },
    ownership('Inv_ApplyReturnSettlesItsOwnOwner', 'if (slot.current !== attempt) return false;', 'const identity = slot.current?.identity; if (!identity) return false;'),
    ownership('Inv_OldReturnPreservesCurrentApply', 'if (slot.current !== attempt) return false;', ''),
    ownership('Inv_CompletionOwnsCurrentContext', 'live.requestKey !== identity.requestKey ||', ''),
    ownership('Inv_CompletionOwnsCurrentContext', 'live.generation !== identity.generation', 'false'),
    predicate('Inv_AcceptedReceiptIsCurrent', 'identity', 'identity.generation === identity.currentGeneration &&'),
    predicate('Inv_AcceptedReceiptMatchesRequest', 'snapshot', 'value.snapshotToken === request.snapshotToken &&'),
    predicate('Inv_ReceiptSelectsRequestedOutput', 'output', 'value.outputs.some((output) => output.outputId === request.outputId)', 'true'),
    { model: 'ReconcileLifecycle', property: 'Inv_NoNewAcceptanceOnFailure', kind: 'vitest', suite: workspaceTest,
        pattern: 'formal mounted reconcile rejects mismatched diagnostics receipt', edits: [[base + 'authoring/reconcileAcceptance.ts', "(diagnostic) => diagnostic.severity === 'error'", '(diagnostic) => false']] },
    predicate('Inv_RetryBounds', 'retry', 'options.attempt < options.maxAttempts', 'options.attempt <= options.maxAttempts'),
    predicate('Inv_RetryBounds', 'refresh', 'options.staleRefreshes < options.maxStaleRefreshes', 'options.staleRefreshes <= options.maxStaleRefreshes'),
    ...acceptanceSourceProofs,
    { model: 'Behavior', property: 'Inv_ArtifactResponseOwnsRequestedIdentity', kind: 'vitest', suite: 'src/api.unit.test.ts', pattern: 'prepared dataset artifact identity', edits: [["ui/packages/loom-ui/src/api.ts", "throw new Error('Prepared dataset artifact does not match the requested project, Explorer, revision, and output.');", 'void 0;']] },
    { model: 'Behavior', property: 'Inv_FileDownloadPreservesConfiguredURL', kind: 'vitest', suite: 'src/features/ExplorerViewer/components.unit.test.ts', pattern: 'Viewer file action URLs', edits: [["ui/packages/loom-ui/src/features/ExplorerViewer/components.tsx", "const pathname = (queryAt < 0 ? target : target.slice(0, queryAt)).replace(/\\/+$/, '');", "const pathname = target.replace(/\\/+$/, '');"]] },

];
const predicateProperties = {
    identity: 'Inv_AcceptedReceiptIsCurrent', snapshot: 'Inv_AcceptedReceiptMatchesRequest',
    output: 'Inv_ReceiptSelectsRequestedOutput', retry: 'Inv_RetryBounds', refresh: 'Inv_RetryBounds',
};
/** Evaluate the production predicate and a separate finite-domain oracle. */
function predicateInput(api, kind, property, inputs) {
    assert.ok(Object.hasOwn(predicateProperties, kind), 'Unknown predicate witness kind');
    assert.equal(predicateProperties[kind], property, 'Unknown predicate witness property/kind');
    let actual, expected;
    if (kind === 'identity') {
        const { generation, currentGeneration, id, currentId, fp, currentFp } = inputs;
        assert.ok([0, 1, 2].includes(generation) && [0, 1, 2].includes(currentGeneration), 'Invalid predicate generation');
        assert.ok(['A', 'B'].includes(id) && ['A', 'B'].includes(currentId), 'Invalid predicate identity');
        assert.ok(['F0', 'F1'].includes(fp) && ['F0', 'F1'].includes(currentFp), 'Invalid predicate fingerprint');
        actual = api.reconcileResultIsCurrent({ generation, currentGeneration, submittedExplorerId: id, currentExplorerId: currentId, submittedFingerprint: fp, currentFingerprint: currentFp });
        expected = generation === currentGeneration && id === currentId && fp === currentFp;
    }
    else if (kind === 'snapshot' || kind === 'output') {
        const { snapshotToken, requested, ids } = inputs;
        const outputId = inputs.outputId ?? undefined;
        assert.ok(['S0', 'S1'].includes(snapshotToken) && ['S0', 'S1'].includes(requested), 'Invalid predicate snapshot');
        assert.ok([undefined, 'A', 'B'].includes(outputId), 'Invalid predicate output');
        assert.ok(['[]', '["A"]', '["B"]', '["A","B"]'].includes(JSON.stringify(ids)), 'Invalid predicate outputs');
        actual = api.reconcileReceiptMatchesRequest({ snapshotToken, outputs: ids.map(outputId => ({ outputId })) }, { snapshotToken: requested, outputId });
        expected = snapshotToken === requested && (outputId === undefined || ids.includes(outputId));
    }
    else {
        const { attempt, staleRefreshes } = inputs;
        assert.ok([1, 2, 3, 4].includes(attempt) && [0, 1, 2, 3].includes(staleRefreshes), 'Invalid predicate retry inputs');
        const stale = kind === 'refresh';
        actual = api.reconcileRecoveryAction({ code: stale ? 'STALE_SNAPSHOT' : 'UNAVAILABLE', retryable: true }, { attempt, maxAttempts: 3, staleRefreshes, maxStaleRefreshes: 2 });
        expected = stale ? staleRefreshes < 2 ? 'refresh-and-retry' : 'repair' : attempt < 3 ? 'retry' : 'repair';
    }
    if (actual !== expected) {
        const error = new Error(`${property}: ${JSON.stringify(inputs)} expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
        error.code = 'FORMAL_PROPERTY';
        error.property = property;
        error.witness = { schemaVersion: 1, kind: 'predicate-input', model: 'ReconcileLifecycle', check: kind, property, inputs, actual, expected };
        throw error;
    }
}
function predicateCheck(api, kind, property) {
    let checks = 0;
    const verify = inputs => { predicateInput(api, kind, property, inputs); checks++; };
    if (kind === 'identity') {
        for (const generation of [0, 1, 2])
            for (const currentGeneration of [0, 1, 2])
                for (const id of ['A', 'B'])
                    for (const currentId of ['A', 'B'])
                        for (const fp of ['F0', 'F1'])
                            for (const currentFp of ['F0', 'F1'])
                                verify({ generation, currentGeneration, id, currentId, fp, currentFp });
    }
    else if (kind === 'snapshot' || kind === 'output') {
        for (const snapshotToken of ['S0', 'S1'])
            for (const requested of ['S0', 'S1'])
                for (const outputId of [null, 'A', 'B'])
                    for (const ids of [[], ['A'], ['B'], ['A', 'B']])
                        verify({ snapshotToken, requested, outputId, ids });
    }
    else {
        for (const attempt of [1, 2, 3, 4])
            for (const staleRefreshes of [0, 1, 2, 3])
                verify({ attempt, staleRefreshes });
    }
    assert.ok(checks > 0);
    return checks;
}
export async function replayPredicateWitness(witness, source = readFileSync(join(root, recovery), 'utf8')) {
    assert.equal(witness.schemaVersion, 1, 'Unknown predicate witness schema');
    assert.equal(witness.model, 'ReconcileLifecycle', 'Unsupported predicate witness model');
    assert.equal(witness.kind, 'predicate-input', 'Unknown predicate witness kind');
    assert.ok(witness.inputs, 'Missing predicate witness inputs');
    try {
        predicateInput(await load(source), witness.check, witness.property, witness.inputs);
        return { status: 'property-held', property: witness.property, inputs: witness.inputs };
    }
    catch (error) {
        if (error.code !== 'FORMAL_PROPERTY' || error.property !== witness.property)
            throw error;
        return { status: 'reproduced', ...error.witness };
    }
}
const load = (text) => import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(text)).toString('base64')}`);
function runVitest(proof, scratch, overrides) {
    const output = join(scratch, 'vitest.json');
    const env = { ...process.env, LOOM_BUILDER_TRACE_OUTPUT: '', LOOM_MUTATION_TARGET: '', LOOM_MUTATION_SOURCE: '', LOOM_MUTATION_OVERRIDES: '', LOOM_VITEST_CACHE_DIR: join(scratch, 'cache') };
    if (overrides) {
        const file = join(scratch, 'overrides.json');
        writeFileSync(file, JSON.stringify(overrides));
        env.LOOM_MUTATION_OVERRIDES = file;
    }
    const result = spawnSync(process.execPath, [join(root, 'ui/node_modules/vitest/vitest.mjs'), 'run', proof.suite, '--testNamePattern', proof.pattern, '--reporter=json', `--outputFile=${output}`], { cwd: join(root, 'ui/packages/loom-ui'), env, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
    assert.ifError(result.error);
    assert.ok(existsSync(output), `No Vitest result: ${result.stderr}`);
    const report = JSON.parse(readFileSync(output, 'utf8'));
    const selected = report.testResults.flatMap((file) => file.assertionResults).filter((test) => test.status !== 'pending' && test.status !== 'skipped');
    assert.ok(selected.length > 0, `Proof selected no tests: ${proof.property}`);
    return { result, report, selected };
}
export async function checkSourceProof(proof, graph, outputDirectory) {
    if (proof.kind === 'reconcile-acceptance') return checkReconcileSourceProof(proof, graph, outputDirectory);
    if (outputDirectory)
        mkdirSync(outputDirectory, { recursive: true });
    const scratch = mkdtempSync(join(tmpdir(), 'loom-source-proof-'));
    try {
        const originals = new Map(proof.edits.map(([file]) => [file, readFileSync(join(root, file), 'utf8')]));
        const changed = new Map(originals);
        for (const [file, before, after] of proof.edits) {
            const text = changed.get(file);
            assert.equal(text.split(before).length, 2, `Nonunique mutation anchor for ${proof.property}: ${before}`);
            changed.set(file, text.replace(before, after));
        }
        const evidence = { property: proof.property, model: proof.model, kind: proof.kind, sourceFiles: [...originals.keys()], positive: false, negative: false };
        if (proof.kind === 'ownership') {
            await checkApplyGraph(graph, { source: originals.get(core), property: proof.property });
            if (proof.property === 'Inv_CompletionOwnsCurrentContext')
                evidence.inputs = await checkApplyIdentityInputs(originals.get(core));
            evidence.positive = true;
            await assert.rejects(async () => {
                await checkApplyGraph(graph, { source: changed.get(core), property: proof.property, witnessFile: outputDirectory && join(outputDirectory, 'counterexample.json') });
                if (proof.property === 'Inv_CompletionOwnsCurrentContext')
                    await checkApplyIdentityInputs(changed.get(core));
            }, error => {
                const matched = error.code === 'FORMAL_PROPERTY' && error.property === proof.property;
                if (matched && error.witness && outputDirectory)
                    writeFileSync(join(outputDirectory, 'counterexample.json'), JSON.stringify(error.witness, null, 2));
                return matched;
            });
            evidence.negative = true;
            await checkApplyGraph(graph, { source: originals.get(core), property: proof.property });
        }
        else if (proof.kind === 'predicate') {
            evidence.inputs = predicateCheck(await load(originals.get(recovery)), proof.check, proof.property);
            evidence.positive = true;
            await assert.rejects(async () => predicateCheck(await load(changed.get(recovery)), proof.check, proof.property), error => {
                const matched = error.code === 'FORMAL_PROPERTY' && error.property === proof.property;
                if (matched && outputDirectory)
                    writeFileSync(join(outputDirectory, 'counterexample.json'), JSON.stringify(error.witness, null, 2));
                return matched;
            });
            evidence.negative = true;
            predicateCheck(await load(originals.get(recovery)), proof.check, proof.property);
        }
        else {
            const positive = runVitest(proof, scratch);
            assert.equal(positive.result.status, 0, JSON.stringify(positive.selected));
            assert.ok(positive.selected.every((test) => test.status === 'passed'));
            evidence.positive = true;
            const overrides = {};
            let n = 0;
            for (const [file, text] of changed) {
                const target = join(scratch, `mutant-${n++}.tsx`);
                writeFileSync(target, text);
                overrides[join(root, file)] = target;
            }
            const negative = runVitest(proof, scratch, overrides);
            assert.equal(negative.result.status, 1, `Mutant did not fail for ${proof.property}`);
            const failed = negative.selected.filter((test) => test.status === 'failed' && test.fullName.match(new RegExp(proof.pattern)) && test.failureMessages.some((message) => message.includes('AssertionError')));
            assert.ok(failed.length > 0, `No matching behavior assertion failed for ${proof.property}: ${JSON.stringify(negative.selected)}`);
            evidence.negative = true;
            evidence.failedTests = failed.map((test) => test.fullName);
            if (outputDirectory)
                writeFileSync(join(outputDirectory, 'mutant-vitest.json'), JSON.stringify(negative.report));
            const restored = runVitest(proof, scratch);
            assert.equal(restored.result.status, 0, 'Pristine source failed after mutant');
        }
        if (outputDirectory && ['ownership', 'predicate'].includes(proof.kind))
            assert.ok(existsSync(join(outputDirectory, 'counterexample.json')), 'Generated source proof lost its counterexample');
        for (const [file, original] of originals)
            assert.equal(readFileSync(join(root, file), 'utf8'), original, `Live source changed: ${file}`);
        if (outputDirectory)
            writeFileSync(join(outputDirectory, 'proof.json'), JSON.stringify(evidence, null, 2));
        console.log(`${proof.property}: real-code positive / broken variant / restored PASS`);
        return evidence;
    }
    finally {
        rmSync(scratch, { recursive: true, force: true });
    }
}
