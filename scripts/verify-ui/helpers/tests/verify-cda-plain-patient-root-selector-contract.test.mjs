import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  assertPatientSelectionCreated,
  chooseDirectPatientPopulationRoute,
  choosePatientObservationSubjectRestorationButton,
  choosePatientSubjectObservationRepairButton,
  choosePatientLinkedObservationOption,
  directObservationRootPreservesPatientState,
  isPlainPatientRoot,
  matchesPlainPatientObservationAssessment,
} from '../../workflows/verify-cda-plain-patient-root-selector.mjs';

const spec = await readFile(new URL('../../specs/standalone-cda-plain-patient-root-selector.spec.mjs', import.meta.url), 'utf8');

test('plain Patient root selector native case has its distinct scenario, case, and exact title', () => {
  assert.match(spec, /cdaScenarioID:\s*'cda-plain-patient-root-selector'/);
  assert.match(spec, /cdaCaseName:\s*'select-observation-directly-and-restore-patient'/);
  assert.match(spec, /select Observation directly from a plain Patient table and restore Patient/);
});

test('plain Patient root recognition rejects an authored descendant', () => {
  assert.equal(isPlainPatientRoot({
    rootResourceType: 'Patient',
    route: { resourceType: 'Patient', children: [] },
  }), true);
  assert.equal(isPlainPatientRoot({
    rootResourceType: 'Patient',
    route: { resourceType: 'Patient', children: [{ resourceType: 'Observation' }] },
  }), false);
});

test('nondefault Patient selection creation requires the API 201 Created response', () => {
  const selection = {
    id: 'selection-1',
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
    resourceType: 'Patient',
    memberCount: 1,
    complete: true,
  };
  assert.doesNotThrow(() => assertPatientSelectionCreated({ status: 201 }, selection));
  assert.throws(() => assertPatientSelectionCreated({ status: 200 }, selection), /201/);
});

test('Observation selection uses the unique Patient Subject path when the catalog exposes alternatives', () => {
  const options = [
    { value: 'disabled', label: 'Observation via Encounter', disabled: true },
    { value: 'patient-focus', label: 'Observation via Patient → Focus → Observation', disabled: false },
    { value: 'patient-subject', label: 'Observation via Patient → Subject → Observation', disabled: false },
  ];
  assert.deepEqual(choosePatientLinkedObservationOption(options), options[2]);
  assert.throws(() => choosePatientLinkedObservationOption([
    options[2],
    { value: 'alternate', label: 'Observation via Patient → Other Subject → Observation', disabled: false },
  ]), /Expected one enabled Observation choice/);
});

test('catalog-only row assessment matches the selected Observation node without requiring a route token', () => {
  const expected = {
    outputId: 'table-1',
    snapshotToken: 'snapshot-1',
    draftVersion: 4,
    draftDigest: 'digest-4',
    rootNodeId: 'observation-node',
  };
  const entry = {
    path: '/authoring/v2/row-change',
    method: 'POST',
    status: 200,
    request: {
      outputId: expected.outputId,
      snapshotToken: expected.snapshotToken,
      draftVersion: expected.draftVersion,
      draftDigest: expected.draftDigest,
      rootNodeId: expected.rootNodeId,
    },
    response: {
      snapshotToken: expected.snapshotToken,
      draftVersion: expected.draftVersion,
      draftDigest: expected.draftDigest,
      currentRootResourceType: 'Patient',
      candidateRootResourceType: 'Observation',
      status: 'READY',
      proposal: { outputId: expected.outputId, rootNodeId: expected.rootNodeId },
    },
  };
  assert.equal(matchesPlainPatientObservationAssessment(entry, expected), true);
  assert.equal(matchesPlainPatientObservationAssessment({
    ...entry,
    request: { ...entry.request, rootOccurrenceId: 'opaque-selected-route' },
    response: { ...entry.response, proposal: { ...entry.response.proposal, rootOccurrenceId: 'opaque-selected-route' } },
  }, expected), true);
  assert.equal(matchesPlainPatientObservationAssessment({
    ...entry,
    request: { ...entry.request, rootNodeId: 'another-node' },
  }, expected), false);
  assert.equal(matchesPlainPatientObservationAssessment({
    ...entry,
    response: { ...entry.response, status: 'BLOCKED' },
  }, expected), false);
});

