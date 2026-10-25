---------------- MODULE PolicyOneDisagreement ----------------
EXTENDS Naturals, Sequences, TLC

CONSTANTS
  OutputId,
  EditorId,
  DocumentTimeOrigin,
  ChoiceId,
  SourceColumnIds,
  SourceBindings,
  AddedPatientIdColumnId,
  AddedPatientIdBinding,
  AllReceiptId,
  RootDraftVersion,
  GroupDraftVersion,
  AllDraftVersion,
  RestoredDraftVersion,
  RootDraftDigest,
  GroupDraftDigest,
  AllDraftDigest,
  RestoredDraftDigest,
  OneRequestId,
  OneCaptureId,
  OneCommandId,
  AllRequestId,
  AllCaptureId,
  AllCommandId,
  ApplyRequestId,
  ApplyCaptureId,
  GroupProposalId,
  GroupApplyCommandId,
  RemovalOneRequestId,
  RemovalOneCaptureId,
  RemovalOneCandidateId,
  RemovalTwoRequestId,
  RemovalTwoCaptureId,
  RemovalTwoCandidateId,
  RemovalApplyCommandId,
  RemovalApplyRequestId,
  RemovalApplyCaptureId,
  GroupPreviewRequestId,
  GroupPreviewCaptureId,
  GroupApplyRequestId,
  GroupApplyCaptureId

PatientIdField == "Patient.id"
NoIdentity == "Absent"
NoCommand == "Absent"
NoDigest == "Absent"
NoCode == "NoCode"
NoPolicy == "NONE"
NoColumn == "NoColumn"
NoAggregate == "NO_AGGREGATE"
NoStatus == 0

SourcePatientIds == {"dev-patient-001", "dev-patient-002"}
SourcePatientRows == [patientIds |-> SourcePatientIds, count |-> 2]
GroupCountRows == <<[count |-> 2]>>
AllRows == <<[patientIds |-> SourcePatientIds, count |-> 2]>>

DraftVersions == {RootDraftVersion, GroupDraftVersion,
                  AllDraftVersion, RestoredDraftVersion}
StringIdentityValues ==
  {OutputId, EditorId, ChoiceId, AddedPatientIdColumnId,
   AddedPatientIdBinding, AllReceiptId,
   RootDraftDigest, GroupDraftDigest, AllDraftDigest, RestoredDraftDigest,
   OneRequestId, OneCaptureId, OneCommandId,
   AllRequestId, AllCaptureId, AllCommandId,
   ApplyRequestId, ApplyCaptureId,
   GroupProposalId, GroupApplyCommandId,
   RemovalOneRequestId, RemovalOneCaptureId, RemovalOneCandidateId,
   RemovalTwoRequestId, RemovalTwoCaptureId, RemovalTwoCandidateId,
   RemovalApplyCommandId, RemovalApplyRequestId, RemovalApplyCaptureId,
   GroupPreviewRequestId, GroupPreviewCaptureId,
   GroupApplyRequestId, GroupApplyCaptureId}

NoGroup ==
  [present |-> FALSE,
   keys |-> <<>>,
   aggregate |-> NoAggregate,
   count |-> 0,
   rowValuePolicy |-> NoPolicy,
   inputColumnId |-> NoColumn]

EmptyKeyCountGroup ==
  [present |-> TRUE,
   keys |-> <<>>,
   aggregate |-> "COUNT_ROWS",
   count |-> 2,
   rowValuePolicy |-> NoPolicy,
   inputColumnId |-> NoColumn]

EmptyKeyCountAllGroup ==
  [present |-> TRUE,
   keys |-> <<>>,
   aggregate |-> "COUNT_ROWS",
   count |-> 2,
   rowValuePolicy |-> "ALL",
   inputColumnId |-> AddedPatientIdColumnId]

MakeWorkspace(group, additionalBindings, renderedRows) ==
  [documentId |-> OutputId,
   rootResourceType |-> "Patient",
   sourceColumnIds |-> SourceColumnIds,
   sourceBindings |-> SourceBindings,
   sourceRows |-> SourcePatientRows,
   additionalBindings |-> additionalBindings,
   group |-> group,
   renderedRows |-> renderedRows]

NoAdditionalBindings == {}
AllPatientIdBinding == {AddedPatientIdBinding}

SourceWorkspace == MakeWorkspace(NoGroup, NoAdditionalBindings, SourcePatientRows)
GroupWorkspace == MakeWorkspace(EmptyKeyCountGroup,
                                NoAdditionalBindings, GroupCountRows)
AllWorkspace == MakeWorkspace(EmptyKeyCountAllGroup,
                              AllPatientIdBinding, AllRows)
RemovedWorkspaceWithAllColumn ==
  MakeWorkspace(NoGroup, AllPatientIdBinding, SourcePatientRows)
RemovedWorkspaceWithoutAllColumn ==
  MakeWorkspace(NoGroup, NoAdditionalBindings, SourcePatientRows)
Workspaces == {SourceWorkspace, GroupWorkspace, AllWorkspace,
               RemovedWorkspaceWithAllColumn, RemovedWorkspaceWithoutAllColumn}

NoGroupPreview ==
  [rowCount |-> 0, keys |-> <<>>, aggregate |-> NoAggregate, count |-> 0]
GroupPreview ==
  [rowCount |-> 1, keys |-> <<>>, aggregate |-> "COUNT_ROWS", count |-> 2]

NoPreview ==
  [outputId |-> NoIdentity, receiptId |-> NoIdentity,
   rowCount |-> 0, patientIds |-> {}, count |-> 0]
