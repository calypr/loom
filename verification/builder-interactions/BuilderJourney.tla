---------------- MODULE BuilderJourney ----------------
\* Navigation supplies the ready source-table preview. Proposal preview and
\* applied operation lifecycle stay separate from that rendered table state.
\* Data-shape correctness remains in BuilderOperations.tla and the browser
\* fixture oracle carried by each trace.
EXTENDS Naturals, Sequences, TLC

MaxReshapes == 2

Phases == {"idle", "selected", "reshaping"}
PreviewStates == {"none", "pending", "ready", "error"}
ProposalOwners == {"P1", "P2", "NoAttempt"}
ChoicePreviewStates == {"none", "pending", "ready", "error"}
TablePreviewStates == {"ready", "error"}
JourneyActions == {"Init", "Group", "GroupAgain", "AddColumn",
                   "CancelFilter", "EditGroup", "EditGroupAgain", "RemoveGroup", "Pivot", "Preview",
                   "PreviewComplete", "PreviewError", "LatePreviewCompleteIgnored",
                   "LatePreviewErrorIgnored", "PreviewRequestCancelled", "ChoicePreview", "ChoicePreviewComplete",
                   "ChoicePreviewError"}

VARIABLES
  phase,
  reshapeCount,
  pivotReady,
  preview,
  proposalPreviewOwner,
  choicePreview,
  choicePreviewOwner,
  proposalIdPresent,
  rowsPresent,
  tablePreview,
  sourceRowsPresent,
  lastAction

vars == <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner, choicePreview,
          choicePreviewOwner, proposalIdPresent,
          rowsPresent, tablePreview, sourceRowsPresent, lastAction>>

Init ==
  /\ phase = "selected"
  /\ reshapeCount = 0
  /\ pivotReady = FALSE
  /\ preview = "none"
  /\ proposalPreviewOwner = "NoAttempt"
  /\ choicePreview = "none"
  /\ choicePreviewOwner = "NoAttempt"
  /\ proposalIdPresent = FALSE
  /\ rowsPresent = FALSE
  /\ tablePreview = "ready"
  /\ sourceRowsPresent = TRUE
  /\ lastAction = "Init"

CloseProposalPreview ==
  /\ preview \in {"none", "ready"}
  /\ preview' = "none"
  /\ proposalIdPresent' = IF preview = "ready" THEN FALSE ELSE proposalIdPresent
  /\ rowsPresent' = IF preview = "ready" THEN FALSE ELSE rowsPresent
  /\ UNCHANGED proposalPreviewOwner

Group ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ CloseProposalPreview
  /\ UNCHANGED <<pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "Group"

GroupAgain ==
  /\ phase = "reshaping"
  /\ ~pivotReady
  /\ reshapeCount = 1
  /\ reshapeCount' = 2
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "GroupAgain"

AddColumn ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ choicePreview = "ready"
  /\ choicePreview' = "none"
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "AddColumn"

CancelFilter ==
  /\ preview \in {"ready", "error"}
  /\ preview' = "none"
  /\ proposalIdPresent' = FALSE
  /\ rowsPresent' = FALSE
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "CancelFilter"

EditGroup ==
  /\ phase = "reshaping"
  /\ reshapeCount = 1
  /\ ~pivotReady
  /\ reshapeCount' = 2
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "EditGroup"

EditGroupAgain ==
  /\ phase = "reshaping"
  /\ reshapeCount = MaxReshapes
  /\ ~pivotReady
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "EditGroupAgain"

RemoveGroup ==
  /\ phase = "reshaping"
  /\ reshapeCount >= 1
  /\ ~pivotReady
  /\ phase' = "selected"
  /\ reshapeCount' = 0
  /\ CloseProposalPreview
  /\ UNCHANGED <<pivotReady, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "RemoveGroup"

Pivot ==
  /\ phase = "reshaping"
  /\ reshapeCount = MaxReshapes
  /\ ~pivotReady
  /\ pivotReady' = TRUE
  /\ CloseProposalPreview
  /\ UNCHANGED <<phase, reshapeCount, choicePreview, choicePreviewOwner, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "Pivot"

