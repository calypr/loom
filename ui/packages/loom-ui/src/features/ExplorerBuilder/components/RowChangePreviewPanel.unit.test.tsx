// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import { RowChangePreviewPanel } from './RowChangePreviewPanel';

afterEach(cleanup);

const preview: ExplorerBuilderPreviewResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderPreview',
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
  receiptId: 'candidate-receipt',
  outputId: 'table',
  columns: [{ column: 'patient_id', label: 'Patient ID', logicalType: 'string', filterable: true, chartable: false }],
  rows: [{ patient_id: 'patient-1' }],
  rowCount: 1,
  sampled: true,
  diagnostics: [],
};

describe('RowChangePreviewPanel', () => {
  it('enables Apply only after proposed rows are rendered', () => {
    const onApply = vi.fn();
    const { rerender } = render(<RowChangePreviewPanel candidateRoot="Observation" status="loading" disabled={false} onApply={onApply} onCancel={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Apply row change' })).toBeDisabled();

    rerender(<RowChangePreviewPanel candidateRoot="Observation" status="ready" preview={preview} disabled={false} onApply={onApply} onCancel={vi.fn()} />);
    expect(screen.getByRole('cell', { name: 'patient-1' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Apply row change' }));
    expect(onApply).toHaveBeenCalledOnce();
  });

  it('keeps Apply disabled after a preview error', () => {
    render(<RowChangePreviewPanel candidateRoot="Observation" status="error" error="Preview failed" disabled={false} onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Preview failed');
    expect(screen.getByRole('button', { name: 'Apply row change' })).toBeDisabled();
  });
});
