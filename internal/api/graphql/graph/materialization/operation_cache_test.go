package materializationapi

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
)

func TestProjectsCacheSharesInflightResolutionPerOperation(t *testing.T) {
	var calls atomic.Int32
	service := NewService(Config{CandidateProjects: func(context.Context) ([]string, error) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return []string{"project"}, nil
	}})
	ctx := service.WithOperationContext(context.Background(), 0)
	principal := &authscope.Principal{Subject: "subject"}
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			projects, err := service.projects(ctx, principal)
			if err != nil || len(projects) != 1 || projects[0] != "project" {
				t.Errorf("projects = %#v, err = %v", projects, err)
			}
		}()
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("candidate project calls = %d, want 1", calls.Load())
	}

	second := service.WithOperationContext(context.Background(), 0)
	if _, err := service.projects(second, principal); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("cache crossed operation contexts: calls = %d", calls.Load())
	}
}
