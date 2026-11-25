import { outputFiltersFor } from './outputFilters';
import type { LoomFacetSpec, LoomOutputFilter, LoomOutputRequest } from '../../client/api';
import type { ExplorerRuntimeBindingV1, ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../../contracts/types';
import { activeOutputState, filterSelectionsFor, isClearedSelection, isSelectedSelection, viewerFilterKind, type ViewerFieldSelection, type ViewerState } from './model';

export const facetName = (outputId: string, column: string): string => `loom:${outputId}:${column}`;
export const chartFacetName = (outputId: string, column: string): string => `loom:${outputId}:chart:${column}`;

export const MISSING_VALUE = 'No value (null or missing)';
export const MISSING_VALUE_DESCRIPTION =
  'Published rows store an absent value and an explicit null identically, so this covers both.';

const supportedChart = (binding: ExplorerRuntimeBindingV1): boolean =>
  ['bar', 'horizontalstacked', 'pie', 'fullpie', 'donut'].includes(binding.type?.trim().toLowerCase() ?? '');

const requestedFacetsFor = (
  runtime: ExplorerRuntimeV1,
  output: ExplorerRuntimeOutputV1,
  state: ViewerState,
): ReadonlyArray<LoomFacetSpec> => {
  const specs = new Map<string, LoomFacetSpec>();
  for (const binding of output.filters) {
    const name = facetName(output.outputId, binding.column);
    const selected = activeOutputState(state, output.outputId).filterValues[binding.column];
    if (!state.expandedFacets.includes(`${output.outputId}:filter:${binding.column}`) && !isSelectedSelection(selected)) continue;
    specs.set(name, {
      name,
      kind: 'TERMS',
      column: binding.column,
      size: 50,
      excludeSelfFilter: true,
    });
  }
  for (const [sharedName, bindings] of Object.entries(runtime.sharedFilters)) {
    for (const binding of bindings) {
      if (binding.outputId && binding.outputId !== output.outputId) continue;
      if (!state.expandedFacets.includes(`${output.outputId}:shared:${sharedName}`) && !isSelectedSelection(state.sharedFilters[sharedName])) continue;
      specs.set(facetName(output.outputId, binding.column), {
        name: facetName(output.outputId, binding.column),
        kind: 'TERMS',
        column: binding.column,
        size: 50,
        excludeSelfFilter: true,
      });
    }
  }
  if (state.chartsVisible[output.outputId] === true) {
    for (const binding of output.charts) {
      if (!supportedChart(binding)) continue;
      specs.set(chartFacetName(output.outputId, binding.column), {
        name: chartFacetName(output.outputId, binding.column),
        kind: 'TERMS',
        column: binding.column,
        size: 12,
      });
    }
  }
  return [...specs.values()];
};

export const outputRequestFor = (
  project: string,
  runtime: ExplorerRuntimeV1,
  state: ViewerState,
  output: ExplorerRuntimeOutputV1,
): LoomOutputRequest => {
  const outputState = activeOutputState(state, output.outputId);
  return {
    project,
    selector: output.selector,
    columns: outputColumnsForRequest(output),
    filters: outputFiltersFor(runtime, state, output),
    ...(outputState.sort ? { sort: outputState.sort } : {}),
    first: outputState.pageSize,
    after: outputState.cursorHistory[outputState.cursorHistory.length - 1],
    facets: requestedFacetsFor(runtime, output, state),
  };
};

const outputColumnsForRequest = (output: ExplorerRuntimeOutputV1): ReadonlyArray<string> => {
  const allowed = new Map(output.columns.map((column) => [column.column, column]));
  return output.table.columns
    .filter((column) => column.visible && allowed.get(column.column)?.visible)
    .sort((left, right) => (allowed.get(left.column)?.order ?? 0) - (allowed.get(right.column)?.order ?? 0))
    .map((column) => column.column);
};

export const filterLabel = (binding: ExplorerRuntimeBindingV1): string =>
  binding.label ?? binding.title ?? binding.column.replaceAll('_', ' ');

export const filterType = viewerFilterKind;
