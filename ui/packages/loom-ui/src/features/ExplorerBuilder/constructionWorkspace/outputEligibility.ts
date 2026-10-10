import type { ExplorerBuilderDocument } from '../../../types';

export interface EffectiveOutputAvailability {
  /** Preserve the existing publish guard for authored columns, including hidden ones. */
  readonly hasAnyOutputColumn: boolean;
  /** Whether the current row-producing stage has a column available to preview. */
  readonly hasVisibleOutputColumn: boolean;
}

const hasVisibleSourceColumn = (document: ExplorerBuilderDocument): boolean =>
  document.columns.some((column) => column.table?.visible ?? Boolean(column.table));

/**
 * Computes the output-column gates from the authored source and terminal stage.
 * A terminal construction may provide the first usable columns for an otherwise
 * empty table (for example, a Combine step). An explicit terminal cohort keeps
 * the source-column gate used by PreviewTable's cohort projection.
 */
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
    // Existing publication eligibility accepts authored columns regardless of
    // their presentation visibility. Also count terminal outputs when a
    // row-shaping construction starts from a zero-column table.
    hasAnyOutputColumn: document.columns.length > 0 || finalStep.outputs.length > 0,
    hasVisibleOutputColumn: hasVisibleConstructionOutput || sourceOutputs.some(
      (column) => column.table?.visible ?? Boolean(column.table),
    ),
  };
};
