// @vitest-environment jsdom
import React from 'react';
import { vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type {
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import { ColumnSelector, columnFromCandidate } from './ColumnSelector';

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot',
  generation: 'generation',
  routePolicy: {},
  nodes: [
    {
      nodeId: 'research-subject',
      resourceType: 'ResearchSubject',
      rowRootEligible: true,
      populated: true,
      documentCount: 3,
    },
  ],
  edges: [],
  candidates: [],
};

const table: DraftTable = {
  outputId: 'Patient',
  tabId: 'patient',
  title: 'Patient',
  document: {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'Patient', title: 'Patient' },
    rootResourceType: 'ResearchSubject',
    route: { occurrenceId: 'base', resourceType: 'ResearchSubject' },
    columns: [
      {
        column: 'research_subject_identifier',
        label: 'Research Subject ID',
        occurrenceId: 'base',
        source: {
          kind: 'field',
          field: {
            path: 'identifier[].value',
            projectionMode: 'FIRST',
          },
        },
        table: { visible: true, order: 0 },
      },
    ],
  },
};

describe('configured V2 columns', () => {
  it('edits repeated values independently from related-record selection', () => {
    const onSourceChange = vi.fn();
    const repeatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      candidates: [{
        candidateId: 'c_identifier',
        nodeId: 'research-subject',
        fieldPath: 'identifier[].value',
        label: 'Research Subject ID',
        logicalType: 'string',
        repeated: true,
        filterable: true,
        chartable: false,
        projectionModes: ['FIRST', 'ALL', 'DISTINCT'],
        defaultProjectionMode: 'FIRST',
      }],
    };

    render(<ColumnSelector catalog={repeatedCatalog} table={table} occurrenceId="base"
      disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()} onChange={vi.fn()}
      onSourceChange={onSourceChange} onRemove={vi.fn()} />);

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Repeated values for Research Subject ID',
    }), { target: { value: 'ALL' } });

    expect(onSourceChange).toHaveBeenCalledWith('research_subject_identifier', {
      kind: 'field',
      field: {
        path: 'identifier[].value',
        projectionMode: 'ALL',
      },
    });
    expect(screen.getByText(/within each Research Subject/)).toBeInTheDocument();
  });

  it('edits an existing resource reduction without changing its scope', () => {
    const onSourceChange = vi.fn();
    const onContributorChange = vi.fn();
    const contributorCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      candidates: [{
        candidateId: 'c_identifier',
        nodeId: 'research-subject',
        fieldPath: 'identifier[].value',
        label: 'Identifier',
        logicalType: 'string',
        repeated: true,
        filterable: true,
        chartable: false,
        projectionModes: ['FIRST', 'ALL'],
        defaultProjectionMode: 'FIRST',
      }],
    };
    const aggregateTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [{
          column: 'subject_count',
          label: 'Subject count',
          occurrenceId: 'base',
          logicalType: 'integer',
          source: { kind: 'aggregate', aggregate: { operation: 'COUNT' } },
          table: { visible: true, order: 0 },
        }],
      },
    };

    render(<ColumnSelector catalog={contributorCatalog} table={aggregateTable} occurrenceId="base"
      disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()} onChange={vi.fn()}
      onSourceChange={onSourceChange} onContributorChange={onContributorChange}
      onRemove={vi.fn()} />);

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Calculation for Subject count',
    }), { target: { value: 'EXISTS' } });

    expect(onSourceChange).toHaveBeenCalledWith('subject_count', {
      kind: 'aggregate',
      aggregate: { operation: 'EXISTS' },
    });
    expect(screen.getByText('Counts matching Research Subject resources.')).toBeInTheDocument();

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Contributors for Subject count',
    }), { target: { value: 'c_identifier' } });
    expect(onContributorChange).toHaveBeenCalledWith('subject_count', {
      candidateId: 'c_identifier',
      operator: 'EXISTS',
      quantifier: 'ANY',
    });
  });

  it('adds count and existence features for a related resource without replacing fields', () => {
    const onAddSource = vi.fn();
    const relatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        ...catalog.nodes,
        {
          nodeId: 'observation',
          resourceType: 'Observation',
          rowRootEligible: true,
          populated: true,
          documentCount: 4,
        },
      ],
      edges: [{
        edgeId: 'subject-observation',
        fromNodeId: 'research-subject',
        toNodeId: 'observation',
        label: 'subject_Observation',
      }],
    };
    const relatedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        route: {
          ...table.document.route,
          children: [{
            occurrenceId: 'observations',
            resourceType: 'Observation',
            relationship: 'subject_Observation',
          }],
        },
      },
    };

    render(<ColumnSelector catalog={relatedCatalog} table={relatedTable} occurrenceId="observations"
      disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()} onAddSource={onAddSource}
      onChange={vi.fn()} onSourceChange={vi.fn()} onRemove={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Count' }));
    fireEvent.click(screen.getByRole('button', { name: 'Yes / no' }));

    expect(onAddSource).toHaveBeenNthCalledWith(1,
      { kind: 'aggregate', aggregate: { operation: 'COUNT' } },
      'Observation count',
    );
    expect(onAddSource).toHaveBeenNthCalledWith(2,
      { kind: 'aggregate', aggregate: { operation: 'EXISTS' } },
      'Has Observation',
    );
  });

  it('requires a separate source edit to acknowledge omitting related records', () => {
    const onSourceChange = vi.fn();
    const onChange = vi.fn();
    const relatedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [{
          column: 'observation_value',
          label: 'Observation value',
          occurrenceId: 'base',
          source: {
            kind: 'field',
            field: {
              path: 'valueQuantity.value',
              projectionMode: 'VALUE',
              relatedSelection: { kind: 'first-by-resource-key', acknowledged: false },
            },
          },
          table: { visible: true, order: 0 },
        }],
      },
    };
    render(<ColumnSelector catalog={catalog} table={relatedTable} occurrenceId="base"
      disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()} onChange={onChange}
      onSourceChange={onSourceChange} onRemove={vi.fn()} />);
    const acknowledgment = screen.getByRole('checkbox', { name: 'Allow first related value for Observation value' });
    expect(acknowledgment).toHaveProperty('checked', false);
    expect(screen.getByText(/Other records are omitted/)).toBeInTheDocument();
    fireEvent.click(acknowledgment);
    expect(onSourceChange).toHaveBeenCalledWith('observation_value', {
      kind: 'field',
      field: {
        path: 'valueQuantity.value',
        projectionMode: 'VALUE',
        relatedSelection: { kind: 'first-by-resource-key', acknowledged: true },
      },
    });
    expect(onChange).not.toHaveBeenCalled();
  });

  it('changes a related value into an explicit cross-record reduction and back', () => {
    const onSourceChange = vi.fn();
    const relatedCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      nodes: [
        ...catalog.nodes,
        {
          nodeId: 'observation',
          resourceType: 'Observation',
          rowRootEligible: true,
          populated: true,
          documentCount: 4,
        },
      ],
      edges: [{
        edgeId: 'subject-observation',
        fromNodeId: 'research-subject',
        toNodeId: 'observation',
        label: 'subject_Observation',
      }],
      candidates: [{
        candidateId: 'c_value',
        nodeId: 'observation',
        fieldPath: 'valueQuantity.value',
        label: 'Measured value',
        logicalType: 'decimal',
        repeated: false,
        filterable: true,
        chartable: true,
        projectionModes: ['VALUE'],
        defaultProjectionMode: 'VALUE',
      }],
    };
    const relatedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        route: {
          ...table.document.route,
          children: [{
            occurrenceId: 'observations',
            resourceType: 'Observation',
            relationship: 'subject_Observation',
          }],
        },
        columns: [{
          column: 'observation_value',
          label: 'Observation value',
          occurrenceId: 'observations',
          source: {
            kind: 'field',
            field: {
              path: 'valueQuantity.value',
              projectionMode: 'VALUE',
              relatedSelection: { kind: 'first-by-resource-key', acknowledged: false },
            },
          },
          table: { visible: true, order: 0 },
        }],
      },
    };

    const { rerender } = render(<ColumnSelector catalog={relatedCatalog} table={relatedTable}
      occurrenceId="observations" disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()}
      onChange={vi.fn()} onSourceChange={onSourceChange} onRemove={vi.fn()} />);

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Across related Observation records for Observation value',
    }), { target: { value: 'MAX' } });
    expect(onSourceChange).toHaveBeenCalledWith('observation_value', {
      kind: 'aggregate',
      aggregate: { operation: 'MAX', path: 'valueQuantity.value' },
    });

    const aggregateTable: DraftTable = {
      ...relatedTable,
      document: {
        ...relatedTable.document,
        columns: [{
          ...relatedTable.document.columns[0],
          source: { kind: 'aggregate', aggregate: { operation: 'MAX', path: 'valueQuantity.value' } },
        }],
      },
    };
    rerender(<ColumnSelector catalog={relatedCatalog} table={aggregateTable}
      occurrenceId="observations" disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()}
      onChange={vi.fn()} onSourceChange={onSourceChange} onRemove={vi.fn()} />);

    expect(screen.getByText(/1 configured · 0 available/)).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', {
      name: 'Across related Observation records for Observation value',
    }), { target: { value: 'FIRST_BY_RESOURCE_KEY' } });
    expect(onSourceChange).toHaveBeenLastCalledWith('observation_value', {
      kind: 'field',
      field: {
        path: 'valueQuantity.value',
        projectionMode: 'VALUE',
        relatedSelection: { kind: 'first-by-resource-key', acknowledged: false },
      },
    });
  });

  it('renders document columns even when the catalog has no candidates', () => {
    const onChange = vi.fn();
    render(
      <ColumnSelector
        catalog={catalog}
        table={table}
        occurrenceId="base"
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={onChange}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(screen.getByText('Research Subject columns')).toBeInTheDocument();
    expect(screen.getByText(/research_subject_identifier/)).toBeInTheDocument();
    expect(screen.getByText(/1 configured · 0 available/)).toBeInTheDocument();

    const displayName = screen.getByRole('textbox', {
      name: 'Display name for configured Research Subject ID',
    });
    fireEvent.change(displayName, { target: { value: 'Subject ID' } });
    fireEvent.blur(displayName);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        column: 'research_subject_identifier',
        label: 'Subject ID',
      }),
    );

    fireEvent.click(
      screen.getByRole('checkbox', {
        name: 'Use Research Subject ID as filter',
      }),
    );
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        column: 'research_subject_identifier',
        filter: { label: 'Research Subject ID' },
      }),
    );
  });

  it('keeps a configured column in place when its display name changes', () => {
    const alpha: ExplorerBuilderCandidate = {
      candidateId: 'c_alpha',
      nodeId: 'research-subject',
      fieldPath: 'alpha',
      label: 'Alpha',
      logicalType: 'string',
      filterable: true,
      chartable: true,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
    };
    const beta: ExplorerBuilderCandidate = {
      ...alpha,
      candidateId: 'c_beta',
      fieldPath: 'beta',
      label: 'Beta',
    };
    const initialTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [{
          column: 'alpha',
          label: 'Alpha',
          occurrenceId: 'base',
          source: { kind: 'field', field: { path: 'alpha', projectionMode: 'FIRST' } },
          table: { visible: true, order: 0 },
        }],
      },
    };
    const Harness = () => {
      const [currentTable, setCurrentTable] = React.useState(initialTable);
      return (
        <ColumnSelector
          catalog={{ ...catalog, candidates: [alpha, beta] }}
          table={currentTable}
          occurrenceId="base"
          disabled={false}
          onAdd={vi.fn()}
          onAddAll={vi.fn()}
          onSourceChange={vi.fn()}
          onChange={(next) => setCurrentTable((current) => ({
            ...current,
            document: {
              ...current.document,
              columns: current.document.columns.map((column) => column.column === next.column ? next : column),
            },
          }))}
          onRemove={vi.fn()}
        />
      );
    };
    render(<Harness />);

    const alphaInput = screen.getByRole('textbox', { name: 'Display name for configured Alpha' });
    expect(screen.getAllByRole('textbox', { name: /Display name for/ }).map((input) => (input as HTMLInputElement).value)).toEqual(['Alpha', 'Beta']);
    fireEvent.change(alphaInput, { target: { value: 'Zulu' } });
    fireEvent.blur(alphaInput);

    expect(screen.getAllByRole('textbox', { name: /Display name for/ }).map((input) => (input as HTMLInputElement).value)).toEqual(['Zulu', 'Beta']);
  });

  it('shows primitive leaf fields, hides object containers, and adds from the table checkbox', () => {
    const candidate: ExplorerBuilderCandidate = {
      candidateId: 'c_birth_date',
      nodeId: 'research-subject',
      fieldPath: 'birthDate',
      label: 'Birth date',
      logicalType: 'date',
      filterable: true,
      chartable: true,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
    };
    const onAdd = vi.fn();
    const onAddAll = vi.fn();
    render(
      <ColumnSelector
        catalog={{
          ...catalog,
          candidates: [
            candidate,
            {
              ...candidate,
              candidateId: 'c_identifier_container',
              fieldPath: 'identifier[]',
              label: 'identifier',
              logicalType: 'unknown',
              repeated: true,
            },
          ],
        }}
        table={table}
        occurrenceId="base"
        disabled={false}
        onAdd={onAdd}
        onAddAll={onAddAll}
        onChange={vi.fn()}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(screen.getByText(/1 configured · 1 available/)).toBeInTheDocument();
    expect(screen.queryByDisplayValue('identifier')).not.toBeInTheDocument();
    const displayName = screen.getByRole('textbox', {
      name: 'Display name for available Birth date',
    });
    fireEvent.change(displayName, { target: { value: 'Date of birth' } });
    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Add Date of birth to table' }),
    );
    expect(onAdd).toHaveBeenCalledWith(candidate, 'Date of birth', 'TABLE');

    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Add Date of birth as filter' }),
    );
    expect(onAdd).toHaveBeenCalledWith(candidate, 'Date of birth', 'FILTER');

    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Add Date of birth as chart' }),
    );
    expect(onAdd).toHaveBeenCalledWith(candidate, 'Date of birth', 'CHART');

    fireEvent.click(
      screen.getByRole('button', { name: 'Select all table columns' }),
    );
    expect(onAddAll).toHaveBeenCalledWith([candidate]);
  });

  it('disables only presentations the catalog reports as unsupported', () => {
    const candidate: ExplorerBuilderCandidate = {
      candidateId: 'c_status',
      nodeId: 'research-subject',
      fieldPath: 'status',
      label: 'Status',
      logicalType: 'string',
      filterable: true,
      chartable: false,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
    };
    render(
      <ColumnSelector
        catalog={{
          ...catalog,
          candidates: [
            candidate,
            {
              ...candidate,
              candidateId: 'c_identifier',
              fieldPath: 'identifier[].value',
              label: 'Research Subject ID',
              filterable: false,
            },
          ],
        }}
        table={table}
        occurrenceId="base"
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={vi.fn()}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(
      screen.getByRole('checkbox', { name: 'Add Status as filter' }),
    ).toBeEnabled();
    expect(
      screen.getByRole('checkbox', { name: 'Add Status as chart' }),
    ).toBeDisabled();
    expect(
      screen.getByRole('checkbox', {
        name: 'Use Research Subject ID as filter',
      }),
    ).toBeDisabled();
    expect(
      screen.getByRole('checkbox', {
        name: 'Use Research Subject ID as chart',
      }),
    ).toBeDisabled();
  });

  it('toggles all configured table columns off without removing their configuration', () => {
    const onChange = vi.fn();
    render(
      <ColumnSelector
        catalog={catalog}
        table={table}
        occurrenceId="base"
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={onChange}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    const toggle = screen.getByRole('button', {
      name: 'Deselect all table columns',
    });
    expect(toggle).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(toggle);

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        column: 'research_subject_identifier',
        table: expect.objectContaining({ visible: false }),
      }),
    );
  });

  it('converts a chosen candidate to one durable typed V2 column', () => {
    const column = columnFromCandidate(
      {
        candidateId: 'c_birth_date',
        nodeId: 'research-subject',
        fieldPath: 'birthDate',
        label: 'Birth date',
        logicalType: 'date',
        filterable: true,
        chartable: true,
        projectionModes: ['FIRST'],
        defaultProjectionMode: 'FIRST',
      },
      'patient-step',
      table.document.columns,
      'Date of birth',
    );

    expect(column).toMatchObject({
      column: 'patient_step__birth_date',
      label: 'Date of birth',
      occurrenceId: 'patient-step',
      source: {
        kind: 'field',
        field: {
          path: 'birthDate',
          projectionMode: 'FIRST',
        },
      },
      table: { visible: true, order: 1 },
    });
  });

  it('derives readable base column keys from resource type and FHIR leaf path', () => {
    const column = columnFromCandidate(
      {
        candidateId: 'c_5d11d8d24ce3124cea823c9e',
        nodeId: 'research-subject',
        fieldPath: 'identifier[].value',
        label: 'Research Subject ID',
        logicalType: 'string',
        repeated: true,
        filterable: true,
        chartable: false,
        projectionModes: ['FIRST', 'ALL'],
        defaultProjectionMode: 'FIRST',
      },
      'base',
      [],
      'Research Subject ID',
      'ResearchSubject',
    );

    expect(column.column).toBe('research_subject_identifier');
    expect(column.source).toMatchObject({ kind: 'field', field: { path: 'identifier[].value' } });
  });
});
