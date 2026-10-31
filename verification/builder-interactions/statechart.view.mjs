export const title = 'Builder ownership verification';
const owner = (value) => value === 'none' ? 'no owner' : value;
export const state = (v) => {
    if ('acceptanceInput' in v) {
        const i = v.acceptanceInput;
        return `Reconcile ${v.phase}; outcome ${v.outcome}; current ${i.currentOwner}/${i.currentFingerprint}/g${i.currentGeneration}; submitted A/F0/g${i.requestGeneration}; snapshots ${i.requestSnapshot}/${i.responseSnapshot}; requested output ${i.requestOutput === 3 ? 'any' : i.requestOutput}; returned {${Object.values(i.responseOutputs).join(',')}}; diagnostics ${i.diagnostic}`;
    }
    if ('pendingApplies' in v)
        return `Workspace ${v.currentContext}; proposal ${owner(v.currentOwner)}; applying ${owner(v.applyingOwner)}${v.lastAction === 'ApplyReturns' ? `; ${v.responseOwner} returned from ${v.ownerContext[v.responseOwner]}, settled ${owner(v.settledOwner)}; before Apply ${owner(v.priorApplyingOwner)}, proposal ${owner(v.priorCurrentOwner)}` : ''}`;
    if ('proposalReady' in v)
        return `Workspace ${v.currentIdentity}, generation ${v.epoch}; proposal ${owner(v.proposalOwner)} generation ${v.proposalEpoch} ${v.proposalReady ? 'ready' : 'pending'}; preview ${owner(v.previewOwner)} generation ${v.previewEpoch} ${v.previewReady ? 'ready' : 'pending'}; Apply ${owner(v.applyOwner)} ${v.applyInFlight ? `running (proposal reviewed=${v.applyProposalWasReady}, preview reviewed=${v.applyPreviewWasReady})` : 'idle'}${v.lastAction === 'ApplySettles' ? `; settled ${v.lastSettlementOwner} for ${v.lastApplyOwner}` : ''}; discovery ${owner(v.discoveryOwner)} generation ${v.discoveryEpoch}/${v.discoveryGeneration} ${v.discoveryReady ? 'ready' : 'pending'}, adopted ${owner(v.adoptedDiscoveryOwner)}`;
    if (!v.observation)
        return `${({ idle: 'No request', requesting: 'Reconciling', stale: 'Old result ignored', retryable: 'Retryable failure', succeeded: 'Current receipt accepted', repair: 'Repair required' })[v.phase]}; workspace ${v.currentIdentity}/${v.generation}/${v.snapshotVersion}/${v.outputVersion}; request ${v.requestOwner}/${v.requestGeneration}/${v.requestSnapshot}/${v.requestOutput}; receipt ${v.receiptOwner}/${v.receiptGeneration}/${v.receiptSnapshot}/${v.receiptOutput}; retries ${v.retryCount}, refreshes ${v.staleRefreshCount}`;
    const o = v.observation, previous = v.previousObservation || o;
    const tags = [];
    const reviewed = Boolean(o.proposalId) && o.proposalOutput === o.currentOutput &&
        o.proposalSnapshot === o.currentSnapshot && o.proposalVersion === o.currentVersion &&
        o.proposalDigest === o.currentDigest && o.previewReceipt === o.proposalId &&
        o.previewOutput === o.currentOutput && o.rowsPresent;
    const visible = (record) => JSON.stringify([record.status, record.proposalId, record.canApply, record.previewReceipt, record.previewOutput]);
    if (o.kind === 'lifecycle') {
        if (['ready', 'applying'].includes(o.status) && !reviewed)
            tags.push('invalid reviewed preview');
        if (o.canApply && (o.status !== 'ready' || !reviewed))
            tags.push('Apply wrongly enabled');
        if (o.event === 'begin-apply' && o.status === 'applying' &&
            (previous.status !== 'ready' || !previous.canApply || previous.proposalId !== o.proposalId))
            tags.push('Apply began without reviewed readiness');
        if (o.staleEvent && visible(o) !== visible(previous))
            tags.push('old response changed current state');
        if (o.event === 'finish-apply' && o.actorAttempt && o.actorAttempt !== previous.activeApplyAttempt && visible(o) !== visible(previous))
            tags.push('old Apply changed current state');
        if (o.completionObserved && o.completionAccepted && (!o.actorContext || o.actorContext !== o.currentContext))
            tags.push('completion claimed a different context');
        return `${o.status}${o.proposalId ? ` (${o.proposalId})` : ''}; Apply ${o.canApply ? 'enabled' : 'disabled'}${o.event === 'finish-apply' ? o.completionObserved && !o.completionAccepted ? `; ${o.actorReceipt} ignored` : ` after ${o.actorReceipt} completes` : ''}${tags.length ? ` [${tags.join('; ')}]` : ''}`;
    }
    if (o.receiptId && (o.receiptWorkspace !== o.currentWorkspace || o.receiptSnapshot !== o.currentSnapshot ||
        o.receiptOutput !== o.currentOutput || !Object.values(o.receiptOutputs).includes(o.currentOutput)))
        tags.push('receipt for another workspace/snapshot/output');
    return `${o.status}${o.receiptId ? ` (${o.receiptId})` : ''}${tags.length ? ` [${tags.join('; ')}]` : ''}`;
};
const abstractLabels = {
    ChooseInputs: 'receive independent current identity and response inputs', ClassifyResponse: 'classify reconcile response',
    StartProposal: 'start proposal', ReviewProposal: 'review proposal', SetCurrentOwner: 'select current proposal owner', BeginApply: 'begin Apply', Cancel: 'cancel current owner (request remains pending)',
    ApplyReturns: 'Apply returns', ChangeContext: 'change workspace context',
    ChangeIdentity: 'change draft identity', CancelProposal: 'cancel proposal', CancelDiscovery: 'cancel discovery',
    ProposalCompletesCurrent: 'proposal returns', StaleProposalIgnored: 'ignore old proposal', StartPreview: 'request preview',
    PreviewCompletesCurrent: 'preview returns', StalePreviewIgnored: 'ignore old preview', ApplySettles: 'Apply completes',
    StartDiscovery: 'discover categories', DiscoveryCompletesCurrent: 'categories return', StaleDiscoveryIgnored: 'ignore old categories',
    StartReconcile: 'start reconcile', ChangeWorkspace: 'edit workspace', ChangeSnapshot: 'change catalog snapshot', ChangeOutput: 'change selected output',
    ResponseArrives: 'reconcile response arrives', StaleSnapshotFailure: 'snapshot is stale', SuccessCurrent: 'accept matching receipt', ReceiptMismatch: 'reject mismatched receipt', StaleResultIgnored: 'ignore old result',
    RetryableFailure: 'request fails', Retry: 'retry [budget remains]', RefreshAndRetry: 'refresh and retry [budget remains]', GiveUpForRepair: 'stop [budget exhausted]',
};
const observedLabels = {
    'propose-construction': 'request proposal', 'proposal-response': 'proposal returns', 'begin-apply': 'begin Apply',
    cancel: 'cancel current owner', 'identity-change': 'change draft identity', preview: 'request preview',
    'preview-response': 'preview returns', retry: 'retry proposal', 'construction-discovery': 'discover categories', 'pivot-mounted': 'choose Pivot fields',
    workspaceChangeB: 'edit workspace', successB: 'current reconcile returns', lateA: 'old reconcile returns',
};
export const step = (action, _from, to) => {
    const label = to.observation
        ? to.observation.event === 'finish-apply'
            ? `${to.observation.actorReceipt || 'Apply'} ${to.observation.completionObserved && !to.observation.completionAccepted ? 'completion ignored' : 'completes'}`
            : observedLabels[to.observation.event]
        : abstractLabels[action];
    if (!label)
        throw new Error(`No reader label for ${action}/${to.observation?.event ?? ''}`);
    return label;
};
