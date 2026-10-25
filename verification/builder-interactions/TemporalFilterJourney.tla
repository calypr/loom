---------------- MODULE TemporalFilterJourney ----------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANTS Profile, Case, ModelEvents, ObservedRequests, InitialMountEpoch

(***************************************************************************
The public transition operators consume normalized events with action,
atMonotonicMs, atEpochMs, mountEpoch, request, before, after, ui, and filter
fields. Request evidence retains captureId, optional requestId, mutation
classification, method, path, status, body, and responseBody.
Saved snapshots retain complete workspaceDocuments, workspaceDigest,
catalogGeneration, draftVersion, draftDigest, outputId, columnId, columns, and
steps. The exported event actions require these actual snapshot bindings:
ExplorerCreated.after; SourceTableCreated.before/after;
TemporalColumnApplied.before/after; FilterProposed(kind).before with no after
read; FilterCancelled.before/after; FilterApplied(kind) or FilterRemoved
before/after; Reload.before/after; and FilterEditOpened.before. Mutation
requests carry `isMutation = TRUE`; proposal reads carry `FALSE`; UI-only
actions use NoRequest. ObservedRequests retains captureId, mutation flag,
method, path, status, candidateColumnIds, response receiptId, and response
outputId. The first observed mount epoch comes from InitialMountEpoch. The event list and profile case are supplied through
the public ModelEvents and Case constants, so a static adapter can bind the
captured normalized sequence without changing these operators.
UI evidence retains proposal/receipt/output/column IDs, native controls,
and row IDs. Filter evidence retains intent, target step, and typed
request/response/saved values.
AbstractModelEvents below use opaque document/digest tokens as bounded fixtures, not
browser evidence.
***************************************************************************)

Profiles == {"DATE", "DATE_TIME"}
No == "No"
NoSaved ==
  [outputId |-> No, columnId |-> No, columns |-> <<>>, steps |-> <<>>,
   draftVersion |-> 0, draftDigest |-> No, workspaceDigest |-> No,
   catalogGeneration |-> No, workspaceDocuments |-> <<>>]
NoProposal ==
  [proposalId |-> No, receiptId |-> No, captureId |-> No,
   requestId |-> No, mountEpoch |-> InitialMountEpoch,
   status |-> No, intent |-> No, stepId |-> No, outputId |-> No,
   columnId |-> No, inputType |-> No, inputValue |-> No, operator |-> No,
   requestValue |-> No, responseValue |-> No, savedValue |-> No,
   baseDraftVersion |-> 0, baseDraftDigest |-> No,
   candidateSteps |-> <<>>, rowIds |-> <<>>]
NoEditor ==
  [intent |-> No, stepId |-> No, inputType |-> No,
   inputValue |-> No, operator |-> No, value |-> No]
NoRequest == [captureId |-> No, requestId |-> No, isMutation |-> FALSE,
               method |-> No, path |-> No, status |-> No,
               body |-> No, responseBody |-> No]
NoObservedRequest ==
  [captureId |-> No, isMutation |-> FALSE, method |-> No, path |-> No,
   status |-> No, candidateColumnIds |-> No, receiptId |-> No, outputId |-> No]
NoSnapshot ==
  [readId |-> No, requestId |-> No, mountEpoch |-> InitialMountEpoch, status |-> No,
   catalogGeneration |-> No, draftVersion |-> 0, draftDigest |-> No,
   workspaceDocuments |-> <<>>, workspaceDigest |-> No,
   outputId |-> No, columnId |-> No, columns |-> <<>>, steps |-> <<>>]
NoUI == [capturedAtMonotonicMs |-> 0, capturedAtEpochMs |-> 0,
         mountEpoch |-> InitialMountEpoch, proposalStatus |-> No, proposalId |-> No,
         receiptId |-> No, outputId |-> No, columnId |-> No,
         inputType |-> No, inputValue |-> No, operator |-> No, rowIds |-> <<>>]
NoFilter == [intent |-> No, stepId |-> No, outputId |-> No,
             columnId |-> No, valueKind |-> No, operator |-> No,
             requestValue |-> No, responseValue |-> No, savedValue |-> No,
             proposalId |-> No, receiptId |-> No]
NoPreview == [receiptId |-> No, outputId |-> No]

EndsWith(value, suffix) ==
  /\ Len(value) >= Len(suffix)
  /\ SubSeq(value, Len(value) - Len(suffix) + 1, Len(value)) = suffix

Mount(offset) == InitialMountEpoch + offset
SequenceSet(sequence) == {sequence[i] : i \in 1..Len(sequence)}

AbstractCase ==
  CASE Profile = "DATE" ->
    [caseName |-> "date", resourceType |-> "Patient", fieldPath |-> "birthDate",
     inputType |-> "date", inputValue |-> "1980-01-02", valueKind |-> "DATE",
     initialOperator |-> "GT", editedOperator |-> "GTE",
     initialRequestValue |-> [kind |-> "DATE", date |-> "1980-01-02"],
     initialResponseValue |-> [kind |-> "DATE", date |-> "1980-01-02"],
     initialSavedValue |-> [kind |-> "DATE", date |-> "1980-01-02"],
     editedRequestValue |-> [kind |-> "DATE", date |-> "1980-01-02"],
     editedResponseValue |-> [kind |-> "DATE", date |-> "1980-01-02"],
     editedSavedValue |-> [kind |-> "DATE", date |-> "1980-01-02"],
     sourceRowIds |-> <<"dev-patient-001", "dev-patient-002">>,
     initialRowIds |-> <<"dev-patient-002">>,
     editedRowIds |-> <<"dev-patient-001", "dev-patient-002">>]
  [] Profile = "DATE_TIME" ->
    [caseName |-> "date-time", resourceType |-> "Observation",
     fieldPath |-> "effectiveDateTime", inputType |-> "datetime-local",
     inputValue |-> "2026-01-02T00:00", valueKind |-> "DATE_TIME",
     initialOperator |-> "GT", editedOperator |-> "GTE",
     initialRequestValue |-> [kind |-> "DATE_TIME", dateTime |-> "2026-01-02T00:00:00.000Z"],
     initialResponseValue |-> [kind |-> "DATE_TIME", dateTime |-> "2026-01-02T00:00:00Z"],
     initialSavedValue |-> [kind |-> "DATE_TIME", dateTime |-> "2026-01-02T00:00:00Z"],
     editedRequestValue |-> [kind |-> "DATE_TIME", dateTime |-> "2026-01-02T00:00:00Z"],
     editedResponseValue |-> [kind |-> "DATE_TIME", dateTime |-> "2026-01-02T00:00:00Z"],
     editedSavedValue |-> [kind |-> "DATE_TIME", dateTime |-> "2026-01-02T00:00:00Z"],
     sourceRowIds |-> <<"dev-observation-001", "dev-observation-002",
                      "dev-observation-003">>,
     initialRowIds |-> <<>>,
     editedRowIds |-> <<"dev-observation-001", "dev-observation-002",
                      "dev-observation-003">>]

AbstractOutputId == "Output-1"
AbstractRootColumnId == "Root-ID-column"
AbstractTemporalColumnId == "Temporal-column"
AbstractFilterStepId == "Filter-step-1"
CatalogGeneration == "Fixture-generation"
OtherSteps ==
  <<[id |-> "Source-step", operation |-> [kind |-> "SOURCE"]],
    [id |-> "Other-step", operation |-> [kind |-> "OTHER"]]>>
RootColumns == <<[columnId |-> AbstractRootColumnId]>>
SourceColumns == Append(RootColumns, [columnId |-> AbstractTemporalColumnId])

FilterStep(stepId, columnId, value, operator) ==
  [id |-> stepId,
   operation |-> [kind |-> "FILTER",
                  filter |-> [columnId |-> columnId,
                              operator |-> operator, values |-> <<value>>]]]
AbstractCreatedRequestSteps ==
  Append(OtherSteps, FilterStep(AbstractFilterStepId, AbstractTemporalColumnId,
                                Case.initialRequestValue, Case.initialOperator))
AbstractCreatedSteps ==
  Append(OtherSteps, FilterStep(AbstractFilterStepId, AbstractTemporalColumnId,
                                Case.initialSavedValue, Case.initialOperator))
AbstractEditedRequestSteps ==
  Append(OtherSteps, FilterStep(AbstractFilterStepId, AbstractTemporalColumnId,
                                Case.editedRequestValue, Case.editedOperator))
AbstractEditedSteps ==
  Append(OtherSteps, FilterStep(AbstractFilterStepId, AbstractTemporalColumnId,
                                Case.editedSavedValue, Case.editedOperator))

AbstractTableDocuments == "AbstractTableDocuments"
AbstractSourceDocuments == "AbstractSourceDocuments"
AbstractCreatedDocuments == "AbstractCreatedDocuments"
AbstractEditedDocuments == "AbstractEditedDocuments"
AbstractRemovedDocuments == "AbstractRemovedDocuments"

