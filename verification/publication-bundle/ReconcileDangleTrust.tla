------------------------ MODULE ReconcileDangleTrust ------------------------
(*
Formal control for F-B12 (CONFIRMED-RELEASE-DANGLE). One candidate execution
ecand starts RUNNING with an expired publisher lease; the reconciler observes
it (snapshot RUNNING), reads the pointer guard while PublishExecution's
pointer-last doc is still unwritten, then the publish lands and a release
activates binding the (then-queryable) execution. The cleanup then drops the
physical table and FAILED-demotes the bound execution with no re-check:
clickhouse/bundle_store.go:1068-1073 guard observed ABSENT pointer, :1084
DropTable, :1089/:1097 demote; release reads resolve via GetExecution
(published/current.go:95) and one broken binding aborts everything (:206-208,
F-B7). Activations never read-pin (export.go:135 and read_pins.go:63 are the
only read-pin callers).

Fix-tier constants (each alone should restore the corresponding guarantee):
  PinAtActivation   - release create/activate acquires a read-pin on every
                      bound execution; cleanup refuses pinned executions
                      (ClaimExecutionCleanup already refuses active pins).
  RecheckBeforeDrop - cleanup re-reads pointer+state immediately before the
                      DropTable/demote step and refuses PUBLISHED/pointer-
                      named executions.
  FixedReader       - CurrentProjectDatasets degrades only the broken binding
                      (per-selector isolation already holds, F-B7 E5a/E5c)
                      instead of aborting the whole list.

SOURCE SYNC (round 26 repair): PublishExecution now commits both docs inside
one Arango transaction (bundle_registry.go WithTransaction over
executions+pointers+leases) => AtomicPublish = TRUE is the shipped shape.
m-dangle-bug keeps AtomicPublish = FALSE as the pre-fix (dc9016bc) fault
control; m-dangle-atomic-current pins the shipped shape, and its residual
violation is precisely why RecheckBeforeDrop (Repair-1b) is still required:
the reconciler's pointer guard and the drop remain separate round trips, so a
publish that lands entirely after the guard read is dropped with its tables
while an ACTIVE release binds it.

SOURCE SYNC (round 29 repair): RecheckBeforeDrop SHIPPED. ClickHouseBundleStore
reconcileExecution and clickHouseBundleTx.Abort now re-read the pointer after
the cleanup claim and veto when it names the candidate, veto when any binding
of the ACTIVE release names it (superset of this model's PUBLISHED/pointer-
named refusal; also consults ActiveRelease bindings), and veto fail-closed on
any other read failure. m-dangle-atomic-recheck (RecheckBeforeDrop = TRUE) is
the validated implemented shape: CLEAN. Production wiring supplies the reader
(server.go SetActiveReleaseReader); the m-dangle-atomic-current witness stays
as the pre-Repair-1b fault control.
*)

EXTENDS Naturals, FiniteSets

CONSTANT EXEC,                 \* {ecand, ehealthy}
         ecand,                \* the racing publish candidate
         AtomicPublish,        \*_BOOLEAN -- TRUE models the r26 repaired
                               \* PublishExecution (both legs inside one Arango
                               \* transaction, bundle_registry.go WithTransaction);
                               \* FALSE models the pre-fix two-doc publish.
         PinAtActivation,      \*_BOOLEAN
         RecheckBeforeDrop,    \*_BOOLEAN
         FixedReader           \*_BOOLEAN

ASSUME Wellformed ==
  /\ EXEC # {}
  /\ ecand \in EXEC
  /\ AtomicPublish \in BOOLEAN
  /\ PinAtActivation \in BOOLEAN
  /\ RecheckBeforeDrop \in BOOLEAN
  /\ FixedReader \in BOOLEAN

CONSTANT RUNNING, PUBLISHED, FAILED   \* model-state values

VARIABLES state,              \* [EXEC -> {RUNNING,PUBLISHED,FAILED}]
          tableOK,            \* [EXEC -> BOOLEAN] physical table presence
          pointerWritten,     \* [EXEC -> BOOLEAN] pointer doc (last doc of publish)
          candidates,         \* SUBSET EXEC reconciler snapshotted while RUNNING
          checkedAbsent,      \* SUBSET EXEC whose pointer guard read came back ABSENT
          activated           \* release Created+Activated (binds ALL of EXEC)

TypeOK ==
  /\ state \in [EXEC -> {RUNNING, PUBLISHED, FAILED}]
  /\ tableOK \in [EXEC -> BOOLEAN]
  /\ pointerWritten \in [EXEC -> BOOLEAN]
  /\ candidates \subseteq EXEC
  /\ checkedAbsent \subseteq candidates
  /\ activated \in BOOLEAN

