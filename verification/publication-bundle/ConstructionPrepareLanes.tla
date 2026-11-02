------------------------- MODULE ConstructionPrepareLanes -------------------------
(* Bounded lane-clearance state machine for the four row-operation prepare lanes
   (ROW-SOURCE-05 P0, shipped at b72a1322). One reachable state = the durable
   server-owned input presence after one persisted prepare proposal. Consumer
   and gap record: verification/publication-bundle/SKILL.md "Prepare-lane
   clearance". Existing durables (internal/explorer/lifecycle/
   filter_source_pivot_clear_test.go, 20 named tests) sample base x carry x
   order; this model quantifies over ALL of it.

   SOURCE SYNC (authored at 0dd7be44; prepare lanes byte-stable since b72a1322/
   9c79307b — 0dd7be44 touched only reconcile ledger + UI gate, verified):
     shared helper          construction_pivot_source.go:566-596 (call sites
                            :313 pivot w/ skipOwner, :230 derive """, :270
                            unpivot """)
     cleared predicate      construction_pivot_source.go:607-633 (base inputs>0
                            /\ candidate carries none of the owner -> clear;
                            >0 carried -> omission -> repair; base empty -> no-op)
     pivot set-branch loop  construction_pivot_source.go:383-387 (guarded :385)
     filter set-branch loop filter_source.go:243-247 (guarded :245)
     derive frozen copy     derive_source.go:296-299   (unguarded; SET reattribution
     unpivot frozen copy    unpivot_source.go:336-339   :274-287 + cascade prune +
                            consumption validation keep unconsumed re-imports
                            out of durable state — assumption A3, falsifiable by
                            arm m-prep-persist-unconsumed)
     consumption validation construction_validation.go:348-376
     cascade prune            authoringv2/construction_change.go:525-530
   Each Propose action is one request build (helper + lane rebuild + persist)
   treated atomic: the durable layer never observes a mid-request candidate.

   Carries mirror the wire contract "nil never re-attaches, [] clears" —
   Untouched = request field nil / client mirror; Omitted = partial chain
   carried (>0 items, repair expected); Cleared = [] (zero-carried with base
   presence => the :607 predicate fires); Replaced = client-supplied new
   signed facts (client intent, never counted as a server restore). *)

EXTENDS Naturals, TLC

CONSTANTS FHelperGateIgnoresCleared,      \* TRUE: shared helper restores even
                                          \*   cleared owners (pre-b72a1322 shape)
          FSetBranchGateIgnoresCleared,   \* TRUE: pivot/filter set-branch bulk
                                          \*   imports ignore the cleared gate
          FSetBranchPersistsUnconsumed,   \* TRUE: derive/unpivot frozen copies
                                          \*   persist without SET-drop/consume
                                          \*   prune (violates A3 by design)
          FSharedClearedGate              \* TRUE: one global cleared flag keyed
                                          \*   on any clear instead of per-owner
                                          \*   (over-gated wrong-fix shape)

ASSUME /\ FHelperGateIgnoresCleared \in BOOLEAN
       /\ FSetBranchGateIgnoresCleared \in BOOLEAN
       /\ FSetBranchPersistsUnconsumed \in BOOLEAN
       /\ FSharedClearedGate \in BOOLEAN

Lanes  == {"pivot", "filter", "derive", "unpivot"}
Carries == {"Untouched", "Omitted", "Cleared", "Replaced"}
States == {"None", "Intact"}

VARIABLES durable,       \* [Lanes -> States]: persisted server-owned inputs per lane
          prev,          \* durable before the latest proposal (ghost, full copy)
          picks,         \* lanes already edited this run (propose-order nondet)
          lastCarried,   \* [Lanes -> Carries \cup {"never"}] of the latest proposal
          result         \* "started" | "persisted" | "error" of the latest proposal

vars == <<durable, prev, picks, lastCarried, result>>

clearedPred(base, carry, o) == /\ base[o] = "Intact"
                               /\ carry[o] = "Cleared"
anyCleared(base, carry) == \E e \in Lanes: clearedPred(base, carry, e)

helperDomain(L, o)   == o = "pivot" /\ L # "pivot"
setBranchSite(L)     == L \in {"pivot", "filter"}
frozenSite(L)        == L \in {"derive", "unpivot"}