AbstractSnapshot(readId, rawId, mount, docs, version, draftDigest, workspaceDigest,
                 outputId, columnId, columns, steps) ==
  [readId |-> readId,
   requestId |-> rawId,
   mountEpoch |-> mount,
   status |-> 200,
   catalogGeneration |-> CatalogGeneration,
   draftVersion |-> version,
   draftDigest |-> draftDigest,
   workspaceDocuments |-> docs,
   workspaceDigest |-> workspaceDigest,
   outputId |-> outputId,
   columnId |-> columnId,
   columns |-> columns,
   steps |-> steps]
SnapshotAt(snapshot, readId, rawId, mount) ==
  AbstractSnapshot(readId, rawId, mount, snapshot.workspaceDocuments,
    snapshot.draftVersion, snapshot.draftDigest, snapshot.workspaceDigest,
    snapshot.outputId, snapshot.columnId, snapshot.columns, snapshot.steps)
SavedFromSnapshot(snapshot) ==
  [outputId |-> snapshot.outputId,
   columnId |-> snapshot.columnId,
   columns |-> snapshot.columns,
   steps |-> snapshot.steps,
   draftVersion |-> snapshot.draftVersion,
   draftDigest |-> snapshot.draftDigest,
   workspaceDigest |-> snapshot.workspaceDigest,
   catalogGeneration |-> snapshot.catalogGeneration,
   workspaceDocuments |-> snapshot.workspaceDocuments]
AbstractTableSnapshot ==
  AbstractSnapshot("read-table", "raw-read-table", Mount(0),
    AbstractTableDocuments, 1,
    "draft-table", "workspace-table", AbstractOutputId, No,
    RootColumns, OtherSteps)
AbstractSourceSnapshot ==
  AbstractSnapshot("read-source", "raw-read-source", Mount(0), AbstractSourceDocuments, 2,
    "draft-source", "workspace-source", AbstractOutputId,
    AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), OtherSteps)
AbstractBlankSnapshot ==
  AbstractSnapshot("read-explorer-created", "raw-read-explorer-created", Mount(0),
    "AbstractBlankDocuments", 0, "draft-blank", "workspace-blank",
    No, No, <<>>, <<>>)
AbstractBeforeTableSnapshot ==
  AbstractSnapshot("read-before-table", "raw-read-before-table", Mount(0),
    "AbstractBlankDocuments", 0, "draft-blank", "workspace-blank",
    No, No, <<>>, <<>>)
AbstractSourceSnapshotMount1 ==
  AbstractSnapshot("read-source-mount-1", "raw-read-source-mount-1", Mount(1),
    AbstractSourceDocuments, 2, "draft-source", "workspace-source",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), OtherSteps)
AbstractCreateBefore ==
  AbstractSnapshot("read-before-create", "raw-read-before-create", Mount(1),
    AbstractSourceDocuments, 2, "draft-source", "workspace-source",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), OtherSteps)
AbstractCreateAfter ==
  AbstractSnapshot("read-after-create", "raw-read-after-create", Mount(1),
    AbstractCreatedDocuments, 3, "draft-create", "workspace-create",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), AbstractCreatedSteps)
AbstractEditBefore ==
  AbstractSnapshot("read-before-edit", "raw-read-before-edit", Mount(2),
    AbstractCreatedDocuments, 3, "draft-create", "workspace-create",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), AbstractCreatedSteps)
AbstractEditAfter ==
  AbstractSnapshot("read-after-edit", "raw-read-after-edit", Mount(2),
    AbstractEditedDocuments, 4, "draft-edit", "workspace-edit",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), AbstractEditedSteps)
AbstractRemoveBefore ==
  AbstractSnapshot("read-before-remove", "raw-read-before-remove", Mount(3),
    AbstractEditedDocuments, 4, "draft-edit", "workspace-edit",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), AbstractEditedSteps)
AbstractRemoveAfter ==
  AbstractSnapshot("read-after-remove", "raw-read-after-remove", Mount(3),
    AbstractRemovedDocuments, 5, "draft-remove", "workspace-remove",
    AbstractOutputId, AbstractTemporalColumnId,
    Append(RootColumns, [columnId |-> AbstractTemporalColumnId]), OtherSteps)

Request(captureId, requestId, isMutation, method, path, status, body, responseBody) ==
  [captureId |-> captureId,
   requestId |-> requestId,
   isMutation |-> isMutation,
   method |-> method,
   path |-> path,
   status |-> status,
   body |-> body,
   responseBody |-> responseBody]

UI(ordinal, mount, status, proposalId, receiptId, outputId, columnId,
   inputType, inputValue, operator, rowIds) ==
  [capturedAtMonotonicMs |-> ordinal * 10,
   capturedAtEpochMs |-> ordinal * 1000,
   mountEpoch |-> mount,
   proposalStatus |-> status,
   proposalId |-> proposalId,
   receiptId |-> receiptId,
   outputId |-> outputId,
   columnId |-> columnId,
   inputType |-> inputType,
   inputValue |-> inputValue,
   operator |-> operator,
   rowIds |-> rowIds]

Filter(intent, stepId, outputId, columnId, valueKind, operator,
       requestValue, responseValue, savedValue, proposalId, receiptId) ==
  [intent |-> intent,
   stepId |-> stepId,
   outputId |-> outputId,
   columnId |-> columnId,
   valueKind |-> valueKind,
   operator |-> operator,
   requestValue |-> requestValue,
   responseValue |-> responseValue,
   savedValue |-> savedValue,
   proposalId |-> proposalId,
   receiptId |-> receiptId]

Event(ordinal, action, mount, request, before, after, ui, filter) ==
  LET proposalCaptures ==
    IF action = "TemporalColumnApplied"
      THEN <<"capture-choice-column">>
      ELSE IF action \in {"FilterProposed(create)", "FilterProposed(edit)",
                          "FilterProposed(remove)"}
        THEN <<request.captureId>>
        ELSE <<>>
  IN [action |-> action,
   atMonotonicMs |-> ordinal * 10,
   atEpochMs |-> ordinal * 1000,
   mountEpoch |-> mount,
   request |-> request,
   before |-> before,
   after |-> after,
   ui |-> ui,
   filter |-> filter,
   proposalCaptureIds |-> proposalCaptures,
   evidenceKind |-> "abstract-model-fixture"]

AbstractObservedRequest(captureId, isMutation, method, path, status,
                        candidateColumnIds, receiptId, outputId) ==
  [captureId |-> captureId, isMutation |-> isMutation,
   method |-> method, path |-> path, status |-> status,
   candidateColumnIds |-> candidateColumnIds,
   receiptId |-> receiptId, outputId |-> outputId]
AbstractObservedRequests ==
  <<AbstractObservedRequest("capture-explorer", TRUE, "POST", "/explorers", 201, No, No, No),
    AbstractObservedRequest("capture-table", TRUE, "POST", "/authoring/v2/commands", 200, No, No, No),
    AbstractObservedRequest("capture-column", TRUE, "POST", "/authoring/v2/commands", 200, No, No, No),
    AbstractObservedRequest("capture-choice-column", FALSE, "POST",
      "/authoring/v2/construction-choice-proposals", 200,
      <<AbstractTemporalColumnId>>, "Receipt-column", AbstractOutputId),
    AbstractObservedRequest("capture-proposal-P-cancel", FALSE, "POST",
      "/authoring/v2/construction-proposals", 200, No, "P-cancel", AbstractOutputId),
    AbstractObservedRequest("capture-apply-create", TRUE, "POST", "/authoring/v2/commands", 200, No, No, No),
    AbstractObservedRequest("capture-proposal-P-create", FALSE, "POST",
      "/authoring/v2/construction-proposals", 200, No, "P-create", AbstractOutputId),
    AbstractObservedRequest("capture-apply-edit", TRUE, "POST", "/authoring/v2/commands", 200, No, No, No),
    AbstractObservedRequest("capture-proposal-P-edit", FALSE, "POST",
      "/authoring/v2/construction-proposals", 200, No, "P-edit", AbstractOutputId),
    AbstractObservedRequest("capture-proposal-P-remove", FALSE, "POST",
      "/authoring/v2/construction-proposals", 200, No, "P-remove", AbstractOutputId),
    AbstractObservedRequest("capture-remove-filter", TRUE, "POST",
      "/authoring/v2/commands", 200, No, No, No)>>

ProposalBody(saved, outputId, candidateSteps) ==
  [outputId |-> outputId,
   expectedDraftVersion |-> saved.draftVersion,
   expectedDraftDigest |-> saved.draftDigest,
   candidateConstruction |-> [steps |-> candidateSteps]]
ProposalResponse(proposalId, saved, outputId, candidateSteps) ==
  [proposalId |-> proposalId,
   outputId |-> outputId,
   draftVersion |-> saved.draftVersion,
   draftDigest |-> saved.draftDigest,
   candidateConstruction |-> [steps |-> candidateSteps],
   preview |-> [receiptId |-> proposalId, outputId |-> outputId]]

