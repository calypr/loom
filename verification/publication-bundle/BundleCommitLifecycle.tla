--------------------------- MODULE BundleCommitLifecycle ---------------------
(* ClickHouse publication bundle commit/lease/pointer/reconcile protocol.
   internal/dataframe/publication/clickhouse/bundle_store.go and
   internal/dataframe/publication/arango/bundle_registry.go.

   Two workers drive publications for one identity. Each Begin mints its own
   execution attempt (per-worker row, lastTouch, cleanup slot, pins); the
   bundle lease is one key for the identity and the visibility pointer is one
   document naming whichever attempt last published, so pointer \in {Nope} +
   Workers identifies whose tables the pointer currently advertises. Publish
   AQL expects this attempt's own pointer name (registry :291) and the
   reconciler skips deletion when the live pointer names the candidate
   attempt (bundle_store.go :1068-1079); no code path ever clears the pointer.

   Reconciliation reads only what the code reads, when the code reads it:
   the candidate set comes from the paged enumeration (:1043-1051) and is
   never re-checked (recorded in rec.cand), and the pointer is read exactly
   once after the lease (:1068) and memoised in rec.ptr. ClaimExecution-
   Cleanup (:414-422) filters ONLY expired pins (:416) and the cleanup slot
   (:418): no lease, state, or pointer re-validation, and the DropTable loop
   (:1083-1089) applies no check either before deleting (:1095-1097). The
   cleanup-lease renewal guardian (bundle_store.go:238, 30 s) can never fire
   inside the 10 s boundedBundleCleanupContext (:378), so the model grants
   the renewal no role the real region could use.

   One action == one awaited DB roundtrip. PublishExecution is one AQL
   (:284-302): its write set (row + pointer) is atomic; a landed publish with
   a lost client reply is nondeterministic ("lost"). Clock ticks model
   time.Now(); expiry comparisons are >= clock. Runner discipline
   (runner.go:35-44 + Commit :811-824): success closes and is never aborted;
   a lost reply must pass ConfirmCommit first; an inconclusive ConfirmCommit
   closes and never deletes (:819-821). QUEUED-survival is a recorded
   liveness finding, not an invariant here.

   Fault overlays are scratch copies with exactly one constant TRUE;
   ALL-FALSE describes the current code:
     FNoLeaseFencePublish        publish AQL drops the lease owner/expiry
                                 filter             bundle_registry.go:289-290
     FReconcileSkipsPointerCheck reconcileExecution omits the live-pointer
                                 skip               bundle_store.go:1073
     FClaimIgnoresPins           ClaimExecutionCleanup omits the active-pin
                                 filter             bundle_registry.go:416-418 *)
EXTENDS Naturals, TLC

CONSTANTS Workers, Horizon, FNoLeaseFencePublish, FReconcileSkipsPointerCheck,
  FClaimIgnoresPins

States == {"missing", "QUEUED", "RUNNING", "VALIDATING", "PUBLISHED", "FAILED"}
Nope == "none"

VARIABLES clock,
  leaseOwner, leaseExpires,                  \* one lease key for the identity
  pointer,                                   \* one pointer doc: Nope | attempt owner
  execState, lastTouch,                      \* per-attempt execution rows
  dropped,                                   \* per-attempt reclaimed table sets
  cleanupOwner, cleanupExpires,              \* per-attempt cleanup slot
  pins, pinExpires,                          \* per-attempt reader pin sets
  phase, response, resolved, committed, closed,      \* runner-local views
  cState, cPointer,                          \* ConfirmCommit's two reads
  publishedForeign,                          \* ghost: publish wrote while the
                                             \* caller held no live lease
  rec,                                        \* reconciler working state:
                                             \*   cur  = enumerated candidate underway
                                             \*   cand = snapshot from ListStaleRuns :1049
                                             \*   ptr  = memoised GetPointer :1068 result

  ghost                                \* drop-moment records: m=fired while
                                     \* memoised ptr named it, p=fired
                                     \* while a reader pin was live
vars == <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
  dropped, cleanupOwner, cleanupExpires, pins, pinExpires,
  phase, response, resolved, committed, closed, cState, cPointer,
  publishedForeign, rec, ghost >>

Fence(w) == /\ leaseOwner = w
            /\ leaseExpires >= clock              \* SaveExecution fence :44-47
AcquirableBy(owner) == \/ leaseOwner = Nope
                       \/ leaseOwner = owner
                       \/ leaseExpires < clock    \* AcquireBundleLease :315
