// @vitest-environment jsdom
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type {
  AggregateTransformationCapability,
  ExplorerBuilderCandidate,
  ExplorerBuilderCatalog,
  ExplorerBuilderColumn,
} from '../../../types';
import type { DraftTable } from '../authoring/model';
import type { ConfiguredColumnContextResponse } from '../../../interpretation';
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
    rows: { kind: 'RECORDS', records: {} },
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

const unavailableTransformations: AggregateTransformationCapability = {
  temporalReduction: {
    available: false,
    reasonCode: 'NO_TIMESTAMP_FIELDS',
    reason: 'No advertised temporal choices are available for this candidate.',
    timestampFields: [],
    anchorFields: [],
  },
  unitNormalization: {
    available: false,
    reasonCode: 'NO_COMPATIBLE_UNIT_PRESET',
    reason: 'No approved unit preset is available for this candidate.',
    presets: [],
  },
};

const contextFor = (
  outputId: string,
  column: string,
  candidateIds: ReadonlyArray<string>,
): ConfiguredColumnContextResponse => ({
  snapshotToken: 'snapshot',
  draftVersion: 1,
  draftDigest: 'draft-digest',
  libraries: [],
  pinnedRevisions: [],
  columns: [{
    outputId,
    column,
    occurrenceId: 'base',
    resolution: { state: 'READY', capabilityCandidateIds: [...candidateIds], applicableRevisionIds: [] },
  }],
});

