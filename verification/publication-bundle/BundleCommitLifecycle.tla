--------------------------- MODULE BundleCommitLifecycle ---------------------
EXTENDS Naturals, TLC

CONSTANTS Workers, Horizon, FNoLeaseFencePublish, FReconcileSkipsPointerCheck,
  FClaimIgnoresPins

States == {"missing", "QUEUED", "RUNNING", "VALIDATING", "PUBLISHED", "FAILED"}
Nope == "none"

VARIABLES clock,
  leaseOwner, leaseExpires,
  pointer,
  execState, lastTouch,
  dropped,
  cleanupOwner, cleanupExpires,
  pins, pinExpires,
  phase, response, resolved, committed, closed,
  cState, cPointer,
  publishedForeign,
  rec,

  ghost
vars == <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
  dropped, cleanupOwner, cleanupExpires, pins, pinExpires,
  phase, response, resolved, committed, closed, cState, cPointer,
  publishedForeign, rec, ghost >>

Fence(w) == /\ leaseOwner = w
            /\ leaseExpires >= clock
AcquirableBy(owner) == \/ leaseOwner = Nope
                       \/ leaseOwner = owner
                       \/ leaseExpires < clock
LivePinOf(t) == /\ pins[t] = TRUE
                /\ pinExpires[t] >= clock
CleanupActiveOf(t) == /\ cleanupOwner[t] # Nope
                      /\ cleanupExpires[t] >= clock
ClaimableFor(t, owner) ==
  /\ \/ cleanupOwner[t] = Nope
     \/ cleanupExpires[t] < clock
     \/ cleanupOwner[t] = owner
  /\ (FClaimIgnoresPins \/ ~LivePinOf(t))
PublishAqlOk(w) ==
  /\ (FNoLeaseFencePublish \/ Fence(w))
  /\ (\/ pointer = Nope
      \/ pointer = w)
CandidateFor(t) == /\ execState[t] \in {"RUNNING", "VALIDATING", "FAILED"}
                   /\ lastTouch[t] + 1 <= clock

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

RestartRunner(w) ==
  /\ phase[w] = "dead"
  /\ phase' = [phase EXCEPT ![w] = "idle"]
  /\ response' = [response EXCEPT ![w] = "none"]
  /\ resolved' = [resolved EXCEPT ![w] = FALSE]
  /\ committed' = [committed EXCEPT ![w] = FALSE]
  /\ closed' = [closed EXCEPT ![w] = FALSE]
  /\ cState' = [cState EXCEPT ![w] = "unread"]
  /\ cPointer' = [cPointer EXCEPT ![w] = "unread"]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires,
    publishedForeign, rec, ghost>>

BeginCheck(w) ==
  /\ phase[w] = "idle"
  /\ \A o \in Workers:
       \/ o = w
           \/ execState[o] \notin {"QUEUED", "RUNNING", "VALIDATING"}
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

BeginQueued(w) ==
  /\ phase[w] = "begun"
  /\ Fence(w)
  /\ execState' = [execState EXCEPT ![w] = "QUEUED"]
  /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, dropped,
    cleanupOwner, cleanupExpires, pins, pinExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

BeginRunning(w) ==
  /\ phase[w] = "begun"
  /\ execState[w] = "QUEUED"
  /\ Fence(w)
  /\ execState' = [execState EXCEPT ![w] = "RUNNING"]
  /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, dropped,
    cleanupOwner, cleanupExpires, pins, pinExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

SaveValidated(w) ==
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
        /\ closed' = [closed EXCEPT ![w] = TRUE]
     \/ /\ response' = [response EXCEPT ![w] = "lost"]
        /\ UNCHANGED <<closed>>
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, cleanupOwner, cleanupExpires,
    pins, pinExpires, phase, resolved, committed, dropped, cState, cPointer, rec, ghost>>

PublishRejected(w) ==
  /\ phase[w] = "begun"
  /\ execState[w] = "VALIDATING"
  /\ response[w] = "none"
  /\ ~closed[w] /\ ~committed[w]
  /\ ~PublishAqlOk(w)
  /\ response' = [response EXCEPT ![w] = "conflict"]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

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

ConfirmAdopt(w) ==
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

ConfirmNegative(w) ==
  /\ phase[w] = "begun"
  /\ response[w] = "lost"
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ cState[w] # "PUBLISHED" /\ cPointer[w] # w
  /\ resolved' = [resolved EXCEPT ![w] = TRUE]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

