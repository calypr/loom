import React, { useMemo } from 'react';
import { createLoomClient, type LoomClient } from './api';
import BuilderWorkspace from './features/ExplorerBuilder/BuilderWorkspace';
import { LoomProvider } from './react';

export interface LoomExplorerBuilderProps {
  readonly project: string;
  readonly explorerId?: string;
  readonly client?: LoomClient;
  /** Optional legacy project split used by Calypr route wrappers. */
  readonly organization?: string;
  readonly onExplorerChange?: (explorerId: string) => void;
  readonly className?: string;
}

export const LoomExplorerBuilder = ({
  project,
  explorerId,
  client,
  organization,
  onExplorerChange,
  className,
}: LoomExplorerBuilderProps) => {
  const ownedClient = useMemo(() => client ?? createLoomClient(), [client]);
  return (
    <LoomProvider client={ownedClient}>
      <div className={['loom-ui-root', className].filter(Boolean).join(' ')}>
        <BuilderWorkspace
          organization={organization}
          project={project}
          explorerId={explorerId}
          onExplorerChange={onExplorerChange}
        />
      </div>
    </LoomProvider>
  );
};

export default LoomExplorerBuilder;
