package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	explorercatalog "github.com/calypr/loom/internal/explorer/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type seedRequest struct {
	Selection        explorer.SelectionRevision `json:"selection"`
	SourceMemberIDs  []string                   `json:"sourceMemberIds"`
	AssignedMemberID string                     `json:"assignedMemberId"`
}

type seedResult struct {
	RevisionID                explorer.ExplicitGroupRevisionID `json:"revisionId"`
	SourceSelectionRevisionID string                           `json:"sourceSelectionRevisionId"`
	SourceMemberIDs           []string                         `json:"sourceMemberIds"`
	Groups                    []seedGroup                      `json:"groups"`
	UnassignedMemberIDs       []string                         `json:"unassignedMemberIds"`
}

type seedGroup struct {
	GroupID   explorer.ExplicitGroupID `json:"groupId"`
	Label     string                   `json:"label"`
	MemberIDs []string                 `json:"memberIds"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: loom-dev-seed-explicit-group <base64-json>")
	}
	encoded, err := base64.StdEncoding.DecodeString(os.Args[1])
	if err != nil {
		return fmt.Errorf("decode seed request: %w", err)
	}
	var request seedRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return fmt.Errorf("parse seed request: %w", err)
	}
	selection := request.Selection.Canonical()
	if err := selection.Validate(); err != nil {
		return fmt.Errorf("validate source selection: %w", err)
	}
	if !selection.Complete || selection.Rule.Kind != explorer.SelectionRuleExplicit || selection.Source.Kind != explorer.SelectionSourceExplicit {
		return fmt.Errorf("source must be a complete explicit resource selection")
	}
	wantIDs := canonicalIDs(request.SourceMemberIDs)
	if len(wantIDs) == 0 || request.AssignedMemberID == "" {
		return fmt.Errorf("source member IDs and one assigned member ID are required")
	}
	if len(wantIDs) != int(selection.MemberCount) || !containsID(wantIDs, request.AssignedMemberID) {
		return fmt.Errorf("requested members do not match the complete source selection")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, "http://arangodb:8529", "loom_dev")
	if err != nil {
		return fmt.Errorf("open development Arango database: %w", err)
	}
	defer func() {
		closeErr := client.Close(context.Background())
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close development Arango database: %w", closeErr))
		}
	}()
	if err := client.Bootstrap(ctx, explorercatalog.BootstrapSpec()); err != nil {
		return fmt.Errorf("bootstrap Explorer storage: %w", err)
	}
	repository, err := explorercatalog.New(client)
	if err != nil {
		return fmt.Errorf("open Explorer storage: %w", err)
	}
	actualMembers, err := readSelectionMembers(ctx, repository, selection)
	if err != nil {
		return err
	}
	actualIDs := make([]string, len(actualMembers))
	for index, member := range actualMembers {
		actualIDs[index] = member.Ref.ID
	}
	if !equalIDs(actualIDs, wantIDs) {
		return fmt.Errorf("stored source member IDs %v do not match fixture IDs %v", actualIDs, wantIDs)
	}

	const groupID explorer.ExplicitGroupID = "j03-reviewed"
	const groupLabel = "J03 reviewed"
	idempotencyKey := "loom-dev-j03-group-v1-" + selection.Project
	revisionID := explorer.ExplicitGroupRevisionIDFor(selection.Project, idempotencyKey)
	header := explorer.ExplicitGroupRevision{
		ID: revisionID, Project: selection.Project, Generation: selection.Generation, ScopeDigest: selection.ScopeDigest,
		ResourceType: selection.ResourceType, SourceSelectionRevisionID: selection.ID, SourceMembershipDigest: selection.MembershipDigest,
		State: explorer.ExplicitGroupRevisionStaging, IdempotencyKey: idempotencyKey, CreatedAt: time.Now().UTC(),
	}
	writerToken := uuid.NewString()
	started, err := repository.BeginExplicitGroupRevision(ctx, header, selection, writerToken)
	if err != nil {
		return fmt.Errorf("begin explicit group revision: %w", err)
	}
	if started.State == explorer.ExplicitGroupRevisionStaging {
		if err := repository.PutExplicitGroupDefinitions(ctx, revisionID, writerToken, []explorer.ExplicitGroupDefinition{{ID: groupID, Label: groupLabel, Ordinal: 0}}); err != nil {
			return fmt.Errorf("store explicit group definition: %w", errors.Join(err, repository.AbortExplicitGroupRevision(context.Background(), revisionID, writerToken)))
		}
		var assigned explorer.ResourceRef
		for _, member := range actualMembers {
			if member.Ref.ID == request.AssignedMemberID {
				assigned = member.Ref
				break
			}
		}
		if assigned.ID == "" {
			return fmt.Errorf("assigned member %q is absent from the stored selection", request.AssignedMemberID)
		}
		if _, err := repository.AppendExplicitGroupMemberships(ctx, revisionID, writerToken, []explorer.ExplicitGroupMembership{{GroupID: groupID, Ref: assigned}}); err != nil {
			return fmt.Errorf("store explicit group membership: %w", errors.Join(err, repository.AbortExplicitGroupRevision(context.Background(), revisionID, writerToken)))
		}
		definitionDigest, membershipDigest, groupCount, memberCount, err := repository.DigestExplicitGroupRevision(ctx, selection.Project, revisionID)
		if err != nil {
			return fmt.Errorf("digest explicit group revision: %w", err)
		}
		if _, err := repository.CompleteExplicitGroupRevision(ctx, revisionID, writerToken, definitionDigest, membershipDigest, groupCount, memberCount, time.Now().UTC()); err != nil {
			return fmt.Errorf("complete explicit group revision: %w", err)
		}
	}
	definitions, err := repository.ListExplicitGroupDefinitions(ctx, selection.Project, revisionID)
	if err != nil {
		return fmt.Errorf("read explicit group definitions: %w", err)
	}
	if len(definitions) != 1 || definitions[0].ID != groupID || definitions[0].Label != groupLabel {
		return fmt.Errorf("stored explicit group definitions differ from the J03 fixture: %#v", definitions)
	}
	groupMembers := make([]string, 0, 1)
	var unassigned []string
	for _, member := range actualMembers {
		if member.Ref.ID == request.AssignedMemberID {
			groupMembers = append(groupMembers, member.Ref.ID)
		} else {
			unassigned = append(unassigned, member.Ref.ID)
		}
	}
	result := seedResult{
		RevisionID: revisionID, SourceSelectionRevisionID: selection.ID, SourceMemberIDs: wantIDs,
		Groups: []seedGroup{{GroupID: groupID, Label: groupLabel, MemberIDs: groupMembers}}, UnassignedMemberIDs: unassigned,
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func readSelectionMembers(ctx context.Context, repository *explorercatalog.Store, selection explorer.SelectionRevision) ([]explorer.SelectionMember, error) {
	members := make([]explorer.SelectionMember, 0, selection.MemberCount)
	after := ""
	for {
		next, err := repository.VisitSelectionMembers(ctx, selection.Project, selection.ID, after, 1000, func(member explorer.SelectionMember) error {
			members = append(members, member)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read stored source selection members: %w", err)
		}
		if next == "" || next == after {
			break
		}
		after = next
	}
	return members, nil
}

func canonicalIDs(ids []string) []string {
	canonical := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			canonical = append(canonical, id)
		}
	}
	sort.Strings(canonical)
	return canonical
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func equalIDs(left, right []string) bool {
	left, right = canonicalIDs(left), canonicalIDs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
