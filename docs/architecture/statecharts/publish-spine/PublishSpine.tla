---- MODULE PublishSpine ----
\* Abstraction of source-load publication after atomic receipt completion:
\*   ingest/sourceload/publisher.go       (load record, claim lease, complete, supersede)
\*   dataset/manifest.go, dataset/arango   (manifest LOADING->STAGED; release CAS, idempotent re-activation)
\*   dataframe/publication(+clickhouse)    (bundle QUEUED/RUNNING->VALIDATING->PUBLISHED|FAILED)
\*   server/sourcepublication/adapter.go   (materialize, create release, PublishProjectConfigs)
\*   explorer/arango/publicationdb         (release pointer + explorer revisions in one transaction)
VARIABLES load, worker, manifest, bundle, release, revision, everLive
vars == <<load, worker, manifest, bundle, release, revision, everLive>>

Init == /\ load = "uploading" /\ worker = "none" /\ manifest = "loading"
        /\ bundle = "none" /\ release = "previous" /\ revision = "none" /\ everLive = FALSE

FilesCopied == load = "uploading" /\ load' = "ready"
    /\ UNCHANGED <<worker, manifest, bundle, release, revision, everLive>>

\* claimSourceLoadPublication: state IN [READY, PUBLISH_PENDING] and lease expired
Claim == load \in {"ready", "pending"} /\ worker = "none"
    /\ load' = "pending" /\ worker' = "working"
    /\ UNCHANGED <<manifest, bundle, release, revision, everLive>>

\* the worker crashes, or renewSourceLoadPublication fails and cancels the context
LeaseLost == worker # "none" /\ worker' = "none"
    /\ UNCHANGED <<load, manifest, bundle, release, revision, everLive>>

\* finalizeSourceLoadCandidate + CompleteSemanticInventoryBuild + TransitionManifest(STAGED)
Stage == worker = "working" /\ manifest = "loading" /\ manifest' = "staged"
    /\ UNCHANGED <<load, worker, bundle, release, revision, everLive>>

BuildDataframe == worker = "working" /\ manifest = "staged" /\ bundle \in {"none", "failed"}
    /\ bundle' = "running"
    /\ UNCHANGED <<load, worker, manifest, release, revision, everLive>>

FinishWriting == bundle = "running" /\ worker = "working" /\ bundle' = "validating"
    /\ UNCHANGED <<load, worker, manifest, release, revision, everLive>>

PassValidation == bundle = "validating" /\ worker = "working" /\ bundle' = "published"
    /\ UNCHANGED <<load, worker, manifest, release, revision, everLive>>

\* Publish returns the error; the deferred release drops the claim
BuildFails == bundle \in {"running", "validating"} /\ worker = "working"
    /\ bundle' = "failed" /\ worker' = "none"
    /\ UNCHANGED <<load, manifest, release, revision, everLive>>

\* PublishProjectConfigs: CAS, views and the owned source-load receipt commit together.
GoLive == load = "pending" /\ worker = "working" /\ bundle = "published" /\ release \in {"previous", "this"}
    /\ release' = "this" /\ revision' = "active" /\ worker' = "none" /\ everLive' = TRUE
    /\ load' = "published"
    /\ UNCHANGED <<manifest, bundle>>

\* ErrReleaseActivationConflict -> supersedeSourceLoad
FindNewerRelease == worker = "working" /\ bundle = "published" /\ release = "newer"
    /\ load' = "superseded" /\ worker' = "none"
    /\ UNCHANGED <<manifest, bundle, release, revision, everLive>>

\* another source load for the same project wins the release CAS
OtherLoadGoesLive == release \in {"previous", "this"}
    /\ release' = "newer"
    /\ revision' = (IF revision = "active" THEN "superseded" ELSE revision)
    /\ UNCHANGED <<load, worker, manifest, bundle, everLive>>

Done == load \in {"published", "superseded"} /\ worker = "none" /\ release = "newer"
    /\ UNCHANGED vars

Next == FilesCopied \/ Claim \/ LeaseLost \/ Stage \/ BuildDataframe \/ FinishWriting
     \/ PassValidation \/ BuildFails \/ GoLive \/ FindNewerRelease
     \/ OtherLoadGoesLive \/ Done

\* Rules the reader agrees to
LiveReleaseHasPublishedData == release = "this" => (manifest = "staged" /\ bundle = "published")
ViewsFollowRelease == (revision = "active") <=> (release = "this")
PublishedLoadWentLive == load = "published" => everLive
SupersededLoadNeverWentLive == load = "superseded" => ~everLive
LiveLoadHasReceipt == everLive => load = "published"
====
