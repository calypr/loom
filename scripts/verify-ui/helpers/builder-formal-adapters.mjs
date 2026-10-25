import {
  buildPolicyOneMutationControls,
  compilePolicyOneJourneyTrace,
  validatePolicyOneJourneyTrace,
} from './policy-one-tla-replay.mjs';

const policyOneRequestCoverage = trace => {
  const rows = Array.isArray(trace?.transportLedger) ? trace.transportLedger : [];
  const indices = rows.map(row => row?.transportIndex);
  const duplicateIndices = indices.length - new Set(indices).size;
  const invalidSetupRows = new Set(rows.filter(row =>
    (row?.classification === 'setup' && (row?.setupMutation !== true || row?.capturePhase !== 'SETUP')) ||
    (row?.setupMutation === true && row?.classification !== 'setup')));
  const modeledActions = rows.filter(row => row?.classification === 'modeled' && !invalidSetupRows.has(row)).length;
  const reconcileReads = rows.filter(row => row?.classification === 'reconcile' && !invalidSetupRows.has(row)).length;
  const supportedReadOnly = rows.filter(row => row?.classification === 'read-only' && !invalidSetupRows.has(row)).length;
  const setupMutations = rows.filter(row => row?.classification === 'setup' && !invalidSetupRows.has(row)).length;
  const modeled = modeledActions + reconcileReads + supportedReadOnly + setupMutations;
  return {
    observed: rows.length,
    modeled,
    unmapped: rows.length - modeled,
    duplicates: duplicateIndices,
    modeledActions,
    reconcileReads,
    supportedReadOnly,
    setupMutations,
  };
};

const adapters = Object.freeze({
  'builder-native-journey-v1': Object.freeze({
    captureAdapter: 'builder-native-journey-v1',
    captureKind: 'builder-cda-native-lifecycle',
    traceName: 'cda-builder-group-related-source',
    model: 'BuilderJourney',
    family: 'cda-builder-lifecycle',
  }),
  'policy-one-disagreement-v1': Object.freeze({
    captureAdapter: 'policy-one-disagreement-v1',
    captureKind: 'builder-policy-one-native-lifecycle',
    traceName: 'builder-authoring:patient-one-disagreement',
    model: 'PolicyOneDisagreement',
    nativeCaseKey: 'builder-authoring/patient-one-disagreement',
    family: 'policy-one-disagreement',
    validateTrace: validatePolicyOneJourneyTrace,
    compileTrace: compilePolicyOneJourneyTrace,
    buildMutationControls: buildPolicyOneMutationControls,
    requestCoverage: policyOneRequestCoverage,
  }),
});

export const builderFormalAdapterForBinding = binding => {
  if (!binding || typeof binding !== 'object' || Array.isArray(binding)) {
    throw new Error('Builder formal adapter lookup requires a registered journeyTrace binding.');
  }
  const adapter = adapters[binding.captureAdapter];
  if (!adapter) {
    throw new Error(`Unknown registered Builder formal adapter: ${String(binding.captureAdapter)}`);
  }
  if (binding.captureKind !== adapter.captureKind) {
    throw new Error(`Builder formal adapter ${adapter.captureAdapter} requires captureKind ${adapter.captureKind}.`);
  }
  if (binding.name !== adapter.traceName || binding.model !== adapter.model) {
    throw new Error(`Builder formal adapter ${adapter.captureAdapter} requires trace ${adapter.traceName}/${adapter.model}.`);
  }
  return adapter;
};

export const builderFormalAdapterForContract = contract => {
  const registration = contract?.nativeJourney;
  if (!registration || typeof registration !== 'object' || Array.isArray(registration)) return null;
  const adapter = adapters[registration.captureAdapter];
  if (!adapter) {
    throw new Error(`Unknown registered Builder formal adapter in nativeJourney contract: ${String(registration.captureAdapter)}`);
  }
  if (registration.traceName !== adapter.traceName || registration.model !== adapter.model ||
      registration.nativeCaseKey !== adapter.nativeCaseKey) {
    throw new Error(`nativeJourney contract does not match static adapter ${adapter.captureAdapter}.`);
  }
  if (registration.exactActionCounts !== true || !Array.isArray(registration.actions) ||
      registration.actions.length === 0 || registration.actions.some(action =>
        !action || typeof action !== 'object' || Array.isArray(action) ||
        typeof action.label !== 'string' || !action.label || typeof action.event !== 'string' || !action.event)) {
    throw new Error(`nativeJourney contract ${adapter.captureAdapter} must declare its exact ordered label/event inventory.`);
  }
  if (!Array.isArray(registration.requiredMutationControlNames) ||
      registration.requiredMutationControlNames.length === 0 ||
      registration.requiredMutationControlNames.some(name => typeof name !== 'string' || !name.trim()) ||
      new Set(registration.requiredMutationControlNames).size !== registration.requiredMutationControlNames.length) {
    throw new Error(`nativeJourney contract ${adapter.captureAdapter} must declare unique required mutation controls.`);
  }
  if (typeof registration.coverageBasis !== 'string' || !registration.coverageBasis.trim()) {
    throw new Error(`nativeJourney contract ${adapter.captureAdapter} must declare its raw-request coverage basis.`);
  }
  return adapter;
};

export const builderFormalAdapterForCaptureKind = captureKind =>
  Object.values(adapters).find(adapter => adapter.captureKind === captureKind) ?? null;

export const isNativeBuilderFormalCaptureKind = captureKind =>
  builderFormalAdapterForCaptureKind(captureKind) !== null;

export const registeredBuilderFormalAdapterIds = Object.freeze(Object.keys(adapters));
