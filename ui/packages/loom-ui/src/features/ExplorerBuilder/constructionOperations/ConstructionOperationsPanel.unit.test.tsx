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
    expect(familyPresentation('KEEP_ROWS')).toEqual({
      title: 'Filter rows',
      introduction: 'Choose which rows appear in the table output.',
    });
  });

  it('states why stage-level row operations are unavailable and retains selected input context', () => {
    renderPanel({ selectedColumns: ['age', 'measurement'] });

    const moreOptions = screen.getByTestId('construction-operation-unavailable');
    expect(moreOptions).not.toHaveAttribute('open');
    fireEvent.click(within(moreOptions).getByText('See more options'));
    const unavailable = within(screen.getByTestId('construction-operation-unavailable'));
    expect(unavailable.getByText('Match conditions')).toBeDisabled();
    expect(unavailable.getByText('Filter output rows by rules about their values.')).toBeInTheDocument();
    expect(unavailable.getByText(/filtering output rows by their values is not available/i)).toBeInTheDocument();
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

  describe('operation-choice request failures', () => {
    const jsonResponse = (body: unknown, status: number, headers: Record<string, string> = {}) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json', ...headers },
      });

    const renderCalculate = (fetchImpl: typeof globalThis.fetch) =>
      renderPanel({ family: 'CALCULATE', client: createLoomClient({ fetch: fetchImpl }) });

    const capabilityError = async () => await screen.findByTestId('construction-operation-capability-error');

    it('keeps loading separate from a request failure while the choices request is pending', async () => {
      let release: ((value: Response) => void) | undefined;
      const pending = new Promise<Response>((resolve) => {
        release = resolve;
      });
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockReturnValue(pending);
      renderCalculate(fetchImpl as unknown as typeof globalThis.fetch);

      expect(await screen.findByText('Checking the choices supported for this table…')).toBeInTheDocument();
      expect(screen.queryByTestId('construction-operation-capability-error')).not.toBeInTheDocument();

      release?.(jsonResponse(capabilities(), 200));
      expect(await screen.findByTestId('construction-operation-available')).toBeInTheDocument();
      expect(screen.queryByTestId('construction-operation-capability-error')).not.toBeInTheDocument();
    });

    it('explains a 401 as a sign-in problem and preserves the server message', async () => {
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
        jsonResponse({ message: 'The session token expired.' }, 401),
      );
      renderCalculate(fetchImpl);

      const alert = await capabilityError();
      expect(alert.textContent).toMatch(/signed out/i);
      expect(alert.textContent).toContain('The session token expired.');
      expect(alert.textContent).not.toMatch(/permission/i);
      expect(alert.textContent).not.toMatch(/right now|reached/i);
    });

    it('explains a 403 as a permission problem and preserves the server message', async () => {
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
        jsonResponse({ message: 'The dataset policy blocks this table.' }, 403),
      );
      renderCalculate(fetchImpl);

      const alert = await capabilityError();
      expect(alert.textContent).toMatch(/permission/i);
      expect(alert.textContent).toContain('The dataset policy blocks this table.');
      expect(alert.textContent).not.toMatch(/signed out/i);
      expect(alert.textContent).not.toMatch(/right now|reached/i);
    });

    it('explains a 5xx as an outage and keeps it distinct from sign-in, permission, and network copy', async () => {
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
        jsonResponse({ message: 'The capability service restarted.' }, 503),
      );
      renderCalculate(fetchImpl);

      const alert = await capabilityError();
      expect(alert.textContent).toMatch(/right now/i);
      expect(alert.textContent).toContain('The capability service restarted.');
      expect(alert.textContent).not.toMatch(/signed out|permission|connection/i);
    });

    it('explains a network failure as unreachable and keeps it distinct from the 5xx copy', async () => {
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError('Failed to fetch'));
      renderCalculate(fetchImpl);

      const alert = await capabilityError();
      expect(alert.textContent).toMatch(/could not be reached/i);
      expect(alert.textContent).toMatch(/connection/i);
      expect(alert.textContent).not.toMatch(/right now/i);
      expect(alert.textContent).not.toMatch(/Failed to fetch/i);
    });

    it('keeps the shipped catch-all when Loom gives no request shape to classify', async () => {
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
        jsonResponse({ unexpected: 'payload' }, 200),
      );
      renderCalculate(fetchImpl);

      const alert = await capabilityError();
      expect(alert.textContent).toBe(
        'Could not check operation choices. Reload the table and try again.',
      );
    });

    it('states that the no-cause line covers responses without a server message', async () => {
      const fetchImpl = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
        new Response('', { status: 500 }),
      );
      renderCalculate(fetchImpl);

      const alert = await capabilityError();
      expect(alert.textContent).toMatch(/right now/i);
      expect(alert.textContent).not.toMatch(/Loom said/i);
    });
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
