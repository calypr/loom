package sourceload

const legacySourceLoadActivationAQL = `
LET active = DOCUMENT(@@lifecycle, @active_key)
LET releasePointer = FIRST(
  FOR pointer IN @@lifecycle
    FILTER pointer.project == @project AND pointer.recordType == "active_project_release"
    LIMIT 1
    RETURN pointer
)
LET source = DOCUMENT(@@states, @state_key)
FILTER active != null AND active.project == @project AND active.recordType == "active_generation"
FILTER active.dataset == null AND active.manifestKey == null
FILTER releasePointer != null AND TO_NUMBER(NOT_NULL(releasePointer.revision, 0)) == @base_revision
FILTER NOT_NULL(releasePointer.releaseKey, "") == "" AND NOT_NULL(releasePointer.releaseId, "") == ""
FILTER source == null
  ? (@legacy_tracked == false AND @legacy_revision == 0 AND @legacy_repository == "")
  : (source.project == @project
    AND TO_NUMBER(NOT_NULL(source.sourceRevision, 0)) == @legacy_revision
    AND NOT_NULL(source.sourceTracked, false) == @legacy_tracked
    AND NOT_NULL(source.sourceRepository, "") == @legacy_repository
    AND NOT_NULL(source.pendingOperationId, "") == ""
    AND NOT_NULL(source.pendingPhase, "") == ""
    AND NOT_NULL(source.leaseOwnerId, "") == ""
    AND TO_NUMBER(NOT_NULL(source.leaseExpiresAt, 0)) < DATE_NOW())
FILTER active.sourceTracked != true OR @legacy_tracked == true
UPDATE active WITH {sourceTracked: false} IN @@lifecycle
RETURN {updated: true}`

const legacySourceLoadActivationWithoutStateAQL = `
LET active = DOCUMENT(@@lifecycle, @active_key)
LET releasePointer = FIRST(
  FOR pointer IN @@lifecycle
    FILTER pointer.project == @project AND pointer.recordType == "active_project_release"
    LIMIT 1
    RETURN pointer
)
FILTER active != null AND active.project == @project AND active.recordType == "active_generation"
FILTER active.dataset == null AND active.manifestKey == null
FILTER releasePointer != null AND TO_NUMBER(NOT_NULL(releasePointer.revision, 0)) == @base_revision
FILTER NOT_NULL(releasePointer.releaseKey, "") == "" AND NOT_NULL(releasePointer.releaseId, "") == ""
FILTER @legacy_tracked == false AND @legacy_revision == 0 AND @legacy_repository == ""
FILTER active.sourceTracked != true
UPDATE active WITH {sourceTracked: false} IN @@lifecycle
RETURN {updated: true}`
