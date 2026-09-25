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
import { constructionHistorySteps } from './constructionHistory';
import { constructionProposalIsApplicable } from './ConstructionProposalPanel';
import { sourceProjectionAvailability } from './sourceProjectionAvailability';
import type { Construction, ConstructionProposalResponse, ExplorerBuilderPreviewResult } from '../../../types';

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

  it('summarizes actual typed construction steps using stable source and stage column identities', () => {
    const construction: Construction = {
      version: 1,
      steps: [
        {
          id: 'derive_bmi',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'DERIVE',
            derive: {
              constructionId: 'derive_bmi',
              outputColumnId: 'bmi',
              operation: 'DIVIDE',
              left: { kind: 'COLUMN', columnId: 'weight' },
              right: { kind: 'COLUMN', columnId: 'height' },
              missingInputPolicy: 'PROPAGATE_NULL',
              divisionByZeroPolicy: 'NULL',
            },
          },
          outputs: [
            { id: 'weight', name: 'weight', label: 'Weight', type: 'decimal' },
            { id: 'height', name: 'height', label: 'Height', type: 'decimal' },
            { id: 'bmi', name: 'bmi', label: 'BMI', type: 'decimal' },
          ],
        },
        {
          id: 'filter_bmi',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'derive_bmi' }],
          operation: {
            kind: 'FILTER',
            filter: {
              columnId: 'bmi',
              operator: 'GTE',
              values: [{ kind: 'DECIMAL', decimal: 25 }],
            },
          },
          outputs: [
            { id: 'weight', name: 'weight', label: 'Weight', type: 'decimal' },
            { id: 'height', name: 'height', label: 'Height', type: 'decimal' },
            { id: 'bmi', name: 'bmi', label: 'BMI', type: 'decimal' },
          ],
        },
      ],
    };

    expect(constructionHistorySteps(construction, [
      { id: 'weight', name: 'weight', label: 'Weight', type: 'decimal' },
      { id: 'height', name: 'height', label: 'Height', type: 'decimal' },
    ])).toEqual([
      { id: 'derive_bmi', title: 'Calculate', summary: 'BMI = Weight ÷ Height.', editable: true },
      { id: 'filter_bmi', title: 'Keep rows', summary: 'Keep rows where BMI is at least 25.', editable: true },
    ]);
  });

  it('only enables source additions when compiler stages retain the source row identity', () => {
    const stages = [
      {
        id: 'source_projection',
        inputStageId: '',
        rowIdentityColumn: 'source_row_id',
        columns: [],
        capabilities: [],
      },
      {
        id: 'derive_bmi',
        inputStageId: 'source_projection',
        rowIdentityColumn: 'source_row_id',
        columns: [],
        capabilities: [],
      },
    ];
    expect(sourceProjectionAvailability(stages).available).toBe(true);
    expect(sourceProjectionAvailability(undefined).available).toBe(false);
    expect(sourceProjectionAvailability([
      stages[0],
      { ...stages[1], rowIdentityColumn: 'reshaped_row_id' },
    ])).toMatchObject({
      available: false,
      reason: expect.stringContaining('row identity'),
    });
  });

  it('gates proposal Apply on an exact ready preview receipt and current draft identity', () => {
    const response: ConstructionProposalResponse = {
      proposalId: 'proposal-123',
      outputId: 'patients',
      snapshotToken: 'snapshot-1',
      draftVersion: 7,
      draftDigest: 'draft-7',
      baseDocumentDigest: 'document-7',
      candidateWorkspaceDigest: 'candidate-8',
      changedStepId: 'derive_bmi',
      candidateConstruction: { version: 1, steps: [] },
      dependencyImpact: { affectedStepIds: [] },
      stages: [],
      previewStatus: 'READY',
      previewDurationMs: 20,
    };
    const preview: ExplorerBuilderPreviewResult = {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderPreview',
      receiptId: 'proposal-123',
      outputId: 'patients',
      columns: [],
      rows: [],
      rowCount: 0,
      diagnostics: [],
    };
    const state = { status: 'ready' as const, response, preview };
    const identity = {
      outputId: 'patients',
      snapshotToken: 'snapshot-1',
      draftVersion: 7,
      draftDigest: 'draft-7',
    };
    expect(constructionProposalIsApplicable(state, identity)).toBe(true);
    expect(constructionProposalIsApplicable(state, { ...identity, draftVersion: 8 })).toBe(false);
    expect(constructionProposalIsApplicable(state, {
      ...identity,
      outputId: 'visits',
    })).toBe(false);
    expect(constructionProposalIsApplicable({
      ...state,
      preview: { ...preview, receiptId: 'base-receipt' },
    }, identity)).toBe(false);
  });
});
