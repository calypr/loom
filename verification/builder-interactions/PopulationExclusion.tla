-------------------------- MODULE PopulationExclusion --------------------------
EXTENDS Naturals, TLC
VARIABLES exclusionPhase, localBase, serverBase, localVersion, serverVersion,
  contextKind, hostAttached, changeKind, routeCalls, submitted
vars == <<exclusionPhase, localBase, serverBase, localVersion, serverVersion,
  contextKind, hostAttached, changeKind, routeCalls, submitted>>
CurrentIntent == hostAttached /\ contextKind = "same" /\ localBase = "A"
  /\ localVersion = serverVersion
Init == /\ exclusionPhase = "creation" /\ localBase = "A" /\ serverBase = "A"
  /\ localVersion = 1 /\ serverVersion = 1 /\ contextKind = "same"
  /\ hostAttached = TRUE /\ changeKind = "none" /\ routeCalls = 0 /\ submitted = FALSE
CreationReturns ==
 /\ exclusionPhase = "creation"
 /\ LET admitted == CurrentIntent IN
    /\ exclusionPhase' = IF admitted THEN "route-one" ELSE "done"
    /\ routeCalls' = IF admitted THEN 1 ELSE 0
 /\ UNCHANGED <<localBase, serverBase, localVersion, serverVersion, contextKind, hostAttached, changeKind, submitted>>
RoutePageReturns(found) ==
 /\ exclusionPhase \in {"route-one", "route-two"}
 /\ (exclusionPhase = "route-one" \/ found)
 /\ LET admitted == CurrentIntent IN
    /\ exclusionPhase' = IF ~admitted THEN "done" ELSE IF found THEN "drain" ELSE "route-two"
    /\ routeCalls' = IF admitted /\ ~found THEN 2 ELSE routeCalls
 /\ UNCHANGED <<localBase, serverBase, localVersion, serverVersion, contextKind, hostAttached, changeKind, submitted>>
DrainReturnsAndSubmit ==
 /\ exclusionPhase = "drain"
 /\ LET admitted == CurrentIntent IN
    /\ exclusionPhase' = "done" /\ submitted' = admitted
 /\ UNCHANGED <<localBase, serverBase, localVersion, serverVersion, contextKind, hostAttached, changeKind, routeCalls>>
CanChange == exclusionPhase # "done" /\ changeKind = "none"
ChangeBase ==
 /\ CanChange /\ changeKind' = "base" /\ localBase' = "B" /\ serverBase' = "B"
 /\ localVersion' = 2 /\ serverVersion' = 2
 /\ UNCHANGED <<exclusionPhase, contextKind, hostAttached, routeCalls, submitted>>
ChangeContext(kind) ==
 /\ CanChange /\ kind \in {"snapshot", "route", "client", "output", "root"}
 /\ changeKind' = "context" /\ contextKind' = kind
 /\ UNCHANGED <<exclusionPhase, localBase, serverBase, localVersion, serverVersion, hostAttached, routeCalls, submitted>>
DetachHost ==
 /\ CanChange /\ changeKind' = "detach" /\ hostAttached' = FALSE
 /\ UNCHANGED <<exclusionPhase, localBase, serverBase, localVersion, serverVersion, contextKind, routeCalls, submitted>>
UnrelatedSave ==
 /\ CanChange /\ changeKind' = "unrelated" /\ localVersion' = 2 /\ serverVersion' = 2
 /\ UNCHANGED <<exclusionPhase, localBase, serverBase, contextKind, hostAttached, routeCalls, submitted>>
BeginHydration ==
 /\ CanChange /\ changeKind' = "hydrating" /\ serverBase' = "B" /\ serverVersion' = 2
 /\ UNCHANGED <<exclusionPhase, localBase, localVersion, contextKind, hostAttached, routeCalls, submitted>>
FinishHydration ==
 /\ exclusionPhase # "done" /\ changeKind = "hydrating"
 /\ changeKind' = "hydrated" /\ localBase' = "B" /\ localVersion' = 2
 /\ UNCHANGED <<exclusionPhase, serverBase, serverVersion, contextKind, hostAttached, routeCalls, submitted>>
Next == CreationReturns \/ (\E found \in BOOLEAN : RoutePageReturns(found))
 \/ DrainReturnsAndSubmit \/ ChangeBase \/ (\E kind \in {"snapshot", "route", "client", "output", "root"} : ChangeContext(kind))
 \/ DetachHost \/ UnrelatedSave \/ BeginHydration \/ FinishHydration
Spec == Init /\ [][Next]_vars
TypeOK == /\ exclusionPhase \in {"creation", "route-one", "route-two", "drain", "done"}
 /\ localBase \in {"A", "B"} /\ serverBase \in {"A", "B"}
 /\ localVersion \in {1,2} /\ serverVersion \in {1,2}
 /\ contextKind \in {"same", "snapshot", "route", "client", "output", "root"}
 /\ hostAttached \in BOOLEAN /\ submitted \in BOOLEAN /\ routeCalls \in 0..2
 /\ changeKind \in {"none", "base", "context", "detach", "unrelated", "hydrating", "hydrated"}
Inv_ExclusionSubmissionUsesCurrentIntent == submitted => CurrentIntent /\ serverBase = "A"
Inv_CurrentExclusionSubmits == exclusionPhase = "done" /\ CurrentIntent /\ serverBase = "A" => submitted
=============================================================================
