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
import { constructionHistorySteps, constructionRowMeaning } from './constructionHistory';
import { constructionProposalIsApplicable } from './ConstructionProposalPanel';
import { sourceProjectionAvailability } from './sourceProjectionAvailability';
import type { Construction, ConstructionProposalResponse, ExplorerBuilderPreviewResult } from '../../../types';

describe('ConstructionWorkspace', () => {
  it('keeps the visible row meaning aligned with the last row-changing step', () => {
    const sourceColumns = [
      { id: 'status', name: 'status', label: 'Status' },
      { id: 'codes', name: 'codes', label: 'Diagnosis codes' },
    ];
    const grouped: Construction = {
      version: 1,
      steps: [
        {
          id: 'group-status', inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: { kind: 'GROUP', group: {
            constructionId: 'group-status', keys: [{ inputColumnId: 'status', outputColumnId: 'status' }],
          } },
          outputs: [{ id: 'status', name: 'status', label: 'Status' }],
        },
        {
          id: 'filter-status', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'group-status' }],
          operation: { kind: 'FILTER', filter: { columnId: 'status', operator: 'EXISTS' } },
          outputs: [{ id: 'status', name: 'status', label: 'Status' }],
        },
      ],
    };
    expect(constructionRowMeaning('One row per Patient.', grouped, sourceColumns))
      .toBe('One row per distinct combination of Status.');
    const expanded: Construction = {
      version: 1,
      steps: [{
        id: 'expand-codes', inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'EXPAND', expand: {
          constructionId: 'expand-codes', inputColumnId: 'codes', outputColumnId: 'code', emptyPolicy: 'PRESERVE_PARENT',
        } },
        outputs: [{ id: 'code', name: 'code', label: 'Diagnosis code' }],
      }],
    };
    expect(constructionRowMeaning('One row per Patient.', expanded, sourceColumns))
      .toBe('One row per value in Diagnosis codes from each input row.');
  });

  it('shows executable operation families and omits Calculate and Combine creation actions', () => {
    const onSelect = vi.fn<(family: ConstructionOperationFamily) => void>();

    render(<ConstructionActionBar onSelect={onSelect} />);

    const actions = [
      ['ADD_COLUMNS', 'Add columns'],
      ['KEEP_ROWS', 'Filter rows'],
      ['RESHAPE', 'Reshape'],
    ] as const;
    for (const [family, label] of actions) {
      const button = screen.getByTestId(
        `construction-action-${family.toLowerCase().replace('_', '-')}`,
      );
      expect(button.getAttribute('aria-label')).toContain(label);
      fireEvent.click(button);
      expect(onSelect).toHaveBeenLastCalledWith(family);
    }
    expect(screen.queryByTestId('construction-action-calculate')).not.toBeInTheDocument();
    expect(screen.queryByTestId('construction-action-combine')).not.toBeInTheDocument();
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
        rowSetup={<button type="button">Configure rows</button>}
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
    expect(screen.getByTestId('construction-row-setup').compareDocumentPosition(
      screen.getByTestId('construction-action-add-columns'),
    ) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
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
      { id: 'filter_bmi', title: 'Filter rows', summary: 'Filter output rows where BMI is at least 25.', editable: true },
    ]);
  });

  it('describes the saved related result form accurately', () => {
    for (const [form, summary] of [
      ['ALL', 'Add all matching values from Observation.status as Observation result.'],
      ['COUNT', 'Count matching Observation records as Observation result.'],
      ['PRESENCE', 'Show whether matching Observation records exist as Observation result.'],
    ] as const) {
      const construction: Construction = {
        version: 1,
        steps: [{
          id: 'related-observation',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'RELATED_SOURCE',
            relatedSource: {
              anchorColumnId: 'patient-id',
              choiceId: 'observation-choice',
              sourceOccurrenceId: 'observation-node',
              source: {
                kind: 'FIELD', candidateId: 'observation-status', nodeId: 'observation-node',
                resourceType: 'Observation', path: 'status', cardinality: 'optional_one', logicalType: 'string',
              },
              route: [],
              contributorRule: { policy: 'ALL_MATCHES' },
              form,
              outputColumnId: 'observation-result',
            },
          },
          outputs: [{ id: 'observation-result', name: 'observation_result', label: 'Observation result' }],
        }],
      };
      expect(constructionHistorySteps(construction, [])).toEqual([
        { id: 'related-observation', title: 'Related source', summary, editable: true },
      ]);
    }
  });

  it('summarizes and exposes saved Group and Expand steps while describing Combine inputs', () => {
    const construction: Construction = {
      version: 1,
      steps: [
        {
          id: 'group_cohorts',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'GROUP',
            group: {
              constructionId: 'group_cohorts',
              keys: [{ inputColumnId: 'cohort_id', outputColumnId: 'cohort' }],
              aggregates: [{ operation: 'SUM', inputColumnId: 'score', outputColumnId: 'total_score' }],
            },
          },
          outputs: [
            { id: 'cohort', name: 'cohort', label: 'Cohort' },
            { id: 'total_score', name: 'total_score', label: 'Total score', type: 'decimal' },
            { id: 'items', name: 'items', label: 'Items', type: 'string' },
          ],
        },
        {
          id: 'expand_items',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'group_cohorts' }],
          operation: {
            kind: 'EXPAND',
            expand: {
              constructionId: 'expand_items',
              inputColumnId: 'items',
              outputColumnId: 'item',
              ordinalColumnId: 'position',
              emptyPolicy: 'EXCLUDE',
            },
          },
          outputs: [
            { id: 'cohort', name: 'cohort', label: 'Cohort' },
            { id: 'total_score', name: 'total_score', label: 'Total score', type: 'decimal' },
            { id: 'item', name: 'item', label: 'Item', type: 'string' },
            { id: 'position', name: 'position', label: 'Position', type: 'integer' },
          ],
        },
        {
          id: 'append_visits',
          inputs: [
            { kind: 'STEP_OUTPUT', stepId: 'expand_items' },
            { kind: 'TABLE_REVISION', tableId: 'visits', revisionId: 'revision-7', outputId: 'visits-output' },
          ],
          operation: {
            kind: 'COMBINE',
            combine: {
              kind: 'APPEND',
              projections: [{ outputColumnId: 'combined_item', inputIndex: 0, inputColumnId: 'item' }],
            },
          },
          outputs: [{ id: 'combined_item', name: 'combined_item', label: 'Combined item' }],
        },
      ],
    };

    expect(constructionHistorySteps(construction, [
      { id: 'cohort_id', name: 'cohort_id', label: 'Cohort ID', type: 'string' },
      { id: 'score', name: 'score', label: 'Score', type: 'decimal' },
    ])).toEqual([
      {
        id: 'group_cohorts',
        title: 'Group',
        summary: 'Group rows by Cohort ID; Total score sums Score.',
        editable: true,
      },
      {
        id: 'expand_items',
        title: 'Expand',
        summary: 'Expand Items into rows with Item and Position columns. Empty lists: exclude.',
        editable: true,
      },
      {
        id: 'append_visits',
        title: 'Combine',
        summary: 'Append rows from visits (revision revision-7).',
      },
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
