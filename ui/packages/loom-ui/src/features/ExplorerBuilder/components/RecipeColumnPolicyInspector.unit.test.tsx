// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCandidate, ExplorerBuilderColumn } from '../../../types';
import { RecipeColumnPolicyInspector } from './RecipeColumnPolicyInspector';

afterEach(cleanup);
const column: ExplorerBuilderColumn = { columnId: 'exact-column', column: 'score', label: 'Observation scores', occurrenceId: 'observation-occurrence',
  source: { kind: 'field', field: { path: 'valueInteger', projectionMode: 'FIRST', relatedSelection: { kind: 'first-by-resource-key', acknowledged: true } } },
  table: { visible: true, order: 3 }, filter: { label: 'Existing filter label' } };
const candidate = { candidateId: 'field-candidate', nodeId: 'observation-node', fieldPath: 'valueInteger', projectionModes: ['FIRST', 'ALL', 'DISTINCT'] } as ExplorerBuilderCandidate;
describe('ordinary recipe column policy inspector', () => {
  it('changes the existing column policy while preserving exact identity and unrelated configuration', () => {
    const change = vi.fn(); render(<RecipeColumnPolicyInspector column={column} sourceNodeId="observation-node" candidate={candidate} onChange={change} onCancel={vi.fn()} />);
    fireEvent.change(screen.getByRole('combobox', { name: 'Value policy for Observation scores' }), { target: { value: 'ALL' } });
    expect(change).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Save column policy' }));
    expect(change).toHaveBeenCalledWith({ ...column, source: { ...column.source, field: { ...('field' in column.source ? column.source.field : {}), projectionMode: 'ALL' } } });
    expect(column.source.kind === 'field' && column.source.field.projectionMode).toBe('FIRST');
  });
  it('Cancel changes no column and does not pretend a preview occurred', () => {
    const change = vi.fn(), cancel = vi.fn(); render(<RecipeColumnPolicyInspector column={column} sourceNodeId="observation-node" candidate={candidate} onChange={change} onCancel={cancel} />);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'ALL' } }); fireEvent.click(screen.getByRole('button', { name: 'Cancel column edit' }));
    expect(change).not.toHaveBeenCalled(); expect(cancel).toHaveBeenCalledOnce(); expect(screen.queryByRole('button', { name: /Preview/ })).toBeNull();
  });
  it('refuses modes from a different occurrence node with the same field path', () => {
    render(<RecipeColumnPolicyInspector column={column} sourceNodeId="another-observation-node" candidate={candidate} onChange={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.queryByRole('combobox')).toBeNull(); expect((screen.getByRole('button', { name: 'Save column policy' }) as HTMLButtonElement).disabled).toBe(true);
  });
  it('uses receipt-backed preview only when its owner explicitly supplies that flow', () => {
    const change = vi.fn(), preview = vi.fn(); render(<RecipeColumnPolicyInspector column={column} sourceNodeId="observation-node" candidate={candidate} onChange={change} onPreview={preview} onCancel={vi.fn()} />);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'DISTINCT' } }); fireEvent.click(screen.getByRole('button', { name: 'Preview column policy' }));
    expect(preview).toHaveBeenCalledOnce(); expect(change).not.toHaveBeenCalled();
  });
});

it('offers Apply only for the validated candidate and revokes it through the owning form action', () => {
  const preview = vi.fn(), change = vi.fn(), changed = vi.fn(), cancel = vi.fn();
  const props = { column, sourceNodeId: 'observation-node', candidate, onChange: change, onPreview: preview, onDraftChange: changed, onCancel: cancel };
  const { rerender } = render(<RecipeColumnPolicyInspector {...props} />);
  fireEvent.change(screen.getByRole('combobox'), { target: { value: 'ALL' } });
  expect(changed).toHaveBeenCalledOnce();
  expect((screen.getByRole('button', { name: 'Apply column policy' }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Preview column policy' }));
  expect(preview).toHaveBeenCalledOnce(); expect(change).not.toHaveBeenCalled();
  rerender(<RecipeColumnPolicyInspector {...props} canApply />);
  fireEvent.click(screen.getByRole('button', { name: 'Apply column policy' }));
  expect(change).toHaveBeenCalledOnce();
  rerender(<RecipeColumnPolicyInspector {...props} busy />);
  fireEvent.click(screen.getByRole('button', { name: 'Cancel column edit' }));
  expect(cancel).toHaveBeenCalledOnce();
});
