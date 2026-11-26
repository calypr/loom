export type ConstructionOperationFamily =
  | 'ADD_COLUMNS'
  | 'KEEP_ROWS'
  | 'CALCULATE'
  | 'RESHAPE'
  | 'COMBINE';

export const constructionOperationFamilies = [
  {
    family: 'ADD_COLUMNS',
    label: 'Add columns',
    description: 'Bring more information into each row.',
  },
  {
    family: 'KEEP_ROWS',
    label: 'Filter rows',
    description: 'Choose which rows appear in the table output.',
  },
  {
    family: 'CALCULATE',
    label: 'Calculate',
    description: 'Create a value from existing columns.',
  },
  {
    family: 'RESHAPE',
    label: 'Pivot / reshape',
    description: 'Pivot categories into columns, unpivot columns into rows, or group rows.',
  },
  {
    family: 'COMBINE',
    label: 'Combine',
    description: 'Create a new table by joining or stacking table versions.',
  },
] satisfies ReadonlyArray<{
  readonly family: ConstructionOperationFamily;
  readonly label: string;
  readonly description: string;
}>;
