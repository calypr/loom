// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type {
  ConstructionChoice,
  ExplorerBuilderCandidate,
  FieldChoiceSource,
} from '../../../types';
import type { CatalogChoiceGroup, CatalogChoiceIntent, CatalogItem } from '../catalogItems';
import {
  CatalogSelectionDialog,
  type GroupedRowValuePolicyControl,
  type RouteMatchCoverage,
  type RouteCoverage,
} from './CatalogSelectionDialog';

type FieldChoice = ConstructionChoice & { readonly source: FieldChoiceSource };

const countOption: ConstructionChoice['options'][number] = {
  form: 'COUNT',
  shape: 'SCALAR',
  decision: 'REQUIRES_DECISION',
  preservation: 'REDUCING',
  rowEffect: 'PRESERVES_ROW_GRAIN',
  support: 'SUPPORTED',
  reason: 'Count distinct matching Observation records.',
  contributorPredicateOperators: ['EXISTS', 'EQUALS'],
};

const allOption: ConstructionChoice['options'][number] = {
  ...countOption,
  form: 'ALL',
  shape: 'LIST',
  preservation: 'PRESERVING',
};

const presenceOption: ConstructionChoice['options'][number] = {
  ...countOption,
  form: 'PRESENCE',
  reason: 'Show whether a matching Observation record exists.',
};

const routeChoice = (
  choiceId: string,
  relationship: string,
  options: ReadonlyArray<ConstructionChoice['options'][number]> = [countOption],
  routeMetadata: Partial<ConstructionChoice['route'][number]> = {},
  direct = false,
  routeOverride?: ConstructionChoice['route'],
): FieldChoice => ({
  choiceId,
  source: {
    kind: 'FIELD',
    candidateId: 'observation-id',
    nodeId: direct ? 'patient-node' : routeMetadata.toNodeId ?? 'observation-node',
    resourceType: direct ? 'Patient' : routeMetadata.toResourceType ?? 'Observation',
    path: 'id',
    cardinality: 'optional_one',
  },
  route: direct ? [] : routeOverride ?? [{
    edgeId: `${choiceId}-edge`,
    fromNodeId: routeMetadata.fromNodeId ?? 'patient-node',
    toNodeId: routeMetadata.toNodeId ?? 'observation-node',
    fromResourceType: routeMetadata.fromResourceType ?? 'Patient',
    toResourceType: routeMetadata.toResourceType ?? 'Observation',
    relationship: routeMetadata.relationship ?? relationship,
    storageDirection: routeMetadata.storageDirection ?? 'INBOUND',
    matchMode: 'OPTIONAL',
  }],
  presentation: {
    summary: `Observation.id through ${relationship}`,
    facts: [{ label: 'Relationship', value: relationship }],
  },
  options: [...options],
});

