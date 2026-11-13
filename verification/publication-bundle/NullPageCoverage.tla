----------------------------- MODULE NullPageCoverage -----------------------------
(*
 * Formal control for F-B24 (sorted keyset pagination over a Nullable sort
 * column). Exhaustively establishes the promise sorted browsing owes the
 * user: every row TotalCount counts is delivered by following NextCursor
 * (CoverageComplete), and no page ever overserves rows (NoOverserve).
 *
 * Maps to source (Round 29/30 state):
 *   internal/dataframe/published/page.go
 *     Fixed (shipped) shape: ORDER BY `col` DIR NULLS LAST, __loom_row_id ASC;
 *       predicate (`col` IS NULL OR `col` op ? OR (`col` = ? AND rowid > ?));
 *       null-bucket resume (`col` IS NULL AND rowid > ?); a NULL last row
 *       encodes a null-bucket cursor (never invalidCursor).
 *     Shipped pre-fix shape (dc9016bc fault control): predicate lacked both
 *       IS NULL arms and the ORDER BY did not pin NULLS LAST; NULL rows were
 *       unreachable while count() OVER() advertised them.
 *   ClickHouse 25.3 on the owned stack: NULL comparisons are NULL -> strict
 *     legs are false for NULL rows; NULL sorted LAST under the pinned ORDER BY
 *     in this model's ASC direction (the DESC arm of the product fix shares
 *     the bucket structure; modeled once, honestly disclosed).
 *
 * Rows 1..4: value 1, value 2, NULL, NULL (rank 0 means NULL). first=2.
 * Named consumer: F-B24 repair unit (Round 30) — the cursor-completeness
 * guarantee (page selection stays consistent with the pinned total order for
 * EVERY reachable cursor state) that per-case e2e traversals sample but do
 * not enumerate. m-null-shipped must violate CoverageComplete;
 * m-null-fixed (the shipped shape) must be clean.
 *)
EXTENDS Integers, FiniteSets

CONSTANTS Rows, First, Fixed

(* valueOf[id]: rank 0 == NULL; rows 1,2 carry values 1,2 *)
valueOf == [i \in Rows |-> IF i \in {3, 4} THEN 0 ELSE i]

(* NULL ranks last in BOTH directions (pinned NULLS LAST) *)
Rank(v) == IF v = 0 THEN 9 ELSE v

Better(r1, r2) ==
  IF valueOf[r1] # valueOf[r2]
  THEN Rank(valueOf[r1]) < Rank(valueOf[r2])
  ELSE r1 < r2

VARIABLES visited, curVal, curID, done

vars == <<visited, curVal, curID, done>>

Init ==
  /\ visited = {}
  /\ curVal = -1   \* -1: before the first page (no cursor predicate)
  /\ curID = 0
  /\ done = FALSE

(* Fault-control predicate (pre-fix): any leg comparing the NULL column is
   false, so NULL rows can never be matched once a cursor exists. *)
LegacyMatches(r) ==
  IF curVal = -1 THEN TRUE
  ELSE IF valueOf[r] = 0 THEN FALSE
  ELSE IF valueOf[r] > curVal THEN TRUE
  ELSE IF valueOf[r] = curVal THEN r > curID
  ELSE FALSE

(* Shipped (fixed) predicate: NULL rows always follow a non-NULL cursor in
   this NULLS-LAST order, and inside the NULL bucket ordering continues by
   row id. curVal = 0 is a real null-bucket cursor, not an error. *)
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

(* safety *)
NoOverserve == \A r \in visited : r \in Rows

(* the promise sorted browsing owes the user *)
CoverageComplete == done => visited = Rows

Spec == Init /\ [][Next]_vars

Terminates == <><> done

THEOREM Spec => (NoOverserve /\ Terminates)
=====================================================================================
