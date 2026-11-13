package authscope

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
)

const fb27ctlSentinelGeneration = "\x00all-generations"

type fb27ctlLister struct {
	mu     sync.Mutex
	calls  int
	opts   []catalog.AuthResourcePathOptions
	behind func(catalog.AuthResourcePathOptions) ([]string, error)
}

func (l *fb27ctlLister) list(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	result, err := l.behind(opts)
	l.mu.Lock()
	l.calls++
	l.opts = append(l.opts, opts)
	l.mu.Unlock()
	return result, err
}

func (l *fb27ctlLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *fb27ctlLister) snapshotCalls() []catalog.AuthResourcePathOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]catalog.AuthResourcePathOptions(nil), l.opts...)
}

func (l *fb27ctlLister) lastCall() (catalog.AuthResourcePathOptions, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.opts) == 0 {
		return catalog.AuthResourcePathOptions{}, false
	}
	return l.opts[len(l.opts)-1], true
}

func fb27ctlResolver(l *fb27ctlLister) *ScopeResolver {
	return NewScopeResolver(ScopeResolverConfig{
		CacheTTL:                      time.Minute,
		ListExistingAuthResourcePaths: l.list,
	})
}

func fb27ctlPrincipal(paths ...string) *Principal {
	return &Principal{Subject: "u", AuthResourcePaths: append([]string(nil), paths...)}
}

func TestFB27SentinelGenerationPoisonCannotReachAuthorizeWrite(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			return []string{"projB/data"}, nil
		}
		return nil, nil
	}}
	r := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("/projA/data")
	ctx := context.Background()

	if err := r.AuthorizeWrite(ctx, principal, "projB", "/projA/data"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED unpoisoned cross-project write = %v, want ErrForbidden", err)
	}
	r.InvalidateProject("projB")

	if _, err := r.ResolveReadScopeForGeneration(ctx, principal, "projB", fb27ctlSentinelGeneration, nil); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED poisoning read (result irrelevant) = %v", err)
	}
	callsAfterPoison := l.count()

	err := r.AuthorizeWrite(ctx, principal, "projB", "/projA/data")
	if l.count() == callsAfterPoison {
		if err == nil {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): F-B27 BYPASS CONFIRMED: sentinel cache collision let a projA-scoped principal write /projA/data into projB whose recorded path is projB/data")
		}
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the write decision was answered from the entry the poisoned read populated (no lister call) with err=%v", err)
	}
	all := l.snapshotCalls()
	if len(all) == 0 {
		t.Fatal("VERDICT=UNEXPECTED no catalog discovery call recorded")
	}
	last, ok := l.lastCall()
	if !ok {
		t.Fatal("VERDICT=UNEXPECTED no catalog discovery call recorded")
	}
	if !last.AllGenerations || last.DatasetGeneration != "" || last.Project != "projB" {
		t.Fatalf("VERDICT=UNEXPECTED project-binding lookup must be all-generations mode with an empty generation for projB, got %+v (all opts %+v)", last, all)
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED warmed cross-project write = %v, want ErrForbidden", err)
	}
	t.Logf("VERDICT=REPAIRED sentinel-literal generation %q stayed an ordinary generation key; the write decision re-listed projB (%d fresh call) and denied /projA/data", fb27ctlSentinelGeneration, l.count()-callsAfterPoison)
}

func TestFB27ConcurrentDistinctKeyBurstKeepsSharedRecorderConsistent(t *testing.T) {
	const goroutines = 64
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			return []string{opts.Project + "/data"}, nil
		}
		return nil, nil
	}}
	r := fb27ctlResolver(l)
	ctx := context.Background()

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			project := "proj" + strconv.Itoa(i)
			if _, err := r.listExistingPaths(ctx, project, "gen-"+strconv.Itoa(i)); err != nil {
				errCh <- err
				return
			}
			if _, err := r.listExistingPathsAllGenerations(ctx, project); err != nil {
				errCh <- err
				return
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("VERDICT=UNEXPECTED concurrent resolver error = %v", err)
	}

	if got := l.count(); got != 2*goroutines {
		t.Fatalf("VERDICT=UNEXPECTED lost/torn recorder increments: count=%d want=%d", got, 2*goroutines)
	}
	snapshot := l.snapshotCalls()
	if len(snapshot) != l.count() {
		t.Fatalf("VERDICT=UNEXPECTED recorder desync: snapshot=%d count=%d", len(snapshot), l.count())
	}
	seenProjects := map[string]int{}
	for _, opts := range snapshot {
		seenProjects[opts.Project]++
	}
	if len(seenProjects) != goroutines {
		t.Fatalf("VERDICT=UNEXPECTED expected %d distinct projects recorded, got %d", goroutines, len(seenProjects))
	}
	t.Logf("VERDICT=HARDENED | concurrent distinct-key burst drove %d goroutines through the shared recorder: %d invocations, %d keys, race-clean", goroutines, l.count(), len(seenProjects))
}
