--------------------- MODULE StartupReconcileWedge ---------------------
(*
Models F-B3 (CONFIRMED): server startup runs one Reconcile
(internal/dataframe/publication/clickhouse/bundle_store.go:1033-1052
first-error-wins, :1076-1079 propagate ErrExecutionReadPinActive :301-313).
A single live artifact read pin aborts the WHOLE first-error-wins pass, so NO
bundle is dropped (collateral) and publicationReady never flips true
(internal/server/server.go:556-559); Reconcile's only caller is startup, so
there is no retry and the process stays degraded for its lifetime.
PinnedExpiry > TimeMax models the 24h artifact horizon outliving the startup
window; PinnedExpiry <= TimeMax is the control in which the pin lapses inside
the observed window.
*)

EXTENDS Naturals, FiniteSets

CONSTANT Bundles,        \* bundles present at startup
         Pinned,         \* SUBSET Bundles with live artifact read pins
         PinnedExpiry,   \* shared pin horizon for all pinned bundles
         TimeMax         \* startup observation window

ASSUME Wellformed ==
  /\ Bundles # {}
  /\ Pinned \subseteq Bundles
  /\ PinnedExpiry \in Nat
  /\ TimeMax \in Nat

VARIABLES now,
          ready,
          dropped        \* bundles physically dropped by the single pass

TypeOK ==
  /\ now \in 0..TimeMax
  /\ ready \in BOOLEAN
  /\ dropped \subseteq Bundles

Init ==
  /\ now = 0
  /\ ready = FALSE
  /\ dropped = {}

PinActive == PinnedExpiry > now

\* Startup Reconcile pass aborted by the pinned bundle's cleanup claim
\* (first-error-wins): every still-pending bundle survives this pass
\* (collateral), including the unpinned ones. Repeated attempts re-fail with
\* no state change; there is no retry mechanism beyond this (server.go:556).
StartupReconcileFail ==
  /\ ~ready
  /\ PinActive
  /\ UNCHANGED <<now, ready, dropped>>

\* Same pass with no active pin: all bundles drop together and
\* publicationReady flips true. Only caller is startup, so it commits at most
\* once per process.
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
        \/ UNCHANGED vars   \* other server activity while booting

\* Viewer-facing guarantee: startup eventually reaches publicationReady
\* permanently (server.go:556-559 sets readiness only if Reconcile succeeds).
EventuallyReady == <>[]ready

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(StartupReconcileSuccess)

=============================================================================