const createDialog = (
  focusOperators: ConstructionChoice['options'][number]['contributorPredicateOperators'],
  routeMetadata: Partial<ConstructionChoice['route'][number]> = {},
  withSavedCondition = true,
  onInspectRouteCoverage?: (selection: CatalogChoiceIntent, signal: AbortSignal) => Promise<RouteCoverage>,
  insideClosedDetails = false,
  groupedRowValuePolicy?: GroupedRowValuePolicyControl,
  options: ReadonlyArray<ConstructionChoice['options'][number]> = [countOption],
  initialForm: 'COUNT' | 'ALL' = 'COUNT',
  directField = false,
  rowRoot = 'Patient',
  savedChoiceRoute?: ConstructionChoice['route'],
) => {
  const subjectChoice = routeChoice('saved-subject-choice', 'subject_Patient', options, routeMetadata, directField, savedChoiceRoute);
  const focusChoice = routeChoice('focus-choice', 'focus_Patient', options.map(option => ({
    ...option,
    contributorPredicateOperators: focusOperators,
  })), routeMetadata, directField);
  const resourceType = directField ? 'Patient' : routeMetadata.toResourceType ?? 'Observation';
  const candidate: ExplorerBuilderCandidate = {
    candidateId: 'observation-id',
    nodeId: directField ? 'patient-node' : routeMetadata.toNodeId ?? 'observation-node',
    fieldPath: 'id',
    label: `${resourceType} ID`,
    logicalType: 'string',
    cardinality: 'optional_one',
    repeated: false,
    filterable: true,
    chartable: false,
    projectionModes: ['VALUE'],
    defaultProjectionMode: 'VALUE',
    constructionChoice: subjectChoice,
    aggregateOperations: [],
    transformations: {
      temporalReduction: {
        available: false,
        reason: 'No temporal reduction is available.',
        timestampFields: [],
        anchorFields: [],
      },
      unitNormalization: {
        available: false,
        reason: 'No unit normalization is available.',
        presets: [],
      },
    },
    valueTransformations: {
      exactCategoryRecode: { available: false },
      codedValueRecoding: { available: false },
    },
  };
  const item: CatalogItem = {
    kind: 'FIELD',
    candidate,
    constructionChoice: subjectChoice,
  };
  const group: CatalogChoiceGroup = {
    item,
    choices: [subjectChoice, focusChoice],
    complete: true,
    truncated: false,
  };
  const onConfirm = vi.fn();

  const dialog = (
    <CatalogSelectionDialog
      groups={[group]}
      rowRoot={rowRoot}
      initialSelection={{
        choiceId: subjectChoice.choiceId,
        form: initialForm,
        ...(withSavedCondition ? { condition: { mode: 'EQUALS' as const, value: 'known-observation-id' } } : {}),
      }}
      groupedRowValuePolicy={groupedRowValuePolicy}
      busy={false}
      onLoadMoreRoutes={vi.fn()}
      onInspectRouteCoverage={onInspectRouteCoverage}
      onCancel={vi.fn()}
      onConfirm={onConfirm}
    />
  );
  render(insideClosedDetails ? <details><summary>Source setup</summary>{dialog}</details> : dialog);

  return { subjectChoice, focusChoice, onConfirm };
};

