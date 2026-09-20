import type {
  ConstructionRouteStep,
  ExplorerBuilderWorkspace,
} from '../../types';

type SavedPopulationRoute = NonNullable<
  ExplorerBuilderWorkspace['documents'][number]['population']
>['route'];

export const sameConstructionRoute = (
  left: ReadonlyArray<ConstructionRouteStep>,
  right: ReadonlyArray<ConstructionRouteStep>,
): boolean =>
  left.length === right.length &&
  left.every((step, index) => {
    const candidate = right[index];
    return candidate !== undefined &&
      step.edgeId === candidate.edgeId &&
      step.fromNodeId === candidate.fromNodeId &&
      step.toNodeId === candidate.toNodeId &&
      step.fromResourceType === candidate.fromResourceType &&
      step.toResourceType === candidate.toResourceType &&
      step.relationship === candidate.relationship &&
      step.storageDirection === candidate.storageDirection &&
      step.matchMode === candidate.matchMode;
  });

export const matchesSavedPopulationRoute = (
  route: ReadonlyArray<ConstructionRouteStep>,
  saved: SavedPopulationRoute,
): boolean =>
  route.length === saved.length &&
  route.every((step, index) => {
    const candidate = saved[index];
    return candidate !== undefined &&
      step.toResourceType === candidate.resourceType &&
      step.relationship === candidate.relationship;
  });
