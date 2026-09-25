# Construction workspace design study

- [x] Scope the decision the prototype exists to make: which layout, which interaction, which density, or for an empirical fork which behavior, timing, or approach. No decision means no prototype. Route to Feature.
  Decision: organize valid actions around the current table and selection, then compare a persistent toolbar with a compact task menu. Show how delayed preview affects editing.
- [x] Gather references when the design space is open. Search for prior art, summarize a moodboard of themes, palettes, and layouts, let the user pick directions before building. Skip when the direction is set.
  skip: the conversation establishes a table workspace with editable construction history.
- [x] Build throwaway in an isolated scratch dir, separate from production source. For a visual decision, vanilla HTML/CSS/JS or the lightest stack that renders the idea, CDN deps, a dev server with hot reload. For a behavioral or timing decision, the smallest script that exercises the question by observing it.
- [x] When comparing alternatives, build them behind one switcher (buttons or a keypress), each variant labeled.
- [x] Verify on the matching surface. Screenshot each variant and drive the interaction.
  Chrome exercised 22 states. See observations.json and the six screenshots. Both entry layouts, the wide and narrow editor, and dependency removal were visually inspected.
- [x] Present alternatives, tradeoffs, and a recommendation.
  The user accepted visible actions, the table navigation, the proposed-change editor, and editable steps. DESIGN.md records that choice.

## Accepted-design follow-up

- [x] Add explicit step removal, affected-step review, Cancel, and Undo to the standalone prototype.
- [x] Verify removal preserves unrelated steps and Undo restores the previous construction in Chrome.
- [x] Specify the intentions, question order, and information requirements within every action family.
- [x] Trace frontend and backend gaps against the current implementation baseline, preserving existing capability and shape-editing work.
- [x] Record focused backend verification and complete the source-reference review.

Throughput checkpoint: the root owns prototype and design documents. One focused backend investigator owns a separate temporary report. Existing UI and backend tests validate reuse claims. No production implementation is part of this design-analysis task.

## Work-package preparation

- [x] Record the user's clarified input-version behavior, successful-preview requirement, calculation defaults, discovery evidence, and ClickHouse publication goal.
- [x] Identify concrete recipe/semantic/physical-plan constraints on intermediate results and repeated reshaping.
- [x] Distinguish immutable explicit groups from arbitrary grouping over derived columns.
- [x] Write packages with contracts, dependencies, source ownership, and executable completion checks.
- [x] Include preview performance from the beginning, with a baseline, frozen workloads, correctness checks, and measured keep/revert decisions.
- [x] Record automatic preview after a short pause, cancellation of superseded requests, and Apply gated by the latest successful preview.
