// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useReducer } from 'react';
import { useAutomaticPreview, type AutomaticPreviewIdentity } from '../useAutomaticPreview';
import type { ExplorerBuilderCompileResult, ExplorerBuilderState } from '../../../types';
import {
  type BuilderAuthoringState,
  derivedOccurrences,
  nextRouteOccurrenceId,
  stateFromBuilder,
  workspaceFromState,
} from './model';
import { builderAuthoringReducer } from './reducer';

const ready: ExplorerBuilderState = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2',
  kind: 'ExplorerBuilderState',
  lifecycleState: 'READY',
  draftVersion: 1,
  draftDigest: 'sha256:draft',
  workspace: {
    apiVersion: 'loom.calypr.org/explorer-authoring/v2',
    kind: 'ExplorerBuilderWorkspace',
    explorer: { title: 'BForePC' },
    documents: [
      {
        kind: 'ExplorerBuilderDocument',
        output: { id: 'Specimen', title: 'Specimen' },
        rootResourceType: 'Specimen',
        route: { occurrenceId: 'base', resourceType: 'Specimen' },
        rows: { kind: 'RECORDS', records: {} },
        columns: [
          {
            column: 'specimen_identifier',
            label: 'HTAN_BIOSPECIMEN_ID',
            occurrenceId: 'base',
            source: {
              kind: 'identifierBySystem',
              lookup: {
                match: 'https://aced-idp.org/project',
                projectionMode: 'FIRST',
              },
            },
            table: { visible: true, order: 0 },
          },
        ],
      },
    ],
    tabs: [
      {
        id: 'specimen',
        title: 'Specimen',
        outputId: 'Specimen',
        order: 0,
        visible: true,
      },
    ],
  },
  catalog: {
    snapshotToken: 'snapshot',
    generation: 'generation',
    routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
    nodes: [
      {
        nodeId: 'specimen-node',
        resourceType: 'Specimen',
        rowRootEligible: true,
        populated: true,
        documentCount: 1,
      },
    ],
    edges: [],
    candidates: [],
  },
};

