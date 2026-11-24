package releasecommit

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	store "github.com/calypr/loom/internal/store/arango"
)

func ActivateBundle(ctx context.Context, client store.TransactionalRowQueryer, service dataset.ReleaseService, manifest dataset.Manifest, execution publication.BundleExecution) error {
	verifier := bundleVerifier{
		project: manifest.Dataset.Project, generation: manifest.Dataset.Generation,
		outputs:  make(map[dataset.DataframeSelector]dataset.PublicationVerification, len(execution.Outputs)),
		fallback: service.Verifier,
	}
	required := append([]dataset.DataframeSelector(nil), service.Required...)
	for _, output := range execution.Outputs {
		selector := execution.Selector(output.Name)
		verifier.outputs[selector] = execution.VerifyOutput(selector, output)
		required = append(required, selector)
	}
	service.Required, service.Verifier = required, verifier
	current, err := service.Active(ctx, manifest.Dataset.Project)
	if err != nil && !errors.Is(err, dataset.ErrNoActiveRelease) {
		return err
	}
	candidate, err := service.Create(ctx, dataset.ActivationRequest{
		Project: manifest.Dataset.Project, Generation: manifest.Dataset.Generation, ExpectedRevision: current.Revision,
	})
	if err != nil {
		return err
	}
	_, err = GoLive(ctx, client, Request{Release: candidate, ExpectedRevision: current.Revision})
	return err
}

type bundleVerifier struct {
	project    string
	generation string
	outputs    map[dataset.DataframeSelector]dataset.PublicationVerification
	fallback   dataset.PublicationVerifier
}

func (v bundleVerifier) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if project == v.project && generation == v.generation {
		if output, ok := v.outputs[selector]; ok {
			return output, nil
		}
	}
	if v.fallback == nil {
		return dataset.PublicationVerification{}, dataset.ErrPublicationRecordMissing
	}
	return v.fallback.VerifyPublication(ctx, project, generation, selector)
}
