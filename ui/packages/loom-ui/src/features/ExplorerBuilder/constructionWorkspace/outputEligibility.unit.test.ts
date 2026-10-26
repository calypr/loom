import { describe, expect, it } from 'vitest';
import type { ExplorerBuilderDocument } from '../../../types';
import { effectiveOutputAvailability } from './outputEligibility';

const document = (
  columns: ExplorerBuilderDocument['columns'],
  construction?: ExplorerBuilderDocument['construction'],
  rows: ExplorerBuilderDocument['rows'] = { kind: 'RECORDS', records: {} },
): ExplorerBuilderDocument => ({
  kind: 'ExplorerBuilderDocument',
  output: { id: 'specimens', title: 'Specimens' },
  rootResourceType: 'Observation',
  route: { occurrenceId: 'base', resourceType: 'Observation' },
  rows,
  columns,
  ...(construction ? { construction } : {}),
});

const sourceColumn = (
  column: string,
  visible?: boolean,
): ExplorerBuilderDocument['columns'][number] => ({
  column,
  label: column,
  columnId: `${column}_id`,
  occurrenceId: 'base',
  source: { kind: 'field', field: { path: column, projectionMode: 'FIRST' } },
  ...(visible === undefined ? {} : { table: { visible, order: 0 } }),
});

const terminalStep = (
  outputs: NonNullable<ExplorerBuilderDocument['construction']>['steps'][number]['outputs'],
  rowValues?: NonNullable<ExplorerBuilderDocument['construction']>['steps'][number]['rowValues'],
): NonNullable<ExplorerBuilderDocument['construction']> => ({
  version: 1,
  steps: [{
    id: 'combine_step',
    inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: {
      kind: 'GROUP',
      group: {
        constructionId: 'group_construction',
        aggregates: outputs.map((candidate) => ({
          operation: 'COUNT_ROWS' as const,
          outputColumnId: candidate.id,
        })),
      },
    },
    outputs,
    ...(rowValues ? { rowValues } : {}),
  }],
  sourceProjections: [],
});

const terminalCombine = (
  outputs: NonNullable<ExplorerBuilderDocument['construction']>['steps'][number]['outputs'],
): NonNullable<ExplorerBuilderDocument['construction']> => ({
  version: 1,
  steps: [{
    id: 'combine_step',
    inputs: [
      { kind: 'WORKSPACE_OUTPUT', outputId: 'left' },
      { kind: 'WORKSPACE_OUTPUT', outputId: 'right' },
    ],
    operation: {
      kind: 'COMBINE',
      combine: {
        kind: 'APPEND',
        projections: [{
          outputColumnId: outputs[0]?.id ?? 'combined',
          inputIndex: 0,
          inputColumnId: 'left_status',
        }],
      },
    },
    outputs,
  }],
  sourceProjections: [],
});

const output = (
  id: string,
  name: string,
  visible?: boolean,
): NonNullable<ExplorerBuilderDocument['construction']>['steps'][number]['outputs'][number] => ({
  id,
  name,
  label: name,
  ...(visible === undefined ? {} : { table: { visible, order: 0 } }),
});

describe('effective output availability', () => {
  it('keeps an ordinary empty table ineligible', () => {
    expect(effectiveOutputAvailability(document([]))).toEqual({
      hasAnyOutputColumn: false,
      hasVisibleOutputColumn: false,
    });
  });

  it('preserves publication eligibility for hidden authored columns while disabling their preview', () => {
    expect(effectiveOutputAvailability(document([sourceColumn('status', false)]))).toEqual({
      hasAnyOutputColumn: true,
      hasVisibleOutputColumn: false,
    });
  });

  it('allows a terminal Combine output to supply the first previewable column', () => {
    expect(effectiveOutputAvailability(document([], terminalCombine([output('combined', 'status')])))).toEqual({
      hasAnyOutputColumn: true,
      hasVisibleOutputColumn: true,
    });
  });

  it('keeps a terminal Combine with only hidden outputs publishable but not previewable', () => {
    expect(effectiveOutputAvailability(document([], terminalCombine([output('combined', 'status', false)])))).toEqual({
      hasAnyOutputColumn: true,
      hasVisibleOutputColumn: false,
    });
  });

  it('uses explicit terminal visibility and source visibility for reused row-value names', () => {
    const hiddenSource = sourceColumn('status', false);
    const reusedOutput = output('status_output', 'status');
    const rowValues = [{ inputColumnId: 'status_id', outputColumnId: 'status_output', policy: 'ALL' as const }];
    expect(effectiveOutputAvailability(document(
      [hiddenSource],
      terminalStep([reusedOutput], rowValues),
    ))).toEqual({ hasAnyOutputColumn: true, hasVisibleOutputColumn: false });
    expect(effectiveOutputAvailability(document(
      [hiddenSource],
      terminalStep([output('status_output', 'status', true)], rowValues),
    ))).toEqual({ hasAnyOutputColumn: true, hasVisibleOutputColumn: true });
  });

  it('keeps ordinary source pass-through visibility when it is emitted by the final stage', () => {
    expect(effectiveOutputAvailability(document(
      [sourceColumn('status', false)],
      terminalStep([output('status_output', 'status', true)]),
    ))).toEqual({ hasAnyOutputColumn: true, hasVisibleOutputColumn: false });
  });

  it('uses only final-stage outputs for non-cohort preview eligibility', () => {
    expect(effectiveOutputAvailability(document(
      [sourceColumn('status', true)],
      terminalCombine([output('combined', 'renamed_status')]),
    ))).toEqual({ hasAnyOutputColumn: true, hasVisibleOutputColumn: true });
    expect(effectiveOutputAvailability(document(
      [sourceColumn('status', true)],
      terminalCombine([]),
    ))).toEqual({ hasAnyOutputColumn: true, hasVisibleOutputColumn: false });
  });

  it('preserves the source-column gate for a terminal explicit cohort', () => {
    const cohortRows: ExplorerBuilderDocument['rows'] = {
      kind: 'GROUPS',
      groups: {
        source: {
          kind: 'EXPLICIT',
          explicit: { revisionId: 'group_revision', unassignedMemberPolicy: 'EXCLUDE' },
        },
        afterStepId: 'combine_step',
      },
    };
    expect(effectiveOutputAvailability(document(
      [sourceColumn('status', true)],
      terminalCombine([]),
      cohortRows,
    ))).toEqual({ hasAnyOutputColumn: true, hasVisibleOutputColumn: true });
  });
});
