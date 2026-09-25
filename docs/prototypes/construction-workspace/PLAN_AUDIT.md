# Construction work-package audit

This audit reviews [F0](SPARSE_RECORD_FRAMING_WP.md), [P01–P09](WORK_PACKAGES.md), and the [framing contract](FRAMING_CONTRACT.md) against the user's goal: a researcher who does not write AQL or SQL can frame sparse, related FHIR records into a documented ClickHouse dataframe. The review started from commit `b1d52361a40847c05858e4a0d60e2ece679de53f` plus the planning edits in this worktree. Four read-only reviews covered source semantics, lifecycle, the researcher experience, and cross-cutting contracts. A fifth review tried to falsify the revised plan. Source inspection checked whether stated capabilities have an implementation path. No runtime behavior was claimed from this document audit.

The audit question for every package was: **Could its stated checks pass while the resulting frame is wrong, inaccessible, or unusable?** A finding is resolved in the plan only when one package owns the behavior and a completion check can fail if it is absent. This does not mean the code is implemented.

## Package verdicts

| Package | Main failure that the earlier checks missed | Revised closure evidence |
| --- | --- | --- |
| F0 | Three illustrative datasets could pass while a new FHIR shape or source state failed. | Held-out resource and path, sparse cases, multi-match forms, cross-engine chain, source-change and security checks. |
| P01 | Stage-local related data and semantic identity could be lost between steps. | Typed stage-local source addition and semantic stage descriptors; edit, reload, and composed compiler checks. |
| P02 | Correct controls could still be hard for a non-SQL researcher to use. | Exact Apply-state assertions and an independent task study with target users. |
| P03 | Discovery could expose unsupported time choices, combine nested values incorrectly, or reduce a code set to one selected result. | Paged multi-code selection, same-element binding, distinct contributors, list and numeric forms, metadata provenance, temporal role and precision, missing anchor, comparator, and dangling-reference checks. |
| P04 | Population filters and contributor filters could be confused. | Existing scoped-condition checks remain the gate; F0 and P09 add excluded-record evidence. |
| P05 | Wide output could appear complete while categories are dropped, null group keys merge silently, or only value lists can expand. | Category-change checks, high-cardinality long output, missing-key grouping, and a related-record expansion editor and check. |
| P06 | A same-engine join could pass while an AQL stage cannot feed a published input; vague pair-combination scope had no framing use case. | AQL group or expand, pinned ClickHouse input, later step, second Combine, explicit membership and append checks, and generation-stable input update. Unbounded pair combinations are removed from scope. |
| P07 | A metadata token could be mistaken for a source-data snapshot. | Immutable-generation proof or stale-preview refusal; exact input artifact, semantic dictionary, refresh value and contributor diff, authorized query and export. |
| P08 | A small benchmark or a reported miss could close a slow builder. | Fixed scale floor, standalone choice and discovery measures, and release latency gates. |
| P09 | A sampled or unavailable statistic could appear exact or zero. | Browser checks for denominator, exact or sampled status, all sparse cases, and authorized trace. |

## Disposition of the earlier findings

| Finding | Plan owner | Required evidence now in the plan |
| --- | --- | --- |
| Stage-local Add source | P01, P03, F0 | Add related columns after group and expansion through a typed stage operation. |
| Same repeated-element binding | P03, F0 | A coded condition and value on two different nested elements cannot cross-bind. |
| Duplicate graph paths | P03, P09, F0 | Distinct source-record counts do not grow when two paths reach one record. |
| Incompatible Combine alignment | P06 | Append or coalescence refuses incompatible code or unit identity without approved mapping. |
| Preview-to-publish source change | P07, F0 | Immutable source generation or refusal of a stale preview after a mutation. |
| Refresh value and contributor drift | P07, F0 | Refresh review catches changes that leave the schema unchanged. |
| Performance closure loophole | P08, F0 | A missed release target leaves both packages open. |
| Resource-specific implementation | F0 | Held-out resource and relationship path work without a new resource-specific branch. |
| Out-of-window sparse state | P03, P09, F0 | A record excluded by a window is distinguishable from no related record. |
| Vacuous numeric support | P03, F0 | Minimum, maximum, sum, and mean on compatible repeated numeric source values. |
| Semantic and coverage refresh drift | P07, F0 | Changed code system, unit, and coverage require review before replacement. |

## New findings from the full audit

