import {
  assertExplorerBuilderCompileResult,
  assertExplorerBuilderPreviewResult,
  assertExplorerBuilderPublishResult,
  assertExplorerBuilderState,
  assertExplorerStateV1,
  explorerAuthoringCapabilitiesSchema,
  explorerBuilderCommandsResultSchema,
  explorerBuilderSuggestionsResultSchema,
  type ExplorerBuilderCatalog,
  type ExplorerBuilderCommand,
  type ExplorerBuilderCompileResult,
  type ExplorerBuilderPreviewResult,
  type ExplorerBuilderState,
  type ExplorerBuilderSuggestionsResult,
  type ExplorerBuilderWorkspace,
  type ExplorerRuntimeV1,
} from './types';
import type { ExplorerAuthoringDiagnostic } from './types';

export interface ExplorerSummary {
  readonly project: string;
  readonly explorerId: string;
  readonly title: string;
  readonly management: string;
  readonly activeRevisionId?: string;
  readonly updatedAt: string;
}

export interface ExplorerAuthoringApiError {
  readonly status: number | 'FETCH_ERROR' | 'CUSTOM_ERROR';
  readonly code?: string;
  readonly message: string;
  readonly diagnostics?: ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly requestId?: string;
  readonly details?: Readonly<Record<string, unknown>>;
  readonly retryable?: boolean;
}

export interface ExplorerAuthoringStateArgs {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
}

export interface ExplorerAuthoringProjectArgs {
  readonly project: string;
  readonly authResourcePath?: string;
}

export interface ApplyExplorerBuilderCommandsArgs extends ExplorerAuthoringStateArgs {
  readonly commandId: string;
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest?: string;
  readonly commands: ReadonlyArray<ExplorerBuilderCommand>;
  readonly requestId?: string;
}

export interface ReconcileExplorerBuilderArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly requestId?: string;
}

export interface PreviewExplorerBuilderArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly outputId: string;
  readonly limit?: number;
  readonly requestId?: string;
}

export interface PublishExplorerBuilderArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly requestId?: string;
}

export interface ExplorerCandidateSuggestionsArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly nodeId: string;
  readonly query?: string;
  readonly requestId?: string;
}

export interface CreateExplorerArgs extends ExplorerAuthoringProjectArgs {
  readonly name: string;
  readonly title?: string;
  readonly sourceExplorerId?: string;
  readonly requestId?: string;
}

export interface DeleteExplorerArgs extends ExplorerAuthoringStateArgs {
  readonly requestId?: string;
}

export interface LoomClientOptions {
  /** URL prefix for the Loom service. `/` is the standalone no-auth default. */
  readonly baseUrl?: string;
  /** Inject Calypr's authenticated fetch implementation when embedded. */
  readonly fetch?: typeof globalThis.fetch;
  readonly headers?: HeadersInit;
  readonly credentials?: RequestCredentials;
}

export interface LoomRowsOptions {
  readonly project: string;
  readonly first?: number;
  readonly signal?: AbortSignal;
}

export type LoomOutputFilterOperator =
  | 'EQ'
  | 'NEQ'
  | 'IN'
  | 'NOT_IN'
  | 'LT'
  | 'LTE'
  | 'GT'
  | 'GTE'
  | 'CONTAINS'
  | 'STARTS_WITH'
  | 'EXISTS'
  | 'IS_NULL'
  | 'ARRAY_CONTAINS'
  | 'ARRAY_OVERLAPS';

export interface LoomOutputFilter {
  readonly column: string;
  readonly op: LoomOutputFilterOperator;
  readonly value: unknown;
}

export interface LoomOutputSort {
  readonly column: string;
  readonly desc?: boolean;
}

export interface LoomFacetSpec {
  readonly name: string;
  readonly kind: 'TERMS' | 'HISTOGRAM' | 'DATE_HISTOGRAM' | 'STATS' | 'MISSING';
  readonly column: string;
  readonly size?: number;
  readonly interval?: number;
  readonly dateInterval?: number;
  readonly excludeSelfFilter?: boolean;
}

export interface LoomOutputRequest {
  readonly project: string;
  readonly selector: ExplorerRuntimeV1['outputs'][number]['selector'];
  readonly columns?: ReadonlyArray<string>;
  readonly filters?: ReadonlyArray<LoomOutputFilter>;
  readonly sort?: LoomOutputSort;
  readonly first?: number;
  readonly after?: string;
  readonly facets?: ReadonlyArray<LoomFacetSpec>;
  readonly exportHeaders?: Readonly<Record<string, string>>;
}

