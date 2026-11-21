// @vitest-environment jsdom
import React, { useState } from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { ImportedSourceParameters, SourceRecipeExpression } from '../../types';
import { SourceExpressionEditor } from './SourceExpressionEditor';
import { SourceStepParameterEditor } from './SourceStepParameterEditor';

afterEach(cleanup);
const recipe = JSON.parse(readFileSync(resolve(process.cwd(), '../../../docs/work-packages/record-network-and-rows/fixtures/default-dataframer.json'), 'utf8'));
function parameterHarness(initial: ImportedSourceParameters) {
  const changed = vi.fn();
  function Harness() { const [parameters, setParameters] = useState(initial); return <SourceStepParameterEditor parameters={parameters} onChange={(next) => { changed(next); setParameters(next); }} />; }
  const view = render(<Harness />);
  return { changed, view };
}
describe('canonical source expression controls', () => {
  it('edits nested UUID namespace and intended argument without altering argument order or selectors', () => {
    const original = recipe.outputs.find((output: { name: string }) => output.name === 'GroupMember').identity.expr as SourceRecipeExpression;
    const before = JSON.stringify(original); const changed = vi.fn();
    render(<SourceExpressionEditor label="Identity" value={original} onChange={changed} />);
    fireEvent.change(screen.getByRole('textbox', { name: 'Identity · Namespace literal value' }), { target: { value: 'project.example' } });
    const next = changed.mock.calls[0][0];
    expect(next.args[0]).toEqual({ literal: 'project.example' });
    expect(next.args.slice(1)).toEqual('args' in original ? original.args?.slice(1) : undefined);
    expect(JSON.stringify(original)).toBe(before);
    expect(screen.queryByRole('textbox', { name: /JSON|query/i })).toBeNull();
  });
  it('uses explicit ordered-argument actions and preserves null as a literal', () => {
    const changed = vi.fn();
    render(<SourceExpressionEditor label="Fallback" value={{ call: 'coalesce', args: [{ select: 'root.id' }, { literal: null }] }} onChange={changed} />);
    fireEvent.click(screen.getByRole('button', { name: 'Fallback move argument 2 earlier' }));
    expect(changed).toHaveBeenLastCalledWith({ call: 'coalesce', args: [{ literal: null }, { select: 'root.id' }] });
    expect(screen.getByText('Explicit null')).toBeTruthy();
  });
});
describe('typed source step parameters', () => {
  it('edits one of32 projections by its stable identity while preserving every other field and order', () => {
    const fields = Array.from({ length: 32 }, (_, index) => ({ fieldId: `fields[${index}]`, enabled: true, name: `value_${index}`, expr: { select: `root.value${index}` } }));
    const original: ImportedSourceParameters = { fieldGroup: { fields } };
    const { changed } = parameterHarness(original);
    expect(within(screen.getByRole('list', { name: 'Projected fields' })).getAllByRole('listitem')).toHaveLength(32);
    expect(screen.queryByRole('textbox', { name: 'Find projected field' })).toBeNull();
    const field = screen.getByText('value_17').closest('details')!; field.open = true;
    fireEvent.change(within(field).getByRole('combobox', { name: 'Field 18 value policy' }), { target: { value: 'ALL' } });
    const result = changed.mock.calls.at(-1)![0].fieldGroup.fields;
    expect(result.map((entry: { fieldId: string }) => entry.fieldId)).toEqual(fields.map((entry) => entry.fieldId));
    expect(result[17]).toEqual({ ...fields[17], valueMode: 'ALL' });
    expect(result.filter((_: unknown, index: number) => index !== 17)).toEqual(fields.filter((_, index) => index !== 17));
    expect(fields[17].name).toBe('value_17');
  });
  it('keeps projected field identity internal and lets the field list use the panel height', () => {
    const { changed } = parameterHarness({ fieldGroup: { fields: [
      { fieldId: 'internal-field-key', enabled: true, name: 'patient_id', expr: { select: 'root.id' } },
    ] } });
    const list = screen.getByRole('list', { name: 'Projected fields' });
    expect(list.className).not.toMatch(/max-h-|overflow-auto/);
    expect(screen.queryByText('Field identity')).toBeNull();
    expect(screen.queryByText('internal-field-key')).toBeNull();
    const field = within(list).getByRole('listitem').querySelector('details')!;
    field.open = true;
    fireEvent.change(within(field).getByRole('combobox', { name: 'Field 1 value policy' }), { target: { value: 'ALL' } });
    expect(changed.mock.calls.at(-1)![0].fieldGroup.fields[0]).toEqual({
      fieldId: 'internal-field-key', enabled: true, name: 'patient_id', expr: { select: 'root.id' }, valueMode: 'ALL',
    });
  });
  it('offers populated choices for catalog paths and field kinds', () => {
    const changed = vi.fn();
    const catalog = { name: 'fields', includePaths: ['identifier'], kinds: ['string'] };
    render(<SourceStepParameterEditor parameters={{ catalog }} onChange={changed}
      fieldPathChoices={['identifier', 'text']} fieldKindChoices={['code', 'string']} />);
    const include = screen.getByRole('combobox', { name: 'Included path 1' });
    expect((include as HTMLSelectElement).value).toBe('identifier');
    fireEvent.change(include, { target: { value: 'text' } });
    expect(changed).toHaveBeenLastCalledWith({ catalog: { ...catalog, includePaths: ['text'] } });
    const kind = screen.getByRole('combobox', { name: 'Field kind 1' });
    fireEvent.change(kind, { target: { value: 'code' } });
    expect(changed).toHaveBeenLastCalledWith({ catalog: { ...catalog, kinds: ['code'] } });
  });
  it('offers resource type and source path choices for traversal parameters', () => {
    const changed = vi.fn();
    const traversal = { name: 'focus', toResourceType: 'Observation', alias: 'focus', from: { select: 'root.focus[]' } };
    render(<SourceStepParameterEditor parameters={{ traversal }} onChange={changed}
      resourceTypeChoices={['Observation', 'Patient']} sourcePathChoices={['root.focus[]', 'root.subject[]']} />);
    const resourceType = screen.getByRole('combobox', { name: 'Related record type' });
    expect((resourceType as HTMLSelectElement).value).toBe('Observation');
    fireEvent.change(resourceType, { target: { value: 'Patient' } });
    expect(changed).toHaveBeenLastCalledWith({ traversal: { ...traversal, toResourceType: 'Patient' } });
    const source = screen.getByRole('combobox', { name: 'Traversal source source path' });
    expect((source as HTMLSelectElement).value).toBe('root.focus[]');
    fireEvent.change(source, { target: { value: 'root.subject[]' } });
    expect(changed).toHaveBeenLastCalledWith({ traversal: { ...traversal, from: { select: 'root.subject[]' } } });
  });
  it('selects an extension source path instead of requiring free-form typing', () => {
    const { changed } = parameterHarness({ extension: { name: 'extensions', source: { select: 'root.extension[]' } } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Extension item source' }), { target: { value: 'root.modifierExtension[]' } });
    expect(changed).toHaveBeenLastCalledWith({ extension: { name: 'extensions', source: { select: 'root.modifierExtension[]' } } });
  });
  it('removes a projected field by disabling its stable slot and allows it to be restored', () => {
    const { changed } = parameterHarness({ fieldGroup: { fields: [
      { fieldId: 'first', enabled: true, name: 'first_field', expr: { select: 'root.first' } },
      { fieldId: 'second', enabled: true, name: 'second_field', expr: { select: 'root.second' } },
    ] } });
    expect(screen.getByText('Projected fields (2 included of 2)')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Remove first_field' }));
    let fields = changed.mock.calls.at(-1)![0].fieldGroup.fields;
    expect(fields.map((field: { fieldId: string }) => field.fieldId)).toEqual(['first', 'second']);
    expect(fields.map((field: { enabled: boolean }) => field.enabled)).toEqual([false, true]);
    expect(screen.getByText('Projected fields (1 included of 2)')).toBeInTheDocument();
    expect(screen.getByText('first_field · disabled')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Restore first_field' }));
    fields = changed.mock.calls.at(-1)![0].fieldGroup.fields;
    expect(fields.map((field: { fieldId: string }) => field.fieldId)).toEqual(['first', 'second']);
    expect(fields.map((field: { enabled: boolean }) => field.enabled)).toEqual([true, true]);
    expect(screen.getByText('Projected fields (2 included of 2)')).toBeInTheDocument();
  });
  it('lets an identifier rule choose a naming style while keeping its item and value paths', () => {
    const rule = {
      name: 'identifier_keys',
      source: { select: 'root.identifier[]' },
      key: { call: 'last_segment', args: [{ select: 'item.system' }] },
      value: { select: 'item.value' },
    };
    const { changed } = parameterHarness({ dynamic: rule });
    expect(screen.queryByText('Advanced expressions')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Read items from type' })).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox', { name: 'Name columns using' }), { target: { value: 'system' } });
    expect(changed.mock.calls.at(-1)?.[0]).toEqual({ dynamic: { ...rule, key: { select: 'item.system' } } });
  });

  it('distinguishes omitted prefix from an explicit empty prefix', () => {
    const { changed } = parameterHarness({ dynamic: { name: 'keys', source: { select: 'root.identifier[]' } } });
    expect(screen.queryByText(/recipe’s default prefix/i)).toBeNull();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Override Dynamic column prefix' }));
    expect(changed.mock.calls.at(-1)![0].dynamic.columnPrefix).toBe('');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Override Dynamic column prefix' }));
    expect(changed.mock.calls.at(-1)![0].dynamic.columnPrefix).toBeUndefined();
  });
  it('edits generated catalog outputs by exact key and restores disabled defaults', () => {
    const changed = vi.fn();
    const catalogOutputs = [
      { outputKey: 'identifier[].value', columnId: 'column-1', defaultName: 'identifier_value', enabled: true },
      { outputKey: 'name[0].text', defaultName: 'name_text', enabled: false },
    ];
    function Harness() {
      const [parameters, setParameters] = useState<ImportedSourceParameters>({ catalog: { name: 'FHIR fields' } });
      return <SourceStepParameterEditor parameters={parameters} onChange={(next) => { changed(next); setParameters(next); }} catalogOutputs={catalogOutputs} />;
    }
    render(<Harness />);
    const outputs = screen.getByRole('list', { name: 'Generated catalog outputs' });
    expect(within(outputs).getAllByRole('listitem')).toHaveLength(2);
    expect(within(outputs).getByRole('textbox', { name: 'Generated column 1 name' })).toHaveProperty('value', 'identifier_value');
    expect(within(outputs).getByRole('textbox', { name: 'Generated column 2 name' })).toHaveProperty('value', 'name_text');

    fireEvent.change(screen.getByRole('textbox', { name: 'Generated column 1 name' }), { target: { value: 'patient_code' } });
    expect(changed.mock.calls.at(-1)![0].catalog.outputs).toEqual([
      { fieldPath: 'identifier[].value', enabled: true, name: 'patient_code' },
    ]);

    fireEvent.click(within(outputs).getByRole('checkbox', { name: 'Include name_text' }));
    expect(changed.mock.calls.at(-1)![0].catalog.outputs).toEqual([
      { fieldPath: 'identifier[].value', enabled: true, name: 'patient_code' },
      { fieldPath: 'name[0].text', enabled: true },
    ]);
  });
  it('preserves a saved output rename across enable toggles and resets only on explicit action', () => {
    const changed = vi.fn();
    const catalogOutputs = [{ outputKey: 'identifier[].value', columnId: 'column-1', defaultName: 'patient_code', enabled: true }];
    function Harness() {
      const [parameters, setParameters] = useState<ImportedSourceParameters>({ catalog: { name: 'FHIR fields', outputs: [
        { fieldPath: 'identifier[].value', enabled: true, name: 'patient_code' },
      ] } });
      return <SourceStepParameterEditor parameters={parameters} onChange={(next) => { changed(next); setParameters(next); }} catalogOutputs={catalogOutputs} />;
    }
    render(<Harness />);

    fireEvent.click(screen.getByRole('checkbox', { name: 'Include patient_code' }));
    expect(changed).toHaveBeenLastCalledWith({ catalog: { name: 'FHIR fields', outputs: [
      { fieldPath: 'identifier[].value', enabled: false, name: 'patient_code' },
    ] } });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Include patient_code' }));
    expect(changed).toHaveBeenLastCalledWith({ catalog: { name: 'FHIR fields', outputs: [
      { fieldPath: 'identifier[].value', enabled: true, name: 'patient_code' },
    ] } });

    fireEvent.click(screen.getByRole('button', { name: 'Use generated name' }));
    expect(changed).toHaveBeenLastCalledWith({ catalog: { name: 'FHIR fields', outputs: undefined } });
    expect(screen.getByText('Generated name pending preview')).toBeInTheDocument();
    const generatedName = screen.getByRole('textbox', { name: 'Generated column 1 name' });
    expect(generatedName).toHaveProperty('value', '');
    expect(generatedName).toHaveAttribute('placeholder', 'Generated name appears after preview');
  });
  it('resolves a catalog reset from a new preview even when the generated label is unchanged', () => {
    const output = { outputKey: 'identifier[].value', columnId: 'column-old', defaultName: 'patient_code', enabled: true };
    const { rerender } = render(<SourceStepParameterEditor parameters={{ catalog: { name: 'FHIR fields', outputs: [
      { fieldPath: 'identifier[].value', enabled: true, name: 'patient_code' },
    ] } }} onChange={vi.fn()} catalogOutputs={[output]} outputRevision="receipt-before" />);
    fireEvent.click(screen.getByRole('button', { name: 'Use generated name' }));
    expect(screen.getByText('Generated name pending preview')).toBeInTheDocument();

    rerender(<SourceStepParameterEditor parameters={{ catalog: { name: 'FHIR fields' } }} onChange={vi.fn()}
      catalogOutputs={[output]} outputRevision="receipt-before" />);
    expect(screen.getByText('Generated name pending preview')).toBeInTheDocument();

    rerender(<SourceStepParameterEditor parameters={{ catalog: { name: 'FHIR fields' } }} onChange={vi.fn()}
      catalogOutputs={[{ ...output, columnId: 'column-after' }]} outputRevision="receipt-after" />);
    expect(screen.queryByText('Generated name pending preview')).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Generated column 1 name' })).toHaveProperty('value', 'patient_code');
  });
  it('keeps a re-enabled saved output checked when it has no current preview column', () => {
    const changed = vi.fn();
    const catalogOutputs = [{ outputKey: 'identifier[].value', defaultName: 'patient_code', enabled: false }];
    function Harness() {
      const [parameters, setParameters] = useState<ImportedSourceParameters>({ catalog: { name: 'FHIR fields', outputs: [
        { fieldPath: 'identifier[].value', enabled: false },
      ] } });
      return <SourceStepParameterEditor parameters={parameters} onChange={(next) => { changed(next); setParameters(next); }} catalogOutputs={catalogOutputs} />;
    }
    render(<Harness />);

    const include = screen.getByRole('checkbox', { name: 'Include patient_code' });
    expect(include).toHaveProperty('checked', false);
    fireEvent.click(include);
    expect(screen.getByRole('checkbox', { name: 'Include patient_code' })).toHaveProperty('checked', true);
    expect(changed).toHaveBeenLastCalledWith({ catalog: { name: 'FHIR fields', outputs: [
      { fieldPath: 'identifier[].value', enabled: true },
    ] } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Generated column 1 name' }), { target: { value: 'patient_identifier' } });
    expect(changed).toHaveBeenLastCalledWith({ catalog: { name: 'FHIR fields', outputs: [
      { fieldPath: 'identifier[].value', enabled: true, name: 'patient_identifier' },
    ] } });
  });
  it('focuses the exact selected generated output', () => {
    const frames: FrameRequestCallback[] = [];
    const frame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => { frames.push(callback); return frames.length; });
    const outputs = [{ outputKey: 'identifier[].value', columnId: 'column-1', defaultName: 'identifier_value', enabled: true }];
    render(<SourceStepParameterEditor parameters={{ catalog: { name: 'FHIR fields' } }} onChange={vi.fn()} catalogOutputs={outputs} selectedOutputKey="identifier[].value" outputFocusRequest={1} />);
    try {
      for (const callback of frames) callback(0);
      expect(document.activeElement).toBe(screen.getByRole('textbox', { name: 'Generated column 1 name' }));
    } finally { frame.mockRestore(); }
  });
  it('edits GroupMember empty-collection policy without changing its collection or alias', () => {
    const expand = recipe.outputs.find((output: { name: string }) => output.name === 'GroupMember').expand;
    const { changed } = parameterHarness({ expansion: expand });
    fireEvent.change(screen.getByRole('combobox', { name: 'Empty collection policy' }), { target: { value: 'PRESERVE_PARENT' } });
    expect(changed).toHaveBeenLastCalledWith({ expansion: { ...expand, emptyPolicy: 'PRESERVE_PARENT' } });
  });
  it('edits generated pivot outputs by raw key while preserving aliases and disabled choices', () => {
    const changed = vi.fn();
    const choices = [
      { outputKey: 'gender', columnId: 'column-gender', defaultName: 'sex', enabled: true },
      { outputKey: 'lab.code', defaultName: 'lab_code', enabled: false },
    ];
    const initial = { pivot: { name: 'measurements', columns: ['gender'], columnAliases: { gender: 'sex', 'lab.code': 'lab_code' }, outputs: [
      { key: 'lab.code', enabled: false, name: 'lab_code' },
    ] } };
    const view = render(<SourceStepParameterEditor parameters={initial} onChange={changed} pivotOutputs={choices} selectedOutputKey="lab.code" outputFocusRequest={1} />);
    const outputs = screen.getByRole('list', { name: 'Generated pivot outputs' });
    expect(within(outputs).getAllByRole('listitem')).toHaveLength(2);
    expect(within(outputs).getByText('lab_code · disabled')).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Pivot column 2 name' })).toBeTruthy();

    fireEvent.click(screen.getByRole('checkbox', { name: 'Include lab_code' }));
    expect(changed.mock.calls.at(-1)![0].pivot).toEqual({ ...initial.pivot, outputs: [{ key: 'lab.code', enabled: true, name: 'lab_code' }] });

    view.rerender(<SourceStepParameterEditor parameters={{ ...initial, pivot: { ...initial.pivot, outputs: [{ key: 'lab.code', enabled: true, name: 'lab_code' }] } }} onChange={changed} pivotOutputs={choices} selectedOutputKey="lab.code" outputFocusRequest={2} />);
    fireEvent.change(screen.getByRole('textbox', { name: 'Pivot column 2 name' }), { target: { value: 'laboratory_code' } });
    expect(changed.mock.calls.at(-1)![0].pivot).toEqual({ ...initial.pivot, outputs: [{ key: 'lab.code', enabled: true, name: 'laboratory_code' }] });

    fireEvent.click(within(outputs.querySelectorAll('li')[1]).getByRole('button', { name: 'Use generated name' }));
    expect(changed.mock.calls.at(-1)![0].pivot).toEqual({ ...initial.pivot, outputs: [{ key: 'lab.code', enabled: true, name: '' }] });
    expect(within(outputs.querySelectorAll('li')[1]).getByText('Generated name pending preview')).toBeInTheDocument();
    const generatedName = screen.getByRole('textbox', { name: 'Pivot column 2 name' });
    expect(generatedName).toHaveProperty('value', '');
    expect(generatedName).toHaveAttribute('placeholder', 'Generated name appears after preview');
  });
  it('resolves a pivot reset from a new preview even when its generated label matches the old alias', () => {
    const output = { outputKey: 'gender', columnId: 'column-old', defaultName: 'sex', enabled: true };
    const { rerender } = render(<SourceStepParameterEditor parameters={{ pivot: { name: 'measurements', outputs: [{ key: 'gender', enabled: true, name: 'sex' }] } }}
      onChange={vi.fn()} pivotOutputs={[output]} outputRevision="receipt-before" />);
    fireEvent.click(screen.getByRole('button', { name: 'Use generated name' }));
    expect(screen.getByText('Generated name pending preview')).toBeInTheDocument();

    rerender(<SourceStepParameterEditor parameters={{ pivot: { name: 'measurements', outputs: [{ key: 'gender', enabled: true, name: '' }] } }}
      onChange={vi.fn()} pivotOutputs={[output]} outputRevision="receipt-before" />);
    expect(screen.getByText('Generated name pending preview')).toBeInTheDocument();

    rerender(<SourceStepParameterEditor parameters={{ pivot: { name: 'measurements', outputs: [{ key: 'gender', enabled: true, name: '' }] } }}
      onChange={vi.fn()} pivotOutputs={[{ ...output, columnId: 'column-after' }]} outputRevision="receipt-after" />);
    expect(screen.queryByText('Generated name pending preview')).toBeNull();
    expect(screen.getByRole('textbox', { name: 'Pivot column 1 name' })).toHaveProperty('value', 'sex');
  });
  it.each([
    [{ traversal: { name: 'subject', toResourceType: 'Patient', alias: 'patient' } }, 'Related record alias', 'person', 'traversal', 'alias'],
  ] as const)('edits a scalar parameter in %s without altering its other canonical properties', (initial, label, value, arm, property) => {
    const { changed } = parameterHarness(initial as unknown as ImportedSourceParameters);
    fireEvent.change(screen.getByRole('textbox', { name: label }), { target: { value } });
    expect(changed).toHaveBeenLastCalledWith({ [arm]: { ...(initial as unknown as Record<string, Record<string, unknown>>)[arm], [property]: value } });
  });
});

it('focuses only the latest selected field when two navigation actions precede a frame', () => {
 const frames: FrameRequestCallback[]=[];const frame=vi.spyOn(window,'requestAnimationFrame').mockImplementation((callback)=>{frames.push(callback);return frames.length;});
 const parameters:ImportedSourceParameters={fieldGroup:{fields:[{fieldId:'a',enabled:true,name:'first',expr:{select:'root.id'}},{fieldId:'b',enabled:true,name:'second',expr:{select:'root.status'}}]}};
 const view=render(<SourceStepParameterEditor parameters={parameters} onChange={vi.fn()} selectedFieldId="a"/>);
 view.rerender(<SourceStepParameterEditor parameters={parameters} onChange={vi.fn()} selectedFieldId="b"/>);
 try {for(const callback of frames)callback(0);expect(document.activeElement).toBe(screen.getByRole('combobox',{name:'Field 2 value policy'}));} finally {frame.mockRestore();}
});

it('preserves unsupported read expressions without offering a query or JSON rewrite', () => {
 const value:SourceRecipeExpression={document:{context:'root'}};const changed=vi.fn();
 render(<SourceExpressionEditor label="Legacy source" value={value} onChange={changed}/>);
 expect(screen.getByText(/preserved unchanged/)).toBeTruthy();expect(screen.queryByRole('textbox')).toBeNull();expect(changed).not.toHaveBeenCalled();expect(value).toEqual({document:{context:'root'}});
});

it('shows original, generated, and added fields together in the source editor', () => {
  const remove = vi.fn();
  const edit = vi.fn();
  render(<SourceStepParameterEditor sourceStepId="fields" parameters={{ fieldGroup: { fields: [
    { fieldId: 'fields[0]', enabled: true, name: 'status', expr: { select: 'root.status' } },
  ] } }} onChange={vi.fn()} onRemoveProjectedField={remove} onEditProjectedField={edit} editableProjectedFieldIds={new Set(['added'])}
    projectedFields={[
      { columnId: 'base', column: 'status', label: 'Status', occurrenceId: 'base', owner: 'PINNED_SOURCE', removal: 'DISABLE_PINNED_FIELD', status: 'EXACT', sourceStepId: 'fields', sourceFieldId: 'fields[0]' },
      { columnId: 'generated', column: 'type', label: 'Type', occurrenceId: 'base', owner: 'PINNED_SOURCE', removal: 'DISABLE_PINNED_OUTPUT', status: 'EXACT', sourceStepId: 'dynamic', sourceOutputKey: 'type' },
      { columnId: 'added', column: 'category', label: 'Category', occurrenceId: 'base', owner: 'AUTHORED', removal: 'REMOVE_COLUMN', status: 'EXACT' },
    ]} />);
  const list = screen.getByRole('list', { name: 'Projected fields' });
  expect(within(list).getAllByRole('listitem')).toHaveLength(3);
  expect(within(list).getByText('Status')).toBeInTheDocument();
  fireEvent.click(within(list).getByRole('button', { name: 'Remove Type' }));
  fireEvent.click(within(list).getByRole('button', { name: 'Value policy for Category' }));
  expect(remove).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ columnId: 'generated' }));
  expect(edit).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ columnId: 'added' }));
});

it('keeps internal projection source names out of cosmetic field editing', () => {
  const { rerender } = render(<SourceStepParameterEditor parameters={{ extension: { name: 'internal_extensions', source: { select: 'root.extension[]' } } }} onChange={vi.fn()} />);
  expect(screen.queryByRole('textbox', { name: 'Extension column name' })).toBeNull();
  rerender(<SourceStepParameterEditor parameters={{ dynamic: { name: 'internal_keys', source: { select: 'root.identifier[]' } } }} onChange={vi.fn()} />);
  expect(screen.queryByRole('textbox', { name: 'Dynamic column name' })).toBeNull();
});
