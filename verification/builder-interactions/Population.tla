---- MODULE Population ----
EXTENDS Naturals, TLC

VARIABLES p, lastAction

vars == <<p, lastAction>>

TypeOK ==
  /\ p.attachPhase \in {"idle", "looking", "ready", "failed"}
  /\ p.selectedRevision \in {"NONE", "R1", "R2"}
  /\ p.attachedRevision \in {"NONE", "R0", "R1", "R2"}
  /\ p.lookupBaseAttached \in {"NONE", "R0", "R1", "R2"}
  /\ p.routeChoice \in {"NONE", "DIRECT", "ALT"}
  /\ p.routeSupported \in BOOLEAN
  /\ p.coveragePhase \in {"idle", "loading", "complete", "incomplete", "stale"}
  /\ p.coverageScope \in {"NONE", "S1", "S2"}
  /\ p.activeScope \in {"S1", "S2"}
  /\ p.coverageComplete \in BOOLEAN
  /\ p.excludePhase \in {"idle", "searching", "attached", "failed"}
  /\ p.excludedMember \in {"NONE", "M1"}
  /\ p.excludeRouteMatches \in BOOLEAN
  /\ p.baseAttached \in {"NONE", "R0", "R1", "R2"}
  /\ p.removalPhase \in {"idle", "previewing", "ready", "applied", "failed"}
  /\ p.removalMember \in {"NONE", "M1"}
  /\ p.openedReceipt \in {"NONE", "RP1"}
  /\ p.appliedReceipt \in {"NONE", "RP1"}
  /\ p.acceptedSelection \in {"R1", "R2"}
  /\ p.baseSelection \in {"R1", "R2"}
  /\ lastAction \in {
       "Init", "LookupRoutes", "AttachSelection", "ReplaceSelection",
       "ClearAttached", "RouteLookupFailed", "RequestCoverage",
       "CoverageComplete", "CoverageIncomplete", "CoverageStale",
       "ExcludeMember", "ExcludeRouteFound", "ExcludeRouteMissing",
       "ReviewRemoval", "RemovalReviewReady", "ApplyRemoval", "RemovalFailed"
     }

Init ==
  /\ p = [
       attachPhase |-> "idle",
       selectedRevision |-> "NONE",
       attachedRevision |-> "NONE",
       lookupBaseAttached |-> "NONE",
       routeChoice |-> "NONE",
       routeSupported |-> FALSE,
       coveragePhase |-> "idle",
       coverageScope |-> "NONE",
       activeScope |-> "S1",
       coverageComplete |-> FALSE,
       excludePhase |-> "idle",
       excludedMember |-> "NONE",
       excludeRouteMatches |-> FALSE,
       baseAttached |-> "NONE",
       removalPhase |-> "idle",
       removalMember |-> "NONE",
       openedReceipt |-> "NONE",
       appliedReceipt |-> "NONE",
       acceptedSelection |-> "R1",
       baseSelection |-> "R1"
     ]
  /\ lastAction = "Init"

LookupRoutes(selection, supported, choice) ==
  /\ selection \in {"R1", "R2"}
  /\ supported \in BOOLEAN
  /\ choice \in {"NONE", "DIRECT", "ALT"}
  /\ (supported <=> choice # "NONE")
  /\ p' = [p EXCEPT
       !.attachPhase = "ready",
       !.selectedRevision = selection,
       !.lookupBaseAttached = p.attachedRevision,
       !.routeSupported = supported,
       !.routeChoice = choice
     ]
  /\ lastAction' = "LookupRoutes"

AttachSelection(selection) ==
  /\ p.attachPhase = "ready"
  /\ p.routeSupported
  /\ selection = p.selectedRevision
  /\ p' = [p EXCEPT !.attachedRevision = selection, !.attachPhase = "idle"]
  /\ lastAction' = "AttachSelection"

ReplaceSelection(selection) ==
  /\ p.attachPhase = "ready"
  /\ p.routeSupported
  /\ selection = p.selectedRevision
  /\ p' = [p EXCEPT !.attachedRevision = selection, !.attachPhase = "idle"]
  /\ lastAction' = "ReplaceSelection"

ClearAttached ==
  /\ p' = [p EXCEPT !.attachedRevision = "NONE", !.attachPhase = "idle"]
  /\ lastAction' = "ClearAttached"

RouteLookupFailed ==
  /\ p.attachPhase = "looking"
  /\ p' = [p EXCEPT !.attachPhase = "failed"]
  /\ lastAction' = "RouteLookupFailed"

RequestCoverage(scope) ==
  /\ scope \in {"S1", "S2"}
  /\ p' = [p EXCEPT
       !.coveragePhase = "loading",
       !.coverageScope = scope,
       !.coverageComplete = FALSE
     ]
  /\ lastAction' = "RequestCoverage"

CoverageComplete(scope) ==
  /\ p.coveragePhase = "loading"
  /\ scope = p.activeScope
  /\ p' = [p EXCEPT
       !.coveragePhase = "complete",
       !.coverageScope = scope,
       !.coverageComplete = TRUE
     ]
  /\ lastAction' = "CoverageComplete"

CoverageIncomplete(scope) ==
  /\ p.coveragePhase = "loading"
  /\ scope = p.activeScope
  /\ p' = [p EXCEPT
       !.coveragePhase = "incomplete",
       !.coverageScope = scope,
       !.coverageComplete = FALSE
     ]
  /\ lastAction' = "CoverageIncomplete"

