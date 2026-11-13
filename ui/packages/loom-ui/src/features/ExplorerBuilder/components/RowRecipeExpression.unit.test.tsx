// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AuthoringNetworkResponse, ConstructionStep, ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { RowRecipeExpression, type RowRecipeExpressionProps } from './RowRecipeExpression';
import { columnPolicy, operationExpression, rowConnectionTerms } from './rowRecipeProjection';

afterEach(cleanup);
const catalog = { nodes: [
  { nodeId: 'subject', resourceType: 'ResearchSubject', rowRootEligible: true, populated: true, documentCount: 4 },
  { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 4 },
  { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 5 },
], edges: [
  { edgeId: 'subject-patient', fromNodeId: 'subject', toNodeId: 'patient', label: 'subject' },
  { edgeId: 'patient-observation', fromNodeId: 'patient', toNodeId: 'observation', label: 'observations' },
], routePolicy: { maxSteps: 8 } } as ExplorerBuilderCatalog;
const group: ConstructionStep = { id: 'group-stage', inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: { kind: 'GROUP', group: { constructionId: 'group-identity', missingKeyPolicy: 'GROUP', keys: [
    { inputColumnId: 'gender', outputColumnId: 'group-gender' }, { inputColumnId: 'status', outputColumnId: 'group-status' },
  ], aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'count' }] } },
  rowValues: [{ inputColumnId: 'values', outputColumnId: 'retained', policy: 'ALL' }], outputs: [
    { id: 'group-gender', name: 'gender', label: 'Gender' }, { id: 'group-status', name: 'status', label: 'Status' },
    { id: 'count', name: 'count', label: 'Subjects' }, { id: 'retained', name: 'retained', label: 'Observation lists' },
  ] };
const table: DraftTable = { outputId: 'subjects', tabId: 'subjects', title: 'Subjects', document: {
  kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject',
  rows: { kind: 'RECORDS', records: {} }, route: { occurrenceId: 'root', resourceType: 'ResearchSubject', children: [
    { occurrenceId: 'person', resourceType: 'Patient', catalogEdgeId: 'subject-patient', relationship: 'subject', matchMode: 'OPTIONAL' },
    { occurrenceId: 'other-person', resourceType: 'Patient', relationship: 'investigator', matchMode: 'REQUIRED' },
  ] }, columns: [
    { columnId: 'values', column: 'observation_values', label: 'Observation values', occurrenceId: 'person', source: { kind: 'field', field: { path: 'valueInteger', projectionMode: 'ALL' } } },
    { columnId: 'gender', column: 'gender', label: 'Gender', occurrenceId: 'person', source: { kind: 'field', field: { path: 'gender', projectionMode: 'VALUE' } } },
    { columnId: 'status', column: 'status', label: 'Status', occurrenceId: 'root', source: { kind: 'field', field: { path: 'status', projectionMode: 'VALUE' } } },
  ], construction: { version: 1, steps: [group, { id: 'filter-after-group', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'group-stage' }],
    operation: { kind: 'FILTER', filter: { columnId: 'count', operator: 'GT', values: [{ kind: 'INTEGER', integer: 1 }] } }, outputs: group.outputs }] },
} };
function props(overrides: Partial<RowRecipeExpressionProps> = {}): RowRecipeExpressionProps {
  return { table, catalog, projectionKind: 'saved', onEditSourceRows: vi.fn(), onAddConnection: vi.fn(), onSetMatchMode: vi.fn(),
    onEditColumn: vi.fn(), onEditImportedSourceStep: vi.fn(), onEditOperation: vi.fn(), onAddRelatedValues: vi.fn(),
    onAddRelatedRows: vi.fn(), onAddGroup: vi.fn(), ...overrides };
}

