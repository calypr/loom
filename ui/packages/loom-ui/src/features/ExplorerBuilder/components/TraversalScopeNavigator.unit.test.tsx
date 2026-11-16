// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { DraftTable } from '../authoring/model';
import { TraversalScopeNavigator, type TraversalScopeNavigatorProps } from './TraversalScopeNavigator';
afterEach(cleanup);
const table: DraftTable = { outputId: 'subjects', tabId: 'subjects', title: 'Subjects', document: {
  kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject',
  route: { occurrenceId: 'base', resourceType: 'ResearchSubject', children: [{ occurrenceId: 'patient', resourceType: 'Patient', relationship: 'subject' }] },
  rows: { kind: 'RECORDS', records: {} }, columns: [], importedSource: { steps: [
    { id: 'fields', kind: 'FIELD_GROUP', title: 'Selected source fields', summary: 'Actual selected fields', enabled: true, editable: true },
    { id: 'patient-step', kind: 'TRAVERSAL', occurrenceId: 'patient', title: 'Related Patient records', summary: 'Optional relationship', enabled: true, editable: true },
  ] },
} };
const props = (): TraversalScopeNavigatorProps => ({ table, projectionKind: 'saved', selectedOccurrenceId: 'base',
  onSelectScope: vi.fn(), onEditSourceStep: vi.fn(), onEditSourceOutput: vi.fn(), onEditConstructionStep: vi.fn(), onEditColumn: vi.fn(), onEditSourceRows: vi.fn(),
  onExpandRelatedRows: vi.fn(), onGroupRows: vi.fn(),
});
it('opens the source operation directly without listing every field as a shortcut', () => {
  const value = props(); render(<TraversalScopeNavigator {...value} />);
  fireEvent.click(screen.getByRole('button', { name: 'Edit source operation 1: Selected source fields' }));
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('fields');
  expect(screen.queryByRole('list', { name: /Fields in / })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Both editors' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Define a row' })).toBeNull();
});
it('presents source and result operations as one continuously numbered recipe', () => {
  const value = props();
  render(<TraversalScopeNavigator {...value} />);
  const steps = screen.getByTestId('traversal-recipe-steps');
  expect(within(steps).getByText('Recipe steps · execution order')).toBeInTheDocument();
  expect(within(steps).getByRole('button', { name: 'Edit source operation 1: Selected source fields' })).toBeInTheDocument();
  expect(within(steps).getByText('Selected source fields')).toBeInTheDocument();
});

