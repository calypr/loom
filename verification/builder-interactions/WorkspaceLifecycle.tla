---------------- MODULE WorkspaceLifecycle ----------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

(*
  Bounded client-side workspace model for explorer hydration, explorer
  selection/create/delete, table selection, occurrence selection, and route
  edge editing. It models the mounted Builder guards and fallbacks. Server
  persistence, authorization, HTTP retries, and query-cache behavior remain
  outside this finite model.
*)

CONSTANTS Explorers, Tables, CatalogEdges

None == "NONE"
Base == "BASE"
ListPhases == {"idle", "loaded", "error"}
BuilderPhases == {"idle", "hydrated"}
Occurrences == {"BASE", "O1", "O2"}
DeleteStates == {FALSE, TRUE}
PreviewPhases == {"idle", "loading", "ready", "failed"}
PreviewIdentities == {"I1", "I2"}
PreviewFailures == {"transient", "receipt", "catalog"}
MaxRecovery == 2
MaxPreviewGeneration == 4

VARIABLES
  explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
  tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
  routeEdges, usedEdges, deleteSupported, confirmed,
  previewPhase, previewIdentity, previewGeneration, acceptedPreview,
  retryBudget, receiptBudget, catalogBudget, lastAction

vars == <<
  explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
  tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
  routeEdges, usedEdges, deleteSupported, confirmed,
  previewPhase, previewIdentity, previewGeneration, acceptedPreview,
  retryBudget, receiptBudget, catalogBudget, lastAction
>>

previewVars == <<
  previewPhase, previewIdentity, previewGeneration, acceptedPreview,
  retryBudget, receiptBudget, catalogBudget
>>

Init ==
  /\ explorers = {}
  /\ selectedExplorer = None
  /\ effectiveExplorer = None
  /\ listPhase = "idle"
  /\ builderPhase = "idle"
  /\ tables = {}
  /\ selectedTable = None
  /\ rememberedTable = None
  /\ selectedOccurrence = Base
  /\ derivedOccurrences = {}
  /\ routeEdges = {}
  /\ usedEdges = {}
  /\ deleteSupported = FALSE
  /\ confirmed = FALSE
  /\ previewPhase = "idle"
  /\ previewIdentity = "I1"
  /\ previewGeneration = 0
  /\ acceptedPreview = "NONE"
  /\ retryBudget = MaxRecovery
  /\ receiptBudget = MaxRecovery
  /\ catalogBudget = MaxRecovery
  /\ lastAction = "Init"

ExplorerListComplete ==
  /\ listPhase \in {"idle", "error"}
  /\ explorers' = Explorers
  /\ effectiveExplorer' =
       IF selectedExplorer \in Explorers THEN selectedExplorer ELSE CHOOSE e \in Explorers: TRUE
  /\ selectedExplorer' = effectiveExplorer'
  /\ listPhase' = "loaded"
  /\ lastAction' = "ExplorerListComplete"
  /\ UNCHANGED previewVars
  /\ UNCHANGED <<
       builderPhase, tables, selectedTable, rememberedTable, selectedOccurrence,
       derivedOccurrences, routeEdges, usedEdges, deleteSupported, confirmed
     >>

ExplorerListError ==
  /\ listPhase = "idle"
  /\ listPhase' = "error"
  /\ lastAction' = "ExplorerListError"
  /\ UNCHANGED previewVars
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, builderPhase, tables,
       selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed
     >>

HydrateBuilder ==
  /\ listPhase = "loaded"
  /\ builderPhase = "idle"
  /\ tables' = Tables
  /\ selectedTable' =
       IF rememberedTable \in Tables THEN rememberedTable ELSE CHOOSE t \in Tables: TRUE
  /\ rememberedTable' = selectedTable'
  /\ selectedOccurrence' = Base
  /\ derivedOccurrences' = {}
  /\ routeEdges' = {}
  /\ usedEdges' = {}
  /\ builderPhase' = "hydrated"
  /\ lastAction' = "HydrateBuilder"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, selectedExplorer, effectiveExplorer, listPhase, deleteSupported, confirmed >>