describe('semantic Builder hydration', () => {
  it('round trips imported recipe references and source columns unchanged', () => {
    const workspace = {
      ...ready.workspace!,
      documents: ready.workspace!.documents.map((document) => ({
        ...document,
        importedRecipe: {
          name: 'shared-default',
          translationVersion: '1',
          digest: 'a'.repeat(64),
          output: 'Specimen',
        },
        columns: document.columns.map((column) => ({
          ...column,
          source: { kind: 'imported' as const, imported: { outputColumn: column.column } },
        })),
      })),
    };
    const state = stateFromBuilder({ ...ready, workspace }, {
      project: 'project',
      explorerId: 'dataframe-default',
    });
    expect(workspaceFromState(state)).toEqual(workspace);
  });

  it('reopens and recompiles the canonical workspace without reconstructing sources', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    expect(state.tables).toHaveLength(1);
    expect(workspaceFromState(state)).toEqual(ready.workspace);
  });

  it('does not serialize stale shared-filter bindings from preserved hot-reload state', () => {
    const state = stateFromBuilder(
      {
        ...ready,
        workspace: {
          ...ready.workspace!,
          sharedFilters: {
            document_reference_patient_identifier: [
              { outputId: 'Specimen', column: 'unknown_old_column' },
            ],
          },
        },
      },
      { project: 'project', explorerId: 'default' },
    );

    expect(workspaceFromState(state).sharedFilters).toBeUndefined();
  });

  it('serializes presentation edits from the V2 document into the next compile workspace', () => {
    const preview = {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2' as const,
      kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: 'receipt_preview',
      outputId: 'Specimen',
      columns: [],
      rows: [{ specimen_identifier: 'sample-1' }],
      rowCount: 1,
      diagnostics: [],
    };
    const state = {
      ...stateFromBuilder(ready, {
        project: 'project',
        explorerId: 'default',
      }),
      preview,
    };
    const original = state.tables[0].document.columns[0];
    const edited = builderAuthoringReducer(state, {
      type: 'updateColumn',
      outputId: 'Specimen',
      column: original.column,
      value: {
        ...original,
        label: 'Specimen identifier',
        table: { visible: false, order: 0 },
        filter: { label: 'Specimen identifier' },
        chart: { type: 'bar', title: 'Specimens' },
      },
    });

    expect(edited.dirty).toBe(true);
    expect(edited.reconciliation).toBe('idle');
    expect(edited.preview).toBe(preview);
    expect(workspaceFromState(edited).documents[0].columns[0]).toMatchObject({
      column: 'specimen_identifier',
      label: 'Specimen identifier',
      table: { visible: false, order: 0 },
      filter: { label: 'Specimen identifier' },
      chart: { type: 'bar', title: 'Specimens' },
      source: original.source,
    });
  });

  it('treats selecting the current table as a no-op and clears preview when switching tables', () => {
    const preview = {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2' as const,
      kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: 'receipt_preview',
      outputId: 'Specimen',
      columns: [],
      rows: [{ specimen_identifier: 'sample-1' }],
      rowCount: 1,
      diagnostics: [],
    };
    const state = {
      ...stateFromBuilder(ready, {
        project: 'project',
        explorerId: 'default',
      }),
      preview,
    };

    const next = builderAuthoringReducer(state, {
      type: 'selectTable',
      outputId: 'Specimen',
    });

    expect(next).toBe(state);

    const secondTable = {
      ...state.tables[0],
      outputId: 'Observation',
      tabId: 'observation',
      title: 'Observation',
      document: {
        ...state.tables[0].document,
        output: { id: 'Observation', title: 'Observation' },
      },
    };
    const switched = builderAuthoringReducer(
      { ...state, tables: [...state.tables, secondTable] },
      { type: 'selectTable', outputId: 'Observation' },
    );

    expect(switched.selectedOutputId).toBe('Observation');
    expect(switched.preview).toBeUndefined();
  });

  it('removes configured columns without candidate identity reconciliation', () => {
    const state = stateFromBuilder(
      {
        ...ready,
        workspace: {
          ...ready.workspace!,
          sharedFilters: {
            document_reference_patient_identifier: [
              { outputId: 'Specimen', column: 'specimen_identifier' },
            ],
          },
        },
      },
      {
        project: 'project',
        explorerId: 'default',
      },
    );
    const edited = builderAuthoringReducer(state, {
      type: 'removeColumn',
      outputId: 'Specimen',
      column: 'specimen_identifier',
    });

    expect(workspaceFromState(edited).documents[0].columns).toEqual([]);
    expect(workspaceFromState(edited).sharedFilters).toBeUndefined();
  });

  it('atomically removes old shared-filter bindings when starting a new query', () => {
    const state = stateFromBuilder(
      {
        ...ready,
        workspace: {
          ...ready.workspace!,
          sharedFilters: {
            document_reference_patient_identifier: [
              { outputId: 'Specimen', column: 'specimen_identifier' },
            ],
          },
        },
        catalog: {
          ...ready.catalog,
          nodes: [
            ...ready.catalog.nodes,
            {
              nodeId: 'patient-node',
              resourceType: 'Patient',
              rowRootEligible: true,
              populated: true,
              documentCount: 1,
            },
          ],
        },
      },
      {
        project: 'project',
        explorerId: 'default',
      },
    );
    const edited = builderAuthoringReducer(state, {
      type: 'changeRoot',
      outputId: 'Specimen',
      nodeId: 'patient-node',
    });

    expect(workspaceFromState(edited).documents[0].columns).toEqual([]);
    expect(workspaceFromState(edited).documents[0].route).toEqual({
      occurrenceId: 'base',
      resourceType: 'Patient',
    });
    expect(workspaceFromState(edited).sharedFilters).toBeUndefined();
  });

  it('adds a complete typed column without introducing candidate selection state', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const edited = builderAuthoringReducer(state, {
      type: 'addColumn',
      outputId: 'Specimen',
      value: {
        column: 'birth_date',
        label: 'Birth date',
        logicalType: 'date',
        occurrenceId: 'base',
        source: {
          kind: 'field',
          field: {
            path: 'birthDate',
            projectionMode: 'FIRST',
          },
        },
        table: { visible: true, order: 1 },
      },
    });

    expect(workspaceFromState(edited).documents[0].columns[1]).toMatchObject({
      column: 'birth_date',
      source: { kind: 'field', field: { path: 'birthDate' } },
    });
  });

  it('does not serialize an invalid empty output title during reactive editing', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const editing = builderAuthoringReducer(state, {
      type: 'renameTable',
      outputId: 'Specimen',
      title: '',
    });

    expect(workspaceFromState(editing).documents[0].output.title).toBe(
      'Specimen',
    );
    expect(workspaceFromState(editing).tabs[0].title).toBe('Specimen');
  });

  it('rejects READY state without authoring intent', () => {
    expect(() =>
      stateFromBuilder(
        { ...ready, workspace: null },
        { project: 'project', explorerId: 'default' },
      ),
    ).toThrow(/READY/);
  });

  it('keeps a refreshed catalog idle until compilation is requested', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const refreshed = builderAuthoringReducer(state, {
      type: 'catalogRefreshed',
      catalog: { ...ready.catalog, snapshotToken: 'new-snapshot' },
    });
    expect(refreshed.reconciliation).toBe('idle');
    expect(refreshed.receipt).toBeUndefined();
    expect(refreshed.tables).toEqual(state.tables);
  });

  it('leaves an incomplete workspace editable after a catalog refresh', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const incomplete = {
      ...state,
      tables: state.tables.map((table) => ({
        ...table,
        document: {
          ...table.document,
          rootResourceType: '',
          route: { occurrenceId: 'base', resourceType: '' },
        },
      })),
    };
    const refreshed = builderAuthoringReducer(incomplete, {
      type: 'catalogRefreshed',
      catalog: { ...ready.catalog, snapshotToken: 'new-snapshot' },
    });
    expect(refreshed.reconciliation).toBe('idle');
  });

  it('keeps a newly added blank table editable until a row root is selected', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const blankTable = {
      outputId: 'blank-output',
      tabId: 'blank-tab',
      title: 'Blank table',
      document: {
        kind: 'ExplorerBuilderDocument' as const,
        output: { id: 'blank-output', title: 'Blank table' },
        rootResourceType: '',
        route: { occurrenceId: 'base', resourceType: '' },
        rows: { kind: 'RECORDS' as const, records: {} },
        columns: [],
      },
    };

    const added = builderAuthoringReducer(state, {
      type: 'addTable',
      table: blankTable,
    });

    expect(added.reconciliation).toBe('idle');
    expect(added.selectedOutputId).toBe('blank-output');

    const rooted = builderAuthoringReducer(added, {
      type: 'setRoot',
      outputId: 'blank-output',
      nodeId: 'specimen-node',
    });
    expect(rooted.tables.at(-1)?.document.rootResourceType).toBe('Specimen');
    expect(rooted.reconciliation).toBe('idle');
  });

  it('hydrates, edits, and removes sibling branches without dropping unrelated routes', () => {
    const branched: ExplorerBuilderState = {
      ...ready,
      workspace: {
        ...ready.workspace!,
        sharedFilters: {
          participant: [
            { outputId: 'Specimen', column: 'patient__identifier' },
          ],
        },
        documents: [
          {
            ...ready.workspace!.documents[0],
            route: {
              occurrenceId: 'base',
              resourceType: 'Specimen',
              children: [
                {
                  occurrenceId: 'observation',
                  resourceType: 'Observation',
                  relationship: 'focus_Specimen',
                },
                {
                  occurrenceId: 'patient',
                  resourceType: 'Patient',
                  relationship: 'subject_Patient',
                },
              ],
            },
            columns: [
              ...ready.workspace!.documents[0].columns,
              {
                column: 'observation__status',
                label: 'Observation status',
                occurrenceId: 'observation',
                source: {
                  kind: 'field',
                  field: {
                    path: 'status',
                    projectionMode: 'FIRST',
                  },
                },
                table: { visible: true, order: 1 },
              },
              {
                column: 'patient__identifier',
                label: 'Participant',
                occurrenceId: 'patient',
                source: {
                  kind: 'field',
                  field: {
                    path: 'identifier[].value',
                    projectionMode: 'FIRST',
                  },
                },
                table: { visible: true, order: 2 },
              },
            ],
          },
        ],
      },
      catalog: {
        ...ready.catalog,
        nodes: [
          ...ready.catalog.nodes,
          {
            nodeId: 'observation-node',
            resourceType: 'Observation',
            rowRootEligible: true,
            populated: true,
            documentCount: 2,
          },
          {
            nodeId: 'patient-node',
            resourceType: 'Patient',
            rowRootEligible: true,
            populated: true,
            documentCount: 2,
          },
          {
            nodeId: 'condition-node',
            resourceType: 'Condition',
            rowRootEligible: true,
            populated: true,
            documentCount: 2,
          },
        ],
        edges: [
          {
            edgeId: 'specimen-observation',
            fromNodeId: 'specimen-node',
            toNodeId: 'observation-node',
            label: 'focus_Specimen',
          },
          {
            edgeId: 'specimen-patient',
            fromNodeId: 'specimen-node',
            toNodeId: 'patient-node',
            label: 'subject_Patient',
          },
          {
            edgeId: 'observation-condition',
            fromNodeId: 'observation-node',
            toNodeId: 'condition-node',
            label: 'focus_Condition',
          },
        ],
      },
    };
    const state = stateFromBuilder(branched, {
      project: 'project',
      explorerId: 'default',
    });
    expect(
      derivedOccurrences(state.tables[0], state.catalog).map(({ id }) => id),
    ).toEqual(['base', 'observation', 'patient']);
    expect(workspaceFromState(state)).toEqual(branched.workspace);

    const occurrenceId = nextRouteOccurrenceId(
      state.tables[0].document.route,
      'observation',
      'Condition',
      'focus_Condition',
    );
    const withNestedBranch = builderAuthoringReducer(state, {
      type: 'addRouteChild',
      outputId: 'Specimen',
      parentOccurrenceId: 'observation',
      edgeId: 'observation-condition',
      occurrenceId,
    });
    expect(occurrenceId).toBe('observation__condition');
    expect(
      derivedOccurrences(
        withNestedBranch.tables[0],
        withNestedBranch.catalog,
      ).map(({ id }) => id),
    ).toEqual(['base', 'observation', 'observation__condition', 'patient']);

    const edited = builderAuthoringReducer(withNestedBranch, {
      type: 'removeRouteSubtree',
      outputId: 'Specimen',
      occurrenceId: 'patient',
    });
    expect(
      derivedOccurrences(edited.tables[0], edited.catalog).map(({ id }) => id),
    ).toEqual(['base', 'observation', 'observation__condition']);
    expect(
      workspaceFromState(edited).documents[0].columns.map(
        (column) => column.column,
      ),
    ).toContain('observation__status');
    expect(
      workspaceFromState(edited).documents[0].columns.map(
        (column) => column.column,
      ),
    ).not.toContain('patient__identifier');
    expect(workspaceFromState(edited).sharedFilters).toBeUndefined();
  });

  it('persists and reconstructs parallel route choices by exact catalog edge', () => {
    const parallel: ExplorerBuilderState = {
      ...ready,
      workspace: {
        ...ready.workspace!,
        documents: [
          {
            ...ready.workspace!.documents[0],
            output: { id: 'Patient', title: 'Patient' },
            rootResourceType: 'Patient',
            route: { occurrenceId: 'base', resourceType: 'Patient' },
            columns: [],
          },
        ],
      },
      catalog: {
        ...ready.catalog,
        nodes: [
          {
            nodeId: 'patient-root',
            resourceType: 'Patient',
            rowRootEligible: true,
            populated: true,
            documentCount: 1,
          },
          {
            nodeId: 'encounter-a',
            resourceType: 'Encounter',
            rowRootEligible: false,
            populated: true,
            documentCount: 1,
          },
          {
            nodeId: 'encounter-b',
            resourceType: 'Encounter',
            rowRootEligible: false,
            populated: true,
            documentCount: 1,
          },
        ],
        edges: [
          {
            edgeId: 'patient-encounter-a',
            fromNodeId: 'patient-root',
            toNodeId: 'encounter-a',
            label: 'encounters',
          },
          {
            edgeId: 'patient-encounter-b',
            fromNodeId: 'patient-root',
            toNodeId: 'encounter-b',
            label: 'encounters',
          },
        ],
      },
    };
    const state = stateFromBuilder(parallel, {
      project: 'project',
      explorerId: 'default',
    });
    const first = builderAuthoringReducer(state, {
      type: 'addRouteChild',
      outputId: 'Patient',
      parentOccurrenceId: 'base',
      edgeId: 'patient-encounter-a',
      occurrenceId: 'encounter-a',
    });
    const both = builderAuthoringReducer(first, {
      type: 'addRouteChild',
      outputId: 'Patient',
      parentOccurrenceId: 'base',
      edgeId: 'patient-encounter-b',
      occurrenceId: 'encounter-b',
    });

    expect(
      derivedOccurrences(both.tables[0], both.catalog).map(
        ({ id, incomingEdgeId }) => [id, incomingEdgeId],
      ),
    ).toEqual([
      ['base', undefined],
      ['encounter-a', 'patient-encounter-a'],
      ['encounter-b', 'patient-encounter-b'],
    ]);
    expect(
      workspaceFromState(both).documents[0].route.children?.map(
        ({ catalogEdgeId }) => catalogEdgeId,
      ),
    ).toEqual(['patient-encounter-a', 'patient-encounter-b']);
  });

  it('does not choose an edge for an ambiguous legacy route tuple', () => {
    const parallel: ExplorerBuilderState = {
      ...ready,
      workspace: {
        ...ready.workspace!,
        documents: [
          {
            ...ready.workspace!.documents[0],
            output: { id: 'Patient', title: 'Patient' },
            rootResourceType: 'Patient',
            route: {
              occurrenceId: 'base',
              resourceType: 'Patient',
              children: [
                {
                  occurrenceId: 'legacy-encounter',
                  resourceType: 'Encounter',
                  relationship: 'encounters',
                },
              ],
            },
            columns: [],
          },
        ],
      },
      catalog: {
        ...ready.catalog,
        nodes: [
          {
            nodeId: 'patient-root',
            resourceType: 'Patient',
            rowRootEligible: true,
            populated: true,
            documentCount: 1,
          },
          {
            nodeId: 'encounter-a',
            resourceType: 'Encounter',
            rowRootEligible: false,
            populated: true,
            documentCount: 1,
          },
          {
            nodeId: 'encounter-b',
            resourceType: 'Encounter',
            rowRootEligible: false,
            populated: true,
            documentCount: 1,
          },
        ],
        edges: [
          {
            edgeId: 'patient-encounter-a',
            fromNodeId: 'patient-root',
            toNodeId: 'encounter-a',
            label: 'encounters',
          },
          {
            edgeId: 'patient-encounter-b',
            fromNodeId: 'patient-root',
            toNodeId: 'encounter-b',
            label: 'encounters',
          },
        ],
      },
    };
    const state = stateFromBuilder(parallel, {
      project: 'project',
      explorerId: 'default',
    });

    expect(derivedOccurrences(state.tables[0], state.catalog).map(({ id }) => id)).toEqual([
      'base',
    ]);
  });

  it('replaces local identities with the authoritative command workspace', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const workspace = {
      ...ready.workspace!,
      documents: ready.workspace!.documents.map((document) => ({
        ...document,
        output: { ...document.output, id: 'out_backend' },
      })),
      tabs: ready.workspace!.tabs.map((tab) => ({
        ...tab,
        id: 'tab_backend',
        outputId: 'out_backend',
      })),
    };
    const next = builderAuthoringReducer(state, {
      type: 'commandsApplied',
      value: {
        commandId: 'command-1',
        workspace,
        draftVersion: 2,
        draftDigest: 'sha256:next',
        results: [
          {
            type: 'TABLE_CREATED',
            outputId: 'out_backend',
            tabId: 'tab_backend',
            occurrenceId: 'base',
          },
        ],
        diagnostics: [],
      },
    });
    expect(next.selectedOutputId).toBe('out_backend');
    expect(next.draftVersion).toBe(2);
    expect(next.workspace).toEqual(workspace);
    expect(next.reconciliation).toBe('idle');
    expect(next.dirty).toBe(false);

    const requested = builderAuthoringReducer(next, {
      type: 'requestRecompile',
    });
    expect(requested.reconciliation).toBe('pending');
    expect(requested.dirty).toBe(false);
  });

  it('adopts a validated Apply receipt at the returned draft and invalidates it on edit', () => {
    const state = stateFromBuilder(ready, {
      project: 'project',
      explorerId: 'default',
    });
    const workspace = ready.workspace;
    if (!workspace) throw new Error('The READY test fixture must include its workspace.');
    const acceptedReceipt: ExplorerBuilderCompileResult = {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderReceipt',
      receiptId: 'proposal-1',
      snapshotToken: ready.catalog.snapshotToken,
      intentDigest: 'sha256:next',
      builder: workspace,
      outputs: [{ outputId: 'Specimen', columns: [] }],
      diagnostics: [],
    };
    const applied = builderAuthoringReducer(state, {
      type: 'commandsApplied',
      value: {
        commandId: 'apply-command',
        workspace,
        draftVersion: 2,
        draftDigest: 'sha256:next',
        results: [{ type: 'TABLE_CHANGED', outputId: 'Specimen' }],
        diagnostics: [],
      },
      acceptedReceipt,
    });

    expect(applied.receipt).toEqual({ ...acceptedReceipt, builder: workspace });
    expect(applied.draftVersion).toBe(2);
    expect(applied.draftDigest).toBe('sha256:next');
    expect(applied.reconciliation).toBe('resolved');

    const edited = builderAuthoringReducer(applied, {
      type: 'renameTable',
      outputId: 'Specimen',
      title: 'Renamed specimen table',
    });
    expect(edited.receipt).toBeUndefined();
    expect(edited.reconciliation).toBe('idle');
  });

  it('preserves the selected traversal node after a table presentation update', () => {
    const workspace = {
      ...ready.workspace!,
      documents: ready.workspace!.documents.map((document) => ({
        ...document,
        route: {
          ...document.route,
          children: [
            {
              occurrenceId: 'observation',
              resourceType: 'Observation',
              relationship: 'focus_Specimen',
            },
          ],
        },
      })),
    };
    const state = {
      ...stateFromBuilder(
        { ...ready, workspace },
        { project: 'project', explorerId: 'default' },
      ),
      selectedOccurrenceId: 'observation',
    };

    const next = builderAuthoringReducer(state, {
      type: 'commandsApplied',
      value: {
        commandId: 'command-2',
        workspace,
        draftVersion: 2,
        draftDigest: 'sha256:next',
        results: [
          {
            type: 'TABLE_CHANGED',
            outputId: 'Specimen',
            column: 'observation__status',
          },
        ],
        diagnostics: [],
      },
    });

    expect(next.selectedOccurrenceId).toBe('observation');
  });

  it('falls back to the root when the selected traversal node was removed', () => {
    const state = {
      ...stateFromBuilder(ready, {
        project: 'project',
        explorerId: 'default',
      }),
      selectedOccurrenceId: 'removed-occurrence',
    };

    const next = builderAuthoringReducer(state, {
      type: 'commandsApplied',
      value: {
        commandId: 'command-3',
        workspace: ready.workspace!,
        draftVersion: 2,
        draftDigest: 'sha256:next',
        results: [{ type: 'TABLE_CHANGED', outputId: 'Specimen' }],
        diagnostics: [],
      },
    });

    expect(next.selectedOccurrenceId).toBe('base');
  });
});

