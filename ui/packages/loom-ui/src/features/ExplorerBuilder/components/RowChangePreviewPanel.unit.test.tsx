// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import { rowCountImpactSummary } from '../constructionWorkspace/ConstructionProposalPreview';
import { RowChangePreviewPanel } from './RowChangePreviewPanel';

afterEach(cleanup);

const preview = (overrides: Partial<ExplorerBuilderPreviewResult> = {}): ExplorerBuilderPreviewResult => ({
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
  ...overrides,
});

const rows = (count: number) => Array.from({ length: count }, (_, index) => ({ patient_id: `patient-${index}` }));

const renderPanel = (
  props: Partial<React.ComponentProps<typeof RowChangePreviewPanel>> & { status: 'loading' | 'ready' | 'error' },
) => render(
  <RowChangePreviewPanel
    candidateRoot={props.candidateRoot ?? 'Observation'}
    status={props.status}
    preview={props.preview}
    error={props.error}
    disabled={props.disabled ?? false}
    onApply={props.onApply ?? vi.fn()}
    onCancel={props.onCancel ?? vi.fn()}
  />,
);

const impact = () => screen.getByTestId('row-change-row-count-impact');
const impactText = () => impact().textContent ?? '';
// Mirror the component's locale-aware count formatting so assertions pin wording, not separators.
const count = (value: number) => `${value.toLocaleString()} ${value === 1 ? 'row' : 'rows'}`;
const applyButton = () => screen.getByRole('button', { name: 'Apply row change' });

