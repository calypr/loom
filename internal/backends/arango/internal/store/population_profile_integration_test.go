package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	populationProfileTargets = 20000
	populationProfileSparse  = 100
	populationProfileDense   = 18000
)

type populationProfileResult struct {
	Targets int                              `json:"targets"`
	Cases   map[string]populationProfileCase `json:"cases"`
}

type populationProfileCase struct {
	Selected         int            `json:"selected"`
	Ratio            float64        `json:"ratio"`
	TargetScan       ProfileSummary `json:"targetScan"`
	MembershipDriven ProfileSummary `json:"membershipDriven"`
}

// TestPopulationPlanProfilesAgainstArango compares the former target-scan
// semijoin with the membership-driven root access path used by the compiler.
// It owns uniquely named collections and is opt-in because it inserts a
// synthetic corpus into a disposable development database.
func TestPopulationPlanProfilesAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_POPULATION_PROFILE") == "" {
		t.Skip("set LOOM_POPULATION_PROFILE=1 to run population plan profiling")
	}
	url := os.Getenv("LOOM_ARANGO_URL")
	if url == "" {
		url = "http://127.0.0.1:8529"
	}
	database := os.Getenv("LOOM_ARANGO_DATABASE")
	if database == "" {
		database = "loom_dev"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	targetsName := "loom_population_profile_targets_" + suffix
	membersName := "loom_population_profile_members_" + suffix
	targets, err := client.ensureCollection(ctx, targetsName, false)
	if err != nil {
		t.Fatal(err)
	}
	members, err := client.ensureCollection(ctx, membersName, false)
	if err != nil {
		_ = targets.Remove(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := members.Remove(cleanup); err != nil {
			t.Errorf("remove owned member collection: %v", err)
		}
		if err := targets.Remove(cleanup); err != nil {
			t.Errorf("remove owned target collection: %v", err)
		}
	})
	if err := ensureIndexes(ctx, targets, [][]string{{"project", "generation", "resourceType", "id"}}); err != nil {
		t.Fatal(err)
	}
	if err := ensureIndexes(ctx, members, [][]string{{"selectionId", "project", "generation", "resourceType", "id"}}); err != nil {
		t.Fatal(err)
	}

	insertPopulationProfileTargets(t, ctx, client, targetsName)
	insertPopulationProfileMembers(t, ctx, client, membersName, "sparse", populationProfileSparse)
	insertPopulationProfileMembers(t, ctx, client, membersName, "dense", populationProfileDense)

	result := populationProfileResult{Targets: populationProfileTargets, Cases: map[string]populationProfileCase{}}
	for _, profileCase := range []struct {
		name     string
		selected int
	}{{name: "sparse", selected: populationProfileSparse}, {name: "dense", selected: populationProfileDense}} {
		targetScan := profilePopulationQuery(t, ctx, client, targetScanPopulationQuery, targetsName, membersName, profileCase.name, profileCase.selected)
		membershipDriven := profilePopulationQuery(t, ctx, client, membershipDrivenPopulationQuery, targetsName, membersName, profileCase.name, profileCase.selected)
		if targetScan.ScannedFull != 0 || membershipDriven.ScannedFull != 0 {
			t.Fatalf("%s population plans performed full collection scans: target=%d membership=%d", profileCase.name, targetScan.ScannedFull, membershipDriven.ScannedFull)
		}
		result.Cases[profileCase.name] = populationProfileCase{
			Selected: profileCase.selected, Ratio: float64(profileCase.selected) / populationProfileTargets,
			TargetScan: targetScan, MembershipDriven: membershipDriven,
		}
		t.Logf("%s selected=%d target_scan runtime=%0.6fs index=%d peak=%d membership_driven runtime=%0.6fs index=%d peak=%d",
			profileCase.name, profileCase.selected,
			targetScan.RuntimeSeconds, targetScan.ScannedIndex, targetScan.PeakMemory,
			membershipDriven.RuntimeSeconds, membershipDriven.ScannedIndex, membershipDriven.PeakMemory,
		)
	}
	if path := strings.TrimSpace(os.Getenv("LOOM_POPULATION_PROFILE_OUTPUT")); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create profile output directory: %v", err)
		}
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func insertPopulationProfileTargets(t *testing.T, ctx context.Context, client *Client, collection string) {
	t.Helper()
	insertPopulationProfileDocuments(t, ctx, client, collection, populationProfileTargets, func(index int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"_key":"target_%08d","project":"profile/project","generation":"generation-1","resourceType":"Specimen","id":"specimen-%08d"}`, index, index))
	})
}

func insertPopulationProfileMembers(t *testing.T, ctx context.Context, client *Client, collection, selectionID string, count int) {
	t.Helper()
	insertPopulationProfileDocuments(t, ctx, client, collection, count, func(index int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"_key":"%s_%08d","selectionId":"%s","project":"profile/project","generation":"generation-1","resourceType":"Specimen","id":"specimen-%08d"}`, selectionID, index, selectionID, index))
	})
}

func insertPopulationProfileDocuments(t *testing.T, ctx context.Context, client *Client, collection string, count int, document func(int) json.RawMessage) {
	t.Helper()
	for offset := 0; offset < count; offset += 1000 {
		end := offset + 1000
		if end > count {
			end = count
		}
		batch := make([]json.RawMessage, 0, end-offset)
		for index := offset; index < end; index++ {
			batch = append(batch, document(index))
		}
		if err := client.InsertBatchRaw(ctx, collection, batch, false, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func profilePopulationQuery(t *testing.T, ctx context.Context, client *Client, query, targets, members, selectionID string, expected int) ProfileSummary {
	t.Helper()
	profile, err := client.Profile(ctx, ProfileRequest{
		Query: query,
		BindVars: map[string]any{
			"@targets": targets, "@members": members, "selection": selectionID,
			"project": "profile/project", "generation": "generation-1", "resourceType": "Specimen",
		},
		BatchSize: populationProfileTargets + 1,
		Count:     true,
		Options:   ProfileOptions{Profile: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.HasMore || len(profile.Result) != expected || profile.Count != expected {
		t.Fatalf("selection %s returned count=%d rows=%d hasMore=%v, want %d", selectionID, profile.Count, len(profile.Result), profile.HasMore, expected)
	}
	return SummarizeProfile(profile)
}

const targetScanPopulationQuery = `
FOR root IN @@targets
  FILTER root.project == @project
  FILTER root.generation == @generation
  FILTER root.resourceType == @resourceType
  FILTER LENGTH((
    FOR member IN @@members
      FILTER member.selectionId == @selection
      FILTER member.project == @project
      FILTER member.generation == @generation
      FILTER member.resourceType == @resourceType
      FILTER member.id == root.id
      LIMIT 1
      RETURN 1
  )) > 0
  RETURN root.id`

const membershipDrivenPopulationQuery = `
FOR member IN @@members
  FILTER member.selectionId == @selection
  FILTER member.project == @project
  FILTER member.generation == @generation
  FILTER member.resourceType == @resourceType
  FOR root IN @@targets
    FILTER root.project == @project
    FILTER root.generation == @generation
    FILTER root.resourceType == @resourceType
    FILTER root.id == member.id
    COLLECT id = root.id
    RETURN id`