describe('editable row recipe expression', () => {
  it('updates the intended occurrence or existing column, keeping distinct Patient paths', () => {
    const callbacks = props(); render(<RowRecipeExpression {...callbacks} />);
    const policies = screen.getAllByRole('combobox', { name: /Match policy for/ }); expect(policies).toHaveLength(2);
    fireEvent.change(policies[1], { target: { value: 'OPTIONAL' } });
    expect(callbacks.onSetMatchMode).toHaveBeenCalledWith('other-person', 'OPTIONAL');
    fireEvent.click(screen.getByRole('button', { name: 'Edit column Observation values: all values as a list' }));
    expect(callbacks.onEditColumn).toHaveBeenCalledWith('values');
    expect(rowConnectionTerms(table.document.route).map((term) => term.occurrenceId)).toEqual(['root', 'person', 'other-person']);
  });
  it('selects an authorized connection without the graph or raw ID authoring', () => {
    const callbacks = props(); render(<RowRecipeExpression {...callbacks} />);
    fireEvent.click(screen.getByText('Connect records'));
    fireEvent.change(screen.getByRole('combobox', { name: 'Connect from occurrence' }), { target: { value: 'person' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Connection relationship' }), { target: { value: 'patient-observation' } });
    fireEvent.click(screen.getByRole('button', { name: 'Preview connection' }));
    expect(callbacks.onAddConnection).toHaveBeenCalledWith('person', 'patient-observation', 'observation');
  });
  it('shows every Group key separately from summaries/retained values and edits the original operation', () => {
    const callbacks = props(); const before = JSON.stringify(table); render(<RowRecipeExpression {...callbacks} />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit operation 1: Group rows by (Gender, Status)' }));
    expect(callbacks.onEditOperation).toHaveBeenCalledWith('group-stage');
    expect(screen.getByText('Summaries: count rows as Subjects')).toBeTruthy();
    expect(screen.getByText('Retained values: Observation values as all values as a list')).toBeTruthy();
    expect(within(screen.getByRole('list', { name: 'Row recipe operations' })).getAllByRole('listitem')
      .map((node) => node.getAttribute('data-operation-id'))).toEqual(['group-stage', 'filter-after-group']);
    expect(JSON.stringify(table)).toBe(before);
  });
  it('uses exact imported step provenance, never inferring it from an occurrence or field name', () => {
    const imported = { ...table, document: { ...table.document, columns: [{ ...table.document.columns[0], source: { kind: 'imported' as const, imported: { outputColumn: 'observation_values' } } }] } };
    const network = { owner: 'IMPORTED_SOURCE', outputId: 'subjects', sourceRows: table.document.rows, occurrences: [], operations: [],
      bindings: [{ columnId: 'values', status: 'EXACT', sourceStepId: 'bounded-values-step', occurrenceId: 'person', valueMode: 'ALL' }] } satisfies AuthoringNetworkResponse['network'];
    const callbacks = props({ table: imported, network }); const view = render(<RowRecipeExpression {...callbacks} network={undefined} />);
    expect(screen.queryByTestId('shared-source-policy-row-values')).toBeNull();
    view.rerender(<RowRecipeExpression {...callbacks} />);
    expect(screen.getByTestId('shared-source-policy-row-values')).toBe(screen.getByRole('button', { name: 'Edit shared source policy for Observation values: all values as a list' }));
    fireEvent.click(screen.getByRole('button', { name: 'Edit shared source policy for Observation values: all values as a list' }));
    expect(callbacks.onEditImportedSourceStep).toHaveBeenCalledWith('bounded-values-step'); expect(callbacks.onEditColumn).not.toHaveBeenCalled();
    expect(columnPolicy(imported.document.columns[0]).sourceStepId).toBeUndefined();
  });
  it('labels proposed semantics and blocks competing mutations without hiding terms', () => {
    const callbacks = props({ projectionKind: 'proposed', disabled: true, disabledReason: 'Apply or Cancel the current proposal.' });
    render(<RowRecipeExpression {...callbacks} />); expect(screen.getByText('Proposed recipe')).toBeTruthy();
    expect(screen.getByText('Apply or Cancel the current proposal.')).toBeTruthy();
    const edit = screen.getByRole('button', { name: 'Edit operation 1: Group rows by (Gender, Status)' }) as HTMLButtonElement;
    expect(edit.disabled).toBe(true); fireEvent.click(edit); expect(callbacks.onEditOperation).not.toHaveBeenCalled();
  });
  it('explains per-input related pairs and preserves existing list semantics explicitly', () => {
    const expanded: ConstructionStep = { id: 'pairs', inputs: [{ kind: 'SOURCE_PROJECTION' }], outputs: [], operation: { kind: 'RELATED_EXPAND',
      relatedExpand: { anchorColumnId: 'subject-id', choiceId: 'match', targetNodeId: 'observation', targetResourceType: 'Observation',
        route: [{ edgeId: 'patient-observation', fromNodeId: 'patient', toNodeId: 'observation', fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'observations', storageDirection: 'INBOUND', matchMode: 'OPTIONAL' }],
        contributorRule: { policy: 'ALL_MATCHES' }, emptyPolicy: 'PRESERVE_PARENT', relatedRecordColumnId: 'observation-id' } } };
    const expression = operationExpression(expanded, new Map([['subject-id', 'Subject identifier']]));
    expect(expression.title).toBe('One row per input–Observation pair'); expect(expression.clauses).toContain('When no related record: keep the input row');
    expect(expression.clauses).toContain('Existing list columns stay lists on every pair. Choose a separate column explicitly for a per-match scalar.');
  });
});

it('keeps legacy source policies readable without inventing a stable identity or offering an invalid proposal', () => {
  const legacy = { ...table, document: { ...table.document, columns: [{ ...table.document.columns[0], columnId: undefined }] } };
  const callbacks = props({ table: legacy }); render(<RowRecipeExpression {...callbacks} />);
  const edit = screen.getByRole('button', { name: 'Edit column Observation values: all values as a list' });
  expect((edit as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText(/Legacy column: no stable column identity/)).toBeInTheDocument();
  expect(legacy.document.columns[0].columnId).toBeUndefined();
});
