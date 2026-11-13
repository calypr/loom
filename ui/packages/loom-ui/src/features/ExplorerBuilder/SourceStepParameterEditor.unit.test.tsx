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
    fireEvent.change(screen.getByRole('textbox', { name: 'Find projected field' }), { target: { value: 'value_17' } });
    const field = screen.getByText('18. value_17').closest('details')!; field.open = true;
    fireEvent.change(within(field).getByRole('textbox', { name: 'Field 18 name' }), { target: { value: 'renamed_value' } });
    const result = changed.mock.calls.at(-1)![0].fieldGroup.fields;
    expect(result.map((entry: { fieldId: string }) => entry.fieldId)).toEqual(fields.map((entry) => entry.fieldId));
    expect(result[17]).toEqual({ ...fields[17], name: 'renamed_value' });
    expect(result.filter((_: unknown, index: number) => index !== 17)).toEqual(fields.filter((_, index) => index !== 17));
    expect(fields[17].name).toBe('value_17');
  });
  it('distinguishes omitted prefix from an explicit empty prefix', () => {
    const { changed } = parameterHarness({ dynamic: { name: 'keys', source: { select: 'root.identifier[]' } } });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Override Dynamic column prefix' }));
    expect(changed.mock.calls.at(-1)![0].dynamic.columnPrefix).toBe('');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Override Dynamic column prefix' }));
    expect(changed.mock.calls.at(-1)![0].dynamic.columnPrefix).toBeUndefined();
  });
  it('edits GroupMember empty-collection policy without changing its collection or alias', () => {
    const expand = recipe.outputs.find((output: { name: string }) => output.name === 'GroupMember').expand;
    const { changed } = parameterHarness({ expansion: expand });
    fireEvent.change(screen.getByRole('combobox', { name: 'Empty collection policy' }), { target: { value: 'PRESERVE_PARENT' } });
    expect(changed).toHaveBeenLastCalledWith({ expansion: { ...expand, emptyPolicy: 'PRESERVE_PARENT' } });
  });
  it('keeps pivot fallback execution order and rejects a duplicate alias instead of overwriting it', () => {
    const { changed } = parameterHarness({ pivot: { name: 'measurements', valueFallbacks: [{ select: 'item.code' }, { select: 'item.display' }], columnAliases: { first: 'one', second: 'two' } } });
    fireEvent.click(screen.getByRole('button', { name: 'Move Pivot value fallback 2 earlier' }));
    expect(changed.mock.calls.at(-1)![0].pivot.valueFallbacks).toEqual([{ select: 'item.display' }, { select: 'item.code' }]);
    const count = changed.mock.calls.length;
    fireEvent.change(screen.getByRole('textbox', { name: 'Alias 1 source column' }), { target: { value: 'second' } });
    expect(screen.getByRole('alert').textContent).toContain('different source column');
    expect(changed).toHaveBeenCalledTimes(count);
  });
  it.each([
    [{ output: { rowGrain: 'file', collisionPolicy: 'overwrite' } }, 'Row grain', 'record', 'output', 'rowGrain'],
    [{ catalog: { name: 'fields', includePaths: ['identifier'], excludePaths: ['text'], kinds: ['string'], naming: 'PATH' } }, 'Projection name', 'renamed', 'catalog', 'name'],
    [{ extension: { name: 'extensions', source: { select: 'root.extension[]' } } }, 'Extension column name', 'renamed', 'extension', 'name'],
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
 try {for(const callback of frames)callback(0);expect(document.activeElement).toBe(screen.getByRole('textbox',{name:'Field 2 name'}));} finally {frame.mockRestore();}
});

it('preserves unsupported read expressions without offering a query or JSON rewrite', () => {
 const value:SourceRecipeExpression={document:{context:'root'}};const changed=vi.fn();
 render(<SourceExpressionEditor label="Legacy source" value={value} onChange={changed}/>);
 expect(screen.getByText(/preserved unchanged/)).toBeTruthy();expect(screen.queryByRole('textbox')).toBeNull();expect(changed).not.toHaveBeenCalled();expect(value).toEqual({document:{context:'root'}});
});
