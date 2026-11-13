package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

var (
	_ catalog.CapabilityEvidenceReader = (*Store)(nil)
	_ catalog.InventoryRebuilder       = (*InventoryAdapter)(nil)
	_ catalog.FieldProfileWriter       = (*FieldProfileAdapter)(nil)
	_ catalog.RelationshipDiscoverer   = (*RelationshipAdapter)(nil)
)

type capabilityLeg struct {
	adapterType   reflect.Type
	ownMethods    []string
	foreignMethod []string
}

func TestPartialAdaptersCannotBePassedAsCapabilityEvidenceReaders(t *testing.T) {
	readerType := reflect.TypeOf((*catalog.CapabilityEvidenceReader)(nil)).Elem()
	legs := []capabilityLeg{
		{
			adapterType: reflect.TypeOf((*InventoryAdapter)(nil)),
			ownMethods: []string{
				"RebuildResourceInventory", "ReadResourceInventory", "DiscoverResourceInventory",
			},
			foreignMethod: []string{
				"WriteFieldProfile", "ReadFieldProfile", "DiscoverFieldEnrichment",
				"DiscoverFields", "DiscoverExistingAuthResourcePaths",
				"DiscoverRelationshipObservations", "ReadRelationshipObservations",
			},
		},
		{
			adapterType: reflect.TypeOf((*FieldProfileAdapter)(nil)),
			ownMethods: []string{
				"WriteFieldProfile", "ReadFieldProfile", "DiscoverFieldEnrichment",
				"DiscoverFields", "DiscoverExistingAuthResourcePaths",
			},
			foreignMethod: []string{
				"RebuildResourceInventory", "ReadResourceInventory", "DiscoverResourceInventory",
				"DiscoverRelationshipObservations", "ReadRelationshipObservations",
			},
		},
		{
			adapterType: reflect.TypeOf((*RelationshipAdapter)(nil)),
			ownMethods: []string{
				"DiscoverRelationshipObservations", "ReadRelationshipObservations",
			},
			foreignMethod: []string{
				"RebuildResourceInventory", "ReadResourceInventory", "DiscoverResourceInventory",
				"WriteFieldProfile", "ReadFieldProfile", "DiscoverFieldEnrichment",
				"DiscoverFields", "DiscoverExistingAuthResourcePaths",
			},
		},
	}
	for _, leg := range legs {
		if leg.adapterType.Implements(readerType) {
			t.Fatalf("%s must not satisfy catalog.CapabilityEvidenceReader", leg.adapterType)
		}
		for _, method := range leg.ownMethods {
			if _, ok := leg.adapterType.MethodByName(method); !ok {
				t.Fatalf("%s must serve its own capability %s", leg.adapterType, method)
			}
		}
		for _, method := range leg.foreignMethod {
			if _, ok := leg.adapterType.MethodByName(method); ok {
				t.Fatalf("%s must not advertise foreign capability %s", leg.adapterType, method)
			}
		}
	}
	fullType := reflect.TypeOf((*Store)(nil))
	if !fullType.Implements(readerType) {
		t.Fatal("*Store must satisfy catalog.CapabilityEvidenceReader")
	}
	for _, leg := range legs {
		for _, method := range leg.ownMethods {
			if _, ok := fullType.MethodByName(method); !ok {
				t.Fatalf("*Store must keep the composed capability %s", method)
			}
		}
	}
}

func TestAdapterConstructorsRefuseNilBoundaries(t *testing.T) {
	if _, err := NewInventoryAdapter(nil); err == nil {
		t.Fatal("NewInventoryAdapter(nil) accepted a nil persistence boundary")
	}
	if _, err := NewFieldProfileAdapter(nil); err == nil {
		t.Fatal("NewFieldProfileAdapter(nil) accepted a nil persistence boundary")
	}
	if _, err := NewRelationshipAdapter(nil); err == nil {
		t.Fatal("NewRelationshipAdapter(nil) accepted a nil persistence boundary")
	}
}

