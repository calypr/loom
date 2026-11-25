import type { ExplorerBuilderDocument } from '../../../contracts/types';

type ImportedSourceStep = NonNullable<ExplorerBuilderDocument['importedSource']>['steps'][number];

export type SourceStepEntry = { readonly step: ImportedSourceStep; readonly number: number };
type SourceStepGroup = { readonly id: string; readonly columns: boolean; readonly entries: SourceStepEntry[] };

export const groupSourceSteps = (steps: ReadonlyArray<ImportedSourceStep>): SourceStepGroup[] => {
  const groups: SourceStepGroup[] = [];
  const columnKinds = new Set(['FIELD_GROUP', 'CATALOG_PROJECTION', 'DYNAMIC_COLUMN', 'EXTENSION_COLUMN', 'PIVOT']);
  for (const [index, step] of steps.entries()) {
    const columns = columnKinds.has(step.kind);
    const previous = groups.at(-1);
    if (columns && previous?.columns && previous.entries[0]?.step.parentId === step.parentId) {
      previous.entries.push({ step, number: index + 1 });
    } else {
      groups.push({ id: step.id, columns, entries: [{ step, number: index + 1 }] });
    }
  }
  return groups;
};
