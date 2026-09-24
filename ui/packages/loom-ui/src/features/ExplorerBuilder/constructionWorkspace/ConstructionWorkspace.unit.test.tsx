// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  ConstructionActionBar,
  ConstructionHistory,
  ConstructionTableNavigation,
  ConstructionWorkspace,
  type ConstructionOperationFamily,
} from './ConstructionWorkspace';

describe('ConstructionWorkspace', () => {
  it('shows the five described operation families and reports the selected family', () => {
    const onSelect = vi.fn<(family: ConstructionOperationFamily) => void>();

    render(<ConstructionActionBar onSelect={onSelect} />);

    const actions = [
      ['ADD_COLUMNS', 'Add columns'],
      ['KEEP_ROWS', 'Keep rows'],
      ['CALCULATE', 'Calculate'],
      ['RESHAPE', 'Reshape'],
      ['COMBINE', 'Combine'],
    ] as const;
    for (const [family, label] of actions) {
      const button = screen.getByTestId(
        `construction-action-${family.toLowerCase().replace('_', '-')}`,
      );
      expect(button.getAttribute('aria-label')).toContain(label);
      fireEvent.click(button);
      expect(onSelect).toHaveBeenLastCalledWith(family);
    }
  });

  it('navigates named tables and keeps a history panel absent when there are no authored steps', () => {
    const onSelectTable = vi.fn();
    const onNewTable = vi.fn();
    render(
      <>
        <ConstructionTableNavigation
          tables={[
            { outputId: 'patients', title: 'Patients' },
            { outputId: 'visits', title: 'Visits' },
          ]}
          selectedOutputId="patients"
          onSelectTable={onSelectTable}
          onNewTable={onNewTable}
          onDuplicateTable={vi.fn()}
          onDeleteTable={vi.fn()}
          onRenameTable={vi.fn()}
          onMoveTable={vi.fn()}
        />
        <ConstructionHistory
          steps={[]}
          selected={{ kind: 'source' }}
          onSelect={vi.fn()}
        />
      </>,
    );

    expect(screen.getByTestId('construction-table-patients')).toHaveAttribute(
      'aria-current',
      'page',
    );
    fireEvent.click(screen.getByTestId('construction-table-visits'));
    expect(onSelectTable).toHaveBeenCalledWith('visits');
    fireEvent.click(screen.getByTestId('construction-new-table'));
    expect(onNewTable).toHaveBeenCalledOnce();
    expect(screen.queryByTestId('construction-history')).not.toBeInTheDocument();
  });

  it('exposes preview status and its receipt identity separately from the current draft', () => {
    const onSelectFamily = vi.fn();
    render(
      <ConstructionWorkspace
        tables={[{ outputId: 'patients', title: 'Patients' }]}
        selectedOutputId="patients"
        onSelectTable={vi.fn()}
        onNewTable={vi.fn()}
        onDuplicateTable={vi.fn()}
        onDeleteTable={vi.fn()}
        onRenameTable={vi.fn()}
        onMoveTable={vi.fn()}
        title="Patients"
        rowMeaning="One row per patient."
        activeFamily="CALCULATE"
        onSelectFamily={onSelectFamily}
        preview={<div role="table">Current rows</div>}
        editor={<div>Calculate editor</div>}
        previewStatus="ready"
        previewReceiptId="receipt-123"
        previewOutputId="patients"
        proposalId="proposal-456"
        draftVersion={7}
        draftDigest="digest-789"
      />,
    );

    const preview = screen.getByTestId('construction-preview');
    expect(preview).toHaveAttribute('data-preview-status', 'ready');
    expect(preview).toHaveAttribute('data-preview-receipt-id', 'receipt-123');
    expect(preview).toHaveAttribute('data-preview-proposal-id', 'proposal-456');
    expect(preview).toHaveAttribute('data-current-draft-version', '7');
    expect(preview).toHaveAttribute('data-current-draft-digest', 'digest-789');
  });
});
