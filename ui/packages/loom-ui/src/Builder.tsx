import React, { useEffect, useMemo, useState } from 'react';
import { createLoomClient, type LoomClient } from './api';
import type { SelectionRevision } from './selection';
import BuilderWorkspace from './features/ExplorerBuilder/BuilderWorkspace';
import { LoomProvider } from './react';

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
  const [populationSelection, setPopulationSelection] = useState<SelectionRevision>();
  const [populationSelectionError, setPopulationSelectionError] = useState<string>();
  const [populationSelectionLoading, setPopulationSelectionLoading] = useState(false);
  useEffect(() => {
    if (!selectionRevisionId) {
      setPopulationSelection(undefined);
      setPopulationSelectionError(undefined);
      return;
    }
    const controller = new AbortController();
    setPopulationSelection(undefined);
    setPopulationSelectionLoading(true);
    setPopulationSelectionError(undefined);
    const projectId = organization ? `${organization}/${project}` : project;
    void ownedClient.getSelection({
      project: projectId,
      explorerId: explorerId || 'default',
      selectionRevision: selectionRevisionId,
      limit: 1,
      authResourcePath: organization ? `/programs/${organization}/projects/${project}` : undefined,
    }, controller.signal).then(
      (page) => setPopulationSelection(page.revision),
      (error: unknown) => {
        if (!controller.signal.aborted) setPopulationSelectionError(error instanceof Error ? error.message : 'Loom could not load the saved selection.');
      },
    ).finally(() => {
      if (!controller.signal.aborted) setPopulationSelectionLoading(false);
    });
    return () => controller.abort();
  }, [explorerId, organization, ownedClient, project, selectionRevisionId]);
  return (
    <LoomProvider client={ownedClient}>
      <div className={['loom-ui-root', className].filter(Boolean).join(' ')}>
        <BuilderWorkspace
          organization={organization}
          project={project}
          explorerId={explorerId}
          populationSelection={populationSelection}
          populationSelectionLoading={populationSelectionLoading}
          populationSelectionError={populationSelectionError}
          onExplorerChange={onExplorerChange}
          featureFocus={featureFocus}
        />
      </div>
    </LoomProvider>
  );
};

export default LoomExplorerBuilder;
