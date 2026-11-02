------------------------- MODULE ConstructionPrepareLanes -------------------------
(* Bounded lane-clearance state machine for the four row-operation prepare lanes
   (ROW-SOURCE-05 P0, shipped at b72a1322). One reachable state = the durable
   server-owned input presence after one persisted prepare proposal. Consumer
   and gap record: verification/publication-bundle/SKILL.md "Prepare-lane
   clearance". Existing durables (internal/explorer/lifecycle/
   filter_source_pivot_clear_test.go, 20 named tests) sample base x carry x
   order; this model quantifies over ALL of it.

    SOURCE SYNC (authored at 0dd7be44; re-synced at ebf97a40; RE-UPDATED at
    8459f080 (C1 SET-repair): the two frozen-copy loops gained the
    !constructionOwnerInputsClearedFromCandidate clause, so "unguarded" no
    longer describes the code — see the note below. Every other cited region
    re-verified byte-identical, shift-only, full-slice match vs ebf97a40; the
    S-B d3a1d12a / S-C 29d63f6f edits (rowSourceInventoryFailure verdict,
    constructionFieldDisplayLabel) live in the capability/discovery + resolve
    paths, outside every region below):
       shared helper          construction_pivot_source.go:763-793 (call sites
                              :510 pivot w/ skipOwner, derive_source.go:257
                              """, unpivot_source.go:325 """)
       cleared predicate      construction_pivot_source.go:804-830 (base inputs>0
                              /\ candidate carries none of the owner -> clear;
                              >0 carried -> omission -> repair; base empty -> no-op)
       pivot set-branch loop  construction_pivot_source.go:580-584 (guarded :581-582)
       filter set-branch loop filter_source.go:266-270 (guarded :267-268)
       derive frozen copy     derive_source.go:323-328 (guarded :324-325 since
       unpivot frozen copy    unpivot_source.go:391-396  8459f080; SET reattribution
                              derive_source.go:301-314 + cascade prune +
                              consumption validation keep the surviving
                              (non-cleared) re-imports out of durable state —
                              assumption A3, falsifiable by arm m-prep-persist-unconsumed)
      consumption validation construction_validation.go:348-376
      cascade prune            authoringv2/construction_change.go:525-530

    Pre-8459f080 both frozen loops lacked the cleared clause and the re-import
    of a cleared foreign owner's projection reached durable state (the C1
    defect; its durables are internal/explorer/lifecycle/
    filter_source_pivot_clear_test.go @8459f080). That is precisely the
    FSetBranchPersistsUnconsumed arm: the falsifier for the shipped guard —
    deleting the Go clause on DERIVE/UNPIVOT must make this model violate
    ClearedStaysCleared. Before 8459f080 the clean arm passed while the code
    persisted that re-import, i.e. A3 was false at the frozen sites and the
    clean pass described a shape the code did not have.
   Each Propose action is one request build (helper + lane rebuild + persist)
   treated atomic: the durable layer never observes a mid-request candidate.

   Carries mirror the wire contract "nil never re-attaches, [] clears" —
   Untouched = request field nil / client mirror; Omitted = partial chain
   carried (>0 items, repair expected); Cleared = [] (zero-carried with base
   presence => the :804-830 predicate fires); Replaced = client-supplied new
   signed facts (client intent, never counted as a server restore). *)

EXTENDS Naturals, TLC

CONSTANTS FHelperGateIgnoresCleared,      \* TRUE: shared helper restores even
                                          \*   cleared owners (pre-b72a1322 shape)
          FSetBranchGateIgnoresCleared,   \* TRUE: pivot/filter set-branch bulk
                                          \*   imports ignore the cleared gate
          FSetBranchPersistsUnconsumed,   \* TRUE: derive/unpivot frozen copies
                                          \*   ignore the 8459f080 cleared guard
                                          \*   and persist the foreign re-import
                                          \*   (pre-C1 shape; violates A3 by design)
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
\* ignores the gate. Helper faults on pivot owners (L # pivot, helper :772);
\* the two guarded set sites fault on every o # L (pivot :581-582, filter
\* :267-268); the frozen copies fault on every o # L and persist
\* (derive :324-325 / unpivot :392-393 shipped guard ignored = pre-8459f080).
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
               ELSE "Intact"                           \* repair (predicate :819-828 carried => not cleared)
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
\* cleared lane has baseInputs = 0 at :816-818 and can never regain items).
NoFalseRestore ==
  \A o \in Lanes: /\ durable[o] = "Intact"
                  /\ prev[o] = "None"
               => lastCarried[o] = "Replaced"

\* The rs05 fix must not eat preservation: prev Intact with a faithful or
\* partial-omission carry persists Intact (helper repair :776-790 re-attaches
\* missing owner steps + projections; per-lane re-attach at the four cited
\* sites). Fires under FSharedClearedGate.
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
