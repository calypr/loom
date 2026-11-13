-------------------------- MODULE ReleaseBindingTrust --------------------------
(*
Models the F-B6 release-binding trust gap: the dataset release service binds a
publication by name-latest lookup (FindExecutionBySelector, bundle_registry.go
:174-182) echoed through the verifier (server/release_verifier.go:35-39) and written
verbatim by release CAS (release_store.go:175-178), while the explorer revision
records a distinct materialized execution. Recheck=TRUE models the proposed
activation-time re-verification of the bound execution against the intended
materialization.
*)

EXTENDS Naturals, Sequences

CONSTANT Exec,               \* set of execution identities
         Name,               \* single selector-visible name in contention
         intended,           \* execution the revision actually materialized
         Recheck,            \*_BOOLEAN: activation re-verifies binding identity
         MaxStamps,          \* bound on publish timestamps
         LegacyHas           \* SUBSET Exec: docs whose publish time is stored
                              \* only under a capitalised PublishedAt key

ASSUME ExecOK == /\ Exec # {}
             /\ intended \in Exec
             /\ Recheck \in BOOLEAN
             /\ MaxStamps \in Nat
             /\ LegacyHas \subseteq Exec

VARIABLES published,        \* [Exec -> BOOLEAN]
          publishedAt,      \* [Exec -> Nat], distinct among published
          bindings,         \* << [name |-> Name, execution |-> e] >> or << >>
          activated

DistinctTimestamps ==
  \A g1 \in Exec: \A g2 \in Exec:
    (published[g1] /\ published[g2] /\ g1 # g2) => (publishedAt[g1] # publishedAt[g2])

TypeOK == /\ published \in [Exec -> BOOLEAN]
          /\ publishedAt \in [Exec -> Nat]
          /\ DistinctTimestamps
          /\ bindings \in { << >> } \cup
               { << [name |-> Name, execution |-> e] >> : e \in Exec }
          /\ activated \in BOOLEAN

Init == /\ published = [e \in Exec |-> FALSE]
        /\ publishedAt = [e \in Exec |-> 0]
        /\ bindings = << >>
        /\ activated = FALSE

Publish(e) ==
  /\ ~published[e]
  /\ \E t \in 1..MaxStamps:
       /\ (\A g \in Exec: published[g] => publishedAt[g] # t)
       /\ published' = [published EXCEPT ![e] = TRUE]
       /\ publishedAt' = [publishedAt EXCEPT ![e] = t]
       /\ UNCHANGED <<bindings, activated>>

\* PrepareRelease+Create: name-latest selection (bundle_registry.go:174-182)
\* echoed verbatim (server/release_verifier.go:35-39) and bound (release_store.go:175-178).
\* SORT reads ONLY lowercase publishedAt (bundle_registry.go:179); a doc whose
\* publish time lives under a capitalised PublishedAt key sorts as absent, so
\* the name-latest predicate here zeroes its ordering key.
OrderKey(e) == IF e \in LegacyHas THEN 0 ELSE publishedAt[e]

PrepareRelease(e) ==
  /\ ~activated   \* production: prepared release targets a not-yet-active revision
  /\ published[e]
  /\ \A g \in Exec: published[g] => OrderKey(g) <= OrderKey(e)
  /\ bindings' = << [name |-> Name, execution |-> e] >>
  /\ UNCHANGED <<published, publishedAt, activated>>

\* PublishAuthoring activates the prepared release against the revision whose
\* materialized execution is `intended`. Recheck models the proposed guard:
\* activation aborts unless the bound execution IS the materialized one.
Activate ==
  /\ bindings # << >>
  /\ ~activated
  /\ Recheck => bindings[1].execution = intended
  /\ activated' = TRUE
  /\ UNCHANGED <<published, publishedAt, bindings>>

vars == <<published, publishedAt, bindings, activated>>

Next == \/ (\E e \in Exec: Publish(e))
        \/ (\E e \in Exec: PrepareRelease(e))
        \/ Activate
        \/ UNCHANGED vars

CorrectBinding ==
  ~activated \/ (bindings # << >> /\ bindings[1].execution = intended)

Spec == Init /\ [][Next]_vars /\ WF_vars(Activate)

=============================================================================
