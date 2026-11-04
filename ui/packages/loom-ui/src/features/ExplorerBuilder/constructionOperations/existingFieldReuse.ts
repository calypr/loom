// ROW-SOURCE-07: when a signed source choice's authoritative field is already
// projected as a table column, the pickers should offer that existing column
// instead of inviting a duplicate signed choice that the server would refuse
// on the named collision. Identity is proven only through server-owned state:
// the construction's sourceProjections (columnId ↔ fieldPath) for private
// source lanes, and the caller-supplied public carriers for ordinary PUBLIC
// columns, whose identity is authored as authoringv2 Column.Source.Field and
// compiled separately from sourceProjections. Labels are never matched, saved
// selections are never rewritten, and an ambiguous or stale identity falls
// back to the current signed-choice flow untouched.

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

export type PublicFieldCarrier = {
  readonly columnId: string;
  readonly fieldPath: string;
};

// Mirrors the server's canonicalRowPath: the root occurrence is implicit, so a
// "root."-prefixed authored path names the same field as the bare path.
const canonicalFieldPath = (fieldPath: string): string => fieldPath.trim().replace(/^root\./, '');

// Returns the single existing column that already carries this exact field
// path, or undefined when reuse is not unambiguously safe: the field is carried
// zero times (plain signed choice), a carrier column is no longer on this stage
// (stale: column removed or step pruned), or more than one live column carries
// the same field path (independent private ownership must stay distinct, so no
// silent pick). Public carriers are caller-supplied server-signed identity for
// ordinary public columns, which never appear in sourceProjections; without
// them a public column stays invisible to reuse.
export const existingColumnForFieldPath = (args: {
  readonly sourceProjections: Construction['sourceProjections'];
  readonly columns: ReadonlyArray<ReusableColumn>;
  readonly fieldPath: string;
  readonly publicCarriers?: ReadonlyArray<PublicFieldCarrier>;
}): ExistingColumnReuse | undefined => {
  const isLive = (columnId: string): boolean =>
    columnId.trim() !== '' && args.columns.some((column) => column.id === columnId);
  const privateCarriers = (args.sourceProjections ?? [])
    .filter((projection) => projection.fieldPath === args.fieldPath && isLive(projection.columnId));
  const wanted = canonicalFieldPath(args.fieldPath);
  const publicCarriers = (args.publicCarriers ?? [])
    .filter((carrier) => canonicalFieldPath(carrier.fieldPath) === wanted && isLive(carrier.columnId));
  if (privateCarriers.length > 1 || publicCarriers.length > 1) return undefined;
  const privateColumnId = privateCarriers[0]?.columnId;
  const publicColumnId = publicCarriers[0]?.columnId;
  if (privateColumnId !== undefined && publicColumnId !== undefined && privateColumnId !== publicColumnId) return undefined;
  const columnId = privateColumnId ?? publicColumnId;
  if (columnId === undefined) return undefined;
  const column = args.columns.find((candidate) => candidate.id === columnId);
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
  readonly publicCarriers?: ReadonlyArray<PublicFieldCarrier>;
}): ReadonlyArray<ReusableChoice<T>> =>
  args.choices.map((choice) => ({
    choice,
    reuse: existingColumnForFieldPath({
      sourceProjections: args.sourceProjections,
      columns: args.columns,
      fieldPath: choice.fieldPath,
      ...(args.publicCarriers ? { publicCarriers: args.publicCarriers } : {}),
    }),
  }));
