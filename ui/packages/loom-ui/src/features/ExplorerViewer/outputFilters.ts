import type { LoomOutputFilter } from '../../client/api';
import type { ExplorerRuntimeOutputV1, ExplorerRuntimeV1 } from '../../contracts/types';
import { filterSelectionsFor, type ViewerFieldSelection, type ViewerState } from './model';

const MISSING_VALUE_PLACEHOLDER: Record<string, never> = {};

const filtersForFixedValues = (column: string, values: ReadonlyArray<string>): ReadonlyArray<LoomOutputFilter> => {
  if (values.length === 1) return [{ column, op: 'EQ', value: values[0] }];
  if (values.length > 1) return [{ column, op: 'IN', value: [...values] }];
  return [];
};

const filtersForSelection = (column: string, selection: ViewerFieldSelection): ReadonlyArray<LoomOutputFilter> => {
  if (selection.kind === 'missing') return [{ column, op: 'IS_NULL', value: MISSING_VALUE_PLACEHOLDER }];
  return filtersForFixedValues(column, selection.values);
};

const fixedFiltersFor = (output: ExplorerRuntimeOutputV1): ReadonlyArray<LoomOutputFilter> =>
  Object.entries(output.fixedFilters).flatMap(([column, values]) => filtersForFixedValues(column, values));

export const outputFiltersFor = (
  runtime: ExplorerRuntimeV1,
  state: ViewerState,
  output: ExplorerRuntimeOutputV1,
): ReadonlyArray<LoomOutputFilter> => {
  const selections = filterSelectionsFor(state, runtime, output);
  const filterBindings = new Map(output.filters.map((binding) => [binding.column, binding]));
  for (const bindings of Object.values(runtime.sharedFilters)) {
    for (const binding of bindings) if (!binding.outputId || binding.outputId === output.outputId) filterBindings.set(binding.column, binding);
  }
  const filters = fixedFiltersFor(output).slice();
  for (const [column, selection] of Object.entries(selections)) {
    if (!filterBindings.has(column)) continue;
    filters.push(...filtersForSelection(column, selection));
  }
  return filters;
};
