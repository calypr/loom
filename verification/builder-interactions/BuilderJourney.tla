---------------- MODULE BuilderJourney ----------------
\* Navigation supplies the ready source-table preview. Proposal preview and
\* applied operation lifecycle stay separate from that rendered table state.
\* Data-shape correctness remains in BuilderOperations.tla and the browser
\* fixture oracle carried by each trace.
\*
\* The Group slice is deliberately finite: it tracks the persisted record,
\* the proposal candidate, and the configuration mounted by the UI. Ordered
\* key sequences are bounded to two tokens; row values and aggregate arithmetic
\* remain the responsibility of the native fixture oracle.
EXTENDS Naturals, Sequences, TLC

CONSTANT CaptureIds

MaxReshapes == 2
MaxMountEpoch == 2

Phases == {"idle", "selected", "reshaping"}
PreviewStates == {"none", "pending", "ready", "error"}
ProposalOwners == {"P1", "P2", "NoAttempt"}
ChoicePreviewStates == {"none", "pending", "ready", "error"}
TablePreviewStates == {"ready", "error"}
JourneyActions == {"Init", "Group", "GroupAgain", "AddColumn",
                   "CancelFilter", "EditGroup", "EditGroupAgain", "RemoveGroup", "Pivot", "Preview",
                   "PreviewComplete", "PreviewError", "LatePreviewCompleteIgnored",
                   "LatePreviewErrorIgnored", "PreviewRequestCancelled", "ChoicePreview", "ChoicePreviewComplete",
                   "ChoicePreviewError", "GroupEditCancel", "GroupRemovalCancel", "Reload"}

GroupShapes == {"EmptyKeys", "ByID", "ByIDGender"}
Aggregates == {"COUNT_ROWS"}
StepIds == {"G1", "G2"}
NoStep == "NoStep"
MountEpochs == 0..MaxMountEpoch
GroupKeyIds == {"ID", "Gender", "Race", "Other"}
GroupKeySequences == {<<>>, <<"ID">>, <<"Gender">>, <<"Race">>, <<"Other">>,
                      <<"ID", "Gender">>, <<"Gender", "ID">>,
                      <<"ID", "Race">>, <<"Race", "ID">>,
                      <<"ID", "Other">>, <<"Other", "ID">>,
                      <<"Gender", "Race">>, <<"Race", "Gender">>,
                      <<"Gender", "Other">>, <<"Other", "Gender">>,
                      <<"Race", "Other">>, <<"Other", "Race">>,
                      <<"ID", "ID">>, <<"Gender", "Gender">>,
                      <<"Race", "Race">>, <<"Other", "Other">>}
CandidateKinds == {"create", "edit", "remove"}

NoGroup == [stepId |-> NoStep, shape |-> "NoShape", keys |-> <<>>,
            aggregate |-> "NoAggregate"]
GroupRecords == {NoGroup} \cup
  [stepId : StepIds, shape : GroupShapes, keys : GroupKeySequences, aggregate : Aggregates]

EmptyGroup == [stepId |-> "G1", shape |-> "EmptyKeys", keys |-> <<>>,
               aggregate |-> "COUNT_ROWS"]
ByIDGroup == [stepId |-> "G1", shape |-> "ByID", keys |-> <<"ID">>,
              aggregate |-> "COUNT_ROWS"]
ByIDGenderGroup == [stepId |-> "G1", shape |-> "ByIDGender",
                    keys |-> <<"ID", "Gender">>, aggregate |-> "COUNT_ROWS"]
GroupRecordOK(groupValue) ==
  IF groupValue = NoGroup
  THEN TRUE
  ELSE /\ groupValue \in [stepId : StepIds, shape : GroupShapes,
                            keys : GroupKeySequences, aggregate : Aggregates]
       /\ CASE groupValue.shape = "EmptyKeys" -> groupValue.keys = <<>>
            [] groupValue.shape = "ByID" -> groupValue.keys = <<"ID">>
            [] groupValue.shape = "ByIDGender" -> groupValue.keys = <<"ID", "Gender">>

NoCandidateIntent == [kind |-> "none", targetStepId |-> NoStep,
                      base |-> NoGroup, proposed |-> NoGroup]
CandidateIntents == {NoCandidateIntent} \cup
  [kind : CandidateKinds, targetStepId : StepIds \cup {NoStep},
   base : GroupRecords, proposed : GroupRecords]

CreateEmptyIntent == [kind |-> "create", targetStepId |-> NoStep,
                      base |-> NoGroup, proposed |-> EmptyGroup]
EditByIDIntent == [kind |-> "edit", targetStepId |-> "G1",
                    base |-> EmptyGroup, proposed |-> ByIDGroup]
EditByIDGenderIntent == [kind |-> "edit", targetStepId |-> "G1",
                          base |-> EmptyGroup, proposed |-> ByIDGenderGroup]
UnchangedEmptyIntent == [kind |-> "edit", targetStepId |-> "G1",
                         base |-> EmptyGroup, proposed |-> EmptyGroup]
UnchangedByIDGenderIntent == [kind |-> "edit", targetStepId |-> "G1",
                              base |-> ByIDGenderGroup, proposed |-> ByIDGenderGroup]
RestoreByIDIntent == [kind |-> "edit", targetStepId |-> "G1",
                      base |-> ByIDGenderGroup, proposed |-> ByIDGroup]
UnchangedByIDIntent == [kind |-> "edit", targetStepId |-> "G1",
                        base |-> ByIDGroup, proposed |-> ByIDGroup]
RemoveByIDIntent == [kind |-> "remove", targetStepId |-> "G1",
                     base |-> ByIDGroup, proposed |-> NoGroup]
CandidateIntentValues == {CreateEmptyIntent, EditByIDIntent,
                          EditByIDGenderIntent, UnchangedEmptyIntent,
                          UnchangedByIDGenderIntent, RestoreByIDIntent,
                          UnchangedByIDIntent, RemoveByIDIntent}

IntentBaseMatchesPersisted(intent, currentGroup) ==
  IF intent.kind = "create"
  THEN /\ intent.base = NoGroup
       /\ currentGroup = NoGroup
  ELSE /\ currentGroup # NoGroup
       /\ intent.base = currentGroup
       /\ intent.targetStepId = currentGroup.stepId

NoPreviewOwner == [mountEpoch |-> 0, captureId |-> "NoCapture"]
PreviewOwners == {NoPreviewOwner} \cup [mountEpoch : MountEpochs, captureId : CaptureIds]
PreviewOwner(epoch, capture) == [mountEpoch |-> epoch, captureId |-> capture]

NoCandidate == [kind |-> "none", targetStepId |-> NoStep,
                 base |-> NoGroup, proposed |-> NoGroup, owner |-> NoPreviewOwner]
CandidateStates == {NoCandidate} \cup
  [kind : CandidateKinds, targetStepId : StepIds \cup {NoStep},
   base : GroupRecords, proposed : GroupRecords, owner : PreviewOwners]

CandidateFromIntent(intent, owner) ==
  [kind |-> intent.kind,
   targetStepId |-> intent.targetStepId,
   base |-> intent.base,
   proposed |-> intent.proposed,
   owner |-> owner]

CandidateIntentOf(currentCandidate) ==
  IF currentCandidate = NoCandidate
  THEN NoCandidateIntent
  ELSE [kind |-> currentCandidate.kind,
        targetStepId |-> currentCandidate.targetStepId,
        base |-> currentCandidate.base,
        proposed |-> currentCandidate.proposed]

NoAppliedCandidate == [intent |-> NoCandidateIntent, owner |-> NoPreviewOwner]
AppliedCandidates == {NoAppliedCandidate} \cup
  [intent : CandidateIntents, owner : PreviewOwners]

VARIABLES
  phase,
  reshapeCount,
  pivotReady,
  preview,
  proposalPreviewOwner,
  proposalPreviewCaptureOwner,
  choicePreview,
  choicePreviewOwner,
  proposalIdPresent,
  rowsPresent,
  tablePreview,
  sourceRowsPresent,
  persistedGroup,
  mountedGroup,
  candidate,
  mountEpoch,
  lastAppliedCandidate,
  lastCancelObservedGroup,
  lastIgnoredRequestOwner,
  lastAction,
  cdaCurrentTable,
  cdaRowExpansionCount,
  cdaGroupInputTable,
  cdaMountedGroupInputTable,
  cdaGroupInputStep,
  cdaMountedGroupInputStep,
  cdaPersistedGroup,
  cdaMountedGroup,
  cdaPersistedRelatedSource,
  cdaMountedRelatedSource,
  cdaMountEpoch,
  cdaPreview,
  cdaProposalPreviewOwner,
  cdaProposalPreviewCaptureOwner,
  cdaProposalIdPresent,
  cdaRowsPresent,
  cdaGroupCandidate,
  cdaExpansionCandidate,
  cdaRelatedCandidate,
  cdaOutputToken,
  cdaDraftVersion,
  cdaDraftDigest,
  cdaLastAppliedGroupCandidate,
  cdaLastAppliedRelatedCandidate,
  cdaLastCancelBefore,
  cdaLastCancelAfter,
  cdaLastIgnoredRequestOwner,
  cdaLastGroupEditRelatedBefore,
  cdaLastGroupEditRelatedAfter,
  cdaLastAction,
  cdaWitnessStage

vars == <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
          proposalPreviewCaptureOwner, choicePreview, choicePreviewOwner, proposalIdPresent,
          rowsPresent, tablePreview, sourceRowsPresent, persistedGroup, mountedGroup,
          candidate, mountEpoch, lastAppliedCandidate, lastCancelObservedGroup,
          lastIgnoredRequestOwner, lastAction>>

GroupStateVars == <<persistedGroup, mountedGroup, candidate, mountEpoch,
                    proposalPreviewCaptureOwner, lastAppliedCandidate,
                    lastCancelObservedGroup, lastIgnoredRequestOwner>>

GroupUnchanged == UNCHANGED GroupStateVars

GroupPreviewUnchanged ==
  UNCHANGED <<persistedGroup, mountedGroup, mountEpoch,
              lastCancelObservedGroup, lastIgnoredRequestOwner>>

GroupProofUnchanged ==
  UNCHANGED <<persistedGroup, mountedGroup, candidate, mountEpoch,
              lastCancelObservedGroup,
              lastIgnoredRequestOwner>>

CloseProposalPreview ==
  /\ preview \in {"none", "ready"}
  /\ preview' = "none"
  /\ proposalIdPresent' = IF preview = "ready" THEN FALSE ELSE proposalIdPresent
  /\ rowsPresent' = IF preview = "ready" THEN FALSE ELSE rowsPresent
  /\ UNCHANGED proposalPreviewOwner

AppliedCandidateRecord(intent, currentCandidate) ==
  [intent |-> intent, owner |-> currentCandidate.owner]

Init ==
  /\ phase = "selected"
  /\ reshapeCount = 0
  /\ pivotReady = FALSE
  /\ preview = "none"
  /\ proposalPreviewOwner = "NoAttempt"
  /\ proposalPreviewCaptureOwner = NoPreviewOwner
  /\ choicePreview = "none"
  /\ choicePreviewOwner = "NoAttempt"
  /\ proposalIdPresent = FALSE
  /\ rowsPresent = FALSE
  /\ tablePreview = "ready"
  /\ sourceRowsPresent = TRUE
  /\ persistedGroup = NoGroup
  /\ mountedGroup = NoGroup
  /\ candidate = NoCandidate
  /\ mountEpoch = 0
  /\ lastAppliedCandidate = NoAppliedCandidate
  /\ lastCancelObservedGroup = NoGroup
  /\ lastIgnoredRequestOwner = NoPreviewOwner
  /\ lastAction = "Init"