describe('CatalogSelectionDialog', () => {
  it('lets users change grouped value policy without losing the selected feature', async () => {
    const onChange = vi.fn();
    const { onConfirm } = createDialog(['EXISTS', 'EQUALS'], {}, false, undefined, false, {
      value: 'ONE',
      onChange,
    });
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });
    const policy = within(dialog).getByRole('combobox', { name: 'Values per grouped row' });

    expect(policy).toHaveProperty('value', 'ONE');
    fireEvent.change(policy, { target: { value: 'ALL' } });
    expect(onChange).toHaveBeenCalledWith('ALL');

    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));
    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith([expect.objectContaining({
      constructionChoice: { choiceId: 'saved-subject-choice', form: 'COUNT' },
    })]));
  });

  it('keeps the dialog visible when opened from a collapsed source disclosure', () => {
    createDialog(['EXISTS', 'EQUALS'], {}, false, undefined, true);
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });
    expect(dialog.closest('details')).toBeNull();
    expect(dialog.parentElement).toBe(document.body.querySelector('[role="presentation"]'));
  });

  it('compares matching records on the visible direct routes without selecting one', async () => {
    const inspect = vi.fn(async (selection: CatalogChoiceIntent): Promise<RouteMatchCoverage> =>
      selection.constructionChoice.choiceId === 'saved-subject-choice'
        ? { zero: 0, one: 0, many: 1, displayedRows: 1, sampled: true }
        : { zero: 0, one: 1, many: 0, displayedRows: 1, sampled: true });
    createDialog(['EXISTS', 'EQUALS'], {}, false, inspect);

    await waitFor(() => expect(inspect).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('catalog-route-coverage-saved-subject-choice')).toHaveTextContent(
      '1 displayed row: 0 with no match, 0 with one, 1 with two or more. This is a sample; full-table coverage has not been measured.',
    );
    expect(screen.getByTestId('catalog-route-coverage-focus-choice')).toHaveTextContent(
      '1 displayed row: 0 with no match, 1 with one, 0 with two or more. This is a sample; full-table coverage has not been measured.',
    );
    expect(inspect.mock.calls.every(([selection]) => selection.constructionChoice.form === 'COUNT')).toBe(true);
  });

  it('keeps a saved related ALL form when checking route coverage', async () => {
    const inspect = vi.fn(async (selection: CatalogChoiceIntent): Promise<RouteCoverage> =>
      selection.constructionChoice.form === 'ALL'
        ? { kind: 'VALUES', empty: 0, one: 1, many: 0, displayedRows: 1, sampled: true }
        : { zero: 0, one: 1, many: 0, displayedRows: 1, sampled: true });
    createDialog(['EXISTS', 'EQUALS'], {}, false, inspect, false, {
      value: 'ALL',
      onChange: vi.fn(),
    }, [allOption, countOption], 'ALL');

    await waitFor(() => expect(inspect).toHaveBeenCalledTimes(2));
    expect(inspect.mock.calls.map(([selection]) => selection.constructionChoice.form)).toEqual(['ALL', 'ALL']);
  });

  it('offers Observation.id PRESENCE on the named-cohort route and retains its exact route choice', () => {
    const namedCohortRoute: ConstructionChoice['route'] = [{
      edgeId: 'specimen-subject-patient',
      fromNodeId: 'specimen-node',
      toNodeId: 'patient-node',
      fromResourceType: 'Specimen',
      toResourceType: 'Patient',
      relationship: 'subject_Patient',
      storageDirection: 'OUTBOUND',
      matchMode: 'OPTIONAL',
    }, {
      edgeId: 'patient-observation-subject',
      fromNodeId: 'patient-node',
      toNodeId: 'observation-node',
      fromResourceType: 'Patient',
      toResourceType: 'Observation',
      relationship: 'subject_Patient',
      storageDirection: 'INBOUND',
      matchMode: 'OPTIONAL',
    }];
    const { subjectChoice, onConfirm } = createDialog(
      [], {}, false, undefined, false, undefined, [countOption, presenceOption], 'COUNT', false, 'Specimen', namedCohortRoute,
    );
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });

    expect(subjectChoice.route).toEqual(namedCohortRoute);
    expect(within(dialog).getByRole('radio', { name: 'Observation ID: Specimen -[subject]-> Patient <-[subject]- Observation' }))
      .toHaveProperty('checked', true);
    expect(within(dialog).getByRole('radio', { name: 'Observation ID: Show whether a match exists' }))
      .toBeInTheDocument();
    expect(within(dialog).getByText('Each row shows whether a match exists (false if none).'))
      .toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('radio', { name: 'Observation ID: Show whether a match exists' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));

    expect(onConfirm).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: subjectChoice.choiceId, form: 'PRESENCE' },
      title: 'Observation ID',
    }]);
  });

  it('keeps grouped root ALL as a root choice with its grouped value policy', async () => {
    const { onConfirm } = createDialog(['EXISTS', 'EQUALS'], {}, false, undefined, false, {
      value: 'ALL',
      onChange: vi.fn(),
    }, [allOption, countOption], 'COUNT', true);
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Patient ID: Keep all matching values' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));

    expect(onConfirm).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'saved-subject-choice', form: 'ALL' },
      title: 'Patient ID',
    }]);
    expect(screen.getByRole('combobox', { name: 'Values per grouped row' })).toHaveProperty('value', 'ALL');
  });

  it('keeps a saved COUNT equality condition when the new route supports it', async () => {
    const { focusChoice, onConfirm } = createDialog(['EXISTS', 'EQUALS']);
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });
    expect(within(dialog).getByText(/Matching records: only records where Observation ID equals known-observation-id/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByText(/Matching records: only records where Observation ID equals known-observation-id/));

    const count = within(dialog).getByRole('radio', { name: 'Observation ID: Count matching records' });
    const equals = within(dialog).getByRole('radio', { name: 'Only records where Observation ID equals' });
    expect(count).toHaveProperty('checked', true);
    expect(equals).toHaveProperty('checked', true);
    expect(within(dialog).getAllByTitle(/Observation\.subject/).length).toBeGreaterThan(0);
    expect(within(dialog).getByText('Each row gets the number of matching records (0 if none), counting each record once.'))
      .toBeInTheDocument();
    const alternateRoute = within(dialog).getByRole('radio', {
      name: 'Observation ID: Patient <-[focus]- Observation',
    });
    expect(alternateRoute.closest('details')).not.toHaveAttribute('open');
    fireEvent.click(within(dialog).getByText('Change relationship path (1 alternatives)'));
    expect(within(dialog).getByRole('radio', { name: 'Observation ID: Patient <-[focus]- Observation' })).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Observation ID: Patient <-[focus]- Observation',
    }));

    expect(count).toHaveProperty('checked', true);
    expect(within(dialog).getByRole('radio', { name: 'Only records where Observation ID equals' }))
      .toHaveProperty('checked', true);
    expect(within(dialog).getByRole('textbox', { name: 'Observation ID exact value' }))
      .toHaveProperty('value', 'known-observation-id');
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));

    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: focusChoice.choiceId, form: 'COUNT' },
      title: 'Observation ID',
      contributorPredicate: {
        candidateId: 'observation-id',
        operator: 'EQUALS',
        value: { kind: 'STRING', string: 'known-observation-id' },
      },
    }]));
  });

  it('blocks Add and requires explicit reset when a target route cannot keep the saved condition', () => {
    const { focusChoice, onConfirm } = createDialog([]);
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });
    fireEvent.click(within(dialog).getByText(/Matching records: only records where Observation ID equals known-observation-id/));
    fireEvent.click(within(dialog).getByText('Change relationship path (1 alternatives)'));
    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Observation ID: Patient <-[focus]- Observation',
    }));

    expect(within(dialog).getByRole('alert')).toHaveTextContent(/known-observation-id.*not supported/i);
    expect(within(dialog).getByRole('button', { name: 'Add 1 column' })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Use all related records' }));

    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Add 1 column' })).toBeEnabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add 1 column' }));
    expect(onConfirm).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: focusChoice.choiceId, form: 'COUNT' },
      title: 'Observation ID',
    }]);
  });

  it('explains route direction from metadata without assuming FHIR resource types', () => {
    createDialog(['EXISTS'], {
      fromNodeId: 'visit-node',
      toNodeId: 'sample-node',
      fromResourceType: 'StudyVisit',
      toResourceType: 'LabSample',
      relationship: 'testedBy_StudyVisit',
      storageDirection: 'OUTBOUND',
    });
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });

    expect(within(dialog).getAllByTitle(/StudyVisit\.testedBy/).length).toBeGreaterThan(0);
  });

  it('keeps all related records by default and places optional filters under Advanced', () => {
    createDialog(['EXISTS', 'EQUALS'], {}, false);
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });
    const matching = within(dialog).getByText('Matching records: all related records · Change').closest('details');
    expect(matching).not.toBeNull();
    if (!matching) return;
    expect(matching).not.toHaveAttribute('open');
    expect(within(dialog).getByText('Matching records: all related records · Change')).toBeInTheDocument();
    fireEvent.click(within(matching).getByText('Matching records: all related records · Change'));
    expect(within(matching).getByRole('radio', { name: 'All related records' })).toHaveProperty('checked', true);
  });
});


