import { QueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';
import type { ConstructionChoice, ConstructionChoiceForm, ConstructionChoiceSelection } from '../../../contracts/types';
import { catalogChoiceIntent, isRelatedFieldCatalogItem, type CatalogChoiceGroup, type CatalogChoiceIntent } from '../model/catalogItems';

export const routeCoverageForm = (
  item: CatalogChoiceGroup['item'],
  choice: ConstructionChoice,
  rowRoot: string,
  groupedRows: boolean,
  initialSelection?: CatalogInitialSelection,
): 'COUNT' | 'ALL' | undefined => {
  if (item.kind === 'FIELD' && isRelatedFieldCatalogItem(item, rowRoot)) {
    const savedForm = choice.choiceId === initialSelection?.choiceId ? initialSelection.form : undefined;
    if ((savedForm === 'COUNT' || savedForm === 'ALL') &&
      choice.options.some((option) => option.form === savedForm && option.support === 'SUPPORTED')) return savedForm;
    if (groupedRows && choice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED')) return 'ALL';
    if (choice.options.some((option) => option.form === 'COUNT' && option.support === 'SUPPORTED')) return 'COUNT';
    if (choice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED')) return 'ALL';
    return undefined;
  }
  if (item.kind === 'SEMANTIC' && choice.route.length > 0 &&
    choice.options.some((option) => option.form === 'ALL' && option.support === 'SUPPORTED')) return 'ALL';
  return undefined;
};

export type ConditionDraft = { readonly mode: 'ALL' | 'EXISTS' | 'EQUALS'; readonly value: string };
type RouteMatchCoverage = {
  readonly zero: number;
  readonly one: number;
  readonly many: number;
  readonly displayedRows: number;
  readonly sampled: boolean;
};
type RouteValueCoverage = {
  readonly kind: 'VALUES';
  readonly empty: number;
  readonly one: number;
  readonly many: number;
  readonly displayedRows: number;
  readonly sampled: boolean;
};
export type RouteCoverage = RouteMatchCoverage | RouteValueCoverage;
type RouteCoverageState =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly coverage: RouteCoverage }
  | { readonly status: 'error'; readonly message: string };
export type CatalogInitialSelection = {
  readonly choiceId: string;
  readonly form: ConstructionChoiceForm;
  readonly condition?: ConditionDraft;
};

export function useRouteCoverage(
  rowRoot: string,
  groupedRows: boolean,
  initialSelection: CatalogInitialSelection | undefined,
  onInspectRouteCoverage: ((selection: CatalogChoiceIntent, signal: AbortSignal) => Promise<RouteCoverage>) | undefined,
) {
  const [routeCoverage, setRouteCoverage] = useState<ReadonlyMap<string, RouteCoverageState>>(() => new Map());
  const [queryClient] = useState(() => new QueryClient({
    defaultOptions: { queries: { retry: false, networkMode: 'always', gcTime: 0 } },
  }));
  const coverageRequests = useRef(new Map<string, object>());
  const cancelCoverage = () => {
    coverageRequests.current.clear();
    void queryClient.cancelQueries();
  };
  const inspectRouteCoverage = (group: CatalogChoiceGroup, choice: ConstructionChoice) => {
    const form = routeCoverageForm(group.item, choice, rowRoot, groupedRows, initialSelection);
    if (!onInspectRouteCoverage || !form || coverageRequests.current.has(choice.choiceId)) return;
    const selection = catalogChoiceIntent({ item: group.item, choice, form, rowRoot });
    if (form === 'COUNT' && !selection.relatedSource) return;
    const request = {};
    coverageRequests.current.set(choice.choiceId, request);
    setRouteCoverage((current) => new Map(current).set(choice.choiceId, { status: 'loading' }));
    void queryClient.fetchQuery({
      queryKey: ['route-coverage', choice.choiceId, selection],
      queryFn: ({ signal }) => onInspectRouteCoverage(selection, signal),
    }).then((coverage) => {
      if (coverageRequests.current.get(choice.choiceId) === request) {
        setRouteCoverage((current) => new Map(current).set(choice.choiceId, { status: 'ready', coverage }));
      }
    }).catch((error: unknown) => {
      if (coverageRequests.current.get(choice.choiceId) === request) {
        setRouteCoverage((current) => new Map(current).set(choice.choiceId, {
          status: 'error',
          message: error instanceof Error ? error.message : 'Route coverage is unavailable.',
        }));
      }
    }).finally(() => {
      if (coverageRequests.current.get(choice.choiceId) === request) {
        coverageRequests.current.delete(choice.choiceId);
      }
    });
  };

  return { routeCoverage, inspectRouteCoverage, cancelCoverage };
}

type ConstructionRowValuePolicy = NonNullable<ConstructionChoiceSelection['rowValuePolicy']>;

export type GroupedRowValuePolicyControl = Readonly<{
  value: ConstructionRowValuePolicy;
  onChange: (value: ConstructionRowValuePolicy) => void;
}>;
