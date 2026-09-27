import type {
  ConstructionRouteStep,
  ExplorerBuilderWorkspace,
  PopulationRouteChoice,
} from '../../types';

type SavedPopulationRoute = NonNullable<
  ExplorerBuilderWorkspace['documents'][number]['population']
>['route'];

type PopulationRouteOption = {
  readonly choice: PopulationRouteChoice;
  readonly label: string;
  readonly isDirectSameResource: boolean;
};

export const populationRouteOptions = ({
  choices,
  selectionResourceType,
  rootResourceType,
}: {
  readonly choices: ReadonlyArray<PopulationRouteChoice>;
  readonly selectionResourceType?: string;
  readonly rootResourceType: string;
}): ReadonlyArray<PopulationRouteOption> => {
  const isDirectSameResource = (choice: PopulationRouteChoice) =>
    choice.route.length === 0 && selectionResourceType === rootResourceType;
  const orderedChoices = [
    ...choices.filter(isDirectSameResource),
    ...choices.filter((choice) => !isDirectSameResource(choice)),
  ];
  const seenRoutes = new Set<string>();
  const uniqueChoices = orderedChoices.filter((choice) => {
    const signature = JSON.stringify(choice.route.map((step) => [
      step.fromResourceType,
      step.toResourceType,
      step.relationship,
      step.storageDirection,
      step.matchMode,
    ]));
    if (seenRoutes.has(signature)) return false;
    seenRoutes.add(signature);
    return true;
  });
  const summaryCounts = new Map<string, number>();
  uniqueChoices.forEach((choice) => {
    summaryCounts.set(
      choice.presentation.summary,
      (summaryCounts.get(choice.presentation.summary) ?? 0) + 1,
    );
  });

  return uniqueChoices.map((choice) => {
    const direct = isDirectSameResource(choice);
    const label = direct || summaryCounts.get(choice.presentation.summary) === 1
      ? choice.presentation.summary
      : choice.route.map((step) => {
        const direction = step.storageDirection === 'OUTBOUND' ? 'outgoing' : 'incoming';
        const requirement = step.matchMode === 'REQUIRED' ? 'required' : 'when available';
        return `${step.fromResourceType} → ${step.toResourceType} via ${step.relationship} (${direction}, ${requirement})`;
      }).join(' then ');
    return { choice, label, isDirectSameResource: direct };
  });
};

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
