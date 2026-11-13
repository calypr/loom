----------- MODULE ReleaseRevisionIdempotence -----------


EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS ShippedNoopGuard, ContentHashIdentity

ASSUME
  /\ ShippedNoopGuard \in BOOLEAN
  /\ ContentHashIdentity \in BOOLEAN

VARIABLES
  revision,
  docs,
  lastContent,
  lastId,
  clicks

Digit == [n \in 0..9 |->
  CASE n = 0 -> "0" [] n = 1 -> "1" [] n = 2 -> "2" [] n = 3 -> "3" [] n = 4 -> "4"
    [] n = 5 -> "5" [] n = 6 -> "6" [] n = 7 -> "7" [] n = 8 -> "8" [] n = 9 -> "9"]
Decimal(n) == IF n <= 9 THEN Digit[n] ELSE "many"

SameContent == "CONTENT-A"

initDocId == "r0"
NoId == "NONE"

Identity(c, rev) ==
  IF ContentHashIdentity
     THEN "rel-" \o c
     ELSE "rel-" \o c \o "-" \o Decimal(rev)

TypeInit ==
  /\ revision = 0
  /\ docs = {}
  /\ lastContent = "UNSET"
  /\ lastId = NoId
  /\ clicks = 0

ActivateClick ==
  /\ clicks < 4
  /\ LET newRev == revision + 1
         newId  == Identity(SameContent, newRev)
         alreadyActive == ShippedNoopGuard /\ newId = lastId
     IN  IF alreadyActive
            THEN UNCHANGED <<revision, docs, lastContent, lastId, clicks>>
            ELSE /\ revision' = newRev
                 /\ docs' = docs \cup {newId}
                 /\ lastContent' = SameContent
                 /\ lastId' = newId
                 /\ clicks' = clicks + 1


Next == ActivateClick

Init == TypeInit
stvars == <<revision, docs, lastContent, lastId, clicks>>

Idempotence ==
  ~(clicks = 4 /\ Cardinality(docs) > 1)

RevisionStable ==
  ~(clicks = 4 /\ revision # 1)

FairSpec == Init /\ [][Next]_stvars /\ WF_stvars(ActivateClick)
=====================================================