it('keeps a saved COUNT route on COUNT while grouped route coverage uses related ALL', async () => {
  const inspect = vi.fn(async (selection: CatalogChoiceIntent): Promise<RouteCoverage> =>
    selection.constructionChoice.form === 'ALL'
      ? { kind: 'VALUES', empty: 0, one: 0, many: 1, displayedRows: 1, sampled: false }
      : { zero: 0, one: 1, many: 0, displayedRows: 1, sampled: true });
  createDialog(['EXISTS', 'EQUALS'], {}, false, inspect, false,
    { value: 'ONE', onChange: vi.fn() },
    [{ ...countOption, form: 'ALL', shape: 'LIST' }, countOption]);
  await waitFor(() => expect(inspect).toHaveBeenCalledTimes(2));
  expect(inspect.mock.calls.map(([selection]) => selection.constructionChoice.form)).toEqual(['COUNT', 'ALL']);
  expect(screen.getByTestId('catalog-route-coverage-saved-subject-choice')).toHaveTextContent(
    '1 displayed row: 0 with no match, 1 with one, 0 with two or more. This is a sample; full-table coverage has not been measured.',
  );
  expect(screen.getByTestId('catalog-route-coverage-focus-choice')).toHaveTextContent(
    '1 displayed row: 0 without this value, 0 with one value, 1 with two or more values. This counts values from matching related records, not matching records.',
  );
  expect(screen.getByRole('combobox', { name: 'Values per grouped row' })).toHaveProperty('value', 'ONE');
});
