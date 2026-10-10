export const CONSTRUCTION_OPERATION_FAMILIES = [
  'ADD_COLUMNS',
  'KEEP_ROWS',
  'CALCULATE',
  'RESHAPE',
  'COMBINE',
] as const;

export type ConstructionOperationFamily = (typeof CONSTRUCTION_OPERATION_FAMILIES)[number];

export type ConstructionOperationIntent =
  | { readonly family: 'ADD_COLUMNS'; readonly intent: 'FIND_INFORMATION' | 'SUMMARIZE_RELATED' | 'REUSE_CALCULATION' }
  | { readonly family: 'KEEP_ROWS'; readonly intent: 'MATCH_CONDITIONS' | 'MATCH_RELATED' | 'REMOVE_DUPLICATES' | 'KEEP_RANKED' }
  | { readonly family: 'CALCULATE'; readonly intent: 'CALCULATE_VALUE' | 'SET_BY_CONDITION' | 'RECODE' | 'HANDLE_MISSING' | 'CALCULATE_ACROSS_ROWS' }
  | { readonly family: 'RESHAPE'; readonly intent: 'SUMMARIZE_GROUPS' | 'PIVOT' | 'UNPIVOT' | 'EXPAND_REPEATED' }
  | { readonly family: 'COMBINE'; readonly intent: 'MATCH_COLUMNS' | 'APPEND_ROWS' | 'COMPARE_MEMBERSHIP' | 'MAKE_COMBINATIONS' };

export type OperationAvailability =
  | { readonly kind: 'supported' }
  | { readonly kind: 'loading'; readonly message: string }
  | { readonly kind: 'unknown'; readonly message: string }
  | { readonly kind: 'unavailable'; readonly reason: string };

export interface OperationIntention<Intent extends ConstructionOperationIntent = ConstructionOperationIntent> {
  readonly value: Intent;
  readonly label: string;
  readonly description: string;
  readonly availability: OperationAvailability;
}

export const familyPresentation = (family: ConstructionOperationFamily): {
  readonly title: string;
  readonly introduction: string;
} => {
  switch (family) {
    case 'ADD_COLUMNS':
      return { title: 'Add columns', introduction: 'Bring more information into each row.' };
    case 'KEEP_ROWS':
      return { title: 'Filter rows', introduction: 'Choose which rows appear in the table output.' };
    case 'CALCULATE':
      return { title: 'Calculate', introduction: 'Create new values using columns in this table.' };
    case 'RESHAPE':
      return { title: 'Reshape', introduction: 'Change what the rows and columns represent.' };
    case 'COMBINE':
      return { title: 'Combine', introduction: 'Use another table to build this one.' };
    default: {
      const exhaustive: never = family;
      return exhaustive;
    }
  }
};