LivePinOf(t) == /\ pins[t] = TRUE
                /\ pinExpires[t] >= clock         \* pin expiry filter :370
CleanupActiveOf(t) == /\ cleanupOwner[t] # Nope
                      /\ cleanupExpires[t] >= clock
ClaimableFor(t, owner) ==                \* ClaimExecutionCleanup :411-427
  /\ \/ cleanupOwner[t] = Nope
     \/ cleanupExpires[t] < clock
     \/ cleanupOwner[t] = owner
  /\ (FClaimIgnoresPins \/ ~LivePinOf(t))         \* active-pin filter :416-418
PublishAqlOk(w) ==                        \* the publish AQL's WHERE filters
  /\ (FNoLeaseFencePublish \/ Fence(w))   \* lease filter :289-290
  /\ (\/ pointer = Nope
      \/ pointer = w)                     \* expected own pointer name :291
CandidateFor(t) == /\ execState[t] \in {"RUNNING", "VALIDATING", "FAILED"}  \* :1043
                   /\ lastTouch[t] + 1 <= clock                              \* :1049

Init ==
  /\ clock = 0
  /\ leaseOwner = Nope /\ leaseExpires = 0
  /\ pointer = Nope
  /\ execState = [w \in Workers |-> "missing"]
  /\ lastTouch = [w \in Workers |-> 0]
  /\ dropped = [w \in Workers |-> FALSE]
  /\ cleanupOwner = [w \in Workers |-> Nope] /\ cleanupExpires = [w \in Workers |-> 0]
  /\ pins = [w \in Workers |-> FALSE] /\ pinExpires = [w \in Workers |-> 0]
  /\ phase = [w \in Workers |-> "idle"]
  /\ response = [w \in Workers |-> "none"]
  /\ resolved = [w \in Workers |-> FALSE]
  /\ committed = [w \in Workers |-> FALSE]
  /\ closed = [w \in Workers |-> FALSE]
  /\ cState = [w \in Workers |-> "unread"]
  /\ cPointer = [w \in Workers |-> "unread"]
  /\ publishedForeign = [w \in Workers |-> FALSE]
  /\ rec = [cur |-> Nope, cand |-> [w \in Workers |-> FALSE],
        ptr |-> [w \in Workers |-> Nope],
        rd |-> [w \in Workers |-> FALSE]]
  /\ ghost = [m |-> FALSE, p |-> FALSE]

(* ------------------------------ Begin :123-184 ----------------------------- *)
RestartRunner(w) ==                           \* process restart after crash: fresh
  /\ phase[w] = "dead"                        \* runner-local view over the same
  /\ phase' = [phase EXCEPT ![w] = "idle"]    \* persisted attempt doc
  /\ response' = [response EXCEPT ![w] = "none"]
  /\ resolved' = [resolved EXCEPT ![w] = FALSE]
  /\ committed' = [committed EXCEPT ![w] = FALSE]
  /\ closed' = [closed EXCEPT ![w] = FALSE]
  /\ cState' = [cState EXCEPT ![w] = "unread"]
  /\ cPointer' = [cPointer EXCEPT ![w] = "unread"]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires,
    publishedForeign, rec, ghost>>

BeginCheck(w) ==                              \* FindExecutionByKey dedup :137-152; an
  /\ phase[w] = "idle"                        \* attempt in QUEUED/RUNNING/VALIDATING
  /\ \A o \in Workers:                        \* blocks a fresh begin (ErrBundleInFlight
       \/ o = w                               \* :146/:165); own terminal/missing row
           \/ execState[o] \notin {"QUEUED", "RUNNING", "VALIDATING"}   \* may be re-used
  /\ phase' = [phase EXCEPT ![w] = "begun"]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

BeginAcquire(w) ==
  /\ phase[w] = "begun"
  /\ AcquirableBy(w)
  /\ leaseOwner' = w /\ leaseExpires' = clock + 2
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, rec, ghost>>

BeginQueued(w) ==                             \* SaveExecution QUEUED upsert :167-173
  /\ phase[w] = "begun"
  /\ Fence(w)
  /\ execState' = [execState EXCEPT ![w] = "QUEUED"]
  /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, dropped,
    cleanupOwner, cleanupExpires, pins, pinExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

