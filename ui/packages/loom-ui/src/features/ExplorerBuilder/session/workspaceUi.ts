import type { SelectionRevision } from '../../../contracts/selection';
import type { ConstructionChoiceSelection } from '../../../contracts/types';
import { createStore } from '../../../data/createStore';
import type { DatasetReviewTarget } from '../model/datasetReviewSelection';

type PopulationSelectionOverride = {
  readonly contextKey: string;
  readonly baseSelectionID: string;
  readonly selection: SelectionRevision;
};

type ClearedPopulationSelection<Client> = {
  readonly contextKey: string;
  readonly client: Client;
  readonly selectionID: string;
};

type WorkspaceUiState<Client> = {
  readonly message: string | undefined;
  readonly pendingSavedRuleRemoval: string | undefined;
  readonly advancedView: 'add' | 'saved';
  readonly cancelingConstructionProposal: boolean;
  readonly populationSelectionOverride: PopulationSelectionOverride | undefined;
  readonly clearedPopulationSelection: ClearedPopulationSelection<Client> | undefined;
  readonly featureMode: 'catalog' | 'graph';
  readonly rowValuePolicy: NonNullable<ConstructionChoiceSelection['rowValuePolicy']>;
  readonly reviewOpen: boolean;
  readonly reviewFocusTarget: Exclude<DatasetReviewTarget, { readonly kind: 'new-table' }> | undefined;
  readonly reviewFocusColumn: Extract<DatasetReviewTarget, { readonly kind: 'column' }> | undefined;
  readonly interpretationContextRefreshVersion: number;
};

type Update<Value> = Value | ((current: Value) => Value);

export function createWorkspaceUi<Panel, Client>() {
  const store = createStore<WorkspaceUiState<Client>>({
    message: undefined,
    pendingSavedRuleRemoval: undefined,
    advancedView: 'add',
    cancelingConstructionProposal: false,
    populationSelectionOverride: undefined,
    clearedPopulationSelection: undefined,
    featureMode: 'catalog',
    rowValuePolicy: 'ALL',
    reviewOpen: false,
    reviewFocusTarget: undefined,
    reviewFocusColumn: undefined,
    interpretationContextRefreshVersion: 0,
  });
  let occurrencePanels: { key: string; panels: Map<string, Panel> } | undefined;

  function set<Field extends keyof WorkspaceUiState<Client>>(field: Field, update: Update<WorkspaceUiState<Client>[Field]>) {
    const current = store.getSnapshot();
    const value = typeof update === 'function' ? update(current[field]) : update;
    if (!Object.is(value, current[field])) store.setSnapshot({ ...current, [field]: value });
  }

  return {
    getSnapshot: store.getSnapshot,
    subscribe: store.subscribe,
    setMessage: (update: Update<WorkspaceUiState<Client>['message']>) => set('message', update),
    setPendingSavedRuleRemoval: (update: Update<WorkspaceUiState<Client>['pendingSavedRuleRemoval']>) => set('pendingSavedRuleRemoval', update),
    setAdvancedView: (update: Update<WorkspaceUiState<Client>['advancedView']>) => set('advancedView', update),
    setPopulationSelectionOverride: (update: Update<WorkspaceUiState<Client>['populationSelectionOverride']>) => set('populationSelectionOverride', update),
    setClearedPopulationSelection: (update: Update<WorkspaceUiState<Client>['clearedPopulationSelection']>) => set('clearedPopulationSelection', update),
    setFeatureMode: (update: Update<WorkspaceUiState<Client>['featureMode']>) => set('featureMode', update),
    setRowValuePolicy: (update: Update<WorkspaceUiState<Client>['rowValuePolicy']>) => set('rowValuePolicy', update),
    setReviewOpen: (update: Update<WorkspaceUiState<Client>['reviewOpen']>) => set('reviewOpen', update),
    setReviewFocusTarget: (update: Update<WorkspaceUiState<Client>['reviewFocusTarget']>) => set('reviewFocusTarget', update),
    setReviewFocusColumn: (update: Update<WorkspaceUiState<Client>['reviewFocusColumn']>) => set('reviewFocusColumn', update),
    setInterpretationContextRefreshVersion: (update: Update<WorkspaceUiState<Client>['interpretationContextRefreshVersion']>) => set('interpretationContextRefreshVersion', update),
    isCancelingConstructionProposal: () => store.getSnapshot().cancelingConstructionProposal,
    beginConstructionCancellation() {
      if (store.getSnapshot().cancelingConstructionProposal) return false;
      set('cancelingConstructionProposal', true);
      return true;
    },
    finishConstructionCancellation: () => set('cancelingConstructionProposal', false),
    occurrencePanelsFor(key: string) {
      if (occurrencePanels?.key !== key) occurrencePanels = { key, panels: new Map() };
      return occurrencePanels.panels;
    },
  };
}