Init ==
  /\ state = [x \in EXEC |-> IF x = ecand THEN RUNNING ELSE PUBLISHED]
  /\ tableOK = [x \in EXEC |-> TRUE]
  /\ pointerWritten = [x \in EXEC |-> x # ecand]
  /\ candidates = {}
  /\ checkedAbsent = {}
  /\ activated = FALSE

\* PublishExecution doc 1: execution becomes queryable PUBLISHED
\* (release verifier's own checks pass on this doc alone; pointer not yet set).
\* Only reachable while the publish is NOT transactional (pre-fix dc9016bc).
PublishExec ==
  /\ ~AtomicPublish
  /\ state[ecand] = RUNNING
  /\ state' = [state EXCEPT ![ecand] = PUBLISHED]
  /\ UNCHANGED <<tableOK, pointerWritten, candidates, checkedAbsent, activated>>

\* PublishExecution doc 2: pointer written LAST (two-doc non-atomicity).
PublishPointer ==
  /\ ~AtomicPublish
  /\ state[ecand] = PUBLISHED
  /\ ~pointerWritten[ecand]
  /\ pointerWritten' = [pointerWritten EXCEPT ![ecand] = TRUE]
  /\ UNCHANGED <<state, tableOK, candidates, checkedAbsent, activated>>

\* Repaired (round 26) PublishExecution: both docs land indivisibly inside one
\* Arango transaction. There is NO intermediate PUBLISHED-without-pointer state.
\* A publish landing entirely AFTER the guard read still loses to the stale
\* one-shot guard (guard read bundle_store.go:1068; drop loop :1082-1088 with
\* no pointer/release re-read), but a LEASE-FENCED producer cannot be the racer
\* there (the reconciler holds the lease from :1057, and the publish AQL
\* requires lease.ownerId == publisher), so this arm is a FAULT-INJECTION /
\* LEGACY-DATA control for the defense-in-depth value of RecheckBeforeDrop.
\* Identified non-lease-guarded writers of PUBLISHED state (F-B13
\* activateReleaseAQL patch, F-B6 selector-bound rows) keep the drop path
\* reachable for that class.
PublishBoth ==
  /\ AtomicPublish
  /\ state[ecand] = RUNNING
  /\ ~pointerWritten[ecand]
  /\ state' = [state EXCEPT ![ecand] = PUBLISHED]
  /\ pointerWritten' = [pointerWritten EXCEPT ![ecand] = TRUE]
  /\ UNCHANGED <<tableOK, candidates, checkedAbsent, activated>>

\* Reconcile enumerate: captures the still-RUNNING candidate (expired lease).
Observe ==
  /\ state[ecand] = RUNNING
  /\ ecand \notin candidates
  /\ candidates' = candidates \cup {ecand}
  /\ UNCHANGED <<state, tableOK, pointerWritten, checkedAbsent, activated>>

\* reconcileExecution pointer guard observes the pointer ABSENT
\* (bundle_store.go:1068-1073 saw ErrBundleNotFound in the window).
CheckPointerAbsent ==
  /\ ecand \in candidates
  /\ ~pointerWritten[ecand]
  /\ ecand \notin checkedAbsent
  /\ checkedAbsent' = checkedAbsent \cup {ecand}
  /\ UNCHANGED <<state, tableOK, pointerWritten, candidates, activated>>

\* Release Create+Activate: binds every execution that looks queryable now;
\* never read-pins today (PinAtActivation models the proposed fix).
Activate ==
  /\ ~activated
  /\ state[ecand] = PUBLISHED
  /\ tableOK[ecand]
  /\ activated' = TRUE
  /\ UNCHANGED <<state, tableOK, pointerWritten, candidates, checkedAbsent>>

\* The cleanup: guarded only by the stale snapshot and the stale guard
\* observation. PinAtActivation refuses (pinned); RecheckBeforeDrop refuses a
\* now-PUBLISHED/pointer-named exec; else drop + demote proceed.
DropDemote ==
  /\ ecand \in candidates
  /\ ecand \in checkedAbsent
  /\ ~ (PinAtActivation /\ activated)
  /\ ~ (RecheckBeforeDrop /\ (state[ecand] = PUBLISHED \/ pointerWritten[ecand]))
  /\ state' = [state EXCEPT ![ecand] = FAILED]
  /\ tableOK' = [tableOK EXCEPT ![ecand] = FALSE]
  /\ UNCHANGED <<pointerWritten, candidates, checkedAbsent, activated>>

vars == <<state, tableOK, pointerWritten, candidates, checkedAbsent, activated>>

Next == \/ PublishExec
        \/ PublishPointer
        \/ PublishBoth
        \/ Observe
        \/ CheckPointerAbsent
        \/ Activate
        \/ DropDemote
        \/ UNCHANGED vars

\* Nothing an ACTIVE release binds may be lost while bound.
NoDanglingBound ==
  ~activated \/ \A x \in EXEC: state[x] # FAILED /\ tableOK[x]

\* Dataset discovery for the healthy sibling survives activation unless a
\* bound execution broke the all-or-nothing reader (FixedReader = degrade).
HealthyDiscovery ==
  ~activated \/ FixedReader \/ \A x \in EXEC: state[x] = PUBLISHED /\ tableOK[x]

Spec == Init /\ [][Next]_vars

=============================================================================