SelectExplorer(e) ==
  /\ listPhase = "loaded"
  /\ e \in explorers
  /\ e # selectedExplorer
  /\ selectedExplorer' = e
  /\ effectiveExplorer' = e
  /\ builderPhase' = "idle"
  /\ tables' = {}
  /\ selectedTable' = None
  /\ rememberedTable' = None
  /\ selectedOccurrence' = Base
  /\ derivedOccurrences' = {}
  /\ routeEdges' = {}
  /\ usedEdges' = {}
  /\ lastAction' = "SelectExplorer(" \o e \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, listPhase, deleteSupported, confirmed >>

CreateExplorer(e) ==
  /\ listPhase = "loaded"
  /\ e \in Explorers \ explorers
  /\ explorers' = explorers \union {e}
  /\ selectedExplorer' = e
  /\ effectiveExplorer' = e
  /\ builderPhase' = "idle"
  /\ tables' = {}
  /\ selectedTable' = None
  /\ rememberedTable' = None
  /\ selectedOccurrence' = Base
  /\ derivedOccurrences' = {}
  /\ routeEdges' = {}
  /\ usedEdges' = {}
  /\ lastAction' = "CreateExplorer(" \o e \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << listPhase, deleteSupported, confirmed >>

DeleteCapability(supported, confirmedValue) ==
  /\ supported \in BOOLEAN
  /\ confirmedValue \in BOOLEAN
  /\ deleteSupported' = supported
  /\ confirmed' = confirmedValue
  /\ lastAction' = "DeleteCapability"
  /\ UNCHANGED previewVars
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges
     >>

DeleteExplorer(e) ==
  /\ listPhase = "loaded"
  /\ deleteSupported
  /\ confirmed
  /\ e \in explorers
  /\ Cardinality(explorers) > 1
  /\ explorers' = explorers \ {e}
  /\ selectedExplorer' =
       IF selectedExplorer = e
       THEN IF "E1" \in explorers' THEN "E1" ELSE CHOOSE x \in explorers': TRUE
       ELSE selectedExplorer
  /\ effectiveExplorer' = selectedExplorer'
  /\ builderPhase' = "idle"
  /\ tables' = {}
  /\ selectedTable' = None
  /\ rememberedTable' = None
  /\ selectedOccurrence' = Base
  /\ derivedOccurrences' = {}
  /\ routeEdges' = {}
  /\ usedEdges' = {}
  /\ lastAction' = "DeleteExplorer(" \o e \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << listPhase, deleteSupported, confirmed >>

SelectTable(t) ==
  /\ builderPhase = "hydrated"
  /\ t \in tables
  /\ t # selectedTable
  /\ selectedTable' = t
  /\ rememberedTable' = t
  /\ selectedOccurrence' = Base
  /\ derivedOccurrences' = {}
  /\ routeEdges' = {}
  /\ usedEdges' = {}
  /\ lastAction' = "SelectTable(" \o t \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase, tables, deleteSupported, confirmed >>

SelectOccurrence(o) ==
  /\ builderPhase = "hydrated"
  /\ o \in derivedOccurrences \union {Base}
  /\ selectedOccurrence' = o
  /\ lastAction' = "SelectOccurrence(" \o o \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase, tables, selectedTable, rememberedTable, derivedOccurrences, routeEdges, usedEdges, deleteSupported, confirmed >>

AddRoute(edge) ==
  /\ builderPhase = "hydrated"
  /\ edge \in CatalogEdges \ usedEdges
  /\ routeEdges' = routeEdges \union {edge}
  /\ usedEdges' = usedEdges \union {edge}
  /\ derivedOccurrences' = derivedOccurrences \union {edge}
  /\ selectedOccurrence' = edge
  /\ lastAction' = "AddRoute(" \o edge \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase, tables, selectedTable, rememberedTable, deleteSupported, confirmed >>

