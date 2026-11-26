import { createContext, useContext, type ReactNode } from 'react';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument, ProjectedField } from '../../../contracts/types';

export interface ImportedSourceStepsSession {
  readonly outputId: string;
  readonly importedSource: ExplorerBuilderDocument['importedSource'];
  readonly disabled?: boolean;
  readonly catalogOutputsForStep?: (stepId: string) => ReadonlyArray<{ readonly outputKey: string; readonly columnId?: string; readonly defaultName: string; readonly enabled: boolean }>;
  readonly pivotOutputsForStep?: (stepId: string) => ReadonlyArray<{ readonly outputKey: string; readonly columnId?: string; readonly defaultName: string; readonly enabled: boolean }>;
  readonly projectedFieldsForStep?: (stepId: string) => ReadonlyArray<ProjectedField>;
  readonly onRemoveProjectedField?: (field: ProjectedField) => void;
  readonly canRemoveProjectedField?: (field: ProjectedField) => boolean;
  readonly onEditProjectedField?: (field: ProjectedField) => void;
  readonly onRenameProjectedField?: (columnId: string, label: string) => void;
  readonly editableProjectedFieldIds?: ReadonlySet<string>;
  readonly projectedFieldRemovalDisabledReason?: string;
  readonly outputRevision?: string;
  readonly resourceTypeChoices?: ReadonlyArray<string>;
  readonly sourcePathChoices?: ReadonlyArray<string>;
  readonly fieldPathChoices?: ReadonlyArray<string>;
  readonly fieldKindChoices?: ReadonlyArray<string>;
  readonly occurrenceForStep?: (stepId: string) => string | undefined;
  readonly onSuspendEdit?: () => boolean;
  readonly onBeginEdit?: (stepId: string) => boolean;
  readonly onActiveOperationChange?: (stepId: string) => void;
  readonly onCancelEdit?: () => void;
  readonly onDraftChange?: () => void;
  readonly onRefresh: () => Promise<boolean>;
  readonly onApply: (command: ExplorerBuilderCommand) => Promise<boolean>;
}

const ImportedSourceStepsContext = createContext<ImportedSourceStepsSession | undefined>(undefined);

export function ImportedSourceStepsSessionProvider({ value, children }: {
  readonly value: ImportedSourceStepsSession;
  readonly children: ReactNode;
}) {
  return <ImportedSourceStepsContext.Provider value={value}>{children}</ImportedSourceStepsContext.Provider>;
}

export function useImportedSourceStepsSession<Selection>(select: (state: ImportedSourceStepsSession) => Selection): Selection {
  const session = useContext(ImportedSourceStepsContext);
  if (!session) throw new Error('ImportedSourceSteps requires its Builder session.');
  return select(session);
}