GroupLegacy(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ candidate = NoCandidate
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ CloseProposalPreview
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ GroupProofUnchanged
  /\ UNCHANGED <<pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "Group"

GroupApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = proposalPreviewOwner
  /\ observedEpoch = mountEpoch
  /\ candidateOwner = candidate.owner
  /\ intent \in CandidateIntentValues
  /\ candidate # NoCandidate
  /\ candidate.kind = "create"
  /\ CandidateIntentOf(candidate) = intent
  /\ candidate.owner = proposalPreviewCaptureOwner
  /\ candidateOwner = proposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = mountEpoch
  /\ preview = "ready"
  /\ savedBeforeGroup = persistedGroup
  /\ intent.base = persistedGroup
  /\ savedAfterGroup = intent.proposed
  /\ intent.proposed # NoGroup
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ persistedGroup' = savedAfterGroup
  /\ mountedGroup' = savedAfterGroup
  /\ candidate' = NoCandidate
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = AppliedCandidateRecord(intent, candidate)
  /\ CloseProposalPreview
  /\ UNCHANGED <<pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent,
                  mountEpoch, lastCancelObservedGroup, lastIgnoredRequestOwner>>
  /\ lastAction' = "Group"

Group(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  \/ GroupLegacy(attempt)
  \/ GroupApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup)

GroupAgain ==
  /\ phase = "reshaping"
  /\ ~pivotReady
  /\ reshapeCount = 1
  /\ reshapeCount' = 2
  /\ CloseProposalPreview
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "GroupAgain"

AddColumn ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ choicePreview = "ready"
  /\ choicePreview' = "none"
  /\ CloseProposalPreview
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "AddColumn"

CancelFilter ==
  /\ preview \in {"ready", "error"}
  /\ preview' = "none"
  /\ proposalIdPresent' = FALSE
  /\ rowsPresent' = FALSE
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "CancelFilter"

EditGroupLegacy(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ candidate = NoCandidate
  /\ phase = "reshaping"
  /\ reshapeCount = 1
  /\ ~pivotReady
  /\ reshapeCount' = 2
  /\ CloseProposalPreview
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ GroupProofUnchanged
  /\ UNCHANGED <<phase, pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "EditGroup"

EditGroupApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = proposalPreviewOwner
  /\ observedEpoch = mountEpoch
  /\ candidateOwner = candidate.owner
  /\ intent \in CandidateIntentValues
  /\ candidate # NoCandidate
  /\ candidate.kind = "edit"
  /\ CandidateIntentOf(candidate) = intent
  /\ candidate.owner = proposalPreviewCaptureOwner
  /\ candidateOwner = proposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = mountEpoch
  /\ preview = "ready"
  /\ persistedGroup # NoGroup
  /\ candidate.targetStepId = persistedGroup.stepId
  /\ intent.targetStepId = persistedGroup.stepId
  /\ intent.proposed # NoGroup
  /\ intent.proposed.stepId = persistedGroup.stepId
  /\ savedBeforeGroup = persistedGroup
  /\ intent.base = persistedGroup
  /\ savedAfterGroup = intent.proposed
  /\ phase = "reshaping"
  /\ reshapeCount = 1
  /\ ~pivotReady
  /\ reshapeCount' = 2
  /\ persistedGroup' = savedAfterGroup
  /\ mountedGroup' = savedAfterGroup
  /\ candidate' = NoCandidate
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = AppliedCandidateRecord(intent, candidate)
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent,
                  mountEpoch, lastCancelObservedGroup, lastIgnoredRequestOwner>>
  /\ lastAction' = "EditGroup"

EditGroup(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  \/ EditGroupLegacy(attempt)
  \/ EditGroupApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup)

EditGroupAgainLegacy(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ candidate = NoCandidate
  /\ phase = "reshaping"
  /\ reshapeCount = MaxReshapes
  /\ ~pivotReady
  /\ CloseProposalPreview
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ GroupProofUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "EditGroupAgain"

EditGroupAgainApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = proposalPreviewOwner
  /\ observedEpoch = mountEpoch
  /\ candidateOwner = candidate.owner
  /\ intent \in CandidateIntentValues
  /\ candidate # NoCandidate
  /\ candidate.kind = "edit"
  /\ CandidateIntentOf(candidate) = intent
  /\ candidate.owner = proposalPreviewCaptureOwner
  /\ candidateOwner = proposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = mountEpoch
  /\ preview = "ready"
  /\ persistedGroup # NoGroup
  /\ candidate.targetStepId = persistedGroup.stepId
  /\ intent.targetStepId = persistedGroup.stepId
  /\ intent.proposed # NoGroup
  /\ intent.proposed.stepId = persistedGroup.stepId
  /\ savedBeforeGroup = persistedGroup
  /\ intent.base = persistedGroup
  /\ savedAfterGroup = intent.proposed
  /\ phase = "reshaping"
  /\ reshapeCount = MaxReshapes
  /\ ~pivotReady
  /\ persistedGroup' = savedAfterGroup
  /\ mountedGroup' = savedAfterGroup
  /\ candidate' = NoCandidate
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = AppliedCandidateRecord(intent, candidate)
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent, mountEpoch,
                  lastCancelObservedGroup, lastIgnoredRequestOwner>>
  /\ lastAction' = "EditGroupAgain"

EditGroupAgain(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  \/ EditGroupAgainLegacy(attempt)
  \/ EditGroupAgainApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup)

RemoveGroupLegacy(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ candidate = NoCandidate
  /\ phase = "reshaping"
  /\ reshapeCount >= 1
  /\ ~pivotReady
  /\ phase' = "selected"
  /\ reshapeCount' = 0
  /\ CloseProposalPreview
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ GroupProofUnchanged
  /\ UNCHANGED <<pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "RemoveGroup"

RemoveGroupApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = proposalPreviewOwner
  /\ observedEpoch = mountEpoch
  /\ candidateOwner = candidate.owner
  /\ intent \in CandidateIntentValues
  /\ candidate # NoCandidate
  /\ candidate.kind = "remove"
  /\ CandidateIntentOf(candidate) = intent
  /\ candidate.owner = proposalPreviewCaptureOwner
  /\ candidateOwner = proposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = mountEpoch
  /\ preview = "ready"
  /\ persistedGroup # NoGroup
  /\ candidate.targetStepId = persistedGroup.stepId
  /\ intent.targetStepId = persistedGroup.stepId
  /\ intent.base = persistedGroup
  /\ intent.proposed = NoGroup
  /\ savedBeforeGroup = persistedGroup
  /\ savedAfterGroup = NoGroup
  /\ phase = "reshaping"
  /\ reshapeCount >= 1
  /\ ~pivotReady
  /\ phase' = "selected"
  /\ reshapeCount' = 0
  /\ persistedGroup' = NoGroup
  /\ mountedGroup' = NoGroup
  /\ candidate' = NoCandidate
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastAppliedCandidate' = AppliedCandidateRecord(intent, candidate)
  /\ CloseProposalPreview
  /\ UNCHANGED <<pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent,
                  mountEpoch, lastCancelObservedGroup, lastIgnoredRequestOwner>>
  /\ lastAction' = "RemoveGroup"

RemoveGroup(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup) ==
  \/ RemoveGroupLegacy(attempt)
  \/ RemoveGroupApply(attempt, observedEpoch, candidateOwner, intent, savedBeforeGroup, savedAfterGroup)

Pivot ==
  /\ phase = "reshaping"
  /\ reshapeCount = MaxReshapes
  /\ ~pivotReady
  /\ pivotReady' = TRUE
  /\ CloseProposalPreview
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "Pivot"

Preview(attempt, observedEpoch, ownerEpoch, captureId, intent) ==
  \* The request owner is the pair of the current mount epoch and the actual
  \* request capture token. Group candidate intent is captured from the
  \* submitted proposal; non-Group previews use NoCandidateIntent.
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch = mountEpoch
  /\ ownerEpoch = observedEpoch
  /\ captureId \in CaptureIds
  /\ intent \in {NoCandidateIntent} \cup CandidateIntentValues
  /\ (intent = NoCandidateIntent \/ IntentBaseMatchesPersisted(intent, persistedGroup))
  /\ phase \in {"selected", "reshaping"}
  /\ preview \in PreviewStates
  /\ preview' = "pending"
  /\ proposalPreviewOwner' = attempt
  /\ proposalPreviewCaptureOwner' = PreviewOwner(ownerEpoch, captureId)
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ candidate' = IF intent = NoCandidateIntent
                  THEN NoCandidate
                  ELSE CandidateFromIntent(intent, PreviewOwner(ownerEpoch, captureId))
  /\ proposalIdPresent' = FALSE
  /\ rowsPresent' = FALSE
  /\ GroupPreviewUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "Preview"

PreviewComplete(attempt, observedEpoch, ownerEpoch, captureId) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch \in MountEpochs
  /\ ownerEpoch \in MountEpochs
  /\ captureId \in CaptureIds
  /\ preview = "pending"
  /\ proposalPreviewOwner = attempt
  /\ proposalPreviewCaptureOwner = PreviewOwner(ownerEpoch, captureId)
  /\ observedEpoch = mountEpoch
  /\ ownerEpoch = observedEpoch
  /\ preview' = "ready"
  /\ proposalIdPresent' = TRUE
  /\ rowsPresent' = TRUE
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "PreviewComplete"

PreviewError(attempt, observedEpoch, ownerEpoch, captureId, hasProposalId) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch \in MountEpochs
  /\ ownerEpoch \in MountEpochs
  /\ captureId \in CaptureIds
  /\ preview = "pending"
  /\ proposalPreviewOwner = attempt
  /\ proposalPreviewCaptureOwner = PreviewOwner(ownerEpoch, captureId)
  /\ observedEpoch = mountEpoch
  /\ ownerEpoch = observedEpoch
  /\ hasProposalId \in BOOLEAN
  /\ preview' = "error"
  /\ proposalIdPresent' = hasProposalId
  /\ rowsPresent' = FALSE
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "PreviewError"

PreviewRequestCancelled(attempt, observedEpoch, ownerEpoch, captureId) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch \in MountEpochs
  /\ ownerEpoch \in MountEpochs
  /\ captureId \in CaptureIds
  /\ preview = "pending"
  /\ proposalPreviewOwner = attempt
  /\ proposalPreviewCaptureOwner = PreviewOwner(ownerEpoch, captureId)
  /\ observedEpoch = mountEpoch
  /\ ownerEpoch = observedEpoch
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  choicePreview, choicePreviewOwner, proposalIdPresent, rowsPresent,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "PreviewRequestCancelled"

ChoicePreview(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ phase \in {"selected", "reshaping"}
  /\ choicePreview \in {"none", "error"}
  /\ choicePreview' = "pending"
  /\ choicePreviewOwner' = attempt
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "ChoicePreview"

ChoicePreviewComplete(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ choicePreview = "pending"
  /\ choicePreviewOwner = attempt
  /\ choicePreview' = "ready"
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  choicePreviewOwner, proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "ChoicePreviewComplete"

ChoicePreviewError(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ choicePreview = "pending"
  /\ choicePreviewOwner = attempt
  /\ choicePreview' = "error"
  /\ GroupUnchanged
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  choicePreviewOwner, proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "ChoicePreviewError"

LatePreviewCompleteIgnored(attempt, observedEpoch, ownerEpoch, captureId) ==
  LET settledOwner == PreviewOwner(ownerEpoch, captureId)
  IN /\ attempt \in {"P1", "P2"}
     /\ observedEpoch \in MountEpochs
     /\ ownerEpoch \in MountEpochs
     /\ observedEpoch = mountEpoch
     /\ captureId \in CaptureIds
     /\ settledOwner # proposalPreviewCaptureOwner
     /\ lastIgnoredRequestOwner' = settledOwner
     /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                     proposalPreviewCaptureOwner, choicePreview, choicePreviewOwner,
                     proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent,
                     persistedGroup, mountedGroup, candidate, mountEpoch, lastAppliedCandidate,
                     lastCancelObservedGroup>>
     /\ lastAction' = "LatePreviewCompleteIgnored"

LatePreviewErrorIgnored(attempt, observedEpoch, ownerEpoch, captureId) ==
  LET settledOwner == PreviewOwner(ownerEpoch, captureId)
  IN /\ attempt \in {"P1", "P2"}
     /\ observedEpoch \in MountEpochs
     /\ ownerEpoch \in MountEpochs
     /\ observedEpoch = mountEpoch
     /\ captureId \in CaptureIds
     /\ settledOwner # proposalPreviewCaptureOwner
     /\ lastIgnoredRequestOwner' = settledOwner
     /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                     proposalPreviewCaptureOwner, choicePreview, choicePreviewOwner,
                     proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent,
                     persistedGroup, mountedGroup, candidate, mountEpoch, lastAppliedCandidate,
                     lastCancelObservedGroup>>
     /\ lastAction' = "LatePreviewErrorIgnored"

GroupEditCancel(observedEpoch, candidateOwner, savedBeforeGroup, savedAfterGroup,
                workspaceSame, versionSame, digestSame) ==
  /\ observedEpoch = mountEpoch
  /\ ((candidate = NoCandidate /\ preview = "none") \/
      (candidate # NoCandidate /\ candidate.kind = "edit"
       /\ candidate.base = persistedGroup
       /\ candidate.owner = proposalPreviewCaptureOwner /\ preview = "ready"))
  /\ IF candidate = NoCandidate
     THEN candidateOwner = NoPreviewOwner
     ELSE /\ candidateOwner = candidate.owner
          /\ candidateOwner = proposalPreviewCaptureOwner
          /\ candidateOwner.mountEpoch = observedEpoch
  /\ savedBeforeGroup = persistedGroup
  /\ savedAfterGroup = persistedGroup
  /\ workspaceSame
  /\ versionSame
  /\ digestSame
  /\ CloseProposalPreview
  /\ candidate' = NoCandidate
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastCancelObservedGroup' = savedAfterGroup
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner,
                  choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent,
                  persistedGroup, mountedGroup, mountEpoch,
                  lastIgnoredRequestOwner>>
  /\ lastAction' = "GroupEditCancel"

GroupRemovalCancel(observedEpoch, candidateOwner, savedBeforeGroup, savedAfterGroup,
                   workspaceSame, versionSame, digestSame) ==
  /\ observedEpoch = mountEpoch
  /\ candidate # NoCandidate
  /\ candidate.kind = "remove"
  /\ candidate.base = persistedGroup
  /\ candidate.owner = proposalPreviewCaptureOwner
  /\ candidateOwner = candidate.owner
  /\ candidateOwner = proposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = observedEpoch
  /\ preview = "ready"
  /\ savedBeforeGroup = persistedGroup
  /\ savedAfterGroup = persistedGroup
  /\ workspaceSame
  /\ versionSame
  /\ digestSame
  /\ CloseProposalPreview
  /\ candidate' = NoCandidate
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ lastCancelObservedGroup' = savedAfterGroup
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner,
                  choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent,
                  persistedGroup, mountedGroup, mountEpoch,
                  lastIgnoredRequestOwner>>
  /\ lastAction' = "GroupRemovalCancel"

Reload(observedEpoch, savedBeforeGroup, savedAfterGroup, observedMountedGroup) ==
  /\ mountEpoch < MaxMountEpoch
  /\ observedEpoch = mountEpoch + 1
  /\ savedBeforeGroup = persistedGroup
  /\ savedAfterGroup = persistedGroup
  /\ observedMountedGroup = persistedGroup
  /\ mountEpoch' = observedEpoch
  /\ mountedGroup' = observedMountedGroup
  /\ candidate' = NoCandidate
  /\ preview' = "none"
  /\ proposalPreviewOwner' = "NoAttempt"
  /\ proposalPreviewCaptureOwner' = NoPreviewOwner
  /\ proposalIdPresent' = FALSE
  /\ rowsPresent' = FALSE
  /\ choicePreview' = "none"
  /\ choicePreviewOwner' = "NoAttempt"
  /\ lastAppliedCandidate' = NoAppliedCandidate
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, tablePreview, sourceRowsPresent,
                  persistedGroup, lastCancelObservedGroup,
                  lastIgnoredRequestOwner>>
  /\ lastAction' = "Reload"

Stutter ==
  UNCHANGED vars

Next ==
  \/ \E attempt \in {"P1", "P2"}, captureId \in CaptureIds,
       intent \in {NoCandidateIntent} \cup CandidateIntentValues:
       Preview(attempt, mountEpoch, mountEpoch, captureId, intent)
  \/ \E attempt \in {"P1", "P2"}, intent \in {NoCandidateIntent} \cup CandidateIntentValues:
       Group(attempt, mountEpoch, candidate.owner, intent, intent.base, intent.proposed)
  \/ GroupAgain
  \/ AddColumn
  \/ CancelFilter
  \/ \E attempt \in {"P1", "P2"}, intent \in {NoCandidateIntent} \cup CandidateIntentValues:
       EditGroup(attempt, mountEpoch, candidate.owner, intent, intent.base, intent.proposed)
  \/ \E attempt \in {"P1", "P2"}, intent \in {NoCandidateIntent} \cup CandidateIntentValues:
       EditGroupAgain(attempt, mountEpoch, candidate.owner, intent, intent.base, intent.proposed)
  \/ \E attempt \in {"P1", "P2"}, intent \in {NoCandidateIntent} \cup CandidateIntentValues:
       RemoveGroup(attempt, mountEpoch, candidate.owner, intent, intent.base, intent.proposed)
  \/ Pivot
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in MountEpochs, captureId \in CaptureIds:
       PreviewComplete(attempt, mountEpoch, ownerEpoch, captureId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in MountEpochs, captureId \in CaptureIds,
       hasProposalId \in BOOLEAN:
       PreviewError(attempt, mountEpoch, ownerEpoch, captureId, hasProposalId)
  \/ \E attempt \in {"P1", "P2"}, captureId \in CaptureIds:
       PreviewRequestCancelled(attempt, mountEpoch, mountEpoch, captureId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in MountEpochs, captureId \in CaptureIds:
       LatePreviewCompleteIgnored(attempt, mountEpoch, ownerEpoch, captureId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in MountEpochs, captureId \in CaptureIds:
       LatePreviewErrorIgnored(attempt, mountEpoch, ownerEpoch, captureId)
  \/ \E attempt \in {"P1", "P2"}: ChoicePreview(attempt)
  \/ \E attempt \in {"P1", "P2"}: ChoicePreviewComplete(attempt)
  \/ \E attempt \in {"P1", "P2"}: ChoicePreviewError(attempt)
  \/ GroupEditCancel(mountEpoch, candidate.owner, persistedGroup, persistedGroup, TRUE, TRUE, TRUE)
  \/ GroupRemovalCancel(mountEpoch, candidate.owner, persistedGroup, persistedGroup, TRUE, TRUE, TRUE)
  \/ Reload(mountEpoch + 1, persistedGroup, persistedGroup, persistedGroup)
  \/ Stutter

\* This static relation explores the full finite Group lifecycle transition
\* system; every applicable Group lifecycle action remains nondeterministic.
GroupSafetyVars == vars

GroupSafetyNext ==
  \/ (\E previewAttempt \in {"P1", "P2"}, previewCapture \in CaptureIds,
        previewIntent \in {NoCandidateIntent} \cup CandidateIntentValues:
        Preview(previewAttempt, mountEpoch, mountEpoch, previewCapture,
                previewIntent))
  \/ (\E createAttempt \in {"P1", "P2"}, createIntent \in CandidateIntentValues:
        GroupApply(createAttempt, mountEpoch, candidate.owner, createIntent,
                   persistedGroup, createIntent.proposed))
  \/ (\E editAttempt \in {"P1", "P2"}, editIntent \in CandidateIntentValues:
        EditGroupApply(editAttempt, mountEpoch, candidate.owner, editIntent,
                       persistedGroup, editIntent.proposed))
  \/ (\E editAgainAttempt \in {"P1", "P2"},
        editAgainIntent \in CandidateIntentValues:
        EditGroupAgainApply(editAgainAttempt, mountEpoch, candidate.owner,
                            editAgainIntent, persistedGroup,
                            editAgainIntent.proposed))
  \/ (\E removeAttempt \in {"P1", "P2"}, removeIntent \in CandidateIntentValues:
        RemoveGroupApply(removeAttempt, mountEpoch, candidate.owner,
                         removeIntent, persistedGroup, removeIntent.proposed))
  \/ (\E completeAttempt \in {"P1", "P2"}, completeEpoch \in MountEpochs,
        completeCapture \in CaptureIds:
        PreviewComplete(completeAttempt, mountEpoch, completeEpoch,
                        completeCapture))
  \/ (\E errorAttempt \in {"P1", "P2"}, errorEpoch \in MountEpochs,
        errorCapture \in CaptureIds, hasProposalId \in BOOLEAN:
        PreviewError(errorAttempt, mountEpoch, errorEpoch, errorCapture,
                     hasProposalId))
  \/ (\E cancelledAttempt \in {"P1", "P2"}, cancelCapture \in CaptureIds:
        PreviewRequestCancelled(cancelledAttempt, mountEpoch, mountEpoch,
                                cancelCapture))
  \/ (\E lateCompleteAttempt \in {"P1", "P2"}, lateCompleteEpoch \in MountEpochs,
        lateCompleteCapture \in CaptureIds:
        LatePreviewCompleteIgnored(lateCompleteAttempt, mountEpoch,
                                   lateCompleteEpoch, lateCompleteCapture))
  \/ (\E lateErrorAttempt \in {"P1", "P2"}, lateErrorEpoch \in MountEpochs,
        lateErrorCapture \in CaptureIds:
        LatePreviewErrorIgnored(lateErrorAttempt, mountEpoch, lateErrorEpoch,
                                lateErrorCapture))
  \/ GroupEditCancel(mountEpoch, candidate.owner, persistedGroup,
                     persistedGroup, TRUE, TRUE, TRUE)
  \/ GroupRemovalCancel(mountEpoch, candidate.owner, persistedGroup,
                        persistedGroup, TRUE, TRUE, TRUE)
  \/ Reload(mountEpoch + 1, persistedGroup, persistedGroup, persistedGroup)

TypeOK ==
  /\ phase \in Phases
  /\ reshapeCount \in 0..MaxReshapes
  /\ pivotReady \in BOOLEAN
  /\ preview \in PreviewStates
  /\ proposalPreviewOwner \in ProposalOwners
  /\ proposalPreviewCaptureOwner \in PreviewOwners
  /\ choicePreview \in ChoicePreviewStates
  /\ choicePreviewOwner \in ProposalOwners
  /\ proposalIdPresent \in BOOLEAN
  /\ rowsPresent \in BOOLEAN
  /\ tablePreview \in TablePreviewStates
  /\ sourceRowsPresent \in BOOLEAN
  /\ persistedGroup \in GroupRecords
  /\ GroupRecordOK(persistedGroup)
  /\ mountedGroup \in GroupRecords
  /\ GroupRecordOK(mountedGroup)
  /\ candidate \in CandidateStates
  /\ (candidate = NoCandidate \/
      /\ CandidateIntentOf(candidate) \in CandidateIntentValues
      /\ GroupRecordOK(candidate.base)
      /\ GroupRecordOK(candidate.proposed))
  /\ mountEpoch \in MountEpochs
  /\ lastAppliedCandidate \in AppliedCandidates
  /\ lastAppliedCandidate.intent \in CandidateIntents
  /\ GroupRecordOK(lastAppliedCandidate.intent.base)
  /\ GroupRecordOK(lastAppliedCandidate.intent.proposed)
  /\ lastCancelObservedGroup \in GroupRecords
  /\ GroupRecordOK(lastCancelObservedGroup)
  /\ lastIgnoredRequestOwner \in PreviewOwners
  /\ lastAction \in JourneyActions

CancelPreservesPersistedGroup ==
  /\ (lastAction \in {"GroupEditCancel", "GroupRemovalCancel"})
     => persistedGroup = lastCancelObservedGroup

ApplyUsesCurrentCandidate ==
  /\ (candidate = NoCandidate \/
      /\ candidate.owner = proposalPreviewCaptureOwner
      /\ candidate.owner # NoPreviewOwner
      /\ candidate.owner.mountEpoch = mountEpoch)
  /\ (lastAppliedCandidate = NoAppliedCandidate \/
      LET applied == lastAppliedCandidate
      IN /\ applied.intent \in CandidateIntentValues
         /\ applied.owner # NoPreviewOwner
         /\ applied.owner.mountEpoch = mountEpoch
         /\ GroupRecordOK(applied.intent.base)
         /\ GroupRecordOK(applied.intent.proposed)
         /\ persistedGroup = applied.intent.proposed
         /\ mountedGroup = persistedGroup
         /\ (applied.intent.kind = "edit" =>
             /\ applied.intent.targetStepId = applied.intent.base.stepId
             /\ applied.intent.targetStepId = applied.intent.proposed.stepId))

ReloadRestoresPersistedGroup ==
  /\ (lastAction = "Reload" =>
      mountedGroup = persistedGroup)

OldOwnerCannotSettleCurrentPreview ==
  /\ (lastAction \in {"LatePreviewCompleteIgnored", "LatePreviewErrorIgnored"} =>
      lastIgnoredRequestOwner # proposalPreviewCaptureOwner)

PivotRequiresReshapeOrDirect ==
  pivotReady => reshapeCount = MaxReshapes

RowsOnlyAfterProposalPreview ==
  rowsPresent => preview = "ready"

SourceRowsRequireReadyTablePreview ==
  sourceRowsPresent => tablePreview = "ready"

\* CDA uses a separate bounded profile so its COUNT_DISTINCT and five-mount
\* source journey do not widen the legacy Group-only model.
CdaMaxMountEpoch == 5
CdaMountEpochs == 0..CdaMaxMountEpoch
CdaReshapeCounts == 0..MaxReshapes
CdaProposalOwners == {"P1", "P2", "NoAttempt"}
CdaRequestOwnerTokens == CaptureIds
CdaOwnerEpochs == CdaMountEpochs
CdaPreviewStates == PreviewStates
CdaGroupInputSteps == {"None", "G1"}
CdaDataTokens == {"Specimen", "Patient", "Observation"}
CdaRowExpansionStages == {"SpecimenPatient", "PatientObservation"}
CdaAggregates == {"COUNT_ROWS", "COUNT_DISTINCT"}
CdaColumnTokens == {"ID", "Status", "O1"}
CdaSourceTokens == {"None", "ObservationStatus"}
CdaRelatedStepTokens == {"None", "R1"}
CdaOutputColumnTokens == {"None", "O1"}
CdaRouteTokens == {"None", "SpecimenPatientObservation"}
CdaWorkspaceTokens == {"W1"}
CdaOutputTokens == {"O0"}
CdaDraftVersions == 0..7
CdaDraftDigests == {"D0", "D1", "D2", "D3", "D4", "D5", "D6", "D7"}

CdaGroupRecords == {NoGroup} \cup
  [stepId : StepIds, shape : GroupShapes, keys : GroupKeySequences,
   aggregate : CdaAggregates]

CdaGroupRecordOK(groupValue) ==
  IF groupValue = NoGroup
  THEN TRUE
  ELSE /\ groupValue \in [stepId : StepIds, shape : GroupShapes,
                            keys : GroupKeySequences, aggregate : CdaAggregates]
       /\ CASE groupValue.shape = "EmptyKeys" -> groupValue.keys = <<>>
            [] groupValue.shape = "ByID" -> groupValue.keys = <<"ID">>
            [] groupValue.shape = "ByIDGender" -> groupValue.keys = <<"ID", "Gender">>

CdaByIDCountRowsGroup == [stepId |-> "G1", shape |-> "ByID",
                          keys |-> <<"ID">>, aggregate |-> "COUNT_ROWS"]
CdaByIDCountDistinctGroup == [stepId |-> "G1", shape |-> "ByID",
                              keys |-> <<"ID">>, aggregate |-> "COUNT_DISTINCT"]

CdaNoGroupIntent == [kind |-> "none", targetStepId |-> NoStep,
                     base |-> NoGroup, proposed |-> NoGroup]
CdaCreateByIDCountRowsIntent == [kind |-> "create", targetStepId |-> NoStep,
                                 base |-> NoGroup,
                                 proposed |-> CdaByIDCountRowsGroup]
CdaEditByIDCountDistinctIntent == [kind |-> "edit", targetStepId |-> "G1",
                                   base |-> CdaByIDCountRowsGroup,
                                   proposed |-> CdaByIDCountDistinctGroup]
CdaEditByIDCountRowsIntent == [kind |-> "edit", targetStepId |-> "G1",
                               base |-> CdaByIDCountDistinctGroup,
                               proposed |-> CdaByIDCountRowsGroup]
CdaUnchangedByIDCountRowsIntent ==
  [kind |-> "edit", targetStepId |-> "G1",
   base |-> CdaByIDCountRowsGroup, proposed |-> CdaByIDCountRowsGroup]
CdaUnchangedByIDCountDistinctIntent ==
  [kind |-> "edit", targetStepId |-> "G1",
   base |-> CdaByIDCountDistinctGroup,
   proposed |-> CdaByIDCountDistinctGroup]
CdaGroupIntentValues == {CdaCreateByIDCountRowsIntent,
                         CdaEditByIDCountDistinctIntent,
                         CdaEditByIDCountRowsIntent,
                         CdaUnchangedByIDCountRowsIntent,
                         CdaUnchangedByIDCountDistinctIntent}
CdaGroupIntentDomain == {CdaNoGroupIntent} \cup CdaGroupIntentValues

CdaNoPreviewOwner == [mountEpoch |-> 0, captureId |-> "NoCapture"]
CdaPreviewOwners == {CdaNoPreviewOwner} \cup
  [mountEpoch : CdaOwnerEpochs, captureId : CdaRequestOwnerTokens]
CdaPreviewOwner(epoch, capture) == [mountEpoch |-> epoch, captureId |-> capture]

CdaNoGroupCandidate == [intent |-> CdaNoGroupIntent,
                        owner |-> CdaNoPreviewOwner,
                        groupInputStep |-> "None",
                        groupInputTable |-> "None"]
CdaGroupCandidates == {CdaNoGroupCandidate} \cup
  [intent : CdaGroupIntentValues, owner : CdaPreviewOwners,
   groupInputStep : CdaGroupInputSteps,
   groupInputTable : CdaDataTokens]
CdaGroupCandidateFromIntent(intent, owner, inputStep, inputTable) ==
  [intent |-> intent, owner |-> owner,
   groupInputStep |-> inputStep, groupInputTable |-> inputTable]

CdaNoExpansionIntent == [kind |-> "none", stage |-> "None",
                         beforeTable |-> "None", afterTable |-> "None"]
CdaSpecimenPatientIntent == [kind |-> "expandRows",
                             stage |-> "SpecimenPatient",
                             beforeTable |-> "Specimen",
                             afterTable |-> "Patient"]
CdaPatientObservationIntent == [kind |-> "expandRows",
                                stage |-> "PatientObservation",
                                beforeTable |-> "Patient",
                                afterTable |-> "Observation"]
CdaExpansionIntentValues == {CdaSpecimenPatientIntent,
                             CdaPatientObservationIntent}
CdaExpansionIntentDomain == {CdaNoExpansionIntent} \cup CdaExpansionIntentValues
CdaNoExpansionCandidate == [intent |-> CdaNoExpansionIntent,
                            owner |-> CdaNoPreviewOwner]
CdaExpansionCandidates == {CdaNoExpansionCandidate} \cup
  [intent : CdaExpansionIntentValues, owner : CdaPreviewOwners]

CdaNoRelatedSource == [sourceToken |-> "None",
                       relatedStepToken |-> "None",
                       inputGroupStepToken |-> "None",
                       outputColumnToken |-> "None",
                       routeToken |-> "None"]
CdaObservationStatusRelatedSource ==
  [sourceToken |-> "ObservationStatus",
   relatedStepToken |-> "R1",
   inputGroupStepToken |-> "G1",
   outputColumnToken |-> "O1",
   routeToken |-> "SpecimenPatientObservation"]
CdaRelatedSources == {CdaNoRelatedSource,
                      CdaObservationStatusRelatedSource}

CdaNoRelatedIntent == [kind |-> "none", targetStepId |-> "None",
                       groupInputStep |-> "None",
                       base |-> CdaNoRelatedSource,
                       proposed |-> CdaNoRelatedSource]
CdaAddObservationStatusIntent ==
  [kind |-> "add", targetStepId |-> "None",
   groupInputStep |-> "G1",
   base |-> CdaNoRelatedSource,
   proposed |-> CdaObservationStatusRelatedSource]
CdaRemoveObservationStatusIntent ==
  [kind |-> "remove", targetStepId |-> "R1",
   groupInputStep |-> "G1",
   base |-> CdaObservationStatusRelatedSource,
   proposed |-> CdaNoRelatedSource]
CdaRelatedIntentValues == {CdaAddObservationStatusIntent,
                           CdaRemoveObservationStatusIntent,
                           [kind |-> "keep", targetStepId |-> "R1",
                            groupInputStep |-> "G1",
                            base |-> CdaObservationStatusRelatedSource,
                            proposed |-> CdaObservationStatusRelatedSource]}
CdaRelatedIntentDomain == {CdaNoRelatedIntent} \cup CdaRelatedIntentValues
CdaNoRelatedCandidate == [intent |-> CdaNoRelatedIntent,
                          owner |-> CdaNoPreviewOwner,
                          attempt |-> "NoAttempt",
                          groupInputStep |-> "None"]
CdaRelatedCandidates == {CdaNoRelatedCandidate} \cup
  [intent : CdaRelatedIntentValues, owner : CdaPreviewOwners,
   attempt : CdaProposalOwners, groupInputStep : CdaGroupInputSteps]
CdaRelatedCandidateFromIntent(intent, owner, attempt, inputStep) ==
  [intent |-> intent, owner |-> owner, attempt |-> attempt,
   groupInputStep |-> inputStep]

CdaSnapshot(groupValue, relatedValue, outputValue, versionValue, digestValue) ==
  [workspaceToken |-> "W1",
   group |-> groupValue,
   relatedSource |-> relatedValue,
   outputToken |-> outputValue,
   draftVersion |-> versionValue,
   draftDigest |-> digestValue]
CdaSnapshot0 == CdaSnapshot(NoGroup, CdaNoRelatedSource, "O0", 0, "D0")
CdaSnapshot1 == CdaSnapshot(NoGroup, CdaNoRelatedSource, "O0", 1, "D1")
CdaSnapshot2 == CdaSnapshot(NoGroup, CdaNoRelatedSource, "O0", 2, "D2")
CdaSnapshot3 == CdaSnapshot(CdaByIDCountRowsGroup,
                             CdaNoRelatedSource, "O0", 3, "D3")
CdaSnapshot4 == CdaSnapshot(CdaByIDCountDistinctGroup,
                             CdaNoRelatedSource, "O0", 4, "D4")
CdaSnapshot5 == CdaSnapshot(CdaByIDCountDistinctGroup,
                             CdaObservationStatusRelatedSource,
                             "O0", 5, "D5")
CdaSnapshot6 == CdaSnapshot(CdaByIDCountRowsGroup,
                             CdaObservationStatusRelatedSource,
                             "O0", 6, "D6")
CdaSnapshot7 == CdaSnapshot(CdaByIDCountRowsGroup,
                             CdaNoRelatedSource, "O0", 7, "D3")
CdaSnapshotRecords == {CdaSnapshot0, CdaSnapshot1, CdaSnapshot2,
                       CdaSnapshot3, CdaSnapshot4, CdaSnapshot5,
                       CdaSnapshot6, CdaSnapshot7}
CdaSnapshotOK(snapshot) ==
  /\ snapshot \in CdaSnapshotRecords
  /\ CdaGroupRecordOK(snapshot.group)
  /\ snapshot.relatedSource \in CdaRelatedSources

CdaCASOf(snapshot) ==
  [workspaceToken |-> snapshot.workspaceToken,
   outputToken |-> snapshot.outputToken,
   versionStep |-> snapshot.draftVersion,
   digestToken |-> snapshot.draftDigest]

CdaNoAppliedGroupCandidate ==
  [intent |-> CdaNoGroupIntent,
   owner |-> CdaNoPreviewOwner,
   groupInputStep |-> "None",
   before |-> CdaSnapshot0,
   after |-> CdaSnapshot0]
CdaAppliedGroupCandidates == {CdaNoAppliedGroupCandidate} \cup
  [intent : CdaGroupIntentValues, owner : CdaPreviewOwners,
   groupInputStep : CdaGroupInputSteps,
   before : CdaSnapshotRecords, after : CdaSnapshotRecords]
CdaAppliedGroupCandidate(intent, owner, inputStep, before, after) ==
  [intent |-> intent, owner |-> owner, groupInputStep |-> inputStep,
   before |-> before, after |-> after]

CdaNoAppliedRelatedCandidate ==
  [intent |-> CdaNoRelatedIntent,
   owner |-> CdaNoPreviewOwner,
   attempt |-> "NoAttempt",
   groupInputStep |-> "None",
   before |-> CdaSnapshot0,
   after |-> CdaSnapshot0]
CdaAppliedRelatedCandidates == {CdaNoAppliedRelatedCandidate} \cup
  [intent : CdaRelatedIntentValues, owner : CdaPreviewOwners,
   attempt : CdaProposalOwners, groupInputStep : CdaGroupInputSteps,
   before : CdaSnapshotRecords, after : CdaSnapshotRecords]
CdaAppliedRelatedCandidate(intent, owner, attempt, inputStep, before, after) ==
  [intent |-> intent, owner |-> owner, attempt |-> attempt,
   groupInputStep |-> inputStep, before |-> before, after |-> after]

CdaCurrentSnapshot ==
  CdaSnapshot(cdaPersistedGroup, cdaPersistedRelatedSource,
              cdaOutputToken, cdaDraftVersion, cdaDraftDigest)

CdaVars == <<cdaCurrentTable, cdaRowExpansionCount, cdaGroupInputTable,
             cdaMountedGroupInputTable, cdaGroupInputStep,
             cdaMountedGroupInputStep, cdaPersistedGroup, cdaMountedGroup,
             cdaPersistedRelatedSource, cdaMountedRelatedSource,
             cdaMountEpoch, cdaPreview, cdaProposalPreviewOwner,
             cdaProposalPreviewCaptureOwner, cdaProposalIdPresent,
             cdaRowsPresent, cdaGroupCandidate, cdaExpansionCandidate,
             cdaRelatedCandidate, cdaOutputToken, cdaDraftVersion,
             cdaDraftDigest, cdaLastAppliedGroupCandidate,
             cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
             cdaLastCancelAfter, cdaLastIgnoredRequestOwner,
             cdaLastGroupEditRelatedBefore, cdaLastGroupEditRelatedAfter,
             cdaLastAction, cdaWitnessStage>>

CdaInitialSnapshot == CdaSnapshot0
CdaInit ==
  /\ cdaCurrentTable = "Specimen"
  /\ cdaRowExpansionCount = 0
  /\ cdaGroupInputTable = "None"
  /\ cdaMountedGroupInputTable = "None"
  /\ cdaGroupInputStep = "None"
  /\ cdaMountedGroupInputStep = "None"
  /\ cdaPersistedGroup = NoGroup
  /\ cdaMountedGroup = NoGroup
  /\ cdaPersistedRelatedSource = CdaNoRelatedSource
  /\ cdaMountedRelatedSource = CdaNoRelatedSource
  /\ cdaMountEpoch = 0
  /\ cdaPreview = "none"
  /\ cdaProposalPreviewOwner = "NoAttempt"
  /\ cdaProposalPreviewCaptureOwner = CdaNoPreviewOwner
  /\ cdaProposalIdPresent = FALSE
  /\ cdaRowsPresent = FALSE
  /\ cdaGroupCandidate = CdaNoGroupCandidate
  /\ cdaExpansionCandidate = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate = CdaNoRelatedCandidate
  /\ cdaOutputToken = "O0"
  /\ cdaDraftVersion = 0
  /\ cdaDraftDigest = "D0"
  /\ cdaLastAppliedGroupCandidate = CdaNoAppliedGroupCandidate
  /\ cdaLastAppliedRelatedCandidate = CdaNoAppliedRelatedCandidate
  /\ cdaLastCancelBefore = CdaInitialSnapshot
  /\ cdaLastCancelAfter = CdaInitialSnapshot
  /\ cdaLastIgnoredRequestOwner = CdaNoPreviewOwner
  /\ cdaLastGroupEditRelatedBefore = CdaNoRelatedSource
  /\ cdaLastGroupEditRelatedAfter = CdaNoRelatedSource
  /\ cdaLastAction = "Init"
  /\ cdaWitnessStage = 0

CdaCurrentTableForExpansionCount(count) ==
  CASE count = 0 -> "Specimen"
    [] count = 1 -> "Patient"
    [] count = 2 -> "Observation"

CdaExpansionBefore(stage) ==
  CASE stage = "SpecimenPatient" -> "Specimen"
    [] stage = "PatientObservation" -> "Patient"

CdaExpansionAfter(stage) ==
  CASE stage = "SpecimenPatient" -> "Patient"
    [] stage = "PatientObservation" -> "Observation"

CdaExpansionBeforeSnapshot(stage) ==
  CASE stage = "SpecimenPatient" -> CdaSnapshot0
    [] stage = "PatientObservation" -> CdaSnapshot1

CdaExpansionAfterSnapshot(stage) ==
  CASE stage = "SpecimenPatient" -> CdaSnapshot1
    [] stage = "PatientObservation" -> CdaSnapshot2

CdaCurrentGroupCandidate(attempt, observedEpoch, candidateOwner,
                         intent, savedGroupInput) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = cdaProposalPreviewOwner
  /\ observedEpoch = cdaMountEpoch
  /\ candidateOwner = cdaGroupCandidate.owner
  /\ candidateOwner = cdaProposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = cdaMountEpoch
  /\ intent = cdaGroupCandidate.intent
  /\ cdaGroupCandidate # CdaNoGroupCandidate
  /\ cdaPreview = "ready"
  /\ savedGroupInput = cdaGroupInputStep
  /\ cdaGroupCandidate.groupInputStep = savedGroupInput

CdaCurrentRelatedCandidate(attempt, observedEpoch, candidateOwner,
                           intent, savedGroupInput) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = cdaProposalPreviewOwner
  /\ observedEpoch = cdaMountEpoch
  /\ candidateOwner = cdaRelatedCandidate.owner
  /\ candidateOwner = cdaProposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = cdaMountEpoch
  /\ attempt = cdaRelatedCandidate.attempt
  /\ intent = cdaRelatedCandidate.intent
  /\ cdaRelatedCandidate # CdaNoRelatedCandidate
  /\ cdaPreview = "ready"
  /\ savedGroupInput = cdaGroupInputStep
  /\ cdaRelatedCandidate.groupInputStep = savedGroupInput

CdaCloseProposalPreview ==
  /\ cdaPreview \in {"none", "ready", "error"}
  /\ cdaPreview' = "none"
  /\ cdaProposalIdPresent' = IF cdaPreview = "ready"
                              THEN FALSE ELSE cdaProposalIdPresent
  /\ cdaRowsPresent' = IF cdaPreview = "ready"
                       THEN FALSE ELSE cdaRowsPresent
  /\ cdaProposalPreviewOwner' = "NoAttempt"
  /\ cdaProposalPreviewCaptureOwner' = CdaNoPreviewOwner

CdaPreview(attempt, observedEpoch, ownerEpoch, captureId, intent,
           savedGroupInput, savedBefore, savedAfter) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch = cdaMountEpoch
  /\ ownerEpoch = observedEpoch
  /\ captureId \in CdaRequestOwnerTokens
  /\ intent \in {CdaNoGroupIntent} \cup
       CdaGroupIntentValues \cup CdaExpansionIntentValues
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedAfter = savedBefore
  /\ cdaPreview \in CdaPreviewStates
  /\ IF intent \in CdaExpansionIntentValues
     THEN /\ savedGroupInput = "None"
          /\ cdaGroupInputStep = "None"
          /\ cdaPersistedGroup = NoGroup
          /\ cdaRowExpansionCount \in 0..1
          /\ intent.beforeTable = cdaCurrentTable
          /\ intent.beforeTable = CdaExpansionBefore(intent.stage)
          /\ intent.afterTable = CdaExpansionAfter(intent.stage)
          /\ intent.stage = IF cdaRowExpansionCount = 0
                            THEN "SpecimenPatient"
                            ELSE "PatientObservation"
          /\ savedBefore = CdaExpansionBeforeSnapshot(intent.stage)
          /\ cdaExpansionCandidate' =
               [intent |-> intent,
                owner |-> CdaPreviewOwner(ownerEpoch, captureId)]
          /\ cdaGroupCandidate' = CdaNoGroupCandidate
     ELSE IF intent = CdaNoGroupIntent
          THEN /\ savedGroupInput = cdaGroupInputStep
               /\ cdaGroupCandidate' = CdaNoGroupCandidate
               /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
          ELSE /\ savedGroupInput = cdaGroupInputStep
               /\ IF intent.kind = "create"
                  THEN /\ cdaPersistedGroup = NoGroup
                       /\ intent = CdaCreateByIDCountRowsIntent
                       /\ savedGroupInput = "None"
                  ELSE /\ cdaRowExpansionCount = 2
                       /\ cdaCurrentTable = "Observation"
                       /\ savedGroupInput = "G1"
                       /\ IF intent = CdaEditByIDCountDistinctIntent
                          THEN cdaPersistedGroup = CdaByIDCountRowsGroup
                          ELSE IF intent = CdaEditByIDCountRowsIntent
                               THEN cdaPersistedGroup = CdaByIDCountDistinctGroup
                               ELSE IF intent = CdaUnchangedByIDCountRowsIntent
                                    THEN cdaPersistedGroup = CdaByIDCountRowsGroup
                                    ELSE /\ intent = CdaUnchangedByIDCountDistinctIntent
                                         /\ cdaPersistedGroup = CdaByIDCountDistinctGroup
               /\ cdaGroupCandidate' =
                    CdaGroupCandidateFromIntent(
                      intent, CdaPreviewOwner(ownerEpoch, captureId),
                      savedGroupInput,
                      IF savedGroupInput = "None"
                      THEN cdaCurrentTable ELSE cdaGroupInputTable)
               /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaPreview' = "pending"
  /\ cdaProposalPreviewOwner' = attempt
  /\ cdaProposalPreviewCaptureOwner' = CdaPreviewOwner(ownerEpoch, captureId)
  /\ cdaProposalIdPresent' = FALSE
  /\ cdaRowsPresent' = FALSE
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastAppliedGroupCandidate' = CdaNoAppliedGroupCandidate
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch,
                  cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                  cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
                  cdaLastCancelAfter, cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "Preview"

CdaPreviewComplete(attempt, observedEpoch, ownerEpoch, captureId) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch \in CdaMountEpochs
  /\ ownerEpoch \in CdaMountEpochs
  /\ captureId \in CdaRequestOwnerTokens
  /\ cdaPreview = "pending"
  /\ cdaProposalPreviewOwner = attempt
  /\ cdaProposalPreviewCaptureOwner = CdaPreviewOwner(ownerEpoch, captureId)
  /\ observedEpoch = cdaMountEpoch
  /\ ownerEpoch = observedEpoch
  /\ cdaPreview' = "ready"
  /\ cdaProposalIdPresent' = TRUE
  /\ cdaRowsPresent' = TRUE
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaProposalPreviewOwner,
                  cdaProposalPreviewCaptureOwner, cdaGroupCandidate,
                  cdaExpansionCandidate, cdaRelatedCandidate,
                  cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                  cdaLastAppliedGroupCandidate,
                  cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
                  cdaLastCancelAfter, cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "PreviewComplete"

CdaPreviewError(attempt, observedEpoch, ownerEpoch, captureId, hasProposalId) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch \in CdaMountEpochs
  /\ ownerEpoch \in CdaMountEpochs
  /\ captureId \in CdaRequestOwnerTokens
  /\ cdaPreview = "pending"
  /\ cdaProposalPreviewOwner = attempt
  /\ cdaProposalPreviewCaptureOwner = CdaPreviewOwner(ownerEpoch, captureId)
  /\ observedEpoch = cdaMountEpoch
  /\ ownerEpoch = observedEpoch
  /\ hasProposalId \in BOOLEAN
  /\ cdaPreview' = "error"
  /\ cdaProposalIdPresent' = hasProposalId
  /\ cdaRowsPresent' = FALSE
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaProposalPreviewOwner,
                  cdaProposalPreviewCaptureOwner, cdaGroupCandidate,
                  cdaExpansionCandidate, cdaRelatedCandidate,
                  cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                  cdaLastAppliedGroupCandidate,
                  cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
                  cdaLastCancelAfter, cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "PreviewError"

CdaPreviewRequestCancelled(attempt, observedEpoch, ownerEpoch, captureId) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch \in CdaMountEpochs
  /\ ownerEpoch \in CdaMountEpochs
  /\ captureId \in CdaRequestOwnerTokens
  /\ cdaPreview = "pending"
  /\ cdaProposalPreviewOwner = attempt
  /\ cdaProposalPreviewCaptureOwner = CdaPreviewOwner(ownerEpoch, captureId)
  /\ observedEpoch = cdaMountEpoch
  /\ ownerEpoch = observedEpoch
  /\ cdaPreview' = "none"
  /\ cdaProposalPreviewOwner' = "NoAttempt"
  /\ cdaProposalPreviewCaptureOwner' = CdaNoPreviewOwner
  /\ cdaProposalIdPresent' = FALSE
  /\ cdaRowsPresent' = FALSE
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaOutputToken, cdaDraftVersion,
                  cdaDraftDigest, cdaLastAppliedGroupCandidate,
                  cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
                  cdaLastCancelAfter, cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "PreviewRequestCancelled"

CdaLatePreviewCompleteIgnored(attempt, observedEpoch, ownerEpoch, captureId) ==
  LET settledOwner == CdaPreviewOwner(ownerEpoch, captureId)
  IN /\ attempt \in {"P1", "P2"}
     /\ observedEpoch \in CdaMountEpochs
     /\ ownerEpoch \in CdaMountEpochs
     /\ captureId \in CdaRequestOwnerTokens
     /\ observedEpoch = cdaMountEpoch
     /\ settledOwner # cdaProposalPreviewCaptureOwner
     /\ cdaLastIgnoredRequestOwner' = settledOwner
     /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                     cdaGroupInputTable, cdaMountedGroupInputTable,
                     cdaGroupInputStep, cdaMountedGroupInputStep,
                     cdaPersistedGroup, cdaMountedGroup,
                     cdaPersistedRelatedSource, cdaMountedRelatedSource,
                     cdaMountEpoch, cdaPreview, cdaProposalPreviewOwner,
                     cdaProposalPreviewCaptureOwner, cdaProposalIdPresent,
                     cdaRowsPresent, cdaGroupCandidate,
                     cdaExpansionCandidate, cdaRelatedCandidate,
                     cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                     cdaLastAppliedGroupCandidate,
                     cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
                     cdaLastCancelAfter,
                     cdaLastGroupEditRelatedBefore,
                     cdaLastGroupEditRelatedAfter>>
     /\ cdaLastAction' = "LatePreviewCompleteIgnored"

CdaLatePreviewErrorIgnored(attempt, observedEpoch, ownerEpoch, captureId) ==
  LET settledOwner == CdaPreviewOwner(ownerEpoch, captureId)
  IN /\ attempt \in {"P1", "P2"}
     /\ observedEpoch \in CdaMountEpochs
     /\ ownerEpoch \in CdaMountEpochs
     /\ captureId \in CdaRequestOwnerTokens
     /\ observedEpoch = cdaMountEpoch
     /\ settledOwner # cdaProposalPreviewCaptureOwner
     /\ cdaLastIgnoredRequestOwner' = settledOwner
     /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                     cdaGroupInputTable, cdaMountedGroupInputTable,
                     cdaGroupInputStep, cdaMountedGroupInputStep,
                     cdaPersistedGroup, cdaMountedGroup,
                     cdaPersistedRelatedSource, cdaMountedRelatedSource,
                     cdaMountEpoch, cdaPreview, cdaProposalPreviewOwner,
                     cdaProposalPreviewCaptureOwner, cdaProposalIdPresent,
                     cdaRowsPresent, cdaGroupCandidate,
                     cdaExpansionCandidate, cdaRelatedCandidate,
                     cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                     cdaLastAppliedGroupCandidate,
                     cdaLastAppliedRelatedCandidate, cdaLastCancelBefore,
                     cdaLastCancelAfter,
                     cdaLastGroupEditRelatedBefore,
                     cdaLastGroupEditRelatedAfter>>
     /\ cdaLastAction' = "LatePreviewErrorIgnored"

ExpandRows(attempt, observedEpoch, candidateOwner, stageToken,
           savedBeforeTable, savedAfterTable, savedBefore, savedAfter) ==
  /\ attempt \in {"P1", "P2"}
  /\ attempt = cdaProposalPreviewOwner
  /\ observedEpoch = cdaMountEpoch
  /\ candidateOwner = cdaExpansionCandidate.owner
  /\ candidateOwner = cdaProposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = cdaMountEpoch
  /\ cdaExpansionCandidate # CdaNoExpansionCandidate
  /\ cdaExpansionCandidate.intent.stage = stageToken
  /\ cdaPreview = "ready"
  /\ stageToken \in CdaRowExpansionStages
  /\ savedBeforeTable = cdaCurrentTable
  /\ savedBeforeTable = CdaExpansionBefore(stageToken)
  /\ savedAfterTable = CdaExpansionAfter(stageToken)
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedBefore = CdaExpansionBeforeSnapshot(stageToken)
  /\ savedAfter = CdaExpansionAfterSnapshot(stageToken)
  /\ cdaRowExpansionCount < 2
  /\ cdaCurrentTable' = savedAfterTable
  /\ cdaRowExpansionCount' = cdaRowExpansionCount + 1
  /\ cdaOutputToken' = savedAfter.outputToken
  /\ cdaDraftVersion' = savedAfter.draftVersion
  /\ cdaDraftDigest' = savedAfter.draftDigest
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastCancelBefore' = cdaLastCancelBefore
  /\ cdaLastCancelAfter' = cdaLastCancelAfter
  /\ CdaCloseProposalPreview
  /\ UNCHANGED <<cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaLastAppliedGroupCandidate,
                  cdaLastAppliedRelatedCandidate,
                  cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "ExpandRows"

CdaGroup(attempt, observedEpoch, candidateOwner, intent, savedGroupInput,
         savedBefore, savedAfter) ==
  /\ CdaCurrentGroupCandidate(attempt, observedEpoch, candidateOwner,
                              intent, savedGroupInput)
  /\ intent = CdaCreateByIDCountRowsIntent
  /\ savedGroupInput = "None"
  /\ cdaRowExpansionCount = 2
  /\ cdaCurrentTable = "Observation"
  /\ cdaGroupInputTable = "None"
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedBefore = CdaSnapshot2
  /\ savedAfter = CdaSnapshot3
  /\ savedBefore.relatedSource = savedAfter.relatedSource
  /\ cdaPersistedGroup' = savedAfter.group
  /\ cdaMountedGroup' = savedAfter.group
  /\ cdaGroupInputStep' = "G1"
  /\ cdaMountedGroupInputStep' = "G1"
  /\ cdaGroupInputTable' = "Observation"
  /\ cdaMountedGroupInputTable' = "Observation"
  /\ cdaOutputToken' = savedAfter.outputToken
  /\ cdaDraftVersion' = savedAfter.draftVersion
  /\ cdaDraftDigest' = savedAfter.draftDigest
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastAppliedGroupCandidate' =
       CdaAppliedGroupCandidate(intent, candidateOwner, savedGroupInput,
                                savedBefore, savedAfter)
  /\ cdaLastGroupEditRelatedBefore' = savedBefore.relatedSource
  /\ cdaLastGroupEditRelatedAfter' = savedAfter.relatedSource
  /\ CdaCloseProposalPreview
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaLastAppliedRelatedCandidate,
                  cdaLastCancelBefore, cdaLastCancelAfter,
                  cdaLastIgnoredRequestOwner>>
  /\ cdaLastAction' = "Group"

CdaEditGroup(attempt, observedEpoch, candidateOwner, intent,
             savedGroupInput, savedBefore, savedAfter) ==
  /\ CdaCurrentGroupCandidate(attempt, observedEpoch, candidateOwner,
                              intent, savedGroupInput)
  /\ intent = CdaEditByIDCountDistinctIntent
  /\ savedGroupInput = "G1"
  /\ cdaRowExpansionCount = 2
  /\ cdaPersistedGroup = CdaByIDCountRowsGroup
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedBefore = CdaSnapshot3
  /\ savedAfter = CdaSnapshot4
  /\ savedBefore.relatedSource = savedAfter.relatedSource
  /\ cdaPersistedGroup' = savedAfter.group
  /\ cdaMountedGroup' = savedAfter.group
  /\ cdaOutputToken' = savedAfter.outputToken
  /\ cdaDraftVersion' = savedAfter.draftVersion
  /\ cdaDraftDigest' = savedAfter.draftDigest
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastAppliedGroupCandidate' =
       CdaAppliedGroupCandidate(intent, candidateOwner, savedGroupInput,
                                savedBefore, savedAfter)
  /\ cdaLastGroupEditRelatedBefore' = savedBefore.relatedSource
  /\ cdaLastGroupEditRelatedAfter' = savedAfter.relatedSource
  /\ CdaCloseProposalPreview
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaLastAppliedRelatedCandidate,
                  cdaLastCancelBefore, cdaLastCancelAfter,
                  cdaLastIgnoredRequestOwner>>
  /\ cdaLastAction' = "EditGroup"

CdaEditGroupAgain(attempt, observedEpoch, candidateOwner, intent,
                  savedGroupInput, savedBefore, savedAfter) ==
  /\ CdaCurrentGroupCandidate(attempt, observedEpoch, candidateOwner,
                              intent, savedGroupInput)
  /\ intent = CdaEditByIDCountRowsIntent
  /\ savedGroupInput = "G1"
  /\ cdaRowExpansionCount = 2
  /\ cdaPersistedGroup = CdaByIDCountDistinctGroup
  /\ cdaPersistedRelatedSource = CdaObservationStatusRelatedSource
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedBefore = CdaSnapshot5
  /\ savedAfter = CdaSnapshot6
  /\ savedBefore.relatedSource = savedAfter.relatedSource
  /\ cdaPersistedGroup' = savedAfter.group
  /\ cdaMountedGroup' = savedAfter.group
  /\ cdaOutputToken' = savedAfter.outputToken
  /\ cdaDraftVersion' = savedAfter.draftVersion
  /\ cdaDraftDigest' = savedAfter.draftDigest
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastAppliedGroupCandidate' =
       CdaAppliedGroupCandidate(intent, candidateOwner, savedGroupInput,
                                savedBefore, savedAfter)
  /\ cdaLastGroupEditRelatedBefore' = savedBefore.relatedSource
  /\ cdaLastGroupEditRelatedAfter' = savedAfter.relatedSource
  /\ CdaCloseProposalPreview
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaLastAppliedRelatedCandidate,
                  cdaLastCancelBefore, cdaLastCancelAfter,
                  cdaLastIgnoredRequestOwner>>
  /\ cdaLastAction' = "EditGroupAgain"

CdaGroupEditCancel(observedEpoch, candidateOwner, savedGroupInput,
                   savedBefore, savedAfter) ==
  /\ observedEpoch = cdaMountEpoch
  /\ cdaGroupCandidate # CdaNoGroupCandidate
  /\ cdaGroupCandidate.intent.kind = "edit"
  /\ cdaGroupCandidate.groupInputStep = cdaGroupInputStep
  /\ candidateOwner = cdaGroupCandidate.owner
  /\ candidateOwner = cdaProposalPreviewCaptureOwner
  /\ candidateOwner.mountEpoch = observedEpoch
  /\ cdaProposalPreviewOwner \in {"P1", "P2"}
  /\ cdaPreview = "ready"
  /\ savedGroupInput = cdaGroupInputStep
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedAfter = savedBefore
  /\ CdaCloseProposalPreview
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastCancelBefore' = savedBefore
  /\ cdaLastCancelAfter' = savedAfter
  /\ cdaLastGroupEditRelatedBefore' = savedBefore.relatedSource
  /\ cdaLastGroupEditRelatedAfter' = savedAfter.relatedSource
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaLastAppliedGroupCandidate,
                  cdaLastAppliedRelatedCandidate,
                  cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                  cdaLastIgnoredRequestOwner>>
  /\ cdaLastAction' = "GroupEditCancel"

RelatedSourcePreview(attempt, observedEpoch, candidateOwner, intent,
                     savedGroupInput, savedBefore, savedAfter) ==
  /\ attempt \in {"P1", "P2"}
  /\ observedEpoch = cdaMountEpoch
  /\ candidateOwner.mountEpoch = observedEpoch
  /\ candidateOwner \in CdaPreviewOwners
  /\ candidateOwner.captureId \in CdaRequestOwnerTokens
  /\ intent \in CdaRelatedIntentValues
  /\ savedGroupInput = "G1"
  /\ savedGroupInput = cdaGroupInputStep
  /\ cdaGroupInputTable = "Observation"
  /\ cdaPersistedGroup # NoGroup
  /\ cdaPersistedGroup.stepId = "G1"
  /\ intent.groupInputStep = savedGroupInput
  /\ intent.base = cdaPersistedRelatedSource
  /\ (intent.kind = "add" => intent.targetStepId = "None")
  /\ (intent.kind \in {"remove", "keep"} =>
      intent.targetStepId = cdaPersistedRelatedSource.relatedStepToken)
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedAfter = savedBefore
  /\ IF intent.kind = "add"
     THEN /\ intent = CdaAddObservationStatusIntent
          /\ intent.targetStepId = "None"
          /\ cdaPersistedGroup.aggregate = "COUNT_DISTINCT"
          /\ savedBefore = CdaSnapshot4
     ELSE IF intent.kind = "remove"
          THEN /\ intent = CdaRemoveObservationStatusIntent
               /\ intent.targetStepId = "R1"
               /\ cdaPersistedGroup.aggregate = "COUNT_ROWS"
               /\ savedBefore = CdaSnapshot6
          ELSE /\ intent.kind = "keep"
               /\ cdaPersistedRelatedSource =
                    CdaObservationStatusRelatedSource
               /\ savedBefore \in {CdaSnapshot5, CdaSnapshot6}
  /\ cdaPreview' = "ready"
  /\ cdaProposalPreviewOwner' = attempt
  /\ cdaProposalPreviewCaptureOwner' = candidateOwner
  /\ cdaProposalIdPresent' = TRUE
  /\ cdaRowsPresent' = FALSE
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' =
       CdaRelatedCandidateFromIntent(intent, candidateOwner, attempt,
                                     savedGroupInput)
  /\ cdaLastAppliedRelatedCandidate' = CdaNoAppliedRelatedCandidate
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaOutputToken,
                  cdaDraftVersion, cdaDraftDigest,
                  cdaLastAppliedGroupCandidate,
                  cdaLastCancelBefore, cdaLastCancelAfter,
                  cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "RelatedSourcePreview"

RelatedSourceApply(attempt, observedEpoch, candidateOwner, intent,
                   savedGroupInput, savedBefore, savedAfter) ==
  /\ CdaCurrentRelatedCandidate(attempt, observedEpoch, candidateOwner,
                                intent, savedGroupInput)
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedBefore = IF intent.kind = "add"
                   THEN CdaSnapshot4 ELSE CdaSnapshot6
  /\ savedAfter = IF intent.kind = "add"
                  THEN CdaSnapshot5 ELSE CdaSnapshot7
  /\ intent.base = cdaPersistedRelatedSource
  /\ intent.proposed = savedAfter.relatedSource
  /\ savedBefore.group = savedAfter.group
  /\ savedBefore.group = cdaPersistedGroup
  /\ savedGroupInput = cdaGroupInputStep
  /\ savedGroupInput = cdaPersistedGroup.stepId
  /\ savedGroupInput = intent.groupInputStep
  /\ IF intent.kind = "add"
     THEN /\ intent = CdaAddObservationStatusIntent
          /\ cdaPersistedRelatedSource = CdaNoRelatedSource
          /\ savedAfter.relatedSource = CdaObservationStatusRelatedSource
          /\ savedAfter.group.aggregate = "COUNT_DISTINCT"
     ELSE /\ intent = CdaRemoveObservationStatusIntent
          /\ cdaPersistedRelatedSource = CdaObservationStatusRelatedSource
          /\ savedAfter.relatedSource = CdaNoRelatedSource
          /\ savedAfter.group.aggregate = "COUNT_ROWS"
  /\ cdaPersistedRelatedSource' = savedAfter.relatedSource
  /\ cdaMountedRelatedSource' = savedAfter.relatedSource
  /\ cdaOutputToken' = savedAfter.outputToken
  /\ cdaDraftVersion' = savedAfter.draftVersion
  /\ cdaDraftDigest' = savedAfter.draftDigest
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastAppliedRelatedCandidate' =
       CdaAppliedRelatedCandidate(intent, candidateOwner, attempt,
                                  savedGroupInput, savedBefore, savedAfter)
  /\ CdaCloseProposalPreview
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaMountEpoch, cdaGroupCandidate,
                  cdaExpansionCandidate, cdaLastAppliedGroupCandidate,
                  cdaLastCancelBefore, cdaLastCancelAfter,
                  cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "RelatedSourceApply"

RelatedSourceCancel(attempt, observedEpoch, candidateOwner, intent,
                    savedGroupInput, savedBefore, savedAfter) ==
  /\ CdaCurrentRelatedCandidate(attempt, observedEpoch, candidateOwner,
                                intent, savedGroupInput)
  /\ intent.base = cdaPersistedRelatedSource
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedAfter = savedBefore
  /\ savedGroupInput = cdaGroupInputStep
  /\ savedGroupInput = cdaPersistedGroup.stepId
  /\ CdaCloseProposalPreview
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaLastCancelBefore' = savedBefore
  /\ cdaLastCancelAfter' = savedAfter
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaMountedGroupInputTable,
                  cdaGroupInputStep, cdaMountedGroupInputStep,
                  cdaPersistedGroup, cdaMountedGroup,
                  cdaPersistedRelatedSource, cdaMountedRelatedSource,
                  cdaMountEpoch, cdaGroupCandidate,
                  cdaExpansionCandidate, cdaLastAppliedGroupCandidate,
                  cdaLastAppliedRelatedCandidate,
                  cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                  cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "RelatedSourceCancel"

CdaReload(observedEpoch, savedBefore, savedAfter,
          observedMountedGroup, observedMountedRelated) ==
  /\ cdaMountEpoch < CdaMaxMountEpoch
  /\ observedEpoch = cdaMountEpoch + 1
  /\ savedBefore = CdaCurrentSnapshot
  /\ savedAfter = savedBefore
  /\ observedMountedGroup = savedAfter.group
  /\ observedMountedRelated = savedAfter.relatedSource
  /\ cdaMountEpoch' = observedEpoch
  /\ cdaMountedGroup' = savedAfter.group
  /\ cdaMountedRelatedSource' = savedAfter.relatedSource
  /\ cdaMountedGroupInputStep' = cdaGroupInputStep
  /\ cdaMountedGroupInputTable' = cdaGroupInputTable
  /\ cdaGroupCandidate' = CdaNoGroupCandidate
  /\ cdaExpansionCandidate' = CdaNoExpansionCandidate
  /\ cdaRelatedCandidate' = CdaNoRelatedCandidate
  /\ cdaPreview' = "none"
  /\ cdaProposalPreviewOwner' = "NoAttempt"
  /\ cdaProposalPreviewCaptureOwner' = CdaNoPreviewOwner
  /\ cdaProposalIdPresent' = FALSE
  /\ cdaRowsPresent' = FALSE
  /\ cdaLastAppliedGroupCandidate' = CdaNoAppliedGroupCandidate
  /\ cdaLastAppliedRelatedCandidate' = CdaNoAppliedRelatedCandidate
  /\ cdaLastCancelBefore' = savedBefore
  /\ cdaLastCancelAfter' = savedAfter
  /\ UNCHANGED <<cdaCurrentTable, cdaRowExpansionCount,
                  cdaGroupInputTable, cdaGroupInputStep,
                  cdaPersistedGroup, cdaPersistedRelatedSource,
                  cdaOutputToken, cdaDraftVersion, cdaDraftDigest,
                  cdaLastIgnoredRequestOwner,
                  cdaLastGroupEditRelatedBefore,
                  cdaLastGroupEditRelatedAfter>>
  /\ cdaLastAction' = "Reload"

CdaStutter == UNCHANGED CdaVars

CdaNext ==
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in CdaMountEpochs,
       captureId \in CdaRequestOwnerTokens,
       intent \in {CdaNoGroupIntent} \cup
                    CdaGroupIntentValues \cup CdaExpansionIntentValues:
       CdaPreview(attempt, cdaMountEpoch, ownerEpoch, captureId, intent,
                  cdaGroupInputStep, CdaCurrentSnapshot, CdaCurrentSnapshot)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in CdaMountEpochs,
       captureId \in CdaRequestOwnerTokens:
       CdaPreviewComplete(attempt, cdaMountEpoch, ownerEpoch, captureId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in CdaMountEpochs,
       captureId \in CdaRequestOwnerTokens, hasProposalId \in BOOLEAN:
       CdaPreviewError(attempt, cdaMountEpoch, ownerEpoch, captureId,
                       hasProposalId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in CdaMountEpochs,
       captureId \in CdaRequestOwnerTokens:
       CdaPreviewRequestCancelled(attempt, cdaMountEpoch, ownerEpoch,
                                  captureId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in CdaMountEpochs,
       captureId \in CdaRequestOwnerTokens:
       CdaLatePreviewCompleteIgnored(attempt, cdaMountEpoch, ownerEpoch,
                                     captureId)
  \/ \E attempt \in {"P1", "P2"}, ownerEpoch \in CdaMountEpochs,
       captureId \in CdaRequestOwnerTokens:
       CdaLatePreviewErrorIgnored(attempt, cdaMountEpoch, ownerEpoch,
                                  captureId)
  \/ \E attempt \in {"P1", "P2"}, stageToken \in CdaRowExpansionStages:
       ExpandRows(attempt, cdaMountEpoch, cdaProposalPreviewCaptureOwner,
                  stageToken, cdaCurrentTable,
                  CdaExpansionAfter(stageToken), CdaCurrentSnapshot,
                  CdaExpansionAfterSnapshot(stageToken))
  \/ \E attempt \in {"P1", "P2"}:
       CdaGroup(attempt, cdaMountEpoch, cdaProposalPreviewCaptureOwner,
                CdaCreateByIDCountRowsIntent, "None",
                CdaCurrentSnapshot, CdaSnapshot3)
  \/ \E attempt \in {"P1", "P2"}:
       CdaEditGroup(attempt, cdaMountEpoch, cdaProposalPreviewCaptureOwner,
                    CdaEditByIDCountDistinctIntent, "G1",
                    CdaCurrentSnapshot, CdaSnapshot4)
  \/ \E attempt \in {"P1", "P2"}:
       CdaEditGroupAgain(attempt, cdaMountEpoch,
                         cdaProposalPreviewCaptureOwner,
                         CdaEditByIDCountRowsIntent, "G1",
                         CdaCurrentSnapshot, CdaSnapshot6)
  \/ CdaGroupEditCancel(cdaMountEpoch, cdaGroupCandidate.owner,
                        cdaGroupInputStep, CdaCurrentSnapshot,
                        CdaCurrentSnapshot)
  \/ \E attempt \in {"P1", "P2"}, captureId \in CdaRequestOwnerTokens,
       intent \in CdaRelatedIntentValues:
       RelatedSourcePreview(attempt, cdaMountEpoch,
                            CdaPreviewOwner(cdaMountEpoch, captureId),
                            intent, cdaGroupInputStep,
                            CdaCurrentSnapshot, CdaCurrentSnapshot)
  \/ \E attempt \in {"P1", "P2"}, intent \in CdaRelatedIntentValues:
       RelatedSourceApply(attempt, cdaMountEpoch,
                          cdaProposalPreviewCaptureOwner, intent,
                          cdaGroupInputStep, CdaCurrentSnapshot,
                          IF intent.kind = "add"
                          THEN CdaSnapshot5 ELSE CdaSnapshot7)
  \/ \E attempt \in {"P1", "P2"}, intent \in CdaRelatedIntentValues:
       RelatedSourceCancel(attempt, cdaMountEpoch,
                           cdaProposalPreviewCaptureOwner, intent,
                           cdaGroupInputStep, CdaCurrentSnapshot,
                           CdaCurrentSnapshot)
  \/ CdaReload(cdaMountEpoch + 1, CdaCurrentSnapshot,
               CdaCurrentSnapshot, cdaPersistedGroup,
               cdaPersistedRelatedSource)
  \/ CdaStutter

CdaWitnessCapture1 ==
  CHOOSE token \in CdaRequestOwnerTokens : TRUE
CdaWitnessCapture2 ==
  CHOOSE token \in CdaRequestOwnerTokens \ {CdaWitnessCapture1} : TRUE

CdaWitnessAction ==
  CASE cdaWitnessStage = 0 ->
         CdaPreview("P1", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture1, CdaSpecimenPatientIntent,
                    "None", CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 1 ->
         CdaPreviewComplete("P1", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture1)
    [] cdaWitnessStage = 2 ->
         ExpandRows("P1", cdaMountEpoch, cdaExpansionCandidate.owner,
                    "SpecimenPatient", cdaCurrentTable, "Patient",
                    CdaCurrentSnapshot, CdaSnapshot1)
    [] cdaWitnessStage = 3 ->
         CdaPreview("P2", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture2, CdaPatientObservationIntent,
                    "None", CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 4 ->
         CdaPreviewComplete("P2", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture2)
    [] cdaWitnessStage = 5 ->
         ExpandRows("P2", cdaMountEpoch, cdaExpansionCandidate.owner,
                    "PatientObservation", cdaCurrentTable, "Observation",
                    CdaCurrentSnapshot, CdaSnapshot2)
    [] cdaWitnessStage = 6 ->
         CdaPreview("P1", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture1, CdaCreateByIDCountRowsIntent,
                    "None", CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 7 ->
         CdaPreviewComplete("P1", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture1)
    [] cdaWitnessStage = 8 ->
         CdaGroup("P1", cdaMountEpoch, cdaProposalPreviewCaptureOwner,
                  CdaCreateByIDCountRowsIntent, "None",
                  CdaCurrentSnapshot, CdaSnapshot3)
    [] cdaWitnessStage = 9 ->
         CdaReload(cdaMountEpoch + 1, CdaCurrentSnapshot,
                   CdaCurrentSnapshot, cdaPersistedGroup,
                   cdaPersistedRelatedSource)
    [] cdaWitnessStage = 10 ->
         CdaPreview("P2", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture2,
                    CdaEditByIDCountDistinctIntent, "G1",
                    CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 11 ->
         CdaPreviewComplete("P2", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture2)
    [] cdaWitnessStage = 12 ->
         CdaGroupEditCancel(cdaMountEpoch, cdaGroupCandidate.owner,
                            cdaGroupInputStep, CdaCurrentSnapshot,
                            CdaCurrentSnapshot)
    [] cdaWitnessStage = 13 ->
         CdaPreview("P1", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture1,
                    CdaEditByIDCountDistinctIntent, "G1",
                    CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 14 ->
         CdaPreviewComplete("P1", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture1)
    [] cdaWitnessStage = 15 ->
         CdaEditGroup("P1", cdaMountEpoch,
                      cdaProposalPreviewCaptureOwner,
                      CdaEditByIDCountDistinctIntent, "G1",
                      CdaCurrentSnapshot, CdaSnapshot4)
    [] cdaWitnessStage = 16 ->
         CdaReload(cdaMountEpoch + 1, CdaCurrentSnapshot,
                   CdaCurrentSnapshot, cdaPersistedGroup,
                   cdaPersistedRelatedSource)
    [] cdaWitnessStage = 17 ->
         RelatedSourcePreview("P2", cdaMountEpoch,
                              CdaPreviewOwner(cdaMountEpoch,
                                              CdaWitnessCapture2),
                              CdaAddObservationStatusIntent, "G1",
                              CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 18 ->
         RelatedSourceCancel("P2", cdaMountEpoch,
                             cdaRelatedCandidate.owner,
                             CdaAddObservationStatusIntent, "G1",
                             CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 19 ->
         RelatedSourcePreview("P1", cdaMountEpoch,
                              CdaPreviewOwner(cdaMountEpoch,
                                              CdaWitnessCapture1),
                              CdaAddObservationStatusIntent, "G1",
                              CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 20 ->
         RelatedSourceApply("P1", cdaMountEpoch,
                            cdaRelatedCandidate.owner,
                            CdaAddObservationStatusIntent, "G1",
                            CdaCurrentSnapshot, CdaSnapshot5)
    [] cdaWitnessStage = 21 ->
         CdaReload(cdaMountEpoch + 1, CdaCurrentSnapshot,
                   CdaCurrentSnapshot, cdaPersistedGroup,
                   cdaPersistedRelatedSource)
    [] cdaWitnessStage = 22 ->
         CdaPreview("P2", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture2, CdaEditByIDCountRowsIntent,
                    "G1", CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 23 ->
         CdaPreviewComplete("P2", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture2)
    [] cdaWitnessStage = 24 ->
         CdaGroupEditCancel(cdaMountEpoch, cdaGroupCandidate.owner,
                            cdaGroupInputStep, CdaCurrentSnapshot,
                            CdaCurrentSnapshot)
    [] cdaWitnessStage = 25 ->
         CdaPreview("P1", cdaMountEpoch, cdaMountEpoch,
                    CdaWitnessCapture1, CdaEditByIDCountRowsIntent,
                    "G1", CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 26 ->
         CdaPreviewComplete("P1", cdaMountEpoch, cdaMountEpoch,
                            CdaWitnessCapture1)
    [] cdaWitnessStage = 27 ->
         CdaEditGroupAgain("P1", cdaMountEpoch,
                           cdaProposalPreviewCaptureOwner,
                           CdaEditByIDCountRowsIntent, "G1",
                           CdaCurrentSnapshot, CdaSnapshot6)
    [] cdaWitnessStage = 28 ->
         CdaReload(cdaMountEpoch + 1, CdaCurrentSnapshot,
                   CdaCurrentSnapshot, cdaPersistedGroup,
                   cdaPersistedRelatedSource)
    [] cdaWitnessStage = 29 ->
         RelatedSourcePreview("P2", cdaMountEpoch,
                              CdaPreviewOwner(cdaMountEpoch,
                                              CdaWitnessCapture2),
                              CdaRemoveObservationStatusIntent, "G1",
                              CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 30 ->
         RelatedSourceCancel("P2", cdaMountEpoch,
                             cdaRelatedCandidate.owner,
                             CdaRemoveObservationStatusIntent, "G1",
                             CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 31 ->
         RelatedSourcePreview("P1", cdaMountEpoch,
                              CdaPreviewOwner(cdaMountEpoch,
                                              CdaWitnessCapture1),
                              CdaRemoveObservationStatusIntent, "G1",
                              CdaCurrentSnapshot, CdaCurrentSnapshot)
    [] cdaWitnessStage = 32 ->
         RelatedSourceApply("P1", cdaMountEpoch,
                            cdaRelatedCandidate.owner,
                            CdaRemoveObservationStatusIntent, "G1",
                            CdaCurrentSnapshot, CdaSnapshot7)
    [] cdaWitnessStage = 33 ->
         CdaReload(cdaMountEpoch + 1, CdaCurrentSnapshot,
                   CdaCurrentSnapshot, cdaPersistedGroup,
                   cdaPersistedRelatedSource)

CdaWitnessNext ==
  \/ /\ cdaWitnessStage \in 0..33
     /\ CdaWitnessAction
     /\ cdaWitnessStage' = cdaWitnessStage + 1
     /\ UNCHANGED vars
  \/ /\ cdaWitnessStage = 34
     /\ UNCHANGED <<vars, CdaVars>>

CdaJourneyRestored ==
  /\ cdaLastAction = "Reload"
  /\ cdaMountEpoch = CdaMaxMountEpoch
  /\ cdaRowExpansionCount = 2
  /\ cdaCurrentTable = "Observation"
  /\ cdaPersistedGroup = CdaByIDCountRowsGroup
  /\ cdaMountedGroup = CdaByIDCountRowsGroup
  /\ cdaGroupInputTable = "Observation"
  /\ cdaMountedGroupInputTable = "Observation"
  /\ cdaGroupInputStep = "G1"
  /\ cdaMountedGroupInputStep = "G1"
  /\ cdaPersistedRelatedSource = CdaNoRelatedSource
  /\ cdaMountedRelatedSource = CdaNoRelatedSource

CdaJourneyEventuallyRestored == <>CdaJourneyRestored

CdaJourneyWitnessVars == <<vars, CdaVars>>
CdaJourneyWitnessSpec ==
  /\ Init
  /\ CdaInit
  /\ [][CdaWitnessNext]_CdaJourneyWitnessVars
  /\ WF_CdaJourneyWitnessVars(CdaWitnessNext)

CdaJourneyNext == CdaNext /\ UNCHANGED <<vars, cdaWitnessStage>>
CdaJourneySpec == Init /\ CdaInit /\ [][CdaJourneyNext]_<<vars, CdaVars>>

Spec == Init /\ CdaInit /\ [][Next /\ UNCHANGED CdaVars]_<<vars, CdaVars>>
GroupSafetySpec ==
  Init /\ CdaInit /\
  [][GroupSafetyNext /\ UNCHANGED CdaVars]_<<GroupSafetyVars, CdaVars>>

CdaTypeOK ==
  /\ cdaCurrentTable \in CdaDataTokens
  /\ cdaRowExpansionCount \in 0..2
  /\ cdaCurrentTable = CdaCurrentTableForExpansionCount(cdaRowExpansionCount)
  /\ cdaGroupInputTable \in CdaDataTokens \cup {"None"}
  /\ cdaMountedGroupInputTable \in CdaDataTokens \cup {"None"}
  /\ cdaGroupInputStep \in CdaGroupInputSteps
  /\ cdaMountedGroupInputStep \in CdaGroupInputSteps
  /\ cdaPersistedGroup \in CdaGroupRecords
  /\ CdaGroupRecordOK(cdaPersistedGroup)
  /\ cdaMountedGroup \in CdaGroupRecords
  /\ CdaGroupRecordOK(cdaMountedGroup)
  /\ cdaPersistedRelatedSource \in CdaRelatedSources
  /\ cdaMountedRelatedSource \in CdaRelatedSources
  /\ cdaMountEpoch \in CdaMountEpochs
  /\ cdaPreview \in CdaPreviewStates
  /\ cdaProposalPreviewOwner \in CdaProposalOwners
  /\ cdaProposalPreviewCaptureOwner \in CdaPreviewOwners
  /\ cdaProposalIdPresent \in BOOLEAN
  /\ cdaRowsPresent \in BOOLEAN
  /\ cdaGroupCandidate \in CdaGroupCandidates
  /\ cdaExpansionCandidate \in CdaExpansionCandidates
  /\ cdaRelatedCandidate \in CdaRelatedCandidates
  /\ cdaOutputToken \in CdaOutputTokens
  /\ cdaDraftVersion \in CdaDraftVersions
  /\ cdaDraftDigest \in CdaDraftDigests
  /\ CdaSnapshotOK(CdaCurrentSnapshot)
  /\ cdaLastAppliedGroupCandidate \in CdaAppliedGroupCandidates
  /\ cdaLastAppliedRelatedCandidate \in CdaAppliedRelatedCandidates
  /\ CdaSnapshotOK(cdaLastCancelBefore)
  /\ CdaSnapshotOK(cdaLastCancelAfter)
  /\ cdaLastIgnoredRequestOwner \in CdaPreviewOwners
  /\ cdaLastGroupEditRelatedBefore \in CdaRelatedSources
  /\ cdaLastGroupEditRelatedAfter \in CdaRelatedSources
  /\ cdaLastAction \in {"Init", "Preview", "PreviewComplete", "PreviewError",
                         "PreviewRequestCancelled", "LatePreviewCompleteIgnored",
                         "LatePreviewErrorIgnored", "ExpandRows", "Group",
                         "EditGroup", "EditGroupAgain", "GroupEditCancel",
                         "RelatedSourcePreview", "RelatedSourceApply",
                         "RelatedSourceCancel", "Reload"}
  /\ cdaWitnessStage \in 0..34

CdaGroupEditsPreserveRelatedBinding ==
  cdaLastAction \in {"EditGroup", "EditGroupAgain"}
  => /\ cdaLastAppliedGroupCandidate.intent.kind = "edit"
     /\ cdaLastAppliedGroupCandidate.before.relatedSource =
          cdaLastAppliedGroupCandidate.after.relatedSource
     /\ cdaPersistedRelatedSource =
          cdaLastAppliedGroupCandidate.after.relatedSource

CdaCanceledProposalsPreserveExactSavedStateAndCAS ==
  cdaLastAction \in {"GroupEditCancel", "RelatedSourceCancel"}
  => /\ cdaLastCancelBefore = cdaLastCancelAfter
     /\ cdaLastCancelBefore = CdaCurrentSnapshot
     /\ CdaCASOf(cdaLastCancelBefore) = CdaCASOf(cdaLastCancelAfter)

CdaRelatedApplyRequiresCurrentOwnerAndSavedGroupInput ==
  cdaLastAction = "RelatedSourceApply"
  => LET applied == cdaLastAppliedRelatedCandidate
     IN /\ applied.attempt \in {"P1", "P2"}
        /\ applied.owner.mountEpoch = cdaMountEpoch
        /\ applied.owner.captureId \in CdaRequestOwnerTokens
        /\ applied.groupInputStep = cdaGroupInputStep
        /\ applied.intent.groupInputStep = applied.groupInputStep
        /\ applied.before = CdaSnapshot4 \/ applied.before = CdaSnapshot6
        /\ applied.before.group = applied.after.group
        /\ applied.after = CdaCurrentSnapshot

CdaRelatedRemovalRestoresPriorGroupAndSourceState ==
  /\ (cdaLastAction = "RelatedSourceApply"
      /\ cdaLastAppliedRelatedCandidate.intent.kind = "remove")
     => /\ cdaLastAppliedRelatedCandidate.before.group =
            cdaLastAppliedRelatedCandidate.after.group
        /\ cdaLastAppliedRelatedCandidate.before.relatedSource =
            CdaObservationStatusRelatedSource
        /\ cdaLastAppliedRelatedCandidate.after.relatedSource =
            CdaNoRelatedSource
        /\ cdaPersistedGroup =
            cdaLastAppliedRelatedCandidate.before.group
        /\ cdaPersistedRelatedSource = CdaNoRelatedSource

CdaReloadRestoresPersistedGroupAndRelatedBinding ==
  cdaLastAction = "Reload"
  => /\ cdaMountedGroup = cdaPersistedGroup
     /\ cdaMountedRelatedSource = cdaPersistedRelatedSource
     /\ cdaMountedGroupInputStep = cdaGroupInputStep
     /\ cdaMountedGroupInputTable = cdaGroupInputTable

CdaApplyUsesCurrentCandidate ==
  cdaLastAction \in {"Group", "EditGroup", "EditGroupAgain"}
  => LET applied == cdaLastAppliedGroupCandidate
     IN /\ applied.owner.mountEpoch = cdaMountEpoch
        /\ applied.groupInputStep = cdaGroupInputStep \/ cdaLastAction = "Group"
        /\ applied.after = CdaCurrentSnapshot
        /\ cdaPersistedGroup = applied.after.group

CdaRowsOnlyAfterProposalPreview ==
  cdaRowsPresent => cdaPreview = "ready"

CdaOldOwnerCannotSettleCurrentPreview ==
  cdaLastAction \in {"LatePreviewCompleteIgnored", "LatePreviewErrorIgnored"}
  => cdaLastIgnoredRequestOwner # cdaProposalPreviewCaptureOwner

CdaJourneyStateMachine == CdaJourneySpec


=============================================================================