ProposalEvidence(ordinal, mount, proposalId, kind, targetStepId,
                 savedSnapshot, requestValue, responseValue, operator,
                 requestSteps, responseSteps, rowIds) ==
  LET capture == "capture-proposal-" \o proposalId
      req == Request(capture, "raw-" \o proposalId, FALSE,
                     "POST", "/authoring/v2/construction-proposals", 200,
                     ProposalBody(SavedFromSnapshot(savedSnapshot), AbstractOutputId,
                       requestSteps),
                     ProposalResponse(proposalId, SavedFromSnapshot(savedSnapshot),
                       AbstractOutputId, responseSteps))
      selectedStep == IF kind = "create"
        THEN requestSteps[CHOOSE i \in 1..Len(requestSteps):
          requestSteps[i].operation.kind = "FILTER"].id
        ELSE targetStepId
      filter == Filter(kind, selectedStep, AbstractOutputId,
        AbstractTemporalColumnId, Case.valueKind, operator, requestValue,
        responseValue, No, proposalId, proposalId)
      ui == UI(ordinal, mount, "ready", proposalId, proposalId,
        AbstractOutputId, AbstractTemporalColumnId,
        IF kind = "remove" THEN No ELSE Case.inputType,
        IF kind = "remove" THEN No ELSE Case.inputValue,
        operator, rowIds)
  IN Event(ordinal, "FilterProposed(" \o kind \o ")", mount,
           req, savedSnapshot, NoSnapshot, ui, filter)

ExplorerRequest ==
  Request("capture-explorer", "raw-explorer", TRUE,
          "POST", "/api/v1/projects/{project}/explorers", 201,
          [intent |-> "CREATE_BLANK", title |-> "Temporal filter"],
          [explorerId |-> "Explorer-1"])
TableRequest ==
  Request("capture-table", "raw-table", TRUE,
          "POST", "/authoring/v2/commands", 200,
          [expectedDraftVersion |-> 0,
           expectedDraftDigest |-> "draft-blank",
           commands |-> <<[type |-> "CREATE_TABLE"]>>],
          [accepted |-> TRUE])
ColumnRequest ==
  Request("capture-column", "raw-column", TRUE,
          "POST", "/authoring/v2/commands", 200,
          [expectedDraftVersion |-> 1,
           expectedDraftDigest |-> "draft-table",
           commands |-> <<[type |-> "APPLY_CONSTRUCTION_CHOICE",
                           outputId |-> AbstractOutputId]>>],
          [accepted |-> TRUE])

ProposalOne == ProposalEvidence(4, Mount(0), "P-cancel", "create", No,
  SnapshotAt(AbstractSourceSnapshot, "read-proposal-cancel-before", "raw-proposal-cancel-before", Mount(0)),
  Case.initialRequestValue, Case.initialResponseValue,
  Case.initialOperator, AbstractCreatedRequestSteps, AbstractCreatedSteps,
  Case.initialRowIds)
ProposalTwo == ProposalEvidence(7, Mount(1), "P-create", "create", No,
  SnapshotAt(AbstractSourceSnapshotMount1, "read-proposal-create-before", "raw-proposal-create-before", Mount(1)),
  Case.initialRequestValue, Case.initialResponseValue,
  Case.initialOperator, AbstractCreatedRequestSteps, AbstractCreatedSteps,
  Case.initialRowIds)
ProposalEdit == ProposalEvidence(11, Mount(2), "P-edit", "edit", AbstractFilterStepId,
  SnapshotAt(AbstractEditBefore, "read-proposal-edit-before", "raw-proposal-edit-before", Mount(2)),
  Case.editedRequestValue, Case.editedResponseValue,
  Case.editedOperator, AbstractEditedRequestSteps, AbstractEditedSteps,
  Case.editedRowIds)
ProposalRemove == ProposalEvidence(14, Mount(3), "P-remove", "remove", AbstractFilterStepId,
  SnapshotAt(AbstractRemoveBefore, "read-proposal-remove-before", "raw-proposal-remove-before", Mount(3)),
  No, No, Case.editedOperator,
  OtherSteps, OtherSteps, Case.sourceRowIds)

ApplyRequest(captureId, proposalId, saved) ==
  Request(captureId, "raw-" \o captureId, TRUE,
    "POST", "/authoring/v2/commands", 200,
    [expectedDraftVersion |-> saved.draftVersion,
     expectedDraftDigest |-> saved.draftDigest,
     commands |-> <<[type |-> "APPLY_CONSTRUCTION_PROPOSAL",
                      outputId |-> AbstractOutputId,
                      proposalId |-> proposalId]>>],
    [accepted |-> TRUE])

ApplyEvent(ordinal, action, mount, captureId, proposalId, intent,
           saved, before, after, operator, requestValue, responseValue,
           savedValue, rowIds) ==
  LET req == ApplyRequest(captureId, proposalId, saved)
      filter == Filter(intent, AbstractFilterStepId, AbstractOutputId,
        AbstractTemporalColumnId, Case.valueKind, operator,
        requestValue, responseValue, savedValue, proposalId, proposalId)
      ui == UI(ordinal, mount, No, No, proposalId, AbstractOutputId,
        No, No, No, No, rowIds)
  IN Event(ordinal, action, mount, req, before, after, ui, filter)

CancelEvent ==
  Event(5, "FilterCancelled", Mount(0), NoRequest,
    SnapshotAt(AbstractSourceSnapshot, "read-cancel-before", "raw-cancel-before", Mount(0)),
    SnapshotAt(AbstractSourceSnapshot, "read-cancel-after", "raw-cancel-after", Mount(0)),
    UI(5, Mount(0), No, No, "Receipt-column", AbstractOutputId, No,
       No, No, No, Case.sourceRowIds),
    NoFilter)

ReloadEvent(ordinal, mount, beforeSnapshot, afterSnapshot, rowIds, previewReceiptId) ==
  Event(ordinal, "Reload", mount, NoRequest, beforeSnapshot, afterSnapshot,
    UI(ordinal, mount, No, No, previewReceiptId, AbstractOutputId,
       No, No, No, No, rowIds), NoFilter)

EditOpenedEvent ==
  Event(10, "FilterEditOpened", Mount(2), NoRequest, AbstractEditBefore, NoSnapshot,
    UI(10, Mount(2), No, No, "P-create", AbstractOutputId, AbstractTemporalColumnId,
       Case.inputType, Case.inputValue, Case.initialOperator, Case.initialRowIds),
    Filter("edit", AbstractFilterStepId, AbstractOutputId,
      AbstractTemporalColumnId, Case.valueKind, Case.initialOperator,
      Case.initialSavedValue, Case.initialSavedValue, Case.initialSavedValue,
      No, No))

AbstractModelEvents ==
  <<Event(1, "ExplorerCreated", Mount(0), ExplorerRequest, NoSnapshot,
      AbstractBlankSnapshot, NoUI, NoFilter),
    Event(2, "SourceTableCreated", Mount(0), TableRequest,
      AbstractBeforeTableSnapshot, AbstractTableSnapshot, NoUI, NoFilter),
    Event(3, "TemporalColumnApplied", Mount(0), ColumnRequest,
      SnapshotAt(AbstractTableSnapshot, "read-before-temporal-column", "raw-before-temporal-column", Mount(0)),
      AbstractSourceSnapshot, NoUI, NoFilter),
    ProposalOne,
    CancelEvent,
    ReloadEvent(6, Mount(1),
      SnapshotAt(AbstractSourceSnapshot, "read-reload-cancel-before", "raw-reload-cancel-before", Mount(0)),
      SnapshotAt(AbstractSourceSnapshot, "read-reload-cancel-after", "raw-reload-cancel-after", Mount(1)),
      Case.sourceRowIds, "Receipt-column"),
    ProposalTwo,
    ApplyEvent(8, "FilterApplied(create)", Mount(1), "capture-apply-create", "P-create", "create",
      [draftVersion |-> 2, draftDigest |-> "draft-source"],
      AbstractCreateBefore, AbstractCreateAfter,
      Case.initialOperator, Case.initialRequestValue,
      Case.initialResponseValue, Case.initialSavedValue,
      Case.initialRowIds),
    ReloadEvent(9, Mount(2),
      SnapshotAt(AbstractCreateAfter, "read-reload-create-before", "raw-reload-create-before", Mount(1)),
      SnapshotAt(AbstractCreateAfter, "read-reload-create-after", "raw-reload-create-after", Mount(2)),
      Case.initialRowIds, "P-create"),
    EditOpenedEvent,
    ProposalEdit,
    ApplyEvent(12, "FilterApplied(edit)", Mount(2), "capture-apply-edit", "P-edit", "edit",
      [draftVersion |-> 3, draftDigest |-> "draft-create"],
      AbstractEditBefore, AbstractEditAfter,
      Case.editedOperator, Case.editedRequestValue,
      Case.editedResponseValue, Case.editedSavedValue,
      Case.editedRowIds),
    ReloadEvent(13, Mount(3),
      SnapshotAt(AbstractEditAfter, "read-reload-edit-before", "raw-reload-edit-before", Mount(2)),
      SnapshotAt(AbstractEditAfter, "read-reload-edit-after", "raw-reload-edit-after", Mount(3)),
      Case.editedRowIds, "P-edit"),
    ProposalRemove,
    ApplyEvent(15, "FilterRemoved", Mount(3), "capture-remove-filter", "P-remove", "remove",
      [draftVersion |-> 4, draftDigest |-> "draft-edit"],
      AbstractRemoveBefore, AbstractRemoveAfter,
      Case.editedOperator, No, No, No,
      Case.sourceRowIds),
    ReloadEvent(16, Mount(4),
      SnapshotAt(AbstractRemoveAfter, "read-reload-remove-before", "raw-reload-remove-before", Mount(3)),
      SnapshotAt(AbstractRemoveAfter, "read-reload-remove-after", "raw-reload-remove-after", Mount(4)),
      Case.sourceRowIds, "P-remove")>>

