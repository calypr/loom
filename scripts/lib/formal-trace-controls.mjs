import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { prepareTraces, sourceFingerprint, tlaValue } from './formal-implementation-conformance.mjs';
import { root } from './formal-tools.mjs';
export const traceRules = Object.keys(JSON.parse(readFileSync(join(root, 'verification/builder-interactions/trace-obligations.json'), 'utf8')));
export function checkTraceRule(property, traceFile, checkModel) {
    const header = JSON.parse(readFileSync(traceFile, 'utf8'));
    const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' });
    assert.equal(head.status, 0);
    const expected = { sourceProfile: 'formal', sourceCommit: head.stdout.trim(), contractSourceCommit: head.stdout.trim(), sourceFingerprint: sourceFingerprint(root) };
    const all = prepareTraces(header, expected);
    let selected;
    for (const trace of all)
        for (let i = 0; i < trace.length; i++) {
            const o = trace[i], previous = trace[i - 1];
            const match = {
                Inv_VisibleReadyIsReviewed: o.status === 'ready',
                Inv_ApplyRequiresReadyPreview: o.kind === 'lifecycle' && o.status === 'idle',
                Inv_BeginApplyWasReviewed: o.event === 'begin-apply' && o.status === 'applying' && Boolean(previous),
                Inv_LateResponseDoesNotReplaceOwner: o.staleEvent && Boolean(previous),
                Inv_OldApplyDoesNotClearReplacement: o.event === 'finish-apply' && Boolean(o.actorAttempt) && previous && o.actorAttempt !== previous.activeApplyAttempt,
                Inv_RenderedReconcileReceiptIsCurrent: o.kind === 'reconcile' && Boolean(o.receiptId),
                Inv_AcceptedCompletionOwnsCurrentContext: o.completionObserved && o.completionAccepted,
            }[property];
            if (!match || selected)
                continue;
            selected = structuredClone(trace);
            const corrupt = selected[i];
            if (property === 'Inv_VisibleReadyIsReviewed')
                corrupt.previewReceipt = 'wrong-receipt';
            else if (property === 'Inv_ApplyRequiresReadyPreview')
                corrupt.canApply = true;
            else if (property === 'Inv_BeginApplyWasReviewed')
                selected[i - 1].canApply = false;
            else if (property === 'Inv_LateResponseDoesNotReplaceOwner' || property === 'Inv_OldApplyDoesNotClearReplacement')
                corrupt.status = previous.status === 'error' ? 'idle' : 'error';
            else if (property === 'Inv_RenderedReconcileReceiptIsCurrent')
                corrupt.receiptSnapshot = 'wrong-snapshot';
            else if (property === 'Inv_AcceptedCompletionOwnsCurrentContext')
                corrupt.actorContext = 'other-context';
        }
    assert.ok(selected, `Trace rule has no triggering real observation: ${property}`);
    const scratch = mkdtempSync(join(tmpdir(), 'loom-trace-rule-'));
    try {
        const source = readFileSync(join(root, 'verification/builder-interactions/ImplementationConformance.tla'), 'utf8');
        writeFileSync(join(scratch, 'ImplementationConformance.tla'), source.replace('\n=============================================================================', `\nObservations == ${tlaValue([selected])}\n=============================================================================`));
        writeFileSync(join(scratch, 'ImplementationConformance.cfg'), `SPECIFICATION Spec\nCHECK_DEADLOCK FALSE\nCONSTANT Traces <- Observations\nINVARIANT ${property}\n`);
        return checkModel('ImplementationConformance', scratch, property);
    }
    finally {
        rmSync(scratch, { recursive: true, force: true });
    }
}
