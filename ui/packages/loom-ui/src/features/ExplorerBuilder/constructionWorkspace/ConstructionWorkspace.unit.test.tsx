// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
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
    const coded: Construction = {
      version: 1,
      steps: [{
        id: 'group-codes', inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'CODED_GROUP', codedGroup: {
          constructionId: 'group-codes',
          source: { occurrenceId: 'base', resourceType: 'Observation', codingPath: 'component[].code.coding[]', fhirType: 'Coding', cardinality: 'MANY', shape: 'ARRAY', route: [] },
          missingKeyPolicy: 'GROUP',
          systemOutputColumnId: 'system', versionOutputColumnId: 'version', codeOutputColumnId: 'code', distinctSourceCountOutputColumnId: 'count',
        } },
        outputs: [{ id: 'system', name: 'system', label: 'Code system' }, { id: 'version', name: 'version', label: 'Code version' }, { id: 'code', name: 'code', label: 'Code' }, { id: 'count', name: 'count', label: 'Source records' }],
      }],
    };
    expect(constructionRowMeaning('One row per Observation record.', coded, sourceColumns))
      .toBe('One row per distinct code, including its system and version.');
  });

  it('describes the current related-record grain, route, and unmatched-row behavior', () => {
    const related: Construction = {
      version: 1,
      steps: [{
        id: 'related-medication-administration',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: {
          kind: 'RELATED_EXPAND',
          relatedExpand: {
            anchorColumnId: '_key',
            choiceId: 'observation-to-medication-administration',
            targetNodeId: 'medication-administration-node',
            targetResourceType: 'MedicationAdministration',
            route: [
              {
                edgeId: 'observation-subject-patient',
                fromNodeId: 'observation-node',
                fromResourceType: 'Observation',
                relationship: 'subject_Patient',
                storageDirection: 'OUTBOUND',
                toNodeId: 'patient-node',
                toResourceType: 'Patient',
                matchMode: 'OPTIONAL',
              },
              {
                edgeId: 'patient-subject-medication-administration',
                fromNodeId: 'patient-node',
                fromResourceType: 'Patient',
                relationship: 'subject_Patient',
                storageDirection: 'INBOUND',
                toNodeId: 'medication-administration-node',
                toResourceType: 'MedicationAdministration',
                matchMode: 'OPTIONAL',
              },
            ],
            contributorRule: { policy: 'ALL_MATCHES' },
            emptyPolicy: 'PRESERVE_PARENT',
            relatedRecordColumnId: 'medication-administration-id',
          },
        },
        outputs: [{ id: 'medication-administration-id', name: 'medication_administration_id', label: 'MedicationAdministration FHIR resource ID', type: 'string', nullable: true }],
      }],
    };

    expect(constructionRowMeaning('One row per Observation record.', related, []))
      .toBe('For each Observation, one row per matching MedicationAdministration through the same Patient; existing row fields repeat on each match; keep one Observation row with an empty MedicationAdministration FHIR resource ID when no match exists.');
    expect(constructionHistorySteps(related, [])).toMatchObject([
      { id: 'related-medication-administration', title: 'Rows from MedicationAdministration' },
    ]);

    const originalStep = related.steps[0]!;
    if (originalStep.operation.kind !== 'RELATED_EXPAND') throw new Error('Expected a related expansion');
    const [firstRouteStep, lastRouteStep] = originalStep.operation.relatedExpand.route;
    const repeatedIntermediaryRoute: Construction = {
      version: 1,
      steps: [{
        ...originalStep,
        operation: {
          kind: 'RELATED_EXPAND',
          relatedExpand: {
            ...originalStep.operation.relatedExpand,
            route: [
              firstRouteStep!,
              { ...firstRouteStep!, edgeId: 'patient-care-team', fromNodeId: 'patient-node', fromResourceType: 'Patient', toNodeId: 'care-team-node', toResourceType: 'CareTeam' },
              { ...firstRouteStep!, edgeId: 'care-team-patient', fromNodeId: 'care-team-node', fromResourceType: 'CareTeam', toNodeId: 'patient-node', toResourceType: 'Patient' },
              lastRouteStep!,
            ],
          },
        },
      }],
    };
    expect(constructionRowMeaning('One row per Observation record.', repeatedIntermediaryRoute, []))
      .toContain('through Patient → CareTeam → Patient');

    const groupedThenRelated: Construction = {
      version: 1,
      steps: [
        {
          id: 'group-status', inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: { kind: 'GROUP', group: { constructionId: 'group-status', keys: [{ inputColumnId: 'status', outputColumnId: 'status' }] } },
          outputs: [{ id: 'status', name: 'status', label: 'Status' }],
        },
        { ...related.steps[0]!, id: 'related-after-group', inputs: [{ kind: 'STEP_OUTPUT', stepId: 'group-status' }] },
      ],
    };
    expect(constructionRowMeaning('One row per Observation record.', groupedThenRelated, [
      { id: 'status', name: 'status', label: 'Status' },
    ])).toBe('For each current row (one row per distinct combination of Status), one row per matching MedicationAdministration through the same Patient; existing row fields repeat on each match; keep the current row once with an empty MedicationAdministration FHIR resource ID when no match exists.');
  });

  it('keeps Add columns and Filter rows visible and groups secondary actions under More actions', () => {
    const onSelect = vi.fn<(family: ConstructionOperationFamily) => void>();

    render(<ConstructionActionBar onSelect={onSelect} />);

    const actions = [
      ['ADD_COLUMNS', 'Add columns'],
      ['KEEP_ROWS', 'Filter rows'],
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
    expect(screen.queryByTestId('construction-action-reshape')).not.toBeInTheDocument();
    const moreActions = screen.getByTestId('construction-more-actions');
    fireEvent.click(moreActions);
    expect(moreActions).toHaveAttribute('aria-expanded', 'true');
    fireEvent.keyDown(moreActions, { key: 'Escape' });
    expect(screen.queryByTestId('construction-more-actions-menu')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(moreActions);
    fireEvent.click(moreActions);
    const menu = screen.getByRole('group', { name: 'More table actions' });
    expect(within(menu).getByTestId('construction-action-combine')).toBeEnabled();
    expect(within(menu).getByTestId('construction-action-reshape')).toBeEnabled();
    fireEvent.click(within(menu).getByTestId('construction-action-combine'));
    expect(onSelect).toHaveBeenLastCalledWith('COMBINE');
  });

  it('offers calculations only when the selected constructed stage supports them', () => {
    const onSelect = vi.fn<(family: ConstructionOperationFamily) => void>();
    render(<ConstructionActionBar onSelect={onSelect} calculateAvailable />);

    fireEvent.click(screen.getByTestId('construction-more-actions'));
    const calculate = screen.getByTestId('construction-action-calculate');
    expect(calculate).toBeEnabled();
    expect(calculate).toHaveTextContent('Calculate');
    fireEvent.click(calculate);
    expect(onSelect).toHaveBeenCalledWith('CALCULATE');
  });

  it('blocks Add columns during preview loading without blocking row filters', () => {
    const onSelect = vi.fn<(family: ConstructionOperationFamily) => void>();
    render(<ConstructionActionBar onSelect={onSelect} addColumnsDisabled />);

    const addColumns = screen.getByTestId('construction-action-add-columns');
    const filterRows = screen.getByTestId('construction-action-keep-rows');
    expect(addColumns).toBeDisabled();
    expect(filterRows).toBeEnabled();
    fireEvent.click(filterRows);
    expect(onSelect).toHaveBeenCalledWith('KEEP_ROWS');
  });

  it('offers a new-table Combine entry without asking users to clear their current table', () => {
    const onSelect = vi.fn<(family: ConstructionOperationFamily) => void>();
    render(<ConstructionActionBar onSelect={onSelect} />);

    fireEvent.click(screen.getByTestId('construction-more-actions'));
    const combine = screen.getByTestId('construction-action-combine');
    expect(combine).toBeEnabled();
    expect(combine).toHaveAttribute('aria-label', 'Combine tables: Create a new table by joining or stacking table versions.');
    fireEvent.click(combine);
    expect(onSelect).toHaveBeenCalledWith('COMBINE');
  });

  it('navigates named tables and keeps the source row available when there are no authored steps', () => {
    const onSelectTable = vi.fn();
    const onNewTable = vi.fn();
    const onRenameTable = vi.fn();
    const onDuplicateTable = vi.fn();
    const onDeleteTable = vi.fn();
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
          onDuplicateTable={onDuplicateTable}
          onDeleteTable={onDeleteTable}
          onRenameTable={onRenameTable}
        />
        <ConstructionHistory
          resourceType="Patient"
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
    expect(screen.getByRole('button', { name: 'New table' })).toHaveTextContent('+');
    expect(screen.queryByTestId('construction-rename-table-visits')).toBeNull();
    const tableActions = screen.getByTestId('construction-table-actions-patients');
    expect(tableActions).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByTestId('construction-table-patients')).toHaveClass('construction-table-tab', 'bg-transparent');
    expect(screen.getByTestId('construction-table-patients')).not.toHaveClass('bg-emerald-100');
    expect(screen.queryByTestId('construction-rename-table-patients')).toBeNull();
    fireEvent.click(tableActions);
    expect(tableActions).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('construction-table-patients').parentElement?.contains(tableActions)).toBe(true);
    expect(screen.getByTestId('construction-table-actions-menu')).toBeInTheDocument();
    fireEvent.keyDown(tableActions, { key: 'Escape' });
    expect(screen.queryByTestId('construction-table-actions-menu')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(tableActions);
    fireEvent.click(tableActions);
    fireEvent.click(screen.getByTestId('construction-rename-table-patients'));
    expect(onRenameTable).toHaveBeenCalledWith('patients');
    fireEvent.click(tableActions);
    fireEvent.click(screen.getByTestId('construction-duplicate-table'));
    expect(onDuplicateTable).toHaveBeenCalledOnce();
    fireEvent.click(tableActions);
    fireEvent.pointerDown(document.body);
    expect(screen.queryByTestId('construction-table-actions-menu')).not.toBeInTheDocument();
    fireEvent.click(tableActions);
    fireEvent.click(screen.getByTestId('construction-delete-table'));
    expect(onDeleteTable).toHaveBeenCalledOnce();
    expect(screen.queryByRole('button', { name: 'Move Patients down' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Move Patients up' })).not.toBeInTheDocument();
    expect(screen.getByTestId('construction-history')).toBeInTheDocument();
    expect(screen.getByTestId('construction-history-source')).toHaveTextContent('Patient');
  });

  it('opens an editable query step directly and retains its existing edit and remove controls', () => {
    const step = { id: 'filter', title: 'Filter rows', summary: 'Keep matching observations.', editable: true };
    const onSelect = vi.fn();
    const onEditStep = vi.fn();
    const onRemoveStep = vi.fn();
    const view = render(<ConstructionHistory resourceType="Patient" steps={[step]} selected={{ kind: 'source' }} onSelect={onSelect} onEditStep={onEditStep} onRemoveStep={onRemoveStep} />);
    expect(screen.getByRole('region', { name: 'Row operations' })).toBeInTheDocument();
    expect(screen.getByTestId('construction-history-step-filter')).toHaveTextContent('Filter rows');
    expect(screen.queryByTestId('construction-edit-step-filter')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-history-step-filter'));
    expect(onSelect).not.toHaveBeenCalled();
    expect(onEditStep).toHaveBeenCalledWith('filter');
    view.rerender(<ConstructionHistory resourceType="Patient" steps={[step]} selected={{ kind: 'step', stepId: 'filter' }} onSelect={onSelect} onEditStep={onEditStep} onRemoveStep={onRemoveStep} />);
    expect(screen.getByTestId('construction-edit-step-filter')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('construction-edit-step-filter'));
    expect(onEditStep).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByTestId('construction-remove-step-filter'));
    expect(onRemoveStep).toHaveBeenCalledWith('filter');
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
    expect(screen.queryByText('Preview rows')).not.toBeInTheDocument();
    expect(screen.queryByText('Full count not measured')).not.toBeInTheDocument();
    expect(screen.queryByTestId('construction-undo')).not.toBeInTheDocument();
    expect(screen.getByText('Calculate editor').closest('[aria-label="Change editor"]')).toBeInTheDocument();
    expect(screen.queryByTestId('construction-row-meaning')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'What each row represents' })).not.toBeInTheDocument();
    expect(screen.queryByTestId('construction-row-setup')).toBeNull();
    expect(screen.queryByTestId('construction-action-add-columns')).toBeNull();
    expect(screen.getByText('Current rows')).toBeInTheDocument();
  });

  it('places the horizontal row-operation strip beside the table result instead of Tables', () => {
    render(<ConstructionWorkspace
      tables={[{ outputId: 'patients', title: 'Patients' }]}
      selectedOutputId="patients"
      onSelectTable={vi.fn()}
      onNewTable={vi.fn()}
      onDuplicateTable={vi.fn()}
      onDeleteTable={vi.fn()}
      onRenameTable={vi.fn()}
      onSelectFamily={vi.fn()}
      history={{
        resourceType: 'Patient',
        steps: [{ id: 'filter', title: 'Filter rows', summary: 'Keep active patients.', editable: true }],
        selected: { kind: 'source' },
        onSelect: vi.fn(),
      }}
      preview={<div>Preview table</div>}
      previewStatus="ready"
      draftVersion={1}
      draftDigest="digest-1"
    />);

    expect(screen.getByTestId('construction-history')).toBeInTheDocument();
    expect(screen.getByTestId('construction-history-source')).toHaveTextContent('Patient');
    expect(within(screen.getByRole('navigation', { name: 'Tables' })).queryByTestId('construction-history')).not.toBeInTheDocument();
    expect(screen.getByTestId('construction-history').compareDocumentPosition(screen.getByTestId('construction-action-add-columns')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByTestId('construction-action-add-columns').compareDocumentPosition(screen.getByTestId('construction-preview')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('keeps the starting-record source chip visible when there are no saved row operations', () => {
    render(<ConstructionWorkspace
      tables={[{ outputId: 'patients', title: 'Patients' }]}
      selectedOutputId="patients"
      onSelectTable={vi.fn()}
      onNewTable={vi.fn()}
      onDuplicateTable={vi.fn()}
      onDeleteTable={vi.fn()}
      onRenameTable={vi.fn()}
      onSelectFamily={vi.fn()}
      rowSetup={<button type="button" data-testid="source-settings">Patient</button>}
      history={{ resourceType: 'Patient', steps: [], selected: { kind: 'source' }, onSelect: vi.fn() }}
      preview={<div>Preview table</div>}
      previewStatus="ready"
      draftVersion={1}
      draftDigest="digest-1"
    />);

    expect(screen.getByTestId('construction-history')).toBeInTheDocument();
    expect(screen.getByTestId('source-settings')).toHaveTextContent('Patient');
    expect(screen.queryByTestId(/^construction-history-step-/)).not.toBeInTheDocument();
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
      { id: 'filter_bmi', title: 'Filter BMI', summary: 'Filter output rows where BMI is at least 25.', editable: true },
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
        { id: 'related-observation', title: 'Related Observation', summary, editable: true },
      ]);
    }
  });

  it('labels saved expansions from their exact chained input columns', () => {
    const longFieldLabel = 'American Joint Committee on Cancer pathological N category allowable value';
    const construction: Construction = {
      version: 1,
      steps: [
        {
          id: 'related-research-subject',
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'RELATED_SOURCE',
            relatedSource: {
              anchorColumnId: 'specimen-id',
              choiceId: 'research-subject-status',
              sourceOccurrenceId: 'research-subject',
              source: {
                kind: 'FIELD', candidateId: 'research-subject-status', nodeId: 'research-subject',
                resourceType: 'ResearchSubject', path: 'status', cardinality: 'optional_one', logicalType: 'string',
              },
              route: [],
              contributorRule: { policy: 'ALL_MATCHES' },
              form: 'ALL',
              outputColumnId: 'type-coding-code',
            },
          },
          outputs: [{ id: 'type-coding-code', name: 'type_coding_code', label: 'Type Coding Code' }],
        },
        {
          id: 'expand-type-coding-code',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'related-research-subject' }],
          operation: { kind: 'EXPAND', expand: {
            constructionId: 'expand-type-coding-code', inputColumnId: 'type-coding-code', outputColumnId: 'allowed-value',
          } },
          outputs: [{ id: 'allowed-value', name: 'allowed_value', label: longFieldLabel }],
        },
        {
          id: 'expand-allowed-value',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'expand-type-coding-code' }],
          operation: { kind: 'EXPAND', expand: {
            constructionId: 'expand-allowed-value', inputColumnId: 'allowed-value', outputColumnId: 'identifier-value',
          } },
          outputs: [{ id: 'identifier-value', name: 'identifier_value', label: 'Identifier Value' }],
        },
        {
          id: 'expand-identifier-value',
          inputs: [{ kind: 'STEP_OUTPUT', stepId: 'expand-allowed-value' }],
          operation: { kind: 'EXPAND', expand: {
            constructionId: 'expand-identifier-value', inputColumnId: 'identifier-value', outputColumnId: 'identifier',
          } },
          outputs: [{ id: 'identifier', name: 'identifier', label: 'Identifier' }],
        },
      ],
    };
    const steps = constructionHistorySteps(construction, []);
    const expectedTitles = [
      'Related ResearchSubject',
      'Expand Type Coding Code',
      `Expand ${longFieldLabel}`,
      'Expand Identifier Value',
    ];

    expect(steps.map(({ title }) => title)).toEqual(expectedTitles);
    expect(new Set(expectedTitles.slice(1)).size).toBe(3);

    render(
      <ConstructionHistory
        resourceType="Specimen"
        steps={steps}
        selected={{ kind: 'source' }}
        onSelect={vi.fn()}
      />,
    );

    for (const step of steps) {
      const button = screen.getByRole('button', { name: step.title });
      expect(button).toHaveAttribute('data-testid', `construction-history-step-${step.id}`);
    }

    const longTitleButton = screen.getByTestId('construction-history-step-expand-allowed-value');
    expect(within(longTitleButton).getByText(`Expand ${longFieldLabel}`))
      .toHaveClass('block', 'max-w-48', 'truncate');
    expect(longTitleButton).toHaveAttribute(
      'title',
      `Expand ${longFieldLabel} into rows with Identifier Value columns.`,
    );
  });

  it('keeps a pivot with many categories readable in the step history', () => {
    const categories = Array.from({ length: 5 }, (_, index) => ({
      key: { kind: 'STRING' as const, string: `specimen-${index}` },
      outputColumnId: `pivot-${index}`,
    }));
    const construction: Construction = {
      version: 1,
      steps: [{
        id: 'pivot-specimens',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'PIVOT', pivot: {
          constructionId: 'pivot-specimens',
          groupKeyIds: ['patient'],
          categoryColumnId: 'specimen',
          valueColumnId: 'value',
          categories,
          duplicatePolicy: 'ERROR',
          missingCellPolicy: 'NULL',
          unlistedCategoryPolicy: 'ERROR',
        } },
        outputs: [
          { id: 'patient', name: 'patient', label: 'Patient Reference' },
          ...categories.map(({ outputColumnId }, index) => ({
            id: outputColumnId, name: outputColumnId, label: `specimen-${index}`,
          })),
        ],
      }],
    };

    const [step] = constructionHistorySteps(construction, [
      { id: 'patient', name: 'patient', label: 'Patient Reference' },
      { id: 'specimen', name: 'specimen', label: 'Specimen ID' },
      { id: 'value', name: 'value', label: 'Value' },
    ]);
    expect(step?.title).toBe('Pivot Specimen ID from Value');
    expect(step?.summary).toBe('Group by Patient Reference; use Specimen ID to create 5 columns from Value.');
  });

  it('identifies configured input fields in the Unpivot history title', () => {
    const construction: Construction = {
      version: 1,
      steps: [{
        id: 'unpivot-visits',
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'UNPIVOT', unpivot: {
          constructionId: 'unpivot-visits',
          inputs: [
            { columnId: 'baseline', key: { kind: 'STRING', string: 'baseline' } },
            { columnId: 'follow-up', key: { kind: 'INTEGER', integer: 2 } },
          ],
          keyOutputColumnId: 'visit',
          valueOutputColumnId: 'result',
          nullRowPolicy: 'PRESERVE',
        } },
        outputs: [
          { id: 'visit', name: 'visit', label: 'Visit' },
          { id: 'result', name: 'result', label: 'Result' },
        ],
      }],
    };

    const [step] = constructionHistorySteps(construction, [
      { id: 'baseline', name: 'baseline', label: 'Baseline' },
      { id: 'follow-up', name: 'follow_up', label: 'Follow up' },
    ]);
    expect(step?.title).toBe('Unpivot Baseline, Follow up');
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
        title: 'Group by Cohort ID',
        summary: 'Group rows by Cohort ID; Total score sums Score.',
        editable: true,
      },
      {
        id: 'expand_items',
        title: 'Expand Items',
        summary: 'Expand Items into rows with Item and Position columns. Empty lists: exclude.',
        editable: true,
      },
      {
        id: 'append_visits',
        title: 'Combine',
        summary: 'Append rows from visits (revision revision-7).',
        editable: true,
      },
    ]);
  });

  it('requires a matching compiler proof before enabling source fields after a row change', () => {
    const direct = [{
      id: 'source_projection',
      inputStageId: '',
      rowIdentityColumn: 'source_row_id',
      columns: [],
      capabilities: [],
    }];
    const transformed = [
      ...direct,
      {
        id: 'related_expand',
        inputStageId: 'source_projection',
        rowIdentityColumn: 'related_row_id',
        columns: [],
        capabilities: [],
      },
    ];
    const grouped = [
      ...direct,
      {
        id: 'group_cohorts',
        inputStageId: 'source_projection',
        rowIdentityColumn: 'cohort_row_id',
        columns: [],
        capabilities: [{ kind: 'ROW_VALUES' as const, supported: true }],
      },
    ];
    expect(sourceProjectionAvailability(direct).available).toBe(true);
    expect(sourceProjectionAvailability(undefined).available).toBe(false);
    expect(sourceProjectionAvailability(transformed).available).toBe(false);
    expect(sourceProjectionAvailability(grouped, {
      stageId: 'group_cohorts', supported: false,
    }, 'group_cohorts')).toMatchObject({
      available: true,
      reason: expect.stringContaining('contributing to each grouped row'),
    });
    expect(sourceProjectionAvailability(grouped, {
      stageId: 'group_cohorts', supported: false,
    }, 'stale_stage').available).toBe(false);
    expect(sourceProjectionAvailability(transformed, {
      stageId: 'related_expand', supported: true,
    }, 'related_expand').available).toBe(true);
    expect(sourceProjectionAvailability(transformed, {
      stageId: 'source_projection', supported: true,
    }, 'related_expand')).toMatchObject({
      available: false,
      reason: expect.stringContaining('does not match'),
    });
    expect(sourceProjectionAvailability(transformed, {
      stageId: 'related_expand', supported: false,
    }, 'related_expand')).toMatchObject({
      available: false,
      reason: expect.stringContaining('not available'),
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
      rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
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