AllResultPreview ==
  [outputId |-> OutputId, receiptId |-> AllReceiptId,
   rowCount |-> 1, patientIds |-> SourcePatientIds, count |-> 2]

NoEditor ==
  [documentId |-> NoIdentity, editorId |-> NoIdentity,
   documentTimeOrigin |-> NoIdentity,
   mounted |-> FALSE, allAvailable |-> FALSE]
OpenEditor ==
  [documentId |-> OutputId, editorId |-> EditorId,
   documentTimeOrigin |-> DocumentTimeOrigin,
   mounted |-> TRUE, allAvailable |-> TRUE]
Editors == {NoEditor, OpenEditor}

NoChoice ==
  [choiceId |-> NoIdentity, field |-> NoIdentity,
   rowValuePolicy |-> NoPolicy, status |-> "idle"]
Choice(policy, status) ==
  [choiceId |-> ChoiceId, field |-> PatientIdField,
   rowValuePolicy |-> policy, status |-> status]
Choices == {NoChoice,
            Choice("ONE", "pending"), Choice("ONE", "error"),
            Choice("ALL", "error"), Choice("ALL", "pending"),
            Choice("ALL", "ready"), Choice("ALL", "applied")}

Request(requestId, captureId, commandId, version, digest, workspace,
        choiceId, field, policy, candidateId, candidateRequestId,
        candidateCaptureId) ==
  [requestId |-> requestId,
   captureId |-> captureId,
   requestObjectId |-> captureId,
   commandId |-> commandId,
   outputId |-> OutputId,
   candidateId |-> candidateId,
   candidateRequestId |-> candidateRequestId,
   candidateCaptureId |-> candidateCaptureId,
   expectedDraftVersion |-> version,
   expectedDraftDigest |-> digest,
   expectedWorkspace |-> workspace,
   choiceId |-> choiceId,
   field |-> field,
   rowValuePolicy |-> policy]

NoRequest == Request(NoIdentity, NoIdentity, NoCommand, RootDraftVersion,
                     NoDigest, SourceWorkspace, NoIdentity, NoIdentity,
                     NoPolicy, NoIdentity, NoIdentity, NoIdentity)
ProposalRequest(requestId, captureId, commandId, policy, version, digest) ==
  Request(requestId, captureId, commandId, version, digest, GroupWorkspace,
          ChoiceId, PatientIdField, policy,
          NoIdentity, NoIdentity, NoIdentity)

OneRequest == ProposalRequest(OneRequestId, OneCaptureId, OneCommandId,
                              "ONE", GroupDraftVersion, GroupDraftDigest)
AllRequest == ProposalRequest(AllRequestId, AllCaptureId, AllCommandId,
                              "ALL", GroupDraftVersion, GroupDraftDigest)
ApplyRequest == ProposalRequest(ApplyRequestId, ApplyCaptureId, AllCommandId,
                                "ALL", GroupDraftVersion, GroupDraftDigest)
GroupRequest(requestId, captureId, commandId, version, digest, workspace) ==
  Request(requestId, captureId, commandId, version, digest, workspace,
          NoIdentity, NoIdentity, NoPolicy,
          NoIdentity, NoIdentity, NoIdentity)
GroupPreviewRequest ==
  GroupRequest(GroupPreviewRequestId, GroupPreviewCaptureId,
               NoCommand, RootDraftVersion, RootDraftDigest,
               SourceWorkspace)
GroupApplyRequest ==
  Request(GroupApplyRequestId, GroupApplyCaptureId, GroupApplyCommandId,
          RootDraftVersion, RootDraftDigest, SourceWorkspace,
          NoIdentity, NoIdentity, NoPolicy,
          GroupProposalId, GroupPreviewRequestId, GroupPreviewCaptureId)
RemovalRequest(requestId, captureId) ==
  GroupRequest(requestId, captureId, NoCommand,
               AllDraftVersion, AllDraftDigest, AllWorkspace)
RemovalOneRequest == RemovalRequest(RemovalOneRequestId,
                                    RemovalOneCaptureId)
RemovalTwoRequest == RemovalRequest(RemovalTwoRequestId,
                                    RemovalTwoCaptureId)
RemovalApplyRequest ==
  Request(RemovalApplyRequestId, RemovalApplyCaptureId, RemovalApplyCommandId,
          AllDraftVersion, AllDraftDigest, AllWorkspace,
          NoIdentity, NoIdentity, NoPolicy,
          RemovalTwoCandidateId, RemovalTwoRequestId, RemovalTwoCaptureId)

NoGroupResponse ==
  [captureId |-> NoIdentity,
   requestObjectId |-> NoIdentity,
   commandId |-> NoCommand,
   proposalId |-> NoIdentity,
   status |-> NoStatus]
GroupPreviewResponse ==
  [captureId |-> GroupPreviewCaptureId,
   requestObjectId |-> GroupPreviewCaptureId,
   commandId |-> NoCommand,
   proposalId |-> GroupProposalId,
   status |-> 200]
GroupAppliedResponse ==
  [captureId |-> GroupApplyCaptureId,
   requestObjectId |-> GroupApplyCaptureId,
   commandId |-> GroupApplyCommandId,
   proposalId |-> NoIdentity,
   status |-> 200]
RemovalOneResponse ==
  [captureId |-> RemovalOneCaptureId,
   requestObjectId |-> RemovalOneCaptureId,
   commandId |-> NoCommand,
   proposalId |-> RemovalOneCandidateId,
   status |-> 200]
RemovalTwoResponse ==
  [captureId |-> RemovalTwoCaptureId,
   requestObjectId |-> RemovalTwoCaptureId,
   commandId |-> NoCommand,
   proposalId |-> RemovalTwoCandidateId,
   status |-> 200]

