import { createContext, useContext, type ReactNode } from 'react';
import type { ExplorerSummary } from '../../../contracts/explorerSummary';
import type { DraftTable } from '../model/authoring';

export interface BuilderToolbarSession {
  explorers: ReadonlyArray<ExplorerSummary>;
  selectedExplorerId: string;
  onExplorerChange: (explorerId: string) => void;
  onCreateExplorer: (name: string, fromDefault: boolean) => void;
  deleteSupported: boolean;
  deleteDisabled?: boolean;
  onDeleteExplorer: () => void;
  tables: ReadonlyArray<DraftTable>;
  selectedOutputId?: string;
  onSelectTable: (outputId: string) => void;
  onRenameTable: (outputId: string, title: string) => void;
  onNewTable: () => void;
  onDuplicateTable: () => void;
  onDeleteTable: () => void;
  onReview: () => void;
  reviewExpanded: boolean;
  onPublish: () => void;
  publishDisabled: boolean;
  publishing: boolean;
  busy?: boolean;
}

const BuilderToolbarContext = createContext<BuilderToolbarSession | undefined>(undefined);

export function BuilderToolbarSessionProvider({ value, children }: {
  readonly value: BuilderToolbarSession;
  readonly children: ReactNode;
}) {
  return <BuilderToolbarContext.Provider value={value}>{children}</BuilderToolbarContext.Provider>;
}

export function useBuilderToolbarSession<Selection>(select: (session: BuilderToolbarSession) => Selection): Selection {
  const session = useContext(BuilderToolbarContext);
  if (!session) throw new Error('Builder toolbar requires its Builder session.');
  return select(session);
}
