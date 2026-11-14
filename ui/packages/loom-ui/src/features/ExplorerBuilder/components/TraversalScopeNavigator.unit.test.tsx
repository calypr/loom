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
  onSelectScope: vi.fn(), onEditSourceStep: vi.fn(), onEditConstructionStep: vi.fn(), onEditColumn: vi.fn(), onEditSourceRows: vi.fn(),
  onAddRelatedValues: vi.fn(), onExpandRelatedRows: vi.fn(), onGroupRows: vi.fn(),
});
it('exposes every actual authored field through progressive disclosure and exact field identities', () => {
  const value = { ...props(), sourceFieldsByStep: new Map([['fields', Array.from({ length: 32 }, (_, index) => ({ fieldId: `field-${index}`, name: `Field ${index}`, valueMode: 'ALL' }))]]), onEditSourceField: vi.fn() }; render(<TraversalScopeNavigator {...value} />);
  const group = screen.getByRole('list', { name: 'Fields in Selected source fields', hidden: true });
  expect(within(group).getAllByRole('button', { hidden: true })).toHaveLength(32);
  fireEvent.click(within(group).getByText('Field 31 · all values as a list'));
  expect(value.onEditSourceField).toHaveBeenCalledExactlyOnceWith('fields', 'field-31');
  expect(screen.queryByRole('button', { name: 'Both editors' })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Define a row' })).toBeNull();
});
it('navigation selects an exact occurrence without changing an operation or anchor', () => {
  const value = props(); render(<TraversalScopeNavigator {...value} />);
  fireEvent.click(within(screen.getByRole('navigation', { name: 'Authored traversal scopes' })).getByText('ResearchSubject → subject → Patient'));
  expect(value.onSelectScope).toHaveBeenCalledExactlyOnceWith('patient');
  expect(value.onEditSourceStep).not.toHaveBeenCalled(); expect(value.onEditConstructionStep).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Collect related values' })); expect(value.onAddRelatedValues).toHaveBeenCalledExactlyOnceWith('base');
  fireEvent.click(screen.getByRole('button', { name: 'Expand related records into rows' })); expect(value.onExpandRelatedRows).toHaveBeenCalledExactlyOnceWith('base');
});
it('does not replace a missing selected scope with root and disables new mutation for proposed data', () => {
  const value = props(); const { rerender } = render(<TraversalScopeNavigator {...value} selectedOccurrenceId="missing" />);
  expect(screen.getByRole('status')).toHaveTextContent('unavailable or ambiguous');
  expect(screen.queryByRole('button', { name: 'Edit source rows' })).toBeNull();
  rerender(<TraversalScopeNavigator {...value} projectionKind="proposed" />);
  expect(screen.getByRole('button', { name: 'Collect related values' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Collect related values' })); expect(value.onAddRelatedValues).not.toHaveBeenCalled();
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

it('field shortcuts cannot bypass a read-only source operation', () => {
  const value = { ...props(), table: { ...table, document: { ...table.document, importedSource: { steps: table.document.importedSource!.steps.map(step => ({ ...step, editable: false })) } } },
    sourceFieldsByStep: new Map([['fields', [{ fieldId: 'read-only', name: 'Read-only field' }]]]), onEditSourceField: vi.fn() };
  render(<TraversalScopeNavigator {...value} />);
  const field = screen.getByText('Read-only field'); expect(field).toBeDisabled(); fireEvent.click(field); expect(value.onEditSourceField).not.toHaveBeenCalled();
});
