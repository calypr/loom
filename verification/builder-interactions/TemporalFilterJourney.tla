---------------- MODULE TemporalFilterJourney ----------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANT Profile

(***************************************************************************
The public transition operators consume normalized events with action,
atMonotonicMs, atEpochMs, mountEpoch, request, before, after, ui, and filter
fields. Request evidence retains captureId, optional requestId and
responseRequestId, isMutation, method, path, status, body, and responseBody.
Saved snapshots retain complete workspaceDocuments, workspaceDigest,
catalogGeneration, draftVersion, draftDigest, outputId, columnId, columns, and
steps. UI evidence retains proposal/receipt/output/column IDs, native controls,
and row IDs. Filter evidence retains intent, target step, and typed
request/response/saved values.
ModelEvents below use opaque document/digest tokens as bounded fixtures, not
browser evidence. A later adapter supplies actual IDs, complete documents,
canonical digests, times, controls, and rows.
***************************************************************************)

Profiles == {"DATE", "DATE_TIME"}
No == "No"
NoSaved ==
  [outputId |-> No, columnId |-> No, columns |-> <<>>, steps |-> <<>>,
   draftVersion |-> 0, draftDigest |-> No, workspaceDigest |-> No,
   catalogGeneration |-> No, workspaceDocuments |-> <<>>]
NoProposal ==
  [proposalId |-> No, receiptId |-> No, captureId |-> No,
   requestId |-> No, responseRequestId |-> No, mountEpoch |-> 0,
   status |-> No, intent |-> No, stepId |-> No, outputId |-> No,
   columnId |-> No, inputType |-> No, inputValue |-> No, operator |-> No,
   requestValue |-> No, responseValue |-> No, savedValue |-> No,
   baseDraftVersion |-> 0, baseDraftDigest |-> No,
   candidateSteps |-> <<>>, rowIds |-> <<>>]
NoEditor ==
  [intent |-> No, stepId |-> No, inputType |-> No,
   inputValue |-> No, operator |-> No, value |-> No]
NoRequest == [captureId |-> No, requestId |-> No,
               responseRequestId |-> No, isMutation |-> No,
               method |-> No, path |-> No, status |-> No,
               body |-> No, responseBody |-> No]
NoSnapshot ==
  [readId |-> No, requestId |-> No, status |-> No,
   catalogGeneration |-> No, draftVersion |-> 0, draftDigest |-> No,
   workspaceDocuments |-> <<>>, workspaceDigest |-> No,
   outputId |-> No, columnId |-> No, columns |-> <<>>, steps |-> <<>>]
NoUI == [capturedAtMonotonicMs |-> 0, capturedAtEpochMs |-> 0,
         mountEpoch |-> 0, proposalStatus |-> No, proposalId |-> No,
         receiptId |-> No, outputId |-> No, columnId |-> No,
         inputType |-> No, inputValue |-> No, operator |-> No, rowIds |-> <<>>]
NoFilter == [intent |-> No, stepId |-> No, outputId |-> No,
             columnId |-> No, valueKind |-> No, operator |-> No,
             requestValue |-> No, responseValue |-> No, savedValue |-> No,
             proposalId |-> No, receiptId |-> No]

EndsWith(value, suffix) ==
  /\ Len(value) >= Len(suffix)
  /\ SubSeq(value, Len(value) - Len(suffix) + 1, Len(value)) = suffix

Case ==
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
  <<[stepId |-> "Source-step", operation |-> [kind |-> "SOURCE"]],
    [stepId |-> "Other-step", operation |-> [kind |-> "OTHER"]]>>
RootColumns == <<AbstractRootColumnId>>
SourceColumns == Append(RootColumns, AbstractTemporalColumnId)

