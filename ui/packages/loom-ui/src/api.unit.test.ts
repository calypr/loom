import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from './api';

describe('Loom project paths', () => {
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

  it('scopes dataframe row queries to the selected project', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ data: { dataframeRows: { columns: ['patient_id'], rows: [], totalCount: 0 } } }),
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
    const payload = JSON.parse(String(fetch.mock.calls[0]?.[1]?.body)) as { variables: { input: Record<string, unknown>; facetInput: Record<string, unknown> } };
    expect(payload.variables.input).toMatchObject({ projectId: 'NCPI_ACCEPTANCE', first: 20, after: 'cursor-1', sort: { column: 'status', desc: true } });
    expect(payload.variables.input.filters).toEqual([{ column: 'status', op: 'IN', value: ['active', 'pending'] }]);
    expect(payload.variables.facetInput.specs).toEqual([{ name: 'status', kind: 'TERMS', column: 'status', size: 10 }]);
    expect(result.rows).toEqual([{ status: 'active' }]);
    expect(result.facets[0]?.rows[0]).toEqual({ key: 'active', doc_count: 1 });
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
});
