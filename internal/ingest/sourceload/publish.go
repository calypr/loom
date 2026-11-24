package sourceload

func sourceLoadPublicationReceipt(record sourceLoadRecord, reused bool) SourcePublishReceipt {
	return SourcePublishReceipt{OperationID: record.OperationID, SourceRevision: record.SourceRevision, ManifestHash: record.ManifestHash, ExecutionID: record.ExecutionID, State: "PUBLISHED", Reused: reused}
}
