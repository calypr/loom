---- MODULE RowDefinitionLifecycle ----
EXTENDS Naturals, TLC

CONSTANT RowKeys
CONSTANT BaseKey
CONSTANT MaxVersion

VARIABLES
  selectedKey, selectionVersion,
  proposalPhase, proposalKey, proposalVersion, proposalAvailable,
  appliedKey, lastAction

vars == <<
  selectedKey, selectionVersion,
  proposalPhase, proposalKey, proposalVersion, proposalAvailable,
  appliedKey, lastAction
>>

TypeOK ==
  /\ selectedKey \in RowKeys \union {BaseKey}
  /\ selectionVersion \in 0..MaxVersion
  /\ proposalPhase \in {"idle", "loading", "ready", "failed"}
  /\ proposalKey \in RowKeys \union {"NONE"}
  /\ proposalVersion \in 0..MaxVersion
  /\ proposalAvailable \in BOOLEAN
  /\ appliedKey \in RowKeys \union {"NONE"}
  /\ lastAction \in {
       "Init", "SelectRow", "RequestProposal", "ProposalComplete",
       "ProposalFailed", "CancelProposal", "ApplyProposal"
     }

Init ==
  /\ selectedKey = BaseKey
  /\ selectionVersion = 0
  /\ proposalPhase = "idle"
  /\ proposalKey = "NONE"
  /\ proposalVersion = 0
  /\ proposalAvailable = FALSE
  /\ appliedKey = "NONE"
  /\ lastAction = "Init"

SelectRow(key) ==
  /\ key \in RowKeys
  /\ selectedKey' = key
  /\ selectionVersion' =
       IF selectionVersion < MaxVersion THEN selectionVersion + 1 ELSE selectionVersion
  /\ proposalPhase' = "idle"
  /\ proposalAvailable' = FALSE
  /\ lastAction' = "SelectRow"
  /\ UNCHANGED <<proposalKey, proposalVersion, appliedKey>>

RequestProposal ==
  /\ selectedKey \in RowKeys
  /\ proposalPhase \in {"idle", "failed"}
  /\ proposalPhase' = "loading"
  /\ proposalKey' = selectedKey
  /\ proposalVersion' = selectionVersion
  /\ proposalAvailable' = FALSE
  /\ lastAction' = "RequestProposal"
  /\ UNCHANGED <<selectedKey, selectionVersion, appliedKey>>

ProposalComplete ==
  /\ proposalPhase = "loading"
  /\ proposalVersion = selectionVersion
  /\ proposalKey = selectedKey
  /\ proposalPhase' = "ready"
  /\ proposalAvailable' = TRUE
  /\ lastAction' = "ProposalComplete"
  /\ UNCHANGED <<selectedKey, selectionVersion, proposalKey, proposalVersion, appliedKey>>

ProposalFailed ==
  /\ proposalPhase = "loading"
  /\ proposalPhase' = "failed"
  /\ proposalAvailable' = FALSE
  /\ lastAction' = "ProposalFailed"
  /\ UNCHANGED <<selectedKey, selectionVersion, proposalKey, proposalVersion, appliedKey>>

CancelProposal ==
  /\ proposalPhase \in {"loading", "ready", "failed"}
  /\ proposalPhase' = "idle"
  /\ proposalAvailable' = FALSE
  /\ lastAction' = "CancelProposal"
  /\ UNCHANGED <<selectedKey, selectionVersion, proposalKey, proposalVersion, appliedKey>>

ApplyProposal ==
  /\ proposalPhase = "ready"
  /\ proposalAvailable = TRUE
  /\ proposalVersion = selectionVersion
  /\ proposalKey = selectedKey
  /\ appliedKey' = proposalKey
  /\ proposalPhase' = "idle"
  /\ proposalAvailable' = FALSE
  /\ lastAction' = "ApplyProposal"
  /\ UNCHANGED <<selectedKey, selectionVersion, proposalKey, proposalVersion>>

Next ==
  \/ \E key \in RowKeys: SelectRow(key)
  \/ RequestProposal
  \/ ProposalComplete
  \/ ProposalFailed
  \/ CancelProposal
  \/ ApplyProposal

Spec == Init /\ [][Next]_vars

Inv_ReadyProposalMatchesSelection ==
  proposalPhase = "ready" =>
    /\ proposalKey = selectedKey
    /\ proposalVersion = selectionVersion
    /\ proposalAvailable

Inv_AppliedProposalMatchesSelection ==
  lastAction = "ApplyProposal" => appliedKey = selectedKey

Inv_SelectionVersionBounds ==
  selectionVersion \in 0..MaxVersion

Inv_ProposalVersionBounds ==
  proposalVersion \in 0..MaxVersion

====