describe('configured V2 columns', () => {
  it('shows the exact FHIR source and saved route, then hands the column to the graph', async () => {
    const onEditInGraph = vi.fn();
    const height = {
      column: 'height',
      label: 'Height',
      logicalType: 'decimal',
      occurrenceId: 'observations',
      source: {
        kind: 'codedValue' as const,
        lookup: {
          binding: {
            ownerPath: 'component[]',
            keyPath: 'code',
            systemPath: 'code.coding[].system',
            codePath: 'code.coding[].code',
            valuePath: 'valueQuantity.value',
            logicalType: 'decimal',
            unitPath: 'valueQuantity.unit',
          },
          key: { system: 'http://loinc.org', code: '8302-2' },
          projectionMode: 'FIRST' as const,
        },
      },
      table: { visible: true, order: 0 },
    };
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
        columns: [height],
      },
    };

    render(
      <ColumnSelector
        catalog={relatedCatalog}
        table={relatedTable}
        occurrenceId="base"
        showAvailable={false}
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={vi.fn()}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
        onInspectSource={vi.fn().mockResolvedValue({
          snapshotToken: 'snapshot',
          outputId: relatedTable.outputId,
          column: height.column,
          summary: 'LOINC height from Observation component',
          route: [
            { occurrenceId: 'base', resourceType: 'ResearchSubject' },
            {
              occurrenceId: 'observations',
              resourceType: 'Observation',
              relationship: 'subject_Observation',
              storageDirection: 'INBOUND',
              matchMode: 'OPTIONAL',
            },
          ],
          facts: [
            { label: 'FHIR owner', value: 'component[]' },
            { label: 'Code', value: 'http://loinc.org · 8302-2' },
            { label: 'Value member', value: 'valueQuantity.value' },
            { label: 'Unit member', value: 'valueQuantity.unit' },
          ],
        })}
        onEditInGraph={onEditInGraph}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Column details' }));

    const inspector = await screen.findByRole('region', { name: 'Source for Height' });
    expect(inspector).toHaveTextContent('ResearchSubject');
    expect(inspector).toHaveTextContent('Observation');
    expect(inspector).toHaveTextContent('via subject_Observation');
    expect(inspector).toHaveTextContent('http://loinc.org · 8302-2');
    expect(inspector).toHaveTextContent('component[]');
    expect(inspector).toHaveTextContent('valueQuantity.value');
    expect(inspector).toHaveTextContent('valueQuantity.unit');

    fireEvent.click(screen.getByRole('button', { name: 'Edit in graph' }));
    expect(onEditInGraph).toHaveBeenCalledWith(height);
  });

  it('narrows and highlights the exact feature handed off for repair', async () => {
    render(<ColumnSelector catalog={catalog} table={table} occurrenceId="base"
      focusColumn="research_subject_identifier" disabled={false} onAdd={vi.fn()}
      onAddAll={vi.fn()} onChange={vi.fn()} onSourceChange={vi.fn()} onRemove={vi.fn()} />);

    await waitFor(() => expect((screen.getByRole('textbox', { name: 'Search columns' }) as HTMLInputElement).value).toBe('research_subject_identifier'));
    expect(document.querySelector('[data-feature-focus="true"]')).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'Display name for configured Research Subject ID' })).toBeInTheDocument();
  });

  it('saves exact category mappings through FeaturePolicyEditor without changing column identity or source', () => {
    const column: ExplorerBuilderColumn = {
      column: 'status',
      label: 'Status',
      occurrenceId: 'base',
      source: { kind: 'field', field: { path: 'status', projectionMode: 'FIRST' } },
      table: { visible: true, order: 0 },
    };
    const transformTable: DraftTable = {
      ...table,
      document: { ...table.document, columns: [column] },
    };
    const candidate: ExplorerBuilderCandidate = {
      candidateId: 'c_status',
      nodeId: 'research-subject',
      fieldPath: 'status',
      label: 'Status',
      logicalType: 'string',
      cardinality: 'optional_one',
      filterable: true,
      chartable: false,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      aggregateOperations: [],
      transformations: unavailableTransformations,
      valueTransformations: {
        exactCategoryRecode: { available: true },
        codedValueRecoding: {
          available: false,
          reasonCode: 'CODED_VALUE_RECODE_UNAVAILABLE',
          reason: 'Both Coding.system and Coding.code must be preserved.',
        },
      },
    };
    const onTransformationChange = vi.fn();
    const onSourceChange = vi.fn();

    render(<ColumnSelector
      catalog={{ ...catalog, candidates: [candidate] }}
      interpretationContext={contextFor(transformTable.outputId, column.column, [candidate.candidateId])}
      table={transformTable}
      occurrenceId="base"
      disabled={false}
      onAdd={vi.fn()}
      onAddAll={vi.fn()}
      onChange={vi.fn()}
      onSourceChange={onSourceChange}
      onTransformationChange={onTransformationChange}
      onRemove={vi.fn()}
    />);

    fireEvent.click(screen.getByText('Recode exact category values'));
    fireEvent.click(screen.getByRole('button', { name: 'Add mapping' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Recorded category 1 for Status' }), {
      target: { value: 'recorded-A' },
    });
    fireEvent.change(screen.getByRole('textbox', { name: 'Replacement value 1 for Status' }), {
      target: { value: 'group-1' },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'Unmapped value policy for Status' }), {
      target: { value: 'KEEP_ORIGINAL' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save recoding' }));

    expect(onTransformationChange).toHaveBeenCalledWith('status', {
      kind: 'SET',
      transformation: {
        kind: 'EXACT_CATEGORY_RECODE',
        exactCategoryRecode: {
          mappings: [{ from: 'recorded-A', to: 'group-1' }],
          unknownPolicy: 'KEEP_ORIGINAL',
        },
      },
    });
    expect(onSourceChange).not.toHaveBeenCalled();
    expect(transformTable.document.columns[0]).toMatchObject({
      column: 'status',
      source: { kind: 'field', field: { path: 'status', projectionMode: 'FIRST' } },
    });
  });

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
        cardinality: 'many',
        repeated: true,
        filterable: true,
        chartable: false,
        projectionModes: ['FIRST', 'ALL', 'DISTINCT'],
        defaultProjectionMode: 'FIRST',
        aggregateOperations: [],
        transformations: unavailableTransformations,
      }],
    };

    render(<ColumnSelector catalog={repeatedCatalog} interpretationContext={contextFor(table.outputId, 'research_subject_identifier', ['c_identifier'])} table={table} occurrenceId="base"
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

  it('uses only the server candidate ID when a source path has multiple candidates', () => {
    const idCandidate: ExplorerBuilderCandidate = {
      candidateId: 'opaque-selected',
      nodeId: 'research-subject',
      fieldPath: 'identifier[].value',
      label: 'Selected identity',
      logicalType: 'string',
      cardinality: 'many',
      repeated: true,
      filterable: false,
      chartable: false,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      aggregateOperations: [],
      transformations: unavailableTransformations,
    };
    const samePathDecoy = { ...idCandidate, candidateId: 'opaque-decoy', label: 'Decoy identity', filterable: true };
    render(<ColumnSelector
      catalog={{ ...catalog, candidates: [idCandidate, samePathDecoy] }}
      interpretationContext={contextFor(table.outputId, 'research_subject_identifier', ['opaque-selected'])}
      table={table}
      occurrenceId="base"
      disabled={false}
      onAdd={vi.fn()}
      onAddAll={vi.fn()}
      onChange={vi.fn()}
      onSourceChange={vi.fn()}
      onRemove={vi.fn()}
    />);

    expect(screen.getByRole('checkbox', { name: 'Use Research Subject ID as filter' })).toBeDisabled();
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
        cardinality: 'many',
        repeated: true,
        filterable: true,
        chartable: false,
        projectionModes: ['FIRST', 'ALL'],
        defaultProjectionMode: 'FIRST',
        aggregateOperations: [],
        transformations: unavailableTransformations,
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

    const { rerender } = render(<ColumnSelector catalog={contributorCatalog} table={aggregateTable} occurrenceId="base"
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

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Contributor condition for Subject count',
    }), { target: { value: 'EQUALS' } });
    fireEvent.change(screen.getByRole('textbox', {
      name: 'Contributor value for Subject count',
    }), { target: { value: 'registered' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply condition' }));
    expect(onContributorChange).toHaveBeenLastCalledWith('subject_count', {
      candidateId: 'c_identifier',
      operator: 'EQUALS',
      quantifier: 'ANY',
      value: { kind: 'STRING', string: 'registered' },
    });

    rerender(<ColumnSelector catalog={contributorCatalog} table={{
      ...aggregateTable,
      document: {
        ...aggregateTable.document,
        columns: [{
          ...aggregateTable.document.columns[0],
          contributor: {
            candidateId: 'c_identifier', operator: 'EQUALS', quantifier: 'ANY',
            value: { kind: 'STRING', string: 'registered' },
          },
        }],
      },
    }} occurrenceId="base" disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()}
      onChange={vi.fn()} onSourceChange={onSourceChange}
      onContributorChange={onContributorChange} onRemove={vi.fn()} />);
    expect(screen.getByText('Only Research Subject records where Identifier equals “registered” contribute to this feature.')).toBeInTheDocument();
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
        cardinality: 'optional_one',
        repeated: false,
        filterable: true,
        chartable: true,
        projectionModes: ['VALUE'],
        defaultProjectionMode: 'VALUE',
        aggregateOperations: [
          {
            operation: 'COUNT',
            rowContext: 'RECORDS',
            supported: true,
            resultLogicalType: 'integer',
            resultCardinality: 'ONE',
            missingValueSemantics: 'missing values are excluded; an empty set returns zero',
            contributorSemantics: 'each non-null value contributes once',
          },
          {
            operation: 'MAX',
            rowContext: 'RECORDS',
            supported: true,
            resultLogicalType: 'decimal',
            resultCardinality: 'OPTIONAL_ONE',
            missingValueSemantics: 'missing values are excluded',
            contributorSemantics: 'all non-null values are considered',
          },
        ],
        transformations: unavailableTransformations,
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

    const { rerender } = render(<ColumnSelector catalog={relatedCatalog} interpretationContext={contextFor(relatedTable.outputId, 'observation_value', ['c_value'])} table={relatedTable}
      occurrenceId="observations" disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()}
      onChange={vi.fn()} onSourceChange={onSourceChange} onRemove={vi.fn()} />);

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Across related Observation records for Observation value',
    }), { target: { value: 'MAX' } });
    expect(onSourceChange).toHaveBeenCalledWith('observation_value', {
      kind: 'aggregate',
      aggregate: { operation: 'MAX', path: 'valueQuantity.value' },
    });

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Across related Observation records for Observation value',
    }), { target: { value: 'COUNT' } });
    expect(onSourceChange).toHaveBeenLastCalledWith('observation_value', {
      kind: 'aggregate',
      aggregate: { operation: 'COUNT', path: 'valueQuantity.value' },
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
    rerender(<ColumnSelector catalog={relatedCatalog} interpretationContext={contextFor(aggregateTable.outputId, 'observation_value', ['c_value'])} table={aggregateTable}
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

  it('applies a complete date-aware related value policy in one source edit', () => {
    const onSourceChange = vi.fn();
    const temporalCatalog: ExplorerBuilderCatalog = {
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
      candidates: [
        {
          candidateId: 'c_anchor',
          nodeId: 'research-subject',
          fieldPath: 'meta.lastUpdated',
          label: 'Row updated at',
          logicalType: 'date_time',
          cardinality: 'optional_one',
          repeated: false,
          filterable: true,
          chartable: false,
          projectionModes: ['VALUE'],
          defaultProjectionMode: 'VALUE',
          aggregateOperations: [],
          transformations: unavailableTransformations,
        },
        {
          candidateId: 'c_value',
          nodeId: 'observation',
          fieldPath: 'valueQuantity.value',
          label: 'Measured value',
          logicalType: 'decimal',
          cardinality: 'optional_one',
          repeated: false,
          filterable: true,
          chartable: true,
          projectionModes: ['VALUE'],
          defaultProjectionMode: 'VALUE',
          aggregateOperations: [{
            operation: 'FIRST_ORDERED',
            rowContext: 'RECORDS',
            supported: true,
            resultLogicalType: 'decimal',
            resultCardinality: 'OPTIONAL_ONE',
            missingValueSemantics: 'missing values are excluded; no eligible input returns null',
            contributorSemantics: 'the value attached to the selected timestamp/resource contributes',
            requiresConfiguration: ['temporal'],
          }],
          transformations: {
            ...unavailableTransformations,
            temporalReduction: {
              available: true,
              timestampFields: [{
                candidateId: 'c_timestamp',
                nodeId: 'observation',
                resourceType: 'Observation',
                fieldPath: 'effectiveDateTime',
                label: 'Observed at',
              }],
              anchorFields: [{
                candidateId: 'c_anchor',
                nodeId: 'research-subject',
                resourceType: 'ResearchSubject',
                fieldPath: 'meta.lastUpdated',
                label: 'Row updated at',
              }],
            },
          },
        },
        {
          candidateId: 'c_timestamp',
          nodeId: 'observation',
          fieldPath: 'effectiveDateTime',
          label: 'Observed at',
          logicalType: 'date_time',
          cardinality: 'optional_one',
          repeated: false,
          filterable: true,
          chartable: false,
          projectionModes: ['VALUE'],
          defaultProjectionMode: 'VALUE',
          aggregateOperations: [],
          transformations: unavailableTransformations,
        },
        {
          candidateId: 'c_unadvertised_anchor',
          nodeId: 'research-subject',
          fieldPath: 'birthDate',
          label: 'Unadvertised row date',
          logicalType: 'date_time',
          cardinality: 'optional_one',
          repeated: false,
          filterable: true,
          chartable: false,
          projectionModes: ['VALUE'],
          defaultProjectionMode: 'VALUE',
          aggregateOperations: [],
          transformations: unavailableTransformations,
        },
        {
          candidateId: 'c_unadvertised_timestamp',
          nodeId: 'observation',
          fieldPath: 'issued',
          label: 'Unadvertised observation date',
          logicalType: 'date_time',
          cardinality: 'optional_one',
          repeated: false,
          filterable: true,
          chartable: false,
          projectionModes: ['VALUE'],
          defaultProjectionMode: 'VALUE',
          aggregateOperations: [],
          transformations: unavailableTransformations,
        },
      ],
    };
    const temporalTable: DraftTable = {
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

    render(<ColumnSelector catalog={temporalCatalog}
      interpretationContext={contextFor(temporalTable.outputId, 'observation_value', ['c_value'])}
      table={temporalTable}
      occurrenceId="observations" disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()}
      onChange={vi.fn()} onSourceChange={onSourceChange} onRemove={vi.fn()} />);

    fireEvent.change(screen.getByRole('combobox', {
      name: 'Across related Observation records for Observation value',
    }), { target: { value: 'FIRST_ORDERED' } });
    expect(onSourceChange).not.toHaveBeenCalled();
    expect(screen.getByRole('combobox', { name: 'Record date' })).toHaveProperty('value', 'effectiveDateTime');
    expect(screen.getByRole('combobox', { name: 'Compare with row date' })).toHaveProperty('value', 'meta.lastUpdated');
    expect(screen.queryByRole('option', { name: 'Unadvertised observation date · Observation · issued' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Unadvertised row date · ResearchSubject · birthDate' })).not.toBeInTheDocument();

    fireEvent.change(screen.getByRole('spinbutton', { name: 'Look back days' }), {
      target: { value: '90' },
    });
    fireEvent.change(screen.getByRole('combobox', { name: 'Equal date handling' }), {
      target: { value: 'RESOURCE_KEY' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Apply date selection' }));

    expect(onSourceChange).toHaveBeenCalledOnce();
    expect(onSourceChange).toHaveBeenCalledWith('observation_value', {
      kind: 'aggregate',
      aggregate: {
        operation: 'FIRST_ORDERED',
        path: 'valueQuantity.value',
        temporal: {
          timestampPath: 'effectiveDateTime',
          anchorPath: 'meta.lastUpdated',
          lowerOffsetSeconds: -7_776_000,
          upperOffsetSeconds: 0,
          lowerInclusive: true,
          upperInclusive: true,
          direction: 'DESC',
          precision: 'INSTANT',
          tiePolicy: 'RESOURCE_KEY',
        },
      },
    });
  });

  it('applies an approved measurement-unit preset without exposing conversion coefficients', () => {
    const onSourceChange = vi.fn();
    const measurementCatalog: ExplorerBuilderCatalog = {
      ...catalog,
      candidates: [{
        candidateId: 'c_height',
        nodeId: 'research-subject',
        fieldPath: 'valueQuantity.value',
        label: 'Height',
        logicalType: 'decimal',
        cardinality: 'optional_one',
        repeated: false,
        filterable: true,
        chartable: true,
        projectionModes: ['VALUE'],
        defaultProjectionMode: 'VALUE',
        aggregateOperations: [],
        transformations: {
          ...unavailableTransformations,
          unitNormalization: {
            available: true,
            presets: [{
              policyId: 'to-centimeters',
              version: '7',
              target: { system: 'http://unitsofmeasure.org', code: 'cm' },
              available: true,
            }, {
              policyId: 'to-kilograms',
              version: '3',
              target: { system: 'http://unitsofmeasure.org', code: 'kg' },
              available: false,
              reasonCode: 'UNIT_PRESET_INCOMPATIBLE',
              reason: 'The preset does not cover every observed source unit.',
            }],
          },
        },
      }],
    };
    const measurementTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [{
          column: 'height',
          label: 'Height',
          occurrenceId: 'base',
          source: { kind: 'aggregate', aggregate: { operation: 'MAX', path: 'valueQuantity.value' } },
          table: { visible: true, order: 0 },
        }],
      },
    };

    render(<ColumnSelector catalog={measurementCatalog} interpretationContext={contextFor(measurementTable.outputId, 'height', ['c_height'])} table={measurementTable}
      occurrenceId="base" disabled={false} onAdd={vi.fn()} onAddAll={vi.fn()}
      onChange={vi.fn()} onSourceChange={onSourceChange} onRemove={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Normalize units' }));
    const presetSelect = screen.getByRole('combobox', { name: 'Unit conversion preset' });
    expect(screen.getByRole('option', {
      name: /to-kilograms v3 — unavailable: The preset does not cover every observed source unit\./,
    })).toBeDisabled();
    fireEvent.change(presetSelect, {
      target: { value: JSON.stringify(['to-centimeters', '7']) },
    });
    expect(screen.queryByText(/scale|offset|system path|code path/i)).not.toBeInTheDocument();
    expect(onSourceChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply normalization' }));

    expect(onSourceChange).toHaveBeenCalledWith('height', {
      kind: 'aggregate',
      aggregate: {
        operation: 'MAX',
        path: 'valueQuantity.value',
        unitNormalization: { policyId: 'to-centimeters', version: '7' },
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
      cardinality: 'required_one',
      filterable: true,
      chartable: true,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      aggregateOperations: [],
      transformations: unavailableTransformations,
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
          interpretationContext={contextFor(initialTable.outputId, 'alpha', ['c_alpha'])}
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

  it('moves a visible configured column to the end with one persisted update', () => {
    const alpha = table.document.columns[0];
    const beta: ExplorerBuilderColumn = {
      ...alpha,
      column: 'research_subject_birth_date',
      label: 'Birth date',
      source: { kind: 'field' as const, field: { path: 'birthDate', projectionMode: 'VALUE' } },
      table: { visible: true, order: 1 },
    };
    const orderedTable: DraftTable = {
      ...table,
      document: {
        ...table.document,
        columns: [{ ...alpha, table: { visible: true, order: 0 } }, beta],
      },
    };
    const onChange = vi.fn();
    render(
      <ColumnSelector
        catalog={catalog}
        table={orderedTable}
        occurrenceId="base"
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={onChange}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Move Research Subject ID to end' }));

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      column: 'research_subject_identifier',
      table: { visible: true, order: 2 },
    }));
    expect(screen.getByRole('button', { name: 'Move Birth date to end' })).toBeDisabled();
  });

  it('normalizes missing orders before moving a configured column to the end', () => {
    const alpha = { ...table.document.columns[0], table: { visible: true } };
    const beta: ExplorerBuilderColumn = {
      ...alpha,
      column: 'research_subject_birth_date',
      label: 'Birth date',
      occurrenceId: 'observations',
      source: { kind: 'field' as const, field: { path: 'birthDate', projectionMode: 'VALUE' } },
    };
    const onColumnsChange = vi.fn();
    render(
      <ColumnSelector
        catalog={catalog}
        table={{ ...table, document: { ...table.document, columns: [alpha, beta] } }}
        occurrenceId="base"
        disabled={false}
        onAdd={vi.fn()}
        onAddAll={vi.fn()}
        onChange={vi.fn()}
        onColumnsChange={onColumnsChange}
        onSourceChange={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Move Research Subject ID to end' }));

    expect(onColumnsChange).toHaveBeenCalledWith([
      expect.objectContaining({ column: 'research_subject_birth_date', table: { visible: true, order: 0 } }),
      expect.objectContaining({ column: 'research_subject_identifier', table: { visible: true, order: 1 } }),
    ]);
  });

  it('shows primitive leaf fields, hides object containers, and adds from the table checkbox', () => {
    const candidate: ExplorerBuilderCandidate = {
      candidateId: 'c_birth_date',
      nodeId: 'research-subject',
      fieldPath: 'birthDate',
      label: 'Birth date',
      logicalType: 'date',
      cardinality: 'optional_one',
      filterable: true,
      chartable: true,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      aggregateOperations: [],
      transformations: unavailableTransformations,
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
        interpretationContext={contextFor(table.outputId, 'research_subject_identifier', ['c_identifier'])}
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
      cardinality: 'required_one',
      filterable: true,
      chartable: false,
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      aggregateOperations: [],
      transformations: unavailableTransformations,
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
        interpretationContext={contextFor(table.outputId, 'research_subject_identifier', ['c_identifier'])}
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
        cardinality: 'optional_one',
        filterable: true,
        chartable: true,
        projectionModes: ['FIRST'],
        defaultProjectionMode: 'FIRST',
        aggregateOperations: [],
        transformations: unavailableTransformations,
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
        cardinality: 'many',
        repeated: true,
        filterable: true,
        chartable: false,
        projectionModes: ['FIRST', 'ALL'],
        defaultProjectionMode: 'FIRST',
        aggregateOperations: [],
        transformations: unavailableTransformations,
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