describe('table selection automatic preview ownership', () => {
  const initialState = () => {
    const state = stateFromBuilder(ready, { project: 'project', explorerId: 'default' });
    const table = state.tables[0];
    return { ...state, tables: [table, { ...table, outputId: 'Observation', tabId: 'observation',
      document: { ...table.document, output: { id: 'Observation', title: 'Observation' } } }] };
  };
  const response = (identity: AutomaticPreviewIdentity) => {
    const receipt: ExplorerBuilderCompileResult = { apiVersion: ready.apiVersion, kind: 'ExplorerBuilderReceipt',
      receiptId: `receipt-${identity.key}`, snapshotToken: identity.snapshotToken, intentDigest: identity.draftDigest,
      builder: ready.workspace!, outputs: [{ outputId: identity.outputId, columns: [] }], diagnostics: [] };
    const preview = { apiVersion: ready.apiVersion, kind: 'ExplorerBuilderPreview' as const,
      rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' },
      receiptId: receipt.receiptId, outputId: identity.outputId, columns: [],
      rows: [{ table: identity.outputId, value: 17 }], rowCount: 1, diagnostics: [] };
    return { identity, receipt, preview };
  };
  const identityFor = (state: BuilderAuthoringState, limit: 25 | 100): AutomaticPreviewIdentity => ({
    ownerKey: 'owner', snapshotToken: state.catalog.snapshotToken, draftVersion: state.draftVersion,
    draftDigest: state.draftDigest, outputId: state.selectedOutputId!, previewRequestVersion: state.previewRequestVersion, limit,
    key: JSON.stringify(['owner', state.catalog.snapshotToken, state.draftVersion, state.draftDigest,
      state.previewRequestVersion, state.selectedOutputId, limit]),
  });
  it.each(['successful', 'failed'] as const)('restores exact saved rows after %s preview and proposal A → B → A Cancel, retaining limit and recipe', async (firstResult) => {
    const initial = initialState();
    const refresh = vi.fn();
    const hook = renderHook(({ enabled, limit }: { enabled: boolean; limit: 25 | 100 }) => {
      const [state, dispatch] = useReducer(builderAuthoringReducer, initial);
      const identity = identityFor(state, limit);
      const query = useAutomaticPreview({ request: identity, enabled, refresh: async (request) => {
        refresh(request);
        if (firstResult === 'failed' && refresh.mock.calls.length === 1) throw new Error('Old A request failed');
        const run = response(request);
        dispatch({ type: 'previewAccepted', ...run, identity: { ...request, project: state.project, explorerId: state.explorerId } });
        return run;
      } });
      return { state, dispatch, query };
    }, { initialProps: { enabled: true, limit: 100 as 25 | 100 } });
    if (firstResult === 'failed') await waitFor(() => expect(hook.result.current.query.error).toHaveProperty('message', 'Old A request failed'));
    else await waitFor(() => expect(hook.result.current.state.preview?.rows).toEqual([{ table: 'Specimen', value: 17 }]));
    hook.rerender({ enabled: false, limit: 100 });
    act(() => hook.result.current.dispatch({ type: 'selectTable', outputId: 'Observation' }));
    expect(hook.result.current.state.preview).toBeUndefined();
    act(() => hook.result.current.dispatch({ type: 'selectTable', outputId: 'Specimen' }));
    hook.rerender({ enabled: true, limit: 100 });
    await waitFor(() => expect(hook.result.current.state.preview?.rows).toEqual([{ table: 'Specimen', value: 17 }]));
    expect(refresh).toHaveBeenCalledTimes(2);
    expect(refresh.mock.calls.map(([request]) => request.limit)).toEqual([100, 100]);
    expect(workspaceFromState(hook.result.current.state)).toEqual(workspaceFromState(initial));
    hook.rerender({ enabled: true, limit: 25 });
    await waitFor(() => expect(refresh).toHaveBeenCalledTimes(3));
    expect(refresh.mock.calls[2][0].limit).toBe(25);
  });
  it('invalidates old A and B responses while same and unknown table selections remain no-ops', () => {
    const initial = initialState();
    expect(builderAuthoringReducer(initial, { type: 'selectTable', outputId: 'Specimen' })).toBe(initial);
    expect(builderAuthoringReducer(initial, { type: 'selectTable', outputId: 'unknown' })).toBe(initial);
    const b = builderAuthoringReducer(initial, { type: 'selectTable', outputId: 'Observation' });
    const a = builderAuthoringReducer(b, { type: 'selectTable', outputId: 'Specimen' });
    expect(b.previewRequestVersion).toBe(initial.previewRequestVersion + 1);
    expect(a.previewRequestVersion).toBe(initial.previewRequestVersion + 2);
    for (const stale of [initial, b]) {
      const run = response(identityFor(stale, 25));
      expect(builderAuthoringReducer(a, { type: 'previewAccepted', ...run,
        identity: { ...run.identity, project: a.project, explorerId: a.explorerId } })).toBe(a);
    }
  });
});