JourneyActions == <<"ExplorerCreated", "SourceTableCreated", "TemporalColumnApplied",
  "FilterProposed(create)", "FilterCancelled", "Reload", "FilterProposed(create)",
  "FilterApplied(create)", "Reload", "FilterEditOpened", "FilterProposed(edit)",
  "FilterApplied(edit)", "Reload", "FilterProposed(remove)", "FilterRemoved", "Reload">>
MutationActions == {"ExplorerCreated", "SourceTableCreated", "TemporalColumnApplied",
                    "FilterApplied(create)", "FilterApplied(edit)", "FilterRemoved"}

VARIABLE journey
vars == <<journey>>

Init ==
  /\ Profile \in Profiles
  /\ InitialMountEpoch \in Nat
  /\ journey =
    [cursor |-> 0,
     phase |-> "empty",
     mountEpoch |-> InitialMountEpoch,
     savedKind |-> "empty",
     explorerId |-> No,
     outputId |-> No,
     temporalColumnId |-> No,
     persisted |-> NoSaved,
     mounted |-> NoSaved,
     savedPreview |-> NoPreview,
     proposal |-> NoProposal,
     editor |-> NoEditor,
     previewRowIds |-> <<>>,
     renderedRowIds |-> <<>>,
     history |-> <<>>,
     lastAction |-> No,
     lastMonotonicMs |-> 0,
     lastEpochMs |-> 0]

EventFits(event, action, expectedMount, hasRequest, mutation) ==
  /\ event.action = action
  /\ event.mountEpoch = expectedMount
  /\ event.atMonotonicMs >= journey.lastMonotonicMs
  /\ event.atEpochMs >= journey.lastEpochMs
  /\ IF hasRequest
       THEN event.request.isMutation = mutation
       ELSE event.request = NoRequest
  /\ IF event.request = NoRequest
       THEN TRUE
       ELSE /\ event.request.captureId # No
            /\ (event.request.requestId = No \/ Len(event.request.requestId) > 0)

SnapshotMatches(saved, snapshot) ==
  /\ snapshot # NoSnapshot
  /\ snapshot.status = 200
  /\ snapshot.draftVersion = saved.draftVersion
  /\ snapshot.draftDigest = saved.draftDigest
  /\ snapshot.workspaceDocuments = saved.workspaceDocuments
  /\ snapshot.workspaceDigest = saved.workspaceDigest
  /\ snapshot.catalogGeneration = saved.catalogGeneration
  /\ snapshot.outputId = saved.outputId
  /\ snapshot.columnId = saved.columnId
  /\ snapshot.columns = saved.columns
  /\ snapshot.steps = saved.steps

SnapshotReadAt(snapshot, mount) ==
  /\ snapshot # NoSnapshot
  /\ snapshot.readId # No
  /\ snapshot.status = 200
  /\ snapshot.mountEpoch = mount

RequestCASMatches(request, snapshot) ==
  /\ request.body.expectedDraftVersion = snapshot.draftVersion
  /\ request.body.expectedDraftDigest = snapshot.draftDigest

LinkedChoiceRequests(event) ==
  {request \in SequenceSet(ObservedRequests):
    request.captureId \in SequenceSet(event.proposalCaptureIds)
    /\ request.method = "POST"
    /\ EndsWith(request.path, "/construction-choice-proposals")}
ChoiceRequest(event) ==
  LET choices == LinkedChoiceRequests(event)
  IN IF Cardinality(choices) = 1
       THEN CHOOSE request \in choices: TRUE
       ELSE NoObservedRequest
ChoiceProposalMatches(event, columnId, outputId) ==
  LET choice == ChoiceRequest(event)
  IN IF choice.captureId = No
       THEN FALSE
       ELSE /\ choice.status = 200
            /\ choice.candidateColumnIds = <<columnId>>
            /\ choice.receiptId # No
            /\ choice.outputId = outputId

PreviewDOMMatches(ui, preview) ==
  /\ (ui.receiptId = No \/ ui.receiptId = preview.receiptId)
  /\ (ui.outputId = No \/ ui.outputId = preview.outputId)
NoActiveProposalDOM(ui, preview) ==
  /\ ui.proposalStatus = No
  /\ ui.proposalId = No
  /\ ui.columnId = No
  /\ PreviewDOMMatches(ui, preview)

StepIdentity(step) == step.id
ColumnIdentity(column) ==
  IF "columnId" \in DOMAIN column THEN column.columnId ELSE column.id
StepIndices(steps, stepId) == {i \in 1..Len(steps) : StepIdentity(steps[i]) = stepId}
UniqueStep(steps, stepId) == Cardinality(StepIndices(steps, stepId)) = 1
ColumnIndices(columns, columnId) ==
  {i \in 1..Len(columns) : ColumnIdentity(columns[i]) = columnId}
UniqueColumn(columns, columnId) == Cardinality(ColumnIndices(columns, columnId)) = 1
FilterIndices(steps) ==
  {i \in 1..Len(steps) : steps[i].operation.kind = "FILTER"}
FilterCount(steps) == Cardinality(FilterIndices(steps))
HasStep(steps, stepId) == StepIndices(steps, stepId) # {}
FilterStepAt(steps, stepId) ==
  steps[CHOOSE i \in StepIndices(steps, stepId): steps[i].operation.kind = "FILTER"]
RemoveAt(sequence, index) ==
  SubSeq(sequence, 1, index - 1) \o SubSeq(sequence, index + 1, Len(sequence))
RemoveStep(steps, stepId) ==
  IF StepIndices(steps, stepId) = {}
  THEN steps
  ELSE RemoveAt(steps, CHOOSE i \in StepIndices(steps, stepId): TRUE)
RemoveColumn(columns, columnId) ==
  IF ColumnIndices(columns, columnId) = {}
  THEN columns
  ELSE RemoveAt(columns,
       CHOOSE i \in ColumnIndices(columns, columnId): TRUE)
SavedFilterStep(saved) ==
  CHOOSE i \in FilterIndices(saved.steps): TRUE
SavedFilterValue(saved) ==
  saved.steps[SavedFilterStep(saved)].operation.filter.values[1]
SavedFilterOperator(saved) ==
  saved.steps[SavedFilterStep(saved)].operation.filter.operator
SavedFilterStepId(saved) == StepIdentity(saved.steps[SavedFilterStep(saved)])

ExpectedRowsFor(savedKind) ==
  CASE savedKind = "empty" -> <<>>
    [] savedKind = "source" -> Case.sourceRowIds
    [] savedKind = "created" -> Case.initialRowIds
    [] savedKind = "edited" -> Case.editedRowIds
    [] savedKind = "removed" -> Case.sourceRowIds
    [] OTHER -> FALSE

Commit(event, updatedJourney) ==
  journey' = [updatedJourney EXCEPT
    !.cursor = @ + 1,
    !.history = Append(@, event),
    !.lastAction = event.action,
    !.lastMonotonicMs = event.atMonotonicMs,
    !.lastEpochMs = event.atEpochMs]

ExplorerCreated(event) ==
  /\ journey.phase = "empty"
  /\ EventFits(event, "ExplorerCreated", InitialMountEpoch, TRUE, TRUE)
  /\ event.before = NoSnapshot
  /\ SnapshotReadAt(event.after, journey.mountEpoch)
  /\ event.after.outputId = No
  /\ event.after.columnId = No
  /\ event.after.columns = <<>>
  /\ event.after.steps = <<>>
  /\ event.request.method = "POST"
  /\ EndsWith(event.request.path, "/explorers")
  /\ event.request.status = 201
  /\ event.request.responseBody.explorerId # No
  /\ Commit(event, [journey EXCEPT
       !.phase = "explorer-created",
       !.savedKind = "empty",
       !.explorerId = event.request.responseBody.explorerId,
       !.persisted = SavedFromSnapshot(event.after),
       !.mounted = SavedFromSnapshot(event.after)])

