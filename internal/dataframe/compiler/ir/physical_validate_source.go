package ir

import (
	"fmt"
)

func (validation *physicalOperationValidation) validateRootScan(i int, operation PhysicalOperation) error {
	p := validation.plan
	if hint := operation.RootScan.IndexHint; hint != "" && (!physicalPathPartPattern.MatchString(hint) || hint[0] == '_') {
		return fmt.Errorf("operation %d: unsafe root index hint %q", i, hint)
	}
	if operation.RootScan.IndexHint != "" && (operation.RootScan.Population != nil || operation.RootScan.CohortSource != nil || operation.RootScan.PageCandidateSeed != nil) {
		return fmt.Errorf("operation %d: root index hint requires a direct scan", i)
	}
	validation.rootScans++
	if validation.rootScans > 1 {
		return fmt.Errorf("operation %d: physical plan has multiple root scans", i)
	}
	if err := requireBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if err := requireCollectionBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if operation.RootScan.Population != nil {
		if err := validatePhysicalPopulationRootSource(*operation.RootScan.Population, p.BindVars); err != nil {
			return fmt.Errorf("operation %d population root source: %w", i, err)
		}
	}
	if operation.RootScan.CohortSource != nil {
		if operation.RootScan.Population != nil {
			return fmt.Errorf("operation %d cannot combine population and cohort root sources", i)
		}
		if err := validatePhysicalCohortRootSource(*operation.RootScan.CohortSource, p.BindVars); err != nil {
			return fmt.Errorf("operation %d cohort root source: %w", i, err)
		}
	}
	if seed := operation.RootScan.PageCandidateSeed; seed != nil {
		if operation.RootScan.Population != nil || operation.RootScan.CohortSource != nil {
			return fmt.Errorf("operation %d root page candidate seed cannot be combined with population or cohort root sources", i)
		}
		if err := validatePhysicalUncorrelatedSubplan(seed.Subplan, p.BindVars); err != nil {
			return fmt.Errorf("operation %d root page candidate seed: %w", i, err)
		}
	}
	if err := definePhysicalVariable(validation.defined, operation.RootScan.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if population := operation.RootScan.Population; population != nil && population.CollectMembersVariable != "" {
		if err := definePhysicalVariable(validation.defined, population.CollectMembersVariable); err != nil {
			return fmt.Errorf("operation %d population member collection: %w", i, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateTraversal(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validateAndDefinePhysicalTraversal(*operation.Traversal, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validatePathSeed(i int, operation PhysicalOperation) error {
	p := validation.plan
	seed := operation.PathSeed
	if err := validatePhysicalPathNode(seed.Node, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d path seed: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, seed.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validatePathExtend(i int, operation PhysicalOperation) error {
	p := validation.plan
	extend := operation.PathExtend
	if err := validatePhysicalPathExtend(*extend, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d path extend: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, extend.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}