RemoveRoute(edge) ==
  /\ builderPhase = "hydrated"
  /\ edge \in routeEdges
  /\ routeEdges' = routeEdges \ {edge}
  /\ usedEdges' = usedEdges \ {edge}
  /\ derivedOccurrences' = derivedOccurrences \ {edge}
  /\ selectedOccurrence' = IF selectedOccurrence = edge THEN Base ELSE selectedOccurrence
  /\ lastAction' = "RemoveRoute(" \o edge \o ")"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase, tables, selectedTable, rememberedTable, deleteSupported, confirmed >>

CommandsApplied ==
  /\ builderPhase = "hydrated"
  /\ selectedOccurrence' =
       IF selectedOccurrence \in derivedOccurrences \union {Base} THEN selectedOccurrence ELSE Base
  /\ lastAction' = "CommandsApplied"
  /\ UNCHANGED previewVars
  /\ UNCHANGED << explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase, tables, selectedTable, rememberedTable, derivedOccurrences, routeEdges, usedEdges, deleteSupported, confirmed >>

StartPreview(identity) ==
  /\ builderPhase = "hydrated"
  /\ identity \in PreviewIdentities
  /\ previewPhase \in {"idle", "failed"}
  /\ previewGeneration < MaxPreviewGeneration
  /\ previewIdentity' = identity
  /\ previewGeneration' = previewGeneration + 1
  /\ previewPhase' = "loading"
  /\ lastAction' = "StartPreview(" \o identity \o ")"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, acceptedPreview,
       retryBudget, receiptBudget, catalogBudget
     >>

PreviewComplete(identity) ==
  /\ previewPhase = "loading"
  /\ identity = previewIdentity
  /\ previewPhase' = "ready"
  /\ lastAction' = "PreviewComplete(" \o identity \o ")"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       previewGeneration, acceptedPreview, retryBudget, receiptBudget, catalogBudget
     >>

PreviewFailed(kind) ==
  /\ previewPhase = "loading"
  /\ kind \in PreviewFailures
  /\ previewPhase' = "failed"
  /\ lastAction' = "PreviewFailed(" \o kind \o ")"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       previewGeneration, acceptedPreview, retryBudget, receiptBudget, catalogBudget
     >>

RetryTransient ==
  /\ previewPhase = "failed"
  /\ retryBudget > 0
  /\ previewGeneration < MaxPreviewGeneration
  /\ retryBudget' = retryBudget - 1
  /\ previewGeneration' = previewGeneration + 1
  /\ previewPhase' = "loading"
  /\ lastAction' = "RetryTransient"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       acceptedPreview, receiptBudget, catalogBudget
     >>

RecompileReceipt ==
  /\ previewPhase = "failed"
  /\ receiptBudget > 0
  /\ receiptBudget' = receiptBudget - 1
  /\ previewPhase' = "loading"
  /\ lastAction' = "RecompileReceipt"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       previewGeneration, acceptedPreview, retryBudget, catalogBudget
     >>

RefreshCatalog ==
  /\ previewPhase = "failed"
  /\ catalogBudget > 0
  /\ catalogBudget' = catalogBudget - 1
  /\ previewPhase' = "loading"
  /\ lastAction' = "RefreshCatalog"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       previewGeneration, acceptedPreview, retryBudget, receiptBudget
     >>

AbortPreview ==
  /\ previewPhase \in {"loading", "failed"}
  /\ previewGeneration < MaxPreviewGeneration
  /\ previewPhase' = "idle"
  /\ previewGeneration' = previewGeneration + 1
  /\ lastAction' = "AbortPreview"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       acceptedPreview, retryBudget, receiptBudget, catalogBudget
     >>

AcceptPreview ==
  /\ previewPhase = "ready"
  /\ acceptedPreview' = previewIdentity
  /\ lastAction' = "AcceptPreview"
  /\ UNCHANGED <<
       explorers, selectedExplorer, effectiveExplorer, listPhase, builderPhase,
       tables, selectedTable, rememberedTable, selectedOccurrence, derivedOccurrences,
       routeEdges, usedEdges, deleteSupported, confirmed, previewIdentity,
       previewGeneration, previewPhase, retryBudget, receiptBudget, catalogBudget
     >>

