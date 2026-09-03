import type { LoomFacetSpec, LoomOutputFilter, LoomOutputRequest } from '../../api';
import type { ExplorerRuntimeBindingV1, ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../../types';
import { activeOutputState, filterValuesFor, viewerFilterKind, type ViewerState } from './model';

export const facetName = (outputId: string, column: string): string => `loom:${outputId}:${column}`;
export const chartFacetName = (outputId: string, column: string): string => `loom:${outputId}:chart:${column}`;

const filterForValues = (column: string, values: ReadonlyArray<string>): LoomOutputFilter | undefined => {
  if (values.length === 0) return undefined;
  return { column, op: values.length === 1 ? 'EQ' : 'IN', value: values.length === 1 ? values[0] : [...values] };
};

export const fixedFiltersFor = (output: ExplorerRuntimeOutputV1): ReadonlyArray<LoomOutputFilter> =>
  Object.entries(output.fixedFilters).flatMap(([column, values]) => {
    const filter = filterForValues(column, values);
    return filter ? [filter] : [];
  });

export const outputFiltersFor = (
  runtime: ExplorerRuntimeV1,
  state: ViewerState,
  output: ExplorerRuntimeOutputV1,
): ReadonlyArray<LoomOutputFilter> => {
  const values = filterValuesFor(state, runtime, output);
  const filterBindings = new Map(output.filters.map((binding) => [binding.column, binding]));
  for (const bindings of Object.values(runtime.sharedFilters)) {
    for (const binding of bindings) if (!binding.outputId || binding.outputId === output.outputId) filterBindings.set(binding.column, binding);
  }
  const filters = fixedFiltersFor(output).slice();
  for (const [column, selected] of Object.entries(values)) {
    if (!filterBindings.has(column)) continue;
    const filter = filterForValues(column, selected);
    if (filter) filters.push(filter);
  }
  return filters;
};

const supportedChart = (binding: ExplorerRuntimeBindingV1): boolean =>
  ['bar', 'horizontalstacked', 'pie', 'fullpie', 'donut'].includes(binding.type?.trim().toLowerCase() ?? '');

export const requestedFacetsFor = (
  runtime: ExplorerRuntimeV1,
  output: ExplorerRuntimeOutputV1,
  state: ViewerState,
): ReadonlyArray<LoomFacetSpec> => {
  const specs = new Map<string, LoomFacetSpec>();
  for (const binding of output.filters) {
    specs.set(facetName(output.outputId, binding.column), {
      name: facetName(output.outputId, binding.column),
      kind: 'TERMS',
      column: binding.column,
      size: 50,
      excludeSelfFilter: true,
    });
  }
  for (const bindings of Object.values(runtime.sharedFilters)) {
    for (const binding of bindings) {
      if (binding.outputId && binding.outputId !== output.outputId) continue;
      specs.set(facetName(output.outputId, binding.column), {
        name: facetName(output.outputId, binding.column),
        kind: 'TERMS',
        column: binding.column,
        size: 50,
        excludeSelfFilter: true,
      });
    }
  }
  if (state.chartsVisible[output.outputId] ?? true) {
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

export const outputColumnsForRequest = (output: ExplorerRuntimeOutputV1): ReadonlyArray<string> => {
  const allowed = new Map(output.columns.map((column) => [column.column, column]));
  return output.table.columns
    .filter((column) => column.visible && allowed.get(column.column)?.visible)
    .sort((left, right) => (allowed.get(left.column)?.order ?? 0) - (allowed.get(right.column)?.order ?? 0))
    .map((column) => column.column);
};

export const filterLabel = (binding: ExplorerRuntimeBindingV1): string =>
  binding.label ?? binding.title ?? binding.column.replaceAll('_', ' ');

export const filterType = viewerFilterKind;
