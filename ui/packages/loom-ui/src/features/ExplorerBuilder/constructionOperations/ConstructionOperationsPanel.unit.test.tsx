// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type LoomClient } from '../../../api';
import { LoomProvider } from '../../../react';
import type { ExplorerBuilderCatalog, TableShapeCapabilities } from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { CatalogChoiceIntent } from '../catalogItems';
import { ConstructionOperationsPanel } from './ConstructionOperationsPanel';
import { CONSTRUCTION_OPERATION_FAMILIES, familyPresentation, type ConstructionOperationFamily } from './operationFamilies';

vi.mock('../components/ConceptCatalog', () => ({
  ConceptCatalog: (props: {
    readonly rowRoot: string;
    readonly layout: string;
    readonly resourceType?: string;
    readonly sourceNodeId?: string;
    readonly sourceProjectionAvailability?: { readonly available: boolean; readonly reason: string };
    readonly routeContext?: { readonly occurrenceId: string; readonly nodeId: string };
    readonly onAddSelected?: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<void>;
  }) => React.createElement(
    'div',
    {
      'data-testid': 'mock-concept-catalog',
      'data-row-root': props.rowRoot,
      'data-layout': props.layout,
      'data-resource-type': props.resourceType,
      'data-source-node-id': props.sourceNodeId,
      'data-source-available': props.sourceProjectionAvailability?.available,
      'data-source-reason': props.sourceProjectionAvailability?.reason,
      'data-occurrence-id': props.routeContext?.occurrenceId,
      'data-node-id': props.routeContext?.nodeId,
    },
    React.createElement('button', {
      type: 'button',
      onClick: () => props.onAddSelected?.([{
        constructionChoice: { choiceId: 'choice-1', form: 'VALUE' },
        title: 'Blood pressure',
      }]),
    }, 'Add example feature'),
  ),
}));

afterEach(cleanup);

const table: DraftTable = {
  outputId: 'table-1',
  tabId: 'table-tab',
  title: 'Measurements',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'table-1', title: 'Measurements' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' },
    rows: { kind: 'RECORDS', records: {} },
    columns: [],
  },
};

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: {},
  nodes: [],
  edges: [],
};

const relatedCatalog: ExplorerBuilderCatalog = {
  ...catalog,
  nodes: [
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 10 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: false, populated: true, documentCount: 20 },
  ],
  edges: [{ edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'subject_Patient' }],
};

const capabilities = (): TableShapeCapabilities => ({
  catalogId: 'catalog-1',
  outputId: 'table-1',
  reshapeModes: [
    { choiceId: 'mode-none', choiceKind: 'reshapeMode', label: 'Keep columns', availability: { kind: 'supported' }, mode: 'NONE' },
    { choiceId: 'mode-pivot', choiceKind: 'reshapeMode', label: 'Grouped pivot', availability: { kind: 'unsupported', reason: 'Pivot is unavailable for these columns.' }, mode: 'GROUPED_PIVOT' },
    { choiceId: 'mode-unpivot', choiceKind: 'reshapeMode', label: 'Unpivot rows', availability: { kind: 'unsupported', reason: 'Unpivot is unavailable for this table.' }, mode: 'UNPIVOT' },
  ],
  groupColumns: [],
  categoryColumns: [],
  valueColumns: [],
  pivotCategoryDiscovery: { kind: 'not-requested' },
  duplicatePolicies: [],
  missingCellPolicies: [],
  unlistedCategoryPolicies: [],
  unpivotColumns: [],
  unpivotKeyOutput: { kind: 'unsupported', reason: 'No key output is supported.' },
  unpivotValueOutput: { kind: 'unsupported', reason: 'No value output is supported.' },
  unpivotNullRowPolicies: [],
  derivedAvailability: { kind: 'supported' },
  unpivotWithDerivedAvailability: { kind: 'unsupported', reason: 'Derived outputs are unavailable after unpivot.' },
  derivedOutputSuggestions: [{
    choiceId: 'output-1', choiceKind: 'derivedOutput', label: 'Calculated value', availability: { kind: 'supported' },
    resultTypeLabel: 'number', suggestedOutput: { column: 'calculated', label: 'Calculated value' },
  }],
  binaryOperators: [{
    choiceId: 'add-1', choiceKind: 'binaryOperator', label: 'Add', availability: { kind: 'supported' },
    requiresDivisionByZeroPolicy: false,
  }],
  operands: [{ choiceId: 'weight-1', choiceKind: 'operand', label: 'Weight', availability: { kind: 'supported' } }],
  missingInputPolicies: [{ choiceId: 'propagate-1', choiceKind: 'missingInputPolicy', label: 'Propagate missing', availability: { kind: 'supported' } }],
  divisionByZeroPolicies: [],
  savedProposalIntent: {
    kind: 'NONE',
    reshapeMode: { kind: 'reshapeMode', choiceId: 'mode-none' },
    derivedColumns: [],
  },
  savedProposalAvailability: { kind: 'supported' },
});

