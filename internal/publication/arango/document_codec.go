package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	publication "github.com/calypr/loom/internal/publication"
)

func (s *Store) validate() error {
	if s == nil || s.client == nil {
		return ErrNilQueryClient
	}
	return nil
}

func (s *Store) manifestRows(ctx context.Context, query string, bindVars map[string]any) ([]publication.Manifest, error) {
	rows := make([]publication.Manifest, 0, 1)
	var unexpected error
	err := s.client.QueryRows(ctx, query, metadataBatchSize, bindVars, func(row map[string]any) error {
		manifest, err := manifestFromValue(row)
		if err != nil {
			unexpected = err
			return err
		}
		rows = append(rows, manifest)
		if len(rows) > 1 {
			unexpected = fmt.Errorf("%w: query returned multiple manifests", ErrUnexpectedStoreResult)
			return unexpected
		}
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return nil, unexpected
		}
		return nil, err
	}
	return rows, nil
}

func lifecycleBindVars(project string) map[string]any {
	return map[string]any{
		"@lifecycle_collection": LifecycleCollection,
		"project":               project,
		"manifest_record_type":  manifestRecordType,
		"active_record_type":    activeRecordType,
	}
}

func validateProject(project string) error {
	_, err := publication.NewRef(project, "generation-project-validation")
	return err
}

func manifestDocument(manifest publication.Manifest) (map[string]any, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode generation manifest document: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode generation manifest document: %w", err)
	}
	document["_key"] = manifestDocumentKey(manifest.Dataset)
	document["recordType"] = manifestRecordType
	return document, nil
}

func activePlaceholderDocument(project string) map[string]any {
	return map[string]any{"_key": activeDocumentKey(project), "recordType": activeRecordType, "project": project}
}

func schemaIdentityBindValue(identity publication.SchemaSnapshot) (map[string]any, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func manifestFromValue(value any) (publication.Manifest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("%w: encode manifest row: %w", ErrUnexpectedStoreResult, err)
	}
	var decoded struct {
		Dataset        publication.Ref            `json:"dataset"`
		State          publication.State          `json:"state"`
		SchemaIdentity publication.SchemaSnapshot `json:"schemaIdentity"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return publication.Manifest{}, fmt.Errorf("%w: decode manifest row: %w", ErrUnexpectedStoreResult, err)
	}
	switch decoded.State {
	case "PREFLIGHT", "ANALYZING":
		decoded.State = publication.StateLoading
	case "SUPERSEDED":
		decoded.State = publication.StateReady
	case publication.StateLoading, publication.StateReady, publication.StateFailed:
	default:
		return publication.Manifest{}, fmt.Errorf("%w: unknown manifest state %q", ErrUnexpectedStoreResult, decoded.State)
	}
	manifest := publication.Manifest(decoded)
	if err := manifest.Validate(); err != nil {
		return publication.Manifest{}, fmt.Errorf("%w: decode manifest row: %w", ErrUnexpectedStoreResult, err)
	}
	return manifest, nil
}

func manifestIdentityEqual(left, right publication.Manifest) bool {
	return reflect.DeepEqual(left, right)
}

func refFromValue(value any) (publication.Ref, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return publication.Ref{}, fmt.Errorf("%w: encode generation reference row: %w", ErrUnexpectedStoreResult, err)
	}
	var ref publication.Ref
	if err := json.Unmarshal(data, &ref); err != nil {
		return publication.Ref{}, fmt.Errorf("%w: decode generation reference row: %w", ErrUnexpectedStoreResult, err)
	}
	if err := ref.Validate(); err != nil {
		return publication.Ref{}, fmt.Errorf("%w: decode generation reference row: %w", ErrUnexpectedStoreResult, err)
	}
	return ref, nil
}

func manifestDocumentKey(ref publication.Ref) string {
	return documentKey("manifest", ref.Project, ref.Generation)
}

func activeDocumentKey(project string) string { return documentKey("active", project) }

func documentKey(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(documentKeyDomain))
	for _, value := range append([]string{kind}, values...) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return kind + "_" + hex.EncodeToString(hash.Sum(nil))
}