FilterStep(stepId, columnId, value, operator) ==
  [stepId |-> stepId,
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

AbstractSnapshot(readId, rawId, docs, version, draftDigest, workspaceDigest,
                 outputId, columnId, columns, steps) ==
  [readId |-> readId,
   requestId |-> rawId,
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
AbstractTableSnapshot ==
  AbstractSnapshot("read-table", "raw-read-table",
    AbstractTableDocuments, 0,
    "draft-table", "workspace-table", AbstractOutputId, No,
    RootColumns, OtherSteps)
AbstractSourceSnapshot ==
  AbstractSnapshot("read-source", "raw-read-source", AbstractSourceDocuments, 1,
    "draft-source", "workspace-source", AbstractOutputId,
    AbstractTemporalColumnId, SourceColumns, OtherSteps)
AbstractCreateBefore ==
  AbstractSnapshot("read-before-create", "raw-read-before-create",
    AbstractSourceDocuments, 1, "draft-source", "workspace-source",
    AbstractOutputId, AbstractTemporalColumnId, SourceColumns, OtherSteps)
AbstractCreateAfter ==
  AbstractSnapshot("read-after-create", "raw-read-after-create",
    AbstractCreatedDocuments, 2, "draft-create", "workspace-create",
    AbstractOutputId, AbstractTemporalColumnId, SourceColumns, AbstractCreatedSteps)
AbstractEditBefore ==
  AbstractSnapshot("read-before-edit", "raw-read-before-edit",
    AbstractCreatedDocuments, 2, "draft-create", "workspace-create",
    AbstractOutputId, AbstractTemporalColumnId, SourceColumns, AbstractCreatedSteps)
AbstractEditAfter ==
  AbstractSnapshot("read-after-edit", "raw-read-after-edit",
    AbstractEditedDocuments, 3, "draft-edit", "workspace-edit",
    AbstractOutputId, AbstractTemporalColumnId, SourceColumns, AbstractEditedSteps)
AbstractRemoveBefore ==
  AbstractSnapshot("read-before-remove", "raw-read-before-remove",
    AbstractEditedDocuments, 3, "draft-edit", "workspace-edit",
    AbstractOutputId, AbstractTemporalColumnId, SourceColumns, AbstractEditedSteps)
AbstractRemoveAfter ==
  AbstractSnapshot("read-after-remove", "raw-read-after-remove",
    AbstractRemovedDocuments, 4, "draft-remove", "workspace-remove",
    AbstractOutputId, AbstractTemporalColumnId, SourceColumns, OtherSteps)

Request(captureId, requestId, responseRequestId, isMutation,
        method, path, status, body, responseBody) ==
  [captureId |-> captureId,
   requestId |-> requestId,
   responseRequestId |-> responseRequestId,
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
  [action |-> action,
   atMonotonicMs |-> ordinal * 10,
   atEpochMs |-> ordinal * 1000,
   mountEpoch |-> mount,
   request |-> request,
   before |-> before,
   after |-> after,
   ui |-> ui,
   filter |-> filter,
   evidenceKind |-> "abstract-model-fixture"]

ProposalBody(kind, targetStepId, outputId, columnId,
             candidateSteps, value, operator) ==
  [intent |-> kind,
   targetStepId |-> targetStepId,
   outputId |-> outputId,
   columnId |-> columnId,
   operator |-> operator,
   value |-> value,
   candidateConstruction |-> [steps |-> candidateSteps]]
ProposalResponse(proposalId, outputId, columnId, candidateSteps,
                 value, operator, rowIds) ==
  [proposalId |-> proposalId,
   previewStatus |-> "READY",
   value |-> value,
   candidateConstruction |-> [steps |-> candidateSteps],
   preview |-> [receiptId |-> proposalId,
                outputId |-> outputId,
                columnId |-> columnId,
                operator |-> operator,
                rowIds |-> rowIds]]

ProposalEvidence(ordinal, mount, proposalId, kind, targetStepId,
                 requestValue, responseValue, savedValue, operator,
                 requestSteps, responseSteps, rowIds) ==
  LET capture == "capture-proposal-" \o proposalId
      req == Request(capture, "raw-" \o proposalId, "raw-" \o proposalId,
                     FALSE, "POST", "/authoring/v2/construction-proposals", 200,
                     ProposalBody(kind, targetStepId, AbstractOutputId,
                       AbstractTemporalColumnId, requestSteps, requestValue, operator),
                     ProposalResponse(proposalId, AbstractOutputId,
                       AbstractTemporalColumnId, responseSteps, responseValue,
                       operator, rowIds))
      filter == Filter(kind, AbstractFilterStepId, AbstractOutputId,
        AbstractTemporalColumnId, Case.valueKind, operator, requestValue,
        responseValue, savedValue, proposalId, proposalId)
      ui == UI(ordinal, mount, "ready", proposalId, proposalId,
        AbstractOutputId, AbstractTemporalColumnId,
        IF kind = "remove" THEN No ELSE Case.inputType,
        IF kind = "remove" THEN No ELSE Case.inputValue,
        operator, rowIds)
  IN Event(ordinal, "FilterProposed(" \o kind \o ")", mount,
           req, NoSnapshot, NoSnapshot, ui, filter)

ExplorerRequest ==
  Request("capture-explorer", "raw-explorer", "raw-explorer", TRUE,
          "POST", "/api/v1/projects/{project}/explorers", 201,
          [intent |-> "CREATE_BLANK", title |-> "Temporal filter"],
          [explorerId |-> "Explorer-1"])
TableRequest ==
  Request("capture-table", "raw-table", "raw-table", TRUE,
          "POST", "/authoring/v2/commands", 200,
          [commands |-> <<[type |-> "CREATE_TABLE",
                           outputId |-> AbstractOutputId]>>],
          [outputId |-> AbstractOutputId])
ColumnRequest ==
  Request("capture-column", "raw-column", "raw-column", TRUE,
          "POST", "/authoring/v2/commands", 200,
          [commands |-> <<[type |-> "APPLY_CONSTRUCTION_CHOICE",
                           outputId |-> AbstractOutputId,
                           columnId |-> AbstractTemporalColumnId]>>],
          [accepted |-> TRUE])

ProposalOne == ProposalEvidence(4, 0, "P-cancel", "create", No,
  Case.initialRequestValue, Case.initialResponseValue, Case.initialSavedValue,
  Case.initialOperator, AbstractCreatedRequestSteps, AbstractCreatedSteps,
  Case.initialRowIds)
ProposalTwo == ProposalEvidence(7, 1, "P-create", "create", No,
  Case.initialRequestValue, Case.initialResponseValue, Case.initialSavedValue,
  Case.initialOperator, AbstractCreatedRequestSteps, AbstractCreatedSteps,
  Case.initialRowIds)
ProposalEdit == ProposalEvidence(11, 2, "P-edit", "edit", AbstractFilterStepId,
  Case.editedRequestValue, Case.editedResponseValue, Case.editedSavedValue,
  Case.editedOperator, AbstractEditedRequestSteps, AbstractEditedSteps,
  Case.editedRowIds)
ProposalRemove == ProposalEvidence(14, 3, "P-remove", "remove", AbstractFilterStepId,
  No, No, Case.editedSavedValue, Case.editedOperator,
  OtherSteps, OtherSteps, Case.sourceRowIds)

ApplyRequest(captureId, proposalId, saved) ==
  Request(captureId, "raw-" \o captureId, "raw-" \o captureId, TRUE,
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
      ui == UI(ordinal, mount, "none", No, No, AbstractOutputId,
        AbstractTemporalColumnId, No, No, No, rowIds)
  IN Event(ordinal, action, mount, req, before, after, ui, filter)

CancelEvent ==
  Event(5, "FilterCancelled", 0, NoRequest, NoSnapshot,
    AbstractSourceSnapshot,
    UI(5, 0, "none", No, No, AbstractOutputId, AbstractTemporalColumnId,
       No, No, No, Case.sourceRowIds),
    Filter("create", AbstractFilterStepId, AbstractOutputId,
      AbstractTemporalColumnId, Case.valueKind, Case.initialOperator,
      Case.initialRequestValue, Case.initialResponseValue,
      Case.initialSavedValue, "P-cancel", "P-cancel"))

ReloadEvent(ordinal, mount, readId, rawId, savedSnapshot, rowIds) ==
  Event(ordinal, "Reload", mount, NoRequest, NoSnapshot, savedSnapshot,
    UI(ordinal, mount, "none", No, No, AbstractOutputId,
       AbstractTemporalColumnId, No, No, No, rowIds), NoFilter)

EditOpenedEvent ==
  Event(10, "FilterEditOpened", 2, NoRequest, NoSnapshot, NoSnapshot,
    UI(10, 2, "none", No, No, AbstractOutputId, AbstractTemporalColumnId,
       Case.inputType, Case.inputValue, Case.initialOperator, Case.initialRowIds),
    Filter("edit", AbstractFilterStepId, AbstractOutputId,
      AbstractTemporalColumnId, Case.valueKind, Case.initialOperator,
      Case.initialSavedValue, Case.initialSavedValue, Case.initialSavedValue,
      No, No))

ModelEvents ==
  <<Event(1, "ExplorerCreated", 0, ExplorerRequest, NoSnapshot, NoSnapshot, NoUI, NoFilter),
    Event(2, "SourceTableCreated", 0, TableRequest, NoSnapshot, NoSnapshot, NoUI, NoFilter),
    Event(3, "TemporalColumnApplied", 0, ColumnRequest,
      AbstractTableSnapshot, AbstractSourceSnapshot, NoUI, NoFilter),
    ProposalOne,
    CancelEvent,
    ReloadEvent(6, 1, "read-reload-cancel", "raw-read-reload-cancel",
      AbstractSourceSnapshot, Case.sourceRowIds),
    ProposalTwo,
    ApplyEvent(8, "FilterApplied(create)", 1, "capture-apply-create", "P-create", "create",
      [draftVersion |-> 1, draftDigest |-> "draft-source"],
      AbstractCreateBefore, AbstractCreateAfter,
      Case.initialOperator, Case.initialRequestValue,
      Case.initialResponseValue, Case.initialSavedValue,
      Case.initialRowIds),
    ReloadEvent(9, 2, "read-reload-create", "raw-read-reload-create",
      AbstractCreateAfter, Case.initialRowIds),
    EditOpenedEvent,
    ProposalEdit,
    ApplyEvent(12, "FilterApplied(edit)", 2, "capture-apply-edit", "P-edit", "edit",
      [draftVersion |-> 2, draftDigest |-> "draft-create"],
      AbstractEditBefore, AbstractEditAfter,
      Case.editedOperator, Case.editedRequestValue,
      Case.editedResponseValue, Case.editedSavedValue,
      Case.editedRowIds),
    ReloadEvent(13, 3, "read-reload-edit", "raw-read-reload-edit",
      AbstractEditAfter, Case.editedRowIds),
    ProposalRemove,
    ApplyEvent(15, "FilterRemoved", 3, "capture-remove-filter", "P-remove", "remove",
      [draftVersion |-> 3, draftDigest |-> "draft-edit"],
      AbstractRemoveBefore, AbstractRemoveAfter,
      Case.editedOperator, No, No, Case.editedSavedValue,
      Case.sourceRowIds),
    ReloadEvent(16, 4, "read-reload-remove", "raw-read-reload-remove",
      AbstractRemoveAfter, Case.sourceRowIds)>>

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
  /\ journey =
    [cursor |-> 0,
     phase |-> "empty",
     mountEpoch |-> 0,
     explorerId |-> No,
     outputId |-> No,
     temporalColumnId |-> No,
     persisted |-> NoSaved,
     mounted |-> NoSaved,
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
            /\ (event.request.requestId = No
                \/ event.request.responseRequestId = No
                \/ event.request.requestId = event.request.responseRequestId)

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

StepIndices(steps, stepId) == {i \in 1..Len(steps) : steps[i].stepId = stepId}
UniqueStep(steps, stepId) == Cardinality(StepIndices(steps, stepId)) = 1
ColumnIndices(columns, columnId) == {i \in 1..Len(columns) : columns[i] = columnId}
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
  IF {i \in 1..Len(columns) : columns[i] = columnId} = {}
  THEN columns
  ELSE RemoveAt(columns,
       CHOOSE i \in 1..Len(columns): columns[i] = columnId)
SavedFilterStep(saved) ==
  CHOOSE i \in FilterIndices(saved.steps): TRUE
SavedFilterValue(saved) ==
  saved.steps[SavedFilterStep(saved)].operation.filter.values[1]
SavedFilterOperator(saved) ==
  saved.steps[SavedFilterStep(saved)].operation.filter.operator
SavedFilterStepId(saved) == saved.steps[SavedFilterStep(saved)].stepId

ExpectedRows(saved) ==
  IF saved = NoSaved
  THEN <<>>
  ELSE IF FilterCount(saved.steps) = 0
  THEN Case.sourceRowIds
  ELSE IF SavedFilterOperator(saved) = Case.initialOperator
  THEN Case.initialRowIds
  ELSE Case.editedRowIds

Commit(event, updatedJourney) ==
  journey' = [updatedJourney EXCEPT
    !.cursor = @ + 1,
    !.history = Append(@, event),
    !.lastAction = event.action,
    !.lastMonotonicMs = event.atMonotonicMs,
    !.lastEpochMs = event.atEpochMs]

ExplorerCreated(event) ==
  /\ journey.phase = "empty"
  /\ EventFits(event, "ExplorerCreated", 0, TRUE, TRUE)
  /\ event.before = NoSnapshot
  /\ event.after = NoSnapshot
  /\ event.request.method = "POST"
  /\ EndsWith(event.request.path, "/explorers")
  /\ event.request.status = 201
  /\ event.request.responseBody.explorerId # No
  /\ Commit(event, [journey EXCEPT
       !.phase = "explorer-created",
       !.explorerId = event.request.responseBody.explorerId])

SourceTableCreated(event) ==
  /\ journey.phase = "explorer-created"
  /\ EventFits(event, "SourceTableCreated", 0, TRUE, TRUE)
  /\ event.request.method = "POST"
  /\ event.request.status = 200
  /\ event.request.body.commands[1].type = "CREATE_TABLE"
  /\ event.request.responseBody.outputId # No
  /\ Commit(event, [journey EXCEPT
       !.phase = "table-created",
       !.outputId = event.request.responseBody.outputId])

TemporalColumnApplied(event) ==
  /\ journey.phase = "table-created"
  /\ EventFits(event, "TemporalColumnApplied", 0, TRUE, TRUE)
  /\ event.request.method = "POST"
  /\ event.request.status = 200
  /\ event.request.body.commands[1].type = "APPLY_CONSTRUCTION_CHOICE"
  /\ event.before.outputId = journey.outputId
  /\ event.after.outputId = journey.outputId
  /\ event.before.columns = RemoveColumn(event.after.columns, event.after.columnId)
  /\ Len(event.after.columns) = Len(event.before.columns) + 1
  /\ ColumnIndices(event.before.columns, event.after.columnId) = {}
  /\ UniqueColumn(event.after.columns, event.after.columnId)
  /\ event.before.steps = event.after.steps
  /\ event.after.columnId # No
  /\ SnapshotMatches(SavedFromSnapshot(event.after), event.after)
  /\ Commit(event, [journey EXCEPT
       !.phase = "source-ready",
       !.temporalColumnId = event.after.columnId,
       !.persisted = SavedFromSnapshot(event.after),
       !.mounted = SavedFromSnapshot(event.after),
       !.renderedRowIds = Case.sourceRowIds])

ProposalFrom(event) ==
  [proposalId |-> event.request.responseBody.proposalId,
   receiptId |-> event.request.responseBody.preview.receiptId,
   captureId |-> event.request.captureId,
   requestId |-> event.request.requestId,
   responseRequestId |-> event.request.responseRequestId,
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
   baseDraftVersion |-> journey.persisted.draftVersion,
   baseDraftDigest |-> journey.persisted.draftDigest,
   candidateSteps |-> event.request.responseBody.candidateConstruction.steps,
   rowIds |-> event.request.responseBody.preview.rowIds]

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
     /\ body.intent = kind
     /\ body.outputId = journey.outputId
     /\ body.columnId = journey.temporalColumnId
     /\ body.operator = operator
     /\ body.value = requestValue
     /\ res.proposalId # No
     /\ res.previewStatus = "READY"
     /\ res.value = responseValue
     /\ res.preview.receiptId = res.proposalId
     /\ res.preview.outputId = journey.outputId
     /\ res.preview.columnId = journey.temporalColumnId
     /\ res.preview.operator = operator
     /\ res.preview.rowIds = rowIds
     /\ ui.mountEpoch = journey.mountEpoch
     /\ ui.proposalStatus = "ready"
     /\ ui.proposalId = res.proposalId
     /\ ui.receiptId = res.preview.receiptId
     /\ ui.outputId = journey.outputId
     /\ ui.columnId = journey.temporalColumnId
     /\ ui.operator = operator
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
          THEN /\ body.targetStepId = target
               /\ UniqueStep(saved.steps, target)
               /\ ~HasStep(candidateRequest, target)
               /\ ~HasStep(candidateResponse, target)
               /\ FilterCount(candidateRequest) = 0
               /\ FilterCount(candidateResponse) = 0
               /\ RemoveStep(saved.steps, target) = candidateResponse
               /\ ui.inputType = No
               /\ ui.inputValue = No
               /\ savedValue = SavedFilterValue(saved)
               /\ operator = SavedFilterOperator(saved)
          ELSE /\ ui.inputType = Case.inputType
               /\ ui.inputValue # No
               /\ IF kind = "create"
                    THEN /\ body.targetStepId = No
                         /\ ~HasStep(saved.steps, target)
                         /\ FilterCount(candidateResponse) = FilterCount(saved.steps) + 1
                         /\ RemoveStep(candidateResponse, target) = saved.steps
                    ELSE /\ kind = "edit"
                         /\ body.targetStepId = target
                         /\ UniqueStep(saved.steps, target)
                         /\ UniqueStep(candidateRequest, target)
                         /\ UniqueStep(candidateResponse, target)
                         /\ FilterCount(candidateResponse) = FilterCount(saved.steps)
                         /\ RemoveStep(candidateResponse, target) = RemoveStep(saved.steps, target)
               /\ FilterStepAt(candidateRequest, target).operation.filter.values[1] = requestValue
               /\ FilterStepAt(candidateResponse, target).operation.filter.values[1] = responseValue
               /\ FilterStepAt(candidateResponse, target).operation.filter.columnId = journey.temporalColumnId
               /\ FilterStepAt(candidateResponse, target).operation.filter.operator = operator

FilterProposed(event, kind, expectedRequestValue, expectedResponseValue,
               expectedSavedValue, expectedOperator, expectedRows) ==
  /\ EventFits(event, "FilterProposed(" \o kind \o ")",
       journey.mountEpoch, TRUE, FALSE)
  /\ event.before = NoSnapshot
  /\ event.after = NoSnapshot
  /\ journey.persisted # NoSaved
  /\ journey.mounted = journey.persisted
  /\ journey.proposal = NoProposal
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
       Case.initialResponseValue, Case.initialSavedValue,
       Case.initialOperator, Case.initialRowIds)

FilterProposedEdit(event) ==
  /\ journey.phase = "editing"
  /\ FilterProposed(event, "edit", Case.editedRequestValue,
       Case.editedResponseValue, Case.editedSavedValue,
       Case.editedOperator, Case.editedRowIds)

FilterProposedRemove(event) ==
  /\ journey.phase = "reloaded"
  /\ FilterCount(journey.persisted.steps) = 1
  /\ FilterProposed(event, "remove", No, No,
       SavedFilterValue(journey.persisted), SavedFilterOperator(journey.persisted),
       Case.sourceRowIds)

FilterCancelled(event) ==
  /\ journey.phase = "proposal-ready"
  /\ journey.proposal # NoProposal
  /\ journey.proposal.intent = "create"
  /\ EventFits(event, "FilterCancelled", journey.mountEpoch, FALSE, FALSE)
  /\ event.filter.proposalId = journey.proposal.proposalId
  /\ event.filter.receiptId = journey.proposal.receiptId
  /\ SnapshotMatches(journey.persisted, event.after)
  /\ event.ui.mountEpoch = journey.mountEpoch
  /\ event.ui.proposalStatus = "none"
  /\ event.ui.proposalId = No
  /\ event.ui.receiptId = No
  /\ event.ui.outputId = journey.outputId
  /\ event.ui.columnId = journey.temporalColumnId
  /\ event.ui.rowIds = ExpectedRows(journey.persisted)
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
  /\ SnapshotMatches(journey.persisted, event.after)
  /\ event.ui.mountEpoch = journey.mountEpoch + 1
  /\ event.ui.proposalStatus = "none"
  /\ event.ui.proposalId = No
  /\ event.ui.receiptId = No
  /\ event.ui.outputId = journey.outputId
  /\ event.ui.columnId = journey.temporalColumnId
  /\ event.ui.rowIds = ExpectedRows(journey.persisted)
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
  /\ event.ui.mountEpoch = journey.mountEpoch
  /\ event.ui.proposalStatus = "none"
  /\ event.ui.outputId = journey.outputId
  /\ event.ui.columnId = journey.temporalColumnId
  /\ event.ui.inputType = Case.inputType
  /\ event.ui.inputValue # No
  /\ event.ui.operator = SavedFilterOperator(journey.persisted)
  /\ event.ui.rowIds = ExpectedRows(journey.persisted)
  /\ event.filter.stepId = SavedFilterStepId(journey.persisted)
  /\ event.filter.savedValue = SavedFilterValue(journey.persisted)
  /\ Commit(event, [journey EXCEPT
       !.phase = "editing",
       !.editor = [intent |-> "edit",
                   stepId |-> SavedFilterStepId(journey.persisted),
                   inputType |-> event.ui.inputType,
                   inputValue |-> event.ui.inputValue,
                   operator |-> event.ui.operator,
                   value |-> event.filter.savedValue]])

ProposalStateMatches(event, kind, expectedRequestValue,
                     expectedResponseValue, expectedSavedValue,
                     expectedOperator, expectedRows) ==
  LET proposal == journey.proposal
      filter == event.filter
      before == event.before
      after == event.after
      command == event.request.body
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
     /\ proposal.savedValue = expectedSavedValue
     /\ proposal.rowIds =
          IF kind = "remove" THEN Case.sourceRowIds ELSE expectedRows
     /\ proposal.proposalId = filter.proposalId
     /\ proposal.receiptId = filter.receiptId
     /\ proposal.stepId = filter.stepId
     /\ proposal.candidateSteps = after.steps
     /\ event.request.isMutation = TRUE
     /\ event.request.method = "POST"
     /\ EndsWith(event.request.path, "/authoring/v2/commands")
     /\ event.request.status = 200
     /\ command.expectedDraftVersion = before.draftVersion
     /\ command.expectedDraftDigest = before.draftDigest
     /\ command.commands[1].type = "APPLY_CONSTRUCTION_PROPOSAL"
     /\ command.commands[1].outputId = journey.outputId
     /\ command.commands[1].proposalId = proposal.proposalId
     /\ SnapshotMatches(journey.persisted, before)
     /\ after.draftVersion > before.draftVersion
     /\ after.draftDigest # before.draftDigest
     /\ after.workspaceDigest # before.workspaceDigest
     /\ after.outputId = journey.outputId
     /\ after.columnId = journey.temporalColumnId
     /\ after.columns = before.columns
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
     /\ event.ui.proposalStatus = "none"
     /\ event.ui.proposalId = No
     /\ event.ui.receiptId = No
     /\ event.ui.outputId = journey.outputId
     /\ event.ui.columnId = journey.temporalColumnId
     /\ event.ui.rowIds = ExpectedRows(SavedFromSnapshot(after))

ApplyFilter(event, kind, action, expectedRequestValue,
            expectedResponseValue, expectedSavedValue, expectedOperator,
            expectedRows) ==
  /\ EventFits(event, action, journey.mountEpoch, TRUE, TRUE)
  /\ ProposalStateMatches(event, kind, expectedRequestValue,
       expectedResponseValue, expectedSavedValue, expectedOperator, expectedRows)
  /\ IF kind = "create"
       THEN /\ FilterCount(event.before.steps) = 0
            /\ FilterCount(event.after.steps) = 1
            /\ ~HasStep(event.before.steps, journey.proposal.stepId)
            /\ UniqueStep(event.after.steps, journey.proposal.stepId)
            /\ RemoveStep(event.after.steps, journey.proposal.stepId) = event.before.steps
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.values[1]
                 = expectedSavedValue
       ELSE IF kind = "edit"
       THEN /\ FilterCount(event.before.steps) = 1
            /\ FilterCount(event.after.steps) = 1
            /\ UniqueStep(event.before.steps, journey.proposal.stepId)
            /\ UniqueStep(event.after.steps, journey.proposal.stepId)
            /\ RemoveStep(event.before.steps, journey.proposal.stepId)
                 = RemoveStep(event.after.steps, journey.proposal.stepId)
            /\ FilterStepAt(event.after.steps, journey.proposal.stepId).operation.filter.values[1]
                 = expectedSavedValue
       ELSE /\ kind = "remove"
            /\ FilterCount(event.before.steps) = 1
            /\ FilterCount(event.after.steps) = 0
            /\ UniqueStep(event.before.steps, journey.proposal.stepId)
            /\ ~HasStep(event.after.steps, journey.proposal.stepId)
            /\ RemoveStep(event.before.steps, journey.proposal.stepId) = event.after.steps
  /\ Commit(event, [journey EXCEPT
       !.phase = IF kind = "create" THEN "created"
                 ELSE IF kind = "edit" THEN "edited" ELSE "removed",
       !.persisted = SavedFromSnapshot(event.after),
       !.mounted = SavedFromSnapshot(event.after),
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
  ELSE /\ saved.outputId # No
       /\ saved.columnId # No
       /\ saved.draftVersion \in Nat
       /\ saved.catalogGeneration # No
       /\ Len(saved.workspaceDocuments) > 0

TypeOK ==
  /\ journey.cursor \in 0..Len(ModelEvents)
  /\ journey.mountEpoch \in 0..4
  /\ Len(journey.history) = journey.cursor
  /\ SavedStateOK(journey.persisted)
  /\ SavedStateOK(journey.mounted)
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
  journey.cursor < 3 \/ journey.mounted = journey.persisted
InvStableColumnIdentity ==
  journey.cursor < 3
  \/ journey.temporalColumnId = journey.persisted.columnId
InvRenderMatchesSavedFilter ==
  journey.cursor < 3 \/ journey.renderedRowIds = ExpectedRows(journey.persisted)
InvProposalPreviewRowsMatch ==
  journey.proposal = NoProposal
  \/ journey.proposal.rowIds = journey.previewRowIds
InvEveryProposalEchoesOwnerAndPredicate ==
  \A i \in 1..journey.cursor:
    LET e == journey.history[i]
    IN IF e.action \in {"FilterProposed(create)", "FilterProposed(edit)",
                         "FilterProposed(remove)"}
       THEN /\ e.request.captureId # No
            /\ e.request.responseBody.proposalId = e.filter.proposalId
            /\ e.request.responseBody.preview.receiptId = e.filter.receiptId
            /\ e.request.responseBody.preview.outputId = e.filter.outputId
            /\ e.request.responseBody.preview.columnId = e.filter.columnId
            /\ e.request.responseBody.preview.operator = e.filter.operator
            /\ e.ui.proposalId = e.filter.proposalId
            /\ e.ui.receiptId = e.filter.receiptId
            /\ e.ui.outputId = e.filter.outputId
            /\ e.ui.columnId = e.filter.columnId
            /\ e.ui.operator = e.filter.operator
            /\ e.ui.rowIds = e.request.responseBody.preview.rowIds
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
            /\ e.before.steps[CHOOSE j \in StepIndices(e.before.steps, target): TRUE].stepId
                 = e.after.steps[CHOOSE j \in StepIndices(e.after.steps, target): TRUE].stepId
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
            /\ e.ui.mountEpoch = e.mountEpoch
            /\ e.ui.rowIds = ExpectedRows(SavedFromSnapshot(e.after))
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