const renderPanel = (args: {
  readonly family?: ConstructionOperationFamily;
  readonly selectedColumns?: ReadonlyArray<string>;
  readonly catalog?: ExplorerBuilderCatalog;
  readonly routeContext?: { readonly occurrenceId: string; readonly nodeId: string };
  readonly sourceProjectionAvailability?: { readonly available: boolean; readonly reason: string };
  readonly onAddSelected?: (selections: ReadonlyArray<CatalogChoiceIntent>) => Promise<void>;
  readonly client?: LoomClient;
} = {}) => render(
  <LoomProvider client={args.client ?? createLoomClient()}>
    <ConstructionOperationsPanel
      family={args.family ?? 'KEEP_ROWS'}
      project="study/project"
      explorerId="explorer-1"
      snapshotToken="snapshot-1"
      draftVersion={1}
      draftDigest="draft-digest-1"
      table={table}
      catalog={args.catalog ?? catalog}
      rowRoot="Patient"
      selectedColumns={args.selectedColumns ?? []}
      routeContext={args.routeContext}
      sourceProjectionAvailability={args.sourceProjectionAvailability}
      disabled={false}
      onAddSelected={args.onAddSelected ?? (async () => undefined)}
      onApplyProposal={async () => true}
    />
  </LoomProvider>,
);

describe('construction operation families', () => {
  it('keeps the five action families ordered and gives each an approachable introduction', () => {
    expect(CONSTRUCTION_OPERATION_FAMILIES).toEqual([
      'ADD_COLUMNS',
      'KEEP_ROWS',
      'CALCULATE',
      'RESHAPE',
      'COMBINE',
    ]);
    for (const family of CONSTRUCTION_OPERATION_FAMILIES) {
      const presentation = familyPresentation(family);
      expect(presentation.title.trim()).not.toBe('');
      expect(presentation.introduction.trim()).not.toBe('');
    }
  });

  it('states why stage-level row operations are unavailable and retains selected input context', () => {
    renderPanel({ selectedColumns: ['age', 'measurement'] });

    const moreOptions = screen.getByTestId('construction-operation-unavailable');
    expect(moreOptions).not.toHaveAttribute('open');
    fireEvent.click(within(moreOptions).getByText('See more options'));
    const unavailable = within(screen.getByTestId('construction-operation-unavailable'));
    expect(unavailable.getByText('Match conditions')).toBeDisabled();
    expect(unavailable.getByText(/cannot be filtered by their values/)).toBeInTheDocument();
    expect(unavailable.getByText(/cannot yet rank rows or choose what to do with ties/)).toBeInTheDocument();

    const inputs = within(screen.getByTestId('construction-operation-selected-inputs'));
    expect(inputs.getByText('age')).toBeInTheDocument();
    expect(inputs.getByText('measurement')).toBeInTheDocument();
  });

  it('opens information discovery with the selected route context and forwards selections', async () => {
    const onAddSelected = vi.fn(async () => undefined);
    renderPanel({
      family: 'ADD_COLUMNS',
      routeContext: { occurrenceId: 'observation-1', nodeId: 'node-observation' },
      onAddSelected,
    });

    fireEvent.click(screen.getByTestId('construction-operation-intention-add_columns-find_information'));
    const catalogPanel = screen.getByTestId('mock-concept-catalog');
    expect(catalogPanel).toHaveAttribute('data-row-root', 'Patient');
    expect(catalogPanel).toHaveAttribute('data-layout', 'panel');
    expect(catalogPanel).toHaveAttribute('data-source-node-id', 'node-observation');
    expect(catalogPanel).toHaveAttribute('data-occurrence-id', 'observation-1');
    expect(catalogPanel).toHaveAttribute('data-node-id', 'node-observation');

    fireEvent.click(screen.getByRole('button', { name: 'Add example feature' }));
    expect(onAddSelected).toHaveBeenCalledWith([{
      constructionChoice: { choiceId: 'choice-1', form: 'VALUE' },
      title: 'Blood pressure',
    }]);
  });

  it('lets researchers choose a related source node and filters discovery to that node', () => {
    renderPanel({ family: 'ADD_COLUMNS', catalog: relatedCatalog });

    fireEvent.click(screen.getByTestId('construction-operation-intention-add_columns-find_information'));
    const source = screen.getByRole('combobox', { name: /Source/ });
    expect(within(source).getByRole('option', { name: 'Patient — table rows' })).toBeInTheDocument();
    expect(within(source).getByRole('option', { name: 'Observation — related source' })).toBeInTheDocument();

    fireEvent.change(source, { target: { value: 'node:observation-node' } });

    const catalogPanel = screen.getByTestId('mock-concept-catalog');
    expect(catalogPanel).toHaveAttribute('data-resource-type', 'Observation');
    expect(catalogPanel).toHaveAttribute('data-source-node-id', 'observation-node');
    expect(catalogPanel).not.toHaveAttribute('data-node-id');
  });

  it('forwards source projection availability to information discovery', () => {
    renderPanel({
      family: 'ADD_COLUMNS',
      sourceProjectionAvailability: {
        available: false,
        reason: 'The current stage no longer has the source projection needed to add fields.',
      },
    });

    fireEvent.click(screen.getByTestId('construction-operation-intention-add_columns-find_information'));
    const catalogPanel = screen.getByTestId('mock-concept-catalog');
    expect(catalogPanel).toHaveAttribute('data-source-available', 'false');
    expect(catalogPanel).toHaveAttribute('data-source-reason', 'The current stage no longer has the source projection needed to add fields.');
  });

  it('enables calculated values only when the current server choices support them', async () => {
    const client = createLoomClient({
      fetch: async () => new Response(JSON.stringify(capabilities()), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    });
    renderPanel({ family: 'CALCULATE', client });

    const available = within(await screen.findByTestId('construction-operation-available'));
    expect(await available.findByTestId('construction-operation-intention-calculate-calculate_value')).toBeEnabled();
    fireEvent.click(screen.getByText('See more options'));
    expect(screen.getByTestId('construction-operation-intention-calculate-set_by_condition')).toBeDisabled();
    expect(screen.getByText(/Conditional assignments are not available here yet/)).toBeInTheDocument();
  });
});
