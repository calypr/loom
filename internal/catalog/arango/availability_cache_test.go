package arango

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestAvailabilityCacheRetainsOnlyMatchingCompleteGraph(t *testing.T) {
	client := newAvailabilityTestClient()
	adapter, _ := New(client)
	directory := t.TempDir()
	if err := adapter.ConfigureAvailabilityCache(directory, "database-a"); err != nil {
		t.Fatal(err)
	}
	stamp, err := adapter.availabilityStamp(t.Context(), "p", "g")
	if err != nil {
		t.Fatal(err)
	}
	build := &availabilityBuild{key: stamp.key, buildID: stamp.buildID, done: make(chan struct{})}
	adapter.buildAvailabilityGraph(t.Context(), build, "p", "g", stamp)
	if build.err != nil {
		t.Fatal(build.err)
	}
	info, err := os.Stat(adapter.availabilityCachePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache must be private: info=%v err=%v", info, err)
	}
	restarted, _ := New(client)
	if err := restarted.ConfigureAvailabilityCache(directory, "database-a"); err != nil {
		t.Fatal(err)
	}
	loaded := &availabilityBuild{key: stamp.key, buildID: stamp.buildID, done: make(chan struct{})}
	restarted.buildAvailabilityGraph(t.Context(), loaded, "p", "g", stamp)
	if loaded.err != nil || loaded.graph == nil || client.transactions != 1 {
		t.Fatalf("restart repeated database scan: err=%v transactions=%d", loaded.err, client.transactions)
	}
	if restarted.readAvailabilityCache(strings.Repeat("0", 64)) != nil {
		t.Fatal("cache accepted a different input stamp")
	}
	other, _ := New(client)
	if err := other.ConfigureAvailabilityCache(directory, "database-b"); err != nil {
		t.Fatal(err)
	}
	if other.readAvailabilityCache(stamp.key) != nil {
		t.Fatal("cache crossed database identities")
	}
	if err := os.WriteFile(adapter.availabilityCachePath, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if restarted.readAvailabilityCache(stamp.key) != nil {
		t.Fatal("cache accepted an incomplete graph")
	}
}

func TestAvailabilityCloseCancelsBuildAndRejectsNewWork(t *testing.T) {
	adapter, _ := New(newAvailabilityTestClient())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter.availability = &availabilityBuild{cancel: cancel, done: make(chan struct{})}
	adapter.CloseAvailability()
	adapter.CloseAvailability()
	if ctx.Err() != context.Canceled {
		t.Fatal("shutdown did not cancel graph build")
	}
	_, err := adapter.AvailableColumns(t.Context(), catalog.AvailabilityOptions{Project: "p", DatasetGeneration: "g"})
	if err == nil {
		t.Fatal("closed store accepted new graph work")
	}
}

func TestAvailabilityFailedMembershipIsNotEndlessPreparation(t *testing.T) {
	for _, semantic := range []bool{false, true} {
		client := newAvailabilityTestClient()
		if semantic {
			client.semantics.State = catalog.SemanticInventoryFailed
		} else {
			client.fields.State = catalog.FieldSourceMembershipFailed
		}
		adapter, _ := New(client)
		_, err := adapter.AvailableColumns(t.Context(), catalog.AvailabilityOptions{Project: "p", DatasetGeneration: "g"})
		if err == nil || adapter.availability != nil {
			t.Fatalf("failed membership semantic=%v started preparation: err=%v", semantic, err)
		}
	}
}
