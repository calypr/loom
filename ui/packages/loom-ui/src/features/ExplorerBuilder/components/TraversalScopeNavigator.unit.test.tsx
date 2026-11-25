// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { DraftTable } from '../model/authoring';
import { TraversalScopeNavigator } from './TraversalScopeNavigator';
type TraversalScopeNavigatorFixtureProps = import('react').ComponentProps<typeof import('./TraversalScopeNavigator').TraversalScopeNavigator>;
type TraversalScopeNavigatorProps = TraversalScopeNavigatorFixtureProps;
afterEach(cleanup);
const table: DraftTable = { outputId: 'subjects', tabId: 'subjects', title: 'Subjects', document: {
  kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject',
  route: { occurrenceId: 'base', resourceType: 'ResearchSubject', children: [{ occurrenceId: 'patient', resourceType: 'Patient', relationship: 'subject' }] },
  rows: { kind: 'RECORDS', records: {} }, columns: [], importedSource: { steps: [
    { id: 'fields', kind: 'FIELD_GROUP', title: 'Selected source fields', summary: 'Actual selected fields', editable: true },
    { id: 'patient-step', kind: 'TRAVERSAL', occurrenceId: 'patient', title: 'Related Patient records', summary: 'Optional relationship', editable: true },
  ] },
} };
const props = (): TraversalScopeNavigatorProps => ({ table, projectionKind: 'saved', selectedOccurrenceId: 'base',
  onSelectScope: vi.fn(), onEditSourceStep: vi.fn(), onEditConstructionStep: vi.fn(), onEditColumn: vi.fn(), onEditSourceRows: vi.fn(),
  onExpandRelatedRows: vi.fn(), onGroupRows: vi.fn(),
});
it('opens the source operation directly without listing every field as a shortcut', () => {
  const value = props(); render(<TraversalScopeNavigator {...value} />);
  fireEvent.click(screen.getByRole('button', { name: 'Edit source operation 1: Fields from ResearchSubject' }));
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('fields');
  expect(screen.queryByRole('list', { name: /Fields in / })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Both editors' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Define a row' })).toBeNull();
});
it('presents source and result operations as one continuously numbered recipe', () => {
  const value = props();
  render(<TraversalScopeNavigator {...value} />);
  const steps = screen.getByTestId('traversal-recipe-steps');
  expect(within(steps).getByText('Recipe steps')).toBeInTheDocument();
  expect(within(steps).getByRole('button', { name: 'Edit source operation 1: Fields from ResearchSubject' })).toBeInTheDocument();
  expect(within(steps).getByText('Fields from ResearchSubject')).toBeInTheDocument();
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
  expect(screen.getByRole('button', { name: 'Edit source operation 1: Fields from ResearchSubject' })).toBeDisabled();
});

it('allows only existing source-operation navigation inside its owning edit', () => {
  const value = props();
  const { rerender } = render(<TraversalScopeNavigator {...value} sourceStepEditOnly projectionKind="proposed" />);
  const sourceOperations = () => within(screen.getByRole('list', { name: 'Source operations in selected scope' }));
  expect(sourceOperations().getByRole('button', { name: 'Edit source operation 1: Fields from ResearchSubject' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'Expand related records into rows' })).toBeDisabled();
  fireEvent.click(sourceOperations().getByRole('button', { name: 'Edit source operation 1: Fields from ResearchSubject' }));
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('fields');
  rerender(<TraversalScopeNavigator {...value} disabled sourceStepEditOnly sourceOperationNavigationDisabled />);
  expect(sourceOperations().getByRole('button', { name: 'Edit source operation 1: Fields from ResearchSubject' })).toBeDisabled();
});

