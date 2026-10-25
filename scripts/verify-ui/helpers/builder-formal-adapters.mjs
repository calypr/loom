import { readFileSync } from 'node:fs';
import {
  buildPolicyOneMutationControls,
  compilePolicyOneJourneyTrace,
  validatePolicyOneJourneyTrace,
} from './policy-one-tla-replay.mjs';
import { TEMPORAL_FILTER_CAPTURE_KIND } from './temporal-filter-trace-adapter.mjs';
import {
  compileTemporalFilterJourneyTrace,
  createTemporalFilterMutationControls,
  TEMPORAL_FILTER_FORMAL_PROOF_ASSERTIONS,
  validateTemporalFilterJourneyTrace,
} from './temporal-filter-tla-adapter.mjs';

const policyOneRequestCoverage = trace => {
  const rows = Array.isArray(trace?.transportLedger) ? trace.transportLedger : [];
  const indices = rows.map(row => row?.transportIndex);
  const duplicateIndices = indices.length - new Set(indices).size;
  const invalidSetupRows = new Set(rows.filter(row =>
    (row?.classification === 'setup' && (row?.setupMutation !== true || row?.capturePhase !== 'SETUP')) ||
    (row?.setupMutation === true && row?.classification !== 'setup') ||
    (row?.capturePhase === 'SETUP' && row?.classification === 'modeled')));
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

const temporalFilterRequestCoverage = trace => {
  const projection = validateTemporalFilterJourneyTrace(trace);
  const rows = trace.nativeRequests;
  const browserRequestIds = rows.map(request => request.browserRequestId);
  const duplicates = browserRequestIds.length - new Set(browserRequestIds).size;
  const reconcileIDs = new Set(rows.filter(request => request.method === 'POST' &&
    request.path.endsWith('/reconcile')).map(request => request.browserRequestId));
  const supportedReadOnlyIDs = new Set(projection.pureReadCaptureIds);
  const modeledActions = rows.filter(request => !reconcileIDs.has(request.browserRequestId) &&
    !supportedReadOnlyIDs.has(request.browserRequestId)).length;
  const reconcileReads = reconcileIDs.size;
  const supportedReadOnly = supportedReadOnlyIDs.size;
  const observed = rows.length;
  const modeled = modeledActions + reconcileReads + supportedReadOnly;
  const unmapped = observed - modeled;
  return {
    observed,
    modeled,
    unmapped,
    duplicates,
    modeledActions,
    reconcileReads,
    supportedReadOnly,
  };
};

const compileTemporalFilterTrace = (trace, options) => {
  const compiled = compileTemporalFilterJourneyTrace(trace, options);
  return {
    ...compiled,
    modelName: 'TemporalFilterJourney',
    baseModelText: readFileSync(new URL(
      '../../../verification/builder-interactions/TemporalFilterJourney.tla', import.meta.url), 'utf8'),
    modelText: compiled.moduleText,
  };
};

const builderNativeAdapter = Object.freeze({
  captureAdapter: 'builder-native-journey-v1',
  captureKind: 'builder-cda-native-lifecycle',
  traceName: 'cda-builder-group-related-source',
  model: 'BuilderJourney',
  family: 'cda-builder-lifecycle',
});

const policyOneAdapter = Object.freeze({
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
  requestCoverageFields: Object.freeze([
    'modeledActions', 'reconcileReads', 'supportedReadOnly', 'setupMutations',
  ]),
});

const temporalFilterAdapterFor = caseName => {
  const profile = caseName === 'date'
    ? { valueKind: 'DATE', resourceType: 'Patient', fieldPath: 'birthDate' }
    : { valueKind: 'DATE_TIME', resourceType: 'Observation', fieldPath: 'effectiveDateTime' };
  return Object.freeze({
    captureAdapter: 'temporal-filter-journey-v1',
    captureKind: TEMPORAL_FILTER_CAPTURE_KIND,
    traceName: `basic-temporal-filter:${caseName}`,
    model: 'TemporalFilterJourney',
    nativeCaseKey: `basic-temporal-filter/${caseName}`,
    family: 'temporal-filter-native-lifecycle',
    caseName,
    ...profile,
    formalProofAssertion: TEMPORAL_FILTER_FORMAL_PROOF_ASSERTIONS[caseName],
    validateTrace: validateTemporalFilterJourneyTrace,
    compileTrace: compileTemporalFilterTrace,
    buildMutationControls: createTemporalFilterMutationControls,
    requestCoverage: temporalFilterRequestCoverage,
    requestCoverageFields: Object.freeze([
      'modeledActions', 'reconcileReads', 'supportedReadOnly',
    ]),
  });
};

const temporalFilterAdapters = Object.freeze({
  'basic-temporal-filter/date': temporalFilterAdapterFor('date'),
  'basic-temporal-filter/date-time': temporalFilterAdapterFor('date-time'),
});

const adapterCasesById = Object.freeze({
  [builderNativeAdapter.captureAdapter]: Object.freeze([builderNativeAdapter]),
  [policyOneAdapter.captureAdapter]: Object.freeze([policyOneAdapter]),
  'temporal-filter-journey-v1': Object.freeze(Object.values(temporalFilterAdapters)),
});

const adaptersForCaptureKind = captureKind => Object.values(adapterCasesById)
  .flat().filter(adapter => adapter.captureKind === captureKind);

const adapterForIdentity = (identity, where) => {
  const candidates = adapterCasesById[identity?.captureAdapter];
  if (!candidates) {
    throw new Error(`Unknown registered Builder formal adapter${where}: ${String(identity?.captureAdapter)}`);
  }
  if (candidates.length === 1) return candidates[0];
  if (typeof identity.nativeCaseKey !== 'string' || identity.nativeCaseKey.length === 0) {
    throw new Error(`Builder formal adapter ${identity.captureAdapter}${where} requires an exact nativeCaseKey.`);
  }
  const adapter = candidates.find(candidate => candidate.nativeCaseKey === identity.nativeCaseKey);
  if (!adapter) {
    throw new Error(`Builder formal adapter ${identity.captureAdapter}${where} does not register nativeCaseKey ${identity.nativeCaseKey}.`);
  }
  return adapter;
};

const temporalContractIdentityProblem = (registration, adapter) => {
  if (adapter.family !== 'temporal-filter-native-lifecycle') return null;
  for (const field of ['valueKind', 'resourceType', 'fieldPath', 'formalProofAssertion']) {
    if (registration[field] !== adapter[field]) {
      return `nativeJourney ${field} does not match its exact Temporal case binding`;
    }
  }
  return null;
};

export const builderFormalAdapterForBinding = binding => {
  if (!binding || typeof binding !== 'object' || Array.isArray(binding)) {
    throw new Error('Builder formal adapter lookup requires a registered journeyTrace binding.');
  }
  const adapter = adapterForIdentity(binding, '');
  if (binding.captureKind !== adapter.captureKind) {
    throw new Error(`Builder formal adapter ${adapter.captureAdapter} requires captureKind ${adapter.captureKind}.`);
  }
  if (binding.name !== adapter.traceName || binding.model !== adapter.model) {
    throw new Error(`Builder formal adapter ${adapter.captureAdapter} requires trace ${adapter.traceName}/${adapter.model}.`);
  }
  if (adapter.family === 'temporal-filter-native-lifecycle') {
    const problem = temporalContractIdentityProblem(binding, adapter);
    if (problem) throw new Error(problem);
  }
  return adapter;
};

export const builderFormalAdapterForContract = contract => {
  const registration = contract?.nativeJourney;
  if (!registration || typeof registration !== 'object' || Array.isArray(registration)) return null;
  const adapter = adapterForIdentity(registration, ' in nativeJourney contract');
  if (registration.captureKind !== undefined && registration.captureKind !== adapter.captureKind) {
    throw new Error(`nativeJourney contract does not match static adapter ${adapter.captureAdapter}.`);
  }
  if (registration.traceName !== adapter.traceName || registration.model !== adapter.model ||
      (adapter.nativeCaseKey !== undefined && registration.nativeCaseKey !== adapter.nativeCaseKey)) {
    throw new Error(`nativeJourney contract does not match static adapter ${adapter.captureAdapter}.`);
  }
  const identityProblem = temporalContractIdentityProblem(registration, adapter);
  if (identityProblem) throw new Error(identityProblem);
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

export const builderFormalAdapterForCaptureKind = captureKind => {
  const matches = adaptersForCaptureKind(captureKind);
  return matches.length === 1 ? matches[0] : null;
};

export const isNativeBuilderFormalCaptureKind = captureKind => adaptersForCaptureKind(captureKind).length > 0;

export const registeredBuilderFormalAdapterIds = Object.freeze(Object.keys(adapterCasesById));
