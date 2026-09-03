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

export interface ViewerOutputState {
  readonly filterValues: Readonly<Record<string, ReadonlyArray<string>>>;
  readonly sort?: ViewerSort;
  readonly pageSize: PageSize;
  readonly cursorHistory: ReadonlyArray<string | undefined>;
}

export interface ViewerSharedFilterProposal {
  readonly name: string;
  readonly values: ReadonlyArray<string>;
}

export type ViewerOverlay =
  | { readonly kind: 'none' }
  | { readonly kind: 'rowDetails'; readonly outputId: string; readonly row: Readonly<Record<string, unknown>> }
  | { readonly kind: 'sharedFilterConfirmation'; readonly proposal: ViewerSharedFilterProposal };

export interface ViewerState {
  readonly activeOutputId: string;
  readonly outputs: Readonly<Record<string, ViewerOutputState>>;
  readonly sharedFilters: Readonly<Record<string, ReadonlyArray<string>>>;
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
  runtime.publication?.revisionId ?? runtime.publication?.generation ?? runtime.generation ?? runtime.schema?.digest ?? 'runtime';

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
    chartsVisible[output.outputId] = true;
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

export const filterValuesFor = (
  state: ViewerState,
  runtime: ExplorerRuntimeV1,
  output: ExplorerRuntimeOutputV1,
): Readonly<Record<string, ReadonlyArray<string>>> => {
  const values: Record<string, ReadonlyArray<string>> = { ...activeOutputState(state, output.outputId).filterValues };
  for (const [name, selected] of Object.entries(state.sharedFilters)) {
    if (selected.length === 0) continue;
    for (const binding of runtime.sharedFilters[name] ?? []) {
      if (!binding.outputId || binding.outputId === output.outputId) values[binding.column] = selected;
    }
  }
  return values;
};