\* A cleared owner may be re-imported only on the site whose fault constant
\* ignores the gate. Helper faults on pivot owners (L # pivot, :575 semantics);
\* the two guarded set sites fault on every o # L (:385/:245); the frozen
\* copies fault on every o # L but persist (:296/:336 under A3 violation).
faultRestores(L, base, carry, o) ==
  IF o # L /\ clearedPred(base, carry, o)
  THEN \/ /\ helperDomain(L, o) /\ FHelperGateIgnoresCleared
       \/ /\ setBranchSite(L) /\ FSetBranchGateIgnoresCleared
       \/ /\ frozenSite(L) /\ FSetBranchPersistsUnconsumed
       \/ FALSE
  ELSE FALSE

\* Under the helper/set-branch faults the unconsumed re-import trips the
\* consumption validator (construction_validation.go:348-376): the proposal
\* ERRORS and durable is untouched (the archived P0 symptom: the user cannot
\* clear the lane). The persists-unconsumed arm has no such veto (A3 dropped).
proposalError(L, base, carry) ==
  \E o \in Lanes: faultRestores(L, base, carry, o)
                 /\ ~(frozenSite(L) /\ FSetBranchPersistsUnconsumed)

newState(L, base, carry, o) ==
  IF o = L
    THEN IF carry[o] = "Replaced" THEN "Intact"
         ELSE IF carry[o] = "Cleared" THEN "None"
         ELSE base[o]                                \* Untouched / Omitted own lane
    ELSE IF base[o] = "Intact"
      THEN IF carry[o] = "Cleared"
        THEN IF faultRestores(L, base, carry, o)
             THEN IF frozenSite(L) /\ FSetBranchPersistsUnconsumed
                  THEN "Intact"
                  ELSE base[o]                        \* error path keeps old state
             ELSE "None"                              \* gated: cleared stays cleared
        ELSE IF carry[o] = "Omitted"
          THEN IF FSharedClearedGate /\ anyCleared(base, carry)
               THEN "None"                             \* wrong-fix: global suppression
               ELSE "Intact"                           \* repair (:607 omission path)
          ELSE IF carry[o] = "Replaced" THEN "Intact"
          ELSE base[o]
      ELSE IF carry[o] = "Replaced" THEN "Intact"     \* client intent creates
      ELSE "None"

Init ==
  /\ \E d \in [Lanes -> States]: /\ durable = d
                                 /\ prev = d
  /\ picks = {}
  /\ lastCarried = [o \in Lanes |-> "never"]
  /\ result = "started"

Propose(L, carry) ==
  /\ L \in Lanes \ picks
  /\ carry \in [Lanes -> Carries]
  /\ picks' = picks \cup {L}
  /\ prev' = durable
  /\ lastCarried' = carry
  /\ IF proposalError(L, durable, carry)
       THEN /\ durable' = durable
            /\ result' = "error"
       ELSE /\ durable' = [o \in Lanes |-> newState(L, durable, carry, o)]
            /\ result' = "persisted"

Next == \E L \in Lanes, carry \in [Lanes -> Carries]: Propose(L, carry)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ durable \in [Lanes -> States]
  /\ prev \in [Lanes -> States]
  /\ picks \subseteq Lanes
  /\ lastCarried \in [Lanes -> (Carries \cup {"never"})]
  /\ result \in {"started", "persisted", "error"}

\* rs05 P0, quantified: a lane whose latest carry was Cleared ends persisted
\* with no server-owned inputs. The archived unconditional-restore shape fails
\* here either by erroring the clear forever or persisting a resurrection.
ClearedStaysCleared ==
  \A o \in Lanes: lastCarried[o] = "Cleared" => /\ result = "persisted"
                                                 /\ durable[o] = "None"

\* Server imports repair, never create: Intact from None only via client
\* Replaced (hasPivotSourceProjection re-imports read the base; a scrubbed
\* cleared lane has baseInputs = 0 at :619 and can never regain items).
NoFalseRestore ==
  \A o \in Lanes: /\ durable[o] = "Intact"
                  /\ prev[o] = "None"
               => lastCarried[o] = "Replaced"

\* The rs05 fix must not eat preservation: prev Intact with a faithful or
\* partial-omission carry persists Intact (repair :578-593 / :383 / :243 /
\* :296 / :336 omission paths). Fires under FSharedClearedGate.
UntouchedOwnerSurvival ==
  \A o \in Lanes: /\ prev[o] = "Intact"
                  /\ lastCarried[o] \in {"Untouched", "Omitted"}
                  /\ result = "persisted"
               => durable[o] = "Intact"

\* Clearing A never touches B: a lane's durable moves only through its own
\* client carry; another lane's import must never write it.
OwnerIsolation ==
  \A o \in Lanes: durable[o] # prev[o]
               => lastCarried[o] \in {"Cleared", "Replaced"}

=============================================================================
