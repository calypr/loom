package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type combineResourceAccess []string

func (a combineResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), a...), nil
}

func TestClickHouseCombineInputResolverUsesExactOrderedPublishedInputs(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	first := recipefixtures.CombineInputReference("labs", "Observation", "execution-a")
	second := recipefixtures.CombineInputReference("visits", "Encounter", "execution-b")
	reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
		first.RevisionID + "/" + first.OutputID:   recipefixtures.CombineInputMaterialization(first, bindings, ScopeDigest(bindings)),
		second.RevisionID + "/" + second.OutputID: recipefixtures.CombineInputMaterialization(second, bindings, ScopeDigest(bindings)),
	}}

	resolved, err := (CombineInputResolver{reader: reader}).Resolve(context.Background(), ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{first, second}}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader.Calls, []string{"execution-a/Observation", "execution-b/Encounter"}) {
		t.Fatalf("exact materialization lookups = %#v", reader.Calls)
	}
	if len(resolved) != 2 || resolved[0].RevisionID != first.RevisionID || resolved[0].OutputID != first.OutputID || resolved[1].RevisionID != second.RevisionID || resolved[1].OutputID != second.OutputID {
		t.Fatalf("resolved input order or identity = %#v", resolved)
	}
	if got := resolved[0].Columns[1].ID; got != "stable-labs-value" {
		t.Fatalf("published stable column ID = %q", got)
	}
	if resolved[0].Project != bindings.Project || resolved[0].DatasetGeneration != bindings.DatasetGeneration || resolved[0].ReceiptID != "receipt-execution-a" || resolved[0].SchemaDigest == "" || resolved[0].ScopeDigest == "" || !resolved[0].Unrestricted {
		t.Fatalf("resolved immutable metadata = %#v", resolved[0])
	}
}

func TestClickHouseCombineInputResolverPreservesExactLegacyNullGeneration(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "project-a", AuthScopeMode: authscope.ReadScopeUnrestricted}
	first := recipefixtures.CombineInputReference("labs", "Observation", "execution-a")
	second := recipefixtures.CombineInputReference("visits", "Encounter", "execution-b")
	reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
		first.RevisionID + "/" + first.OutputID:   recipefixtures.CombineInputMaterialization(first, bindings, ScopeDigest(bindings)),
		second.RevisionID + "/" + second.OutputID: recipefixtures.CombineInputMaterialization(second, bindings, ScopeDigest(bindings)),
	}}

	resolved, err := (CombineInputResolver{reader: reader}).Resolve(context.Background(), ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{first, second}}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 || resolved[0].DatasetGeneration != "" || resolved[1].DatasetGeneration != "" {
		t.Fatalf("resolved legacy-null generation identity = %#v", resolved)
	}
}

func TestClickHouseCombineInputResolverRejectsMismatchedPublishedIdentity(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	reference := recipefixtures.CombineInputReference("labs", "Observation", "execution-a")
	tests := []struct {
		name   string
		mutate func(*published.Materialization)
	}{
		{name: "revision", mutate: func(m *published.Materialization) { m.Revision = "execution-old" }},
		{name: "table selector", mutate: func(m *published.Materialization) { m.Selector.Recipe = "other" }},
		{name: "project", mutate: func(m *published.Materialization) { m.Project = "project-b" }},
		{name: "dataset generation", mutate: func(m *published.Materialization) { m.DatasetGeneration = "generation-b" }},
		{name: "receipt", mutate: func(m *published.Materialization) { m.ReceiptID = "" }},
		{name: "scope digest", mutate: func(m *published.Materialization) { m.ScopeDigest = "wrong-scope" }},
		{name: "stable column ID", mutate: func(m *published.Materialization) { m.Columns[1].ID = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			materialization := recipefixtures.CombineInputMaterialization(reference, bindings, ScopeDigest(bindings))
			test.mutate(&materialization)
			reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
				reference.RevisionID + "/" + reference.OutputID: materialization,
			}}
			_, err := (CombineInputResolver{reader: reader}).Resolve(context.Background(), ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{reference, recipefixtures.CombineInputReference("visits", "Encounter", "execution-b")}}, bindings)
			if err == nil {
				t.Fatal("mismatched published materialization was accepted")
			}
		})
	}
}

