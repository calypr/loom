import type { ExplorerBuilderDocument } from '../../../types';

export interface EffectiveOutputAvailability {
  readonly hasAnyOutputColumn: boolean;
  readonly hasVisibleOutputColumn: boolean;
}

const hasVisibleSourceColumn = (document: ExplorerBuilderDocument): boolean =>
  document.columns.some((column) => column.table?.visible ?? Boolean(column.table));

export const effectiveOutputAvailability = (
  document: ExplorerBuilderDocument,
): EffectiveOutputAvailability => {
  const finalStep = document.construction?.steps.at(-1);
  const groups = document.rows.kind === 'GROUPS' && document.rows.groups.source.kind === 'EXPLICIT'
    ? document.rows.groups
    : undefined;
  const terminalCohort = Boolean(groups && (
    !finalStep
      ? (document.construction?.steps.length ?? 0) === 0
      : groups.afterStepId === finalStep.id
  ));

  if (!finalStep || terminalCohort) {
    return {
      hasAnyOutputColumn: document.columns.length > 0,
      hasVisibleOutputColumn: hasVisibleSourceColumn(document),
    };
  }

  const authoredByName = new Map(document.columns.map((column) => [column.column, column]));
  const rowValueOutputIds = new Set((finalStep.rowValues ?? []).map((value) => value.outputColumnId));
  const constructionOutputs = finalStep.outputs.filter(
    (output) => !authoredByName.has(output.name) || rowValueOutputIds.has(output.id),
  );
  const constructionOutputNames = new Set(constructionOutputs.map((output) => output.name));
  const finalOutputNames = new Set(finalStep.outputs.map((output) => output.name));
  const sourceOutputs = document.columns.filter(
    (column) => finalOutputNames.has(column.column) && !constructionOutputNames.has(column.column),
  );
  const hasVisibleConstructionOutput = constructionOutputs.some((output) => {
    if (output.table?.visible !== undefined) return output.table.visible;
    const authored = authoredByName.get(output.name);
    return authored
      ? authored.table?.visible ?? Boolean(authored.table)
      : true;
  });

  return {
    hasAnyOutputColumn: document.columns.length > 0 || finalStep.outputs.length > 0,
    hasVisibleOutputColumn: hasVisibleConstructionOutput || sourceOutputs.some(
      (column) => column.table?.visible ?? Boolean(column.table),
    ),
  };
};