SourceTableCreated(event) ==
  /\ journey.phase = "explorer-created"
  /\ EventFits(event, "SourceTableCreated", journey.mountEpoch, TRUE, TRUE)
  /\ SnapshotReadAt(event.before, journey.mountEpoch)
  /\ SnapshotReadAt(event.after, journey.mountEpoch)
  /\ SnapshotMatches(journey.persisted, event.before)
  /\ event.before.outputId = No
  /\ event.after.outputId # No
  /\ event.after.columnId = No
  /\ event.after.columns # <<>>
  /\ event.request.method = "POST"
  /\ event.request.status = 200
  /\ event.request.body.commands[1].type = "CREATE_TABLE"
  /\ RequestCASMatches(event.request, event.before)
  /\ event.after.draftVersion > event.before.draftVersion
  /\ event.after.draftDigest # event.before.draftDigest
  /\ event.after.workspaceDigest # event.before.workspaceDigest
  /\ event.after.workspaceDocuments # event.before.workspaceDocuments
  /\ Commit(event, [journey EXCEPT
       !.phase = "table-created",
       !.savedKind = "source",
       !.outputId = event.after.outputId,
       !.persisted = SavedFromSnapshot(event.after),
       !.mounted = SavedFromSnapshot(event.after),
       !.renderedRowIds = Case.sourceRowIds])

TemporalColumnApplied(event) ==
  /\ journey.phase = "table-created"
  /\ EventFits(event, "TemporalColumnApplied", journey.mountEpoch, TRUE, TRUE)
  /\ SnapshotReadAt(event.before, journey.mountEpoch)
  /\ SnapshotReadAt(event.after, journey.mountEpoch)
  /\ SnapshotMatches(journey.persisted, event.before)
  /\ event.request.method = "POST"
  /\ event.request.status = 200
  /\ event.request.body.commands[1].type = "APPLY_CONSTRUCTION_CHOICE"
  /\ RequestCASMatches(event.request, event.before)
  /\ event.before.outputId = journey.outputId
  /\ event.after.outputId = journey.outputId
  /\ event.before.columns = RemoveColumn(event.after.columns, event.after.columnId)
  /\ Len(event.after.columns) = Len(event.before.columns) + 1
  /\ ColumnIndices(event.before.columns, event.after.columnId) = {}
  /\ UniqueColumn(event.after.columns, event.after.columnId)
  /\ event.before.steps = event.after.steps
  /\ event.after.columnId # No
  /\ event.request.body.commands[1].outputId = event.after.outputId
  /\ ChoiceProposalMatches(event, event.after.columnId, event.after.outputId)
  /\ event.after.draftVersion > event.before.draftVersion
  /\ event.after.draftDigest # event.before.draftDigest
  /\ event.after.workspaceDigest # event.before.workspaceDigest
  /\ event.after.workspaceDocuments # event.before.workspaceDocuments
  /\ Commit(event, [journey EXCEPT
       !.phase = "source-ready",
       !.savedKind = "source",
       !.temporalColumnId = event.after.columnId,
       !.persisted = SavedFromSnapshot(event.after),
       !.mounted = SavedFromSnapshot(event.after),
       !.savedPreview = [receiptId |-> ChoiceRequest(event).receiptId,
                         outputId |-> ChoiceRequest(event).outputId],
       !.renderedRowIds = Case.sourceRowIds])

ProposalFrom(event) ==
  [proposalId |-> event.request.responseBody.proposalId,
   receiptId |-> event.request.responseBody.preview.receiptId,
   captureId |-> event.request.captureId,
   requestId |-> event.request.requestId,
   mountEpoch |-> event.mountEpoch,
   status |-> "ready",
   intent |-> event.filter.intent,
   stepId |-> event.filter.stepId,
   outputId |-> event.filter.outputId,
   columnId |-> event.filter.columnId,
   inputType |-> event.ui.inputType,
   inputValue |-> event.ui.inputValue,
   operator |-> event.filter.operator,
   requestValue |-> event.filter.requestValue,
   responseValue |-> event.filter.responseValue,
   savedValue |-> event.filter.savedValue,
   baseDraftVersion |-> event.before.draftVersion,
   baseDraftDigest |-> event.before.draftDigest,
   candidateSteps |-> event.request.responseBody.candidateConstruction.steps,
   rowIds |-> event.ui.rowIds]

ProposalMatches(event, kind, requestValue, responseValue, savedValue,
                operator, rowIds) ==
  LET req == event.request
      res == req.responseBody
      body == req.body
      candidateRequest == body.candidateConstruction.steps
      candidateResponse == res.candidateConstruction.steps
      filter == event.filter
      ui == event.ui
      target == filter.stepId
      saved == journey.persisted
  IN /\ req.isMutation = FALSE
     /\ req.method = "POST"
     /\ EndsWith(req.path, "/authoring/v2/construction-proposals")
     /\ req.status = 200
     /\ body.outputId = journey.outputId
     /\ RequestCASMatches(req, event.before)
     /\ res.proposalId # No
     /\ res.outputId = journey.outputId
     /\ res.draftVersion = event.before.draftVersion
     /\ res.draftDigest = event.before.draftDigest
     /\ res.preview.receiptId = res.proposalId
     /\ res.preview.outputId = journey.outputId
     /\ SnapshotReadAt(event.before, journey.mountEpoch)
     /\ SnapshotMatches(saved, event.before)
     /\ ui.mountEpoch = journey.mountEpoch
     /\ ui.proposalStatus \in {"ready", "READY"}
     /\ ui.proposalId = res.proposalId
     /\ ui.receiptId = res.preview.receiptId
     /\ ui.outputId = journey.outputId
     /\ ui.rowIds = rowIds
     /\ filter.intent = kind
     /\ filter.outputId = journey.outputId
     /\ filter.columnId = journey.temporalColumnId
     /\ filter.valueKind = Case.valueKind
     /\ filter.operator = operator
     /\ filter.requestValue = requestValue
     /\ filter.responseValue = responseValue
     /\ filter.savedValue = savedValue
     /\ filter.proposalId = res.proposalId
     /\ filter.receiptId = res.preview.receiptId
     /\ IF kind = "remove"
          THEN /\ UniqueStep(saved.steps, target)
               /\ ~HasStep(candidateRequest, target)
               /\ ~HasStep(candidateResponse, target)
               /\ FilterCount(candidateRequest) = 0
               /\ FilterCount(candidateResponse) = 0
               /\ RemoveStep(saved.steps, target) = candidateRequest
               /\ RemoveStep(saved.steps, target) = candidateResponse
               /\ ui.inputType = No
               /\ ui.inputValue = No
               /\ filter.columnId = journey.temporalColumnId
               /\ filter.requestValue = No
               /\ filter.responseValue = No
               /\ filter.savedValue = No
               /\ operator = SavedFilterOperator(saved)
          ELSE /\ FilterCount(candidateRequest) = 1
               /\ FilterCount(candidateResponse) = 1
               /\ UniqueStep(candidateRequest, target)
               /\ UniqueStep(candidateResponse, target)
               /\ filter.stepId = target
               /\ filter.columnId = journey.temporalColumnId
               /\ filter.savedValue = No
               /\ RemoveStep(candidateRequest, target) = RemoveStep(saved.steps, target)
               /\ RemoveStep(candidateResponse, target) = RemoveStep(saved.steps, target)
               /\ FilterStepAt(candidateRequest, target).operation.filter.values[1] = requestValue
               /\ FilterStepAt(candidateResponse, target).operation.filter.values[1] = responseValue
               /\ FilterStepAt(candidateRequest, target).operation.filter.columnId = journey.temporalColumnId
               /\ FilterStepAt(candidateResponse, target).operation.filter.columnId = journey.temporalColumnId
               /\ FilterStepAt(candidateRequest, target).operation.filter.operator = operator
               /\ FilterStepAt(candidateResponse, target).operation.filter.operator = operator
               /\ ui.inputType = Case.inputType
               /\ ui.inputValue = Case.inputValue
               /\ ui.columnId = journey.temporalColumnId
               /\ ui.operator = operator
               /\ IF kind = "create"
                    THEN /\ ~HasStep(saved.steps, target)
                         /\ FilterCount(saved.steps) = 0
                    ELSE /\ kind = "edit"
                         /\ UniqueStep(saved.steps, target)
               /\ RemoveStep(candidateRequest, target) = RemoveStep(candidateResponse, target)

FilterProposed(event, kind, expectedRequestValue, expectedResponseValue,
               expectedSavedValue, expectedOperator, expectedRows) ==
  /\ EventFits(event, "FilterProposed(" \o kind \o ")",
       journey.mountEpoch, TRUE, FALSE)
  /\ SnapshotReadAt(event.before, journey.mountEpoch)
  /\ event.after = NoSnapshot
  /\ journey.persisted # NoSaved
  /\ journey.mounted = journey.persisted
  /\ journey.proposal = NoProposal
  /\ expectedSavedValue = No
  /\ ProposalMatches(event, kind, expectedRequestValue, expectedResponseValue,
       expectedSavedValue, expectedOperator, expectedRows)
  /\ Commit(event, [journey EXCEPT
       !.phase = "proposal-ready",
       !.proposal = ProposalFrom(event),
       !.previewRowIds = expectedRows,
       !.editor = IF kind = "remove" THEN NoEditor ELSE
         [intent |-> kind, stepId |-> event.filter.stepId,
          inputType |-> event.ui.inputType,
          inputValue |-> event.ui.inputValue,
          operator |-> event.filter.operator,
          value |-> event.filter.requestValue]])