NoOneResponse ==
  [captureId |-> NoIdentity,
   requestObjectId |-> NoIdentity,
   commandId |-> NoCommand,
   status |-> NoStatus,
   code |-> NoCode,
   savedWorkspace |-> SourceWorkspace,
   draftVersion |-> RootDraftVersion,
   draftDigest |-> RootDraftDigest]
OneRefusalResponse ==
  [captureId |-> OneCaptureId,
   requestObjectId |-> OneCaptureId,
   commandId |-> NoCommand,
   status |-> 422,
   code |-> "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES",
   savedWorkspace |-> GroupWorkspace,
   draftVersion |-> GroupDraftVersion,
   draftDigest |-> GroupDraftDigest]

NoAllResponse ==
  [captureId |-> NoIdentity,
   requestObjectId |-> NoIdentity,
   commandId |-> NoCommand,
   status |-> NoStatus,
   code |-> NoCode]
AllReadyResponse ==
  [captureId |-> AllCaptureId,
   requestObjectId |-> AllCaptureId,
   commandId |-> AllCommandId,
   status |-> 200,
   code |-> NoCode]
AllAppliedResponse ==
  [captureId |-> ApplyCaptureId,
   requestObjectId |-> ApplyCaptureId,
   commandId |-> AllCommandId,
   status |-> 200,
   code |-> NoCode]
RemovalAppliedResponse ==
  [captureId |-> RemovalApplyCaptureId,
   requestObjectId |-> RemovalApplyCaptureId,
   commandId |-> RemovalApplyCommandId,
   status |-> 200,
   code |-> NoCode]

NoSnapshot ==
  [workspace |-> SourceWorkspace,
   draftVersion |-> RootDraftVersion,
   draftDigest |-> RootDraftDigest]
Snapshot(workspace, version, digest) ==
  [workspace |-> workspace, draftVersion |-> version, draftDigest |-> digest]

NoRemovalCandidate ==
  [candidateId |-> NoIdentity,
   requestId |-> NoIdentity,
   captureId |-> NoIdentity,
   requestObjectId |-> NoIdentity,
   commandId |-> NoCommand,
   savedWorkspace |-> SourceWorkspace,
   draftVersion |-> RootDraftVersion,
   draftDigest |-> RootDraftDigest]
RemovalCandidate(candidateId, request, workspace, version, digest) ==
  [candidateId |-> candidateId,
   requestId |-> request.requestId,
   captureId |-> request.captureId,
   requestObjectId |-> request.requestObjectId,
   commandId |-> request.commandId,
   savedWorkspace |-> workspace,
   draftVersion |-> version,
   draftDigest |-> digest]
RemovalOneCandidate ==
  RemovalCandidate(RemovalOneCandidateId, RemovalOneRequest,
                   AllWorkspace, AllDraftVersion, AllDraftDigest)
RemovalTwoCandidate ==
  RemovalCandidate(RemovalTwoCandidateId, RemovalTwoRequest,
                   AllWorkspace, AllDraftVersion, AllDraftDigest)

InitialModel ==
  [workspace |-> SourceWorkspace,
   draftVersion |-> RootDraftVersion,
   draftDigest |-> RootDraftDigest,
   groupPreview |-> NoGroupPreview,
   groupPreviewRequest |-> NoRequest,
   groupPreviewResponse |-> NoGroupResponse,
   groupApplyRequest |-> NoRequest,
   groupApplyResponse |-> NoGroupResponse,
   editor |-> NoEditor,
   choice |-> NoChoice,
   oneRequest |-> NoRequest,
   oneResponse |-> NoOneResponse,
   allRequest |-> NoRequest,
   allResponse |-> NoAllResponse,
   applyRequest |-> NoRequest,
   applyResponse |-> NoAllResponse,
   allPreview |-> NoPreview,
   removalSnapshot |-> NoSnapshot,
   removalCancelSnapshot |-> NoSnapshot,
   removalRequest |-> NoRequest,
   removalResponse |-> NoGroupResponse,
   removalCandidate |-> NoRemovalCandidate,
   cancelledRemovalCandidate |-> NoRemovalCandidate,
   removalApplyRequest |-> NoRequest,
   removalApplyResponse |-> NoAllResponse,
   reloadedAllSnapshot |-> NoSnapshot,
   reloadedSourceSnapshot |-> NoSnapshot]

VARIABLES phase, model, lastAction
vars == <<phase, model, lastAction>>

Init ==
  /\ phase = "source-ready"
  /\ model = InitialModel
  /\ lastAction = "Init"

GroupPreviewSubmitted ==
  /\ phase = "source-ready"
  /\ model.groupPreview = NoGroupPreview
  /\ LET nextModel == [model EXCEPT
         !.groupPreviewRequest = GroupPreviewRequest]
     IN /\ phase' = "group-preview-submitted"
        /\ model' = nextModel
        /\ lastAction' = "GroupPreviewSubmitted"

GroupPreviewReady ==
  /\ phase = "group-preview-submitted"
  /\ model.groupPreviewRequest = GroupPreviewRequest
  /\ LET nextModel == [model EXCEPT
         !.groupPreview = GroupPreview,
         !.groupPreviewResponse = GroupPreviewResponse]
     IN /\ phase' = "group-preview-ready"
        /\ model' = nextModel
        /\ lastAction' = "GroupPreviewReady"

