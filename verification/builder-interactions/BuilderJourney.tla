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
  lastAction

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

Spec == Init /\ [][Next]_vars

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

GroupSafetySpec == Init /\ [][GroupSafetyNext]_GroupSafetyVars

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


=============================================================================
