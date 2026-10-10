// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';
import type { LoomClient } from '../../../api';
import type {
  TableShapeCapabilities,
  TableShapeCategoryDiscovery,
  TableShapeComparison,
  TableShapeProposal,
  TableShapeProposalIntent as WireTableShapeProposalIntent,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { TableShapeSettingsPanel, type TableShapeSettingsPanelProps } from './TableShapeSettingsPanel';

const reference = <Kind extends string>(kind: Kind, choiceId: string) => ({ kind, choiceId });
const supported = { kind: 'supported' as const };
const editorChoice = <Kind extends string>(choiceKind: Kind, choiceId: string, label: string) => ({
  choiceId, choiceKind, label, availability: supported,
});

const categoryPair = {
  categoryColumn: reference('column', 'category'),
  valueColumn: reference('column', 'measure'),
};

const category = {
  ...editorChoice('pivotCategory', 'cat-sbp', 'Systolic'),
  suggestedOutput: { column: 'systolic', label: 'Systolic' },
  value: { kind: 'STRING' as const, string: 'systolic' },
};

const noneIntent = {
  kind: 'NONE' as const,
  reshapeMode: reference('reshapeMode', 'mode-none'),
  derivedColumns: [],
};

const savedPivotIntent = {
  kind: 'GROUPED_PIVOT' as const,
  reshapeMode: reference('reshapeMode', 'mode-pivot'),
  pivot: {
    groupColumns: [reference('column', 'patient-id')],
    categoryColumn: categoryPair.categoryColumn,
    valueColumn: categoryPair.valueColumn,
    categoryDiscoveryIdentity: 'saved-discovery',
    includedCategories: [{ category: reference('pivotCategory', 'cat-sbp'), output: { column: 'saved_sbp', label: 'Saved SBP' } }],
    duplicatePolicy: reference('duplicatePolicy', 'duplicate-error'),
    missingCellPolicy: reference('missingCellPolicy', 'missing-null'),
    unlistedCategoryPolicy: reference('unlistedCategoryPolicy', 'unlisted-error'),
  },
  derivedColumns: [],
};

const capabilities = (savedProposalIntent: WireTableShapeProposalIntent = noneIntent): TableShapeCapabilities => ({
  catalogId: 'catalog-1',
  outputId: 'table-1',
  reshapeModes: [
    { ...editorChoice('reshapeMode', 'mode-none', 'Keep columns'), mode: 'NONE' },
    { ...editorChoice('reshapeMode', 'mode-pivot', 'Grouped pivot'), mode: 'GROUPED_PIVOT' },
    { ...editorChoice('reshapeMode', 'mode-unpivot', 'Unpivot rows'), mode: 'UNPIVOT' },
  ],
  groupColumns: [editorChoice('column', 'patient-id', 'Patient ID')],
  categoryColumns: [editorChoice('column', 'category', 'Category')],
  valueColumns: [editorChoice('column', 'measure', 'Measure'), editorChoice('column', 'measure-2', 'Measure 2')],
  pivotCategoryDiscovery: { kind: 'not-requested' },
  duplicatePolicies: [editorChoice('duplicatePolicy', 'duplicate-error', 'Reject duplicates')],
  missingCellPolicies: [editorChoice('missingCellPolicy', 'missing-null', 'Use null')],
  unlistedCategoryPolicies: [editorChoice('unlistedCategoryPolicy', 'unlisted-error', 'Reject new categories')],
  unpivotColumns: [editorChoice('column', 'measure', 'Measure'), editorChoice('column', 'measure-2', 'Measure 2')],
  unpivotKeyOutput: {
    kind: 'supported', resultTypeLabel: 'text', suggestions: [{
      ...editorChoice('unpivotKeyOutput', 'key-output', 'Source name'),
      resultTypeLabel: 'text', suggestedOutput: { column: 'source_name', label: 'Source name' },
    }],
  },
  unpivotValueOutput: {
    kind: 'supported', resultTypeLabel: 'number', suggestions: [{
      ...editorChoice('unpivotValueOutput', 'value-output', 'Value'),
      resultTypeLabel: 'number', suggestedOutput: { column: 'value', label: 'Value' },
    }],
  },
  unpivotNullRowPolicies: [editorChoice('unpivotNullRowPolicy', 'null-drop', 'Drop null rows')],
  derivedAvailability: supported,
  unpivotWithDerivedAvailability: { kind: 'unsupported', reason: 'Derived values after unpivot are unavailable.' },
  derivedOutputSuggestions: [{
    ...editorChoice('derivedOutput', 'derived-output', 'Calculated value'),
    resultTypeLabel: 'number', suggestedOutput: { column: 'calculated', label: 'Calculated value' },
  }],
  binaryOperators: [{ ...editorChoice('binaryOperator', 'add', 'Add'), requiresDivisionByZeroPolicy: false }],
  operands: [editorChoice('operand', 'weight', 'Weight'), editorChoice('operand', 'height', 'Height')],
  missingInputPolicies: [editorChoice('missingInputPolicy', 'missing-propagate', 'Propagate missing')],
  divisionByZeroPolicies: [],
  savedProposalIntent,
  savedProposalAvailability: supported,
});

const discovery: TableShapeCategoryDiscovery = {
  catalogId: 'catalog-1',
  kind: 'complete',
  discoveryIdentity: 'discovery-1',
  pair: categoryPair,
  categories: [category],
};

const comparison: TableShapeComparison = {
  status: 'AVAILABLE' as const,
  base: { rowCount: 2, sampled: false },
  candidate: { rowCount: 2, sampled: false },
  changedColumns: ['calculated'],
  changedRowCount: 1,
  changedRowsSampled: false,
  changedRows: [{
    rowIdentity: 'row-1', basePresent: true, candidatePresent: true, changedColumns: ['calculated'],
    changedCells: [{
      column: 'calculated',
      before: { present: true, value: null },
      after: { present: true, value: 0 },
      trace: {
        state: 'AVAILABLE' as const, cellStatus: 'VALUE', complete: true, sampled: false,
        contributors: [{ resourceType: 'Observation', resourceId: 'obs-1', value: '' }],
      },
    }],
  }],
  contributors: [{ resourceType: 'Observation', resourceId: 'obs-1' }],
  contributorsSampled: false,
  exclusions: { status: 'COMPLETE', records: [], complete: true, sampled: false },
  declaredInformationLoss: { status: 'COMPLETE', items: [] },
  evidenceLimitations: [{ code: 'CHANGED_CELLS_ONLY', message: 'Trace values are returned only for changed cells.' }],
  notices: ['One missing value was preserved.'],
};

const proposal = (mode: 'ADD' | 'REPLACE' | 'REMOVE', overrides: Partial<TableShapeProposal> = {}): TableShapeProposal => ({
  proposalId: 'proposal-1',
  baseReceiptId: 'receipt-1',
  baseDocumentDigest: 'document-1',
  candidateWorkspaceDigest: 'workspace-2',
  draftDigest: 'digest-1',
  draftVersion: 1,
  mode,
  outputId: 'table-1',
  snapshotToken: 'snapshot-1',
  comparison,
  ...overrides,
});

const table: DraftTable = {
  outputId: 'table-1', tabId: 'table-tab', title: 'Measurements',
  document: {
    kind: 'ExplorerBuilderDocument', output: { id: 'table-1', title: 'Measurements' },
    rootResourceType: 'Patient', route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} }, columns: [],
  },
};

