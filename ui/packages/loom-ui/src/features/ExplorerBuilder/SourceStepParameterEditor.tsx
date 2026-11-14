import { useRef, useState } from 'react';
import type { ImportedSourceParameters, SourceRecipeExpression } from '../../types';
import { moveExpressionItem, SourceExpressionEditor } from './SourceExpressionEditor';

const controlClass = 'min-w-0 rounded border border-slate-300 bg-white px-2 py-1 text-sm';
function Text({ label, value, onChange }: { label: string; value?: string; onChange(value: string): void }) {
  return <label className="grid gap-1 text-xs">{label}<input aria-label={label} className={controlClass} value={value ?? ''} onChange={(event) => onChange(event.currentTarget.value)} /></label>;
}
function OptionalText({ label, value, onChange }: { label: string; value?: string; onChange(value: string | undefined): void }) {
  return <div className="space-y-1"><label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={value !== undefined} onChange={(event) => onChange(event.currentTarget.checked ? '' : undefined)} />Override {label}</label>
    {value !== undefined ? <Text label={label} value={value} onChange={onChange} /> : <p className="text-xs text-slate-600">Use the recipe’s default prefix.</p>}</div>;
}
function Strings({ label, values, onChange }: { label: string; values: ReadonlyArray<string>; onChange(values: string[]): void }) {
  return <fieldset className="space-y-1"><legend className="text-xs font-semibold">{label}</legend>
    {values.map((value, index) => <div key={index} className="flex items-end gap-2">
      <Text label={`${label} ${index + 1}`} value={value} onChange={(next) => onChange(values.map((item, i) => i === index ? next : item))} />
      <button type="button" aria-label={`Remove ${label} ${index + 1}`} onClick={() => onChange(values.filter((_, i) => i !== index))}>Remove</button>
    </div>)}
    <button type="button" className="text-xs text-blue-800 underline" onClick={() => onChange([...values, ''])}>Add {label}</button>
  </fieldset>;
}
function Expressions({ label, values, onChange }: { label: string; values: ReadonlyArray<SourceRecipeExpression>; onChange(values: SourceRecipeExpression[]): void }) {
  return <fieldset className="space-y-2"><legend className="text-xs font-semibold">{label} · tried in this order</legend>
    {values.map((value, index) => <div key={index}>
      <SourceExpressionEditor label={`${label} ${index + 1}`} value={value} onChange={(next) => onChange(values.map((item, i) => i === index ? next : item))} />
      <div className="flex gap-2 text-xs">
        <button type="button" disabled={index === 0} onClick={() => onChange(moveExpressionItem(values, index, -1))} aria-label={`Move ${label} ${index + 1} earlier`}>Move earlier</button>
        <button type="button" disabled={index === values.length - 1} onClick={() => onChange(moveExpressionItem(values, index, 1))} aria-label={`Move ${label} ${index + 1} later`}>Move later</button>
        <button type="button" onClick={() => onChange(values.filter((_, i) => i !== index))}>Remove {label} {index + 1}</button>
      </div>
    </div>)}
    <button type="button" className="text-xs text-blue-800 underline" onClick={() => onChange([...values, { select: 'root.id' }])}>Add {label}</button>
  </fieldset>;
}
function OptionalExpression({ label, value, onChange }: { label: string; value?: SourceRecipeExpression; onChange(value: SourceRecipeExpression | undefined): void }) {
  return <div className="space-y-1"><label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={value !== undefined} onChange={(event) => onChange(event.currentTarget.checked ? { select: 'root.id' } : undefined)} />Use {label}</label>
    {value ? <SourceExpressionEditor label={label} value={value} onChange={onChange} /> : null}</div>;
}

