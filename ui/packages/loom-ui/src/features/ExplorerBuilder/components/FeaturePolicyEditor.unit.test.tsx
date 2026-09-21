// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ContributorWindow, ExplorerBuilderCandidate, ExplorerBuilderColumn } from '../../../types';
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
  aggregateOperations: [
    { operation: 'COUNT', rowContext: 'RECORDS', supported: true, resultLogicalType: 'integer', resultCardinality: 'ONE' },
    { operation: 'EXISTS', rowContext: 'RECORDS', supported: true, resultLogicalType: 'boolean', resultCardinality: 'ONE' },
    { operation: 'MIN', rowContext: 'RECORDS', supported: true, resultLogicalType: 'decimal', resultCardinality: 'OPTIONAL_ONE' },
    { operation: 'MAX', rowContext: 'RECORDS', supported: true, resultLogicalType: 'decimal', resultCardinality: 'OPTIONAL_ONE' },
    { operation: 'SUM', rowContext: 'RECORDS', supported: true, resultLogicalType: 'decimal', resultCardinality: 'OPTIONAL_ONE' },
    { operation: 'MEAN', rowContext: 'RECORDS', supported: true, resultLogicalType: 'decimal', resultCardinality: 'OPTIONAL_ONE' },
    {
      operation: 'FIRST_ORDERED',
      rowContext: 'RECORDS',
      supported: true,
      resultLogicalType: 'decimal',
      resultCardinality: 'OPTIONAL_ONE',
      requiresConfiguration: ['temporal'],
    },
  ],
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
    unitNormalization: {
      available: true,
      presets: [{
        policyId: 'to-centimeters',
        version: '2',
        target: { system: 'http://unitsofmeasure.org', code: 'cm' },
        available: true,
      }],
    },
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
      contributorWindow: {
        timestampPath: 'effectiveDateTime',
        anchorPath: 'meta.lastUpdated',
        lowerOffsetSeconds: -172_800,
        upperOffsetSeconds: 0,
        lowerInclusive: true,
        upperInclusive,
        precision: 'INSTANT',
      },
      ordering: {
        timestampPath: 'effectiveDateTime',
        direction: 'DESC',
        tiePolicy: 'REQUIRE_UNIQUE',
      },
    },
  },
}) satisfies ExplorerBuilderColumn;

const sumColumn = (window?: ContributorWindow) => ({
  column: 'observation_sum',
  label: 'Observation sum',
  logicalType: 'decimal',
  occurrenceId: 'root',
  source: {
    kind: 'aggregate',
    aggregate: {
      operation: 'SUM',
      path: 'valueQuantity.value',
      ...(window ? { contributorWindow: window } : {}),
      unitNormalization: { policyId: 'to-centimeters', version: '2' },
    },
  },
}) satisfies ExplorerBuilderColumn;

