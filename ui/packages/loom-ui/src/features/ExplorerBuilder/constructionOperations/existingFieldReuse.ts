
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

const canonicalFieldPath = (fieldPath: string): string => fieldPath.trim().replace(/^root\./, '');

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
