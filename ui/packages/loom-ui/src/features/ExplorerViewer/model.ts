import type {
  ExplorerRuntimeBindingV1,
  ExplorerRuntimeOutputV1,
  ExplorerRuntimeV1,
} from '../../types';

export const PAGE_SIZES = [5, 10, 20, 40, 100] as const;
export type PageSize = (typeof PAGE_SIZES)[number];

export const isPageSize = (value: number): value is PageSize => {
  for (const size of PAGE_SIZES) if (size === value) return true;
  return false;
};

export type ViewerFilterKind = 'enum' | 'terms';

export interface ViewerSort {
  readonly column: string;
  readonly desc: boolean;
}

export type ViewerFieldSelection =
  | { readonly kind: 'values'; readonly values: ReadonlyArray<string> }
  | { readonly kind: 'missing' };

export const valuesSelection = (values: ReadonlyArray<string>): ViewerFieldSelection => ({
  kind: 'values',
  values: [...values],
});

export const missingSelection = (): ViewerFieldSelection => ({ kind: 'missing' });

export const clearedSelection = (): ViewerFieldSelection => valuesSelection([]);

export const isClearedSelection = (selection: ViewerFieldSelection): boolean =>
  selection.kind === 'values' && selection.values.length === 0;

export const isSelectedSelection = (selection: ViewerFieldSelection | undefined): boolean =>
  selection !== undefined && !isClearedSelection(selection);

export interface ViewerOutputState {
  readonly filterValues: Readonly<Record<string, ViewerFieldSelection>>;
  readonly sort?: ViewerSort;
  readonly pageSize: PageSize;
  readonly cursorHistory: ReadonlyArray<string | undefined>;
}

export interface ViewerSharedFilterProposal {
  readonly name: string;
  readonly selection: ViewerFieldSelection;
}

export type ViewerOverlay =
  | { readonly kind: 'none' }
  | { readonly kind: 'rowDetails'; readonly outputId: string; readonly row: Readonly<Record<string, unknown>> }
  | { readonly kind: 'sharedFilterConfirmation'; readonly proposal: ViewerSharedFilterProposal };

export interface ViewerState {
  readonly activeOutputId: string;
  readonly outputs: Readonly<Record<string, ViewerOutputState>>;
  readonly sharedFilters: Readonly<Record<string, ViewerFieldSelection>>;
  readonly expandedFacets: ReadonlyArray<string>;
  readonly chartsVisible: Readonly<Record<string, boolean>>;
  readonly overlay: ViewerOverlay;
}

export const viewerFilterKind = (binding: ExplorerRuntimeBindingV1): ViewerFilterKind =>
  binding.type?.trim().toLowerCase() === 'enum' ? 'enum' : 'terms';

const initialOutputState = (): ViewerOutputState => ({
  filterValues: {},
  pageSize: 20,
  cursorHistory: [undefined],
});

export const runtimeSessionKey = (runtime: ExplorerRuntimeV1): string =>
  runtime.responseIdentity ?? runtime.publication?.revisionId ?? runtime.publication?.generation ?? runtime.generation ?? runtime.schema?.digest ?? '';

export const initialViewerState = (
  runtime: ExplorerRuntimeV1,
  activeOutputId?: string,
): ViewerState => {
  const firstOutput = runtime.outputs[0];
  const selectedCandidate = runtime.outputs.some((output) => output.outputId === activeOutputId)
    ? activeOutputId
    : undefined;
  const selected = selectedCandidate ?? firstOutput?.outputId ?? '';
  const outputs: Record<string, ViewerOutputState> = {};
  const chartsVisible: Record<string, boolean> = {};
  for (const output of runtime.outputs) {
    outputs[output.outputId] = initialOutputState();
    chartsVisible[output.outputId] = false;
  }
  return {
    activeOutputId: selected,
    outputs,
    sharedFilters: {},
    expandedFacets: [],
    chartsVisible,
    overlay: { kind: 'none' },
  };
};

export const activeOutputState = (state: ViewerState, outputId: string): ViewerOutputState =>
  state.outputs[outputId] ?? { filterValues: {}, pageSize: 20, cursorHistory: [undefined] };

export const filterSelectionsFor = (
  state: ViewerState,
  runtime: ExplorerRuntimeV1,
  output: ExplorerRuntimeOutputV1,
): Readonly<Record<string, ViewerFieldSelection>> => {
  const values: Record<string, ViewerFieldSelection> = { ...activeOutputState(state, output.outputId).filterValues };
  for (const [name, selection] of Object.entries(state.sharedFilters)) {
    if (isClearedSelection(selection)) continue;
    for (const binding of runtime.sharedFilters[name] ?? []) {
      if (!binding.outputId || binding.outputId === output.outputId) values[binding.column] = selection;
    }
  }
  return values;
};
