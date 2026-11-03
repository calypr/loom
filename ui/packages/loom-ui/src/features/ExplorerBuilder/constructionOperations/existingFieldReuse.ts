// ROW-SOURCE-07: when a signed source choice's authoritative field is already
// projected as a table column, the pickers should offer that existing column
// instead of inviting a duplicate signed choice that the server would refuse
// on the named collision. Identity is proven only through the construction's
// server-owned sourceProjections (columnId ↔ fieldPath); labels are never
// matched, saved selections are never rewritten, and an ambiguous or stale
// projection falls back to the current signed-choice flow untouched.

import type { Construction } from '../../../types';

export type ExistingColumnReuse = {
  readonly columnId: string;
  readonly label: string;
  readonly fieldPath: string;
};

type ReusableColumn = {
  readonly id: string;
  readonly label: string;
};

// Returns the single existing column that already carries this exact field
// path, or undefined when reuse is not unambiguously safe: the construction
// listed the field zero times (plain signed choice), the projected column is
// no longer on this stage (stale: column removed or step pruned), or more
// than one column carries the same field path (independent private ownership
// must stay distinct, so no silent pick).
export const existingColumnForFieldPath = (args: {
  readonly sourceProjections: Construction['sourceProjections'];
  readonly columns: ReadonlyArray<ReusableColumn>;
  readonly fieldPath: string;
}): ExistingColumnReuse | undefined => {
  const projections = args.sourceProjections ?? [];
  const matching = projections.filter((projection) => projection.fieldPath === args.fieldPath);
  const usable = matching.filter((projection) =>
    args.columns.some((column) => column.id === projection.columnId));
  if (usable.length !== 1) return undefined;
  const column = args.columns.find((candidate) => candidate.id === usable[0]!.columnId);
  if (column === undefined) return undefined;
  return { columnId: column.id, label: column.label, fieldPath: args.fieldPath };
};

export type ReusableChoice<T> = {
  readonly choice: T;
  readonly reuse: ExistingColumnReuse | undefined;
};

// Partitions picker choices into what each one should render as: the existing
// column when reuse applies, or the signed choice otherwise. Pure and total —
// callers keep their current ordering and filtering around it.
export const partitionChoicesForReuse = <T extends { readonly fieldPath: string }>(args: {
  readonly choices: ReadonlyArray<T>;
  readonly sourceProjections: Construction['sourceProjections'];
  readonly columns: ReadonlyArray<ReusableColumn>;
}): ReadonlyArray<ReusableChoice<T>> =>
  args.choices.map((choice) => ({
    choice,
    reuse: existingColumnForFieldPath({
      sourceProjections: args.sourceProjections,
      columns: args.columns,
      fieldPath: choice.fieldPath,
    }),
  }));
