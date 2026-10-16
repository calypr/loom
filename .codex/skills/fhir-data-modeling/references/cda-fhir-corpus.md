# Open CDA-FHIR corpus evidence

These counts describe `/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META` on 2026-09-19. They are corpus evidence, not FHIR invariants.

The scan read 2,055,978 resources from 11 NDJSON files. The corpus uses R5 structures. `Substance.code` uses `CodeableReference`, and Medication Administration uses the R5 resource shape.

## Structural pairings

- The corpus contains 2,430,173 Identifier objects with both `system` and `value`.
- No scanned Identifier lacks either member.
- The 18 identifier namespaces include `https://proteomic.datacommons.cancer.gov/pdc/case_id` with 3,953 values.
- The corpus contains 2,513,241 `part-of-study` extensions with `valueReference`.
- It contains 129,388 birth-sex extensions with `valueCode`.
- It contains 71,339 race or ethnicity extensions with `valueString`.
- No scanned extension object lacks a value arm.

These pairings are directly authorable. A missing Loom compiler branch is an `UNSUPPORTED` state, not an `AMBIGUOUS` state.

## Observation shapes

The corpus contains 815,261 Observations.

- 37,076 use root `valueQuantity`.
- 28,816 use root `valueCodeableConcept`.
- 6,864 use root `valueString`.
- 742,505 omit the root value and contain 1,592,147 coded components instead.
- Component values include 1,389,952 `valueString` values and 202,195 `valueInteger` values.
- Every scanned component has a code and one value arm.

The three component concepts are `specimen_type`, `primary_disease_type`, and `days_to_collection`. Each component code and value belongs to the same component object.

## Coded result warning

All 28,816 `valueCodeableConcept` results contain codings and no `text`. A selector that reads only `valueCodeableConcept.text` loses every result in this corpus.

The corpus also reuses one SNOMED system and code with many different displays. For example, code `1222593009` appears with displays such as `Stage I`, `Stage IIA`, and `Stage IV`. FHIR defines `display` as the representation of a code, so these instances do not provide distinct standardized result codes.

For an ML dataframe, Loom may preserve the observed display as a dataset category. Loom must attach a data-quality warning and must not claim that the display is a standardized coded identity. A terminology service cannot repair an instance whose result code does not distinguish the categories.

## Quantity evidence

All 37,076 root quantities use `http://unitsofmeasure.org|d` with the display `days`. The structure permits a coded unit conversion policy. Structural validity does not establish clinical plausibility, so range checks remain a separate data-quality concern.

## References

The corpus uses References heavily. Common paths include `subject`, `focus[]`, `specimen`, `parent[]`, `study`, `partOf[]`, and extension `valueReference`. The path supplies the relationship role. The reference target alone does not.

## Reproduce the scan

The local audit scanner is `.audit/c01-implementation-20260919/fhir_corpus_scan.go`. Run:

```bash
go run .audit/c01-implementation-20260919/fhir_corpus_scan.go /Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META
```

The scanner records counts, systems, paths, value arms, and bounded examples. It does not retain resource IDs or Identifier values.
