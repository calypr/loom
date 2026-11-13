-------------------------- MODULE ReleaseBindingTrust --------------------------

EXTENDS Naturals, Sequences

CONSTANT Exec,
         Name,
         intended,
         Recheck,
         MaxStamps,
         LegacyHas

ASSUME ExecOK == /\ Exec # {}
             /\ intended \in Exec
             /\ Recheck \in BOOLEAN
             /\ MaxStamps \in Nat
             /\ LegacyHas \subseteq Exec

VARIABLES published,
          publishedAt,
          bindings,
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

OrderKey(e) == IF e \in LegacyHas THEN 0 ELSE publishedAt[e]

PrepareRelease(e) ==
  /\ ~activated
  /\ published[e]
  /\ \A g \in Exec: published[g] => OrderKey(g) <= OrderKey(e)
  /\ bindings' = << [name |-> Name, execution |-> e] >>
  /\ UNCHANGED <<published, publishedAt, activated>>

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