test('ambiguous direct root selection requires the catalog-labelled Patient Subject repair and accepts its READY assessment', () => {
  const expected = {
    outputId: 'table-1',
    snapshotToken: 'snapshot-1',
    draftVersion: 4,
    draftDigest: 'digest-4',
    rootNodeId: 'observation-node',
  };
  const blocked = {
    path: '/authoring/v2/row-change',
    method: 'POST',
    status: 200,
    request: {
      outputId: expected.outputId,
      snapshotToken: expected.snapshotToken,
      draftVersion: expected.draftVersion,
      draftDigest: expected.draftDigest,
      rootNodeId: expected.rootNodeId,
    },
    response: {
      snapshotToken: expected.snapshotToken,
      draftVersion: expected.draftVersion,
      draftDigest: expected.draftDigest,
      currentRootResourceType: 'Patient',
      candidateRootResourceType: 'Observation',
      status: 'BLOCKED',
      unresolved: [{
        code: 'AMBIGUOUS_ROW_ROOT_OCCURRENCE',
        alternatives: ['catalog-choice-one', 'catalog-choice-two'],
      }],
    },
  };
  assert.equal(matchesPlainPatientObservationAssessment(blocked, expected, { status: 'BLOCKED' }), true);
  assert.equal(matchesPlainPatientObservationAssessment({
    ...blocked,
    request: { ...blocked.request, rootOccurrenceId: 'selected-route-token' },
  }, expected, { status: 'BLOCKED' }), true);
  assert.equal(matchesPlainPatientObservationAssessment({
    ...blocked,
    response: { ...blocked.response, unresolved: [{ code: 'OTHER', alternatives: ['opaque'] }] },
  }, expected, { status: 'BLOCKED' }), false);

  const buttons = [
    { text: 'Use Patient → Focus → Observation from Patient rows', disabled: false },
    { text: 'Use Patient → Subject → Observation from Patient rows', disabled: false },
    { text: 'Use Patient → Subject → Observation from Patient rows', disabled: true },
  ];
  assert.deepEqual(choosePatientSubjectObservationRepairButton(buttons), buttons[1]);
  assert.throws(() => choosePatientSubjectObservationRepairButton([
    { text: 'Use Patient → Subject → Observation from Patient rows', disabled: true },
  ]), /Expected one enabled Patient Subject Observation repair button/);
});

test('restoration repair selects the current inverse Subject label between Observation and Patient', () => {
  const buttons = [
    { text: 'Match through Encounter between Observation and Patient', disabled: false },
    { text: 'Match through Subject between Observation and Patient', disabled: false },
    { text: 'Match through Subject between Observation and Patient', disabled: true },
    { text: 'Patient through subject_Observation', disabled: false },
  ];
  assert.deepEqual(choosePatientObservationSubjectRestorationButton(buttons), buttons[1]);
  assert.throws(() => choosePatientObservationSubjectRestorationButton([
    { text: 'Match through Subject', disabled: false },
  ]), /Expected one enabled Subject route from Observation to Patient/);
});

test('native starting collection chooses exactly one direct Patient route', () => {
  const direct = { route: [], routeChoiceId: 'same-patient' };
  assert.deepEqual(chooseDirectPatientPopulationRoute([
    { route: [{ relationship: 'subject_Patient' }], routeChoiceId: 'related' },
    direct,
  ]), direct);
  assert.throws(() => chooseDirectPatientPopulationRoute([
    direct,
    { route: [], routeChoiceId: 'duplicate-direct' },
  ]), /Expected one direct Patient population route/);
});

test('direct Observation rebase preserves the Patient field, active filter, and starting collection', () => {
  const before = {
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient', children: [] },
    output: { id: 'table-1', title: 'Patient observations' },
    columns: [{
      columnId: 'patient-id',
      occurrenceId: 'base',
      label: 'Patient ID',
      source: { kind: 'field', field: { path: 'id' } },
    }],
    construction: {
      version: 1,
      steps: [{
        id: 'patient-filter',
        operation: { kind: 'FILTER', filter: { columnId: 'patient-id', operator: 'EQUALS', values: ['patient-1'] } },
      }],
    },
    population: {
      selectionRevisionId: 'selection-1',
      route: [{ resourceType: 'Patient', relationship: 'selected-members' }],
    },
  };
  const after = {
    ...structuredClone(before),
    rootResourceType: 'Observation',
    route: {
      occurrenceId: 'base', resourceType: 'Observation',
      children: [{ occurrenceId: 'patient-occurrence', resourceType: 'Patient', children: [] }],
    },
    columns: [{ ...before.columns[0], occurrenceId: 'patient-occurrence' }],
    population: {
      ...before.population,
      route: [
        { resourceType: 'Observation', relationship: 'subject_Patient' },
        ...before.population.route,
      ],
    },
  };
  assert.equal(directObservationRootPreservesPatientState(before, after), true);
  const changedFilter = structuredClone(after);
  changedFilter.construction.steps[0].operation.filter.values = ['patient-2'];
  assert.equal(directObservationRootPreservesPatientState(before, changedFilter), false);
  const changedCollection = structuredClone(after);
  changedCollection.population.selectionRevisionId = 'selection-2';
  assert.equal(directObservationRootPreservesPatientState(before, changedCollection), false);
});