GroupApplySubmitted ==
  /\ phase = "group-preview-ready"
  /\ model.groupPreviewResponse.commandId = NoCommand
  /\ model.groupPreviewResponse.proposalId = GroupProposalId
  /\ LET nextModel == [model EXCEPT !.groupApplyRequest = GroupApplyRequest]
     IN /\ phase' = "group-apply-submitted"
        /\ model' = nextModel
        /\ lastAction' = "GroupApplySubmitted"

GroupApplied ==
  /\ phase = "group-apply-submitted"
  /\ model.groupPreview = GroupPreview
  /\ model.groupPreview.rowCount = 1
  /\ model.groupPreview.keys = <<>>
  /\ model.groupPreview.aggregate = "COUNT_ROWS"
  /\ model.groupPreview.count = 2
  /\ model.groupApplyRequest.commandId = GroupApplyCommandId
  /\ model.groupApplyRequest.candidateId =
       model.groupPreviewResponse.proposalId
  /\ LET nextModel == [model EXCEPT
         !.workspace = GroupWorkspace,
         !.draftVersion = GroupDraftVersion,
         !.draftDigest = GroupDraftDigest,
         !.groupApplyResponse = GroupAppliedResponse]
     IN /\ phase' = "group-applied"
        /\ model' = nextModel
        /\ lastAction' = "GroupApplied"

OneSubmitted ==
  /\ phase = "group-applied"
  /\ model.workspace.group = EmptyKeyCountGroup
  /\ model.draftVersion = GroupDraftVersion
  /\ model.draftDigest = GroupDraftDigest
  /\ LET nextModel == [model EXCEPT
         !.editor = OpenEditor,
         !.choice = Choice("ONE", "pending"),
         !.oneRequest = OneRequest]
     IN /\ phase' = "one-submitted"
        /\ model' = nextModel
        /\ lastAction' = "OneSubmitted"

OneRejected ==
  /\ phase = "one-submitted"
  /\ model.oneRequest = OneRequest
  /\ LET nextModel == [model EXCEPT
         !.choice = Choice("ONE", "error"),
         !.oneResponse = OneRefusalResponse]
     IN /\ phase' = "one-rejected"
        /\ model' = nextModel
        /\ lastAction' = "OneRejected"

SelectAll ==
  /\ phase = "one-rejected"
  /\ model.editor = OpenEditor
  /\ model.choice = Choice("ONE", "error")
  /\ LET nextModel == [model EXCEPT !.choice = Choice("ALL", "error")]
     IN /\ phase' = "all-selected"
        /\ model' = nextModel
        /\ lastAction' = "SelectAll"

AllSubmitted ==
  /\ phase = "all-selected"
  /\ model.editor = OpenEditor
  /\ model.choice = Choice("ALL", "error")
  /\ LET nextModel == [model EXCEPT
         !.choice = Choice("ALL", "pending"),
         !.allRequest = AllRequest]
     IN /\ phase' = "all-submitted"
        /\ model' = nextModel
        /\ lastAction' = "AllSubmitted"

AllReady ==
  /\ phase = "all-submitted"
  /\ model.allRequest = AllRequest
  /\ LET nextModel == [model EXCEPT
         !.choice = Choice("ALL", "ready"),
         !.allResponse = AllReadyResponse,
         !.allPreview = AllResultPreview]
     IN /\ phase' = "all-ready"
        /\ model' = nextModel
        /\ lastAction' = "AllReady"

AllApplySubmitted ==
  /\ phase = "all-ready"
  /\ model.allResponse.commandId = AllCommandId
  /\ model.choice = Choice("ALL", "ready")
  /\ LET nextModel == [model EXCEPT !.applyRequest = ApplyRequest]
     IN /\ phase' = "all-apply-submitted"
        /\ model' = nextModel
        /\ lastAction' = "AllApplySubmitted"

AllApplied ==
  /\ phase = "all-apply-submitted"
  /\ model.applyRequest.commandId = model.allResponse.commandId
  /\ model.applyRequest.choiceId = model.allRequest.choiceId
  /\ LET nextModel == [model EXCEPT
         !.workspace = AllWorkspace,
         !.draftVersion = AllDraftVersion,
         !.draftDigest = AllDraftDigest,
         !.editor = NoEditor,
         !.choice = Choice("ALL", "applied"),
         !.applyResponse = AllAppliedResponse]
     IN /\ phase' = "all-applied"
        /\ model' = nextModel
        /\ lastAction' = "AllApplied"

ReloadedAll ==
  /\ phase = "all-applied"
  /\ LET nextModel == [model EXCEPT
         !.reloadedAllSnapshot =
           Snapshot(model.workspace, model.draftVersion, model.draftDigest)]
     IN /\ phase' = "reloaded-all"
        /\ model' = nextModel
        /\ lastAction' = "ReloadedAll"

RemovalPreviewSubmitted ==
  /\ phase = "reloaded-all"
  /\ model.workspace.group = EmptyKeyCountAllGroup
  /\ LET nextModel == [model EXCEPT
         !.removalSnapshot =
           Snapshot(model.workspace, model.draftVersion, model.draftDigest),
         !.removalRequest = RemovalOneRequest,
         !.removalCandidate = NoRemovalCandidate,
         !.removalResponse = NoGroupResponse]
     IN /\ phase' = "removal-preview-submitted"
        /\ model' = nextModel
        /\ lastAction' = "RemovalPreviewSubmitted"

RemovalPreviewReadyFirst ==
  /\ phase = "removal-preview-submitted"
  /\ model.removalRequest = RemovalOneRequest
  /\ model.removalResponse.proposalId = RemovalOneCandidateId
  /\ LET nextModel == [model EXCEPT
         !.removalCandidate = RemovalOneCandidate,
         !.removalResponse = RemovalOneResponse]
     IN /\ phase' = "removal-preview-ready"
        /\ model' = nextModel
        /\ lastAction' = "RemovalPreviewReady"