describe('saved occurrence inspection', () => {
  it('selects unresolved saved occurrences without changing the recipe or requiring catalog resolution', () => {
    const state = stateFromBuilder(ready, { project: 'project', explorerId: 'default' });
    const table = state.tables[0];
    const source: BuilderAuthoringState = { ...state, tables: [{ ...table, document: { ...table.document, route: {
      ...table.document.route, children: [{ occurrenceId: 'unresolved', resourceType: 'Patient', relationship: 'retired', catalogEdgeId: 'removed', children: [
        { occurrenceId: 'unresolved-child', resourceType: 'Observation', relationship: 'retired-child' },
      ] }],
    } } }] };
    expect(derivedOccurrences(source.tables[0], source.catalog).map(item => item.id)).toEqual(['base']);
    for (const occurrenceId of ['unresolved', 'unresolved-child', 'base']) {
      const selected = builderAuthoringReducer(source, { type: 'selectOccurrence', occurrenceId });
      expect(selected.selectedOccurrenceId).toBe(occurrenceId);
      expect(selected).toEqual({ ...source, selectedOccurrenceId: occurrenceId });
      expect(selected.tables).toBe(source.tables);
    }
    expect(builderAuthoringReducer(source, { type: 'selectOccurrence', occurrenceId: 'not-in-this-table' })).toBe(source);
  });
});