| Finding | Why the earlier checks could pass | Plan owner and new check |
| --- | --- | --- |
| Independent researcher use | Browser automation proves control behavior, not that the target user can find and explain a sparse column. | P02 task study with five non-SQL domain researchers and a four-of-five independent completion threshold. |
| Apply-state proof | Mentioning failure and late responses does not prove the button is gated. | P02 asserts the disabled and enabled states for exact proposal identity. |
| Capability and discovery speed | Edit-to-preview timing can hide slow menu, field search, and coverage calls. | P08 measures these actions separately and gates warm structural choices. |
| Sampled evidence labeling | A coverage value can be correct for a sample but shown as exact. | P09 checks exact, sampled, and unavailable states with denominators. |
| List output | Other output forms could pass while a many-match list is missing. | P03 and F0 preview, publish, and reopen an ordered list with every contributor. |
| Transformed AQL-to-ClickHouse input | Combining two already published tables avoids the required engine boundary. | P06 and F0 run a transformed FHIR stage into a pinned published input, then another step. |
| Materialization pin | A construction revision can be republished on changed source data. | P06 pins generation and materialization; G1 remains until explicit G2 update. |
| FHIR security labels | Project or path access alone may not authorize every resource in that path. | F0, P03, and P07 use a resource-label policy; unknown labels fail closed; two principals test all read paths. |
| Positive availability claim | Withholding every “as of” claim could satisfy the earlier negative test. | P03 and F0 test a declared availability role and refuse an event or update time without that assertion. |
| Partial FHIR dates | A field can be advertised for a window and fail only at AQL preview. | P03 and F0 require an upfront reason or a saved precision policy. |
| Missing row-time anchor | One sparse row can abort the entire preview when the time anchor is null. | P03 and F0 keep the row with no eligible windowed contributors and report the missing anchor. |
| Quantity comparator | A bound such as `>100 mg` can be reduced as if it were exactly `100 mg`. | P03 and F0 preserve the comparator or reject point-value reductions before preview. |
| Missing group key | The compiler can collect null keys into one group without a saved user policy. | P05 and F0 require a visible absent/null-key policy and check group counts. |
| Dangling reference | A stored edge with no target can look identical to no reference. | P03, P09, and F0 report unresolved references when authorization permits. |
| Related-record expansion editor | The written Reshape editor only expands a repeated value field, while F0 requires rows for related graph records. | P05 and the submenu define path, parent identity, contributor, and empty-match choices, then verify the result. |
| Unjustified Combine mode | A generic “Make combinations” action had no F0 dataset need or acceptance check. | P06 and the submenu remove that mode; membership remains and gains a cohort inclusion/exclusion check. |
| Observed code-set selection | F0 requires a code set, while the Add columns menu and P03 check could pass with only one code. | P03 and the submenu select across pages, show member coverage and output mapping, and reopen exact code identities. |

## Source checks behind the highest-risk findings

The current [construction operation enum](../../../internal/explorer/authoringv2/construction.go) has Pivot, Derive, Filter, Unpivot, Group, Expand, and Combine, but no stage-local related-source operation. [Construction validation](../../../internal/explorer/authoringv2/construction_validation.go) and the [cross-engine lowering](../../../internal/dataframe/compiler/lower/recipe_construction_composite.go) still restrict Combine and reject a grouped prefix. These source facts explain why Checkpoints A and C require real compiled chains.

The [temporal choice type](../../../internal/explorer/authoringv2/semantic_types.go) has no availability role, and the [AQL time-window renderer](../../../internal/dataframe/compiler/render/aql/collections.go) rejects incomplete timestamp precision and missing anchors. The [Quantity model](../../../generated/fhir/model.go) has a comparator, while the [current reduction path](../../../internal/dataframe/semantic/recipe_rich_shaping.go) selects scalar values. The [ingestion edge path](../../../internal/ingest/documents.go) and [graph traversal](../../../internal/dataframe/compiler/render/aql/graph_render.go) do not establish a resolved target for every stored reference. These facts make the temporal, comparator, and dangling-reference tests necessary.

FHIR R4 permits partial `dateTime` values and a comparator on `Quantity` in its [datatype definitions](https://hl7.org/fhir/R4/datatypes.html). It distinguishes [resource update time](https://hl7.org/fhir/R4/resource-definitions.html) from a source field such as [Observation.issued](https://hl7.org/fhir/R4/observation-definitions.html), which describes when that observation version was available to providers. The plan uses metadata assertions so this distinction works for other resource types too.

The [scope resolver](../../../internal/authscope/scope_resolver.go) establishes project and resource-path scopes. FHIR R4 defines [`Meta.security`](https://hl7.org/fhir/R4/resource-definitions.html) as labels that connect resources to security policy. The risk from mixed labels is an inference when an institution uses those labels for access control. The revised plan makes the effective label policy explicit and tests it across discovery and published reads; it does not claim that a leak has been reproduced.

## Remaining implementation decisions

The plan sets safe defaults where implementation still needs a concrete mechanism. A source-data generation needs immutable records or a publish-time unchanged-data check. A published input is the exact materialization, not merely a table name or construction revision. Resource security labels require an explicit project policy; unknown labels are excluded. Partial dates cannot enter an exact-instant window without a declared interval or exclusion rule. A missing row-time anchor keeps its output row with no eligible windowed contributors. A comparator-bearing Quantity does not enter an exact point-value reduction without an approved interval-aware policy. These are implementation choices with acceptance checks, not permission to silently reduce the product scope.

P08's scale floor and latency targets are release criteria for this plan. The first baseline must record the local environment and may show that the target is hard to reach. If it does, P08 remains open while the team measures and improves the actual bottleneck. Changing the target requires a recorded product decision with the measured effect; a report of missed latency alone cannot close the package.
