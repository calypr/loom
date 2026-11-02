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

// F-B27 durable controls (REPAIR-15). The project-write-binding entry that
// backs AuthorizeWrite must live in its own cache namespace, separated by a
// discriminator on the key type and not by a reserved string. Read-path
// datasetGeneration values arrive from request payloads and are only
// TrimSpace'd (catalog.NormalizeDatasetGeneration), unlike publish-time
// validateOpaqueIdentifier, so any byte sequence is an ordinary generation
// key on the read side. That is exactly why a string sentinel was unsafe: a
// restricted caller could address the write-binding entry directly.

// fb27ctlSentinelGeneration is the literal that the first F-B27 fix used as
// its all-generations cache sentinel. The promoted probe below replays it as
// the poison payload.
const fb27ctlSentinelGeneration = "\x00all-generations"

// fb27ctlLister is a catalog-discovery test double. It records every invocation
// so the controls below can prove which decisions re-consulted the catalog. It
// is SHARED mutable state across a test's resolver calls, which is why the
// recording is mutex-guarded: ScopeResolver invokes its lister OUTSIDE the
// cache lock (scope_resolver.go:378,421), so a distinct-key burst on a cold
// cache drives several goroutines into list() concurrently — the counters-map
// flake class this helper shares with the pre-hygiene authscope doubles (the
// per-subtest recorder standard recorded in
// covers_execution_paths_control_test.go:102-105). Guarding here keeps a
// -count>1 / concurrent driver race-clean in addition to the already-serial
// single-threaded controls. The behind callback is invoked outside the lock so
// it never runs while the recorder is held.
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

// count returns the number of recorded invocations under the recorder lock.
func (l *fb27ctlLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// snapshotCalls returns a copy of every recorded options value under the
// recorder lock, safe to read while other goroutines keep invoking list().
func (l *fb27ctlLister) snapshotCalls() []catalog.AuthResourcePathOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]catalog.AuthResourcePathOptions(nil), l.opts...)
}

// lastCall returns the most recently recorded options value under the recorder
// lock.
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

// TestFB27SentinelGenerationPoisonCannotReachAuthorizeWrite promotes the
// adversarial probe TestFB27RevSentinelCollisionPoisonsWriteBinding into the
// repo suite. A restricted principal performs a read whose datasetGeneration
// is the reserved all-generations literal, so an empty path list lands under
// {victim, "\x00all-generations"}, and then asks to publish a path it holds
// for another project into that victim. Repaired contract: the poisoned entry
// is never served to AuthorizeWrite, the victim's project lister is consulted
// on the decision, and the write stays forbidden for the TTL.
func TestFB27SentinelGenerationPoisonCannotReachAuthorizeWrite(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			// The victim genuinely records this path: the exact state that
			// made the pre-repair path-membership check exploitable.
			return []string{"projB/data"}, nil
		}
		// No real catalog holds a control-character generation, because
		// publish rejects it, so a generation-scoped listing is empty.
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

// TestFB27ConcurrentDistinctKeyBurstKeepsSharedRecorderConsistent is the
// durability control for the counters-map flake class: it drives ONE shared
// fb27ctlLister from many goroutines at once. ScopeResolver consults the lister
// outside its cache lock (scope_resolver.go:378,421), so a cold-cache burst
// across DISTINCT project/generation keys — which never collide in the resolver
// cache — runs list() concurrently on the shared recorder. Under the pre-hygiene
// unlocked counter this is exactly where `go test -race` flagged the shared
// calls/opts mutation; the recorder mutex added above makes it race-clean. The
// assertions confirm the shared control stayed internally consistent (one
// recording per invocation, no lost/torn appends) while the resolvers returned
// the correct per-key answers. The behind callback is read-only so the only
// shared mutable state exercised here is the recorder under test.
func TestFB27ConcurrentDistinctKeyBurstKeepsSharedRecorderConsistent(t *testing.T) {
	const goroutines = 64
	l := &fb27ctlLister{behind: func(opts catalog.AuthResourcePathOptions) ([]string, error) {
		// Read-only dispatch: an all-generations lookup (the write-binding
		// namespace) yields the held path; a generation-scoped read yields
		// none. Deterministic per opts only — the behind closure touches no
		// shared state, so the only shared mutable state under test is the
		// recorder itself.
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
			// Distinct project per goroutine guarantees distinct resolver cache
			// keys on a cold cache, so BOTH lookups below are concurrent lister
			// misses that funnel into the shared recorder at once.
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

	// Exactly one generation-scoped read plus one all-generations listing per
	// goroutine landed in the recorder, with none lost or torn.
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