describe('RowChangePreviewPanel', () => {
  it('enables Apply only after proposed rows are rendered', () => {
    const onApply = vi.fn();
    const { rerender } = render(<RowChangePreviewPanel candidateRoot="Observation" status="loading" disabled={false} onApply={onApply} onCancel={vi.fn()} />);
    expect(applyButton()).toBeDisabled();

    rerender(<RowChangePreviewPanel candidateRoot="Observation" status="ready" preview={preview()} disabled={false} onApply={onApply} onCancel={vi.fn()} />);
    expect(screen.getByRole('cell', { name: 'patient-1' })).toBeInTheDocument();
    fireEvent.click(applyButton());
    expect(onApply).toHaveBeenCalledOnce();
  });

  it('keeps Apply disabled after a preview error', () => {
    render(<RowChangePreviewPanel candidateRoot="Observation" status="error" error="Preview failed" disabled={false} onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Preview failed');
    expect(applyButton()).toBeDisabled();
  });

  it('states how many rows an exact-count proposal produces alongside an enabled Apply', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 1234, sampled: false, rows: rows(1234) }) });
    expect(impactText()).toBe(`The proposed Observation row root would produce ${count(1234)}.`);
    expect(applyButton()).toBeEnabled();
  });

  it('labels the sampled rowCount as the proposal total and states the displayed count separately', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 2, sampled: true, rows: rows(2) }) });
    expect(impactText()).toBe(
      'The proposed Observation row root would produce 2 proposal rows in total; this sampled preview displays 2 of those rows.',
    );
    expect(screen.getByText('Showing 2 preview rows. Full-output coverage is unavailable before publication.')).toBeInTheDocument();
  });

  it('uses singular copy for a one-row sampled preview total and sample', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 1, sampled: true, rows: rows(1) }) });
    expect(impactText()).toBe(
      'The proposed Observation row root would produce 1 proposal row in total; this sampled preview displays 1 of those rows.',
    );
  });

  // The preview schema leaves sampled optional, so an absent flag must not be reported as a
  // proven total the proposed root produces.
  it('names an unreported sampling status instead of claiming a total', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 25, sampled: undefined, rows: rows(2) }) });
    expect(impactText()).toBe(
      'This preview returned 25 proposal rows and displays 2 of those rows; the response did not state whether that is the total the proposed Observation row root produces.',
    );
    expect(impactText()).not.toMatch(/in total/i);
    expect(applyButton()).toBeEnabled();
  });

  it('states the returned count without a displayed-row count when rows are missing and sampling is unreported', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 3, sampled: undefined, rows: null }) });
    expect(impactText()).toBe(
      'This preview returned 3 proposal rows and displays no rows; the response did not state whether that is the total the proposed Observation row root produces.',
    );
  });

  // A sampled preview still reports the response rowCount as the proposal total; the
  // separate displayed count is what tells the researcher the sample is smaller.
  it('separates the sampled proposal total from the smaller displayed count', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 25, sampled: true, rows: rows(2) }) });
    expect(impactText()).toBe(
      'The proposed Observation row root would produce 25 proposal rows in total; this sampled preview displays 2 of those rows.',
    );
    expect(impactText()).toMatch(/would produce 25 proposal rows in total/i);
    expect(impactText()).toMatch(/displays 2 of those rows/i);
  });

  it('reads as grammatical copy without a duplicated count unit', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 1, sampled: true, rows: rows(1) }) });
    expect(impactText()).not.toMatch(/\brows?\s+(?:total\s+)?rows\b/i);
  });

  it('announces the impact summary as a status live region', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 2, sampled: true, rows: rows(2) }) });
    expect(impact()).toHaveAttribute('role', 'status');
    expect(impact()).toHaveTextContent('would produce 2 proposal rows in total');
  });

  it('states the sampled total without a displayed-row count when no preview rows came back', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 0, sampled: true, rows: null }) });
    expect(impactText()).toBe(
      'The proposed Observation row root would produce 0 proposal rows in total; this sampled preview displays no rows.',
    );
    expect(screen.getByText('Loom did not return preview rows for this proposal.')).toBeInTheDocument();
    expect(applyButton()).toBeEnabled();
  });

  it('states partial validation without implying full validation', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 1, sampled: true, partialValidation: true, rows: rows(1) }) });
    expect(impactText()).toContain('Preview validation is partial');
    expect(impactText()).toContain('Only displayed rows were checked in this preview.');
    expect(impactText()).not.toMatch(/showing all|fully validated|complete validation/i);
    expect(applyButton()).toBeEnabled();
  });

  it('states partial validation on an exact-count preview without implying full validation', () => {
    renderPanel({ status: 'ready', preview: preview({ rowCount: 4, sampled: false, partialValidation: true, rows: rows(4) }) });
    expect(impactText()).toBe(
      'The proposed Observation row root would produce 4 rows. Preview validation is partial: Only displayed rows were checked in this preview.',
    );
    expect(impactText()).not.toMatch(/fully validated|complete validation/i);
  });

  it('shows no impact count while loading or on error so a prior root change cannot leak a stale count', () => {
    const { rerender } = renderPanel({ status: 'ready', preview: preview({ rowCount: 1234, sampled: false, rows: rows(1234) }) });
    expect(impactText()).toBe(`The proposed Observation row root would produce ${count(1234)}.`);

    rerender(<RowChangePreviewPanel candidateRoot="Condition" status="loading" disabled={false} onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(impactText()).toBe('');
    expect(impact()).toHaveClass('sr-only');
    expect(applyButton()).toBeDisabled();

    rerender(
      <RowChangePreviewPanel
        candidateRoot="Condition"
        status="error"
        error="Preview failed"
        disabled={false}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(impactText()).toBe('');
    expect(impact()).toHaveClass('sr-only');
    expect(applyButton()).toBeDisabled();
  });

  // Assistive tech only announces mutations to a live region that is already mounted, so the
  // impact must be one node that changes text rather than one that mounts with each proposal.
  it('keeps one live region mounted so the first ready proposal after loading is announced', () => {
    const { rerender } = renderPanel({ status: 'loading' });
    const region = impact();
    expect(region).toHaveAttribute('aria-live', 'polite');
    expect(region.textContent).toBe('');

    rerender(<RowChangePreviewPanel candidateRoot="Observation" status="ready" preview={preview({ rowCount: 7, sampled: false, rows: rows(7) })} disabled={false} onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(impact()).toBe(region);
    expect(impactText()).toBe('The proposed Observation row root would produce 7 rows.');

    rerender(<RowChangePreviewPanel candidateRoot="Condition" status="loading" disabled={false} onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(impact()).toBe(region);
    expect(impactText()).toBe('');
  });

  it('binds the impact summary to the current proposal preview receipt', () => {
    const { rerender } = renderPanel({ status: 'ready', preview: preview({ rowCount: 5, sampled: false, rows: rows(5) }) });
    expect(screen.getByTestId('construction-proposal-preview')).toHaveAttribute('data-preview-receipt-id', 'candidate-receipt');
    expect(impactText()).toBe('The proposed Observation row root would produce 5 rows.');

    rerender(
      <RowChangePreviewPanel
        candidateRoot="Condition"
        status="ready"
        preview={preview({ receiptId: 'next-receipt', rowCount: 42, sampled: false, rows: rows(42) })}
        disabled={false}
        onApply={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId('construction-proposal-preview')).toHaveAttribute('data-preview-receipt-id', 'next-receipt');
    expect(impactText()).toBe('The proposed Condition row root would produce 42 rows.');
  });
});

// The panel is the only consumer of the exported summary, so its wording is pinned here: the
// acceptance copy for each sampling status, and the claim the contract does not license.
describe('rowCountImpactSummary', () => {
  const summary = (overrides: Partial<ExplorerBuilderPreviewResult>) =>
    rowCountImpactSummary(preview(overrides), 'Observation row root', 'Only displayed rows were checked in this preview.');

  it.each([
    [false, 'The proposed Observation row root would produce 25 rows.'],
    [true, 'The proposed Observation row root would produce 25 proposal rows in total; this sampled preview displays 2 of those rows.'],
    [undefined, 'This preview returned 25 proposal rows and displays 2 of those rows; the response did not state whether that is the total the proposed Observation row root produces.'],
  ] as const)('describes rowCount for sampled=%s from the response fields alone', (sampled, expected) => {
    expect(summary({ rowCount: 25, sampled, rows: rows(2) })).toBe(expected);
  });

  // The contract defines rowCount as returned rows, never as distinct root resources.
  it('never equates rows with distinct root resources', () => {
    for (const sampled of [false, true, undefined] as const) {
      expect(summary({ rowCount: 25, sampled, rows: rows(2) })).not.toMatch(/distinct|unique|resource/i);
    }
  });

  it('states partial validation as partial in every sampling status', () => {
    for (const sampled of [false, true, undefined] as const) {
      const text = summary({ rowCount: 25, sampled, partialValidation: true, rows: rows(2) });
      expect(text).toContain('Preview validation is partial: Only displayed rows were checked in this preview.');
      expect(text).not.toMatch(/fully validated|complete validation|showing all/i);
    }
  });
});
