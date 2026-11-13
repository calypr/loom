--------------------- MODULE StartupReconcileWedge ---------------------

EXTENDS Naturals, FiniteSets

CONSTANT Bundles,
         Pinned,
         PinnedExpiry,
         TimeMax

ASSUME Wellformed ==
  /\ Bundles # {}
  /\ Pinned \subseteq Bundles
  /\ PinnedExpiry \in Nat
  /\ TimeMax \in Nat

VARIABLES now,
          ready,
          dropped

TypeOK ==
  /\ now \in 0..TimeMax
  /\ ready \in BOOLEAN
  /\ dropped \subseteq Bundles

Init ==
  /\ now = 0
  /\ ready = FALSE
  /\ dropped = {}

PinActive == PinnedExpiry > now

StartupReconcileFail ==
  /\ ~ready
  /\ PinActive
  /\ UNCHANGED <<now, ready, dropped>>

StartupReconcileSuccess ==
  /\ ~ready
  /\ ~PinActive
  /\ dropped' = Bundles
  /\ ready' = TRUE
  /\ UNCHANGED now

Tick ==
  /\ now < TimeMax
  /\ now' = now + 1
  /\ UNCHANGED <<ready, dropped>>

vars == <<now, ready, dropped>>

Next == \/ StartupReconcileFail
        \/ StartupReconcileSuccess
        \/ Tick
        \/ UNCHANGED vars

EventuallyReady == <>[]ready

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(StartupReconcileSuccess)

=============================================================================
