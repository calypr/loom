export function hasSingleScopedRowTable(namesRowSetting: boolean, scopedTableCount: number): boolean {
  return namesRowSetting && scopedTableCount === 1;
}

export type DatasetReviewTarget =
  | {
      readonly kind: 'column';
      readonly outputId: string;
      readonly column: string;
      readonly label: string;
      readonly occurrenceId: string;
    }
  | {
      readonly kind: 'row';
      readonly outputId: string;
      readonly control: 'row-type' | 'row-definition';
    }
  | { readonly kind: 'new-table' }
  | { readonly kind: 'table'; readonly outputId: string };
