export const findSavedAuthoringColumn = (columns, columnId) =>
  columns.find(column => column.columnId === columnId);

export const savedAuthoringPreviewSchema = columns =>
  columns.map(({ column, label }) => ({ column, label }));