ConfirmInconclusive(w) ==
  /\ phase[w] = "begun"
  /\ response[w] = "lost"
  /\ ~closed[w] /\ ~committed[w] /\ ~resolved[w]
  /\ ~ (cState[w] = "PUBLISHED" /\ cPointer[w] = w)
  /\ ~ (cState[w] # "PUBLISHED" /\ cPointer[w] # w)
  /\ cState[w] # "unread" /\ cPointer[w] # "unread"
  /\ closed' = [closed EXCEPT ![w] = TRUE]
  /\ resolved' = [resolved EXCEPT ![w] = TRUE]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    committed, cState, cPointer, publishedForeign, rec, ghost>>

AbortClaim(w) ==
  /\ phase[w] = "begun"
  /\ ~closed[w] /\ ~committed[w]
  /\ (\/ response[w] # "lost"
      \/ resolved[w])
  /\ Fence(w)
  /\ ClaimableFor(w, w)
  /\ cleanupOwner' = [cleanupOwner EXCEPT ![w] = w]
  /\ cleanupExpires' = [cleanupExpires EXCEPT ![w] = clock + 1]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, pins, pinExpires, phase, response, resolved, committed, closed,
    cState, cPointer, publishedForeign, rec, ghost>>

AbortDrop(w) ==
  /\ phase[w] = "begun"
  /\ ~closed[w] /\ ~committed[w]
  /\ cleanupOwner[w] = w
  /\ cleanupExpires[w] >= clock
  /\ ~dropped[w]
   /\ dropped' = [dropped EXCEPT ![w] = TRUE]
   /\ cleanupOwner' = [cleanupOwner EXCEPT ![w] = Nope]
   /\ ghost' = [ghost EXCEPT !.p = ghost.p \/ LivePinOf(w)]
   /\ IF Fence(w)
        THEN /\ execState' = [execState EXCEPT ![w] = "FAILED"]
             /\ lastTouch' = [lastTouch EXCEPT ![w] = clock]
        ELSE UNCHANGED <<execState, lastTouch>>
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, cleanupExpires, pointer,
    pins, pinExpires, phase, response, resolved, committed, closed, cState,
    cPointer, publishedForeign, rec>>

ReconcileEnumerate(t) ==
  /\ rec.cur = Nope
  /\ CandidateFor(t)
  /\ rec' = [rec EXCEPT !.cur = t,
             !.cand = [rec.cand EXCEPT ![t] = TRUE],
             !.rd = [rec.rd EXCEPT ![t] = FALSE]]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, ghost>>

ReconcileAcquire(t) ==
  /\ rec.cur = t
  /\ AcquirableBy("rec")
  /\ leaseOwner' = "rec" /\ leaseExpires' = clock + 2
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, rec, ghost>>

ReconcilePointerRead(t) ==
  /\ rec.cur = t
  /\ ~rec.rd[t]
  /\ leaseOwner = "rec"
  /\ leaseExpires >= clock
  /\ rec' = [rec EXCEPT !.ptr = [rec.ptr EXCEPT ![t] = pointer],
             !.rd = [rec.rd EXCEPT ![t] = TRUE]]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, pins, pinExpires, phase, response,
    resolved, committed, closed, cState, cPointer, publishedForeign, ghost>>

ReconcileSkip(t) ==
  /\ ~FReconcileSkipsPointerCheck
  /\ rec.cur = t
  /\ leaseOwner = "rec"
  /\ rec.ptr[t] = t
  /\ leaseOwner' = Nope /\ leaseExpires' = 0
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, ghost>>

ReconcileAbandon(t) ==
  /\ rec.cur = t
  /\ leaseOwner = "rec"
  /\ leaseOwner' = Nope /\ leaseExpires' = 0
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, ghost>>

ReconcileClaim(t) ==
  /\ rec.cur = t
  /\ (FReconcileSkipsPointerCheck
      \/ rec.ptr[t] # t)
  /\ rec.rd[t]
  /\ ClaimableFor(t, "rec")
  /\ cleanupOwner' = [cleanupOwner EXCEPT ![t] = "rec"]
  /\ cleanupExpires' = [cleanupExpires EXCEPT ![t] = clock + 1]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, pins, pinExpires, phase, response, resolved, committed, closed,
    cState, cPointer, publishedForeign, rec, ghost>>

ReconcileDrop(t) ==
  /\ rec.cur = t
  /\ cleanupOwner[t] = "rec"
  /\ cleanupExpires[t] >= clock
  /\ ~dropped[t]
  /\ dropped' = [dropped EXCEPT ![t] = TRUE]
  /\ cleanupOwner' = [cleanupOwner EXCEPT ![t] = Nope]
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

ReconcileRelease ==
  /\ leaseOwner = "rec"
  /\ leaseOwner' = Nope /\ leaseExpires' = 0
  /\ rec' = [rec EXCEPT !.cur = Nope]
  /\ UNCHANGED <<clock, pointer, execState, lastTouch, dropped, cleanupOwner,
    cleanupExpires, pins, pinExpires, phase, response, resolved, committed,
    closed, cState, cPointer, publishedForeign, ghost>>

AcquirePin(w) ==
  /\ execState[w] # "missing"
  /\ pins[w] = FALSE
  /\ ~CleanupActiveOf(w)
  /\ pins' = [pins EXCEPT ![w] = TRUE]
  /\ pinExpires' = [pinExpires EXCEPT ![w] = clock + 4]
  /\ UNCHANGED <<clock, leaseOwner, leaseExpires, pointer, execState, lastTouch,
    dropped, cleanupOwner, cleanupExpires, phase, response, resolved,
    committed, closed, cState, cPointer, publishedForeign, rec, ghost>>

Crash(w) ==
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
  \/ UNCHANGED vars

Spec == Init /\ [][Next]_vars

InvariantNoDropUnderPointer ==
  \A w \in Workers: dropped[w] => pointer # w

InvariantNoDropWhenNamed ==
  ghost.m = FALSE

InvariantNoDropUnderPin ==
  ghost.p = FALSE

InvariantNoForeignPublish ==
  \A w \in Workers: publishedForeign[w] = FALSE
================================================================
