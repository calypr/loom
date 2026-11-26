import type { Construction } from '../../../contracts/types';
import {
  pivotDiscoveryMatches,
  pivotDiscoveryRequestIdentity,
  type CandidateIntent,
  type CompletePivotDiscovery,
  type ConstructionReshapePivotDiscovery,
  type ConstructionReshapePivotDiscoveryRequest,
  type ConstructionReshapeStep,
  type PivotCategoryMemory,
  type ReshapeCapabilities,
  type ReshapeForm,
  type ReshapeStage,
  type StageCapability,
} from './reshapeForms';

type PivotDiscoveryOwner = (
  request: ConstructionReshapePivotDiscoveryRequest,
  onComplete?: (discovery: CompletePivotDiscovery) => void,
) => void;

export type PivotDiscoverySnapshot = {
  readonly contextKey: string;
  readonly formContextKey: string;
  readonly owner?: PivotDiscoveryOwner;
  readonly stageId: string;
  readonly form: ReshapeForm;
  readonly construction: Construction;
  readonly capabilities: ReshapeCapabilities;
  readonly editingStep?: ConstructionReshapeStep;
  readonly stage: ReshapeStage;
  readonly groupStage: ReshapeStage;
  readonly groupSupport: StageCapability;
  readonly groupCodedValuesSupported: boolean;
  readonly sourceGroupSupport: StageCapability;
  readonly codedGroupSupport: StageCapability;
  readonly expandSupport: StageCapability;
  readonly relatedExpandSupport: StageCapability;
  readonly pivotSupport: StageCapability;
  readonly codedPivotSupport: StageCapability;
  readonly unpivotSupport: StageCapability;
  readonly discovery?: ConstructionReshapePivotDiscovery;
  readonly disabled: boolean;
  readonly pivotSupported: boolean;
  readonly onCandidateChange: (intent: CandidateIntent | undefined) => void;
  readonly onFormKindChange?: (kind: ReshapeForm['kind']) => void;
};

export type PendingPivotDiscovery = {
  readonly identity: string;
  readonly revision: number;
  readonly owner: PivotDiscoveryOwner;
};

export type ScheduledPivotDiscovery = PendingPivotDiscovery & { readonly timer: number };

type AwaitingPivotDiscovery = PendingPivotDiscovery & {
  readonly contextKey: string;
  readonly seen: ConstructionReshapePivotDiscovery | undefined;
};

type PivotDiscoveryState = {
  readonly snapshot: PivotDiscoverySnapshot;
  readonly mounted: boolean;
  readonly revision: number;
  readonly automaticallySelectedPairs: ReadonlySet<string>;
  readonly manuallySelectedPairs: ReadonlySet<string>;
  readonly categoryMemory: ReadonlyMap<string, PivotCategoryMemory>;
  readonly requested?: PendingPivotDiscovery;
  readonly scheduled?: ScheduledPivotDiscovery;
  readonly activationTimer?: number;
  readonly awaiting?: AwaitingPivotDiscovery;
  readonly reportedInitialContextKey?: string;
};

type PivotDiscoveryAction =
  | { readonly type: 'root-attached'; readonly snapshot: PivotDiscoverySnapshot }
  | { readonly type: 'root-detached' }
  | { readonly type: 'initial-state-reported'; readonly contextKey: string; readonly hasInitialEntry: boolean }
  | { readonly type: 'activation-scheduled'; readonly contextKey: string; readonly timer: number }
  | { readonly type: 'activation-fired'; readonly contextKey: string; readonly timer: number }
  | { readonly type: 'schedule-created'; readonly scheduled: ScheduledPivotDiscovery }
  | { readonly type: 'schedule-canceled'; readonly identity?: string; readonly revision?: number }
  | { readonly type: 'awaiting-cleared'; readonly identity: string; readonly revision: number }
  | { readonly type: 'schedule-fired'; readonly scheduled: ScheduledPivotDiscovery }
  | { readonly type: 'request-started'; readonly pending: PendingPivotDiscovery }
  | { readonly type: 'discovery-arrived'; readonly pending: PendingPivotDiscovery; readonly contextKey: string; readonly stageId: string; readonly discovery: CompletePivotDiscovery }
  | { readonly type: 'form-changed'; readonly form: ReshapeForm; readonly requestIdentityChanged: boolean }
  | { readonly type: 'automatic-pair-selected'; readonly identity: string }
  | { readonly type: 'manual-pair-selected'; readonly identity: string; readonly memoryKey: string; readonly excluded: ReadonlySet<string> }
  | { readonly type: 'automatic-pair-forgotten'; readonly identity: string }
  | { readonly type: 'pivot-pairs-forgotten'; readonly identities: ReadonlyArray<string>; readonly memoryKeys: ReadonlyArray<string> };

