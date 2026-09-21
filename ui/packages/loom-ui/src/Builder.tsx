import React, { useEffect, useMemo, useState } from 'react';
import { createLoomClient, type LoomClient } from './api';
import type { SelectionRevision } from './selection';
import BuilderWorkspace from './features/ExplorerBuilder/BuilderWorkspace';
import { LoomProvider } from './react';

type PopulationSelectionLoadState =
  | { readonly requestId?: undefined; readonly status: 'idle' }
  | { readonly requestId: string; readonly status: 'loading' }
  | {
      readonly requestId: string;
      readonly status: 'ready';
      readonly selection: SelectionRevision;
    }
  | {
      readonly requestId: string;
      readonly status: 'error';
      readonly message: string;
    };

const idlePopulationSelection: PopulationSelectionLoadState = {
  status: 'idle',
};

const populationSelectionForRequest = (
  state: PopulationSelectionLoadState,
  requestId: string | undefined,
): PopulationSelectionLoadState => {
  if (!requestId) return idlePopulationSelection;
  if (state.requestId === requestId) return state;
  return { requestId, status: 'loading' };
};

export interface LoomExplorerBuilderProps {
  readonly project: string;
  readonly explorerId?: string;
  /** Completed immutable selection supplied by the enclosing project explorer. */
  readonly selectionRevisionId?: string;
  readonly client?: LoomClient;
  /** Optional legacy project split used by Calypr route wrappers. */
  readonly organization?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly className?: string;
  /** Feature selected from a Viewer explanation for focused repair. */
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
  featureFocus,
}: LoomExplorerBuilderProps) => {
  const ownedClient = useMemo(() => client ?? createLoomClient(), [client]);
  const [populationSelectionLoad, setPopulationSelectionLoad] =
    useState<PopulationSelectionLoadState>(() =>
      selectionRevisionId
        ? { requestId: selectionRevisionId, status: 'loading' }
        : idlePopulationSelection,
    );
  const currentPopulationSelection = populationSelectionForRequest(
    populationSelectionLoad,
    selectionRevisionId,
  );
  useEffect(() => {
    if (!selectionRevisionId) {
      setPopulationSelectionLoad(idlePopulationSelection);
      return;
    }
    const controller = new AbortController();
    setPopulationSelectionLoad({
      requestId: selectionRevisionId,
      status: 'loading',
    });
    const projectId = organization ? `${organization}/${project}` : project;
    void ownedClient.getSelection({
      project: projectId,
      explorerId: explorerId || 'default',
      selectionRevision: selectionRevisionId,
      limit: 1,
      authResourcePath: organization ? `/programs/${organization}/projects/${project}` : undefined,
    }, controller.signal).then(
      (page) => {
        if (!controller.signal.aborted) {
          setPopulationSelectionLoad({
            requestId: selectionRevisionId,
            status: 'ready',
            selection: page.revision,
          });
        }
      },
      (error: unknown) => {
        if (!controller.signal.aborted) {
          setPopulationSelectionLoad({
            requestId: selectionRevisionId,
            status: 'error',
            message: error instanceof Error
              ? error.message
              : 'Loom could not load the saved selection.',
          });
        }
      },
    );
    return () => controller.abort();
  }, [explorerId, organization, ownedClient, project, selectionRevisionId]);
  return (
    <LoomProvider client={ownedClient}>
      <div className={['loom-ui-root', className].filter(Boolean).join(' ')}>
        <BuilderWorkspace
          organization={organization}
          project={project}
          explorerId={explorerId}
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
        />
      </div>
    </LoomProvider>
  );
};

export default LoomExplorerBuilder;
