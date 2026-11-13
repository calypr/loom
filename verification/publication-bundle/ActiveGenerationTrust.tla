----------------------- MODULE ActiveGenerationTrust -----------------------
(*
Models F-B13 (CONFIRMED-by-reading): the dataset release CAS repoints the
active_generation pointer to ANY staged manifest the publish receipt names,
with no comparison to the currently active generation (release_store.go:163-170:
the manifest filter checks only the receipt's OWN generation; the pointer patch
writes it unconditionally), while readers follow the pointer
(manifest_lifecycle.go:304-305, active_resolver.go:20). Prior generations stay
STAGED forever (manifest_lifecycle.go only writes STAGED/FAILED from the
loader). CrossGen=TRUE models the proposed CAS guard
generationPointer.dataset.generation == manifest.dataset.generation.
*)

EXTENDS Naturals

CONSTANT GENS,                 \* loaded dataset generations, e.g. {g1, g2}
         NoGen,                \* sentinel: nothing loaded yet
         CrossGen              \*_BOOLEAN: CAS refuses a generation != active

ASSUME Wellformed ==
  /\ GENS # {}
  /\ NoGen \notin GENS
  /\ CrossGen \in BOOLEAN

VARIABLES lastLoaded,   \* generation most recently loaded+activated by the loader
          activeGen,    \* generation the pointer exposes to readers
          boundGen      \* the receipt's generation fixed at compile time

TypeOK ==
  /\ lastLoaded \in GENS \cup {NoGen}
  /\ activeGen \in GENS \cup {NoGen}
  /\ boundGen \in GENS \cup {NoGen}

Init ==
  /\ lastLoaded = NoGen
  /\ activeGen = NoGen
  /\ boundGen = NoGen

\* Loader: dataset load activates a generation and moves the pointer itself
\* (generation_orchestration Activate; prior generations remain STAGED).
LoadAndActivate(g) ==
  /\ g \in GENS
  /\ lastLoaded' = g
  /\ activeGen' = g
  /\ UNCHANGED boundGen

\* Builder compile fixes a receipt to the THEN-active generation
\* (ResolveForCompilation DOES enforce the active check
\* explorer_capability.go ~:214-218).
CompileReceipt ==
  /\ lastLoaded # NoGen
  /\ boundGen' = lastLoaded
  /\ UNCHANGED <<lastLoaded, activeGen>>

\* Explorer publish -> release Create->Activate CAS: filters the receipt's own
\* manifest (staged), never compares it to the pointer, then patches
\* generationPointer to the receipt generation (release_store.go:170).
PublishCAS ==
  /\ boundGen # NoGen
  /\ CrossGen => boundGen = activeGen
  /\ activeGen' = boundGen
  /\ UNCHANGED <<lastLoaded, boundGen>>

vars == <<lastLoaded, activeGen, boundGen>>

Next == (\E g \in GENS: LoadAndActivate(g))
        \/ CompileReceipt
        \/ PublishCAS

\* Users' expectation: the generation readers follow is what the loader last
\* activated; a publish may not silently downgrade it.
NoSilentDowngrade == (activeGen = NoGen) \/ (activeGen = lastLoaded)

Spec == Init /\ [][Next]_vars

=============================================================================
