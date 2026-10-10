# Ancestor-aware extension bindings

## Problem

The catalog distinguishes `parent:left/leaf` from `parent:right/leaf`, but the legacy authoring lookup matches the leaf URL alone. A new writable binding must retain ancestry and a checked value type without introducing another execution pipeline.

## Caller shape

```json
{
  "kind": "extensionByUrl",
  "lookup": {
    "extension": {
      "ownerPath": "extension[].extension[]",
      "urlPath": ["urn:parent:left", "urn:leaf"],
      "valuePath": "valueString",
      "logicalType": "string",
      "choiceArms": ["valueString"]
    },
    "projectionMode": "ALL"
  }
}
```

The example's source-kind spelling must follow the existing `SourceExtensionByURL` wire constant. The new payload is `lookup.extension`; it cannot coexist with legacy `match`/`path` or Coding `binding`/`key`.

## Shape and ownership

Generated-FHIR-aware validation owns a typed `ExtensionBinding` with `ownerPath`, `urlPath`, `valuePath`, `logicalType`, optional `choiceArms`, `valueFallback`, and `unitPath`. Validation proves each extension boundary, one URL per extension depth, and a scalar value path relative to the terminal extension. Share checked value/choice validation with Coding bindings rather than duplicating datatype rules. An omitted choice-arm list must not turn an incompatible observed value into ordinary absence.

Authoring and recipe compilation carry this checked intent into the existing correlation IR. Lowering binds each URL separately and emits nested extension-owner loops, preserving each parent before descending. The existing value projection reductions handle VALUE, FIRST, ALL, and DISTINCT. No second query compiler, raw AQL input, independent flatten-and-zip matching, or inferred string coalescing is added.

The command boundary rejects new legacy extension, Observation-component, and Coding lookup writes without explicit typed bindings. Immutable saved recipes and historical receipt readers remain readable. This does not reinterpret old artifacts or silently invent missing systems or ancestor URLs.

## Alternatives and decision

A generic public ancestor-predicate language could express this lookup, but would expose traversal scope and arbitrary predicates to every client. The closed extension binding is smaller and directly represents FHIR URL ancestry. Generic IR selectors remain internal implementation details. Root selected this design under the conservative pstack override; no multi-agent design arena was needed.

## Verification

Prove left/leaf returns only `left-only` and right/leaf only `right-only` through commands, reconcile, and real receipt Preview. Add missing-parent and wrong-arm negatives. Prove new legacy writes are rejected, typed Observation bindings still work, and historical recipe decoding remains compatible. Run focused schema/compiler/authoring/server checks and the existing browser journey at the integrated checkpoint.
