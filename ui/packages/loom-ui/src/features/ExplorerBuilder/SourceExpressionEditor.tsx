import { sourceRecipeEditFunctions, type SourceRecipeExpression } from '../../types';

export const sourceExpressionFunctions = sourceRecipeEditFunctions;
export const moveExpressionItem = <T,>(items: ReadonlyArray<T>, index: number, delta: -1 | 1): T[] => {
  const next = [...items];
  const destination = index + delta;
  if (index < 0 || index >= items.length || destination < 0 || destination >= items.length) return next;
  [next[index], next[destination]] = [next[destination], next[index]];
  return next;
};
const inputClass = 'min-w-0 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs';
const argumentLabel = (call: string, index: number) => call === 'uuid5' && index === 0 ? 'Namespace'
  : call === 'join' && index === 1 ? 'Separator' : `Argument ${index + 1}`;

/** Edits the existing recipe.Expression tree. Array order is execution order. */
export function SourceExpressionEditor({ value, onChange, label, disabled = false, pathChoices }: {
  readonly value: SourceRecipeExpression;
  readonly onChange: (value: SourceRecipeExpression) => void;
  readonly label: string;
  readonly disabled?: boolean;
  readonly pathChoices?: ReadonlyArray<string>;
}) {
  const unsupported = value.document ? 'a document reference'
    : value.call && !sourceExpressionFunctions.some((name) => name === value.call) ? `the function ${value.call}`
    : Object.hasOwn(value, 'literal') && value.literal !== null && !['string', 'number', 'boolean'].includes(typeof value.literal) ? 'a complex literal' : undefined;
  if (unsupported) return <fieldset aria-label={label} className="rounded-md bg-amber-50 p-2 text-xs"><legend className="px-1 text-[11px] font-semibold text-amber-900">{label}</legend><p>This saved expression uses {unsupported}. It is preserved unchanged; structured editing supports source paths, primitive literals, and the supported recipe functions.</p></fieldset>;
  const kind = typeof value.select === 'string' ? 'select' : typeof value.call === 'string' ? 'call' : 'literal';
  return <fieldset aria-label={label} disabled={disabled} className="min-w-0 space-y-1.5">
    <legend className="text-[11px] font-semibold uppercase tracking-wide text-slate-500">{label}</legend>
    <label className="flex flex-wrap items-center gap-1.5 text-xs">Expression type
      <select aria-label={`${label} type`} className={inputClass} value={kind} onChange={(event) => {
        const next = event.currentTarget.value;
        onChange(next === 'select' ? { select: 'root.id' } : next === 'call' ? { call: 'first', args: [{ select: 'root.id' }] } : { literal: '' });
      }}><option value="select">Select a source path</option><option value="literal">Literal value</option><option value="call">Function</option></select>
    </label>
    {typeof value.select === 'string' ? <label className="grid gap-1 text-xs">Source path
      {pathChoices !== undefined ? <select aria-label={`${label} source path`} className={inputClass} value={value.select} onChange={(event) => onChange({ select: event.currentTarget.value })}>
        {!pathChoices.includes(value.select) ? <option value={value.select}>{value.select || 'Choose a source path'}</option> : null}
        {pathChoices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}
      </select> : <input aria-label={`${label} source path`} className={inputClass} value={value.select} onChange={(event) => onChange({ select: event.currentTarget.value })} />}
    </label> : null}
    {Object.hasOwn(value, 'literal') ? <div className="space-y-1.5">
      <label className="flex items-center gap-2 text-xs">Literal type<select aria-label={`${label} literal type`} className={inputClass} value={value.literal === null ? 'null' : typeof value.literal}
        onChange={(event) => onChange({ literal: event.currentTarget.value === 'null' ? null : event.currentTarget.value === 'boolean' ? false : event.currentTarget.value === 'number' ? 0 : '' })}>
        <option value="string">Text</option><option value="number">Number</option><option value="boolean">Boolean</option><option value="null">Null</option>
      </select></label>
      {typeof value.literal === 'boolean' ? <label className="flex items-center gap-1.5 text-xs"><input type="checkbox" aria-label={`${label} boolean`} checked={value.literal} onChange={(event) => onChange({ literal: event.currentTarget.checked })} />True</label>
        : value.literal === null ? <p className="text-xs">Explicit null</p>
        : <label className="grid gap-1 text-xs">Literal value<input aria-label={`${label} literal value`} className={inputClass} type={typeof value.literal === 'number' ? 'number' : 'text'} step="any" value={typeof value.literal === 'string' || typeof value.literal === 'number' ? value.literal : ''}
          onChange={(event) => { const raw = event.currentTarget.value; if (typeof value.literal === 'number') { if (raw.trim() !== '' && Number.isFinite(Number(raw))) onChange({ literal: Number(raw) }); } else onChange({ literal: raw }); }} /></label>}
    </div> : null}
    {typeof value.call === 'string' ? <div className="space-y-1.5">
      <label className="flex items-center gap-1.5 text-xs">Function<select aria-label={`${label} function`} className={inputClass} value={value.call} onChange={(event) => onChange({ ...value, call: event.currentTarget.value })}>
        {!sourceExpressionFunctions.some((name) => name === value.call) ? <option value={value.call}>{value.call} (existing function)</option> : null}
        {sourceExpressionFunctions.map((name) => <option key={name} value={name}>{name}</option>)}
      </select></label>
      <ol aria-label={`${label} ordered arguments`} className="space-y-1.5 border-l border-slate-200 pl-2">
        {(value.args ?? []).map((argument, index, args) => <li key={index}>
          <SourceExpressionEditor label={`${label} · ${argumentLabel(value.call ?? '', index)}`} value={argument} onChange={(next) => onChange({ ...value, args: args.map((item, i) => i === index ? next : item) })} disabled={disabled} />
          <div className="mt-0.5 flex flex-wrap gap-1 text-[11px]">
            <button type="button" className="rounded px-1.5 py-0.5 text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-800 disabled:opacity-40" disabled={disabled || index === 0} onClick={() => onChange({ ...value, args: moveExpressionItem(args, index, -1) })} aria-label={`${label} move argument ${index + 1} earlier`}>Move earlier</button>
            <button type="button" className="rounded px-1.5 py-0.5 text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-800 disabled:opacity-40" disabled={disabled || index === args.length - 1} onClick={() => onChange({ ...value, args: moveExpressionItem(args, index, 1) })} aria-label={`${label} move argument ${index + 1} later`}>Move later</button>
            <button type="button" className="rounded px-1.5 py-0.5 text-slate-500 transition-colors hover:bg-red-50 hover:text-red-700 disabled:opacity-40" disabled={disabled} onClick={() => onChange({ ...value, args: args.filter((_, i) => i !== index) })} aria-label={`${label} remove argument ${index + 1}`}>Remove argument</button>
          </div>
        </li>)}
      </ol>
      <button type="button" className="text-[11px] font-medium text-blue-700 hover:text-blue-900 hover:underline" onClick={() => onChange({ ...value, args: [...(value.args ?? []), { literal: '' }] })}>Add argument to {label}</button>
    </div> : null}
  </fieldset>;
}
