# GRAPH-ROWS compact layout comparison

Retain the network beside the preview at desktop width. On the same actual
ResearchSubject default-recipe output, the stacked alternative sacrifices too
much result height at 1536×900. Its wider table exposes more columns, but only
one of the four actual source rows fits without internal scrolling. The retained
layout shows all four identities while preserving a bounded network inspector.

This is a bounded presentation experiment, not another full lifecycle run.
The isolated review checkout served the real app on Vite port 4189 against the
existing saved native fixture `loom_dev_verify_gr-mwe8pl3n-0` / `dataframe-default`.
That fixture uses the unmodified immutable reference recipe; the earlier Study
relationship has a removal tombstone and exact baseline values were restored.
The actual catalog, route occurrences, 18 source steps, fields, four preview
records and current row meaning were identical for both arrangements.
The ResearchSubject occurrence was selected with a native click. A CSS-only
stacked alternative placed a bounded 200px network above the full-width preview.
No handler calls, injected authoring events or authored-state edits were used.
The /authoring/v2/commands route was guarded; zero commands were attempted and
zero browser errors occurred. The four relevant production UI source hashes
match the parent's integrated checkout and are recorded in comparison.json.

| Viewport | Arrangement | Network width × height | Preview width × height | Fully visible actual rows |
| --- | --- | --- | --- | --- |
| 1536×900 | Beside, retained | 523×322 | 981×330 | 4 |
| 1536×900 | Stacked candidate | 1512×200 | 1512×122 | 1 |
| 1536×1100 | Beside, retained | 523×518 | 981×526 | 4 |
| 1536×1100 | Stacked candidate | 1512×200 | 1512×318 | 4 |

The stacked candidate falls below the registered native useful-preview minimum
of 150px at the shorter viewport. The narrower retained table requires bounded
horizontal table navigation to see later columns; that is preferable to hiding
most source records during relationship inspection. Both variants keep the
existing readable text size and actual metadata. Neither produces horizontal
page overflow. The document reports a two-pixel vertical rounding overflow in
both arrangements; this comparison does not claim a zero-overflow proof.

Returning from 1100px to 900px restored the retained preview to 326px and all four
rows remained visible. Rendering details are in comparison.json; four PNGs show
both arrangements at both viewport heights. The actual native lifecycle evidence
continues to be imported6-native-report.json, ordinary1-native-report.json and
related2-native-report.json, rather than this CSS experiment.

Command: `node work/graph-rows-layout-comparison/compare.mjs`
Exit: 0. Raw output: run.log. Generated source: compare.mjs.