FilterProposedCreate(event) ==
  /\ journey.phase \in {"source-ready", "reloaded"}
  /\ FilterCount(journey.persisted.steps) = 0
  /\ FilterProposed(event, "create", Case.initialRequestValue,
       Case.initialResponseValue, No,
       Case.initialOperator, Case.initialRowIds)

FilterProposedEdit(event) ==
  /\ journey.phase = "editing"
  /\ FilterProposed(event, "edit", Case.editedRequestValue,
       Case.editedResponseValue, No,
       Case.editedOperator, Case.editedRowIds)

FilterProposedRemove(event) ==
  /\ journey.phase = "reloaded"
  /\ FilterCount(journey.persisted.steps) = 1
  /\ FilterProposed(event, "remove", No, No,
       No, SavedFilterOperator(journey.persisted),
       Case.sourceRowIds)

FilterCancelled(event) ==
  /\ journey.phase = "proposal-ready"
  /\ journey.proposal # NoProposal
  /\ journey.proposal.intent = "create"
  /\ EventFits(event, "FilterCancelled", journey.mountEpoch, FALSE, FALSE)
  /\ SnapshotReadAt(event.before, journey.mountEpoch)
  /\ SnapshotReadAt(event.after, journey.mountEpoch)
  /\ SnapshotMatches(journey.persisted, event.before)
  /\ SnapshotMatches(journey.persisted, event.after)
  /\ event.before.readId # event.after.readId
  /\ event.before.workspaceDocuments = event.after.workspaceDocuments
  /\ event.before.draftVersion = event.after.draftVersion
  /\ event.before.draftDigest = event.after.draftDigest
  /\ event.ui.mountEpoch = journey.mountEpoch
  /\ NoActiveProposalDOM(event.ui, journey.savedPreview)
  /\ event.ui.rowIds = ExpectedRowsFor(journey.savedKind)
  /\ Commit(event, [journey EXCEPT
       !.phase = "source-ready",
       !.proposal = NoProposal,
       !.editor = NoEditor,
       !.previewRowIds = <<>>,
       !.renderedRowIds = event.ui.rowIds])

Reload(event) ==
  /\ journey.phase \in {"source-ready", "created", "edited", "removed"}
  /\ journey.proposal = NoProposal
  /\ EventFits(event, "Reload", journey.mountEpoch + 1, FALSE, FALSE)
  /\ SnapshotReadAt(event.before, journey.mountEpoch)
  /\ SnapshotMatches(journey.persisted, event.before)
  /\ SnapshotReadAt(event.after, journey.mountEpoch + 1)
  /\ SnapshotMatches(journey.persisted, event.after)
  /\ event.before.readId # event.after.readId
  /\ event.ui.mountEpoch = journey.mountEpoch + 1
  /\ NoActiveProposalDOM(event.ui, journey.savedPreview)
  /\ event.ui.rowIds = ExpectedRowsFor(journey.savedKind)
  /\ Commit(event, [journey EXCEPT
       !.phase = "reloaded",
       !.mountEpoch = @ + 1,
       !.mounted = journey.persisted,
       !.proposal = NoProposal,
       !.editor = NoEditor,
       !.previewRowIds = <<>>,
       !.renderedRowIds = event.ui.rowIds])

FilterEditOpened(event) ==
  /\ journey.phase = "reloaded"
  /\ journey.persisted # NoSaved
  /\ FilterCount(journey.persisted.steps) = 1
  /\ EventFits(event, "FilterEditOpened", journey.mountEpoch, FALSE, FALSE)
  /\ SnapshotReadAt(event.before, journey.mountEpoch)
  /\ SnapshotMatches(journey.persisted, event.before)
  /\ event.after = NoSnapshot
  /\ event.ui.mountEpoch = journey.mountEpoch
  /\ event.ui.proposalStatus = No
  /\ event.ui.proposalId = No
  /\ PreviewDOMMatches(event.ui, journey.savedPreview)
  /\ event.ui.inputType = Case.inputType
  /\ event.ui.inputValue = Case.inputValue
  /\ event.ui.operator = SavedFilterOperator(journey.persisted)
  /\ event.ui.rowIds = Case.initialRowIds
  /\ Commit(event, [journey EXCEPT
       !.phase = "editing",
       !.editor = [intent |-> "edit",
                   stepId |-> SavedFilterStepId(journey.persisted),
                   inputType |-> event.ui.inputType,
                   inputValue |-> event.ui.inputValue,
                   operator |-> event.ui.operator,
                   value |-> SavedFilterValue(journey.persisted)]])

ProposalStateMatches(event, kind, expectedRequestValue,
                     expectedResponseValue, expectedSavedValue,
                     expectedOperator, expectedRows) ==
  LET proposal == journey.proposal
      filter == event.filter
      before == event.before
      after == event.after
      command == event.request.body
      persistedPreview == [receiptId |-> proposal.receiptId,
                           outputId |-> proposal.outputId]
  IN /\ proposal # NoProposal
     /\ proposal.status = "ready"
     /\ proposal.intent = kind
     /\ proposal.mountEpoch = journey.mountEpoch
     /\ proposal.outputId = journey.outputId
     /\ proposal.columnId = journey.temporalColumnId
     /\ proposal.baseDraftVersion = before.draftVersion
     /\ proposal.baseDraftDigest = before.draftDigest
     /\ proposal.operator = expectedOperator
     /\ proposal.requestValue = expectedRequestValue
     /\ proposal.responseValue = expectedResponseValue
     /\ proposal.savedValue = No
     /\ proposal.rowIds =
          IF kind = "remove" THEN Case.sourceRowIds ELSE expectedRows
     /\ proposal.proposalId = filter.proposalId
     /\ proposal.receiptId = filter.receiptId
     /\ proposal.stepId = filter.stepId
     /\ proposal.candidateSteps = after.steps
     /\ SnapshotReadAt(before, journey.mountEpoch)
     /\ event.request.isMutation = TRUE
     /\ event.request.method = "POST"
     /\ EndsWith(event.request.path, "/authoring/v2/commands")
     /\ event.request.status = 200
     /\ RequestCASMatches(event.request, before)
     /\ command.commands[1].type = "APPLY_CONSTRUCTION_PROPOSAL"
     /\ command.commands[1].outputId = journey.outputId
     /\ command.commands[1].proposalId = proposal.proposalId
     /\ SnapshotMatches(journey.persisted, before)
     /\ SnapshotReadAt(after, journey.mountEpoch)
     /\ before.readId # after.readId
     /\ after.draftVersion > before.draftVersion
     /\ after.draftDigest # before.draftDigest
     /\ after.workspaceDigest # before.workspaceDigest
     /\ after.outputId = journey.outputId
     /\ after.columnId = journey.temporalColumnId
     /\ after.columns = before.columns
     /\ after.steps = proposal.candidateSteps
     /\ filter.intent = kind
     /\ filter.stepId = proposal.stepId
     /\ filter.outputId = journey.outputId
     /\ filter.columnId = journey.temporalColumnId
     /\ filter.valueKind = Case.valueKind
     /\ filter.operator = expectedOperator
     /\ filter.requestValue = expectedRequestValue
     /\ filter.responseValue = expectedResponseValue
     /\ filter.savedValue = expectedSavedValue
     /\ event.ui.mountEpoch = journey.mountEpoch
     /\ NoActiveProposalDOM(event.ui, persistedPreview)
     /\ event.ui.rowIds = expectedRows