RemovalCancelled ==
  /\ phase = "removal-preview-ready"
  /\ model.removalSnapshot =
       Snapshot(model.workspace, model.draftVersion, model.draftDigest)
  /\ LET nextModel == [model EXCEPT
         !.removalCancelSnapshot = model.removalSnapshot,
         !.cancelledRemovalCandidate = model.removalCandidate]
     IN /\ phase' = "removal-cancelled"
        /\ model' = nextModel
        /\ lastAction' = "RemovalCancelled"

RemovalPreviewSubmittedAgain ==
  /\ phase = "removal-cancelled"
  /\ model.removalCancelSnapshot = model.removalSnapshot
  /\ model.removalSnapshot =
       Snapshot(model.workspace, model.draftVersion, model.draftDigest)
  /\ LET nextModel == [model EXCEPT
         !.removalSnapshot =
           Snapshot(model.workspace, model.draftVersion, model.draftDigest),
         !.removalRequest = RemovalTwoRequest,
         !.removalCandidate = NoRemovalCandidate,
         !.removalResponse = NoGroupResponse]
     IN /\ phase' = "removal-preview-submitted-again"
        /\ model' = nextModel
        /\ lastAction' = "RemovalPreviewSubmitted"

RemovalPreviewReadyAgain ==
  /\ phase = "removal-preview-submitted-again"
  /\ model.removalRequest = RemovalTwoRequest
  /\ model.cancelledRemovalCandidate = RemovalOneCandidate
  /\ model.removalResponse.proposalId = RemovalTwoCandidateId
  /\ LET nextModel == [model EXCEPT
         !.removalCandidate = RemovalTwoCandidate,
         !.removalResponse = RemovalTwoResponse]
     IN /\ phase' = "removal-preview-reopened"
        /\ model' = nextModel
        /\ lastAction' = "RemovalPreviewReady"

RemovalApplySubmitted ==
  /\ phase = "removal-preview-reopened"
  /\ model.removalSnapshot =
       Snapshot(model.workspace, model.draftVersion, model.draftDigest)
  /\ model.removalCandidate = RemovalTwoCandidate
  /\ model.cancelledRemovalCandidate = RemovalOneCandidate
  /\ LET nextModel == [model EXCEPT
         !.removalApplyRequest = RemovalApplyRequest]
     IN /\ phase' = "removal-apply-submitted"
        /\ model' = nextModel
        /\ lastAction' = "RemovalApplySubmitted"

RemovalApplied(keepAddedColumn) ==
  /\ phase = "removal-apply-submitted"
  /\ keepAddedColumn \in BOOLEAN
  /\ model.removalApplyRequest = RemovalApplyRequest
  /\ model.removalApplyRequest.candidateId = model.removalCandidate.candidateId
  /\ model.removalApplyRequest.candidateRequestId =
       model.removalCandidate.requestId
  /\ model.removalApplyRequest.candidateCaptureId =
       model.removalCandidate.captureId
  /\ model.removalApplyRequest.commandId = RemovalApplyCommandId
  /\ LET restoredWorkspace ==
         IF keepAddedColumn
         THEN RemovedWorkspaceWithAllColumn
         ELSE RemovedWorkspaceWithoutAllColumn
     IN LET nextModel == [model EXCEPT
            !.workspace = restoredWorkspace,
            !.draftVersion = RestoredDraftVersion,
            !.draftDigest = RestoredDraftDigest,
            !.removalApplyResponse = RemovalAppliedResponse]
        IN /\ phase' = "removal-applied"
           /\ model' = nextModel
           /\ lastAction' = "RemovalApplied"

ReloadedSource ==
  /\ phase = "removal-applied"
  /\ LET nextModel == [model EXCEPT
         !.reloadedSourceSnapshot =
           Snapshot(model.workspace, model.draftVersion, model.draftDigest)]
     IN /\ phase' = "reloaded-source"
        /\ model' = nextModel
        /\ lastAction' = "ReloadedSource"

Next ==
  \/ GroupPreviewSubmitted
  \/ GroupPreviewReady
  \/ GroupApplySubmitted
  \/ GroupApplied
  \/ OneSubmitted
  \/ OneRejected
  \/ SelectAll
  \/ AllSubmitted
  \/ AllReady
  \/ AllApplySubmitted
  \/ AllApplied
  \/ ReloadedAll
  \/ RemovalPreviewSubmitted
  \/ RemovalPreviewReadyFirst
  \/ RemovalCancelled
  \/ RemovalPreviewSubmittedAgain
  \/ RemovalPreviewReadyAgain
  \/ RemovalApplySubmitted
  \/ \E keepAddedColumn \in BOOLEAN:
       RemovalApplied(keepAddedColumn)
  \/ ReloadedSource

GroupRecordOK(groupValue) ==
  IF groupValue.present
  THEN /\ groupValue.keys = <<>>
       /\ groupValue.aggregate = "COUNT_ROWS"
       /\ groupValue.count = 2
       /\ groupValue.rowValuePolicy \in {NoPolicy, "ALL"}
       /\ (groupValue.rowValuePolicy = "ALL" =>
             groupValue.inputColumnId = AddedPatientIdColumnId)
  ELSE /\ groupValue = NoGroup

