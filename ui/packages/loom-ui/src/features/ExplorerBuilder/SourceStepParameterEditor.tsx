import { useRef, useState } from 'react';
import type { ImportedSourceParameters, ProjectedField, SourceRecipeExpression } from '../../types';
import { moveExpressionItem, SourceExpressionEditor } from './SourceExpressionEditor';

const controlClass = 'min-w-0 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs';
type CatalogOutputOverride = { fieldPath: string; enabled: boolean; name?: string };
type PivotOutputOverride = { key: string; enabled: boolean; name?: string };
type EditorSourceParameters = Omit<ImportedSourceParameters, 'catalog' | 'pivot'> & {
  catalog?: NonNullable<ImportedSourceParameters['catalog']> & { outputs?: CatalogOutputOverride[] };
  pivot?: NonNullable<ImportedSourceParameters['pivot']> & { outputs?: PivotOutputOverride[] };
};
export type CatalogOutputChoice = { outputKey: string; columnId?: string; defaultName: string; enabled: boolean };
function Text({ label, value, onChange, placeholder }: { label: string; value?: string; onChange(value: string): void; placeholder?: string }) {
  return <label className="grid gap-1 text-xs">{label}<input aria-label={label} placeholder={placeholder} className={controlClass} value={value ?? ''} onChange={(event) => onChange(event.currentTarget.value)} /></label>;
}
function ColumnLabelEditor({ field, disabled, onRename }: { field: ProjectedField; disabled?: boolean; onRename: (columnId: string, label: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState(field.label);
  if (!field.columnId) return null;
  if (!editing) return <button type="button" disabled={disabled} className="shrink-0 px-1.5 py-0.5 text-xs text-slate-600" aria-label={`Rename ${field.label}`}
    onClick={(event) => { event.preventDefault(); event.stopPropagation(); setLabel(field.label); setEditing(true); }}>Rename</button>;
  return <span className="flex min-w-0 items-center gap-1" onClick={(event) => event.stopPropagation()}>
    <input aria-label={`Display label for ${field.label}`} className={controlClass} value={label} disabled={disabled} onChange={(event) => setLabel(event.currentTarget.value)}
      onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); event.stopPropagation(); if (!disabled && label.trim() && field.columnId) { onRename(field.columnId, label.trim()); setEditing(false); } } }} />
    <button type="button" disabled={disabled || !label.trim()} onClick={() => { if (field.columnId) onRename(field.columnId, label.trim()); setEditing(false); }}>Save label</button>
    <button type="button" onClick={() => setEditing(false)}>Cancel rename</button>
  </span>;
}
function OptionalText({ label, value, onChange }: { label: string; value?: string; onChange(value: string | undefined): void }) {
  return <div className="space-y-1"><label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={value !== undefined} onChange={(event) => onChange(event.currentTarget.checked ? '' : undefined)} />Override {label}</label>
    {value !== undefined ? <Text label={label} value={value} onChange={onChange} /> : null}</div>;
}
function Strings({ label, values, onChange, options }: { label: string; values: ReadonlyArray<string>; onChange(values: string[]): void; options?: ReadonlyArray<string> }) {
  const addValue = options?.length ? options.find((option) => !values.includes(option)) ?? '' : '';
  const hasOptions = options !== undefined;
  return <fieldset className="space-y-1"><legend className="text-xs font-semibold">{label}</legend>
    {values.map((value, index) => <div key={index} className="flex items-end gap-2">
      {hasOptions ? <label className="grid min-w-0 flex-1 gap-1 text-xs">{label} {index + 1}
        <select aria-label={`${label} ${index + 1}`} className={controlClass} value={value} onChange={(event) => onChange(values.map((item, i) => i === index ? event.currentTarget.value : item))}>
          {!options!.includes(value) ? <option value={value}>{value || `Choose ${label.toLowerCase()}`}</option> : null}
          {(options ?? []).map((option) => <option key={option} value={option}>{option}</option>)}
        </select>
      </label> : <Text label={`${label} ${index + 1}`} value={value} onChange={(next) => onChange(values.map((item, i) => i === index ? next : item))} />}
      <button type="button" aria-label={`Remove ${label} ${index + 1}`} onClick={() => onChange(values.filter((_, i) => i !== index))}>Remove</button>
    </div>)}
    <button type="button" className="text-xs text-blue-800 underline" onClick={() => onChange([...values, addValue])}>Add {label}</button>
  </fieldset>;
}
function OptionalExpression({ label, value, onChange, pathChoices }: { label: string; value?: SourceRecipeExpression; onChange(value: SourceRecipeExpression | undefined): void; pathChoices?: ReadonlyArray<string> }) {
  return <div className="space-y-1"><label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={value !== undefined} onChange={(event) => onChange(event.currentTarget.checked ? { select: 'root.id' } : undefined)} />Use {label}</label>
    {value ? <SourceExpressionEditor label={label} value={value} onChange={onChange} pathChoices={pathChoices} /> : null}</div>;
}