ApplyFilter(event, kind, action, expectedRequestValue,
            expectedResponseValue, expectedSavedValue, expectedOperator,
            expectedRows) ==
  /\ EventFits(event, action, journey.mountEpoch, TRUE, TRUE)
  /\ ProposalStateMatches(event, kind, expectedRequestValue,
       expectedResponseValue, expectedSavedValue, expectedOperator, expectedRows)
  /\ event.filter.proposalId = journey.proposal.proposalId
  /\ event.filter.receiptId = journey.proposal.receiptId
  /\ event.filter.stepId = journey.proposal.stepId
  /\ IF kind = "create"
       THEN /\ FilterCount(event.before.steps) = 0
            /\ FilterCount(event.after.steps) = 1
            /\ ~HasStep(event.before.steps, journey.proposal.stepId)
            /\ UniqueStep(event.after.steps, journey.proposal.stepId)
            /\ RemoveStep(event.after.steps, journey.proposal.stepId) = event.before.steps
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.values[1]
                 = expectedSavedValue
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.columnId
                 = journey.temporalColumnId
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.operator
                 = expectedOperator
       ELSE IF kind = "edit"
       THEN /\ FilterCount(event.before.steps) = 1
            /\ FilterCount(event.after.steps) = 1
            /\ UniqueStep(event.before.steps, journey.proposal.stepId)
            /\ UniqueStep(event.after.steps, journey.proposal.stepId)
            /\ RemoveStep(event.before.steps, journey.proposal.stepId)
                 = RemoveStep(event.after.steps, journey.proposal.stepId)
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.values[1]
                 = expectedSavedValue
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.columnId
                 = journey.temporalColumnId
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.operator
                 = expectedOperator
            /\ [FilterStepAt(event.before.steps, journey.proposal.stepId)
                 EXCEPT !.operation.filter =
                   FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter]
                 = FilterStepAt(event.after.steps, journey.proposal.stepId)
       ELSE /\ kind = "remove"
            /\ FilterCount(event.before.steps) = 1
            /\ FilterCount(event.after.steps) = 0
            /\ UniqueStep(event.before.steps, journey.proposal.stepId)
            /\ ~HasStep(event.after.steps, journey.proposal.stepId)
            /\ RemoveStep(event.before.steps, journey.proposal.stepId) = event.after.steps
  /\ Commit(event, [journey EXCEPT
       !.phase = IF kind = "create" THEN "created"
                 ELSE IF kind = "edit" THEN "edited" ELSE "removed",
       !.savedKind = IF kind = "create" THEN "created"
                     ELSE IF kind = "edit" THEN "edited" ELSE "removed",
       !.persisted = SavedFromSnapshot(event.after),
       !.mounted = SavedFromSnapshot(event.after),
       !.savedPreview = [receiptId |-> journey.proposal.receiptId,
                         outputId |-> journey.proposal.outputId],
       !.proposal = NoProposal,
       !.editor = NoEditor,
       !.previewRowIds = <<>>,
       !.renderedRowIds = event.ui.rowIds])

FilterAppliedCreate(event) ==
  /\ journey.phase = "proposal-ready"
  /\ ApplyFilter(event, "create", "FilterApplied(create)",
       Case.initialRequestValue, Case.initialResponseValue,
       Case.initialSavedValue, Case.initialOperator, Case.initialRowIds)
FilterAppliedEdit(event) ==
  /\ journey.phase = "proposal-ready"
  /\ ApplyFilter(event, "edit", "FilterApplied(edit)",
       Case.editedRequestValue, Case.editedResponseValue,
       Case.editedSavedValue, Case.editedOperator, Case.editedRowIds)
FilterRemoved(event) ==
  /\ journey.phase = "proposal-ready"
  /\ ApplyFilter(event, "remove", "FilterRemoved", No, No,
       journey.proposal.savedValue, journey.proposal.operator, Case.sourceRowIds)

ConsumeTemporalEvent(event) ==
  CASE event.action = "ExplorerCreated" -> ExplorerCreated(event)
    [] event.action = "SourceTableCreated" -> SourceTableCreated(event)
    [] event.action = "TemporalColumnApplied" -> TemporalColumnApplied(event)
    [] event.action = "FilterProposed(create)" -> FilterProposedCreate(event)
    [] event.action = "FilterProposed(edit)" -> FilterProposedEdit(event)
    [] event.action = "FilterProposed(remove)" -> FilterProposedRemove(event)
    [] event.action = "FilterCancelled" -> FilterCancelled(event)
    [] event.action = "Reload" -> Reload(event)
    [] event.action = "FilterEditOpened" -> FilterEditOpened(event)
    [] event.action = "FilterApplied(create)" -> FilterAppliedCreate(event)
    [] event.action = "FilterApplied(edit)" -> FilterAppliedEdit(event)
    [] event.action = "FilterRemoved" -> FilterRemoved(event)
    [] OTHER -> FALSE

Next ==
  IF journey.cursor < Len(ModelEvents)
  THEN ConsumeTemporalEvent(ModelEvents[journey.cursor + 1])
  ELSE UNCHANGED journey
Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

SavedStateOK(saved) ==
  IF saved = NoSaved
  THEN TRUE
  ELSE /\ saved.draftVersion \in Nat
       /\ saved.draftDigest # No
       /\ saved.workspaceDigest # No
       /\ saved.catalogGeneration # No
       /\ IF saved.outputId = No
            THEN /\ saved.columnId = No
                 /\ saved.columns = <<>>
                 /\ saved.steps = <<>>
            ELSE saved.outputId # No

TypeOK ==
  /\ journey.cursor \in 0..Len(ModelEvents)
  /\ journey.mountEpoch \in InitialMountEpoch..InitialMountEpoch + 4
  /\ journey.savedKind \in {"empty", "source", "created", "edited", "removed"}
  /\ Len(journey.history) = journey.cursor
  /\ SavedStateOK(journey.persisted)
  /\ SavedStateOK(journey.mounted)
  /\ (journey.savedPreview = NoPreview
      \/ /\ journey.savedPreview.receiptId # No
         /\ journey.savedPreview.outputId = journey.outputId)
  /\ journey.previewRowIds \in {<<>>, Case.initialRowIds,
       Case.editedRowIds, Case.sourceRowIds}
  /\ journey.renderedRowIds \in {<<>>, Case.initialRowIds,
       Case.editedRowIds, Case.sourceRowIds}

InvOnlyRegisteredProfiles ==
  /\ Profile \in {"DATE", "DATE_TIME"}
  /\ IF Profile = "DATE"
       THEN /\ Case.caseName = "date"
            /\ Case.resourceType = "Patient"
            /\ Case.fieldPath = "birthDate"
            /\ Case.inputType = "date"
            /\ Case.sourceRowIds = <<"dev-patient-001", "dev-patient-002">>
            /\ Case.initialRowIds = <<"dev-patient-002">>
            /\ Case.editedRowIds = <<"dev-patient-001", "dev-patient-002">>
       ELSE /\ Case.caseName = "date-time"
            /\ Case.resourceType = "Observation"
            /\ Case.fieldPath = "effectiveDateTime"
            /\ Case.inputType = "datetime-local"
            /\ Case.sourceRowIds = <<"dev-observation-001", "dev-observation-002",
                                     "dev-observation-003">>
            /\ Case.initialRowIds = <<>>
            /\ Case.editedRowIds = <<"dev-observation-001", "dev-observation-002",
                                     "dev-observation-003">>
InvOrderedTrace ==
  \A i \in 1..journey.cursor: journey.history[i].action = JourneyActions[i]
InvSemanticActionOrder ==
  \A i \in 1..Len(JourneyActions): ModelEvents[i].action = JourneyActions[i]
ObservedMutationRequests ==
  {request \in SequenceSet(ObservedRequests): request.isMutation = TRUE}
ObservedMutationIds ==
  {request.captureId : request \in ObservedMutationRequests}
EventRequests ==
  SequenceSet([i \in 1..Len(ModelEvents) |-> ModelEvents[i].request])
EventMutationRequests ==
  {request \in EventRequests: request.isMutation = TRUE}
EventMutationIds ==
  {request.captureId : request \in EventMutationRequests}
ObservedMutationCount ==
  Cardinality({i \in 1..Len(ObservedRequests): ObservedRequests[i].isMutation = TRUE})
EventMutationCount ==
  Cardinality({i \in 1..Len(ModelEvents): ModelEvents[i].request.isMutation = TRUE})
InvObservedRequestIDsUnique ==
  \A i, j \in 1..Len(ObservedRequests):
    i # j => ObservedRequests[i].captureId # ObservedRequests[j].captureId
InvObservedMutationsConsumed ==
  /\ ObservedMutationIds = EventMutationIds
  /\ Cardinality(ObservedMutationIds) = ObservedMutationCount
  /\ Cardinality(EventMutationIds) = EventMutationCount
InvChoiceProposalBindsNewColumn ==
  \A i \in 1..journey.cursor:
    LET event == journey.history[i]
    IN IF event.action = "TemporalColumnApplied"
       THEN ChoiceProposalMatches(event, event.after.columnId, event.after.outputId)
       ELSE TRUE
InvInactiveDOMKeepsProposalAndColumnAbsent ==
  \A i \in 1..journey.cursor:
    LET event == journey.history[i]
    IN IF event.action \in {"FilterCancelled", "Reload",
          "FilterApplied(create)", "FilterApplied(edit)", "FilterRemoved"}
       THEN /\ event.ui.proposalStatus = No
            /\ event.ui.proposalId = No
            /\ event.ui.columnId = No
       ELSE TRUE
ObservedProposalRequests ==
  {request \in SequenceSet(ObservedRequests):
    request.method = "POST"
    /\ (EndsWith(request.path, "/construction-proposals")
        \/ EndsWith(request.path, "/construction-choice-proposals"))}
ObservedProposalIds ==
  {request.captureId : request \in ObservedProposalRequests}
LinkedProposalIds ==
  UNION {SequenceSet(ModelEvents[i].proposalCaptureIds)
    : i \in 1..Len(ModelEvents)}
