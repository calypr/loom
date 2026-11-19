import type { ReshapeColumn } from './reshapeForms';

export const ListColumnExpandChoices = (props: {
  readonly columns: ReadonlyArray<ReshapeColumn>;
  readonly supported: boolean;
  readonly disabled: boolean;
  readonly onExpand?: (columnId: string) => void;
  readonly inlineGrainAction?: boolean;
}) => {
  if (props.columns.length === 0) return null;
  const choices = props.supported && props.onExpand ? (
    <ul className="m-0 mt-2 grid list-none gap-1 p-0 sm:grid-cols-2">
      {props.columns.map((column) => (
        <li key={column.id}>
          <button type="button" aria-label={`Expand ${column.label}`} disabled={props.disabled}
            onClick={() => props.onExpand?.(column.id)}
            className="w-full rounded px-2 py-1 text-left text-xs text-slate-700 hover:bg-blue-50 disabled:opacity-50">
            Split {column.label} into rows
          </button>
        </li>
      ))}
    </ul>
  ) : <p className="m-0 mt-2 text-xs text-slate-600">These repeated fields can’t be split into rows at this stage.</p>;
  if (props.inlineGrainAction) {
    return (
      <section data-testid="construction-reshape-list-column-settings" aria-label="Repeated field actions" className="rounded border border-slate-200 bg-slate-50 px-2 py-2">
        <h3 className="m-0 text-xs font-semibold text-slate-800">Repeated fields</h3>
        <p className="mb-0 mt-1 text-xs text-slate-600">
          Splitting a repeated field changes the input grain: each list item becomes its own row and values from other fields repeat.
        </p>
        {choices}
      </section>
    );
  }
  return (
    <details open data-testid="construction-reshape-list-column-settings" className="border-t border-slate-200 pt-2">
      <summary className="cursor-pointer text-xs font-medium text-blue-800">List column settings</summary>
      {choices}
    </details>
  );
};
