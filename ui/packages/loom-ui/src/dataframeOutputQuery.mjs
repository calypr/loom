export const DATAFRAME_OUTPUT_QUERY_VERSION = 'v1';

const DATAFRAME_ROWS_SELECTION = 'materialization { id name revision projectId datasetGeneration state rowCount selector { recipe translationVersion output } } columns rows totalCount pageInfo { hasNextPage endCursor }';

export const dataframeOutputQuery = (operationName, includeAggregations = false) => {
  const variables = includeAggregations
    ? '$input: DataframeRowsInput!, $facetInput: DataframeAggregationsInput!'
    : '$input: DataframeRowsInput!';
  const aggregations = includeAggregations
    ? ' dataframeAggregations(input: $facetInput) { aggregations }'
    : '';
  return `query ${operationName}(${variables}) { dataframeRows(input: $input) { ${DATAFRAME_ROWS_SELECTION} }${aggregations} }`;
};
