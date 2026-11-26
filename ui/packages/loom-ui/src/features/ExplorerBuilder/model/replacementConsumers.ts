import type { RowChangeAssessment } from '../../../contracts/types';
import type { DraftTable } from './authoring';

const columnLabel = (table: DraftTable | undefined, columnId: string): string =>
  table?.document.columns.find((column) => column.columnId === columnId)?.label ??
  table?.document.columns.find((column) => column.column === columnId)?.label ?? columnId;

type ReplacementConsumerPresentation = {
  readonly key: string;
  readonly title: string;
  readonly detail: string;
};

export const replacementConsumerPresentations = (
  workspaceTables: ReadonlyArray<DraftTable>,
  replacementOutputId: string,
  references: NonNullable<RowChangeAssessment['workspaceOutputConsumers']>,
): ReadonlyArray<ReplacementConsumerPresentation> => references.map((reference) => {
  const consumer = workspaceTables.find((candidate) => candidate.outputId === reference.outputId);
  const step = consumer?.document.construction?.steps.find((candidate) => candidate.id === reference.stepId);
  const input = step?.inputs[reference.inputIndex];
  const targetInput = input?.kind === 'WORKSPACE_OUTPUT' && input.outputId === replacementOutputId;
  const title = consumer?.title ?? 'Another table';
  const fallbackDetail = step?.operation.kind === 'COMBINE'
    ? step.operation.combine.kind === 'KEY_JOIN' ? 'Uses this table in a join'
      : step.operation.combine.kind === 'APPEND' ? 'Uses this table in an append'
        : 'Uses this table in a membership check'
    : 'Uses this table in a construction step';
  if (!targetInput || !step || step.operation.kind !== 'COMBINE') {
    return { key: `${reference.outputId}:${reference.stepId}:${reference.inputIndex}`, title, detail: fallbackDetail };
  }

  const columnIds = new Set<string>();
  for (const projection of step.operation.combine.projections) {
    if (projection.inputIndex === reference.inputIndex) columnIds.add(projection.inputColumnId);
  }
  for (const key of step.operation.combine.keys ?? []) {
    const keyColumnId = reference.inputIndex === 0
      ? key.leftColumnId
      : reference.inputIndex === 1 ? key.rightColumnId : undefined;
    if (keyColumnId) columnIds.add(keyColumnId);
  }
  if (columnIds.size === 0) return { key: `${reference.outputId}:${reference.stepId}:${reference.inputIndex}`, title, detail: fallbackDetail };
  const labels = [...columnIds].map((columnId) => columnLabel(
    workspaceTables.find((candidate) => candidate.outputId === replacementOutputId), columnId,
  ));
  const operationLabel = step.operation.combine.kind === 'KEY_JOIN'
    ? 'Join fields'
    : step.operation.combine.kind === 'APPEND' ? 'Append fields' : 'Membership fields';
  return {
    key: `${reference.outputId}:${reference.stepId}:${reference.inputIndex}`,
    title,
    detail: `${operationLabel}: ${labels.join(', ')}`,
  };
});