/** All edits are the canonical typed parameters of one pinned source step. */
export function SourceStepParameterEditor({ parameters, onChange, disabled = false, selectedFieldId, fieldFocusRequest = 0 }: {
  readonly parameters: ImportedSourceParameters;
  readonly onChange: (parameters: ImportedSourceParameters) => void;
  readonly disabled?: boolean;
  readonly selectedFieldId?: string;
  readonly fieldFocusRequest?: number;
}) {
  const [fieldSearch, setFieldSearch] = useState('');
  const [activeFieldId, setActiveFieldId] = useState<string>();
  const focusedField = useRef<string | undefined>(undefined);
  const fieldFocusGeneration = useRef(0);
  const [aliasError, setAliasError] = useState<string>();
  const fieldFocusKey = JSON.stringify([selectedFieldId, fieldFocusRequest]);
  return <fieldset disabled={disabled} aria-label="Source step parameters" className="min-w-0 space-y-3 border-t border-slate-200 py-2">
    <legend className="text-sm font-semibold">Parameters</legend>
    {'output' in parameters && parameters.output ? <>
      <Text label="Row grain" value={parameters.output.rowGrain} onChange={(rowGrain) => onChange({ ...parameters, output: { ...parameters.output!, rowGrain } })} />
      <label className="grid gap-1 text-xs">Column collision policy<select aria-label="Column collision policy" className={controlClass} value={parameters.output.collisionPolicy ?? ''} onChange={(event) => onChange({ ...parameters, output: { ...parameters.output!, collisionPolicy: (event.currentTarget.value || undefined) as NonNullable<ImportedSourceParameters['output']>['collisionPolicy'] } })}><option value="">Recipe default</option><option value="error">Report an error</option><option value="overwrite">Overwrite earlier column</option><option value="coalesce">Coalesce values</option></select></label>
    </> : null}
    {'fieldGroup' in parameters && parameters.fieldGroup ? (() => {
      const group = parameters.fieldGroup;
      const update = (index: number, patch: Partial<typeof group.fields[number]>) => onChange({ ...parameters, fieldGroup: { fields: group.fields.map((field, i) => i === index ? { ...field, ...patch } : field) } });
      const matching = group.fields.map((field, index) => ({ field, index })).filter(({ field }) => field.fieldId === selectedFieldId || field.fieldId === activeFieldId || `${field.name} ${field.label ?? ''}`.toLocaleLowerCase().includes(fieldSearch.toLocaleLowerCase()));
      return <div className="space-y-2">
        <Text label="Find projected field" value={fieldSearch} onChange={(value) => { setActiveFieldId(undefined); setFieldSearch(value); }} />
        <p className="text-xs">{group.fields.length} fields in recipe order. Field identity stays fixed when its name or expression changes.</p>
        <ol aria-label="Projected fields" className="max-h-96 space-y-2 overflow-auto">
          {matching.map(({ field, index }) => <li key={field.fieldId} data-field-id={field.fieldId} onFocusCapture={() => setActiveFieldId(field.fieldId)} ref={(node) => {
            if (!node || field.fieldId !== selectedFieldId || focusedField.current === fieldFocusKey) return;
            focusedField.current = fieldFocusKey;
            const generation = ++fieldFocusGeneration.current;
            const initiator = document.activeElement;
            window.requestAnimationFrame(() => {
              if (!node.isConnected || generation !== fieldFocusGeneration.current || (document.activeElement !== initiator && document.activeElement !== document.body)) return;
              node.scrollIntoView?.({ block: 'nearest' });
              node.querySelector<HTMLInputElement>('input[aria-label$=" name"]')?.focus();
            });
          }}>
            <details open={field.fieldId === selectedFieldId ? true : undefined} className="rounded border border-slate-200 p-2"><summary className="cursor-pointer text-sm">{index + 1}. {field.label || field.name}{!field.enabled ? ' · disabled' : ''}</summary>
              <div className="space-y-2 pt-2">
                <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={field.enabled} onChange={(event) => update(index, { enabled: event.currentTarget.checked })} />Enable {field.name}</label>
                <Text label={`Field ${index + 1} name`} value={field.name} onChange={(name) => update(index, { name })} />
                <Text label={`Field ${index + 1} label`} value={field.label} onChange={(label) => update(index, { label })} />
                <SourceExpressionEditor label={`Field ${index + 1} expression`} value={field.expr} onChange={(expr) => update(index, { expr })} />
                <Expressions label={`Field ${index + 1} fallback`} values={field.fallbacks ?? []} onChange={(fallbacks) => update(index, { fallbacks })} />
                <label className="grid gap-1 text-xs">Field value policy<select aria-label={`Field ${index + 1} value policy`} className={controlClass} value={field.valueMode ?? ''} onChange={(event) => update(index, { valueMode: (event.currentTarget.value || undefined) as typeof field.valueMode })}>
                  <option value="">Recipe default</option>{(['AUTO', 'FIRST', 'ALL', 'DISTINCT'] as const).map((mode) => <option key={mode} value={mode}>{{AUTO:'Automatic',FIRST:'First value',ALL:'All values as a list',DISTINCT:'Distinct values as a list'}[mode]}</option>)}
                </select></label>
                <details className="text-xs"><summary>Field identity</summary><code>{field.fieldId}</code></details>
              </div>
            </details>
          </li>)}
        </ol>
        {!matching.length ? <p className="text-xs">No matching fields.</p> : null}
      </div>;
    })() : null}
    {'catalog' in parameters && parameters.catalog ? (() => { const p = parameters.catalog; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, catalog: { ...p, ...patch } }); return <>
      <Text label="Projection name" value={p.name} onChange={(name) => update({ name })} />
      <Strings label="Included path" values={p.includePaths ?? []} onChange={(includePaths) => update({ includePaths })} />
      <Strings label="Excluded path" values={p.excludePaths ?? []} onChange={(excludePaths) => update({ excludePaths })} />
      <Strings label="Field kind" values={p.kinds ?? []} onChange={(kinds) => update({ kinds })} />
      <label className="grid gap-1 text-xs">Column naming<select aria-label="Column naming" className={controlClass} value={p.naming ?? ''} onChange={(event) => update({ naming: (event.currentTarget.value || undefined) as typeof p.naming })}><option value="">Recipe default</option><option value="PATH">Full path</option><option value="PATH_SUFFIX">Path suffix</option></select></label>
    </>; })() : null}
    {'dynamic' in parameters && parameters.dynamic ? (() => { const p = parameters.dynamic; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, dynamic: { ...p, ...patch } }); return <>
      <Text label="Dynamic column name" value={p.name} onChange={(name) => update({ name })} />
      <OptionalText label="Dynamic column prefix" value={p.columnPrefix} onChange={(columnPrefix) => update({ columnPrefix })} />
      <SourceExpressionEditor label="Dynamic item source" value={p.source} onChange={(source) => update({ source })} />
      <OptionalExpression label="Dynamic key" value={p.key} onChange={(key) => update({ key })} />
      <OptionalExpression label="Dynamic value" value={p.value} onChange={(value) => update({ value })} />
    </>; })() : null}
    {'extension' in parameters && parameters.extension ? (() => { const p = parameters.extension; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, extension: { ...p, ...patch } }); return <>
      <Text label="Extension column name" value={p.name} onChange={(name) => update({ name })} />
      <OptionalText label="Extension column prefix" value={p.columnPrefix} onChange={(columnPrefix) => update({ columnPrefix })} />
      <SourceExpressionEditor label="Extension item source" value={p.source} onChange={(source) => update({ source })} />
    </>; })() : null}
    {'pivot' in parameters && parameters.pivot ? (() => { const p = parameters.pivot; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, pivot: { ...p, ...patch } }); return <>
      <Text label="Pivot name" value={p.name} onChange={(name) => update({ name })} />
      <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={!!p.discovery} onChange={(event) => update({ discovery: event.currentTarget.checked ? { family: '', path: '' } : undefined })} />Discover pivot columns</label>
      {p.discovery ? <><Text label="Pivot discovery family" value={p.discovery.family} onChange={(family) => update({ discovery: { ...p.discovery, family } })} /><Text label="Pivot discovery path" value={p.discovery.path} onChange={(path) => update({ discovery: { ...p.discovery, path } })} /></> : null}
      <OptionalExpression label="Pivot column expression" value={p.columnExpr} onChange={(columnExpr) => update({ columnExpr })} />
      <OptionalExpression label="Pivot value expression" value={p.valueExpr} onChange={(valueExpr) => update({ valueExpr })} />
      <Expressions label="Pivot value fallback" values={p.valueFallbacks ?? []} onChange={(valueFallbacks) => update({ valueFallbacks })} />
      <OptionalExpression label="Pivot item source" value={p.itemSource} onChange={(itemSource) => update({ itemSource })} />
      <Text label="Pivot item record type" value={p.itemResourceType} onChange={(itemResourceType) => update({ itemResourceType })} />
      <Strings label="Explicit pivot column" values={p.columns ?? []} onChange={(columns) => update({ columns })} />
      <fieldset className="space-y-2"><legend className="text-xs font-semibold">Pivot column aliases</legend>
        {Object.entries(p.columnAliases ?? {}).map(([key, value], index) => <div key={index} className="grid gap-2 sm:grid-cols-2">
          <Text label={`Alias ${index + 1} source column`} value={key} onChange={(next) => { if (next !== key && Object.hasOwn(p.columnAliases ?? {}, next)) { setAliasError('Each alias must identify a different source column.'); return; } setAliasError(undefined); update({ columnAliases: Object.fromEntries(Object.entries(p.columnAliases ?? {}).map(([k, v]) => [k === key ? next : k, v])) }); }} />
          <Text label={`Alias ${index + 1} output name`} value={value} onChange={(next) => update({ columnAliases: { ...p.columnAliases, [key]: next } })} />
          <button type="button" onClick={() => update({ columnAliases: Object.fromEntries(Object.entries(p.columnAliases ?? {}).filter(([k]) => k !== key)) })}>Remove alias {index + 1}</button>
        </div>)}
        {aliasError ? <p role="alert" className="text-xs text-red-800">{aliasError}</p> : null}
        <button type="button" className="text-xs text-blue-800 underline" onClick={() => { let n = 1; while (Object.hasOwn(p.columnAliases ?? {}, `column_${n}`)) n++; update({ columnAliases: { ...p.columnAliases, [`column_${n}`]: '' } }); }}>Add pivot alias</button>
      </fieldset>
    </>; })() : null}
    {'expansion' in parameters && parameters.expansion ? (() => { const p = parameters.expansion; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, expansion: { ...p, ...patch } }); return <>
      <SourceExpressionEditor label="Expanded collection" value={p.from} onChange={(from) => update({ from })} />
      <Text label="Expanded item alias" value={p.as} onChange={(as) => update({ as })} />
      <Text label="Ordinality column" value={p.ordinality} onChange={(ordinality) => update({ ordinality })} />
      <label className="grid gap-1 text-xs">Empty collection<select aria-label="Empty collection policy" className={controlClass} value={p.emptyPolicy ?? ''} onChange={(event) => update({ emptyPolicy: (event.currentTarget.value || undefined) as typeof p.emptyPolicy })}>
        <option value="">Recipe default</option><option value="ERROR">Report an error</option><option value="EXCLUDE">Exclude the parent row</option><option value="PRESERVE_PARENT">Keep the parent row</option>
      </select></label>
    </>; })() : null}
    {'identity' in parameters && parameters.identity ? <>
      <Text label="Identity column name" value={parameters.identity.name} onChange={(name) => onChange({ ...parameters, identity: { ...parameters.identity!, name } })} />
      <SourceExpressionEditor label="Row identity expression" value={parameters.identity.expr} onChange={(expr) => onChange({ ...parameters, identity: { ...parameters.identity!, expr } })} />
    </> : null}
    {'traversal' in parameters && parameters.traversal ? (() => { const p = parameters.traversal; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, traversal: { ...p, ...patch } }); return <>
      <Text label="Traversal name" value={p.name} onChange={(name) => update({ name })} />
      <Text label="Related record type" value={p.toResourceType} onChange={(toResourceType) => update({ toResourceType })} />
      <Text label="Related record alias" value={p.alias} onChange={(alias) => update({ alias })} />
      <OptionalExpression label="Traversal source" value={p.from} onChange={(from) => update({ from })} />
      <p className="text-xs text-slate-600">Preview validates this relationship against the current catalog and checks its dependent source steps.</p>
    </>; })() : null}
  </fieldset>;
}