const args: Omit<TableShapeSettingsPanelProps, 'client' | 'onApply'> = {
  project: 'study/project', explorerId: 'explorer-1', authResourcePath: '/programs/study/projects/project',
  snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'digest-1', table, disabled: false,
};

type TableShapeClientMethods = Pick<LoomClient,
  'getTableShapeCapabilities' | 'discoverTableShapeCategories' | 'resolveTableShape' | 'proposeTableShape'>;
type TableShapeClientMocks = { readonly [Key in keyof TableShapeClientMethods]: Mock<TableShapeClientMethods[Key]> };

const makeClient = (overrides: Partial<TableShapeClientMocks> = {}): TableShapeClientMocks => ({
  getTableShapeCapabilities: vi.fn<TableShapeClientMethods['getTableShapeCapabilities']>(async () => capabilities()),
  discoverTableShapeCategories: vi.fn<TableShapeClientMethods['discoverTableShapeCategories']>(async () => discovery),
  resolveTableShape: vi.fn<TableShapeClientMethods['resolveTableShape']>(async (request) => ({
    catalogId: 'catalog-1', resolutionId: `resolution-${request.kind.toLowerCase()}`,
    kind: request.kind, outputDescriptors: [], postPivotOperands: [],
  })),
  proposeTableShape: vi.fn<TableShapeClientMethods['proposeTableShape']>(async (request) => proposal(request.mode)),
  ...overrides,
});

