----------------------------- MODULE NullPageCoverage -----------------------------
EXTENDS Integers, FiniteSets

CONSTANTS Rows, First, Fixed

valueOf == [i \in Rows |-> IF i \in {3, 4} THEN 0 ELSE i]

Rank(v) == IF v = 0 THEN 9 ELSE v

Better(r1, r2) ==
  IF valueOf[r1] # valueOf[r2]
  THEN Rank(valueOf[r1]) < Rank(valueOf[r2])
  ELSE r1 < r2

VARIABLES visited, curVal, curID, done

vars == <<visited, curVal, curID, done>>

Init ==
  /\ visited = {}
  /\ curVal = -1
  /\ curID = 0
  /\ done = FALSE

LegacyMatches(r) ==
  IF curVal = -1 THEN TRUE
  ELSE IF valueOf[r] = 0 THEN FALSE
  ELSE IF valueOf[r] > curVal THEN TRUE
  ELSE IF valueOf[r] = curVal THEN r > curID
  ELSE FALSE

FixedMatches(r) ==
  IF curVal = -1 THEN TRUE
  ELSE IF Rank(valueOf[r]) > Rank(curVal) THEN TRUE
  ELSE IF Rank(valueOf[r]) = Rank(curVal) THEN r > curID
  ELSE FALSE

Matches(r) == IF Fixed THEN FixedMatches(r) ELSE LegacyMatches(r)

PageRows ==
  { P \in SUBSET Rows :
      /\ \A r \in P : Matches(r)
      /\ Cardinality(P) =
           IF Cardinality({ r \in Rows : Matches(r) }) <= First
           THEN Cardinality({ r \in Rows : Matches(r) })
           ELSE First
      /\ \A q \in Rows : Matches(q) /\ ~ (q \in P) =>
           \A p \in P : Better(p, q) }

LastOf(P) == CHOOSE r \in P : \A q \in P : ~ Better(q, r)

Next ==
  (\/ ~ done
    /\ \E P \in PageRows :
        /\ P # {}
        /\ visited' = visited \cup P
        /\ curVal' = valueOf[LastOf(P)]
        /\ curID'  = LastOf(P)
        /\ done'   = (Cardinality({ r \in Rows : Matches(r) }) <= First))
  \/ (done /\ UNCHANGED <<visited, curVal, curID, done>>)

NoOverserve == \A r \in visited : r \in Rows

CoverageComplete == done => visited = Rows

Spec == Init /\ [][Next]_vars

Terminates == <><> done

THEOREM Spec => (NoOverserve /\ Terminates)
=====================================================================================
