import React, { useMemo } from 'react';
import { createLoomClient, type LoomClient } from './api';
import BuilderWorkspace from './features/ExplorerBuilder/BuilderWorkspace';
import { LoomProvider, useQuery } from './react';

export interface LoomExplorerBuilderProps {
  readonly project: string;
  readonly explorerId?: string;

  readonly selectionRevisionId?: string;
  readonly client?: LoomClient;

  readonly organization?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly className?: string;

  readonly workspaceComposition?: React.ComponentProps<typeof BuilderWorkspace>['workspaceComposition'];

  readonly featureFocus?: LoomBuilderFeatureFocus;
}

export interface LoomBuilderFeatureFocus {
  readonly outputId: string;
  readonly column: string;
  readonly label?: string;
}

export const LoomExplorerBuilder = ({
  project,
  explorerId,
  selectionRevisionId,
  client,
  organization,
  onExplorerChange,
  className,
  workspaceComposition,
  featureFocus,
}: LoomExplorerBuilderProps) => {
  const ownedClient = useMemo(() => client ?? createLoomClient(), [client]);
  const populationSelectionQuery = useQuery(
    (signal) => {
      if (!selectionRevisionId) throw new Error('No saved selection was requested.');
      const projectId = organization ? `${organization}/${project}` : project;
      return ownedClient.getSelection({
        project: projectId,
        explorerId: !explorerId || explorerId === 'default' ? 'dataframe-default' : explorerId,
        selectionRevision: selectionRevisionId,
        limit: 1,
        authResourcePath: organization ? `/programs/${organization}/projects/${project}` : undefined,
      }, signal).then((page) => page.revision);
    },
    [explorerId, organization, ownedClient, project, selectionRevisionId],
    Boolean(selectionRevisionId),
  );
  const currentPopulationSelection = !selectionRevisionId
    ? { status: 'idle' }
    : populationSelectionQuery.error
      ? {
          requestId: selectionRevisionId,
          status: 'error',
          message: populationSelectionQuery.error instanceof Error
            ? populationSelectionQuery.error.message
            : 'Loom could not load the saved selection.',
        }
      : populationSelectionQuery.data
        ? { requestId: selectionRevisionId, status: 'ready', selection: populationSelectionQuery.data }
        : { requestId: selectionRevisionId, status: 'loading' };
  return (
    <LoomProvider client={ownedClient}>
      <div className={['loom-ui-root', className].filter(Boolean).join(' ')}>
        <BuilderWorkspace
          organization={organization}
          project={project}
          explorerId={!explorerId || explorerId === 'default' ? 'dataframe-default' : explorerId}
          populationSelection={
            currentPopulationSelection.status === 'ready'
              ? currentPopulationSelection.selection
              : undefined
          }
          populationSelectionLoading={
            currentPopulationSelection.status === 'loading'
          }
          populationSelectionError={
            currentPopulationSelection.status === 'error'
              ? currentPopulationSelection.message
              : undefined
          }
          onExplorerChange={onExplorerChange}
          featureFocus={featureFocus}
          workspaceComposition={workspaceComposition}
        />
      </div>
    </LoomProvider>
  );
};

export default LoomExplorerBuilder;