const renderPanel = (
  client = makeClient(),
  onApply = vi.fn(async () => true),
  panelArgs: Partial<Omit<TableShapeSettingsPanelProps, 'client' | 'onApply'>> = {},
) => {
  const view = render(<TableShapeSettingsPanel {...args} {...panelArgs} client={client} onApply={onApply} />);
  return { ...view, client, onApply };
};

const open = async () => {
  fireEvent.click(await screen.findByTestId('ui04-open-table-shape-settings'));
  await screen.findByTestId('ui04-reshape-mode');
};
const choose = (label: string, value: string) => fireEvent.change(screen.getByRole('combobox', { name: label }), { target: { value } });
const enter = (label: string, value: string) => fireEvent.change(screen.getByRole('textbox', { name: label }), { target: { value } });
const discoverAndSelectSystolic = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Discover categories' }));
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Include Systolic category' }));
};
const choosePivot = () => {
  choose('Table shape', 'mode-pivot');
  fireEvent.click(screen.getByRole('checkbox', { name: 'Pivot group columns: Patient ID' }));
  choose('Pivot category column', 'category');
  choose('Pivot value column', 'measure');
  choose('Duplicate cell policy', 'duplicate-error');
  choose('Missing cell policy', 'missing-null');
  choose('Unlisted category policy', 'unlisted-error');
};

afterEach(cleanup);

