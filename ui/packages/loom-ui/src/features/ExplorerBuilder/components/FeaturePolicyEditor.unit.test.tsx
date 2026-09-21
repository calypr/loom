// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCandidate, ExplorerBuilderColumn } from '../../../types';
import { FeaturePolicyEditor } from './FeaturePolicyEditor';

const column = {
  column: 'weight',
  label: 'Weight',
  logicalType: 'decimal',
  occurrenceId: 'root',
  source: { kind: 'field' as const, field: { path: 'root.weight' } },
} satisfies ExplorerBuilderColumn;

const temporalCandidate = {
  candidateId: 'c_value',
  nodeId: 'observation',
  fieldPath: 'valueQuantity.value',
  label: 'Observation value',
  logicalType: 'decimal',
  cardinality: 'optional_one',
  repeated: false,
  filterable: true,
  chartable: true,
  projectionModes: ['VALUE'],
  defaultProjectionMode: 'VALUE',
  aggregateOperations: [{
    operation: 'FIRST_ORDERED',
    rowContext: 'RECORDS',
    supported: true,
    resultLogicalType: 'decimal',
    resultCardinality: 'OPTIONAL_ONE',
    requiresConfiguration: ['temporal'],
  }],
  transformations: {
    temporalReduction: {
      available: true,
      timestampFields: [{
        candidateId: 'c_timestamp',
        nodeId: 'observation',
        resourceType: 'Observation',
        fieldPath: 'effectiveDateTime',
        label: 'Observed at',
      }],
      anchorFields: [{
        candidateId: 'c_anchor',
        nodeId: 'patient',
        resourceType: 'Patient',
        fieldPath: 'meta.lastUpdated',
        label: 'Row updated at',
      }],
    },
    unitNormalization: { available: false, presets: [] },
  },
  valueTransformations: {
    exactCategoryRecode: { available: false },
    codedValueRecoding: { available: false },
  },
} satisfies ExplorerBuilderCandidate;

const temporalColumn = (
  columnId: string,
  label: string,
  path: string,
  upperInclusive: boolean,
) => ({
  column: columnId,
  label,
  logicalType: 'decimal',
  occurrenceId: 'root',
  source: {
    kind: 'aggregate' as const,
    aggregate: {
      operation: 'FIRST_ORDERED' as const,
      path,
      temporal: {
        timestampPath: 'effectiveDateTime',
        anchorPath: 'meta.lastUpdated',
        lowerOffsetSeconds: -172_800,
        upperOffsetSeconds: 0,
        lowerInclusive: true,
        upperInclusive,
        direction: 'DESC' as const,
        precision: 'INSTANT' as const,
        tiePolicy: 'REQUIRE_UNIQUE' as const,
      },
    },
  },
}) satisfies ExplorerBuilderColumn;

const relatedFieldColumn = (columnId: string, label: string) => ({
  column: columnId,
  label,
  logicalType: 'decimal',
  occurrenceId: 'observations',
  source: {
    kind: 'field' as const,
    field: {
      path: 'valueQuantity.value',
      projectionMode: 'VALUE' as const,
      relatedSelection: {
        kind: 'first-by-resource-key' as const,
        acknowledged: false,
      },
    },
  },
}) satisfies ExplorerBuilderColumn;

