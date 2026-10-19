---- MODULE Interpretation ----
EXTENDS Naturals, TLC

VARIABLES i, lastAction

vars == <<i, lastAction>>

TypeOK ==
  /\ i.createPhase \in {"idle", "blocked", "creating", "created", "failed"}
  /\ i.explanation \in {"", "x"}
  /\ i.libraryHead \in BOOLEAN
  /\ i.newLibraryId \in BOOLEAN
  /\ i.revision \in {"NONE", "V1"}
  /\ i.applied \in BOOLEAN
  /\ i.reviewPhase \in {"idle", "reviewing", "ready", "applied", "stale"}
  /\ i.reviewReceipt \in {"NONE", "RC1"}
  /\ i.reviewComplete \in BOOLEAN
  /\ i.reviewRevision \in {"NONE", "V1"}
  /\ i.appliedRevision \in {"NONE", "V1"}
  /\ i.appliedReceipt \in {"NONE", "RC1"}
  /\ i.outputColumn \in {"C1", "C2"}
  /\ i.appliedOutputColumn \in {"NONE", "C1", "C2"}
  /\ i.exactCoverage \in BOOLEAN
  /\ lastAction \in {
       "Init", "BeginCreate", "CreateBlocked", "CreateRevision",
       "CreateFailed", "CancelCreate", "ReviewCandidate", "ReviewReady",
       "ApplyCandidate", "ReviewStale"
     }

Init ==
  /\ i = [
       createPhase |-> "idle",
       explanation |-> "",
       libraryHead |-> FALSE,
       newLibraryId |-> FALSE,
       revision |-> "NONE",
       applied |-> FALSE,
       reviewPhase |-> "idle",
       reviewReceipt |-> "NONE",
       reviewComplete |-> FALSE,
       reviewRevision |-> "NONE",
       appliedRevision |-> "NONE",
       appliedReceipt |-> "NONE",
       outputColumn |-> "C1",
       appliedOutputColumn |-> "NONE",
       exactCoverage |-> FALSE
     ]
  /\ lastAction = "Init"

BeginCreate(explanation, libraryHead, newLibraryId) ==
  /\ explanation \in {"", "x"}
  /\ libraryHead \in BOOLEAN
  /\ newLibraryId \in BOOLEAN
  /\ explanation # ""
  /\ (libraryHead \/ newLibraryId)
  /\ i' = [i EXCEPT
       !.createPhase = "creating",
       !.explanation = explanation,
       !.libraryHead = libraryHead,
       !.newLibraryId = newLibraryId
     ]
  /\ lastAction' = "BeginCreate"

CreateBlocked ==
  /\ i' = [i EXCEPT !.createPhase = "blocked"]
  /\ lastAction' = "CreateBlocked"

CreateRevision(revision) ==
  /\ i.createPhase = "creating"
  /\ revision \in {"V1"}
  /\ i' = [i EXCEPT
       !.createPhase = "created",
       !.revision = revision,
       !.applied = FALSE
     ]
  /\ lastAction' = "CreateRevision"

CreateFailed ==
  /\ i.createPhase = "creating"
  /\ i' = [i EXCEPT !.createPhase = "failed"]
  /\ lastAction' = "CreateFailed"

CancelCreate ==
  /\ i.createPhase \in {"blocked", "creating", "created", "failed"}
  /\ i' = [i EXCEPT !.createPhase = "idle"]
  /\ lastAction' = "CancelCreate"

ReviewCandidate(receipt, complete) ==
  /\ i.revision # "NONE"
  /\ receipt \in {"RC1"}
  /\ complete \in BOOLEAN
  /\ i' = [i EXCEPT
       !.reviewPhase = "reviewing",
       !.reviewReceipt = receipt,
       !.reviewComplete = complete,
       !.reviewRevision = i.revision,
       !.exactCoverage = FALSE
     ]
  /\ lastAction' = "ReviewCandidate"

ReviewReady(receipt, complete) ==
  /\ i.reviewPhase = "reviewing"
  /\ receipt = i.reviewReceipt
  /\ complete = i.reviewComplete
  /\ i' = [i EXCEPT
       !.reviewPhase = "ready",
       !.exactCoverage = complete
     ]
  /\ lastAction' = "ReviewReady"

ApplyCandidate(receipt) ==
  /\ i.reviewPhase = "ready"
  /\ receipt = i.reviewReceipt
  /\ i.reviewRevision = i.revision
  /\ i' = [i EXCEPT
       !.reviewPhase = "applied",
       !.applied = TRUE,
       !.appliedRevision = i.reviewRevision,
       !.appliedReceipt = receipt,
       !.appliedOutputColumn = i.outputColumn
     ]
  /\ lastAction' = "ApplyCandidate"

ReviewStale ==
  /\ i.reviewPhase = "reviewing"
  /\ i' = [i EXCEPT
       !.reviewPhase = "stale",
       !.reviewReceipt = "NONE",
       !.exactCoverage = FALSE
     ]
  /\ lastAction' = "ReviewStale"

Next ==
  \/ \E explanation \in {"", "x"}, libraryHead \in BOOLEAN, newLibraryId \in BOOLEAN:
       BeginCreate(explanation, libraryHead, newLibraryId)
  \/ CreateBlocked
  \/ \E revision \in {"V1"}: CreateRevision(revision)
  \/ CreateFailed
  \/ CancelCreate
  \/ \E receipt \in {"RC1"}, complete \in BOOLEAN: ReviewCandidate(receipt, complete)
  \/ \E receipt \in {"RC1"}, complete \in BOOLEAN: ReviewReady(receipt, complete)
  \/ \E receipt \in {"RC1"}: ApplyCandidate(receipt)
  \/ ReviewStale

Spec == Init /\ [][Next]_vars

Inv_CreateRequiresExplanation ==
  i.createPhase = "creating" => i.explanation # ""

Inv_CreatedRevisionUnapplied ==
  lastAction = "CreateRevision" => ~i.applied

Inv_ReviewBoundToRevision ==
  i.reviewPhase \in {"reviewing", "ready", "applied"} =>
    i.reviewRevision = i.revision

Inv_ReviewReadyHasReceipt ==
  i.reviewPhase = "ready" => i.reviewReceipt # "NONE"

Inv_IncompleteReviewNotExact ==
  ~i.reviewComplete => ~i.exactCoverage

Inv_ApplyExactReceiptAndRevision ==
  lastAction = "ApplyCandidate" =>
    /\ i.appliedReceipt = i.reviewReceipt
    /\ i.appliedRevision = i.reviewRevision
    /\ i.appliedOutputColumn = i.outputColumn

Inv_StaleReviewCleared ==
  i.reviewPhase = "stale" => i.reviewReceipt = "NONE"

====
