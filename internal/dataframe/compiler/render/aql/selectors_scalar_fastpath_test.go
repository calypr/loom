package aql

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/spec"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestRenderSelectorArrayFromSourceUsesNullSafeSingleton(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	selector := spec.Selector{Steps: []spec.SelectorStep{{Field: "outer"}, {Field: "value"}}}

	for _, firstOnly := range []bool{false, true} {
		got, err := renderer.renderSelectorArrayFromSource("doc", selector, false, firstOnly)
		if err != nil {
			t.Fatal(err)
		}
		want := "(doc.outer.value == null ? [] : [doc.outer.value])"
		if got != want {
			t.Fatalf("firstOnly=%v expression = %q, want %q", firstOnly, got, want)
		}
	}
}

func TestRenderSelectorArrayFromSourceUsesInlineArrayExpansion(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	selector := spec.Selector{Steps: []spec.SelectorStep{
		{Field: "outer"},
		{Field: "items", Iterate: true},
		{Field: "nested"},
		{Field: "value"},
	}}

	got, err := renderer.renderSelectorArrayFromSource("doc", selector, false, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "(doc.outer.items ? doc.outer.items[* FILTER CURRENT.nested.value != null RETURN CURRENT.nested.value] : [])"
	if got != want {
		t.Fatalf("array expansion = %q, want %q", got, want)
	}
}

func TestRenderSelectorArrayFromSourceKeepsGeneralSelectorsOnSubqueries(t *testing.T) {
	index := 1
	tests := []struct {
		name      string
		selector  spec.Selector
		setSource bool
		firstOnly bool
		source    string
		want      []string
		wantBinds map[string]any
	}{
		{
			name: "multiple iterations",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "items", Iterate: true}, {Field: "nested", Iterate: true}, {Field: "value"},
			}},
			source: "doc",
			want:   []string{"FOR __root IN [doc]", "FOR __s0 IN (", "FOR __s1 IN ("},
		},
		{
			name: "indexed",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "items"}, {Field: "value", Index: &index},
			}},
			source: "doc",
			want:   []string{"FOR __root IN [doc]", "__value = ((__s0.value ? __s0.value : [])[1])"},
		},
		{
			name: "indexed after iteration",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "items", Iterate: true}, {Field: "value", Index: &index},
			}},
			source: "doc",
			want:   []string{"FOR __root IN [doc]", "FOR __s0 IN (", "__value = ((__s0.value ? __s0.value : [])[1])"},
		},
		{
			name: "indexed before iteration",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "groups", Index: &index}, {Field: "items", Iterate: true}, {Field: "value"},
			}},
			source: "doc",
			want:   []string{"FOR __root IN [doc]", "LET __s0 = ((__root.groups ? __root.groups : [])[1])", "FOR __s1 IN ("},
		},
		{
			name: "final iteration",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "items", Iterate: true}, {Field: "nested", Iterate: true},
			}},
			source: "doc",
			want:   []string{"FOR __root IN [doc]", "FOR __s0 IN (", "__value = (__s0.nested ? __s0.nested : [])"},
		},
		{
			name: "first only",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "items", Iterate: true}, {Field: "value"},
			}},
			source:    "doc",
			firstOnly: true,
			want:      []string{"FOR __root IN [doc]", "LIMIT 1", "RETURN __value"},
		},
		{
			name: "filtered",
			selector: spec.Selector{
				Steps:  []spec.SelectorStep{{Field: "items", Iterate: true}, {Field: "kind"}},
				Filter: &spec.ContainsFilter{Field: "display", Needle: "target"},
			},
			source:    "doc",
			want:      []string{"FOR __root IN [doc]", "FOR __s0 IN (", "FILTER CONTAINS(__s0.display ? __s0.display : \"\", @__loom_physical_selector_contains)"},
			wantBinds: map[string]any{"__loom_physical_selector_contains": "target"},
		},
		{
			name: "set source",
			selector: spec.Selector{Steps: []spec.SelectorStep{
				{Field: "items", Iterate: true}, {Field: "value"},
			}},
			setSource: true,
			source:    "child_set",
			want:      []string{"FOR __item IN child_set", "FOR __root IN [__item.payload]", "FOR __s0 IN ("},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			renderer := &physicalPlanRenderer{
				bindVars:       map[string]any{},
				collectionKeys: map[string]struct{}{},
				setVariables:   map[string]string{},
				reservedVars:   map[string]struct{}{},
			}
			got, err := renderer.renderSelectorArrayFromSource(test.source, test.selector, test.setSource, test.firstOnly)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, "? [] : [") {
				t.Fatalf("general selector unexpectedly used singleton rendering: %s", got)
			}
			if strings.Contains(got, "[* FILTER CURRENT.") {
				t.Fatalf("ineligible selector unexpectedly used inline array expansion: %s", got)
			}
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("expression %q does not contain %q", got, want)
				}
			}
			wantBinds := test.wantBinds
			if wantBinds == nil {
				wantBinds = map[string]any{}
			}
			if !reflect.DeepEqual(renderer.bindVars, wantBinds) {
				t.Errorf("bind vars = %#v, want %#v", renderer.bindVars, wantBinds)
			}
		})
	}
}

