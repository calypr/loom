----------------------- MODULE ActiveGenerationTrust -----------------------

EXTENDS Naturals

CONSTANT GENS,
         NoGen,
         CrossGen

ASSUME Wellformed ==
  /\ GENS # {}
  /\ NoGen \notin GENS
  /\ CrossGen \in BOOLEAN

VARIABLES lastLoaded,
          activeGen,
          boundGen

TypeOK ==
  /\ lastLoaded \in GENS \cup {NoGen}
  /\ activeGen \in GENS \cup {NoGen}
  /\ boundGen \in GENS \cup {NoGen}

Init ==
  /\ lastLoaded = NoGen
  /\ activeGen = NoGen
  /\ boundGen = NoGen

LoadAndActivate(g) ==
  /\ g \in GENS
  /\ lastLoaded' = g
  /\ activeGen' = g
  /\ UNCHANGED boundGen

CompileReceipt ==
  /\ lastLoaded # NoGen
  /\ boundGen' = lastLoaded
  /\ UNCHANGED <<lastLoaded, activeGen>>

PublishCAS ==
  /\ boundGen # NoGen
  /\ CrossGen => boundGen = activeGen
  /\ activeGen' = boundGen
  /\ UNCHANGED <<lastLoaded, boundGen>>

vars == <<lastLoaded, activeGen, boundGen>>

Next == (\E g \in GENS: LoadAndActivate(g))
        \/ CompileReceipt
        \/ PublishCAS

NoSilentDowngrade == (activeGen = NoGen) \/ (activeGen = lastLoaded)

Spec == Init /\ [][Next]_vars

=============================================================================
