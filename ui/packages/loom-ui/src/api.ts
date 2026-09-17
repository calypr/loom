import {
  EXPLORER_AUTHORING_SEMANTICS_VERSION,
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
import { z } from 'zod';
import { dataframeOutputQuery } from './dataframeOutputQuery.mjs';
import {
  selectionPageSchema,
  selectionRevisionSchema,
  resourceRefSchema,
  type ResourceRef,
  type SelectionPage,
  type SelectionRevision,
  type SelectionSourceIntent,
} from './selection';

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

export interface PopulationMappingArgs extends ExplorerAuthoringStateArgs {
  readonly receiptId: string;
  readonly outputId: string;
  readonly cursor?: string;
  readonly limit?: number;
}

const populationMappingDiagnosticSchema = z.object({
  severity: z.string(),
  stage: z.string(),
  code: z.string(),
  message: z.string(),
}).strict();
const populationMappingCountsSchema = z.object({
  selected: z.number().int().nonnegative(),
  mapped: z.number().int().nonnegative(),
  unmapped: z.number().int().nonnegative(),
  emittedRows: z.number().int().nonnegative(),
}).strict();
export const populationMappingResponseSchema = z.object({
  status: z.enum(['COMPLETE', 'INCOMPLETE']),
  counts: populationMappingCountsSchema.nullable().optional(),
  unmapped: z.array(resourceRefSchema),
  nextCursor: z.string().min(1).optional(),
  diagnostics: z.array(populationMappingDiagnosticSchema),
}).strict();
export type PopulationMappingResponse = z.infer<typeof populationMappingResponseSchema>;

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

export interface CreateSelectionArgs extends ExplorerAuthoringStateArgs {
  readonly snapshotToken: string;
  readonly idempotencyKey: string;
  readonly source: SelectionSourceIntent;
  readonly exclusions?: ReadonlyArray<ResourceRef>;
  readonly requestId?: string;
}

export interface GetSelectionArgs extends ExplorerAuthoringStateArgs {
  readonly selectionRevision: string;
  readonly cursor?: string;
  readonly limit?: number;
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
  readonly createSelection: (args: CreateSelectionArgs, signal?: AbortSignal) => Promise<SelectionRevision>;
  readonly getSelection: (args: GetSelectionArgs, signal?: AbortSignal) => Promise<SelectionPage>;
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
  readonly populationMapping: (
    args: PopulationMappingArgs,
    signal?: AbortSignal,
  ) => Promise<PopulationMappingResponse>;
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
  readonly fetchGraphQL: (
    query: string,
    variables?: Readonly<Record<string, unknown>>,
    signal?: AbortSignal,
  ) => Promise<unknown>;
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

export const canonicalProject = (project: string): string => {
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

const graphQLRowSchema = z.union([
  z.array(z.unknown()),
  z.record(z.string(), z.unknown()),
]);
type GraphQLRow = z.infer<typeof graphQLRowSchema>;
const graphQLFacetSchema = z.object({
  name: z.string(),
  kind: z.string(),
  columns: z.array(z.string()),
  rows: z.array(graphQLRowSchema),
  missingCount: z.number().finite().nullable().optional(),
  truncated: z.boolean().optional(),
}).passthrough();
type GraphQLFacet = z.infer<typeof graphQLFacetSchema>;
const graphQLMaterializationSchema = z.object({
  id: z.string().optional(),
  name: z.string().optional(),
  revision: z.string().optional(),
  projectId: z.string().optional(),
  datasetGeneration: z.string().optional(),
  state: z.string().optional(),
  rowCount: z.number().int().nonnegative().nullable().optional(),
  selector: z.object({
    recipe: z.string(),
    translationVersion: z.string(),
    output: z.string(),
  }).strict().nullable().optional(),
}).passthrough();
const graphQLConnectionSchema = z.object({
  materialization: graphQLMaterializationSchema.optional(),
  columns: z.array(z.string()),
  rows: z.array(graphQLRowSchema),
  totalCount: z.number().int().nonnegative().nullable(),
  pageInfo: z.object({
    hasNextPage: z.boolean(),
    endCursor: z.string().nullable().optional(),
  }).strict(),
}).passthrough();
const graphQLOutputDataSchema = z.object({
  dataframeRows: graphQLConnectionSchema,
  dataframeAggregations: z.object({ aggregations: z.array(graphQLFacetSchema) }).passthrough().optional(),
}).passthrough();

const shapeRows = (rows: ReadonlyArray<GraphQLRow>, columns: ReadonlyArray<string>): Array<Record<string, unknown>> => {
  return rows.map((row) => {
    const source = Array.isArray(row)
      ? Object.fromEntries(columns.map((column, index) => [column, row[index]]))
      : row;
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
        if (!isRecord(nested)) {
          const next: Record<string, unknown> = {};
          cursor[part] = next;
          cursor = next;
        } else {
          cursor = nested;
        }
      });
      cursor[parts[parts.length - 1]] = value;
    });
    return result;
  });
};