func TestRenderSelectorArrayFromSourceRejectsEmptySelector(t *testing.T) {
	renderer := &physicalPlanRenderer{}
	if _, err := renderer.renderSelectorArrayFromSource("doc", spec.Selector{}, false, false); err == nil {
		t.Fatal("empty selector unexpectedly rendered")
	}
}

func TestRenderSelectorArrayFromSourceSingletonValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("close Arango client: %v", err)
		}
	}()

	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	selector := spec.Selector{Steps: []spec.SelectorStep{{Field: "outer"}, {Field: "value"}}}
	expression, err := renderer.renderSelectorArrayFromSource("doc", selector, false, false)
	if err != nil {
		t.Fatal(err)
	}
	documents := []map[string]any{
		{},
		{"outer": nil},
		{"outer": map[string]any{}},
		{"outer": map[string]any{"value": nil}},
		{"outer": map[string]any{"value": 0}},
		{"outer": map[string]any{"value": false}},
		{"outer": map[string]any{"value": ""}},
		{"outer": map[string]any{"value": []any{1, 2}}},
	}
	var got []map[string]any
	query := "FOR doc IN @documents RETURN {selected: " + expression + "}"
	if err := client.QueryRows(ctx, query, len(documents), map[string]any{"documents": documents}, func(row map[string]any) error {
		got = append(got, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{0}},
		{"selected": []any{false}},
		{"selected": []any{""}},
		{"selected": []any{[]any{1, 2}}},
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("selector values = %s, want %s", gotJSON, wantJSON)
	}
}

func TestRenderSelectorArrayFromSourceArrayExpansionValuesAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := arangostore.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("close Arango client: %v", err)
		}
	}()

	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{},
		collectionKeys: map[string]struct{}{},
		setVariables:   map[string]string{},
		reservedVars:   map[string]struct{}{},
	}
	selector := spec.Selector{Steps: []spec.SelectorStep{
		{Field: "outer"},
		{Field: "items", Iterate: true},
		{Field: "nested"},
		{Field: "value"},
	}}
	expression, err := renderer.renderSelectorArrayFromSource("doc", selector, false, false)
	if err != nil {
		t.Fatal(err)
	}
	documents := []map[string]any{
		{},
		{"outer": nil},
		{"outer": map[string]any{}},
		{"outer": map[string]any{"items": nil}},
		{"outer": map[string]any{"items": []any{}}},
		{"outer": map[string]any{"items": []any{
			map[string]any{},
			map[string]any{"nested": nil},
			map[string]any{"nested": map[string]any{"value": nil}},
			map[string]any{"nested": map[string]any{"value": 0}},
			map[string]any{"nested": map[string]any{"value": false}},
			map[string]any{"nested": map[string]any{"value": ""}},
			map[string]any{"nested": map[string]any{"value": "second"}},
			map[string]any{"nested": map[string]any{"value": "first"}},
			map[string]any{"nested": map[string]any{"value": "second"}},
		}}},
	}
	var got []map[string]any
	query := "FOR doc IN @documents RETURN {selected: " + expression + "}"
	if err := client.QueryRows(ctx, query, len(documents), map[string]any{"documents": documents}, func(row map[string]any) error {
		got = append(got, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{}},
		{"selected": []any{0, false, "", "second", "first", "second"}},
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("array selector values = %s, want %s", gotJSON, wantJSON)
	}
}
