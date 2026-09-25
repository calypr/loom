# ML dataset construction contract

This contract refocuses the construction workspace on the researcher's dataset, rather than on a menu of dataframe operators. The table-first workspace and editable steps remain. The operation families are implementation vocabulary and optional advanced actions; they are not the product's success criteria.

## Product promise

Loom turns sparse, related institutional records into a reproducible model input. The researcher can define what one example represents, gather signal from the available records, choose an output representation, inspect whether the result is usable, and publish the exact reviewed construction. A target and a prediction time are optional because descriptive, unsupervised, and non-temporal ML datasets also matter.

Generality comes from typed, composable table operations. The interface organizes those operations around dataset decisions. A calculation, filter, join, or pivot earns a place when it answers a dataset question; merely exposing the operator does not make a model-ready dataset.

## Dataset decisions in the workspace

| Researcher decision | What Loom must make explicit | Underlying operations |
| --- | --- | --- |
| Define examples | Row unit, entity key, repeated examples, optional anchor time, population and eligibility | Select root/table, group, filter, expand |
| Add signal | Source meaning, relationship, contributors, lookback/window, reduction, multiplicity, output type, absence semantics | Traverse/join, count, aggregate, derive, pivot, combine |
| Define outcome when applicable | Label source, horizon, ascertainment, censoring/unknown state, separation from predictor windows | Related-record match, filter, derive, aggregate |
| Choose model representation | Wide columns, long events, sequences, categorical and numeric forms, stable schema | Reshape, encode, expand, append |
| Review readiness | Row identity and duplication, source and output-row coverage, sparsity, class balance when labeled, time leakage, schema and provenance | Profiles, lineage, comparison, validation |
| Publish | Saved construction and pinned input versions, exact schema and rows, query/export continuity | Compile, materialize, reopen, inspect |

The same source can yield a value, presence flag, count, summary, trend, or event sequence. Those are meaningful feature choices. Missing source records, recorded nulls, zero values, and values outside a time window remain distinguishable. Loom must not silently turn one into another.

## Interaction priorities

The primary path is **Define examples → Add signal → Review readiness → Publish**. **Define outcome** appears when a labeled task is intended. The selected table and editable history stay visible throughout. Advanced table actions remain available for unusual workflows, but the primary Add signal flow must cover related sources, code discovery, aggregation, time windows, and missingness decisions.

Keep rows becomes an eligibility or feature-contributor decision with an explicit scope: exclude an example, select contributing records, or define an outcome. Calculate belongs inside feature or outcome authoring when values need combining, recoding, or transformation. Combine is a way to bring another versioned table's signal into the current result, and its output remains transformable. Reshape chooses a model representation. These controls may share backend primitives without sharing a top-level menu label.

## ML-readiness evidence

Before publication, show the scope and completeness of each number:

- Number of examples; distinct entities; duplicate row identities; examples per entity.
- Feature coverage over output rows, source-record availability, missing/recorded-null/zero counts, and variation across relevant subgroups or time periods where that evidence is available.
- Contributor multiplicity and the effect of each reduction or matching policy; row growth or loss after an edit.
- For labeled data: label counts, unknown/censored labels, observation and outcome windows, and predictors whose source time follows the allowed observation boundary.
- Schema, types, units, code sets, pinned source/table versions, and lineage back to contributors, with uncertainty or unavailable checks labeled honestly.
- If data is split for model evaluation: entity overlap and time leakage across partitions; imputation or statistics learned from data must be scoped to the training partition.

A warning may be appropriate where a check cannot prove safety. The UI must not label a dataset “ML ready” merely because it has rows and no compiler error. Publication requires the exact reviewed construction and input versions; evidence may load separately from the fast row preview.

## Acceptance gates across work packages

1. A researcher can define the row unit and, when needed, an anchor without writing SQL. The table displays that meaning and keeps it through edits and publication.
2. Add signal can use related records and another constructed table, and can express presence, count, reduction, representative value, or repeated output where supported. The available forms come from Loom before Apply.
3. Feature and outcome windows can be expressed independently when time matters. The preview explains which records contributed and which were excluded by time or policy.
4. Sparse data remains honest: absence, null, and zero do not collapse; coverage uses a declared output-row denominator; changes in coverage and row count are visible before Apply.
5. An applied operation can be reopened and edited; later operations can consume its output, including after Combine. The exact accepted construction survives reload and publishes to ClickHouse.
6. A readiness review exposes the checks above with their scope and limits, and the published table can be queried/exported with the same schema and values that were reviewed.

These are structural checks, not a list of hardcoded clinical datasets. Verification should vary row unit, relationship shape, feature form, time behavior, sparsity, and output representation so one fixture cannot satisfy the contract by accident.

## Work-package correction

P01 owns the typed, composable construction model. P02 owns the table-first workspace and shared proposal lifecycle. P03 owns source discovery and Add signal, including related records and coverage. P04 owns scoped eligibility, feature rules, and expressions; standalone Filter and Calculate menus do not complete it. P05 owns output representation, including group/reshape/expansion. P06 owns exact versioned table inputs and the execution boundary, not a separate Combine product feature. P07 owns reviewed construction through publication and inspection. P08 owns measured responsiveness. **P09 owns ML-readiness evidence and the optional outcome/time contract**, integrating existing profiles and provenance with the new stage model. P03, P04, P06, and P07 must expose the evidence P09 needs; P09 cannot be a cosmetic final screen.

The existing operation work is reusable, but completing an operator alone does not close its work package. The current terminal-only Combine support, arithmetic-only formula editor, and isolated Filter path are partial primitives. They must be evaluated against the acceptance gates above before being presented as finished dataset-building capabilities.

## External design checks

[OHDSI FeatureExtraction](https://ohdsi.github.io/FeatureExtraction/articles/UsingFeatureExtraction.html) organizes feature construction around a cohort, source concepts, and observation windows; its [study-population contract](https://ohdsi.github.io/PatientLevelPrediction/reference/createStudyPopulation.html) makes example identity, index time, outcome, and risk window explicit. The [TRIPOD+AI supplement](https://www.tripod-statement.org/wp-content/uploads/2024/04/TRIPODAI-Supplement.pdf) calls for reporting predictor timing, missingness, and leakage across evaluation partitions. These are checks on Loom's product focus, not a restriction to OHDSI data or supervised prediction.
