export const sourceColumnSchema = column => ({
  columnId: column.columnId,
  column: column.column,
  label: column.label,
  ...(column.logicalType === undefined ? {} : { logicalType: column.logicalType }),
});

export const finalOutputSchema = output => ({
  id: output.id,
  name: output.name,
  label: output.label,
  ...(output.type === undefined ? {} : { type: output.type }),
});
