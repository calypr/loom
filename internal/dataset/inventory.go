package dataset

import (
	"context"
	"sort"
)

type ExecutionProjectSource interface {
	ListExecutionProjects(context.Context) ([]string, error)
}

type ProjectInventory struct {
	Snapshots  SnapshotRepository
	Releases   ReleaseRepository
	Executions ExecutionProjectSource
}

// ExpectedProjects returns only caller-authorized identities in auth mode. In
// no-auth development mode it exposes every project observed through snapshot
// sessions, releases, or publication attempts, including projects that never
// reached successful publication.
func (i ProjectInventory) ExpectedProjects(ctx context.Context, authorized []string, noAuth bool) ([]string, error) {
	if !noAuth {
		set := make(map[string]struct{}, len(authorized))
		for _, project := range authorized {
			if project != "" {
				set[project] = struct{}{}
			}
		}
		return sortedSet(set), nil
	}
	set := make(map[string]struct{})
	collect := func(values []string, err error) error {
		if err != nil {
			return err
		}
		for _, value := range values {
			if value != "" {
				set[value] = struct{}{}
			}
		}
		return nil
	}
	if i.Snapshots != nil {
		if err := collect(i.Snapshots.ListSnapshotProjects(ctx)); err != nil {
			return nil, err
		}
	}
	if i.Releases != nil {
		if err := collect(i.Releases.ListReleaseProjects(ctx)); err != nil {
			return nil, err
		}
	}
	if i.Executions != nil {
		if err := collect(i.Executions.ListExecutionProjects(ctx)); err != nil {
			return nil, err
		}
	}
	result := sortedSet(set)
	sort.Strings(result)
	return result, nil
}