const savedWindow: ContributorWindow = {
  timestampPath: 'effectiveDateTime',
  anchorPath: 'meta.lastUpdated',
  lowerOffsetSeconds: -172_800,
  upperOffsetSeconds: 0,
  lowerInclusive: true,
  upperInclusive: false,
  precision: 'INSTANT',
};

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

    fireEvent.click(screen.getByRole('button', { name: 'Edit date window' }));
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
        contributorWindow: {
          timestampPath: 'effectiveDateTime',
          anchorPath: 'meta.lastUpdated',
          lowerOffsetSeconds: -172_800,
          upperOffsetSeconds: 0,
          lowerInclusive: true,
          upperInclusive: false,
          precision: 'INSTANT',
        },
        ordering: {
          timestampPath: 'effectiveDateTime',
          direction: 'DESC',
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
    expect(observationSettings.getByRole('combobox', { name: 'Date selection direction' })).toBeTruthy();
    expect(observationSettings.getByRole('combobox', { name: 'Equal date handling' })).toBeTruthy();
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
        contributorWindow: {
          timestampPath: 'effectiveDateTime',
          anchorPath: 'meta.lastUpdated',
          lowerOffsetSeconds: -172_800,
          upperOffsetSeconds: 0,
          lowerInclusive: true,
          upperInclusive: false,
          precision: 'INSTANT',
        },
        ordering: {
          timestampPath: 'effectiveDateTime',
          direction: 'DESC',
          tiePolicy: 'REQUIRE_UNIQUE',
        },
      },
    });
    expect(onOtherSourceChange).not.toHaveBeenCalled();
  });

  it('authors an exact date window on a related SUM without changing its unit preset', () => {
    const onSourceChange = vi.fn();
    render(
      <FeaturePolicyEditor
        column={sumColumn()}
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

    fireEvent.click(screen.getByRole('button', { name: 'Add date window' }));
    expect(screen.queryByRole('combobox', { name: 'Date selection direction' })).toBeNull();
    expect(screen.queryByRole('combobox', { name: 'Equal date handling' })).toBeNull();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Look back days' }), {
      target: { value: '3' },
    });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Include end boundary' }));
    fireEvent.click(screen.getByRole('button', { name: 'Apply date window' }));

    expect(onSourceChange).toHaveBeenCalledWith({
      kind: 'aggregate',
      aggregate: {
        operation: 'SUM',
        path: 'valueQuantity.value',
        contributorWindow: {
          timestampPath: 'effectiveDateTime',
          anchorPath: 'meta.lastUpdated',
          lowerOffsetSeconds: -259_200,
          upperOffsetSeconds: 0,
          lowerInclusive: true,
          upperInclusive: false,
          precision: 'INSTANT',
        },
        unitNormalization: { policyId: 'to-centimeters', version: '2' },
      },
    });
  });

  it('keeps a compatible window and normalization when changing SUM to MEAN', () => {
    const onSourceChange = vi.fn();
    render(
      <FeaturePolicyEditor
        column={sumColumn(savedWindow)}
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

    fireEvent.change(screen.getByRole('combobox', { name: 'Across related Patient records for Observation sum' }), {
      target: { value: 'MEAN' },
    });

    expect(onSourceChange).toHaveBeenCalledWith({
      kind: 'aggregate',
      aggregate: {
        operation: 'MEAN',
        path: 'valueQuantity.value',
        contributorWindow: savedWindow,
        unitNormalization: { policyId: 'to-centimeters', version: '2' },
      },
    });
  });

  it('keeps a compatible window and drops normalization when changing SUM to COUNT', () => {
    const onSourceChange = vi.fn();
    render(
      <FeaturePolicyEditor
        column={sumColumn(savedWindow)}
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

    fireEvent.change(screen.getByRole('combobox', { name: 'Across related Patient records for Observation sum' }), {
      target: { value: 'COUNT' },
    });

    expect(onSourceChange).toHaveBeenCalledWith({
      kind: 'aggregate',
      aggregate: {
        operation: 'COUNT',
        path: 'valueQuantity.value',
        contributorWindow: savedWindow,
      },
    });
  });

  it('keeps the window and removes ordering when changing FIRST_ORDERED to SUM', () => {
    const onSourceChange = vi.fn();
    render(
      <FeaturePolicyEditor
        column={temporalColumn('observation_value', 'Observation value', 'valueQuantity.value', false)}
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

    fireEvent.change(screen.getByRole('combobox', { name: 'Across related Patient records for Observation value' }), {
      target: { value: 'SUM' },
    });

    expect(onSourceChange).toHaveBeenCalledWith({
      kind: 'aggregate',
      aggregate: {
        operation: 'SUM',
        path: 'valueQuantity.value',
        contributorWindow: savedWindow,
      },
    });
  });

  it('does not offer unit normalization for COUNT or EXISTS', () => {
    for (const operation of ['COUNT', 'EXISTS'] as const) {
      const view = render(
        <FeaturePolicyEditor
          column={{
            column: 'observation_count',
            label: 'Observation count',
            logicalType: 'integer',
            occurrenceId: 'root',
            source: { kind: 'aggregate', aggregate: { operation, path: 'valueQuantity.value' } },
          }}
          candidate={temporalCandidate}
          candidates={[]}
          related
          rowContext="RECORDS"
          resourceLabel="Patient"
          disabled={false}
          onSourceChange={vi.fn()}
          onTransformationChange={vi.fn()}
          onContributorChange={vi.fn()}
        />,
      );

      expect(screen.queryByRole('button', { name: 'Normalize units' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Edit unit normalization' })).toBeNull();
      view.unmount();
    }
  });

  it('does not persist a canceled date-window edit', () => {
    const onSourceChange = vi.fn();
    const savedColumn = sumColumn(savedWindow);
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

    fireEvent.click(screen.getByRole('button', { name: 'Edit date window' }));
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Look back days' }), {
      target: { value: '9' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel date window' }));

    expect(onSourceChange).not.toHaveBeenCalled();
    expect(savedColumn.source).toEqual(sumColumn(savedWindow).source);
  });

  it('shows the exact temporal capability refusal for a related reducer', () => {
    const reason = 'This row has no date field that can anchor contributors.';
    const unsupportedCandidate = {
      ...temporalCandidate,
      transformations: {
        ...temporalCandidate.transformations,
        temporalReduction: {
          ...temporalCandidate.transformations.temporalReduction,
          available: false,
          reason,
        },
      },
    } satisfies ExplorerBuilderCandidate;
    render(
      <FeaturePolicyEditor
        column={sumColumn()}
        candidate={unsupportedCandidate}
        candidates={[]}
        related
        rowContext="RECORDS"
        resourceLabel="Patient"
        disabled={false}
        onSourceChange={vi.fn()}
        onTransformationChange={vi.fn()}
        onContributorChange={vi.fn()}
      />,
    );

    expect(within(screen.getByRole('group', { name: 'Time and units' }))
      .getByRole('status').textContent).toBe(`Unavailable: ${reason}`);
    expect(screen.getByRole('button', { name: 'Add date window' })).toHaveProperty('disabled', true);
  });
});