func TestClickHouseCombineInputResolverReauthorizesExactGenerationAndScope(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-a"},
	}
	first := recipefixtures.CombineInputReference("labs", "Observation", "execution-a")
	second := recipefixtures.CombineInputReference("visits", "Encounter", "execution-b")
	reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
		first.RevisionID + "/" + first.OutputID:   recipefixtures.CombineInputMaterialization(first, bindings, ScopeDigest(bindings)),
		second.RevisionID + "/" + second.OutputID: recipefixtures.CombineInputMaterialization(second, bindings, ScopeDigest(bindings)),
	}}
	var resolvedProject, resolvedGeneration string
	scopes := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: func(_ context.Context, options authscope.AuthResourcePathOptions) ([]string, error) {
			resolvedProject, resolvedGeneration = options.Project, options.DatasetGeneration
			return []string{"allowed-a", "allowed-b"}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"allowed-a", "allowed-b"}})

	resolved, err := (CombineInputResolver{reader: reader, scopes: scopes}).Resolve(ctx, ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{first, second}}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedProject != "project-a" || resolvedGeneration != "generation-a" {
		t.Fatalf("authorization lookup used project %q generation %q", resolvedProject, resolvedGeneration)
	}
	if resolved[0].Unrestricted || !reflect.DeepEqual(resolved[0].AuthResourcePaths, []string{"allowed-a"}) {
		t.Fatalf("effective restricted source scope = %#v", resolved[0])
	}
}

func TestClickHouseCombineInputResolverRejectsScopeMismatchAndRestrictedEmpty(t *testing.T) {
	t.Run("different current scope", func(t *testing.T) {
		persisted := recipe.RuntimeBindings{
			Project: "project-a", DatasetGeneration: "generation-a",
			AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-a"},
		}
		reference := recipefixtures.CombineInputReference("labs", "Observation", "execution-a")
		materialization := recipefixtures.CombineInputMaterialization(reference, persisted, ScopeDigest(persisted))
		reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
			reference.RevisionID + "/" + reference.OutputID: materialization,
		}}
		scopes := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
				return []string{"allowed-a", "allowed-b"}, nil
			},
		})
		ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"allowed-a", "allowed-b"}})
		requested := persisted
		requested.AuthResourcePaths = []string{"allowed-b"}
		_, err := (CombineInputResolver{reader: reader, scopes: scopes}).Resolve(ctx, ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{reference, recipefixtures.CombineInputReference("visits", "Encounter", "execution-b")}}, requested)
		if err == nil {
			t.Fatal("input with a different effective scope was accepted")
		}
	})

	t.Run("restricted empty caller scope", func(t *testing.T) {
		bindings := recipe.RuntimeBindings{
			Project: "project-a", DatasetGeneration: "generation-a",
			AuthScopeMode: authscope.ReadScopeUnrestricted,
		}
		reference := recipefixtures.CombineInputReference("labs", "Observation", "execution-a")
		reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
			reference.RevisionID + "/" + reference.OutputID: recipefixtures.CombineInputMaterialization(reference, bindings, ScopeDigest(bindings)),
		}}
		scopes := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ResourceAccess: combineResourceAccess{},
			ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
				return []string{"allowed-a"}, nil
			},
		})
		ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthorizationHeader: "Bearer token"})
		_, err := (CombineInputResolver{reader: reader, scopes: scopes}).Resolve(ctx, ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{reference, recipefixtures.CombineInputReference("visits", "Encounter", "execution-b")}}, bindings)
		if err == nil {
			t.Fatal("restricted-empty current scope was accepted")
		}
	})
}