export interface LoomFacetResult {
  readonly name: string;
  readonly kind: string;
  readonly columns: ReadonlyArray<string>;
  readonly rows: ReadonlyArray<Record<string, unknown>>;
  readonly missingCount?: number;
  readonly truncated?: boolean;
}

export interface LoomOutputResult {
  readonly columns: ReadonlyArray<string>;
  readonly rows: ReadonlyArray<Record<string, unknown>>;
  readonly totalCount: number | null;
  readonly pageInfo: {
    readonly hasNextPage: boolean;
    readonly endCursor?: string;
  };
  readonly materialization?: Readonly<Record<string, unknown>>;
  readonly facets: ReadonlyArray<LoomFacetResult>;
}

export interface LoomClient {
  readonly listExplorers: (
    args: ExplorerAuthoringProjectArgs,
    signal?: AbortSignal,
  ) => Promise<ReadonlyArray<ExplorerSummary>>;
  readonly getBuilder: (
    args: ExplorerAuthoringStateArgs,
    options?: { readonly signal?: AbortSignal; readonly reload?: boolean },
  ) => Promise<ExplorerBuilderState>;
  readonly getCapability: (
    args: ExplorerAuthoringStateArgs,
    signal?: AbortSignal,
  ) => Promise<ReturnType<typeof explorerAuthoringCapabilitiesSchema.parse>>;
  readonly getExplorer: (
    args: ExplorerAuthoringStateArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerRuntimeV1>;
  readonly applyCommands: (
    args: ApplyExplorerBuilderCommandsArgs,
    signal?: AbortSignal,
  ) => Promise<ReturnType<typeof explorerBuilderCommandsResultSchema.parse>>;
  readonly reconcile: (
    args: ReconcileExplorerBuilderArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderCompileResult>;
  readonly suggestions: (
    args: ExplorerCandidateSuggestionsArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderSuggestionsResult>;
  readonly preview: (
    args: PreviewExplorerBuilderArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerBuilderPreviewResult>;
  readonly publish: (
    args: PublishExplorerBuilderArgs,
    signal?: AbortSignal,
  ) => Promise<ReturnType<typeof assertExplorerBuilderPublishResult>>;
  readonly createExplorer: (
    args: CreateExplorerArgs,
    signal?: AbortSignal,
  ) => Promise<ExplorerSummary>;
  readonly deleteExplorer: (
    args: DeleteExplorerArgs,
    signal?: AbortSignal,
  ) => Promise<null>;
  readonly fetchGraphQL: <T>(
    query: string,
    variables?: Readonly<Record<string, unknown>>,
    signal?: AbortSignal,
  ) => Promise<T>;
  readonly rows: (
    selector: ExplorerRuntimeV1['outputs'][number]['selector'],
    columns: ReadonlyArray<string>,
    options: LoomRowsOptions,
  ) => Promise<{ readonly columns: ReadonlyArray<string>; readonly rows: ReadonlyArray<Record<string, unknown>>; readonly totalCount?: number | null }>;
  readonly queryOutput: (
    request: LoomOutputRequest,
    signal?: AbortSignal,
  ) => Promise<LoomOutputResult>;
  readonly exportOutput: (
    request: LoomOutputRequest,
    signal?: AbortSignal,
  ) => Promise<Blob>;
  readonly invalidate: (scope?: 'explorers' | 'builder' | 'all') => void;
}

const canonicalProject = (project: string): string => {
  let value = project.trim();
  try {
    value = decodeURIComponent(value);
  } catch {
    // Leave malformed URL input for the server to reject with its contract.
  }
  value = value.replace(/^\/+|\/+$/g, '');
  return value;
};

const encodedProject = (project: string): string =>
  encodeURIComponent(encodeURIComponent(canonicalProject(project)));

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const parseJSON = async (response: Response): Promise<unknown> => {
  const text = await response.text();
  if (!text) return {};
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return { message: text };
  }
};

const diagnosticsFrom = (value: unknown): ReadonlyArray<ExplorerAuthoringDiagnostic> =>
  Array.isArray(value) ? value.filter(isRecord).flatMap((item) => {
    if (
      typeof item.code !== 'string' ||
      typeof item.message !== 'string' ||
      (item.severity !== 'error' && item.severity !== 'warning' && item.severity !== 'info')
    ) return [];
    return [{
      severity: item.severity,
      code: item.code,
      message: item.message,
      ...(typeof item.stage === 'string' ? { stage: item.stage } : {}),
      ...(typeof item.path === 'string' || item.path === null ? { path: item.path } : {}),
      ...(typeof item.fieldPath === 'string' || item.fieldPath === null ? { fieldPath: item.fieldPath } : {}),
      ...(typeof item.requestId === 'string' ? { requestId: item.requestId } : {}),
    }];
  }) : [];

const requestError = async (response: Response): Promise<ExplorerAuthoringApiError> => {
  const payload = await parseJSON(response);
  const record = isRecord(payload) ? payload : {};
  const nested = isRecord(record.error) ? record.error : record;
  const code = typeof nested.code === 'string' ? nested.code : undefined;
  const message = typeof nested.message === 'string'
    ? nested.message
    : `Loom request failed (${response.status}).`;
  return {
    status: response.status,
    code,
    message,
    diagnostics: diagnosticsFrom(nested.diagnostics),
    requestId: response.headers.get('x-request-id') ?? response.headers.get('request-id') ?? undefined,
    details: isRecord(nested.details) ? nested.details : undefined,
    retryable: response.status === 408 || response.status === 429 || response.status >= 500,
  };
};

export class LoomRequestError extends Error implements ExplorerAuthoringApiError {
  readonly info: ExplorerAuthoringApiError;
  readonly status: ExplorerAuthoringApiError['status'];
  readonly code?: string;
  readonly diagnostics?: ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly requestId?: string;
  readonly details?: Readonly<Record<string, unknown>>;
  readonly retryable?: boolean;
  constructor(info: ExplorerAuthoringApiError) {
    super(info.message);
    this.name = 'LoomRequestError';
    this.info = info;
    this.status = info.status;
    this.code = info.code;
    this.diagnostics = info.diagnostics;
    this.requestId = info.requestId;
    this.details = info.details;
    this.retryable = info.retryable;
  }
}

const shapeRows = (rows: unknown, columns: ReadonlyArray<string>): Array<Record<string, unknown>> => {
  if (!Array.isArray(rows)) return [];
  return rows.map((row) => {
    const source = Array.isArray(row)
      ? Object.fromEntries(columns.map((column, index) => [column, row[index]]))
      : isRecord(row) ? row : {};
    const result: Record<string, unknown> = {};
    Object.entries(source).forEach(([key, value]) => {
      const parts = key.split('.').filter(Boolean);
      if (parts.length < 2) {
        result[key] = value;
        return;
      }
      let cursor = result;
      parts.slice(0, -1).forEach((part) => {
        const nested = cursor[part];
        if (!isRecord(nested)) cursor[part] = {};
        cursor = cursor[part] as Record<string, unknown>;
      });
      cursor[parts[parts.length - 1]] = value;
    });
    return result;
  });
};

const numberOrNull = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const normalizedFacet = (value: unknown): LoomFacetResult | undefined => {
  if (!isRecord(value) || typeof value.name !== 'string' || typeof value.kind !== 'string') return undefined;
  const columns = Array.isArray(value.columns) ? value.columns.filter((column): column is string => typeof column === 'string') : [];
  const rows = shapeRows(value.rows, columns);
  const missingCount = numberOrNull(value.missingCount);
  return {
    name: value.name,
    kind: value.kind,
    columns,
    rows,
    ...(missingCount === null ? {} : { missingCount }),
    ...(typeof value.truncated === 'boolean' ? { truncated: value.truncated } : {}),
  };
};

const outputQuery = (request: LoomOutputRequest): {
  readonly query: string;
  readonly variables: Readonly<Record<string, unknown>>;
} => {
  const hasFacets = (request.facets?.length ?? 0) > 0;
  const input: Record<string, unknown> = {
    projectId: canonicalProject(request.project),
    selector: request.selector,
    ...(request.columns && request.columns.length > 0 ? { columns: [...request.columns] } : {}),
    ...(request.filters && request.filters.length > 0 ? { filters: request.filters.map((filter) => ({ column: filter.column, op: filter.op, value: filter.value })) } : {}),
    ...(request.sort ? { sort: { column: request.sort.column, desc: request.sort.desc ?? false } } : {}),
    ...(request.first === undefined ? {} : { first: request.first }),
    ...(request.after ? { after: request.after } : {}),
  };
  const query = hasFacets
    ? `query LoomOutput($input: DataframeRowsInput!, $facetInput: DataframeAggregationsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows totalCount pageInfo { hasNextPage endCursor } } dataframeAggregations(input: $facetInput) { aggregations } }`
    : `query LoomOutput($input: DataframeRowsInput!) { dataframeRows(input: $input) { materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows totalCount pageInfo { hasNextPage endCursor } } }`;
  const variables: Record<string, unknown> = { input };
  if (hasFacets) {
    variables.facetInput = {
      projectId: canonicalProject(request.project),
      selector: request.selector,
      ...(request.filters && request.filters.length > 0 ? { filters: request.filters.map((filter) => ({ column: filter.column, op: filter.op, value: filter.value })) } : {}),
      specs: [...(request.facets ?? [])],
    };
  }
  return { query, variables };
};

export const createLoomClient = (options: LoomClientOptions = {}): LoomClient => {
  const fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
  const baseUrl = options.baseUrl ?? '/';
  const cache = new Map<string, Promise<unknown>>();

  const urlFor = (path: string): string => {
    if (/^https?:\/\//.test(baseUrl)) return `${baseUrl.replace(/\/$/, '')}${path}`;
    return `${baseUrl.replace(/\/$/, '')}${path}` || '/';
  };
  const request = async (path: string, init: RequestInit = {}): Promise<unknown> => {
    try {
      const response = await fetcher(urlFor(path), {
        ...init,
        credentials: options.credentials ?? 'same-origin',
        headers: {
          Accept: 'application/json',
          ...(init.body ? { 'Content-Type': 'application/json' } : {}),
          ...(options.headers ?? {}),
          ...(init.headers ?? {}),
        },
      });
      if (!response.ok) throw new LoomRequestError(await requestError(response));
      return parseJSON(response);
    } catch (error) {
      if (error instanceof LoomRequestError) throw error;
      if (init.signal?.aborted) throw error;
      throw new LoomRequestError({
        status: 'FETCH_ERROR',
        message: error instanceof Error ? error.message : String(error),
        retryable: true,
      });
    }
  };
  const getCached = <T>(key: string, run: () => Promise<T>, reload = false): Promise<T> => {
    if (reload) cache.delete(key);
    const existing = cache.get(key);
    if (existing) return existing as Promise<T>;
    const promise = run();
    cache.set(key, promise);
    void promise.catch(() => cache.delete(key));
    return promise;
  };
  const authoringPath = (args: ExplorerAuthoringStateArgs, suffix: string): string =>
    `/api/v1/projects/${encodedProject(args.project)}/explorers/${encodeURIComponent(args.explorerId)}/authoring/v2${suffix}`;
  const projectPath = (args: ExplorerAuthoringProjectArgs): string =>
    `/api/v1/projects/${encodedProject(args.project)}/explorers`;
  const authResourcePathQuery = (authResourcePath?: string): string => {
    const value = authResourcePath?.trim();
    return value ? `?${new URLSearchParams({ auth_resource_path: value }).toString()}` : '';
  };
  const durableAuthoringPath = (args: ExplorerAuthoringStateArgs, suffix: string): string =>
    `${authoringPath(args, suffix)}${authResourcePathQuery(args.authResourcePath)}`;
  const durableProjectPath = (args: ExplorerAuthoringProjectArgs): string =>
    `${projectPath(args)}${authResourcePathQuery(args.authResourcePath)}`;
  const withJson = (body: unknown, signal?: AbortSignal, requestId?: string): RequestInit => ({
    method: 'POST',
    body: JSON.stringify(body),
    signal,
    headers: requestId ? { 'X-Request-ID': requestId } : undefined,
  });

  const listExplorers = (args: ExplorerAuthoringProjectArgs, signal?: AbortSignal) =>
    getCached(`explorers:${canonicalProject(args.project)}`, async () => {
      const value = await request(projectPath(args), { signal });
      if (!Array.isArray(value)) throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_LIST', message: 'Loom returned an invalid Explorer list.', retryable: false });
      return value as ReadonlyArray<ExplorerSummary>;
    });
  const getBuilder = (args: ExplorerAuthoringStateArgs, queryOptions: { readonly signal?: AbortSignal; readonly reload?: boolean } = {}) =>
    getCached(`builder:${canonicalProject(args.project)}:${args.explorerId}`, async () => assertExplorerBuilderState(await request(authoringPath(args, '/builder'), { signal: queryOptions.signal })), queryOptions.reload);
  const getCapability = (args: ExplorerAuthoringStateArgs, signal?: AbortSignal) =>
    getCached(`capability:${canonicalProject(args.project)}:${args.explorerId}`, async () => explorerAuthoringCapabilitiesSchema.parse(await request(authoringPath(args, '/capability'), { signal })));
  const getExplorer = (args: ExplorerAuthoringStateArgs, signal?: AbortSignal) =>
    getCached(`viewer:${canonicalProject(args.project)}:${args.explorerId}`, async () => {
      const value = await request(`/api/v1/projects/${encodedProject(args.project)}/explorers/${encodeURIComponent(args.explorerId)}`, { signal });
      const state = assertExplorerStateV1(value);
      if (!state.runtime) throw new LoomRequestError({ status: 422, code: 'EXPLORER_RUNTIME_REQUIRED', message: 'The selected Explorer has no published runtime.', retryable: false });
      return state.runtime;
    });
  const applyCommands = async (args: ApplyExplorerBuilderCommandsArgs, signal?: AbortSignal) => {
    const value = explorerBuilderCommandsResultSchema.parse(await request(durableAuthoringPath(args, '/commands'), withJson({
      commandId: args.commandId,
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      ...(args.expectedDraftDigest ? { expectedDraftDigest: args.expectedDraftDigest } : {}),
      commands: args.commands,
    }, signal, args.requestId)));
    cache.delete(`builder:${canonicalProject(args.project)}:${args.explorerId}`);
    return value;
  };
  const reconcile = (args: ReconcileExplorerBuilderArgs, signal?: AbortSignal) =>
    request(durableAuthoringPath(args, '/reconcile'), withJson({ snapshotToken: args.snapshotToken, draftVersion: args.draftVersion, draftDigest: args.draftDigest }, signal, args.requestId)).then(assertExplorerBuilderCompileResult);
  const suggestions = (args: ExplorerCandidateSuggestionsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/suggestions'), withJson({ snapshotToken: args.snapshotToken, nodeId: args.nodeId, ...(args.query ? { query: args.query } : {}) }, signal, args.requestId)).then((value) => explorerBuilderSuggestionsResultSchema.parse(value));
  const preview = (args: PreviewExplorerBuilderArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/preview'), withJson({ receiptId: args.receiptId, outputId: args.outputId, ...(args.limit === undefined ? {} : { limit: args.limit }) }, signal, args.requestId)).then(assertExplorerBuilderPreviewResult);
  const publish = async (args: PublishExplorerBuilderArgs, signal?: AbortSignal) => {
    const result = assertExplorerBuilderPublishResult(
      await request(
        durableAuthoringPath(args, '/publish'),
        withJson({ receiptId: args.receiptId }, signal, args.requestId),
      ),
    );
    cache.delete(`viewer:${canonicalProject(args.project)}:${args.explorerId}`);
    return result;
  };
  const createExplorer = async (args: CreateExplorerArgs, signal?: AbortSignal) => {
    const value = await request(durableProjectPath(args), withJson({ name: args.name, ...(args.title ? { title: args.title } : {}), ...(args.sourceExplorerId ? { sourceExplorerId: args.sourceExplorerId } : {}) }, signal, args.requestId));
    cache.delete(`explorers:${canonicalProject(args.project)}`);
    return value as ExplorerSummary;
  };
  const deleteExplorer = async (args: DeleteExplorerArgs, signal?: AbortSignal) => {
    await request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}`, { method: 'DELETE', signal, headers: args.requestId ? { 'X-Request-ID': args.requestId } : undefined });
    cache.delete(`explorers:${canonicalProject(args.project)}`);
    cache.delete(`builder:${canonicalProject(args.project)}:${args.explorerId}`);
    return null;
  };
  const fetchGraphQL = async <T>(query: string, variables?: Readonly<Record<string, unknown>>, signal?: AbortSignal): Promise<T> => {
    const payload = await request('/graphql/graph', { method: 'POST', signal, body: JSON.stringify({ query, variables }) });
    if (!isRecord(payload)) throw new LoomRequestError({ status: 502, code: 'INVALID_GRAPHQL_RESPONSE', message: 'Loom returned an invalid GraphQL response.', retryable: false });
    if (Array.isArray(payload.errors) && payload.errors.length > 0) {
      const first = isRecord(payload.errors[0]) && typeof payload.errors[0].message === 'string' ? payload.errors[0].message : 'Loom GraphQL request failed.';
      throw new LoomRequestError({ status: 'CUSTOM_ERROR', code: 'GRAPHQL_ERROR', message: first, retryable: false });
    }
    return payload.data as T;
  };
  const queryOutput = async (outputRequest: LoomOutputRequest, signal?: AbortSignal): Promise<LoomOutputResult> => {
    const prepared = outputQuery(outputRequest);
    const data = await fetchGraphQL<unknown>(prepared.query, prepared.variables, signal);
    if (!isRecord(data) || !isRecord(data.dataframeRows)) {
      throw new LoomRequestError({ status: 502, code: 'INVALID_OUTPUT_RESPONSE', message: 'Loom returned an invalid output response.', retryable: false });
    }
    const connection = data.dataframeRows;
    const columns = Array.isArray(connection.columns) ? connection.columns.filter((column): column is string => typeof column === 'string') : [];
    const pageInfo = isRecord(connection.pageInfo) ? connection.pageInfo : {};
    const facets = isRecord(data.dataframeAggregations) && Array.isArray(data.dataframeAggregations.aggregations)
      ? data.dataframeAggregations.aggregations.map(normalizedFacet).filter((facet): facet is LoomFacetResult => facet !== undefined)
      : [];
    const materialization = isRecord(connection.materialization) ? connection.materialization : undefined;
    const endCursor = typeof pageInfo.endCursor === 'string' ? pageInfo.endCursor : undefined;
    return {
      columns,
      rows: shapeRows(connection.rows, columns),
      totalCount: numberOrNull(connection.totalCount),
      pageInfo: { hasNextPage: pageInfo.hasNextPage === true, ...(endCursor ? { endCursor } : {}) },
      ...(materialization ? { materialization } : {}),
      facets,
    };
  };

  const exportOutput = async (outputRequest: LoomOutputRequest, signal?: AbortSignal): Promise<Blob> => {
    const rows: Array<Record<string, unknown>> = [];
    let columns: ReadonlyArray<string> = outputRequest.columns ?? [];
    let after: string | undefined;
    const first = Math.max(outputRequest.first ?? 100, 1000);
    while (true) {
      if (signal?.aborted) throw new DOMException('The export was aborted.', 'AbortError');
      const page = await queryOutput({ ...outputRequest, first, after, facets: [] }, signal);
      if (columns.length === 0) columns = page.columns;
      rows.push(...page.rows);
      if (!page.pageInfo.hasNextPage || !page.pageInfo.endCursor || page.pageInfo.endCursor === after) break;
      after = page.pageInfo.endCursor;
    }
    const csvValue = (value: unknown): string => {
      if (value === undefined || value === null) return '';
      if (typeof value === 'string') return value;
      if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') return String(value);
      try { return JSON.stringify(value); } catch { return String(value); }
    };
    const csvCell = (value: unknown): string => {
      const text = csvValue(value);
      return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
    };
    const lines = [columns.map((column) => csvCell(outputRequest.exportHeaders?.[column] ?? column)).join(','), ...rows.map((row) => columns.map((column) => csvCell(row[column])).join(','))];
    return new Blob([lines.join('\n') + '\n'], { type: 'text/csv;charset=utf-8' });
  };

  const rows = async (selector: ExplorerRuntimeV1['outputs'][number]['selector'], columns: ReadonlyArray<string>, rowOptions: LoomRowsOptions) => {
    const result = await queryOutput({ project: rowOptions.project, selector, columns, first: rowOptions.first ?? 100 }, rowOptions.signal);
    return { columns: result.columns, rows: result.rows, totalCount: result.totalCount };
  };
  return {
    listExplorers,
    getBuilder,
    getCapability,
    getExplorer,
    applyCommands,
    reconcile,
    suggestions,
    preview,
    publish,
    createExplorer,
    deleteExplorer,
    fetchGraphQL,
    rows,
    queryOutput,
    exportOutput,
    invalidate: (scope = 'all') => {
      if (scope === 'all' || scope === 'explorers') [...cache.keys()].filter((key) => key.startsWith('explorers:')).forEach((key) => cache.delete(key));
      if (scope === 'all' || scope === 'builder') [...cache.keys()].filter((key) => key.startsWith('builder:')).forEach((key) => cache.delete(key));
      if (scope === 'all') [...cache.keys()].filter((key) => key.startsWith('viewer:')).forEach((key) => cache.delete(key));
    },
  };
};

export type { ExplorerBuilderCatalog };