const numberOrNull = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const normalizedFacet = (value: GraphQLFacet): LoomFacetResult => {
  const columns = value.columns;
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
  const query = dataframeOutputQuery('LoomOutput', hasFacets);
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

const materializationIdentity = (
  value: Readonly<Record<string, unknown>>,
): string | undefined => {
  const selector = isRecord(value.selector)
    ? {
        recipe: value.selector.recipe,
        translationVersion: value.selector.translationVersion,
        output: value.selector.output,
      }
    : undefined;
  const identity = {
    id: value.id,
    revision: value.revision,
    projectId: value.projectId,
    datasetGeneration: value.datasetGeneration,
    selector,
  };
  return Object.values(identity).every((part) => part !== undefined)
    ? JSON.stringify(identity)
    : undefined;
};

export const createLoomClient = (options: LoomClientOptions = {}): LoomClient => {
  const fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
  const baseUrl = options.baseUrl ?? '/';
  interface CacheEntry {
    readonly controller: AbortController;
    readonly promise: Promise<unknown>;
    consumers: number;
    settled: boolean;
  }
  const cache = new Map<string, CacheEntry>();

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
  const evictCached = (key: string): void => {
    const entry = cache.get(key);
    if (!entry) return;
    cache.delete(key);
    if (!entry.settled) entry.controller.abort();
  };
  const abortError = (): DOMException =>
    new DOMException('The request was aborted.', 'AbortError');
  const getCached = <T>(
    key: string,
    run: (signal: AbortSignal) => Promise<unknown>,
    parse: (value: unknown) => T,
    signal?: AbortSignal,
    reload = false,
  ): Promise<T> => {
    if (reload) evictCached(key);
    let entry = cache.get(key);
    if (!entry) {
      const controller = new AbortController();
      const promise = Promise.resolve().then(() => run(controller.signal));
      const createdEntry: CacheEntry = { controller, promise, consumers: 0, settled: false };
      entry = createdEntry;
      cache.set(key, createdEntry);
      void promise.then(
        () => {
          createdEntry.settled = true;
        },
        () => {
          createdEntry.settled = true;
          if (cache.get(key) === createdEntry) cache.delete(key);
        },
      );
    }
    const current = entry;
    current.consumers += 1;
    const value = new Promise<unknown>((resolve, reject) => {
      let released = false;
      const release = () => {
        if (released) return;
        released = true;
        current.consumers -= 1;
        if (current.consumers === 0 && !current.settled && cache.get(key) === current) {
          cache.delete(key);
          current.controller.abort();
        }
      };
      const onAbort = () => {
        signal?.removeEventListener('abort', onAbort);
        release();
        reject(signal?.reason ?? abortError());
      };
      if (signal?.aborted) {
        onAbort();
        return;
      }
      signal?.addEventListener('abort', onAbort, { once: true });
      void current.promise.then(
        (result) => {
          signal?.removeEventListener('abort', onAbort);
          release();
          resolve(result);
        },
        (error: unknown) => {
          signal?.removeEventListener('abort', onAbort);
          release();
          reject(error);
        },
      );
    });
    return value.then(parse);
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
    getCached(
      `explorers:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`,
      (requestSignal) => request(projectPath(args), { signal: requestSignal }),
      (value): ReadonlyArray<ExplorerSummary> => {
        if (!Array.isArray(value)) throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_LIST', message: 'Loom returned an invalid Explorer list.', retryable: false });
        return value.map((item): ExplorerSummary => {
          if (!isRecord(item) || typeof item.project !== 'string' || typeof item.explorerId !== 'string' || typeof item.title !== 'string' || typeof item.management !== 'string' || typeof item.updatedAt !== 'string') throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_LIST', message: 'Loom returned an invalid Explorer list.', retryable: false });
          return {
            project: item.project,
            explorerId: item.explorerId,
            title: item.title,
            management: item.management,
            ...(typeof item.activeRevisionId === 'string' ? { activeRevisionId: item.activeRevisionId } : {}),
            updatedAt: item.updatedAt,
          };
        });
      },
      signal,
    );
  const getBuilder = (args: ExplorerAuthoringStateArgs, queryOptions: { readonly signal?: AbortSignal; readonly reload?: boolean } = {}) =>
    getCached(
      `builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`,
      (signal) => request(authoringPath(args, '/builder'), { signal }),
      assertExplorerBuilderState,
      queryOptions.signal,
      queryOptions.reload,
    );
  const getCapability = (args: ExplorerAuthoringStateArgs, signal?: AbortSignal) =>
    getCached(
      `capability:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`,
      (requestSignal) => request(authoringPath(args, '/capability'), { signal: requestSignal }),
      (value) => explorerAuthoringCapabilitiesSchema.parse(value),
      signal,
    );
  const getExplorer = (args: ExplorerAuthoringStateArgs, signal?: AbortSignal) =>
    getCached(
      `viewer:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`,
      (requestSignal) => request(`/api/v1/projects/${encodedProject(args.project)}/explorers/${encodeURIComponent(args.explorerId)}`, { signal: requestSignal }),
      (value) => {
        let state: ReturnType<typeof assertExplorerStateV1>;
        try {
          state = assertExplorerStateV1(value);
        } catch {
          throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_STATE', message: 'Loom returned an invalid Explorer state.', retryable: false });
        }
        if (!state.runtime) throw new LoomRequestError({ status: 422, code: 'EXPLORER_RUNTIME_REQUIRED', message: 'The selected Explorer has no published runtime.', retryable: false });
        const runtimeIdentity = state.runtime.publication?.revisionId
          ?? state.runtime.publication?.generation
          ?? state.runtime.generation
          ?? state.runtime.schema?.digest;
        if (runtimeIdentity) return state.runtime;
        const responseIdentity = state.active.revisionId ?? state.updatedAt;
        if (!responseIdentity) {
          throw new LoomRequestError({ status: 502, code: 'INVALID_EXPLORER_STATE', message: 'Loom returned a published runtime without a session identity.', retryable: false });
        }
        return { ...state.runtime, responseIdentity };
      },
      signal,
    );
  const applyCommands = async (args: ApplyExplorerBuilderCommandsArgs, signal?: AbortSignal) => {
    const value = explorerBuilderCommandsResultSchema.parse(await request(durableAuthoringPath(args, '/commands'), withJson({
      commandId: args.commandId,
      semanticsVersion: EXPLORER_AUTHORING_SEMANTICS_VERSION,
      snapshotToken: args.snapshotToken,
      expectedDraftVersion: args.expectedDraftVersion,
      ...(args.expectedDraftDigest ? { expectedDraftDigest: args.expectedDraftDigest } : {}),
      commands: args.commands,
    }, signal, args.requestId)));
    evictCached(`builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`);
    return value;
  };
  const reconcile = (args: ReconcileExplorerBuilderArgs, signal?: AbortSignal) =>
    request(durableAuthoringPath(args, '/reconcile'), withJson({ snapshotToken: args.snapshotToken, draftVersion: args.draftVersion, draftDigest: args.draftDigest }, signal, args.requestId)).then(assertExplorerBuilderCompileResult);
  const suggestions = (args: ExplorerCandidateSuggestionsArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/suggestions'), withJson({ snapshotToken: args.snapshotToken, nodeId: args.nodeId, ...(args.query ? { query: args.query } : {}) }, signal, args.requestId)).then((value) => explorerBuilderSuggestionsResultSchema.parse(value));
  const preview = (args: PreviewExplorerBuilderArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/preview'), withJson({ receiptId: args.receiptId, outputId: args.outputId, ...(args.limit === undefined ? {} : { limit: args.limit }) }, signal, args.requestId)).then(assertExplorerBuilderPreviewResult);
  const populationMapping = (args: PopulationMappingArgs, signal?: AbortSignal) =>
    request(authoringPath(args, '/population-mapping'), withJson({ receiptId: args.receiptId, outputId: args.outputId, ...(args.cursor === undefined ? {} : { cursor: args.cursor }), ...(args.limit === undefined ? {} : { limit: args.limit }) }, signal)).then((value) => populationMappingResponseSchema.parse(value));
  const publish = async (args: PublishExplorerBuilderArgs, signal?: AbortSignal) => {
    const result = assertExplorerBuilderPublishResult(
      await request(
        durableAuthoringPath(args, '/publish'),
        withJson({ receiptId: args.receiptId }, signal, args.requestId),
      ),
    );
    evictCached(`viewer:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`);
    return result;
  };
  const createExplorer = async (args: CreateExplorerArgs, signal?: AbortSignal) => {
    const value = await request(durableProjectPath(args), withJson({ name: args.name, ...(args.title ? { title: args.title } : {}), ...(args.sourceExplorerId ? { sourceExplorerId: args.sourceExplorerId } : {}) }, signal, args.requestId));
    evictCached(`explorers:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`);
    return value as ExplorerSummary;
  };
  const createSelection = (args: CreateSelectionArgs, signal?: AbortSignal) =>
    request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}/selections${authResourcePathQuery(args.authResourcePath)}`, withJson({
      snapshotToken: args.snapshotToken,
      idempotencyKey: args.idempotencyKey,
      source: args.source,
      ...(args.exclusions ? { exclusions: args.exclusions } : {}),
    }, signal, args.requestId)).then((value) => selectionRevisionSchema.parse(value));
  const getSelection = (args: GetSelectionArgs, signal?: AbortSignal) => {
    const params = new URLSearchParams();
    if (args.cursor !== undefined) params.set('cursor', args.cursor);
    if (args.limit !== undefined) params.set('limit', String(args.limit));
    const query = params.size > 0 ? `?${params}` : '';
    return request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}/selections/${encodeURIComponent(args.selectionRevision)}${query}`, { signal })
      .then((value) => selectionPageSchema.parse(value));
  };
  const deleteExplorer = async (args: DeleteExplorerArgs, signal?: AbortSignal) => {
    await request(`${projectPath(args)}/${encodeURIComponent(args.explorerId)}`, { method: 'DELETE', signal, headers: args.requestId ? { 'X-Request-ID': args.requestId } : undefined });
    evictCached(`explorers:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}`);
    evictCached(`builder:${canonicalProject(args.project)}:${args.authResourcePath?.trim() ?? ''}:${args.explorerId}`);
    return null;
  };
  const fetchGraphQL = async (query: string, variables?: Readonly<Record<string, unknown>>, signal?: AbortSignal): Promise<unknown> => {
    const payload = await request('/graphql/graph', { method: 'POST', signal, body: JSON.stringify({ query, variables }) });
    if (!isRecord(payload)) throw new LoomRequestError({ status: 502, code: 'INVALID_GRAPHQL_RESPONSE', message: 'Loom returned an invalid GraphQL response.', retryable: false });
    if (Array.isArray(payload.errors) && payload.errors.length > 0) {
      const firstError = isRecord(payload.errors[0]) ? payload.errors[0] : {};
      const message = typeof firstError.message === 'string' ? firstError.message : 'Loom GraphQL request failed.';
      const extensions = isRecord(firstError.extensions) ? firstError.extensions : {};
      const code = typeof extensions.code === 'string' ? extensions.code : 'GRAPHQL_ERROR';
      throw new LoomRequestError({
        status: code === 'STALE_CURSOR' ? 409 : 'CUSTOM_ERROR',
        code,
        message,
        retryable: extensions.retryable === true,
        details: extensions,
      });
    }
    return payload.data;
  };
  const queryOutput = async (outputRequest: LoomOutputRequest, signal?: AbortSignal): Promise<LoomOutputResult> => {
    const prepared = outputQuery(outputRequest);
    const data = await fetchGraphQL(prepared.query, prepared.variables, signal);
    const parsed = graphQLOutputDataSchema.safeParse(data);
    if (!parsed.success || (prepared.variables.facetInput !== undefined && !parsed.data?.dataframeAggregations)) {
      throw new LoomRequestError({ status: 502, code: 'INVALID_OUTPUT_RESPONSE', message: 'Loom returned an invalid output response.', retryable: false });
    }
    const connection = parsed.data.dataframeRows;
    const columns = connection.columns;
    const pageInfo = connection.pageInfo;
    const facets = parsed.data.dataframeAggregations?.aggregations.map(normalizedFacet) ?? [];
    const materialization = connection.materialization;
    const endCursor = pageInfo.endCursor ?? undefined;
    return {
      columns,
      rows: shapeRows(connection.rows, columns),
      totalCount: connection.totalCount,
      pageInfo: { hasNextPage: pageInfo.hasNextPage, ...(endCursor ? { endCursor } : {}) },
      ...(materialization ? { materialization } : {}),
      facets,
    };
  };

  const exportOutput = async (outputRequest: LoomOutputRequest, signal?: AbortSignal): Promise<Blob> => {
    const rows: Array<Record<string, unknown>> = [];
    let columns: ReadonlyArray<string> = outputRequest.columns ?? [];
    let after: string | undefined;
    let pinnedMaterialization: string | undefined;
    const first = Math.max(outputRequest.first ?? 100, 1000);
    while (true) {
      if (signal?.aborted) throw new DOMException('The export was aborted.', 'AbortError');
      const page = await queryOutput({ ...outputRequest, first, after, facets: [] }, signal);
      if (page.materialization) {
        const identity = materializationIdentity(page.materialization);
        if (identity && pinnedMaterialization && identity !== pinnedMaterialization) {
          throw new LoomRequestError({
            status: 409,
            code: 'PUBLICATION_CONFLICT',
            message: 'The published output changed during export; restart from the first page.',
            retryable: false,
          });
        }
        if (identity) pinnedMaterialization = identity;
      }
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
    createSelection,
    getSelection,
    getBuilder,
    getCapability,
    getExplorer,
    applyCommands,
    reconcile,
    suggestions,
    preview,
    populationMapping,
    publish,
    createExplorer,
    deleteExplorer,
    fetchGraphQL,
    rows,
    queryOutput,
    exportOutput,
    invalidate: (scope = 'all') => {
      if (scope === 'all' || scope === 'explorers') [...cache.keys()].filter((key) => key.startsWith('explorers:')).forEach(evictCached);
      if (scope === 'all' || scope === 'builder') [...cache.keys()].filter((key) => key.startsWith('builder:')).forEach(evictCached);
      if (scope === 'all') [...cache.keys()].filter((key) => key.startsWith('viewer:')).forEach(evictCached);
    },
  };
};

export type { ExplorerBuilderCatalog };
