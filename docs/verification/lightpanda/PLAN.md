# Lightpanda adoption experiment

## Throughput revision

The user challenged the initial serial test as insufficient to evaluate
Lightpanda's strength. Serial fixture latency is not a throughput result.

- [x] Build persistent worker pools and a bounded, deterministic job scheduler.
- [x] Give Lightpanda independent processes so workers can use several cores.
- [x] Keep DOM semantic contracts separate from native input/download acceptance.
- [x] Use the same production release bundle, immutable assets and job assertions on both engines.
- [x] Explore concurrency at 1, 4, 8, 16 and 32; inspect driver CPU and profile the limiting path.
- [x] Confirm chosen settings with five alternating batches per engine, with only one engine resident.
- [x] Prove wrong rows, wrong ownership and evidence-write failures do not qualify; verify owned processes exit.
- [x] Prepare the existing PR update with measured results, setup corrections and exact coverage boundaries.

The workload is 64 independent jobs drawn from two named semantic contracts,
not 64 distinct restored tests. Every job owns a worker context while it runs,
has its own HTML identity, checks literal production DOM output, and retains
reload assertions. Workers retain browser/page/cache state across jobs and clear
caller storage before the next job. No layout or native pointer assertion is
silently replaced by a DOM assertion.

Only one engine executes at a time. More browser slots than logical CPUs are
exploratory I/O concurrency, not a claim to additional CPU cores. The supervisor
records its own CPU utilization so a saturated driver cannot be called an
engine limit. Tuning samples select configuration; separate confirmation samples
decide performance. Build, startup, warmup, steady work and shutdown are separate.

The original serial plan below is historical. Its measurements remain scoped
to their original workload and cannot reject concurrent throughput adoption.

Goal: recover useful browser regression coverage from PR #32's history without
restoring duplicate journeys or slowing the normal test loop.

## Completion predicate

The branch starts at `899f3bb6b757f826916870c5dac5c581719df4f5`.
Every current executable Playwright test and historical browser spec receives a
coverage disposition. Selected tests use real production components and retain
their observable assertions. No passing timing may include a failed, skipped,
or incomplete journey. Adopt Lightpanda and restore the selected suite only if
equivalent representative journeys are over 5 times faster end to end than
Chromium. Otherwise retain the experiment and explain the rejection in the PR.

## Work plan

- [x] Read the poteto-mode principles and repository instructions.
- [x] Create an isolated branch from the verified PR tip.
- [x] Inventory current and deleted tests; group by guarantee rather than filename.
- [x] Build a bounded compatibility and timing runner before restoring tests.
- [x] Capture an unchanged Chromium baseline and Lightpanda pilot.
- [x] Run five alternating trials per engine for the compatible exact-row load/reload candidate.
- [x] Keep four candidates opt-in. Reject adoption and broad restoration because compatibility and speed gates fail.
- [x] Verify failures remain failures; check source identity and cleanup.
- [x] Review the final diff and evidence solo. Independent review is omitted at the user's explicit request.
- [x] Prepare the complete experiment for commit and a PR against PR #32's branch.

Delivery is the local commit and the PR against `feature/deep-clean`. The PR
itself is the publication receipt; this plan records the work before publication.

Rigor is high for evidence equivalence and measurement validity, moderate for
the reversible opt-in runner. No production behavior or deployment changes.
Browser runs are serial to avoid shared CPU interference. The user requested a
solo mission, so review and investigation remain in the foreground.

The first checkpoint of the historical plan was real-component compatibility. A failure changes the
work to an auditable adoption rejection rather than a weaker replacement test.
The second checkpoint is measured end-to-end speed. Browser startup and journey
time are reported separately so a startup improvement cannot disguise slow
application behavior. Build time is recorded separately and identical on both
sides. Generated evidence stays outside Git.