WorkspaceOK(workspaceValue) ==
  /\ workspaceValue \in Workspaces
  /\ workspaceValue.documentId = OutputId
  /\ workspaceValue.rootResourceType = "Patient"
  /\ workspaceValue.sourceColumnIds = SourceColumnIds
  /\ workspaceValue.sourceBindings = SourceBindings
  /\ workspaceValue.sourceRows = SourcePatientRows
  /\ workspaceValue.sourceBindings \subseteq STRING
  /\ workspaceValue.additionalBindings \in
       {NoAdditionalBindings, AllPatientIdBinding}
  /\ GroupRecordOK(workspaceValue.group)

TypeOK ==
  /\ phase \in {"source-ready", "group-preview-submitted",
       "group-preview-ready", "group-apply-submitted", "group-applied",
       "one-submitted", "one-rejected", "all-selected", "all-submitted",
       "all-ready", "all-apply-submitted", "all-applied", "reloaded-all",
       "removal-preview-submitted", "removal-preview-ready",
       "removal-cancelled", "removal-preview-submitted-again",
       "removal-preview-reopened", "removal-apply-submitted",
       "removal-applied", "reloaded-source"}
  /\ WorkspaceOK(model.workspace)
  /\ model.draftVersion \in DraftVersions
  /\ model.draftDigest \in {RootDraftDigest, GroupDraftDigest,
                            AllDraftDigest, RestoredDraftDigest}
  /\ model.groupPreview \in {NoGroupPreview, GroupPreview}
  /\ model.groupPreviewRequest \in {NoRequest, GroupPreviewRequest}
  /\ model.groupPreviewResponse \in {NoGroupResponse, GroupPreviewResponse}
  /\ model.groupApplyRequest \in {NoRequest, GroupApplyRequest}
  /\ model.groupApplyResponse \in {NoGroupResponse, GroupAppliedResponse}
  /\ model.editor \in Editors
  /\ model.choice \in Choices
  /\ model.oneRequest \in {NoRequest, OneRequest}
  /\ model.oneResponse \in {NoOneResponse, OneRefusalResponse}
  /\ model.allRequest \in {NoRequest, AllRequest}
  /\ model.allResponse \in {NoAllResponse, AllReadyResponse}
  /\ model.applyRequest \in {NoRequest, ApplyRequest}
  /\ model.applyResponse \in {NoAllResponse, AllAppliedResponse}
  /\ model.removalRequest \in
       {NoRequest, RemovalOneRequest, RemovalTwoRequest}
  /\ model.removalResponse \in
       {NoGroupResponse, RemovalOneResponse, RemovalTwoResponse}
  /\ model.removalCandidate \in
       {NoRemovalCandidate, RemovalOneCandidate, RemovalTwoCandidate}
  /\ model.cancelledRemovalCandidate \in
       {NoRemovalCandidate, RemovalOneCandidate}
  /\ model.removalApplyRequest \in {NoRequest, RemovalApplyRequest}
  /\ model.removalApplyResponse \in
       {NoAllResponse, RemovalAppliedResponse}
  /\ model.allPreview \in {NoPreview, AllResultPreview}
  /\ model.removalSnapshot \in
       {NoSnapshot,
        Snapshot(GroupWorkspace, GroupDraftVersion, GroupDraftDigest),
        Snapshot(AllWorkspace, AllDraftVersion, AllDraftDigest)}
  /\ model.removalCancelSnapshot \in
       {NoSnapshot,
        Snapshot(AllWorkspace, AllDraftVersion, AllDraftDigest)}
  /\ model.reloadedAllSnapshot \in
       {NoSnapshot, Snapshot(AllWorkspace, AllDraftVersion, AllDraftDigest)}
  /\ model.reloadedSourceSnapshot \in
       {NoSnapshot,
        Snapshot(RemovedWorkspaceWithAllColumn, RestoredDraftVersion,
                 RestoredDraftDigest),
        Snapshot(RemovedWorkspaceWithoutAllColumn, RestoredDraftVersion,
                 RestoredDraftDigest)}
  /\ SourceColumnIds \in SUBSET STRING
  /\ SourceBindings \subseteq STRING
  /\ StringIdentityValues \subseteq STRING
  /\ DocumentTimeOrigin \in STRING
  /\ lastAction \in {"Init", "GroupPreviewSubmitted", "GroupPreviewReady",
       "GroupApplySubmitted", "GroupApplied",
       "OneSubmitted", "OneRejected", "SelectAll", "AllSubmitted",
       "AllReady", "AllApplySubmitted", "AllApplied", "ReloadedAll",
       "RemovalPreviewSubmitted", "RemovalPreviewReady", "RemovalCancelled",
       "RemovalApplySubmitted",
       "RemovalApplied", "ReloadedSource"}

GroupPreviewRetainsTwoPatientPrecondition ==
  /\ (model.groupPreview = GroupPreview =>
       /\ model.groupPreview.rowCount = 1
       /\ model.groupPreview.keys = <<>>
       /\ model.groupPreview.aggregate = "COUNT_ROWS"
       /\ model.groupPreview.count = 2)
  /\ (model.workspace.group.present =>
       /\ model.workspace.group.keys = <<>>
       /\ model.workspace.group.aggregate = "COUNT_ROWS"
       /\ model.workspace.group.count = 2)