describe('FeaturePolicyEditor', () => {
  it('groups value settings separately from server-resolved time and unit settings', () => {
    render(
      <FeaturePolicyEditor
        column={column}
        candidate={undefined}
        candidates={[]}
        related={false}
        resourceLabel="Patient"
        disabled={false}
        onSourceChange={vi.fn()}
        onTransformationChange={vi.fn()}
        onContributorChange={vi.fn()}
      />,
    );

    expect(screen.getByRole('group', { name: 'Values' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Time and units' })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Values' }).textContent)
      .toContain('Category recoding is unavailable until the server resolves this column capability.');
    expect(screen.getByRole('group', { name: 'Time and units' }).textContent)
      .toContain('Choose a date-aware value selection to configure its time window here.');
  });

  it('reloads a saved exclusive end boundary and reapplies the saved temporal values', () => {
    const onSourceChange = vi.fn();
    const savedColumn = temporalColumn(
      'observation_value',
      'Observation value',
      'valueQuantity.value',
      false,
    );

    render(
      <FeaturePolicyEditor
        column={savedColumn}
        candidate={temporalCandidate}
        candidates={[]}
        related
        rowContext="RECORDS"
        resourceLabel="Patient"
        disabled={false}
        onSourceChange={onSourceChange}
        onTransformationChange={vi.fn()}
        onContributorChange={vi.fn()}
      />,
    );

    expect(screen.getByRole('checkbox', { name: 'Include start boundary' }))
      .toHaveProperty('checked', true);
    expect(screen.getByRole('checkbox', { name: 'Include end boundary' }))
      .toHaveProperty('checked', false);
    expect(screen.getByRole('spinbutton', { name: 'Look back days' }))
      .toHaveProperty('value', '2');
    expect(screen.getByText(/start is inclusive and end is exclusive\./).textContent)
      .toContain('from 2 days before the row date to the row date');

    fireEvent.click(screen.getByRole('button', { name: 'Apply date selection' }));

    expect(onSourceChange).toHaveBeenCalledOnce();
    expect(onSourceChange).toHaveBeenCalledWith({
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        temporal: {
          timestampPath: 'effectiveDateTime',
          anchorPath: 'meta.lastUpdated',
          lowerOffsetSeconds: -172_800,
          upperOffsetSeconds: 0,
          lowerInclusive: true,
          upperInclusive: false,
          direction: 'DESC',
          precision: 'INSTANT',
          tiePolicy: 'REQUIRE_UNIQUE',
        },
      },
    });
  });

  it('authors an inclusive start and exclusive end for one column only', () => {
    const onObservationSourceChange = vi.fn();
    const onOtherSourceChange = vi.fn();
    const observationColumn = relatedFieldColumn('observation_value', 'Observation value');
    const otherColumn = relatedFieldColumn('other_value', 'Other value');

    render(
      <>
        <FeaturePolicyEditor
          column={observationColumn}
          candidate={temporalCandidate}
          candidates={[]}
          related
          rowContext="RECORDS"
          resourceLabel="Patient"
          disabled={false}
          onSourceChange={onObservationSourceChange}
          onTransformationChange={vi.fn()}
          onContributorChange={vi.fn()}
        />
        <FeaturePolicyEditor
          column={otherColumn}
          candidate={temporalCandidate}
          candidates={[]}
          related
          rowContext="RECORDS"
          resourceLabel="Patient"
          disabled={false}
          onSourceChange={onOtherSourceChange}
          onTransformationChange={vi.fn()}
          onContributorChange={vi.fn()}
        />
      </>,
    );

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Across related Patient records for Observation value',
    }), { target: { value: 'FIRST_ORDERED' } });

    const [observationTimeSettings, otherTimeSettings] = screen.getAllByTestId('feature-policy-time-units');
    const observationSettings = within(observationTimeSettings);
    const otherSettings = within(otherTimeSettings);
    const startBoundary = observationSettings.getByRole('checkbox', { name: 'Include start boundary' });
    const endBoundary = observationSettings.getByRole('checkbox', { name: 'Include end boundary' });

    expect(startBoundary).toHaveProperty('checked', true);
    expect(endBoundary).toHaveProperty('checked', true);
    fireEvent.change(observationSettings.getByRole('spinbutton', { name: 'Look back days' }), {
      target: { value: '2' },
    });
    fireEvent.click(endBoundary);
    expect(startBoundary).toHaveProperty('checked', true);
    expect(endBoundary).toHaveProperty('checked', false);
    expect(observationSettings.getByText(/start is inclusive and end is exclusive\./)).toBeTruthy();
    expect(otherSettings.queryByRole('button', { name: 'Apply date selection' })).toBeNull();

    fireEvent.click(observationSettings.getByRole('button', { name: 'Apply date selection' }));

    expect(onObservationSourceChange).toHaveBeenCalledOnce();
    expect(onObservationSourceChange).toHaveBeenCalledWith({
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        temporal: {
          timestampPath: 'effectiveDateTime',
          anchorPath: 'meta.lastUpdated',
          lowerOffsetSeconds: -172_800,
          upperOffsetSeconds: 0,
          lowerInclusive: true,
          upperInclusive: false,
          direction: 'DESC',
          precision: 'INSTANT',
          tiePolicy: 'REQUIRE_UNIQUE',
        },
      },
    });
    expect(onOtherSourceChange).not.toHaveBeenCalled();
  });
});
