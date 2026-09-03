import type { ExplorerRuntimeV1 } from '../../types';
import {
  activeOutputState,
  initialViewerState,
  type PageSize,
  type ViewerSharedFilterProposal,
  type ViewerSort,
  type ViewerState,
} from './model';

export type ViewerAction =
  | { readonly type: 'selectOutput'; readonly outputId: string }
  | { readonly type: 'setFilter'; readonly outputId: string; readonly column: string; readonly values: ReadonlyArray<string> }
  | { readonly type: 'setSharedFilter'; readonly name: string; readonly values: ReadonlyArray<string> }
  | { readonly type: 'proposeSharedFilter'; readonly proposal: ViewerSharedFilterProposal }
  | { readonly type: 'clearOverlay' }
  | { readonly type: 'setSort'; readonly outputId: string; readonly sort?: ViewerSort }
  | { readonly type: 'setPageSize'; readonly outputId: string; readonly pageSize: PageSize }
  | { readonly type: 'nextPage'; readonly outputId: string; readonly cursor: string }
  | { readonly type: 'previousPage'; readonly outputId: string }
  | { readonly type: 'toggleFacet'; readonly facet: string }
  | { readonly type: 'toggleCharts'; readonly outputId: string }
  | { readonly type: 'showRowDetails'; readonly outputId: string; readonly row: Readonly<Record<string, unknown>> }
  | { readonly type: 'hideOverlay' };

const resetPage = (output: ReturnType<typeof activeOutputState>) => ({
  ...output,
  cursorHistory: [undefined],
});

const outputMap = (
  state: ViewerState,
  outputId: string,
  update: (output: ReturnType<typeof activeOutputState>) => ReturnType<typeof activeOutputState>,
): ViewerState => ({
  ...state,
  outputs: { ...state.outputs, [outputId]: update(activeOutputState(state, outputId)) },
});

export const viewerReducer = (state: ViewerState, action: ViewerAction): ViewerState => {
  switch (action.type) {
    case 'selectOutput':
      return state.outputs[action.outputId]
        ? { ...state, activeOutputId: action.outputId, overlay: { kind: 'none' } }
        : state;
    case 'setFilter': {
      return outputMap(state, action.outputId, (output) => {
        const filterValues = { ...output.filterValues };
        if (action.values.length > 0) filterValues[action.column] = [...action.values];
        else delete filterValues[action.column];
        return resetPage({ ...output, filterValues });
      });
    }
    case 'proposeSharedFilter':
      return { ...state, overlay: { kind: 'sharedFilterConfirmation', proposal: action.proposal } };
    case 'setSharedFilter': {
      const nextFilters = { ...state.sharedFilters };
      if (action.values.length > 0) nextFilters[action.name] = [...action.values];
      else delete nextFilters[action.name];
      const nextOutputs = Object.fromEntries(
        Object.entries(state.outputs).map(([outputId, output]) => [outputId, resetPage(output)]),
      );
      return { ...state, sharedFilters: nextFilters, outputs: nextOutputs, overlay: { kind: 'none' } };
    }
    case 'clearOverlay':
    case 'hideOverlay':
      return { ...state, overlay: { kind: 'none' } };
    case 'setSort':
      return outputMap(state, action.outputId, (output) => resetPage({ ...output, sort: action.sort }));
    case 'setPageSize':
      return outputMap(state, action.outputId, (output) => ({ ...output, pageSize: action.pageSize, cursorHistory: [undefined] }));
    case 'nextPage': {
      if (!action.cursor) return state;
      return outputMap(state, action.outputId, (output) => {
        const history = [...output.cursorHistory];
        if (history[history.length - 1] === action.cursor) return output;
        history.push(action.cursor);
        return { ...output, cursorHistory: history };
      });
    }
    case 'previousPage':
      return outputMap(state, action.outputId, (output) => output.cursorHistory.length > 1
        ? { ...output, cursorHistory: output.cursorHistory.slice(0, -1) }
        : output);
    case 'toggleFacet':
      return state.expandedFacets.includes(action.facet)
        ? { ...state, expandedFacets: state.expandedFacets.filter((facet) => facet !== action.facet) }
        : { ...state, expandedFacets: [...state.expandedFacets, action.facet] };
    case 'toggleCharts':
      return { ...state, chartsVisible: { ...state.chartsVisible, [action.outputId]: !(state.chartsVisible[action.outputId] ?? true) } };
    case 'showRowDetails':
      return { ...state, overlay: { kind: 'rowDetails', outputId: action.outputId, row: action.row } };
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
};

export const createViewerReducerState = (runtime: ExplorerRuntimeV1, activeOutputId?: string): ViewerState =>
  initialViewerState(runtime, activeOutputId);