RefusalPreservesSavedDraftAndWorkspace ==
  /\ (model.oneResponse.status = 422 =>
       /\ model.oneResponse.code = "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES"
       /\ model.oneResponse.savedWorkspace = model.oneRequest.expectedWorkspace
       /\ model.oneResponse.draftVersion = model.oneRequest.expectedDraftVersion
       /\ model.oneResponse.draftDigest = model.oneRequest.expectedDraftDigest
       /\ model.oneResponse.savedWorkspace = GroupWorkspace
       /\ model.oneResponse.draftVersion = GroupDraftVersion
       /\ model.oneResponse.draftDigest = GroupDraftDigest)
  /\ (phase \in {"one-submitted", "one-rejected", "all-selected",
                  "all-submitted", "all-ready", "all-apply-submitted"} =>
       /\ model.workspace = model.oneRequest.expectedWorkspace
       /\ model.draftVersion = model.oneRequest.expectedDraftVersion
       /\ model.draftDigest = model.oneRequest.expectedDraftDigest)

OneRequestHasActualRequestBinding ==
  model.oneRequest = OneRequest =>
    /\ model.oneRequest.requestId = OneRequestId
    /\ model.oneRequest.captureId = OneCaptureId
    /\ model.oneRequest.requestObjectId = OneCaptureId
    /\ model.oneRequest.commandId = OneCommandId
    /\ model.oneRequest.outputId = OutputId
    /\ model.oneRequest.choiceId = ChoiceId
    /\ model.oneRequest.field = PatientIdField
    /\ model.oneRequest.rowValuePolicy = "ONE"
    /\ model.oneRequest.expectedDraftVersion = GroupDraftVersion
    /\ model.oneRequest.expectedDraftDigest = GroupDraftDigest

AllRequestKeepsTheSameFieldAndDraft ==
  model.allRequest = AllRequest =>
    /\ model.allRequest.requestId = AllRequestId
    /\ model.allRequest.captureId = AllCaptureId
    /\ model.allRequest.requestObjectId = AllCaptureId
    /\ model.allRequest.choiceId = model.oneRequest.choiceId
    /\ model.allRequest.field = model.oneRequest.field
    /\ model.allRequest.rowValuePolicy = "ALL"
    /\ model.allRequest.expectedWorkspace = model.oneRequest.expectedWorkspace
    /\ model.allRequest.expectedDraftVersion = model.oneRequest.expectedDraftVersion
    /\ model.allRequest.expectedDraftDigest = model.oneRequest.expectedDraftDigest

GroupRequestsUseObservedDraftAndCommand ==
  /\ (model.groupPreviewRequest = GroupPreviewRequest =>
       /\ model.groupPreviewRequest.requestId = GroupPreviewRequestId
       /\ model.groupPreviewRequest.captureId = GroupPreviewCaptureId
       /\ model.groupPreviewRequest.requestObjectId = GroupPreviewCaptureId
       /\ model.groupPreviewRequest.commandId = NoCommand
       /\ model.groupPreviewRequest.outputId = OutputId
       /\ model.groupPreviewRequest.expectedDraftVersion = RootDraftVersion
       /\ model.groupPreviewRequest.expectedDraftDigest = RootDraftDigest)
  /\ (model.groupApplyRequest = GroupApplyRequest =>
       /\ model.groupApplyRequest.requestId = GroupApplyRequestId
       /\ model.groupApplyRequest.captureId = GroupApplyCaptureId
       /\ model.groupApplyRequest.requestObjectId = GroupApplyCaptureId
       /\ model.groupApplyRequest.outputId = OutputId
       /\ model.groupApplyRequest.commandId = GroupApplyCommandId
       /\ model.groupApplyRequest.commandId # NoCommand
       /\ model.groupApplyRequest.commandId # model.groupPreviewResponse.proposalId
       /\ model.groupApplyRequest.candidateId =
            model.groupPreviewResponse.proposalId
       /\ model.groupApplyRequest.candidateRequestId =
            model.groupPreviewRequest.requestId
       /\ model.groupApplyRequest.candidateCaptureId =
            model.groupPreviewRequest.captureId
       /\ model.groupApplyRequest.expectedDraftVersion = RootDraftVersion
       /\ model.groupApplyRequest.expectedDraftDigest = RootDraftDigest)

RequestResponseOwnership ==
  /\ (model.groupPreviewResponse.status = 200 =>
       /\ model.groupPreviewResponse.captureId =
            model.groupPreviewRequest.captureId
       /\ model.groupPreviewResponse.requestObjectId =
            model.groupPreviewRequest.requestObjectId
       /\ model.groupPreviewResponse.commandId = NoCommand
       /\ model.groupPreviewResponse.proposalId = GroupProposalId)
  /\ (model.groupApplyResponse.status = 200 =>
       /\ model.groupApplyResponse.captureId = model.groupApplyRequest.captureId
       /\ model.groupApplyResponse.requestObjectId =
            model.groupApplyRequest.requestObjectId
       /\ model.groupApplyResponse.commandId = model.groupApplyRequest.commandId
       /\ model.groupApplyRequest.commandId = GroupApplyCommandId
       /\ model.groupApplyResponse.proposalId = NoIdentity
       /\ model.groupApplyRequest.candidateId =
            model.groupPreviewResponse.proposalId
       /\ model.groupApplyRequest.candidateRequestId =
            model.groupPreviewRequest.requestId
       /\ model.groupApplyRequest.candidateCaptureId =
            model.groupPreviewRequest.captureId)
  /\ (model.oneResponse.status = 422 =>
       /\ model.oneResponse.captureId = model.oneRequest.captureId
       /\ model.oneResponse.requestObjectId = model.oneRequest.requestObjectId
       /\ model.oneResponse.commandId = NoCommand)
  /\ (model.allResponse.status = 200 =>
       /\ model.allResponse.captureId = model.allRequest.captureId
       /\ model.allResponse.requestObjectId = model.allRequest.requestObjectId
       /\ model.allResponse.commandId = model.allRequest.commandId
       /\ model.allResponse.commandId = AllCommandId)
  /\ (model.applyResponse.status = 200 =>
       /\ model.applyResponse.captureId = model.applyRequest.captureId
       /\ model.applyResponse.requestObjectId = model.applyRequest.requestObjectId
       /\ model.applyResponse.commandId = model.allResponse.commandId
       /\ model.applyRequest.commandId = model.allResponse.commandId)
  /\ (model.removalApplyResponse.status = 200 =>
       /\ model.removalApplyResponse.captureId =
            model.removalApplyRequest.captureId
       /\ model.removalApplyResponse.requestObjectId =
            model.removalApplyRequest.requestObjectId
       /\ model.removalApplyResponse.commandId =
            model.removalApplyRequest.commandId)
  /\ (model.removalResponse.status = 200 =>
       /\ model.removalResponse.captureId = model.removalRequest.captureId
       /\ model.removalResponse.requestObjectId =
            model.removalRequest.requestObjectId
       /\ model.removalResponse.commandId = NoCommand
       /\ model.removalResponse.proposalId =
            model.removalCandidate.candidateId)