InvProposalEvidenceLinked ==
  /\ LinkedProposalIds = ObservedProposalIds
  /\ \A i \in 1..Len(ModelEvents):
       IF ModelEvents[i].action \in {"FilterProposed(create)",
            "FilterProposed(edit)", "FilterProposed(remove)"}
       THEN /\ Len(ModelEvents[i].proposalCaptureIds) = 1
            /\ ModelEvents[i].proposalCaptureIds[1]
                 = ModelEvents[i].request.captureId
       ELSE TRUE
  /\ \A i \in 1..Len(ModelEvents):
       \A j \in 1..Len(ModelEvents):
         \A p \in 1..Len(ModelEvents[i].proposalCaptureIds):
           \A q \in 1..Len(ModelEvents[j].proposalCaptureIds):
             (i # j \/ p # q) =>
               ModelEvents[i].proposalCaptureIds[p]
                 # ModelEvents[j].proposalCaptureIds[q]
InvAllWritesConsumed ==
  \A i \in 1..journey.cursor:
    (journey.history[i].request # NoRequest)
      => (journey.history[i].request.isMutation = TRUE
          <=> journey.history[i].action \in MutationActions)
InvMountOwnsProposal ==
  journey.proposal = NoProposal
  \/ /\ journey.proposal.mountEpoch = journey.mountEpoch
     /\ journey.proposal.outputId = journey.outputId
     /\ journey.proposal.columnId = journey.temporalColumnId
InvMountedStateMatchesSaved ==
  journey.mounted = journey.persisted
InvStableColumnIdentity ==
  journey.cursor < 3
  \/ journey.temporalColumnId = journey.persisted.columnId
InvRenderMatchesSavedFilter ==
  journey.renderedRowIds = ExpectedRowsFor(journey.savedKind)
InvProposalPreviewRowsMatch ==
  journey.proposal = NoProposal
  \/ journey.proposal.rowIds = journey.previewRowIds
InvEveryProposalEchoesOwnerAndPredicate ==
  \A i \in 1..journey.cursor:
    LET e == journey.history[i]
        requestCandidate == e.request.body.candidateConstruction.steps
        responseCandidate == e.request.responseBody.candidateConstruction.steps
        target == e.filter.stepId
    IN IF e.action \in {"FilterProposed(create)", "FilterProposed(edit)",
                         "FilterProposed(remove)"}
       THEN /\ e.request.captureId # No
            /\ e.request.responseBody.proposalId = e.filter.proposalId
            /\ e.request.responseBody.preview.receiptId = e.filter.receiptId
            /\ e.request.body.outputId = e.filter.outputId
            /\ e.request.responseBody.outputId = e.filter.outputId
            /\ e.request.body.expectedDraftVersion = e.before.draftVersion
            /\ e.request.body.expectedDraftDigest = e.before.draftDigest
            /\ e.request.responseBody.draftVersion = e.before.draftVersion
            /\ e.request.responseBody.draftDigest = e.before.draftDigest
            /\ e.ui.proposalId = e.filter.proposalId
            /\ e.ui.receiptId = e.filter.receiptId
            /\ e.ui.outputId = e.filter.outputId
            /\ IF e.filter.intent = "remove"
                 THEN /\ FilterCount(requestCandidate) = 0
                      /\ FilterCount(responseCandidate) = 0
                 ELSE /\ FilterCount(requestCandidate) = 1
                      /\ FilterCount(responseCandidate) = 1
                      /\ UniqueStep(requestCandidate, target)
                      /\ UniqueStep(responseCandidate, target)
                      /\ FilterStepAt(requestCandidate, target).operation.filter.columnId
                           = e.filter.columnId
                      /\ FilterStepAt(responseCandidate, target).operation.filter.columnId
                           = e.filter.columnId
                      /\ FilterStepAt(requestCandidate, target).operation.filter.operator
                           = e.filter.operator
                      /\ FilterStepAt(responseCandidate, target).operation.filter.operator
                           = e.filter.operator
                      /\ FilterStepAt(requestCandidate, target).operation.filter.values[1]
                           = e.filter.requestValue
                      /\ FilterStepAt(responseCandidate, target).operation.filter.values[1]
                           = e.filter.responseValue
       ELSE TRUE
InvAllOtherStepsAndColumnsPreserved ==
  \A i \in 1..journey.cursor:
    LET e == journey.history[i]
        target == e.filter.stepId
    IN IF e.action = "FilterApplied(create)"
       THEN /\ FilterCount(e.after.steps) = FilterCount(e.before.steps) + 1
            /\ RemoveStep(e.after.steps, target) = e.before.steps
            /\ e.after.columns = e.before.columns
       ELSE IF e.action = "FilterApplied(edit)"
       THEN /\ RemoveStep(e.before.steps, target) = RemoveStep(e.after.steps, target)
            /\ StepIdentity(e.before.steps[CHOOSE j \in StepIndices(e.before.steps, target): TRUE])
                 = StepIdentity(e.after.steps[CHOOSE j \in StepIndices(e.after.steps, target): TRUE])
            /\ e.after.columns = e.before.columns
       ELSE IF e.action = "FilterRemoved"
       THEN /\ RemoveStep(e.before.steps, target) = e.after.steps
            /\ e.after.columns = e.before.columns
            /\ e.after.steps = RemoveStep(e.before.steps, target)
       ELSE TRUE
InvApplyUsesCurrentProposalAndCAS ==
  \A i \in 1..journey.cursor:
    LET e == journey.history[i]
    IN IF e.action \in {"FilterApplied(create)", "FilterApplied(edit)", "FilterRemoved"}
       THEN /\ e.request.isMutation = TRUE
            /\ e.request.body.expectedDraftVersion = e.before.draftVersion
            /\ e.request.body.expectedDraftDigest = e.before.draftDigest
            /\ e.request.body.commands[1].type = "APPLY_CONSTRUCTION_PROPOSAL"
            /\ e.request.body.commands[1].proposalId = e.filter.proposalId
            /\ e.request.body.commands[1].outputId = e.filter.outputId
            /\ e.after.draftVersion > e.before.draftVersion
            /\ e.before.workspaceDocuments # e.after.workspaceDocuments
            /\ e.filter.proposalId # No
            /\ e.filter.receiptId = e.filter.proposalId
       ELSE TRUE
InvTypedProfileValues ==
  /\ Case.initialOperator = "GT"
  /\ Case.editedOperator = "GTE"
  /\ Case.valueKind = IF Profile = "DATE" THEN "DATE" ELSE "DATE_TIME"
  /\ Case.initialRequestValue.kind = Case.valueKind
  /\ Case.initialResponseValue.kind = Case.valueKind
  /\ Case.initialSavedValue.kind = Case.valueKind
  /\ Case.editedRequestValue.kind = Case.valueKind
  /\ Case.editedResponseValue.kind = Case.valueKind
  /\ Case.editedSavedValue.kind = Case.valueKind
  /\ IF Profile = "DATE"
       THEN /\ Case.initialRequestValue.date = "1980-01-02"
            /\ Case.initialResponseValue.date = "1980-01-02"
            /\ Case.initialSavedValue.date = "1980-01-02"
            /\ Case.editedRequestValue.date = "1980-01-02"
            /\ Case.editedResponseValue.date = "1980-01-02"
            /\ Case.editedSavedValue.date = "1980-01-02"
       ELSE /\ Case.initialRequestValue.dateTime = "2026-01-02T00:00:00.000Z"
            /\ Case.initialResponseValue.dateTime = "2026-01-02T00:00:00Z"
            /\ Case.initialSavedValue.dateTime = "2026-01-02T00:00:00Z"
            /\ Case.editedRequestValue.dateTime = "2026-01-02T00:00:00Z"
            /\ Case.editedResponseValue.dateTime = "2026-01-02T00:00:00Z"
            /\ Case.editedSavedValue.dateTime = "2026-01-02T00:00:00Z"
InvReloadRestoresPersisted ==
  \A i \in 1..journey.cursor:
    LET e == journey.history[i]
    IN IF e.action = "Reload"
       THEN /\ e.after.status = 200
            /\ e.before.status = 200
            /\ e.before.readId # e.after.readId
            /\ e.before.workspaceDocuments = e.after.workspaceDocuments
            /\ e.before.draftVersion = e.after.draftVersion
            /\ e.before.draftDigest = e.after.draftDigest
            /\ e.before.mountEpoch + 1 = e.after.mountEpoch
            /\ e.ui.mountEpoch = e.mountEpoch
            /\ e.ui.rowIds =
                 CASE i = 6 -> Case.sourceRowIds
                   [] i = 9 -> Case.initialRowIds
                   [] i = 13 -> Case.editedRowIds
                   [] i = 16 -> Case.sourceRowIds
                   [] OTHER -> FALSE
       ELSE TRUE
InvPreviewRowsAreLiteral ==
  \A i \in 1..journey.cursor:
    LET e == journey.history[i]
    IN IF e.action = "FilterProposed(create)"
       THEN e.ui.rowIds = Case.initialRowIds
       ELSE IF e.action = "FilterProposed(edit)"
       THEN e.ui.rowIds = Case.editedRowIds
       ELSE IF e.action = "FilterProposed(remove)"
       THEN e.ui.rowIds = Case.sourceRowIds
       ELSE TRUE

Complete == journey.cursor = Len(ModelEvents)
EventuallyComplete == <> Complete

=============================================================================
