export const title = 'Builder ownership verification';
const owner = (value) => value === 'none' ? 'no owner' : value;
export const state = (v) => {
    if ('exclusionPhase' in v) return `Population exclusion ${v.exclusionPhase}; local ${v.localBase}/v${v.localVersion}; server ${v.serverBase}/v${v.serverVersion}; context ${v.contextKind}; attached ${v.hostAttached}; change ${v.changeKind}; route requests ${v.routeCalls}; submitted ${v.submitted}`;
    if ('transactionResult' in v) return `SaveDraft ${v.phase}; staged history ${v.stagedHistory}, staged head ${v.stagedHead}; committed history ${v.history}, committed head ${v.head}; transaction ${v.transactionResult}; returned ${v.result}${v.backendConflict ? '; backend write conflict retained' : ''}`;
    if ('rStatus' in v) {
        const set = value => `{${(Array.isArray(value) ? value : []).join(', ')}}`;
        return `Reconcile ${v.rStatus} for head ${v.rTarget}, captured ${v.rCaptured}; save ${v.sStatus} of head ${v.sTarget}; head ${v.headVersion}; receipts ${set(v.receipts)}; previews executed ${set(v.previewExecuted)}, echo ${v.previewEcho}; client head ${v.clientCurrent}; admitted ${owner(v.admitted)}`;
    }
    if ('removalInput' in v) return `Removal ${v.removalPhase}; receipt ${v.removalInput.receiptMatches}; provenance ${v.removalInput.provenanceMatches}; authorization ${v.removalInput.authorizationMatches}; preview ${v.removalInput.previewCompletes}; competing write ${v.removalInput.concurrentDraftChange}; accepted ${v.accepted}; preview calls ${v.previewCalls}; save attempts ${v.saveAttempts}; writes ${v.successfulSaves}; preserved head ${v.preservedHead}; exact candidate ${v.exactCandidate}; conflict ${v.conflict}`;
    if ('acknowledgementInput' in v) {
        const input = v.acknowledgementInput;
        return `Publication ${v.acknowledgementPhase}; attached ${input.workspaceAttached}; owner ${input.ownerMatches}; Explorer ${input.explorerMatches}; draft ${input.draftDigestMatches}; intent ${input.intentMatches}; snapshot ${input.snapshotMatches}; preview recompiled ${input.previewRecompiled}; acknowledgement ${v.accepted ? 'accepted' : 'ignored'}`;
    }
    if ('populationInput' in v) {
        const input = v.populationInput;
        return `Population ${v.populationPhase}; scope ${input.authorizedScopeMatches}; complete ${input.selectionComplete}; binding ${input.selectionBindingMatches}; member read ${input.memberReadCompletes}; mapping ${input.mappingCompletes}; outcome ${v.outcome}; reads ${v.memberReads}; execution ${v.mappingCalls}; counts {${Object.values(v.counts).join(',')}}`;
    }
    if ('chainInput' in v) {
        const input = v.chainInput;
        return `Construction ${v.chainPhase}; first ${input.firstKind}; second reference ${input.secondInput}; filter column ${input.secondColumn}; ${v.chainPhase === 'checked' ? v.accepted ? 'accepted' : 'rejected' : 'not validated'}`;
    }
    if ('refreshPhase' in v) {
      const current = v => v.workspaceAttached && v.currentOwner === 'A' && v.currentFingerprint === 'F0' && v.currentGeneration === 0;
  if (v.refreshPhase === 'idle') return 'No recovery wait';
  if (v.refreshPhase === 'waiting' && !v.workspaceAttached) return 'Detached Workspace recovery waiting';
  if (v.refreshPhase === 'waiting') return v.cancelled ? 'Recovery pending; owner cancelled' : current(v) ? 'Recovery pending; original workspace current' : 'Recovery pending; original workspace obsolete';
  if (v.synchronized || v.retried) return `${current(v) && !v.cancelled ? 'Current' : 'Obsolete'} recovery ${v.synchronized ? 'synchronized' : 'resumed'}${v.retried ? '; retry submitted' : '; stopped'}`;
  return v.cancelled ? 'Cancelled recovery ignored' : !current(v) ? 'Obsolete recovery ignored' : 'Refresh returned no builder data';
    }
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
    CreationReturns: 'excluded collection revision returns', RoutePageReturns: 'population route page returns',
    DrainReturnsAndSubmit: 'command queue drains and checks population intent before submission',
    ChangeBase: 'another selection replaces the original population', DetachHost: 'Workspace host unmounts',
    UnrelatedSave: 'save an unrelated edit with the same population',
    BeginHydration: 'fresh Builder response updates server identity before React admission',
    FinishHydration: 'React admits the refreshed population',
    Begin: 'begin draft transaction', Query: 'execute draft transaction query', Abort: 'abort draft transaction and discard staged writes', Commit: 'commit transaction or observe commit error', Return: 'classify transaction result and return saved draft',
    DetachWorkspace: 'Workspace host detaches while recovery waits',
    ChooseRemoval: 'choose independently bound removal request and persistence outcome', ApplyRemoval: 'execute checked removal and preserve or commit the exact head',
    ChooseAcknowledgement: 'choose current publication context and independent preview compilation', ReceiveAcknowledgement: 'publication succeeds and acknowledgement is checked',
    ChoosePopulation: 'choose authorization and population completion inputs', ClassifyPopulation: 'check public population report outcome and disclosure',
    ChooseChain: 'choose two-stage construction inputs', ClassifyChain: 'validate predecessor reference and available columns',
    AwaitRecovery: 'wait for catalog or retry delay', ChangeExplorer: 'change selected Explorer', EditWorkspace: 'edit workspace', StartNewCompile: 'start newer compile', CancelOwner: 'cancel automatic preview owner', ResumeRecovery: 'recovery wait completes',
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
    REntry: 'entry CAS accepts reconcile or rejects stale identity', RCapture: 'captured head enters compilation',
    RPersist: 'immutable receipt persisted', RComplete: 'reconcile returns the captured receipt',
    SCommit: 'save commits; dispatch cancels in-flight and admitted previews', SCommitStale: 'stale save rejected; click still cancels previews',
    SAck: 'save ack rebuilds client state without the admitted preview',
    Preview: 'execute a persisted receipt', Admit: 'admit echoed receipt to the preview display',
};
const observedLabels = {
    'propose-construction': 'request proposal', 'proposal-response': 'proposal returns', 'begin-apply': 'begin Apply',
    cancel: 'cancel current owner', 'identity-change': 'change draft identity', preview: 'request preview',
    'preview-response': 'preview returns', retry: 'retry proposal', 'construction-discovery': 'discover categories', 'pivot-mounted': 'choose Pivot fields',
    workspaceChangeB: 'edit workspace', successB: 'current reconcile returns', lateA: 'old reconcile returns',
};
export const step = (action, _from, to) => {
    if (action === 'Query' && 'transactionResult' in _from) {
        const labels = { read: 'read head with expected version and digest', archive: 'insert immutable previous draft revision', update: 'update head with expected version and digest' };
        if (!labels[_from.phase]) throw new Error(`No reader label for draft query phase ${_from.phase}`);
        return labels[_from.phase];
    }
    const label = to.observation
        ? to.observation.event === 'finish-apply'
            ? `${to.observation.actorReceipt || 'Apply'} ${to.observation.completionObserved && !to.observation.completionAccepted ? 'completion ignored' : 'completes'}`
            : observedLabels[to.observation.event]
        : abstractLabels[action] ?? abstractLabels[action.split('/')[0].split('(')[0]];
    if (!label)
        throw new Error(`No reader label for ${action}/${to.observation?.event ?? ''}`);
    return label;
};
