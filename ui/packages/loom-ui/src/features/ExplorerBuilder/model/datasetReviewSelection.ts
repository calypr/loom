export function hasSingleScopedRowTable(namesRowSetting: boolean, scopedTableCount: number): boolean {
  return namesRowSetting && scopedTableCount === 1;
}
