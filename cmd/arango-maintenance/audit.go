package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	storearango "github.com/calypr/loom/internal/store/arango"
)

func runPreviewIndexAudit(endpoint, database string, timeout time.Duration) error {
	if err := validateLocalTarget(endpoint, database, true, false); err != nil {
		return err
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := storearango.OpenExisting(ctx, endpoint, database)
	if err != nil {
		return err
	}
	defer client.Close(context.Background())
	collections, err := client.CollectionNamesReadOnly(ctx)
	if err != nil {
		return err
	}
	workspaceReport := explorerarango.WorkspaceAuditReport{
		Collections: []string{}, Entries: []explorerarango.WorkspaceAuditEntry{},
		ByClass: map[string]int{}, DigestRelations: map[string]int{}, ByFeature: map[string]int{},
	}
	for _, collection := range collections {
		workspaceReport.Collections = append(workspaceReport.Collections, collection)
		if err := client.VisitCollectionDocumentsRaw(ctx, collection, func(raw json.RawMessage) error {
			return workspaceReport.ObserveWorkspaceAuditDocument(collection, raw)
		}); err != nil {
			return fmt.Errorf("workspace audit incomplete at collection %s: %w", collection, err)
		}
	}
	previewIndexReport, err := client.PreviewIndexes().AuditPreviewSupersededIndexes(ctx)
	if err != nil {
		return fmt.Errorf("preview-index audit incomplete: %w", err)
	}
	output := struct {
		Database  string                              `json:"database"`
		Complete  bool                                `json:"complete"`
		Workspace explorerarango.WorkspaceAuditReport `json:"workspace"`
		storearango.PreviewSupersedesAudit
	}{Database: database, Complete: true, Workspace: workspaceReport, PreviewSupersedesAudit: previewIndexReport}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}
