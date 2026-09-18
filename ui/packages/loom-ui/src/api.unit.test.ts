import { describe, expect, it, vi } from 'vitest';
import { createLoomClient, populationMappingResponseSchema } from './api';

describe('Loom project paths', () => {
  it('parses a bounded population coverage report', () => {
    expect(populationMappingResponseSchema.parse({
      binding: { receiptId: 'receipt-1', outputId: 'patients', project: 'NCPI_ACCEPTANCE', explorerId: 'default', generation: 'generation-1', scopeDigest: 'scope-1', selectionRevisionId: 'selection-1', membershipDigest: 'members-1', resourceType: 'DocumentReference' },
      status: 'COMPLETE',
      counts: { selected: 3, mapped: 2, unmapped: 1, emittedRows: 1 },
      unmapped: [{ project: 'NCPI_ACCEPTANCE', generation: 'generation-1', resourceType: 'DocumentReference', id: 'dev-file-004' }],
      diagnostics: [],
    })).toEqual({
      binding: { receiptId: 'receipt-1', outputId: 'patients', project: 'NCPI_ACCEPTANCE', explorerId: 'default', generation: 'generation-1', scopeDigest: 'scope-1', selectionRevisionId: 'selection-1', membershipDigest: 'members-1', resourceType: 'DocumentReference' },
      status: 'COMPLETE',
      counts: { selected: 3, mapped: 2, unmapped: 1, emittedRows: 1 },
      unmapped: [{ project: 'NCPI_ACCEPTANCE', generation: 'generation-1', resourceType: 'DocumentReference', id: 'dev-file-004' }],
      diagnostics: [],
    });
  });

  it('posts population coverage checks with the receipt and output binding', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({
      binding: { receiptId: 'receipt-1', outputId: 'patients', project: 'NCPI_ACCEPTANCE', explorerId: 'default', generation: 'generation-1', scopeDigest: 'scope-1', selectionRevisionId: 'selection-1', membershipDigest: 'members-1', resourceType: 'DocumentReference' },
      status: 'COMPLETE',
      counts: { selected: 3, mapped: 2, unmapped: 1, emittedRows: 1 },
      unmapped: [{ project: 'NCPI_ACCEPTANCE', generation: 'generation-1', resourceType: 'DocumentReference', id: 'dev-file-004' }],
      diagnostics: [],
    }), { status: 200, headers: { 'content-type': 'application/json' } }));
    const client = createLoomClient({ fetch });

    await expect(client.populationMapping({
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      authResourcePath: '/programs/NCPI/projects/acceptance',
      receiptId: 'receipt-1',
      outputId: 'patients',
      cursor: 'cursor-1',
      limit: 100,
    })).resolves.toMatchObject({ status: 'COMPLETE', counts: { selected: 3, mapped: 2, unmapped: 1, emittedRows: 1 } });
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers/default/authoring/v2/population-mapping',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ receiptId: 'receipt-1', outputId: 'patients', cursor: 'cursor-1', limit: 100 }) }),
    );
  });

  it('parses and posts a draft-bound row-change assessment', async () => {
    const response = {
      snapshotToken: 'snapshot-1',
      draftVersion: 4,
      draftDigest: 'digest-4',
      status: 'READY',
      currentRootResourceType: 'Patient',
      candidateRootResourceType: 'Encounter',
      preservedFeatureKeys: ['patient_id'],
      proposal: {
        outputId: 'patients',
        rootNodeId: 'n_encounter',
        rootOccurrenceId: 'encounter',
        sourceDocumentDigest: 'document-4',
        routeRebase: [{ occurrenceId: 'base', edgeId: 'encounter-patient' }],
        preservedFeatureKeys: ['patient_id'],
      },
      unresolved: [],
      diagnostics: [],
    };
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify(response), { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    const client = createLoomClient({ fetch });

    await expect(client.assessRowChange({
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      snapshotToken: 'snapshot-1',
      draftVersion: 4,
      draftDigest: 'digest-4',
      outputId: 'patients',
      rootNodeId: 'n_encounter',
    })).resolves.toEqual(response);
    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers/default/authoring/v2/row-change',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'digest-4', outputId: 'patients', rootNodeId: 'n_encounter' }),
      }),
    );
  });

  it('sends Calypr authorization scope only on durable Explorer mutations', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: 'TEST_STOP', message: 'stop after transport' } }), {
        status: 403,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const client = createLoomClient({ fetch });
    const scope = {
      project: 'HTAN_INT/BForePC',
      explorerId: 'test',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
    };
    const ignoreFailure = async (request: Promise<unknown>) => {
      await request.catch(() => undefined);
    };

    await ignoreFailure(client.createExplorer({ ...scope, name: 'test' }));
    await ignoreFailure(client.applyCommands({
      ...scope,
      commandId: 'command-1',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      commands: [],
    }));
    await ignoreFailure(client.reconcile({
      ...scope,
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'digest-1',
    }));
    await ignoreFailure(client.publish({ ...scope, receiptId: 'receipt-1' }));
    await ignoreFailure(client.suggestions({ ...scope, snapshotToken: 'snapshot-1', nodeId: 'Patient' }));
    await ignoreFailure(client.assessRowChange({ ...scope, snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'digest-1', outputId: 'patients', rootNodeId: 'Patient' }));
    await ignoreFailure(client.preview({ ...scope, receiptId: 'receipt-1', outputId: 'patients' }));

    const urls = fetch.mock.calls.map(([url]) => String(url));
    const encodedScope = 'auth_resource_path=%2Fprograms%2FHTAN_INT%2Fprojects%2FBForePC';
    expect(urls.slice(0, 4)).toEqual([
      `/api/v1/projects/HTAN_INT%252FBForePC/explorers?${encodedScope}`,
      `/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/commands?${encodedScope}`,
      `/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/reconcile?${encodedScope}`,
      `/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/publish?${encodedScope}`,
    ]);
    expect(urls.slice(4)).toEqual([
      '/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/suggestions',
      '/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/row-change',
      '/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/preview',
    ]);
  });

  it('omits authorization scope cleanly for standalone mutations', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: 'TEST_STOP', message: 'stop after transport' } }), {
        status: 403,
        headers: { 'content-type': 'application/json' },
      }),
    );
    const client = createLoomClient({ fetch });

    await client.createExplorer({ project: 'NCPI_ACCEPTANCE', name: 'default' }).catch(() => undefined);
    await client.applyCommands({
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      commandId: 'command-1',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      commands: [],
    }).catch(() => undefined);

    await client.reconcile({
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      snapshotToken: 'snapshot-1',
      draftVersion: 1,
      draftDigest: 'digest-1',
    }).catch(() => undefined);
    await client.publish({
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      receiptId: 'receipt-1',
    }).catch(() => undefined);

    expect(fetch.mock.calls.map(([url]) => String(url))).toEqual([
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers',
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers/default/authoring/v2/commands',
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers/default/authoring/v2/reconcile',
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers/default/authoring/v2/publish',
    ]);
    expect(JSON.parse(String(fetch.mock.calls[1][1]?.body))).toEqual({
      commandId: 'command-1',
      semanticsVersion: 4,
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 1,
      commands: [],
    });
  });

  it('preserves standalone project identifiers', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response('[]', { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    const client = createLoomClient({ fetch });

    await client.listExplorers({ project: 'NCPI_ACCEPTANCE' });

    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/projects/NCPI_ACCEPTANCE/explorers',
      expect.any(Object),
    );
  });

  it('keeps Calypr organization and project identifiers double encoded', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response('[]', { status: 200, headers: { 'content-type': 'application/json' } }),
    );
    const client = createLoomClient({ fetch });

    await client.listExplorers({ project: 'HTAN_INT/BForePC' });

    expect(fetch).toHaveBeenCalledWith(
      '/api/v1/projects/HTAN_INT%252FBForePC/explorers',
      expect.any(Object),
    );
  });

  it('separates cached Builder reads by authorization scope', async () => {
    const builder = JSON.stringify({
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderState',
      lifecycleState: 'NEW',
      draftVersion: 1,
      draftDigest: 'digest',
      workspace: null,
      catalog: { snapshotToken: 'snapshot-1', generation: 'generation-1', routePolicy: {}, nodes: [], edges: [], candidates: [] },
    });
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockImplementation(async () => new Response(builder, { status: 200 }));
    const client = createLoomClient({ fetch });
    const base = { project: 'HTAN_INT/BForePC', explorerId: 'default' };

    await client.getBuilder({ ...base, authResourcePath: '/programs/HTAN_INT/projects/BForePC' });
    await client.getBuilder({ ...base, authResourcePath: '/programs/HTAN_INT/projects/OtherPC' });

    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('scopes dataframe row queries to the selected project', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ data: { dataframeRows: { columns: ['patient_id'], rows: [], totalCount: 0, pageInfo: { hasNextPage: false } } } }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      ),
    );
    const client = createLoomClient({ fetch });

    await client.rows(
      { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
      ['patient_id'],
      { project: 'NCPI_ACCEPTANCE' },
    );

    const request = fetch.mock.calls[0]?.[1];
    const body = JSON.parse(String(request?.body)) as { variables: { input: { projectId?: string } } };
    expect(body.variables.input.projectId).toBe('NCPI_ACCEPTANCE');
  });

  it('exposes structured HTTP error fields to Builder recovery code', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({
          error: {
            code: 'STALE_SNAPSHOT',
            message: 'The catalog snapshot changed.',
            diagnostics: [
              {
                severity: 'error',
                code: 'STALE_SNAPSHOT',
                message: 'Reload the Builder state.',
              },
            ],
          },
        }),
        {
          status: 409,
          headers: {
            'content-type': 'application/json',
            'x-request-id': 'request-123',
          },
        },
      ),
    );
    const client = createLoomClient({ fetch });

    await expect(
      client.listExplorers({ project: 'NCPI_ACCEPTANCE' }),
    ).rejects.toMatchObject({
      status: 409,
      code: 'STALE_SNAPSHOT',
      retryable: false,
      requestId: 'request-123',
      diagnostics: [
        expect.objectContaining({ code: 'STALE_SNAPSHOT' }),
      ],
    });
  });

  it('invalidates cached Viewer runtime after publication', async () => {
    const explorerState = (generation: string) => ({
      apiVersion: 'loom.calypr.org/explorer-state/v1',
      kind: 'ExplorerState',
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      title: 'Cohort',
      management: 'interactive',
      active: {},
      draft: { version: 1, digest: 'digest' },
      generated: {},
      activeUrl: '/viewer',
      runtime: { generation, outputs: [], sharedFilters: {}, diagnostics: [] },
    });
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(
        new Response(JSON.stringify(explorerState('old')), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            apiVersion: 'loom.calypr.org/explorer-authoring/v2',
            kind: 'ExplorerBuilderPublication',
            receiptId: 'receipt-1',
            revisionId: 'revision-2',
            state: 'READY',
            outputs: [],
            diagnostics: [],
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        ),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(explorerState('new')), {
          status: 200,
          headers: { 'content-type': 'application/json' },
        }),
      );
    const client = createLoomClient({ fetch });
    const scope = { project: 'NCPI_ACCEPTANCE', explorerId: 'default' };

    await expect(client.getExplorer(scope)).resolves.toMatchObject({ generation: 'old' });
    await client.publish({ ...scope, receiptId: 'receipt-1' });
    await expect(client.getExplorer(scope)).resolves.toMatchObject({ generation: 'new' });
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('rejects a runtime output that omits its selector at the API boundary', async () => {
    const state = {
      apiVersion: 'loom.calypr.org/explorer-state/v1',
      kind: 'ExplorerState',
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      title: 'Cohort',
      management: 'interactive',
      active: {},
      draft: { version: 1, digest: 'digest' },
      generated: {},
      activeUrl: '/viewer',
      runtime: {
        outputs: [{
          outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient',
          columns: [], table: { columns: [] }, filters: [], charts: [], fixedFilters: {},
        }],
        sharedFilters: {}, diagnostics: [],
      },
    };
    const client = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(state), { status: 200 })),
    });

    await expect(client.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .rejects.toMatchObject({ status: 502, code: 'INVALID_EXPLORER_STATE' });
  });

  it('rejects a runtime output with a non-string column binding', async () => {
    const state = {
      apiVersion: 'loom.calypr.org/explorer-state/v1',
      kind: 'ExplorerState',
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      title: 'Cohort',
      management: 'interactive',
      active: {},
      draft: { version: 1, digest: 'digest' },
      generated: {},
      activeUrl: '/viewer',
      runtime: {
        outputs: [{
          outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient',
          selector: { recipe: 'r', translationVersion: 'v1', output: 'patients' },
          columns: [{ column: 7, label: 'Patient ID', logicalType: 'string', visible: true, order: 0, filterable: true, chartable: false }],
          table: { columns: [] }, filters: [], charts: [], fixedFilters: {},
        }],
        sharedFilters: {}, diagnostics: [],
      },
    };
    const client = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(state), { status: 200 })),
    });

    await expect(client.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .rejects.toMatchObject({ status: 502, code: 'INVALID_EXPLORER_STATE' });
  });

  it('parses a canonical runtime fixture without changing its typed output', async () => {
    const state = {
      apiVersion: 'loom.calypr.org/explorer-state/v1',
      kind: 'ExplorerState',
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      title: 'Cohort',
      management: 'interactive',
      active: {},
      draft: { version: 1, digest: 'digest' },
      generated: {},
      activeUrl: '/viewer',
      runtime: {
        generation: 'generation-1',
        outputs: [{
          outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient',
          selector: { recipe: 'r', translationVersion: 'v1', output: 'patients' },
          columns: [{ column: 'patient_id', label: 'Patient ID', logicalType: 'string', visible: true, order: 0, filterable: true, chartable: false }],
          table: { columns: [{ column: 'patient_id', visible: true }] }, filters: [], charts: [], fixedFilters: {},
        }],
        sharedFilters: {}, diagnostics: [],
      },
    };
    const client = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(state), { status: 200 })),
    });

    await expect(client.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .resolves.toMatchObject({ generation: 'generation-1', outputs: [{ selector: state.runtime.outputs[0].selector }] });
  });

  it('accepts a nullable generated dataset output list at the API boundary', async () => {
    const state = {
      apiVersion: 'loom.calypr.org/explorer-state/v1',
      kind: 'ExplorerState',
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      title: 'Cohort',
      management: 'interactive',
      active: { revisionId: 'revision-1' },
      draft: { version: 1, digest: 'digest' },
      generated: {
        dataset: { generation: 'generation-1', schemaDigest: 'schema-1', outputs: null },
      },
      activeUrl: '/viewer',
      runtime: { outputs: [], sharedFilters: {}, diagnostics: [] },
    };
    const client = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(state), { status: 200 })),
    });

    await expect(client.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .resolves.toMatchObject({ outputs: [], responseIdentity: 'revision-1' });
  });

  it('derives distinct session identities for legacy runtimes from response revisions', async () => {
    const state = (revisionId: string) => ({
      apiVersion: 'loom.calypr.org/explorer-state/v1',
      kind: 'ExplorerState',
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      title: 'Cohort',
      management: 'interactive',
      active: { revisionId },
      draft: { version: 1, digest: 'digest' },
      generated: {},
      activeUrl: '/viewer',
      runtime: { outputs: [], sharedFilters: {}, diagnostics: [] },
    });
    const first = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(state('revision-a')), { status: 200 })),
    });
    const second = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify(state('revision-b')), { status: 200 })),
    });

    await expect(first.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .resolves.toMatchObject({ responseIdentity: 'revision-a' });
    await expect(second.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .resolves.toMatchObject({ responseIdentity: 'revision-b' });
  });

  it('rejects an identity-free legacy runtime instead of sharing Viewer state', async () => {
    const client = createLoomClient({
      fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({
        apiVersion: 'loom.calypr.org/explorer-state/v1',
        kind: 'ExplorerState',
        project: 'NCPI_ACCEPTANCE',
        explorerId: 'default',
        title: 'Cohort',
        management: 'interactive',
        active: {},
        draft: { version: 1, digest: 'digest' },
        generated: {},
        activeUrl: '/viewer',
        runtime: { outputs: [], sharedFilters: {}, diagnostics: [] },
      }), { status: 200 })),
    });

    await expect(client.getExplorer({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }))
      .rejects.toMatchObject({ status: 502, code: 'INVALID_EXPLORER_STATE' });
  });

  it('serializes server-side filters, sort, cursors, and requested facets', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({
      data: {
        dataframeRows: {
          materialization: { id: 'mat-1', state: 'READY' },
          columns: ['status'], rows: [['active']], totalCount: 1,
          pageInfo: { hasNextPage: false, endCursor: null },
        },
        dataframeAggregations: { aggregations: [{ name: 'status', kind: 'TERMS', columns: ['key', 'doc_count'], rows: [{ key: 'active', doc_count: 1 }] }] },
      },
    }), { status: 200 }));
    const client = createLoomClient({ fetch });
    const result = await client.queryOutput({
      project: 'NCPI_ACCEPTANCE',
      selector: { recipe: 'cohort', translationVersion: 'v1', output: 'patients' },
      columns: ['status'],
      filters: [{ column: 'status', op: 'IN', value: ['active', 'pending'] }],
      sort: { column: 'status', desc: true }, first: 20, after: 'cursor-1',
      facets: [{ name: 'status', kind: 'TERMS', column: 'status', size: 10 }],
    });
    const payload = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as { query: string; variables: { input: Record<string, unknown>; facetInput: Record<string, unknown> } };
    expect(payload.query).toBe('query LoomOutput($input: DataframeRowsInput!, $facetInput: DataframeAggregationsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows rowIds totalCount pageInfo { hasNextPage endCursor } } dataframeAggregations(input: $facetInput) { aggregations } }');
    expect(payload.variables.input).toMatchObject({ projectId: 'NCPI_ACCEPTANCE', first: 20, after: 'cursor-1', sort: { column: 'status', desc: true } });
    expect(payload.variables.input.filters).toEqual([{ column: 'status', op: 'IN', value: ['active', 'pending'] }]);
    expect(payload.variables.facetInput.specs).toEqual([{ name: 'status', kind: 'TERMS', column: 'status', size: 10 }]);
    expect(result.rows).toEqual([{ status: 'active' }]);
    expect(result.facets[0]?.rows[0]).toEqual({ key: 'active', doc_count: 1 });
  });

  it('rejects malformed GraphQL output connections instead of defaulting fields', async () => {
    const validConnection = {
      columns: ['status'],
      rows: [['active']],
      totalCount: 1,
      pageInfo: { hasNextPage: false },
    };
    const malformed = [
      { ...validConnection, columns: undefined },
      { ...validConnection, rows: undefined },
      { ...validConnection, pageInfo: undefined },
      { ...validConnection, materialization: { revision: 7 } },
    ];
    for (const connection of malformed) {
      const client = createLoomClient({
        fetch: vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({ data: { dataframeRows: connection } }), { status: 200 })),
      });
      await expect(client.queryOutput({
        project: 'NCPI_ACCEPTANCE',
        selector: { recipe: 'r', translationVersion: 'v1', output: 'o' },
      })).rejects.toMatchObject({ status: 502, code: 'INVALID_OUTPUT_RESPONSE' });
    }
  });

  it('normalizes array and object GraphQL row forms without changing values', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({ data: {
      dataframeRows: {
        columns: ['status', 'nested.value'],
        rows: [['active', 1], { status: 'pending', 'nested.value': 2 }],
        totalCount: 2,
        pageInfo: { hasNextPage: false },
      },
    } }), { status: 200 }));
    const client = createLoomClient({ fetch });

    await expect(client.queryOutput({
      project: 'NCPI_ACCEPTANCE',
      selector: { recipe: 'r', translationVersion: 'v1', output: 'o' },
    })).resolves.toMatchObject({ rows: [{ status: 'active', nested: { value: 1 } }, { status: 'pending', nested: { value: 2 } }] });
  });

  it('exports all cursor pages as an escaped CSV Blob', async () => {
    const page = (rows: unknown[][], hasNextPage: boolean, endCursor?: string) => new Response(JSON.stringify({ data: { dataframeRows: { columns: ['id', 'label'], rows, totalCount: 2, pageInfo: { hasNextPage, endCursor } } } }), { status: 200 });
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(page([['one', 'a,b']], true, 'next'))
      .mockResolvedValueOnce(page([['two', 'say "hi"']], false));
    const client = createLoomClient({ fetch });
    const blob = await client.exportOutput({ project: 'NCPI_ACCEPTANCE', selector: { recipe: 'r', translationVersion: 'v1', output: 'o' }, columns: ['id', 'label'], first: 1, after: 'current-page', exportHeaders: { id: 'Record ID' } });
    expect(await blob.text()).toBe('Record ID,label\none,"a,b"\ntwo,"say ""hi"""\n');
    expect(fetch).toHaveBeenCalledTimes(2);
    const secondBody = JSON.parse(String(fetch.mock.calls[1]?.[1]?.body)) as { variables: { input: { after?: string } } };
    const firstBody = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as { variables: { input: { after?: string; first?: number } } };
    expect(firstBody.variables.input.after).toBeUndefined();
    expect(firstBody.variables.input.first).toBe(1000);
    expect(secondBody.variables.input.after).toBe('next');
  });

  it('preserves a typed stale-cursor conflict from GraphQL', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(new Response(JSON.stringify({
      errors: [{ message: 'The page cursor is stale.', extensions: { code: 'STALE_CURSOR', retryable: false } }],
    }), { status: 200 }));
    const client = createLoomClient({ fetch });

    await expect(client.queryOutput({
      project: 'NCPI_ACCEPTANCE',
      selector: { recipe: 'r', translationVersion: 'v1', output: 'o' },
    })).rejects.toMatchObject({ status: 409, code: 'STALE_CURSOR', retryable: false });
  });

  it('rejects export pages that change publication identity', async () => {
    const materialization = (revision: string) => ({
      id: `execution:${revision}:Patient`,
      revision,
      projectId: 'NCPI_ACCEPTANCE',
      datasetGeneration: 'generation-1',
      selector: { recipe: 'r', translationVersion: 'v1', output: 'o' },
    });
    const page = (revision: string, rows: unknown[][], hasNextPage: boolean, endCursor?: string) => new Response(JSON.stringify({ data: {
      dataframeRows: {
        materialization: materialization(revision),
        columns: ['id'],
        rows,
        totalCount: 2,
        pageInfo: { hasNextPage, endCursor },
      },
    } }), { status: 200 });
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(page('revision-a', [['one']], true, 'next'))
      .mockResolvedValueOnce(page('revision-b', [['two']], false));
    const client = createLoomClient({ fetch });

    await expect(client.exportOutput({
      project: 'NCPI_ACCEPTANCE',
      selector: { recipe: 'r', translationVersion: 'v1', output: 'o' },
      columns: ['id'],
    })).rejects.toMatchObject({ status: 409, code: 'PUBLICATION_CONFLICT' });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