/** All edits are the canonical typed parameters of one pinned source step. */
export function SourceStepParameterEditor({ parameters, onChange, disabled = false, selectedFieldId, fieldFocusRequest = 0, catalogOutputs = [], pivotOutputs = [], selectedOutputKey, outputFocusRequest = 0, outputRevision = 'unversioned', resourceTypeChoices, sourcePathChoices, fieldPathChoices, fieldKindChoices, projectedFields, sourceStepId, onRemoveProjectedField, onEditProjectedField, onRenameProjectedField, editableProjectedFieldIds, projectedFieldRemovalDisabledReason, view = 'all' }: {
  readonly view?: 'all' | 'fields' | 'settings';
  readonly parameters: EditorSourceParameters;
  readonly onChange: (parameters: ImportedSourceParameters) => void;
  readonly disabled?: boolean;
  readonly selectedFieldId?: string;
  readonly fieldFocusRequest?: number;
  readonly catalogOutputs?: ReadonlyArray<CatalogOutputChoice>;
  readonly pivotOutputs?: ReadonlyArray<CatalogOutputChoice>;
  readonly selectedOutputKey?: string;
  readonly outputFocusRequest?: number;
  readonly outputRevision?: string;
  readonly resourceTypeChoices?: ReadonlyArray<string>;
  readonly sourcePathChoices?: ReadonlyArray<string>;
  readonly fieldPathChoices?: ReadonlyArray<string>;
  readonly fieldKindChoices?: ReadonlyArray<string>;
  readonly projectedFields?: ReadonlyArray<ProjectedField>;
  readonly sourceStepId?: string;
  readonly onRemoveProjectedField?: (columnId: string) => void;
  readonly onEditProjectedField?: (field: ProjectedField) => void;
  readonly onRenameProjectedField?: (columnId: string, label: string) => void;
  readonly editableProjectedFieldIds?: ReadonlySet<string>;
  readonly projectedFieldRemovalDisabledReason?: string;
}) {
  const focusedField = useRef<string | undefined>(undefined);
  const fieldFocusGeneration = useRef(0);
  const focusedOutput = useRef<string | undefined>(undefined);
  const outputFocusGeneration = useRef(0);
  const [pendingGeneratedNames, setPendingGeneratedNames] = useState<ReadonlyMap<string, string>>(() => new Map());
  const markGeneratedNamePending = (key: string) => setPendingGeneratedNames((current) => new Map(current).set(key, outputRevision));
  const clearGeneratedNamePending = (key: string) => setPendingGeneratedNames((current) => {
    if (!current.has(key)) return current;
    const next = new Map(current);
    next.delete(key);
    return next;
  });
  const fieldFocusKey = JSON.stringify([selectedFieldId, fieldFocusRequest]);
  const outputFocusKey = JSON.stringify([selectedOutputKey, outputFocusRequest]);
  const projectedFieldRow = (field: ProjectedField, index: number) => <li key={field.columnId ?? `${field.column}:${index}`} data-column-id={field.columnId} className="flex items-center gap-2 rounded border border-slate-200 p-1.5 text-xs">
    <span className="min-w-0 flex-1 truncate" title={field.label}>{field.label}</span>
    {onRenameProjectedField ? <ColumnLabelEditor field={field} disabled={Boolean(projectedFieldRemovalDisabledReason)} onRename={onRenameProjectedField} /> : null}
    {field.columnId && ((field.sourceStepId && field.sourceFieldId) || editableProjectedFieldIds?.has(field.columnId)) && onEditProjectedField ? <button type="button" aria-label={`Edit ${field.label}`}
      disabled={Boolean(projectedFieldRemovalDisabledReason)} title={projectedFieldRemovalDisabledReason} className="shrink-0 rounded px-1.5 py-0.5 text-slate-600 hover:bg-slate-100"
      onClick={() => onEditProjectedField(field)}>Edit</button> : null}
    {field.columnId && field.removal !== 'UNAVAILABLE' && onRemoveProjectedField ? <button type="button" aria-label={`Remove ${field.label}`}
      disabled={Boolean(projectedFieldRemovalDisabledReason)} title={projectedFieldRemovalDisabledReason} className="shrink-0 rounded px-1.5 py-0.5 text-slate-600 hover:bg-slate-100"
      onClick={() => { if (field.columnId) onRemoveProjectedField(field.columnId); }}>Remove</button> : <span className="text-slate-500">Removal unavailable</span>}
  </li>;
  return <fieldset disabled={disabled} aria-label="Source step parameters" className="min-w-0 space-y-2 py-1">
    {view !== 'settings' && !parameters.fieldGroup && projectedFields ? <div className="space-y-2">
      <p className="text-xs font-semibold">Projected fields ({projectedFields.length} included)</p>
      <ol aria-label="Projected fields" className="space-y-2">{projectedFields.map(projectedFieldRow)}</ol>
      {projectedFieldRemovalDisabledReason ? <p role="note" className="text-xs text-amber-900">{projectedFieldRemovalDisabledReason}</p> : null}
    </div> : null}
    {view !== 'fields' && 'output' in parameters && parameters.output ? <>
      <label className="grid gap-1 text-xs">Column collision policy<select aria-label="Column collision policy" className={controlClass} value={parameters.output.collisionPolicy ?? ''} onChange={(event) => onChange({ ...parameters, output: { ...parameters.output!, collisionPolicy: (event.currentTarget.value || undefined) as NonNullable<ImportedSourceParameters['output']>['collisionPolicy'] } })}><option value="">Recipe default</option><option value="error">Report an error</option><option value="overwrite">Overwrite earlier column</option><option value="coalesce">Coalesce values</option></select></label>
    </> : null}
    {view !== 'settings' && 'fieldGroup' in parameters && parameters.fieldGroup ? (() => {
      const group = parameters.fieldGroup;
      const update = (index: number, patch: Partial<typeof group.fields[number]>) => onChange({ ...parameters, fieldGroup: { fields: group.fields.map((field, i) => i === index ? { ...field, ...patch } : field) } });
      const includedCount = group.fields.filter((field) => field.enabled).length;
      const extraFields = (projectedFields ?? []).filter((field) => field.sourceStepId !== sourceStepId);
      return <div className="space-y-2">
        <p className="text-xs font-semibold">Projected fields ({projectedFields ? `${includedCount + extraFields.length} included` : `${includedCount} included of ${group.fields.length}`})</p>
        <ol aria-label="Projected fields" className="space-y-2">
          {group.fields.map((field, index) => {
            const projectedField = projectedFields?.find((item) => item.sourceStepId === sourceStepId && item.sourceFieldId === field.fieldId);
            const displayLabel = projectedField?.label || field.label || field.name;
            return <li key={field.fieldId} data-field-id={field.fieldId} ref={(node) => {
            if (!node || field.fieldId !== selectedFieldId || focusedField.current === fieldFocusKey) return;
            focusedField.current = fieldFocusKey;
            const generation = ++fieldFocusGeneration.current;
            const initiator = document.activeElement;
            window.requestAnimationFrame(() => {
              if (!node.isConnected || generation !== fieldFocusGeneration.current || (document.activeElement !== initiator && document.activeElement !== document.body)) return;
              node.scrollIntoView?.({ block: 'nearest' });
              node.querySelector<HTMLElement>('select')?.focus();
            });
          }}>
            <details open={field.fieldId === selectedFieldId ? true : undefined} className="rounded border border-slate-200 p-1.5">
              <summary className="flex cursor-pointer items-center justify-between gap-2 text-xs">
                <span className="truncate">{displayLabel}{!field.enabled ? ' · disabled' : ''}</span>
                {projectedField && onRenameProjectedField ? <ColumnLabelEditor field={projectedField} disabled={Boolean(projectedFieldRemovalDisabledReason)} onRename={onRenameProjectedField} /> : null}
                <button type="button" aria-label={`${field.enabled ? 'Remove' : 'Restore'} ${displayLabel}`} title={field.enabled ? 'Remove field from the projection' : 'Restore field to the projection'}
                  className="shrink-0 rounded px-1.5 py-0.5 text-xs text-slate-600 hover:bg-slate-100 hover:text-slate-900"
                  onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    update(index, { enabled: !field.enabled });
                  }}>
                  {field.enabled ? 'Remove' : 'Restore'}
                </button>
              </summary>
              <div className="space-y-2 pt-2">
                <label className="grid gap-1 text-xs">Field value policy<select aria-label={`Field ${index + 1} value policy`} className={controlClass} value={field.valueMode ?? ''} onChange={(event) => update(index, { valueMode: (event.currentTarget.value || undefined) as typeof field.valueMode })}>
                  <option value="">Recipe default</option>{(['AUTO', 'FIRST', 'ALL', 'DISTINCT'] as const).map((mode) => <option key={mode} value={mode}>{{AUTO:'Automatic',FIRST:'First value',ALL:'All values as a list',DISTINCT:'Distinct values as a list'}[mode]}</option>)}
                </select></label>
              </div>
            </details>
          </li>; })}
          {extraFields.map(projectedFieldRow)}
        </ol>
        {projectedFieldRemovalDisabledReason ? <p role="note" className="text-xs text-amber-900">{projectedFieldRemovalDisabledReason}</p> : null}
      </div>;
    })() : null}
    {view !== 'fields' && 'catalog' in parameters && parameters.catalog ? (() => { const p = parameters.catalog; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, catalog: { ...p, ...patch } }); return <>
      {catalogOutputs.length ? <fieldset className="space-y-2"><legend className="text-xs font-semibold">Generated columns ({catalogOutputs.length})</legend>
        <ol aria-label="Generated catalog outputs" className="space-y-2">
          {catalogOutputs.map((output, index) => {
            const override = p.outputs?.find((candidate) => candidate.fieldPath === output.outputKey);
            const enabled = override?.enabled ?? output.enabled;
            const name = override?.name ?? output.defaultName;
            const namePending = pendingGeneratedNames.get(output.outputKey) === outputRevision;
            const visibleName = namePending ? 'Generated name pending preview' : name;
            const updateOutput = (patch: { enabled?: boolean; name?: string }, resetName = false) => {
              const nextEnabled = patch.enabled ?? enabled;
              const nextName = resetName ? undefined : patch.name === undefined ? override?.name : patch.name;
              if (resetName) markGeneratedNamePending(output.outputKey);
              else if (patch.name !== undefined) clearGeneratedNamePending(output.outputKey);
              const outputs = (p.outputs ?? []).filter((candidate) => candidate.fieldPath !== output.outputKey);
              const keepEnabled = nextEnabled !== output.enabled ||
                (override?.enabled !== undefined && (override.enabled === false || output.columnId === undefined));
              if (keepEnabled || nextName !== undefined) outputs.push({ fieldPath: output.outputKey, enabled: nextEnabled, ...(nextName !== undefined ? { name: nextName } : {}) });
              update({ outputs: outputs.length ? outputs : undefined });
            };
            return <li key={output.outputKey} data-output-key={output.outputKey} ref={(node) => {
              if (!node || output.outputKey !== selectedOutputKey || focusedOutput.current === outputFocusKey) return;
              focusedOutput.current = outputFocusKey;
              const generation = ++outputFocusGeneration.current;
              const initiator = document.activeElement;
              window.requestAnimationFrame(() => {
                if (!node.isConnected || generation !== outputFocusGeneration.current || (document.activeElement !== initiator && document.activeElement !== document.body)) return;
                node.scrollIntoView?.({ block: 'nearest' });
                node.querySelector<HTMLInputElement>(`input[aria-label="Generated column ${index + 1} name"]`)?.focus();
              });
            }}>
              <details open={output.outputKey === selectedOutputKey ? true : undefined} className="rounded border border-slate-200 p-2">
                <summary className="cursor-pointer text-sm">{visibleName}{!enabled ? ' · disabled' : ''}</summary>
                <div className="space-y-2 pt-2">
                  <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={enabled} onChange={(event) => updateOutput({ enabled: event.currentTarget.checked })} />Include {visibleName}</label>
                  <Text label={`Generated column ${index + 1} name`} value={namePending ? '' : name} placeholder={namePending ? 'Generated name appears after preview' : undefined}
                    onChange={(nextName) => updateOutput({ name: nextName })} />
                  {override?.name !== undefined ? <button type="button" className="text-xs text-blue-800 underline" onClick={() => updateOutput({}, true)}>Use generated name</button> : null}
                </div>
              </details>
            </li>;
          })}
        </ol>
      </fieldset> : null}
      <Strings label="Included path" values={p.includePaths ?? []} options={fieldPathChoices} onChange={(includePaths) => update({ includePaths })} />
      <Strings label="Excluded path" values={p.excludePaths ?? []} options={fieldPathChoices} onChange={(excludePaths) => update({ excludePaths })} />
      <Strings label="Field kind" values={p.kinds ?? []} options={fieldKindChoices} onChange={(kinds) => update({ kinds })} />
      <label className="grid gap-1 text-xs">Column naming<select aria-label="Column naming" className={controlClass} value={p.naming ?? ''} onChange={(event) => update({ naming: (event.currentTarget.value || undefined) as typeof p.naming })}><option value="">Recipe default</option><option value="PATH">Full path</option><option value="PATH_SUFFIX">Path suffix</option></select></label>
    </>; })() : null}
    {view !== 'fields' && 'dynamic' in parameters && parameters.dynamic ? (() => { const p = parameters.dynamic; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, dynamic: { ...p, ...patch } }); return <>
      <OptionalText label="Dynamic column prefix" value={p.columnPrefix} onChange={(columnPrefix) => update({ columnPrefix })} />
      <SourceExpressionEditor label="Dynamic item source" value={p.source} onChange={(source) => update({ source })} pathChoices={sourcePathChoices} />
      <OptionalExpression label="Dynamic key" value={p.key} onChange={(key) => update({ key })} pathChoices={sourcePathChoices} />
      <OptionalExpression label="Dynamic value" value={p.value} onChange={(value) => update({ value })} pathChoices={sourcePathChoices} />
    </>; })() : null}
    {view !== 'fields' && 'extension' in parameters && parameters.extension ? (() => { const p = parameters.extension; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, extension: { ...p, ...patch } }); return <>
      {(() => {
        const choices = ['root.extension[]', 'root.modifierExtension[]'];
        const current = typeof p.source.select === 'string' ? p.source.select : '';
        return <label className="grid gap-1 text-xs">Extension item source
          <select aria-label="Extension item source" className={controlClass} value={current}
            onChange={(event) => update({ source: { select: event.currentTarget.value } })}>
            {!choices.includes(current) ? <option value={current}>{current || 'Choose an extension path'}</option> : null}
            {choices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}
          </select>
        </label>;
      })()}
    </>; })() : null}
    {view !== 'fields' && 'pivot' in parameters && parameters.pivot ? (() => { const p = parameters.pivot; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, pivot: { ...p, ...patch } }); return <>
      <p className="text-xs text-slate-600">Pivot columns are added from Table operations → Pivot / reshape.</p>
      {pivotOutputs.length ? <fieldset className="space-y-2"><legend className="text-xs font-semibold">Pivot columns ({pivotOutputs.length})</legend>
        <ol aria-label="Generated pivot outputs" className="space-y-2">
          {pivotOutputs.map((output, index) => {
            const override = p.outputs?.find((candidate) => candidate.key === output.outputKey);
            const enabled = override?.enabled ?? output.enabled;
            const name = override?.name === '' ? output.defaultName : override?.name ?? output.defaultName;
            const namePending = pendingGeneratedNames.get(output.outputKey) === outputRevision;
            const visibleName = namePending ? 'Generated name pending preview' : name;
            const updateOutput = (patch: { enabled?: boolean; name?: string }, resetName = false) => {
              const nextEnabled = patch.enabled ?? enabled;
              const nextName = resetName ? '' : patch.name === undefined ? override?.name : patch.name;
              if (resetName) markGeneratedNamePending(output.outputKey);
              else if (patch.name !== undefined) clearGeneratedNamePending(output.outputKey);
              const outputs = (p.outputs ?? []).filter((candidate) => candidate.key !== output.outputKey);
              outputs.push({ key: output.outputKey, enabled: nextEnabled, ...(nextName !== undefined ? { name: nextName } : {}) });
              update({ outputs });
            };
            return <li key={output.outputKey} data-output-key={output.outputKey} ref={(node) => {
              if (!node || output.outputKey !== selectedOutputKey || focusedOutput.current === outputFocusKey) return;
              focusedOutput.current = outputFocusKey;
              const generation = ++outputFocusGeneration.current;
              const initiator = document.activeElement;
              window.requestAnimationFrame(() => {
                if (!node.isConnected || generation !== outputFocusGeneration.current || (document.activeElement !== initiator && document.activeElement !== document.body)) return;
                node.scrollIntoView?.({ block: 'nearest' });
                node.querySelector<HTMLInputElement>(`input[aria-label="Pivot column ${index + 1} name"]`)?.focus();
              });
            }}>
              <details open={output.outputKey === selectedOutputKey ? true : undefined} className="rounded border border-slate-200 p-2">
                <summary className="cursor-pointer text-sm">{visibleName}{!enabled ? ' · disabled' : ''}</summary>
                <div className="space-y-2 pt-2">
                  <label className="flex items-center gap-2 text-xs"><input type="checkbox" checked={enabled} onChange={(event) => updateOutput({ enabled: event.currentTarget.checked })} />Include {visibleName}</label>
                  <Text label={`Pivot column ${index + 1} name`} value={namePending ? '' : name} placeholder={namePending ? 'Generated name appears after preview' : undefined}
                    onChange={(nextName) => updateOutput({ name: nextName })} />
                  {override?.name !== undefined ? <button type="button" className="text-xs text-blue-800 underline" onClick={() => updateOutput({}, true)}>Use generated name</button> : null}
                </div>
              </details>
            </li>;
          })}
        </ol>
      </fieldset> : null}
    </>; })() : null}
    {view !== 'fields' && 'expansion' in parameters && parameters.expansion ? (() => { const p = parameters.expansion; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, expansion: { ...p, ...patch } }); return <>
      <SourceExpressionEditor label="Expanded collection" value={p.from} onChange={(from) => update({ from })} pathChoices={sourcePathChoices} />
      <Text label="Expanded item alias" value={p.as} onChange={(as) => update({ as })} />
      <Text label="Ordinality column" value={p.ordinality} onChange={(ordinality) => update({ ordinality })} />
      <label className="grid gap-1 text-xs">Empty collection<select aria-label="Empty collection policy" className={controlClass} value={p.emptyPolicy ?? ''} onChange={(event) => update({ emptyPolicy: (event.currentTarget.value || undefined) as typeof p.emptyPolicy })}>
        <option value="">Recipe default</option><option value="ERROR">Report an error</option><option value="EXCLUDE">Exclude the parent row</option><option value="PRESERVE_PARENT">Keep the parent row</option>
      </select></label>
    </>; })() : null}
    {view !== 'fields' && 'identity' in parameters && parameters.identity ? <>
      <Text label="Identity column name" value={parameters.identity.name} onChange={(name) => onChange({ ...parameters, identity: { ...parameters.identity!, name } })} />
      <SourceExpressionEditor label="Row identity expression" value={parameters.identity.expr} onChange={(expr) => onChange({ ...parameters, identity: { ...parameters.identity!, expr } })} pathChoices={sourcePathChoices} />
    </> : null}
    {view !== 'fields' && 'traversal' in parameters && parameters.traversal ? (() => { const p = parameters.traversal; const update = (patch: Partial<typeof p>) => onChange({ ...parameters, traversal: { ...p, ...patch } }); return <>
      <Text label="Traversal name" value={p.name} onChange={(name) => update({ name })} />
      {resourceTypeChoices !== undefined ? <label className="grid gap-1 text-xs">Related record type<select aria-label="Related record type" className={controlClass} value={p.toResourceType} onChange={(event) => update({ toResourceType: event.currentTarget.value })}>
        {!resourceTypeChoices.includes(p.toResourceType) ? <option value={p.toResourceType}>{p.toResourceType || 'Choose a resource type'}</option> : null}
        {resourceTypeChoices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}
      </select></label> : <Text label="Related record type" value={p.toResourceType} onChange={(toResourceType) => update({ toResourceType })} />}
      <Text label="Related record alias" value={p.alias} onChange={(alias) => update({ alias })} />
      <OptionalExpression label="Traversal source" value={p.from} onChange={(from) => update({ from })} pathChoices={sourcePathChoices} />
    </>; })() : null}
  </fieldset>;
}
