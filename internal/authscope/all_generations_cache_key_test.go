package authscope

import (
	"context"
	"errors"
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

type fb27ctlLister struct {
	calls  int
	opts   []catalog.AuthResourcePathOptions
	behind func(catalog.AuthResourcePathOptions) ([]string, error)
}

func (l *fb27ctlLister) list(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
	l.calls++
	l.opts = append(l.opts, opts)
	return l.behind(opts)
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
	callsAfterPoison := l.calls

	err := r.AuthorizeWrite(ctx, principal, "projB", "/projA/data")
	if l.calls == callsAfterPoison {
		if err == nil {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): F-B27 BYPASS CONFIRMED: sentinel cache collision let a projA-scoped principal write /projA/data into projB whose recorded path is projB/data")
		}
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the write decision was answered from the entry the poisoned read populated (no lister call) with err=%v", err)
	}
	if len(l.opts) == 0 {
		t.Fatal("VERDICT=UNEXPECTED no catalog discovery call recorded")
	}
	last := l.opts[len(l.opts)-1]
	if !last.AllGenerations || last.DatasetGeneration != "" || last.Project != "projB" {
		t.Fatalf("VERDICT=UNEXPECTED project-binding lookup must be all-generations mode with an empty generation for projB, got %+v (all opts %+v)", last, l.opts)
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED warmed cross-project write = %v, want ErrForbidden", err)
	}
	t.Logf("VERDICT=REPAIRED sentinel-literal generation %q stayed an ordinary generation key; the write decision re-listed projB (%d fresh call) and denied /projA/data", fb27ctlSentinelGeneration, l.calls-callsAfterPoison)
}