type PivotDiscoveryTransition = {
  readonly state: PivotDiscoveryState;
  readonly accepted: boolean;
};

export const initialPivotDiscoveryState = (snapshot: PivotDiscoverySnapshot): PivotDiscoveryState => ({
  snapshot,
  mounted: false,
  revision: 0,
  automaticallySelectedPairs: new Set(),
  manuallySelectedPairs: new Set(),
  categoryMemory: new Map(),
});

const requestIsCurrent = (state: PivotDiscoveryState, pending: PendingPivotDiscovery): boolean => {
  const { snapshot } = state;
  return state.mounted
    && state.revision === pending.revision
    && snapshot.owner === pending.owner
    && snapshot.formContextKey === snapshot.contextKey
    && snapshot.form.kind === 'pivot'
    && pivotDiscoveryRequestIdentity(snapshot.form, snapshot.stageId, snapshot.contextKey) === pending.identity;
};

export const transitionPivotDiscovery = (
  state: PivotDiscoveryState,
  action: PivotDiscoveryAction,
): PivotDiscoveryTransition => {
  switch (action.type) {
    case 'root-attached': {
      const contextChanged = state.snapshot.contextKey !== action.snapshot.contextKey;
      const ownerChanged = state.snapshot.owner !== action.snapshot.owner;
      const next: PivotDiscoveryState = {
        ...state,
        snapshot: action.snapshot,
        mounted: true,
        ...(contextChanged ? {
          revision: state.revision + 1,
          automaticallySelectedPairs: new Set<string>(),
          manuallySelectedPairs: new Set<string>(),
          categoryMemory: new Map<string, PivotCategoryMemory>(),
          requested: undefined,
          scheduled: undefined,
          awaiting: undefined,
        } : ownerChanged ? {
          revision: state.revision + 1,
          scheduled: undefined,
        } : {}),
      };
      return { state: next, accepted: true };
    }
    case 'root-detached':
      return {
        state: { ...state, mounted: false, activationTimer: undefined, scheduled: undefined },
        accepted: true,
      };
    case 'initial-state-reported':
      if (action.hasInitialEntry) {
        return { state: { ...state, reportedInitialContextKey: undefined }, accepted: true };
      }
      if (state.reportedInitialContextKey === action.contextKey) return { state, accepted: false };
      return { state: { ...state, reportedInitialContextKey: action.contextKey }, accepted: true };
    case 'activation-scheduled':
      if (!state.mounted || state.snapshot.contextKey !== action.contextKey) return { state, accepted: false };
      return { state: { ...state, activationTimer: action.timer }, accepted: true };
    case 'activation-fired':
      if (state.activationTimer !== action.timer) return { state, accepted: false };
      return {
        state: { ...state, activationTimer: undefined },
        accepted: state.mounted && state.snapshot.contextKey === action.contextKey,
      };
    case 'schedule-created': {
      const form = state.snapshot.form;
      if (!requestIsCurrent(state, action.scheduled)
          || state.snapshot.disabled || !state.snapshot.pivotSupported
          || form.kind !== 'pivot'
          || action.scheduled.identity !== pivotDiscoveryRequestIdentity(form, state.snapshot.stageId, state.snapshot.contextKey)) {
        return { state, accepted: false };
      }
      return {
        state: {
          ...state,
          scheduled: action.scheduled,
          awaiting: {
            ...action.scheduled,
            contextKey: state.snapshot.contextKey,
            seen: state.snapshot.discovery,
          },
        },
        accepted: true,
      };
    }
    case 'schedule-canceled': {
      const scheduled = state.scheduled;
      if (!scheduled || action.identity !== undefined && scheduled.identity !== action.identity
          || action.revision !== undefined && scheduled.revision !== action.revision) return { state, accepted: false };
      const clearAwaiting = state.awaiting?.identity === scheduled.identity
        && state.awaiting.revision === scheduled.revision;
      return {
        state: {
          ...state,
          scheduled: undefined,
          ...(clearAwaiting ? { awaiting: undefined } : {}),
        },
        accepted: true,
      };
    }
    case 'awaiting-cleared':
      if (!state.awaiting || state.awaiting.identity !== action.identity || state.awaiting.revision !== action.revision) {
        return { state, accepted: false };
      }
      return { state: { ...state, awaiting: undefined }, accepted: true };
    case 'schedule-fired': {
      const scheduled = state.scheduled;
      if (!scheduled || scheduled.timer !== action.scheduled.timer) return { state, accepted: false };
      const form = state.snapshot.form;
      const current = requestIsCurrent(state, action.scheduled)
        && !state.snapshot.disabled
        && state.snapshot.pivotSupported
        && form.kind === 'pivot';
      return { state: { ...state, scheduled: undefined }, accepted: current };
    }
    case 'request-started': {
      if (!requestIsCurrent(state, action.pending) || state.snapshot.disabled || !state.snapshot.pivotSupported) {
        return { state, accepted: false };
      }
      return {
        state: {
          ...state,
          requested: action.pending,
          scheduled: undefined,
          awaiting: {
            ...action.pending,
            contextKey: state.snapshot.contextKey,
            seen: state.snapshot.discovery,
          },
        },
        accepted: true,
      };
    }
    case 'discovery-arrived': {
      const form = state.snapshot.form;
      if (!requestIsCurrent(state, action.pending)
          || state.snapshot.contextKey !== action.contextKey
          || state.snapshot.stageId !== action.stageId
          || form.kind !== 'pivot'
          || !pivotDiscoveryMatches(form, action.stageId, action.discovery)) {
        return { state, accepted: false };
      }
      const clearAwaiting = state.awaiting?.identity === action.pending.identity
        && state.awaiting.revision === action.pending.revision;
      return {
        state: {
          ...state,
          snapshot: { ...state.snapshot, discovery: action.discovery },
          ...(clearAwaiting ? { awaiting: undefined } : {}),
        },
        accepted: true,
      };
    }
    case 'form-changed': {
      const next: PivotDiscoveryState = { ...state, snapshot: { ...state.snapshot, form: action.form } };
      if (action.requestIdentityChanged) return { state: { ...next, revision: state.revision + 1, scheduled: undefined }, accepted: true };
      return { state: next, accepted: true };
    }
    case 'automatic-pair-selected': {
      if (state.automaticallySelectedPairs.has(action.identity)) return { state, accepted: false };
      const automaticallySelectedPairs = new Set(state.automaticallySelectedPairs);
      automaticallySelectedPairs.add(action.identity);
      return { state: { ...state, automaticallySelectedPairs }, accepted: true };
    }
    case 'manual-pair-selected': {
      const manuallySelectedPairs = new Set(state.manuallySelectedPairs);
      manuallySelectedPairs.add(action.identity);
      const categoryMemory = new Map(state.categoryMemory);
      categoryMemory.set(action.memoryKey, { excluded: new Set(action.excluded) });
      return { state: { ...state, manuallySelectedPairs, categoryMemory }, accepted: true };
    }
    case 'automatic-pair-forgotten': {
      if (!state.automaticallySelectedPairs.has(action.identity)) return { state, accepted: false };
      const automaticallySelectedPairs = new Set(state.automaticallySelectedPairs);
      automaticallySelectedPairs.delete(action.identity);
      return { state: { ...state, automaticallySelectedPairs }, accepted: true };
    }
    case 'pivot-pairs-forgotten': {
      const automaticallySelectedPairs = new Set(state.automaticallySelectedPairs);
      const manuallySelectedPairs = new Set(state.manuallySelectedPairs);
      const categoryMemory = new Map(state.categoryMemory);
      for (const identity of action.identities) {
        automaticallySelectedPairs.delete(identity);
        manuallySelectedPairs.delete(identity);
      }
      for (const key of action.memoryKeys) categoryMemory.delete(key);
      return { state: { ...state, automaticallySelectedPairs, manuallySelectedPairs, categoryMemory }, accepted: true };
    }
  }
};

export const pivotDiscoveryReducer = (state: PivotDiscoveryState, action: PivotDiscoveryAction): PivotDiscoveryState =>
  transitionPivotDiscovery(state, action).state;