Next ==
  \/ ExplorerListComplete
  \/ ExplorerListError
  \/ HydrateBuilder
  \/ \E e \in Explorers: SelectExplorer(e)
  \/ \E e \in Explorers: CreateExplorer(e)
  \/ \E e \in Explorers: DeleteExplorer(e)
  \/ \E supported \in BOOLEAN, confirmedValue \in BOOLEAN: DeleteCapability(supported, confirmedValue)
  \/ \E t \in Tables: SelectTable(t)
  \/ \E o \in Occurrences: SelectOccurrence(o)
  \/ \E edge \in CatalogEdges: AddRoute(edge)
  \/ \E edge \in CatalogEdges: RemoveRoute(edge)
  \/ CommandsApplied
  \/ \E identity \in PreviewIdentities: StartPreview(identity)
  \/ \E identity \in PreviewIdentities: PreviewComplete(identity)
  \/ \E kind \in PreviewFailures: PreviewFailed(kind)
  \/ RetryTransient
  \/ RecompileReceipt
  \/ RefreshCatalog
  \/ AbortPreview
  \/ AcceptPreview

Spec == Init /\ [][Next]_vars

Inv_EffectiveExplorerExists ==
  effectiveExplorer = None \/ effectiveExplorer \in explorers

Inv_AtLeastOneExplorer ==
  listPhase = "loaded" => Cardinality(explorers) >= 1

Inv_HydrationSelectsKnownTable ==
  builderPhase = "hydrated" => selectedTable \in tables

Inv_OccurrenceBelongsToRoute ==
  selectedOccurrence \in derivedOccurrences \union {Base}

Inv_RouteEdgesLegal ==
  /\ routeEdges \subseteq CatalogEdges
  /\ usedEdges \subseteq CatalogEdges
  /\ routeEdges \subseteq usedEdges

Inv_ExplorerSwitchResetsTable ==
  \/ lastAction \in {"SelectExplorer(E1)", "SelectExplorer(E2)", "SelectExplorer(E3)"} => selectedTable = None
  \/ lastAction \notin {"SelectExplorer(E1)", "SelectExplorer(E2)", "SelectExplorer(E3)"}

Inv_AcceptedPreviewMatchesIdentity ==
  lastAction = "AcceptPreview" => acceptedPreview = previewIdentity

Inv_PreviewReadyHasIdentity ==
  previewPhase = "ready" => previewIdentity \in PreviewIdentities

Inv_RecoveryBudgetsNonNegative ==
  /\ retryBudget >= 0
  /\ receiptBudget >= 0
  /\ catalogBudget >= 0

Inv_DeleteKeepsFallback ==
  lastAction \in {"DeleteExplorer(E1)", "DeleteExplorer(E2)", "DeleteExplorer(E3)"} =>
    effectiveExplorer \in explorers

TypeOK ==
  /\ explorers \subseteq Explorers
  /\ selectedExplorer \in Explorers \union {None}
  /\ effectiveExplorer \in Explorers \union {None}
  /\ listPhase \in ListPhases
  /\ builderPhase \in BuilderPhases
  /\ tables \subseteq Tables
  /\ selectedTable \in Tables \union {None}
  /\ rememberedTable \in Tables \union {None}
  /\ selectedOccurrence \in Occurrences \union {Base} \union CatalogEdges
  /\ derivedOccurrences \subseteq CatalogEdges
  /\ routeEdges \subseteq CatalogEdges
  /\ usedEdges \subseteq CatalogEdges
  /\ deleteSupported \in DeleteStates
  /\ confirmed \in DeleteStates
  /\ previewPhase \in PreviewPhases
  /\ previewIdentity \in PreviewIdentities
  /\ previewGeneration \in 0..MaxPreviewGeneration
  /\ acceptedPreview \in PreviewIdentities \union {"NONE"}
  /\ retryBudget \in 0..MaxRecovery
  /\ receiptBudget \in 0..MaxRecovery
  /\ catalogBudget \in 0..MaxRecovery
====