Preview(attempt) ==
  \* The candidate preview request precedes its command apply; the source
  \* table's automatic navigation preview is represented by tablePreview.
  /\ attempt \in {"P1", "P2"}
  /\ phase \in {"selected", "reshaping"}
  /\ preview \in PreviewStates
  /\ preview' = "pending"
  /\ proposalPreviewOwner' = attempt
  /\ proposalIdPresent' = FALSE
  /\ rowsPresent' = FALSE
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "Preview"

PreviewComplete(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ preview = "pending"
  /\ proposalPreviewOwner = attempt
  /\ preview' = "ready"
  /\ proposalIdPresent' = TRUE
  /\ rowsPresent' = TRUE
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "PreviewComplete"

PreviewError(attempt, hasProposalId) ==
  /\ attempt \in {"P1", "P2"}
  /\ preview = "pending"
  /\ proposalPreviewOwner = attempt
  /\ hasProposalId \in BOOLEAN
  /\ preview' = "error"
  /\ proposalIdPresent' = hasProposalId
  /\ rowsPresent' = FALSE
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  tablePreview, sourceRowsPresent>>
  /\ lastAction' = "PreviewError"

PreviewRequestCancelled(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ preview = "pending"
  /\ proposalPreviewOwner = attempt
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
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "ChoicePreview"

ChoicePreviewComplete(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ choicePreview = "pending"
  /\ choicePreviewOwner = attempt
  /\ choicePreview' = "ready"
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  choicePreviewOwner, proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "ChoicePreviewComplete"

ChoicePreviewError(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ choicePreview = "pending"
  /\ choicePreviewOwner = attempt
  /\ choicePreview' = "error"
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner,
                  choicePreviewOwner, proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "ChoicePreviewError"

LatePreviewCompleteIgnored(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ proposalPreviewOwner \in {"P1", "P2"}
  /\ proposalPreviewOwner # attempt
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "LatePreviewCompleteIgnored"

LatePreviewErrorIgnored(attempt) ==
  /\ attempt \in {"P1", "P2"}
  /\ proposalPreviewOwner \in {"P1", "P2"}
  /\ proposalPreviewOwner # attempt
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, preview, proposalPreviewOwner, choicePreview, choicePreviewOwner,
                  proposalIdPresent, rowsPresent, tablePreview, sourceRowsPresent>>
  /\ lastAction' = "LatePreviewErrorIgnored"

Stutter ==
  UNCHANGED vars

Next ==
  \/ Group
  \/ GroupAgain
  \/ AddColumn
  \/ CancelFilter
  \/ EditGroup
  \/ EditGroupAgain
  \/ RemoveGroup
  \/ Pivot
  \/ \E attempt \in {"P1", "P2"}: Preview(attempt)
  \/ \E attempt \in {"P1", "P2"}: PreviewComplete(attempt)
  \/ \E attempt \in {"P1", "P2"}, hasProposalId \in BOOLEAN: PreviewError(attempt, hasProposalId)
  \/ \E attempt \in {"P1", "P2"}: PreviewRequestCancelled(attempt)
  \/ \E attempt \in {"P1", "P2"}: LatePreviewCompleteIgnored(attempt)
  \/ \E attempt \in {"P1", "P2"}: LatePreviewErrorIgnored(attempt)
  \/ \E attempt \in {"P1", "P2"}: ChoicePreview(attempt)
  \/ \E attempt \in {"P1", "P2"}: ChoicePreviewComplete(attempt)
  \/ \E attempt \in {"P1", "P2"}: ChoicePreviewError(attempt)
  \/ Stutter

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ phase \in Phases
  /\ reshapeCount \in 0..MaxReshapes
  /\ pivotReady \in BOOLEAN
  /\ preview \in PreviewStates
  /\ proposalPreviewOwner \in ProposalOwners
  /\ choicePreview \in ChoicePreviewStates
  /\ choicePreviewOwner \in ProposalOwners
  /\ proposalIdPresent \in BOOLEAN
  /\ rowsPresent \in BOOLEAN
  /\ tablePreview \in TablePreviewStates
  /\ sourceRowsPresent \in BOOLEAN
  /\ lastAction \in JourneyActions

PivotRequiresReshapeOrDirect ==
  pivotReady => reshapeCount = MaxReshapes

RowsOnlyAfterProposalPreview ==
  rowsPresent => preview = "ready"

SourceRowsRequireReadyTablePreview ==
  sourceRowsPresent => tablePreview = "ready"

=============================================================================
