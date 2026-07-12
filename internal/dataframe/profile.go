package dataframe

import (
	"context"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// ProfileCompiledQuery runs Arango's opt-in PROFILE operation for a compiled
// request. It is intentionally separate from normal dataframe execution so a
// frontend request never pays profiling overhead.
func ProfileCompiledQuery(ctx context.Context, opts arangostore.ConnectionOptions, compiled CompiledQuery, profileLevel int) (arangostore.ProfileResult, error) {
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return arangostore.ProfileResult{}, err
	}
	defer client.Close(ctx)
	return client.Profile(ctx, arangostore.ProfileRequest{
		Query:    compiled.Query,
		BindVars: compiled.BindVars,
		Options:  arangostore.ProfileOptions{Profile: profileLevel},
	})
}