CoverageStale(scope) ==
  /\ p.coveragePhase = "loading"
  /\ scope # p.activeScope
  /\ p' = [p EXCEPT
       !.coveragePhase = "stale",
       !.coverageScope = scope,
       !.coverageComplete = FALSE
     ]
  /\ lastAction' = "CoverageStale"

ExcludeMember(member) ==
  /\ member \in {"M1"}
  /\ p' = [p EXCEPT
       !.excludePhase = "searching",
       !.excludedMember = member,
       !.excludeRouteMatches = FALSE,
       !.baseAttached = p.attachedRevision
     ]
  /\ lastAction' = "ExcludeMember"

ExcludeRouteFound ==
  /\ p.excludePhase = "searching"
  /\ p' = [p EXCEPT
       !.excludePhase = "attached",
       !.excludeRouteMatches = TRUE,
       !.attachedRevision = "R2"
     ]
  /\ lastAction' = "ExcludeRouteFound"

ExcludeRouteMissing ==
  /\ p.excludePhase = "searching"
  /\ p' = [p EXCEPT
       !.excludePhase = "failed",
       !.excludeRouteMatches = FALSE,
       !.attachedRevision = p.baseAttached
     ]
  /\ lastAction' = "ExcludeRouteMissing"

ReviewRemoval(member) ==
  /\ member \in {"M1"}
  /\ p' = [p EXCEPT
       !.removalPhase = "previewing",
       !.removalMember = member,
       !.openedReceipt = "NONE",
       !.appliedReceipt = "NONE",
       !.baseSelection = p.acceptedSelection
     ]
  /\ lastAction' = "ReviewRemoval"

RemovalReviewReady(receipt) ==
  /\ p.removalPhase = "previewing"
  /\ receipt \in {"RP1"}
  /\ p' = [p EXCEPT
       !.removalPhase = "ready",
       !.openedReceipt = receipt
     ]
  /\ lastAction' = "RemovalReviewReady"

ApplyRemoval(receipt) ==
  /\ p.removalPhase = "ready"
  /\ receipt = p.openedReceipt
  /\ p' = [p EXCEPT
       !.removalPhase = "applied",
       !.appliedReceipt = receipt,
       !.acceptedSelection = "R2"
     ]
  /\ lastAction' = "ApplyRemoval"

RemovalFailed ==
  /\ p.removalPhase = "ready"
  /\ p' = [p EXCEPT
       !.removalPhase = "failed",
       !.appliedReceipt = "NONE",
       !.acceptedSelection = p.baseSelection
     ]
  /\ lastAction' = "RemovalFailed"

Next ==
  \/ \E selection \in {"R1", "R2"}, supported \in BOOLEAN, choice \in {"NONE", "DIRECT", "ALT"}:
       LookupRoutes(selection, supported, choice)
  \/ \E selection \in {"R1", "R2"}: AttachSelection(selection)
  \/ \E selection \in {"R1", "R2"}: ReplaceSelection(selection)
  \/ ClearAttached
  \/ RouteLookupFailed
  \/ \E scope \in {"S1", "S2"}: RequestCoverage(scope)
  \/ \E scope \in {"S1", "S2"}: CoverageComplete(scope)
  \/ \E scope \in {"S1", "S2"}: CoverageIncomplete(scope)
  \/ \E scope \in {"S1", "S2"}: CoverageStale(scope)
  \/ \E member \in {"M1"}: ExcludeMember(member)
  \/ ExcludeRouteFound
  \/ ExcludeRouteMissing
  \/ \E member \in {"M1"}: ReviewRemoval(member)
  \/ \E receipt \in {"RP1"}: RemovalReviewReady(receipt)
  \/ \E receipt \in {"RP1"}: ApplyRemoval(receipt)
  \/ RemovalFailed

Spec == Init /\ [][Next]_vars

Inv_AttachReadyMatchesRoute ==
  p.attachPhase = "ready" => (p.routeSupported <=> p.routeChoice # "NONE")

Inv_LookupKeepsAttached ==
  lastAction = "LookupRoutes" => p.attachedRevision = p.lookupBaseAttached

Inv_ClearLeavesNoSelection ==
  lastAction = "ClearAttached" => p.attachedRevision = "NONE"

Inv_CoverageCompleteMatchesScope ==
  p.coveragePhase = "complete" =>
    /\ p.coverageScope = p.activeScope
    /\ p.coverageComplete

Inv_CoverageStaleMismatch ==
  p.coveragePhase = "stale" => p.coverageScope # p.activeScope

Inv_CoverageIncompleteIsNotComplete ==
  p.coveragePhase = "incomplete" => ~p.coverageComplete

Inv_ExcludeAttachRequiresRoute ==
  p.excludePhase = "attached" => p.excludeRouteMatches

Inv_ExcludeFailureKeepsCollection ==
  lastAction = "ExcludeRouteMissing" => p.attachedRevision = p.baseAttached

Inv_RemovalApplyRequiresExactReceipt ==
  lastAction = "ApplyRemoval" =>
    /\ p.appliedReceipt = p.openedReceipt
    /\ p.appliedReceipt # "NONE"

Inv_RemovalFailureKeepsCollection ==
  lastAction = "RemovalFailed" => p.acceptedSelection = p.baseSelection

Inv_RemovalAppliedHasReceipt ==
  p.removalPhase = "applied" => p.appliedReceipt # "NONE"

====