it('shows source operations individually beside the unified field editor', () => {
  const value = { ...props(), table: { ...table, document: { ...table.document,
    importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' },
    importedSource: { steps: [
      { id: 'output', kind: 'OUTPUT' as const, title: 'Output settings', summary: '', editable: true },
      { id: 'fields', kind: 'FIELD_GROUP' as const, title: 'Fields from ResearchSubject', summary: '', editable: true },
      { id: 'dynamic', kind: 'DYNAMIC_COLUMN' as const, title: 'Keyed columns', summary: '', editable: true },
      { id: 'extension', kind: 'EXTENSION_COLUMN' as const, title: 'Extension columns', summary: '', editable: true },
      { id: 'patient-step', kind: 'TRAVERSAL' as const, occurrenceId: 'patient', title: 'Related Patient records', summary: '', editable: true },
      { id: 'patient-dynamic', parentId: 'patient-step', kind: 'DYNAMIC_COLUMN' as const, title: 'Patient keys', summary: '', editable: true },
    ] },
  } } };
  const { rerender } = render(<TraversalScopeNavigator {...value} />);
  expect(screen.getByRole('button', { name: 'Edit source operation 2: Fields from ResearchSubject' })).toBeEnabled();
  expect(screen.getByText('Keyed columns')).toBeInTheDocument();
  expect(screen.getByText('Extension columns')).toBeInTheDocument();
  rerender(<TraversalScopeNavigator {...value} table={{ ...value.table, document: { ...value.table.document,
    importedSource: { ...value.table.document.importedSource, removedStepIds: ['dynamic'] },
  } }} />);
  expect(screen.queryByText(/Keyed columns/)).not.toBeInTheDocument();
  expect(screen.getByText('Extension columns')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Remove source operation 2/ })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Edit source operation 2: Fields from ResearchSubject' }));
  expect(value.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('fields');
  rerender(<TraversalScopeNavigator {...value} selectedOccurrenceId="patient" />);
  fireEvent.click(screen.getByRole('button', { name: 'Edit source operation 6: Patient keys' }));
  expect(value.onEditSourceStep).toHaveBeenLastCalledWith('patient-dynamic');
});

it('removes editable source rules by step identity and leaves the field group actionless', () => {
  const onRemoveSourceStep = vi.fn();
  const value = { ...props(), onRemoveSourceStep, table: { ...table, document: { ...table.document,
    importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' },
    importedSource: { steps: [
      { id: 'fields', kind: 'FIELD_GROUP', title: 'Fields from ResearchSubject', summary: '', editable: true },
      { id: 'keys', kind: 'DYNAMIC_COLUMN', title: 'Keyed columns', summary: '', editable: true },
    ] },
  } } };
  const { rerender } = render(<TraversalScopeNavigator {...value} />);
  expect(screen.queryByRole('button', { name: /Remove source operation 1/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Remove source operation 2: Keyed columns' }));
  expect(onRemoveSourceStep).toHaveBeenCalledExactlyOnceWith('keys');
  rerender(<TraversalScopeNavigator {...value} table={{ ...value.table, document: { ...value.table.document,
    importedSource: { steps: [{ id: 'fields', kind: 'FIELD_GROUP', title: 'Fields from ResearchSubject', summary: '', editable: true }], removedStepIds: ['keys'] },
  } }} />);
  expect(screen.queryByRole('button', { name: /Keyed columns/ })).not.toBeInTheDocument();
});

it('hides duplicated action controls while requested', () => {
  const { rerender } = render(<TraversalScopeNavigator {...props()} showActions={false} />);
  expect(screen.queryByRole('button', { name: 'Expand related records into rows' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Group resulting rows' })).toBeNull();
  rerender(<TraversalScopeNavigator {...props()} />);
  expect(screen.getByRole('button', { name: 'Expand related records into rows' })).toBeEnabled();
});

it('lists unresolved pinned fields once', () => {
  const importedTable: DraftTable = { ...table, document: { ...table.document,
    importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' },
    columns: [{ columnId: 'legacy-id', occurrenceId: 'base', column: 'legacy', label: 'Legacy field', source: { kind: 'imported', imported: { outputColumn: 'legacy' } } }],
  } };
  render(<TraversalScopeNavigator {...props()} table={importedTable} />);
  expect(screen.getByText('Unresolved source scope (0 operations, 1 fields)')).toBeInTheDocument();
  expect(document.querySelectorAll('[data-column-id="legacy-id"]')).toHaveLength(1);
});
