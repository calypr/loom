package workspace

import "github.com/calypr/loom/internal/explorer/authoringv2"

const CurrentSemanticsVersion = authoringv2.CurrentSemanticsVersion

type Workspace = authoringv2.Workspace
type Document = authoringv2.Document
type CatalogSnapshot = authoringv2.CatalogSnapshot
type StoredWorkspaceMigration = authoringv2.StoredWorkspaceMigration
type Command = authoringv2.Command
type CommandResult = authoringv2.CommandResult

var (
	DecodeWorkspace                      = authoringv2.DecodeWorkspace
	DecodeCurrentWorkspace               = authoringv2.DecodeCurrentWorkspace
	ConvertStoredSourcePlan              = authoringv2.ConvertStoredSourcePlan
	MigrateStoredWorkspace               = authoringv2.MigrateStoredWorkspace
	CanonicalStoredWorkspaceForMigration = authoringv2.CanonicalStoredWorkspaceForMigration
	VerifyStoredWorkspaceForMigration    = authoringv2.VerifyStoredWorkspaceForMigration
)