describe('TableShapeSettingsPanel receipt-driven controller', () => {
  it('loads the server catalog and reconstructs the saved pivot before editing', async () => {
    const client = makeClient({ getTableShapeCapabilities: vi.fn(async () => capabilities(savedPivotIntent)) });
    renderPanel(client);
    await open();

    expect(client.getTableShapeCapabilities).toHaveBeenCalledWith(expect.objectContaining({
      project: args.project, explorerId: args.explorerId, authResourcePath: args.authResourcePath,
      snapshotToken: 'snapshot-1', expectedDraftVersion: 1, expectedDraftDigest: 'digest-1', outputId: 'table-1',
    }));
    const outputColumn = await screen.findByRole('textbox', { name: 'Systolic output column' });
    expect((outputColumn as HTMLInputElement).value).toBe('saved_sbp');
    expect(client.discoverTableShapeCategories).toHaveBeenCalledWith(expect.objectContaining({
      catalogId: 'catalog-1', categoryColumnChoiceId: 'category', valueColumnChoiceId: 'measure',
    }));
  });

  it('invalidates categories when the pair changes and preserves the draft after recoverable discovery failure', async () => {
    const client = makeClient({
      discoverTableShapeCategories: vi.fn()
        .mockResolvedValueOnce(discovery)
        .mockRejectedValueOnce(new Error('Discovery service busy')),
    });
    renderPanel(client);
    await open();
    choosePivot();
    await discoverAndSelectSystolic();
    expect((screen.getByRole('checkbox', { name: 'Include Systolic category' }) as HTMLInputElement).checked).toBe(true);

    choose('Pivot value column', 'measure-2');
    expect(screen.queryByRole('checkbox', { name: 'Include Systolic category' })).toBeNull();
    choose('Pivot value column', 'measure');
    fireEvent.click(screen.getByRole('button', { name: 'Discover categories' }));

    expect((await screen.findByRole('alert')).textContent).toContain('Discovery service busy');
    expect((screen.getByRole('checkbox', { name: 'Pivot group columns: Patient ID' }) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByRole('combobox', { name: 'Pivot value column' }) as HTMLSelectElement).value).toBe('measure');
  });

  it('resolves pivot first, maps exact pivot descriptors, then resolves derived columns in authored order', async () => {
    const client = makeClient({
      resolveTableShape: vi.fn(async (request: Parameters<LoomClient['resolveTableShape']>[0]) => request.kind === 'PIVOT'
        ? {
            catalogId: 'catalog-1', resolutionId: 'pivot-resolution', kind: 'PIVOT', postPivotOperands: [],
            outputDescriptors: [
              { kind: 'group', groupColumn: reference('column', 'patient-id'), operandChoiceId: 'pivot-patient-operand', outputColumn: 'patient', outputLabel: 'Patient', type: { logicalType: 'string', nullable: false } },
              { kind: 'category', category: reference('pivotCategory', 'cat-sbp'), operandChoiceId: 'pivot-sbp-operand', outputColumn: 'systolic', outputLabel: 'Systolic', type: { logicalType: 'number', nullable: true } },
            ],
          }
        : request.kind === 'DERIVED' ? {
            catalogId: 'catalog-1', resolutionId: `derived-${request.derived.outputColumn}`, kind: 'DERIVED',
            postPivotOperands: [], outputDescriptors: [{ kind: 'derived', outputColumn: request.derived.outputColumn, outputLabel: request.derived.outputLabel, type: { logicalType: 'number', nullable: true } }],
          } : {
            catalogId: 'catalog-1', resolutionId: 'unpivot-resolution', kind: 'UNPIVOT',
            postPivotOperands: [], outputDescriptors: [],
          }),
    });
    renderPanel(client);
    await open();
    choosePivot();
    await discoverAndSelectSystolic();

    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enter('Derived column 1 output column', 'adjusted_sbp');
    enter('Derived column 1 output label', 'Adjusted SBP');
    choose('Derived column 1 operation', 'add');
    choose('Derived column 1 first operand source', 'pivotOutput');
    fireEvent.change(screen.getByTestId('ui04-derived-left-operand-1-pivot-output'), { target: { value: 'category:cat-sbp' } });
    choose('Derived column 1 second operand source', 'literal');
    enter('Derived column 1 second operand numeric literal', '0');
    choose('Derived column 1 missing-input policy', 'missing-propagate');

    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enter('Derived column 2 output column', 'twice_adjusted_sbp');
    enter('Derived column 2 output label', 'Twice adjusted SBP');
    choose('Derived column 2 operation', 'add');
    choose('Derived column 2 first operand source', 'derived');
    fireEvent.change(screen.getByTestId('ui04-derived-left-operand-2-derived'), { target: { value: 'draft-0' } });
    choose('Derived column 2 second operand source', 'pivotOutput');
    fireEvent.change(screen.getByTestId('ui04-derived-right-operand-2-pivot-output'), { target: { value: 'group:patient-id' } });
    choose('Derived column 2 missing-input policy', 'missing-propagate');

    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    await screen.findByTestId('ui04-table-shape-comparison');

    const requests = vi.mocked(client.resolveTableShape).mock.calls.map(([request]) => request);
    expect(requests.map((request) => request.kind)).toEqual(['PIVOT', 'DERIVED', 'DERIVED']);
    expect(requests[1]).toMatchObject({
      derived: {
        outputColumn: 'adjusted_sbp', pivotResolutionId: 'pivot-resolution',
        left: { kind: 'CATALOG_CHOICE', choiceId: 'pivot-sbp-operand' },
        right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 0 } },
      },
    });
    expect(requests[2]).toMatchObject({
      derived: {
        outputColumn: 'twice_adjusted_sbp',
        left: { kind: 'RESOLUTION_OUTPUT', resolutionId: 'derived-adjusted_sbp' },
        right: { kind: 'CATALOG_CHOICE', choiceId: 'pivot-patient-operand' },
      },
    });
    expect(client.proposeTableShape).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'ADD', catalogId: 'catalog-1', reshapeResolutionId: 'pivot-resolution',
      derivedResolutionIds: ['derived-adjusted_sbp', 'derived-twice_adjusted_sbp'],
    }));
  });

  it('resolves derived-only definitions and sends typed decimal literals without client-authored table shape', async () => {
    const client = makeClient({
      resolveTableShape: vi.fn(async (request: Parameters<LoomClient['resolveTableShape']>[0]) => ({
        catalogId: 'catalog-1', resolutionId: 'derived-resolution', kind: 'DERIVED',
        outputDescriptors: [], postPivotOperands: [],
      })),
    });
    renderPanel(client);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enter('Derived column 1 output column', 'weight_plus_half');
    enter('Derived column 1 output label', 'Weight plus half');
    choose('Derived column 1 operation', 'add');
    choose('Derived column 1 first operand', 'weight');
    choose('Derived column 1 second operand source', 'literal');
    enter('Derived column 1 second operand numeric literal', '0.5');
    choose('Derived column 1 missing-input policy', 'missing-propagate');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    await screen.findByTestId('ui04-table-shape-comparison');
    expect(client.resolveTableShape).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'DERIVED', derived: expect.objectContaining({
        outputColumn: 'weight_plus_half',
        left: { kind: 'CATALOG_CHOICE', choiceId: 'weight' },
        right: { kind: 'LITERAL', literal: { kind: 'DECIMAL', decimal: 0.5 } },
      }),
    }));
    expect(client.proposeTableShape).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'ADD', derivedResolutionIds: ['derived-resolution'],
    }));
    expect(client.proposeTableShape.mock.calls[0]?.[0]).not.toHaveProperty('tableShape');
  });

  it('resolves unpivot and proposes REMOVE without resolution IDs when a saved shape is cleared', async () => {
    const client = makeClient({
      getTableShapeCapabilities: vi.fn().mockResolvedValueOnce(capabilities()).mockResolvedValueOnce(capabilities(savedPivotIntent)),
      resolveTableShape: vi.fn(async (request) => ({
        catalogId: 'catalog-1', resolutionId: 'unpivot-resolution', kind: 'UNPIVOT',
        outputDescriptors: [], postPivotOperands: [],
      })),
    });
    renderPanel(client);
    await open();
    choose('Table shape', 'mode-unpivot');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Unpivot input columns: Measure' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Unpivot input columns: Measure 2' }));
    choose('Unpivot null-row policy', 'null-drop');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    await screen.findByTestId('ui04-table-shape-comparison');
    expect(client.resolveTableShape).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'UNPIVOT', unpivot: expect.objectContaining({ inputColumnChoiceIds: ['measure', 'measure-2'] }),
    }));
    fireEvent.click(screen.getByTestId('ui04-cancel-table-shape-proposal'));
    fireEvent.click(screen.getByTestId('ui04-open-table-shape-settings'));
    const outputColumn = await screen.findByRole('textbox', { name: 'Systolic output column' });
    expect((outputColumn as HTMLInputElement).value).toBe('saved_sbp');
    choose('Table shape', 'mode-none');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    await screen.findByTestId('ui04-table-shape-comparison');
    expect(client.proposeTableShape).toHaveBeenLastCalledWith(expect.objectContaining({
      mode: 'REMOVE', derivedResolutionIds: [],
    }));
    expect(client.proposeTableShape.mock.calls.at(-1)?.[0]).not.toHaveProperty('reshapeResolutionId');
  });

  it('keeps comparison value presence and source literals distinct, and cancel never applies', async () => {
    const onApply = vi.fn(async () => true);
    const client = makeClient();
    renderPanel(client, onApply);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    await screen.findByRole('textbox', { name: 'Derived column 1 output column' });
    enter('Derived column 1 output column', 'calculated');
    enter('Derived column 1 output label', 'Calculated');
    choose('Derived column 1 operation', 'add');
    choose('Derived column 1 first operand', 'weight');
    choose('Derived column 1 second operand', 'height');
    choose('Derived column 1 missing-input policy', 'missing-propagate');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    const review = await screen.findByTestId('ui04-table-shape-comparison');
    expect(review.textContent).toContain('null');
    expect(review.textContent).toContain('0');
    expect(review.textContent).toContain('Observation/obs-1: ""');
    expect(screen.getByTestId('ui04-comparison-limitations').textContent).toContain('Trace values are returned only for changed cells.');
    expect(screen.getByTestId('ui04-comparison-notices').textContent).toContain('One missing value was preserved.');
    fireEvent.click(screen.getByTestId('ui04-cancel-table-shape-proposal'));

    expect(onApply).not.toHaveBeenCalled();
    expect(screen.queryByTestId('ui04-table-shape-dialog')).toBeNull();
  });

  it('rejects stale proposal identity without enabling apply', async () => {
    const staleProposal = proposal('ADD', { draftVersion: 2 });
    const client = makeClient({ proposeTableShape: vi.fn(async () => staleProposal) });
    renderPanel(client);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enter('Derived column 1 output column', 'calculated');
    enter('Derived column 1 output label', 'Calculated');
    choose('Derived column 1 operation', 'add');
    choose('Derived column 1 first operand', 'weight');
    choose('Derived column 1 second operand', 'height');
    choose('Derived column 1 missing-input policy', 'missing-propagate');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));

    expect(await screen.findByTestId('ui04-stale-table-shape')).toBeTruthy();
    expect(screen.queryByTestId('ui04-confirm-table-shape')).toBeNull();
  });

  it('retains editable values when execution fails instead of offering an unavailable review', async () => {
    const client = makeClient({ proposeTableShape: vi.fn(async () => { throw Object.assign(new Error('The candidate preview failed.'), { status: 500 }); }) });
    const { onApply } = renderPanel(client);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    enter('Derived column 1 output column', 'calculated');
    enter('Derived column 1 output label', 'Calculated');
    choose('Derived column 1 operation', 'add');
    choose('Derived column 1 first operand', 'weight');
    choose('Derived column 1 second operand', 'height');
    choose('Derived column 1 missing-input policy', 'missing-propagate');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('The candidate preview failed.');
    expect(screen.getByDisplayValue('calculated')).toBeTruthy();
    expect(screen.getByRole('combobox', { name: 'Derived column 1 first operand' })).toBeEnabled();
    expect(screen.queryByTestId('ui04-confirm-table-shape')).toBeNull();
    expect(onApply).not.toHaveBeenCalled();
  });

  it('requires a separate confirmation to apply the proposal through the receipt command', async () => {
    const onApply = vi.fn(async () => true);
    const client = makeClient();
    renderPanel(client, onApply);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Add derived column' }));
    await screen.findByRole('textbox', { name: 'Derived column 1 output column' });
    enter('Derived column 1 output column', 'calculated');
    enter('Derived column 1 output label', 'Calculated');
    choose('Derived column 1 operation', 'add');
    choose('Derived column 1 first operand', 'weight');
    choose('Derived column 1 second operand', 'height');
    choose('Derived column 1 missing-input policy', 'missing-propagate');
    fireEvent.click(screen.getByRole('button', { name: 'Preview table shape' }));
    fireEvent.click(await screen.findByTestId('ui04-confirm-table-shape'));

    await waitFor(() => expect(onApply).toHaveBeenCalledWith('proposal-1'));
    expect(screen.queryByTestId('ui04-table-shape-dialog')).toBeNull();
  });
});
