/** Framework-neutral Explorer contract for Loom's versioned flat API. */
export type DataframeSelector = {
  recipe: string;
  translationVersion: string;
  output: string;
};

export type FederationAvailability = "AVAILABLE" | "DEGRADED" | "UNAVAILABLE";
export type ProjectState = "CURRENT" | "STALE" | "BUILDING" | "FAILED" | "MISSING" | "EXCLUDED";

export type DataframeColumn = {
  name: string;
  logicalType: string;
  nullable: boolean;
  repeated: boolean;
  filterable: boolean;
  sortable: boolean;
  aggregatable: boolean;
};

export type ProjectStatus = {
  project: string;
  state: ProjectState;
  generation?: string | null;
  executionId?: string | null;
  createdAt?: string | null;
  updatedAt?: string | null;
  errorCode?: string | null;
  retryable: boolean;
};

export type DatasetMetadata = {
  id: string;
  name: string;
  revision: string;
  selector: DataframeSelector;
  activeContractVersion: string;
  availability: FederationAvailability;
  completeness: number;
  includedProjectCount: number;
  expectedProjectCount: number;
  columns: DataframeColumn[];
  projectStatuses: ProjectStatus[];
};

// Project-specific columns are intentionally sparse, so row values remain
// open and missing fields are normal rather than client schema failures.
export type DataframeRow = Record<string, unknown>;

export type PageInfo = { hasNextPage: boolean; endCursor?: string | null };
export type ExplorerRows = {
  materialization: DatasetMetadata;
  columns: string[];
  rows: DataframeRow[];
  totalCount?: number | null;
  pageInfo: PageInfo;
};

export type ExplorerRowsResponse = { data?: { dataframeRows: ExplorerRows }; errors?: LoomGraphQLError[] };
export type ExplorerDatasetResponse = { data?: { dataframeDataset: DatasetMetadata | null }; errors?: LoomGraphQLError[] };
export type ExplorerDatasetsResponse = { data?: { dataframeDatasets: DatasetMetadata[] }; errors?: LoomGraphQLError[] };

export type LoomErrorExtensions = {
  code: string;
  retryable: boolean;
  requestId?: string;
  fieldPath?: string[];
  details?: Record<string, unknown>;
};
export type LoomGraphQLError = { message: string; extensions: LoomErrorExtensions };

export type ExactDatasetInput = { selector: DataframeSelector; dataType?: never };
export type LegacyDatasetInput = { dataType: string; selector?: never };
export type DatasetIdentityInput = ExactDatasetInput | LegacyDatasetInput;

export type StatusCounts = Record<ProjectState, number>;
export const emptyStatusCounts = (): StatusCounts => ({
  CURRENT: 0, STALE: 0, BUILDING: 0, FAILED: 0, MISSING: 0, EXCLUDED: 0,
});

export function statusCounts(statuses: ProjectStatus[]): StatusCounts {
  return statuses.reduce((counts, status) => {
    counts[status.state] += 1;
    return counts;
  }, emptyStatusCounts());
}

