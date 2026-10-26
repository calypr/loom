import type { RootChangeSelection } from './rootChangeSelection';

export function hasPairedPreviewRows(
  previewStatus: string,
  columnId: string | undefined,
  rows: readonly unknown[] | null | undefined,
): rows is readonly unknown[] {
  return previewStatus === 'READY' && columnId !== undefined && rows != null;
}

export function hasCommandSelectedTable(
  selectsTableThroughCommand: boolean,
  selectedOutputId: string | null | undefined,
): selectedOutputId is string {
  return selectsTableThroughCommand && selectedOutputId != null;
}

export type CompilerNavigationSelection = Extract<
  RootChangeSelection,
  { readonly kind: 'compiler-route' | 'compiler-occurrence' }
>;

export function isCompilerNavigationSelection(
  selection: RootChangeSelection,
): selection is CompilerNavigationSelection {
  return selection.kind === 'compiler-route' || selection.kind === 'compiler-occurrence';
}
