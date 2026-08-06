import type { DataframeSelector, DatasetIdentityInput } from "./types";

export const DATASET_FIELDS = `
  id name revision
  selector { recipe translationVersion output }
  activeContractVersion availability completeness
  includedProjectCount expectedProjectCount
  columns { name logicalType nullable repeated filterable sortable aggregatable }
  projectStatuses {
    project state generation executionId createdAt updatedAt errorCode retryable
  }
`;

export const EXPLORER_DATASET = `
  query ExplorerDataset($input: DataframeDatasetInput!) {
    dataframeDataset(input: $input) { ${DATASET_FIELDS} }
  }
`;

export const EXPLORER_DATASETS = `
  query ExplorerDatasets {
    dataframeDatasets { ${DATASET_FIELDS} }
  }
`;

export const EXPLORER_ROWS = `
  query ExplorerRows($input: DataframeRowsInput!) {
    dataframeRows(input: $input) {
      materialization { ${DATASET_FIELDS} }
      columns rows totalCount
      pageInfo { hasNextPage endCursor }
    }
  }
`;

export function exactRowsVariables(selector: DataframeSelector, first = 100) {
  return { input: { selector, first } };
}

export function legacyRowsVariables(dataType: string, first = 100) {
  return { input: { dataType, first } };
}

export function datasetVariables(identity: DatasetIdentityInput) {
  return { input: identity };
}