BeginRunning(w) ==                            \* SaveExecution RUNNING :174-180
  /\ phase[w] = "begun"
  /\ execState[w] = "QUEUED"
  /\ Fence(w)
  /\ execState' = [execState EXCEPT ![w] = "RUNNING"]
  /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, dropped,
    cleanupOwner, cleanupExpires, pins, pinExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

SaveValidated(w) ==                           \* Commit valid stage :781-787
  /\ phase[w] = "begun"
  /\ execState[w] = "RUNNING"
  /\ response[w] = "none"
  /\ ~closed[w] /\ ~committed[w]
  /\ Fence(w)
  /\ execState' = [execState EXCEPT ![w] = "VALIDATING"]
  /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, dropped,
    cleanupOwner, cleanupExpires, pins, pinExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

(* ------------------- PublishExecution :284-302 (one AQL) ------------------- *)
PublishLanded(w) ==
  /\ phase[w] = "begun"
  /\ execState[w] = "VALIDATING"
  /\ response[w] = "none"
  /\ ~closed[w] /\ ~committed[w]
  /\ PublishAqlOk(w)
  /\ execState' = [execState EXCEPT ![w] = "PUBLISHED"]
  /\ pointer' = w
  /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
  /\ publishedForeign' = [publishedForeign EXCEPT ![w] =
       publishedForeign[w] \/ ~Fence(w)]
  /\ \/ /\ response' = [response EXCEPT ![w] = "ok"]
        /\ closed' = [closed EXCEPT ![w] = TRUE]  \* success: never aborted
     \/ /\ response' = [response EXCEPT ![w] = "lost"]   \* lost reply
        /\ UNCHANGED <<closed>>                    \* must confirm before Abort
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, cleanupOwner, cleanupExpires,
    pins, pinExpires, phase, resolved, committed, dropped, cState, cPointer, rec, ghost>>

PublishRejected(w) ==                         \* filter false, AQL wrote nothing;
  /\ phase[w] = "begun"                       \* Commit failPhase -> runner Abort
  /\ execState[w] = "VALIDATING"
  /\ response[w] = "none"
  /\ ~closed[w] /\ ~committed[w]
  /\ ~PublishAqlOk(w)
  /\ response' = [response EXCEPT ![w] = "conflict"]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

(* ---------------- ConfirmCommit :841-859 (two separate reads) -------------- *)
ConfirmExecRead(w) ==
  /\ phase[w] = "begun"
  /\ response[w] = "lost"
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ cState' = [cState EXCEPT ![w] = execState[w]]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cPointer, publishedForeign, rec, ghost>>

ConfirmPointerRead(w) ==
  /\ phase[w] = "begun"
  /\ response[w] = "lost"
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ cPointer' = [cPointer EXCEPT ![w] = pointer]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cState, publishedForeign, rec, ghost>>

ConfirmAdopt(w) ==                            \* queryable + pointer names us :851-854
  /\ phase[w] = "begun"
  /\ response[w] = "lost"
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ cState[w] = "PUBLISHED" /\ cPointer[w] = w
  /\ committed' = [committed EXCEPT ![w] = TRUE]
  /\ closed' = [closed EXCEPT ![w] = TRUE]
  /\ resolved' = [resolved EXCEPT ![w] = TRUE]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    cState, cPointer, publishedForeign, rec, ghost>>

ConfirmNegative(w) ==                         \* conclusive not-published :855-856
  /\ phase[w] = "begun"
  /\ response[w] = "lost"
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ cState[w] # "PUBLISHED" /\ cPointer[w] # w
  /\ resolved' = [resolved EXCEPT ![w] = TRUE]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

