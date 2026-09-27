// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type {
  ConstructionChoice,
  ExplorerBuilderCandidate,
  FieldChoiceSource,
} from '../../../types';
import type { CatalogChoiceGroup, CatalogItem } from '../catalogItems';
import { CatalogSelectionDialog } from './CatalogSelectionDialog';

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

const routeChoice = (
  choiceId: string,
  relationship: string,
  options: ReadonlyArray<ConstructionChoice['options'][number]> = [countOption],
): FieldChoice => ({
  choiceId,
  source: {
    kind: 'FIELD',
    candidateId: 'observation-id',
    nodeId: 'observation-node',
    resourceType: 'Observation',
    path: 'id',
    cardinality: 'optional_one',
  },
  route: [{
    edgeId: `${choiceId}-edge`,
    fromNodeId: 'patient-node',
    toNodeId: 'observation-node',
    fromResourceType: 'Patient',
    toResourceType: 'Observation',
    relationship,
    storageDirection: 'INBOUND',
    matchMode: 'OPTIONAL',
  }],
  presentation: {
    summary: `Observation.id through ${relationship}`,
    facts: [{ label: 'Relationship', value: relationship }],
  },
  options: [...options],
});

const createDialog = (focusOperators: ConstructionChoice['options'][number]['contributorPredicateOperators']) => {
  const subjectChoice = routeChoice('saved-subject-choice', 'subject_Patient');
  const focusChoice = routeChoice('focus-choice', 'focus_Patient', [{
    ...countOption,
    contributorPredicateOperators: focusOperators,
  }]);
  const candidate: ExplorerBuilderCandidate = {
    candidateId: 'observation-id',
    nodeId: 'observation-node',
    fieldPath: 'id',
    label: 'Observation ID',
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

  render(
    <CatalogSelectionDialog
      groups={[group]}
      initialSelection={{
        choiceId: subjectChoice.choiceId,
        form: 'COUNT',
        condition: { mode: 'EQUALS', value: 'known-observation-id' },
      }}
      busy={false}
      onLoadMoreRoutes={vi.fn()}
      onCancel={vi.fn()}
      onConfirm={onConfirm}
    />,
  );

  return { focusChoice, onConfirm };
};

describe('CatalogSelectionDialog', () => {
  it('keeps a saved COUNT equality condition when the new route supports it', async () => {
    const { focusChoice, onConfirm } = createDialog(['EXISTS', 'EQUALS']);
    const dialog = screen.getByRole('dialog', { name: 'Choose how to add these fields' });

    const count = within(dialog).getByRole('radio', { name: 'Observation ID: Count matching records' });
    const equals = within(dialog).getByRole('radio', { name: 'Only records where Observation ID equals' });
    expect(count).toHaveProperty('checked', true);
    expect(equals).toHaveProperty('checked', true);

    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Observation ID: Direct relationship: Patient to Observation via Focus',
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
    fireEvent.click(within(dialog).getByRole('radio', {
      name: 'Observation ID: Direct relationship: Patient to Observation via Focus',
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
});