func TestZeroValueAdaptersRefuseTypedNeverPanic(t *testing.T) {
	ctx := context.Background()
	type probe struct {
		name string
		call func() error
	}
	probes := []probe{
		{"inventory-only: rebuild", func() error {
			_, err := (&InventoryAdapter{}).RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
				Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}, BatchSize: 2})
			return refusalProbe(err)
		}},
		{"inventory-only: discover", func() error {
			res, err := (&InventoryAdapter{}).DiscoverResourceInventory(ctx, catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g"})
			if err = refusalProbe(err); err != nil {
				return err
			}
			return typedUnavailableResourceInventory(res)
		}},
		{"inventory-only: read alias", func() error {
			res, err := (&InventoryAdapter{}).ReadResourceInventory(ctx, catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g"})
			if err = refusalProbe(err); err != nil {
				return err
			}
			return typedUnavailableResourceInventory(res)
		}},
		{"profile-only: write", func() error {
			return refusalProbe((&FieldProfileAdapter{}).WriteFieldProfile(ctx, fdScope("p", "g", "/a", "Patient"), nil, 100))
		}},
		{"profile-only: read", func() error {
			_, err := (&FieldProfileAdapter{}).ReadFieldProfile(ctx, fdScope("p", "g", "/a", "Patient"))
			return refusalProbe(err)
		}},
		{"profile-only: enrichment", func() error {
			res, err := (&FieldProfileAdapter{}).DiscoverFieldEnrichment(ctx, catalog.FieldEnrichmentOptions{Project: "p", DatasetGeneration: "g"})
			if err = refusalProbe(err); err != nil {
				return err
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("enrichment refusal must be typed unavailable with no values")
			}
			return nil
		}},
		{"profile-only: discover fields", func() error {
			_, err := (&FieldProfileAdapter{}).DiscoverFields(ctx, catalog.PopulatedFieldOptions{Project: "p", DatasetGeneration: "g"})
			return refusalProbe(err)
		}},
		{"profile-only: auth path listing", func() error {
			_, err := (&FieldProfileAdapter{}).DiscoverExistingAuthResourcePaths(ctx, catalog.AuthResourcePathOptions{Project: "p", AllGenerations: true})
			return refusalProbe(err)
		}},
		{"relationship-only: discover", func() error {
			res, err := (&RelationshipAdapter{}).DiscoverRelationshipObservations(ctx, catalog.RelationshipObservationOptions{Project: "p", DatasetGeneration: "g"})
			if err = refusalProbe(err); err != nil {
				return err
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("relationship refusal must be typed unavailable with no values")
			}
			return nil
		}},
		{"relationship-only: read alias", func() error {
			res, err := (&RelationshipAdapter{}).ReadRelationshipObservations(ctx, catalog.RelationshipObservationOptions{Project: "p", DatasetGeneration: "g"})
			if err = refusalProbe(err); err != nil {
				return err
			}
			if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
				return errors.New("relationship alias refusal must be typed unavailable with no values")
			}
			return nil
		}},
		{"composed zero store: every leg", func() error {
			unassembled := &Store{}
			_, err := unassembled.RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
				Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}, BatchSize: 2})
			if err = refusalProbe(err); err != nil {
				return err
			}
			res, err := unassembled.DiscoverResourceInventory(ctx, catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g"})
			if err = refusalProbe(err); err != nil {
				return err
			}
			if err := typedUnavailableResourceInventory(res); err != nil {
				return err
			}
			_, err = unassembled.ReadFieldProfile(ctx, fdScope("p", "g", "/a", "Patient"))
			if err = refusalProbe(err); err != nil {
				return err
			}
			_, err = unassembled.DiscoverRelationshipObservations(ctx, catalog.RelationshipObservationOptions{Project: "p", DatasetGeneration: "g"})
			return refusalProbe(err)
		}},
	}
	for _, p := range probes {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("%s: panic instead of typed refusal: %v", p.name, rec)
				}
			}()
			if err := p.call(); err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
		}()
	}
}

func refusalProbe(err error) error {
	if err == nil {
		return errors.New("expected typed refusal, got nil")
	}
	if !errors.Is(err, ErrStoreLegNotAssembled) {
		return errors.New("refusal must wrap ErrStoreLegNotAssembled, got " + err.Error())
	}
	return nil
}

func typedUnavailableResourceInventory(res catalog.ResourceInventoryResult) error {
	if res.Available || res.Status != catalog.EvidenceUnavailable || len(res.Values) != 0 {
		return errors.New("inventory refusal must be typed unavailable with no values")
	}
	return nil
}
