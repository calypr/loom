import { useMemo, useState } from 'react';
import { useGetExplorerAuthoringExplorersQuery, useLoomClient } from '../../../react';
import { canonicalProject } from '../../../client/api';

export const builderOwnerKeyFor = (
  project: string,
  authResourcePath: string | undefined,
  explorerId: string,
): string => JSON.stringify([
  canonicalProject(project),
  authResourcePath?.trim() ?? '',
  explorerId,
]);

export function useBuilderWorkspaceScope({ organization, project, explorerId }: {
  readonly organization?: string;
  readonly project: string;
  readonly explorerId?: string;
}) {
  const loomClient = useLoomClient();
  const projectId = organization ? `${organization}/${project}` : project;
  const authResourcePath = organization
    ? `/programs/${organization}/projects/${project}`
    : undefined;
  const [selectedExplorerId, setSelectedExplorerId] = useState(
    !explorerId || explorerId === 'default' ? 'dataframe-default' : explorerId,
  );
  const explorers = useGetExplorerAuthoringExplorersQuery({
    project: projectId,
    authResourcePath,
  });
  const effectiveExplorerId =
    selectedExplorerId && selectedExplorerId !== 'default'
      ? selectedExplorerId
      : 'dataframe-default';
  const explorerSummaries = useMemo(() => {
    const available = (explorers.data ?? []).filter(
      (candidate) =>
        candidate.explorerId === 'dataframe-default' ||
        (candidate.explorerId !== 'default' && candidate.management?.toUpperCase() !== 'REPOSITORY'),
    );
    if (available.some((candidate) => candidate.explorerId === 'dataframe-default')) return available;
    return [{
      project: projectId,
      explorerId: 'dataframe-default',
      title: 'Shared default dataframe',
      management: 'SYSTEM',
      updatedAt: '',
    }, ...available];
  }, [explorers.data, projectId]);
  const ownerKey = builderOwnerKeyFor(projectId, authResourcePath, effectiveExplorerId);
  return {
    loomClient,
    projectId,
    authResourcePath,
    selectedExplorerId,
    setSelectedExplorerId,
    explorers,
    effectiveExplorerId,
    explorerSummaries,
    ownerKey,
  };
}