ConfirmInconclusive(w) ==                     \* reads disagree -> Commit closes
  /\ phase[w] = "begun"                       \* before returning, never deletes
  /\ response[w] = "lost"                     \* (:819-821)
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ ~ (cState[w] = "PUBLISHED" /\ cPointer[w] = w)
  /\ ~ (cState[w] # "PUBLISHED" /\ cPointer[w] # w)
  /\ cState[w] # "unread" /\ cPointer[w] # "unread"
  /\ closed' = [closed EXCEPT ![w] = TRUE]
  /\ resolved' = [resolved EXCEPT ![w] = TRUE]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    committed, cState, cPointer, publishedForeign, rec, ghost>>

(* ------------------------------- Abort :861-897 ---------------------------- *)
AbortClaim(w) ==                              \* ensureLease :865-867 + Claim :871-874
  /\ phase[w] = "begun"
  /\ ~closed[w] /\ ~committed[w]
  /\ (\/ response[w] # "lost"
      \/ resolved[w])                         \* lost reply confirms first :811-824
  /\ Fence(w)
  /\ ClaimableFor(w, w)
  /\ cleanupOwner' = [cleanupOwner EXCEPT ![w] = w]
  /\ cleanupExpires' = [cleanupExpires EXCEPT ![w] = clock + 1]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, pins, pinExpires, phase, response, resolved, committed, closed,
    cState, cPointer, publishedForeign, rec, ghost>>

AbortDrop(w) ==                               \* DropTable loop unconditional :878-882;
  /\ phase[w] = "begun"                       \* fenced failPhase save :890-891
  /\ ~closed[w] /\ ~committed[w]
  /\ cleanupOwner[w] = w
  /\ cleanupExpires[w] >= clock
  /\ ~dropped[w]
   /\ dropped' = [dropped EXCEPT ![w] = TRUE]
   /\ cleanupOwner' = [cleanupOwner EXCEPT ![w] = Nope]  \* defer release :896
   /\ ghost' = [ghost EXCEPT !.p = ghost.p \/ LivePinOf(w)]
   /\ IF Fence(w)
        THEN /\ execState' = [execState EXCEPT ![w] = "FAILED"]
             /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
        ELSE UNCHANGED <<execState, lastTouch>>
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, cleanupExpires, pointer,
    pins, pinExpires, phase, response, resolved, committed, closed, cState,
    cPointer, publishedForeign, rec>>

(* ------------------------------ reconciler --------------------------------- *)
ReconcileEnumerate(t) ==                      \* VisitExecutionPages :1049 snapshots the
  /\ rec.cur = Nope                           \* candidate; the real loop (reconcile-
  /\ CandidateFor(t)                          \* Execution :1056) never re-reads row
  /\ rec' = [rec EXCEPT !.cur = t,
             !.cand = [rec.cand EXCEPT ![t] = TRUE],
             !.rd = [rec.rd EXCEPT ![t] = FALSE]]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, ghost>>

ReconcileAcquire(t) ==                        \* AcquireBundleLease :1058; if not
  /\ rec.cur = t                              \* claimed :1062 the candidate is simply
  /\ AcquirableBy("rec")                      \* abandoned (ReconcileAbandon covers the
                                              \* release path once the lease is held)
  /\ leaseOwner' = "rec" /\ leaseExpires' = clock + 2
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, rec, ghost>>

ReconcilePointerRead(t) ==                    \* GetPointer is one awaited read (:1068)
  /\ rec.cur = t
  /\ ~rec.rd[t]                              \* memoised: read once
  /\ leaseOwner = "rec"
  /\ leaseExpires >= clock
  /\ rec' = [rec EXCEPT !.ptr = [rec.ptr EXCEPT ![t] = pointer],
             !.rd = [rec.rd EXCEPT ![t] = TRUE]]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, ghost>>

ReconcileSkip(t) ==                           \* :1073-1074 releases and returns when the
  /\ ~FReconcileSkipsPointerCheck              \* memoised read named this candidate;
  /\ rec.cur = t                              \* FReconcileSkipsPointerCheck removes
  /\ leaseOwner = "rec"                        \* the skip (its fault overlay)
  /\ rec.ptr[t] = t
  /\ leaseOwner' = Nope /\ leaseExpires' = 0
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, ghost>>

ReconcileAbandon(t) ==                        \* claim refused or lease gone: :1078
  /\ rec.cur = t                              \* ReleaseBundleLease path, no delete
  /\ leaseOwner = "rec"
  /\ leaseOwner' = Nope /\ leaseExpires' = 0
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, ghost>>