it('offers row-definition editing only for authored rows, not imported recipes', () => {
  const authored = props(); const { rerender } = render(<TraversalScopeNavigator {...authored} />);
  fireEvent.click(screen.getByRole('button', { name: 'Edit row definition' }));
  expect(authored.onEditSourceRows).toHaveBeenCalledOnce();

  rerender(<TraversalScopeNavigator {...authored} table={{ ...table, document: { ...table.document,
    importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' } } }} />);
  expect(screen.queryByRole('button', { name: 'Edit row definition' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Edit source rows' })).toBeNull();
});

it('navigation selects an exact occurrence without changing an operation or anchor', () => {
  const value = props(); render(<TraversalScopeNavigator {...value} />);
  fireEvent.click(within(screen.getByRole('navigation', { name: 'Authored traversal scopes' })).getByText('ResearchSubject → subject → Patient'));
  expect(value.onSelectScope).toHaveBeenCalledExactlyOnceWith('patient');
  expect(value.onEditSourceStep).not.toHaveBeenCalled(); expect(value.onEditConstructionStep).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Expand related records into rows' })); expect(value.onExpandRelatedRows).toHaveBeenCalledExactlyOnceWith('base');
});
it('does not replace a missing selected scope with root and disables new mutation for proposed data', () => {
  const value = props(); const { rerender } = render(<TraversalScopeNavigator {...value} selectedOccurrenceId="missing" />);
  expect(screen.getByRole('status')).toHaveTextContent('unavailable or ambiguous');
  expect(screen.queryByRole('button', { name: 'Edit row definition' })).toBeNull();
  rerender(<TraversalScopeNavigator {...value} projectionKind="proposed" />);
  expect(screen.getByRole('button', { name: 'Expand related records into rows' })).toBeDisabled();
});

it('keeps scope editing available without duplicating the traversal bar navigation', () => {
  const value = props();
  render(<TraversalScopeNavigator {...value} showScopeNavigation={false} selectedOccurrenceId="patient" />);
  expect(screen.queryByRole('navigation', { name: 'Authored traversal scopes' })).toBeNull();
  expect(screen.getByRole('heading', { name: 'Patient operations' })).toBeInTheDocument();
  fireEvent.click(within(screen.getByRole('list', { name: 'Source operations in selected scope' })).getByRole('button', { name: 'Edit source operation 2: Related Patient records' }));
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('patient-step');
  expect(value.onSelectScope).not.toHaveBeenCalled();
});

it('does not expose field shortcuts for a read-only source operation', () => {
  const value = { ...props(), table: { ...table, document: { ...table.document, importedSource: { steps: table.document.importedSource!.steps.map(step => ({ ...step, editable: false })) } } } };
  render(<TraversalScopeNavigator {...value} />);
  expect(screen.queryByText('Read-only field')).toBeNull();
  expect(screen.getByRole('button', { name: 'Edit source operation 1: Selected source fields' })).toBeDisabled();
});

it('allows only existing source-operation navigation inside its owning edit', () => {
  const value = props();
  const { rerender } = render(<TraversalScopeNavigator {...value} sourceStepEditOnly projectionKind="proposed" />);
  const sourceOperations = () => within(screen.getByRole('list', { name: 'Source operations in selected scope' }));
  expect(sourceOperations().getByRole('button', { name: 'Edit source operation 1: Selected source fields' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Expand related records into rows' })).toBeDisabled();
  fireEvent.click(sourceOperations().getByRole('button', { name: 'Edit source operation 1: Selected source fields' }));
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('fields');
  rerender(<TraversalScopeNavigator {...value} disabled sourceStepEditOnly sourceOperationNavigationDisabled />);
  expect(sourceOperations().getByRole('button', { name: 'Edit source operation 1: Selected source fields' })).toBeDisabled();
});

it('edits a catalog output by compiler key and owning source operation', () => {
  const generatedColumn = { columnId: 'generated-id', column: 'id', label: 'Patient ID', occurrenceId: 'patient', source: { kind: 'imported' as const, imported: { outputColumn: 'patient_id' } } };
  const value = { ...props(),
    selectedOccurrenceId: 'patient',
    table: { ...table, document: { ...table.document, importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' },
      importedSource: { steps: [...table.document.importedSource!.steps, { id: 'patient-fields', parentId: 'patient-step', kind: 'CATALOG_PROJECTION', title: 'Fields from Patient', summary: 'Patient fields', enabled: true, editable: true }] }, columns: [generatedColumn] } },
    network: { owner: 'IMPORTED_SOURCE' as const, outputId: 'subjects', sourceRows: table.document.rows, operations: [], occurrences: [],
      bindings: [{ columnId: 'generated-id', occurrenceId: 'patient', sourceStepId: 'patient-fields', outputKey: 'identifier[].value', status: 'EXACT' as const }] },
  };
  render(<TraversalScopeNavigator {...value} />);

  const sourceOperations = screen.getByRole('list', { name: 'Source operations in selected scope' });
  const operation = within(sourceOperations).getByRole('button', { name: 'Edit source operation 3: Fields from Patient' });
  expect(operation).toBeEnabled();
  expect(screen.queryByRole('button', { name: 'Value policy for Patient ID' })).toBeNull();
  expect(screen.queryByText('Operation details')).toBeNull();
  const row = operation.closest('[data-operation-id]') as HTMLElement;
  fireEvent.click(within(row).getByText('Outputs (1)'));
  fireEvent.click(within(row).getByRole('button', { name: 'Edit output Patient ID' }));
  expect(value.onEditSourceOutput).toHaveBeenCalledExactlyOnceWith('patient-fields', 'identifier[].value');
  fireEvent.click(operation);
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('patient-fields');
});

it('edits a pivot output by its raw compiler key', () => {
  const onEditSourceOutput = vi.fn();
  const generatedColumn = { columnId: 'pivot-column', column: 'quantity', label: 'quantity', occurrenceId: 'patient', source: { kind: 'imported' as const, imported: { outputColumn: 'quantity' } } };
  const importedTable: DraftTable = { ...table, document: { ...table.document,
    importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' },
    importedSource: { steps: [...table.document.importedSource!.steps, { id: 'measurement-pivot', parentId: 'patient-step', kind: 'PIVOT', title: 'Observation values', summary: 'Pivot values', enabled: true, editable: true }] },
    columns: [generatedColumn],
  } };
  const network: NonNullable<TraversalScopeNavigatorProps['network']> = { owner: 'IMPORTED_SOURCE', outputId: 'subjects', sourceRows: table.document.rows,
    occurrences: [], operations: [], bindings: [{ columnId: 'pivot-column', occurrenceId: 'patient', sourceStepId: 'measurement-pivot', outputKey: 'valueQuantity', status: 'EXACT' }] };
  const value = { ...props(), table: importedTable, network, selectedOccurrenceId: 'patient', onEditSourceOutput };
  render(<TraversalScopeNavigator {...value} />);

  const sourceOperations = screen.getByRole('list', { name: 'Source operations in selected scope' });
  const row = within(sourceOperations).getByRole('button', { name: 'Edit source operation 3: Observation values' }).closest('[data-operation-id]') as HTMLElement;
  fireEvent.click(within(row).getByText('Outputs (1)'));
  fireEvent.click(within(row).getByRole('button', { name: 'Edit output quantity' }));
  expect(onEditSourceOutput).toHaveBeenCalledExactlyOnceWith('measurement-pivot', 'valueQuantity');
});
