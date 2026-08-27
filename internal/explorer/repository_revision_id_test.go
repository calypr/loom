package explorer

import "testing"

func TestRepositoryRevisionIDIncludesImmutableReceiptIdentity(t *testing.T) {
	first := RepositoryRevisionID("program/project", "commit", "intent", "generation", "receipt-a")
	replayed := RepositoryRevisionID("program/project", "commit", "intent", "generation", "receipt-a")
	upgraded := RepositoryRevisionID("program/project", "commit", "intent", "generation", "receipt-b")
	if first != replayed {
		t.Fatalf("repository replay identity changed: %q != %q", first, replayed)
	}
	if first == upgraded {
		t.Fatalf("compiler receipt upgrade reused revision identity %q", first)
	}
}