ReconcileClaim(t) ==                          \* ClaimExecutionCleanup :414-422 acts on
  /\ rec.cur = t                              \* the enumeration snapshot and the
  /\ (FReconcileSkipsPointerCheck              \* memoised pointer read: NO live state,
      \/ rec.ptr[t] # t)                      \* lease, or pointer re-validation
  /\ rec.rd[t]                               \* :1068 precedes :1076
  /\ ClaimableFor(t, "rec")
  /\ cleanupOwner' = [cleanupOwner EXCEPT ![t] = "rec"]
  /\ cleanupExpires' = [cleanupExpires EXCEPT ![t] = clock + 1]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, pins, pinExpires, phase, response, resolved, committed, closed,
    cState, cPointer, publishedForeign, rec, ghost>>

ReconcileDrop(t) ==                           \* DropTable :1083-1089 then fenced
  /\ rec.cur = t                              \* SaveExecution FAILED :1095-1097:
  /\ cleanupOwner[t] = "rec"                  \* NO pointer and NO lease re-check
  /\ cleanupExpires[t] >= clock               \* between claim and drop
  /\ ~dropped[t]
  /\ dropped' = [dropped EXCEPT ![t] = TRUE]
  /\ cleanupOwner' = [cleanupOwner EXCEPT ![t] = Nope]  \* deferred release :1100
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ ghost' = [ghost EXCEPT !.m = ghost.m \/ (rec.ptr[t] = t),
                !.p = ghost.p \/ LivePinOf(t)]
  /\ IF leaseOwner = "rec" /\ leaseExpires >= clock
        THEN /\ execState' = [execState EXCEPT ![t] = "FAILED"]
             /\ lastTouch' = [lastTouch EXCEPT ![t] = clock]
        ELSE UNCHANGED <<execState, lastTouch>>
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, cleanupExpires, pointer, pins,
     pinExpires, phase, response, resolved, committed, closed, cState, cPointer,
     publishedForeign>>

ReconcileRelease ==                           \* ReleaseBundleLease with owner match
  /\ leaseOwner = "rec"                       \* (:333 is a no-op once the key is
  /\ leaseOwner' = Nope /\ leaseExpires' = 0   \* someone else's)
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, ghost>>

(* --------------------------- reader pins :343-376 -------------------------- *)
AcquirePin(w) ==                              \* reader pins its attempt doc;
  /\ execState[w] # "missing"                 \* refused while cleanup is claimed :353
  /\ pins[w] = FALSE
  /\ ~CleanupActiveOf(w)
  /\ pins' = [pins EXCEPT ![w] = TRUE]
  /\ pinExpires' = [pinExpires EXCEPT ![w] = clock + 4]   \* read pins outlive lease
                                                          \* churn (minutes in prod)
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

Crash(w) ==                                   \* runner dies mid-flight
  /\ phase[w] = "begun"
  /\ phase' = [phase EXCEPT ![w] = "dead"]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

Tick ==
  /\ clock < Horizon
  /\ clock' = clock + 1
  /\ UNCHANGED <<leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

Next ==
  \/ \E w \in Workers:
       \/ RestartRunner(w)
       \/ BeginCheck(w) \/ BeginAcquire(w) \/ BeginQueued(w) \/ BeginRunning(w)
       \/ SaveValidated(w)
       \/ PublishLanded(w) \/ PublishRejected(w)
       \/ ConfirmExecRead(w) \/ ConfirmPointerRead(w) \/ ConfirmAdopt(w)
       \/ ConfirmNegative(w) \/ ConfirmInconclusive(w)
       \/ AbortClaim(w) \/ AbortDrop(w)
       \/ AcquirePin(w) \/ Crash(w)
       \/ ReconcileEnumerate(w) \/ ReconcileAcquire(w) \/ ReconcilePointerRead(w)
       \/ ReconcileSkip(w) \/ ReconcileAbandon(w)
       \/ ReconcileClaim(w) \/ ReconcileDrop(w)
  \/ ReconcileRelease
  \/ Tick
  \/ UNCHANGED vars      \* termination behavior: no protocol step left to take

Spec == Init /\ [][Next]_vars

(* ------------------------------- SAFETY ----------------------------------- *)
\* A reclaimed table set is never the one the visibility pointer advertises
\* (the invariant the ConfirmCommit comment names, :840). Harm: reads resolve
\* through a live pointer onto ClickHouse sets Arango still names but Click-
\* House no longer has.
InvariantNoDropUnderPointer ==
  \A w \in Workers: dropped[w] => pointer # w

\* No reclaim while any reader pin on that attempt is active
\* (ClaimExecutionCleanup :416-418 exists to prevent this). Harm: an in-flight
\* export or dashboard read loses its rows mid-stream.
InvariantNoDropWhenNamed ==
  ghost.m = FALSE

InvariantNoDropUnderPin ==
  ghost.p = FALSE

\* With all fault constants FALSE, PublishExecution can only write while its
\* caller holds a live lease: the AQL lease filter :289-290 is the gate.
InvariantNoForeignPublish ==
  \A w \in Workers: publishedForeign[w] = FALSE
================================================================
