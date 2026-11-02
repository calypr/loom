----------- MODULE ReleaseRevisionIdempotence -----------
(* Formal witness for confirmed bug F-B15 (dataset layer; dual e2e runs
   e00a... wait see FINDINGS rounds 10/13: uuids 22ffa92b + 25b5af79):
   the authoring server activates a release by reading the LIVE revision
   (internal/server/server.go:569-580 activateExplorerRelease pattern) and calling
   ReleaseService.Activate, which ALWAYS re-enters Create
   (internal/dataset/release.go:247 Create call; Create at :147). Create mints
   release.ID = releaseID(release, currentRevision + 1) folding the revision
   into the sha over (project, generation, REVISION, publications)
   (release.go:288-295 releaseID at shipped shape; the pre-fix fold hashed REVISION too and was removed at repair), so a byte-identical re-activation is never a
   no-op: revision churns 1->4, one immutable project_release document per
   click, both pointer rows churn. The AQL alreadyActive short-circuit needs
   the SAME releaseId (dataset/arango/release_store.go:162-163 alreadyActive), which
   Create-then-CAS callers never re-submit — structurally unreachable.

   Arms: ShippedNoopGuard=FALSE reproduces churn (identityOf folds revision;
   alreadyActive tests releaseId equality). ContentHashIdentity=TRUE models
   the fix shape: identity excludes revision, so the second identical
   activation hits alreadyActive and the store is unchanged. *)

   (* SOURCE SYNC (round 31 repair SHIPPED): releaseID is content-addressed
   (revision excluded, internal/dataset/release.go) matching
   ContentHashIdentity=TRUE; the activation CAS now hits alreadyActive with
   ZERO pointer writes (release_store.go: alreadyActive drops the
   expected-revision clause and update list becomes empty), and SaveRelease
   treats a re-save of identical content as the untouched existing document
   (createdAt excluded from the compatibility equality, keeping the original
   first-creation timestamp). m-rri-shipped stays the pre-fix fault control
   (Idempotence violated); m-rri-fixed = the validated shipped shape (CLEAN).
   e2e witness: revision_inflation PREVENTED (/tmp/opencode/fb15-after-green
   .log); pre-fix probe reproduced the CONFIRMED churn verbatim (/tmp/
   opencode/fb15-prefixprobe.log). *)

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS ShippedNoopGuard, ContentHashIdentity

ASSUME
  /\ ShippedNoopGuard \in BOOLEAN
  /\ ContentHashIdentity \in BOOLEAN

VARIABLES
  revision,      \* active release revision (0 == none activated)
  docs,          \* set of release document IDs created (grows unboundedly w/ bug)
  lastContent,   \* canonical content of the most recently activated release
  lastId,        \* releaseId of the most recently activated release
  clicks         \* number of identical activate requests issued

(* Local Nat->string: activations are bounded by clicks < 4, so a 1-digit
   table suffices and avoids a TLC-module dependency. *)
Digit == [n \in 0..9 |->
  CASE n = 0 -> "0" [] n = 1 -> "1" [] n = 2 -> "2" [] n = 3 -> "3" [] n = 4 -> "4"
    [] n = 5 -> "5" [] n = 6 -> "6" [] n = 7 -> "7" [] n = 8 -> "8" [] n = 9 -> "9"]
Decimal(n) == IF n <= 9 THEN Digit[n] ELSE "many"

SameContent == "CONTENT-A"   \* every click submits this byte-identical release

initDocId == "r0"
NoId == "NONE"

(* Identity constructor. Only SameContent is ever activated, so shipped shape
   (pre-fix: releaseID folded REVISION too; current release.go:288-295), is faithfully modeled as
   an identity that varies with revision; the fix shape (content-addressed) is
   an identity that ignores revision. Both are plain strings. *)
Identity(c, rev) ==
  IF ContentHashIdentity
     THEN "rel-" \o c                       \* revision excluded => stable across clicks
     ELSE "rel-" \o c \o "-" \o Decimal(rev)  \* revision folded in => new id each click

TypeInit ==
  /\ revision = 0
  /\ docs = {}
  /\ lastContent = "UNSET"
  /\ lastId = NoId
  /\ clicks = 0

(* Authoring click: server reads LIVE revision then activates the SAME byte
   release (internal/server/server.go:569-580 live-revision pattern). Always enters Create
   (release.go:247) — the caller-side no-op short-circuit does not exist. *)
ActivateClick ==
  /\ clicks < 4
  /\ LET newRev == revision + 1
         newId  == Identity(SameContent, newRev)
         alreadyActive == ShippedNoopGuard /\ newId = lastId   \* release_store.go:162-163 needs SAME id
     IN  IF alreadyActive
            THEN UNCHANGED <<revision, docs, lastContent, lastId, clicks>>
            ELSE /\ revision' = newRev
                 /\ docs' = docs \cup {newId}
                 /\ lastContent' = SameContent
                 /\ lastId' = newId
                 /\ clicks' = clicks + 1

(* pointer churn witness is implicit: revision changes => both pointer rows
   re-patched (release_store.go:163,175-178; alreadyActive arms skip writes); docs size is the durable-growth harm *)

Next == ActivateClick

Init == TypeInit
stvars == <<revision, docs, lastContent, lastId, clicks>>

(* The harm predicate (promise: activating an already-active, byte-identical
   release is a no-op): with 4 identical clicks the durable state must be the
   same as after the 1st click. *)
Idempotence ==
  ~(clicks = 4 /\ Cardinality(docs) > 1)

(* Secondary: revision must never advance on identical re-activation *)
RevisionStable ==
  ~(clicks = 4 /\ revision # 1)

FairSpec == Init /\ [][Next]_stvars /\ WF_stvars(ActivateClick)
=====================================================
