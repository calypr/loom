# Independent record-network and row fixture

These synthetic records and `oracle.json` specify literal authored data,
independent of Loom queries and preview responses. The reference helper follows only explicit
references and selected scalar fields; its passing tests establish fixture
consistency, **not compiler or browser acceptance**.

Run `node --test scripts/verify-ui/helpers/tests/graph-rows-fixture-oracle.test.mjs`.
A native/API driver can import `assertGraphRowsResult` and must map the actual
public result columns explicitly to the oracle shape, preserving every record
identity and duplicate. The helper sorts rows only; it does not sort values,
coerce nulls, discard fields, deduplicate or choose a first related record.

The unmodified default Helm recipe remains the separate import/editor input in
`docs/work-packages/record-network-and-rows/fixtures/default-dataframer.json`.
This fixture uses its existing ResearchSubject.subject → Patient and reverse
Observation.subject / Condition.subject relationships. The second Patient
occurrence uses Observation.performer; its raw relationship metadata must remain
explicit rather than inventing a clinical label. Ingestion must authorize these
exact catalog relationships before using them in an authored proposal.

| Subject | Patient | Observation IDs / scalar values | Condition IDs |
| --- | --- | --- | --- |
| rs-zero | p-zero | none | none |
| rs-one | p-one | obs-one / 7 | none |
| rs-many | p-many | obs-many-A / 11, obs-many-B / 13 | condition-A, condition-B, condition-C |
| rs-shared | p-many | obs-many-A / 11, obs-many-B / 13 | condition-A, condition-B, condition-C |

All observations have `performer = Patient/p-performer`; `rs-many` therefore
reaches `p-many` via subject and `p-performer` via observations/performer. These
must have distinct occurrence IDs and column bindings, even though both are
Patient. Observation statuses are `final`, `final`, `amended`, respectively.
Values are selected `valueInteger` scalars; this fixture defines no new FHIR
value interpretation or datatype coercion.

The source row identity is `[ResearchSubject.id]`. Optional relationships keep
four rows, lists `[11,13]`, count 2 and presence true for each shared patient
subject. Required Observation match keeps the same identity shape but removes
`rs-zero`, leaving three rows. The independent two/three branches never produce
six rows for `rs-many`: optional values stay lists in one row. A proposal for an
unsupported branch product must fail precisely and leave the four saved rows.

Deliberate RELATED_EXPAND uses `[input subject ID, related observation ID]`,
returning five matched pairs. The two `obs-many-A` pairs remain distinct;
globally unique Observation identity would be wrong. Preserve-parent adds
`[rs-zero,null]`; drop-missing excludes it. This operation is distinct from source
Rows.EXPANDED over `ResearchSubject.identifier[]`, whose identities include the
item index, and from its keep/drop empty-array policy. Exact extra rows for both
empty policies are recorded separately in the oracle.

Construction GROUP uses the complete tuple `[Patient.gender, ResearchSubject.status]`
and retains subject contributor IDs plus their observation values. The missing
key row is `[null,"active"]` under keep-missing; drop-missing excludes only that
row. A changed tuple that omits status would incorrectly merge `rs-many` with
`rs-shared`. Named source groups include three explicitly listed active subjects
and a deliberately empty named group; they are not field-key grouping.
Rows.GROUPS/FIELD remains unsupported.

List/count/presence expectations are exact. If capabilities offer FIRST, its
selected order and policy must be visible and asserted; this oracle does not
bless an arbitrary choice. A one-distinct-value policy accepts `obs-one`'s 7 and
must reject the two distinct values 11 and 13; it must not be presented as FIRST.
Numeric summaries are exercised only if offered by backend capabilities: sum 24
and mean 12 for each p-many subject, sum 7 and mean 7 for rs-one, with explicit
empty-input policy for rs-zero. No supported summary is inferred from this prose.

The native workflow supplement adds two ResearchStudy records with literal titles
`One study` and `Many study`. All subjects have the required R5 Study reference; `rs-zero` names an absent
`ResearchStudy/study-unmatched` target while the other three references resolve.
The native graph can add the previously absent source Study relationship without
a resource cycle. Optional retains four rows; required retains the three known
Study matches. `nativeStudy` records exact IDs and title lists. The separate
Observation/performer Patient path remains an explicit unsupported-cycle probe
under the first-release policy; its distinct source occurrences must never be
collapsed or guessed as the subject Patient.

The default-import supplement provides one DocumentReference, one Specimen, one
MedicationAdministration (selected dosage scalar 5), and a Group with the two
explicit Patient members p-one and p-many. This makes all five default outputs
populated and independently checkable without altering the reference recipe.
`defaultOutputIdentities` records the exact source IDs and expanded Group/member
pairs. These extra roots do not change any ResearchSubject/Observation/Condition
expectation. The document URL is a synthetic nonresolving fixture value; native
verification never downloads it.

With Observation as the source root, `subject` and `performer` are two sibling
Patient occurrences. The literal `observationPatientOccurrences` rows explicitly
retain p-one/p-many separately from p-performer for all three observations.
This is an acyclic editable source-network witness. Existing pinned native
recipes can also contain the longer ResearchSubject/Patient/Observation/Patient
path; preserving those legacy source semantics does not authorize a new cyclic
overlay addition.

Production-ingestion validation requires Condition.clinicalStatus,
Observation.code, MedicationAdministration.medication (R5 CodeableReference),
and ResearchSubject.study. The fixture supplies structural `text` concepts or
one synthetic Coding per Observation (`urn:loom:graph-rows:synthetic-observation`),
not clinical interpretation. Medication uses the `concept` arm and preserves the
already selected dosage scalar. These required metadata additions do not change
any authored expected row, match count, or numeric value. The zero Study case is
an explicit dangling reference, not a missing required Reference property.