SameEditorRepairsONEWithALL ==
  phase \in {"one-rejected", "all-selected", "all-submitted",
              "all-ready", "all-apply-submitted"} =>
    /\ model.editor = OpenEditor
    /\ model.editor.documentId = model.oneRequest.outputId
    /\ model.editor.editorId = EditorId
    /\ model.editor.documentTimeOrigin = DocumentTimeOrigin
    /\ model.editor.allAvailable = TRUE
    /\ model.choice.choiceId = model.oneRequest.choiceId
    /\ model.choice.field = PatientIdField
    /\ model.choice.rowValuePolicy =
         IF phase = "one-rejected" THEN "ONE" ELSE "ALL"
    /\ model.choice.status =
         CASE phase \in {"one-rejected", "all-selected"} -> "error"
           [] phase = "all-submitted" -> "pending"
           [] phase \in {"all-ready", "all-apply-submitted"} -> "ready"

CorrectALLPreview ==
  model.allPreview = AllResultPreview =>
    /\ model.choice.rowValuePolicy = "ALL"
    /\ model.allPreview.outputId = OutputId
    /\ model.allPreview.rowCount = 1
    /\ model.allPreview.patientIds = SourcePatientIds
    /\ model.allPreview.count = 2

ApplyAndReloadPersistALL ==
  model.reloadedAllSnapshot # NoSnapshot =>
    /\ model.reloadedAllSnapshot.workspace = AllWorkspace
    /\ model.reloadedAllSnapshot.workspace.group.rowValuePolicy = "ALL"
    /\ model.reloadedAllSnapshot.workspace.group.keys = <<>>
    /\ model.reloadedAllSnapshot.workspace.group.aggregate = "COUNT_ROWS"
    /\ model.reloadedAllSnapshot.workspace.additionalBindings = AllPatientIdBinding
    /\ model.reloadedAllSnapshot.draftVersion = AllDraftVersion
    /\ model.reloadedAllSnapshot.draftDigest = AllDraftDigest

RemovalCancelPreservesExactSavedState ==
  model.removalCancelSnapshot # NoSnapshot =>
    /\ model.removalCancelSnapshot = model.removalSnapshot
    /\ model.removalCancelSnapshot.workspace = AllWorkspace
    /\ model.removalCancelSnapshot.draftVersion = AllDraftVersion
    /\ model.removalCancelSnapshot.draftDigest = AllDraftDigest

RemovalApplyUsesSecondCandidate ==
  phase \in {"removal-apply-submitted", "removal-applied", "reloaded-source"} =>
    /\ model.cancelledRemovalCandidate = RemovalOneCandidate
    /\ model.removalCandidate = RemovalTwoCandidate
    /\ RemovalOneCaptureId # RemovalTwoCaptureId
    /\ model.removalResponse.proposalId = RemovalTwoCandidateId
    /\ model.removalApplyRequest = RemovalApplyRequest
    /\ model.removalApplyRequest.candidateId =
         model.removalResponse.proposalId
    /\ model.removalApplyRequest.candidateRequestId =
         model.removalRequest.requestId
    /\ model.removalApplyRequest.candidateCaptureId =
         model.removalResponse.captureId
    /\ model.removalApplyRequest.commandId = RemovalApplyCommandId
    /\ model.removalApplyRequest.commandId # NoCommand
    /\ model.removalApplyRequest.commandId # RemovalTwoCandidateId
    /\ model.removalApplyRequest.requestId = RemovalApplyRequestId
    /\ model.removalApplyRequest.captureId = RemovalApplyCaptureId

RemovalRestoresSourceAfterReload ==
  model.reloadedSourceSnapshot # NoSnapshot =>
    /\ model.reloadedSourceSnapshot.workspace.group = NoGroup
    /\ model.reloadedSourceSnapshot.workspace.sourceColumnIds = SourceColumnIds
    /\ model.reloadedSourceSnapshot.workspace.sourceBindings = SourceBindings
    /\ model.reloadedSourceSnapshot.workspace.sourceRows = SourcePatientRows
    /\ model.reloadedSourceSnapshot.workspace.renderedRows = SourcePatientRows
    /\ model.reloadedSourceSnapshot.workspace.additionalBindings
         \in {NoAdditionalBindings, AllPatientIdBinding}
    /\ model.reloadedSourceSnapshot.draftVersion = RestoredDraftVersion
    /\ model.reloadedSourceSnapshot.draftDigest = RestoredDraftDigest

Spec == Init /\ [][Next]_vars

=============================================================================
