// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../contracts/types';
import { ImportedSourceStepsSessionProvider } from './session/ImportedSourceStepsSession';
import { ImportedSourceSteps as ConnectedImportedSourceSteps } from './ImportedSourceSteps';
import { groupSourceSteps } from './model/groupSourceSteps';

const ImportedSourceSteps = (props: import('react').ComponentProps<typeof ConnectedImportedSourceSteps> &
  import('react').ComponentProps<typeof ImportedSourceStepsSessionProvider>['value']) => (
  <ImportedSourceStepsSessionProvider value={props}>
    <ConnectedImportedSourceSteps {...props} />
  </ImportedSourceStepsSessionProvider>
);


afterEach(cleanup);

const sourcePlan = {
  steps: [
    {
      id: 'fields',
      kind: 'FIELD_GROUP',
      title: 'Patient fields',
      summary: 'Configure fields from each Patient record',
      editable: true,
    },
    {
      id: 'traversals[0]',
      kind: 'TRAVERSAL',
      title: 'Observation',
      summary: 'Traverse from Patient to Observation records',
      editable: true,
    },
    {
      id: 'traversals[0].pivots[0]',
      parentId: 'traversals[0]',
      kind: 'PIVOT',
      title: 'Observation values',
      summary: 'Discover columns for Observation values',
      editable: true,
      maxColumns: 128,
      valueMode: 'FIRST',
    },
  ],
} satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;

const renderSourceSteps = (
  props: Partial<Parameters<typeof ImportedSourceSteps>[0]> = {},
  navigation?: React.ReactNode,
) => {
  const onRefresh = vi.fn<() => Promise<boolean>>().mockResolvedValue(true);
  const onApply = vi.fn<(command: ExplorerBuilderCommand) => Promise<boolean>>().mockResolvedValue(true);
  const view = render(
    <>{navigation}<ImportedSourceSteps
      outputId="patients"
      importedSource={sourcePlan}
      onRefresh={onRefresh}
      onApply={onApply}
      {...props}
    /></>,
  );
  for (const group of screen.queryAllByTestId(/^source-step-group-/)) {
    if (group instanceof HTMLDetailsElement) group.open = true;
  }
  return { onRefresh, onApply, view };
};

describe('ImportedSourceSteps', () => {
  it('does not render a redundant summary or text-only relationship details', () => {
    renderSourceSteps({ importedSource: { steps: [{
      id: 'patient-route', kind: 'TRAVERSAL', title: 'Traversal to Patient via subject_Patient',
      summary: 'Optional relationship with nested source steps.', editable: true,
    }] } });
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Related Patient records' }));
    expect(screen.queryByText('Use fields from these related records. A match is optional.')).toBeNull();
    expect(screen.queryByText('Relationship details')).toBeNull();
    expect(screen.queryByText('Traversal to Patient via subject_Patient')).toBeNull();
  });

  it('groups only adjacent columns in one scope and never moves a step across a traversal', () => {
    const steps = [
      sourcePlan.steps[0],
      { ...sourcePlan.steps[0], id: 'catalog', kind: 'CATALOG_PROJECTION' },
      sourcePlan.steps[1],
      sourcePlan.steps[2],
      { ...sourcePlan.steps[0], id: 'later-columns' },
    ];
    const groups = groupSourceSteps(steps);
    expect(groups.map((group) => group.entries.map(({ step }) => step.id))).toEqual([
      ['fields', 'catalog'], ['traversals[0]'], ['traversals[0].pivots[0]'], ['later-columns'],
    ]);
    expect(groups.flatMap((group) => group.entries.map(({ step }) => step.id))).toEqual(steps.map((step) => step.id));
    expect(groups.flatMap((group) => group.entries.map(({ number }) => number))).toEqual([1, 2, 3, 4, 5]);
    expect(groupSourceSteps(Array.from({ length: 100 }, (_, i) => ({ ...sourcePlan.steps[0], id: `column-${i}` })))).toHaveLength(1);
  });

  it('keeps one selected inspector while every recipe step stays selectable', () => {
    renderSourceSteps();
    expect(screen.getAllByRole('button', { name: /^Source step / })).toHaveLength(3);
    expect(screen.queryByText(sourcePlan.steps[0].summary)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Patient fields' }));
    fireEvent.click(screen.getByRole('button', { name: 'Source step 3: Observation values' }));
    expect(screen.queryByText(sourcePlan.steps[0].summary)).toBeNull();
    expect(screen.queryByText(sourcePlan.steps[2].summary)).toBeNull();
    expect(screen.getByText('Under Observation')).toBeTruthy();
    expect(screen.getAllByTestId('imported-source-step-editor')).toHaveLength(1);
  });

  it('shows numbered recipe summaries and editable settings without enable toggles', async () => {
    const { onApply } = renderSourceSteps();

    expect(screen.getByRole('button', { name: 'Source step 1: Patient fields' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Source step 2: Observation' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Source step 3: Observation values' })).toBeTruthy();

    fireEvent.click(screen.getByTestId('imported-source-step-traversals[0].pivots[0]'));
    expect(screen.queryByTestId('imported-source-rule-enabled')).toBeNull();
    expect(screen.getByTestId('imported-source-value-mode')).toHaveProperty('value', 'FIRST');
    expect(screen.getByTestId('imported-source-max-columns')).toHaveProperty('value', '128');

    fireEvent.change(screen.getByTestId('imported-source-max-columns'), { target: { value: '64' } });
    fireEvent.change(screen.getByTestId('imported-source-value-mode'), { target: { value: 'ALL' } });
    fireEvent.click(screen.getByTestId('imported-source-step-apply'));

    await waitFor(() => expect(onApply).toHaveBeenCalledWith({
      type: 'UPDATE_IMPORTED_SOURCE_STEP',
      outputId: 'patients',
      sourceStep: {
        stepId: 'traversals[0].pivots[0]',
        maxColumns: 64,
        valueMode: 'ALL',
      },
    }));
    await waitFor(() => expect(screen.queryByTestId('imported-source-step-editor')).toBeNull());
  });

  it('keeps the editor open when the command wrapper reports failure', async () => {
    const onApply = vi.fn<(command: ExplorerBuilderCommand) => Promise<boolean>>().mockResolvedValue(false);
    renderSourceSteps({ onApply });

    fireEvent.click(screen.getByTestId('imported-source-step-traversals[0].pivots[0]'));
    fireEvent.change(screen.getByTestId('imported-source-value-mode'), { target: { value: 'ALL' } });
    fireEvent.click(screen.getByTestId('imported-source-step-apply'));

    await waitFor(() => expect(onApply).toHaveBeenCalledWith({
      type: 'UPDATE_IMPORTED_SOURCE_STEP',
      outputId: 'patients',
      sourceStep: expect.objectContaining({ stepId: 'traversals[0].pivots[0]', valueMode: 'ALL' }),
    }));
    expect(screen.getByTestId('imported-source-step-editor')).toBeTruthy();
  });

  it('exposes the same Cancel path for an owning panel to close a clean source editor', () => {
    const cancelEditActionRef: { current: (() => void) | undefined } = { current: undefined };
    const onBeginEdit = vi.fn(() => true);
    const onCancelEdit = vi.fn();
    renderSourceSteps({ cancelEditActionRef, onBeginEdit, onCancelEdit });

    fireEvent.click(screen.getByTestId('imported-source-step-fields'));
    expect(screen.getByTestId('imported-source-step-editor')).toBeTruthy();
    act(() => cancelEditActionRef.current?.());

    expect(onBeginEdit).toHaveBeenCalledOnce();
    expect(onCancelEdit).toHaveBeenCalledOnce();
    expect(screen.queryByTestId('imported-source-step-editor')).toBeNull();
  });

  it('offers a refresh action only when an older imported document has no source plan', async () => {
    const { onRefresh } = renderSourceSteps({ importedSource: undefined });

    fireEvent.click(screen.getByTestId('imported-source-refresh'));

    await waitFor(() => expect(onRefresh).toHaveBeenCalledOnce());
    expect(screen.getByTestId('imported-source-refresh')).toBeTruthy();
  });

  it('shows only supported controls advertised for each source step', () => {
    renderSourceSteps();
    fireEvent.click(screen.getByTestId('imported-source-step-fields'));

    expect(screen.queryByTestId('imported-source-max-columns')).toBeNull();
    expect(screen.queryByTestId('imported-source-value-mode')).toBeNull();
  });
});

it('keeps navigator-only parameter editing on the existing shared review and cancellation path', () => {
  const parameters = { fieldGroup: { fields: [{fieldId:'fields[0]',enabled:true,name:'id',expr:{select:'root.id'}},{fieldId:'fields[1]',enabled:true,name:'status',expr:{select:'root.status'}}] } };
  const editActionRef: {current: ((stepId:string)=>void)|undefined}={current:undefined};
  const fieldEditActionRef: {current: ((stepId:string,fieldId:string)=>void)|undefined}={current:undefined};
  const changed=vi.fn(), canceled=vi.fn();let owns=false;
  renderSourceSteps({navigatorOnly:true,editActionRef,fieldEditActionRef,importedSource:{steps:[{...sourcePlan.steps[0],parameters},sourcePlan.steps[1]]},
    onBeginEdit:()=>{if(owns)return false;owns=true;return true;},onDraftChange:changed,onCancelEdit:canceled,
    renderReview:(change)=><pre data-testid="reviewed-parameters">{JSON.stringify(change)}</pre>});
  expect(screen.queryByTestId('imported-source-step-list')).toBeNull();
  act(()=>editActionRef.current?.('fields'));
  screen.getByText('id').closest('details')!.open=true;
  fireEvent.change(screen.getByRole('combobox',{name:'Field 1 value policy'}),{target:{value:'ALL'}});
  expect(changed).toHaveBeenCalledOnce();
  const review=JSON.parse(screen.getByTestId('reviewed-parameters').textContent!);
  expect(review).toEqual({stepId:'fields',parameters:{fieldGroup:{fields:[{...parameters.fieldGroup.fields[0],valueMode:'ALL'},parameters.fieldGroup.fields[1]]}}});
  const reviewedBeforeNavigation=screen.getByTestId('reviewed-parameters').textContent;
  act(()=>fieldEditActionRef.current?.('fields','fields[1]'));
  expect(screen.getByRole('combobox',{name:'Field 2 value policy'}).closest('details')?.open).toBe(true);
  expect(screen.getByTestId('reviewed-parameters').textContent).toBe(reviewedBeforeNavigation);
  expect(changed).toHaveBeenCalledOnce();
  act(()=>editActionRef.current?.('traversals[0]'));
  expect((screen.getByRole('combobox',{name:'Field 1 value policy'}) as HTMLSelectElement).value).toBe('ALL');
  expect(parameters.fieldGroup.fields[0].name).toBe('id');
  fireEvent.click(screen.getByRole('button',{name:'Close'}));
  expect(canceled).toHaveBeenCalledOnce();expect(screen.queryByTestId('imported-source-step-editor')).toBeNull();
});

it('previews field removal as a staged disable with the same slots and discards it on Cancel', () => {
  const parameters = { fieldGroup: { fields: [
    { fieldId: 'first', enabled: true, name: 'first', expr: { select: 'root.id' } },
    { fieldId: 'second', enabled: true, name: 'second', expr: { select: 'root.status' } },
  ] } };
  const editActionRef: { current: ((stepId: string) => void) | undefined } = { current: undefined };
  const canceled = vi.fn();
  const source = { steps: [{ ...sourcePlan.steps[0], parameters }] };
  const { onApply } = renderSourceSteps({ importedSource: source, navigatorOnly: true, editActionRef,
    onCancelEdit: canceled,
    renderReview: (change, onCancel) => <><pre data-testid="field-removal-review">{JSON.stringify(change)}</pre>
      <button type="button" onClick={onCancel}>Cancel whole proposal</button></>,
  });

  act(() => editActionRef.current?.('fields'));
  fireEvent.click(screen.getByRole('button', { name: 'Remove first' }));

  const review = JSON.parse(screen.getByTestId('field-removal-review').textContent!);
  const fields = review.parameters.fieldGroup.fields;
  expect(fields.map((field: { fieldId: string }) => field.fieldId)).toEqual(['first', 'second']);
  expect(fields.map((field: { enabled: boolean }) => field.enabled)).toEqual([false, true]);
  expect(parameters.fieldGroup.fields.map((field) => field.enabled)).toEqual([true, true]);

  fireEvent.click(screen.getByRole('button', { name: 'Cancel whole proposal' }));
  expect(canceled).toHaveBeenCalledOnce();
  expect(onApply).not.toHaveBeenCalled();
  expect(screen.queryByTestId('imported-source-step-editor')).toBeNull();
  expect(parameters.fieldGroup.fields.map((field) => field.enabled)).toEqual([true, true]);
});

it('stages an expansion alias and explicit dependent selector edits in one review without retargeting the primary owner', () => {
  const editActionRef: {current: ((stepId:string)=>void)|undefined}={current:undefined};
  const fieldEditActionRef: {current: ((stepId:string,fieldId:string)=>void)|undefined}={current:undefined};
  const begin=vi.fn(()=>true), draftChange=vi.fn(), cancel=vi.fn();
  const source = {steps:[
    {id:'expand',kind:'EXPANSION',title:'Expand Group members',summary:'One row per member',editable:true,parameters:{expansion:{from:{select:'root.member[]'},as:'member'}}},
    {id:'identity',kind:'IDENTITY',title:'Member identity',summary:'Stable member row identity',editable:true,parameters:{identity:{name:'id',expr:{select:'member.entity.reference'}}}},
    {id:'fields',kind:'FIELD_GROUP',title:'Member fields',summary:'Projected member fields',editable:true,parameters:{fieldGroup:{fields:[{fieldId:'member_id',enabled:true,name:'member_id',expr:{select:'member.entity.reference'}}]}}},
  ]} satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;
  const navigation = <nav aria-label="Existing traversal operations">
    <button type="button" onClick={() => editActionRef.current?.('expand')}>Expand</button>
    <button type="button" onClick={() => editActionRef.current?.('identity')}>Member identity</button>
    <button type="button" onClick={() => editActionRef.current?.('fields')}>Member fields</button>
  </nav>;
  const {onRefresh,onApply,view}=renderSourceSteps({importedSource:source,navigatorOnly:true,editActionRef,fieldEditActionRef,onBeginEdit:begin,onDraftChange:draftChange,onCancelEdit:cancel,
    renderReview:(change,_close,batch)=><pre data-testid="batch-review">{JSON.stringify(batch ?? [change])}</pre>}, navigation);
  act(()=>editActionRef.current?.('expand'));
  fireEvent.change(screen.getByRole('textbox',{name:'Expanded item alias'}),{target:{value:'participant'}});
  expect(screen.queryByRole('region', { name: 'Recipe operations' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Member identity' }));
  expect(screen.queryByRole('textbox',{name:'Expanded item alias'})).toBeNull();
  expect((screen.getByRole('textbox',{name:'Row identity expression source path'}) as HTMLInputElement).value).toBe('member.entity.reference');
  fireEvent.change(screen.getByRole('textbox',{name:'Row identity expression source path'}),{target:{value:'participant.entity.reference'}});
  fireEvent.click(screen.getByRole('button', { name: 'Member fields' }));
  expect(screen.queryByRole('textbox',{name:'Row identity expression source path'})).toBeNull();
  expect((screen.getByRole('combobox',{name:'Field 1 value policy'}) as HTMLSelectElement).value).toBe('');
  act(()=>fieldEditActionRef.current?.('fields','member_id'));
  fireEvent.change(screen.getByRole('combobox',{name:'Field 1 value policy'}),{target:{value:'ALL'}});
  fireEvent.click(screen.getByRole('button', { name: 'Member identity' }));
  expect((screen.getByRole('textbox',{name:'Row identity expression source path'}) as HTMLInputElement).value).toBe('participant.entity.reference');
  expect(screen.queryByRole('combobox',{name:'Field 1 value policy'})).toBeNull();
  const batch=JSON.parse(screen.getByTestId('batch-review').textContent!);
  expect(batch.map((change:{stepId:string})=>change.stepId)).toEqual(['expand','identity','fields']);
  expect(batch[0].parameters.expansion).toEqual({...source.steps[0].parameters.expansion,as:'participant'});
  expect(batch[1].parameters.identity.expr).toEqual({select:'participant.entity.reference'});
  expect(batch[2].parameters.fieldGroup.fields[0]).toEqual({...source.steps[2].parameters.fieldGroup!.fields[0],valueMode:'ALL'});
  expect(begin).toHaveBeenCalledTimes(1);
  expect(source.steps[1].parameters.identity!.expr).toEqual({select:'member.entity.reference'});
  fireEvent.click(screen.getByRole('button', { name: 'Member fields' }));
  fireEvent.click(screen.getByRole('button', { name: 'Remove Member fields from this change' }));
  expect(JSON.parse(screen.getByTestId('batch-review').textContent!).map((change:{stepId:string})=>change.stepId)).toEqual(['expand','identity']);
  expect(()=>view.rerender(<>{navigation}<ImportedSourceSteps outputId="patients" importedSource={{steps:[source.steps[1],source.steps[2]]}} onRefresh={onRefresh} onApply={onApply} navigatorOnly editActionRef={editActionRef} fieldEditActionRef={fieldEditActionRef} onBeginEdit={begin} onDraftChange={draftChange} onCancelEdit={cancel} renderReview={(change,_close,batch)=><pre data-testid="batch-review">{JSON.stringify(batch ?? [change])}</pre>} /></>)).not.toThrow();
  expect(screen.getByRole('alert').textContent).toMatch(/primary or selected source operation is no longer available/i);
  fireEvent.click(screen.getByRole('button',{name:'Close source editor'}));
  expect(cancel).toHaveBeenCalledOnce();expect(screen.queryByTestId('batch-review')).toBeNull();
});

it('ignores a stale field-navigation callback without invalidating the preview', () => {
  const editActionRef: { current: ((stepId: string) => void) | undefined } = { current: undefined };
  const fieldEditActionRef: { current: ((stepId: string, fieldId: string) => void) | undefined } = { current: undefined };
  const onDraftChange = vi.fn();
  const importedSource = { steps: [
    { id: 'primary', kind: 'FIELD_GROUP', title: 'Primary fields', summary: 'Primary fields', editable: true,
      parameters: { fieldGroup: { fields: [{ fieldId: 'primary-id', enabled: true, name: 'id', expr: { select: 'root.id' } }] } } },
    { id: 'dependent', kind: 'FIELD_GROUP', title: 'Dependent fields', summary: 'Dependent fields', editable: true,
      parameters: { fieldGroup: { fields: [{ fieldId: 'dependent-id', enabled: true, name: 'patient_id', expr: { select: 'root.patient_id' } }] } } },
  ] } satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;
  renderSourceSteps({ importedSource, navigatorOnly: true, editActionRef, fieldEditActionRef, onDraftChange,
    renderReview: (change, _close, batch) => <pre data-testid="field-navigation-batch">{JSON.stringify(batch ?? [change])}</pre> });
  act(() => editActionRef.current?.('primary'));
  const previewBefore = screen.getByTestId('field-navigation-batch').textContent;
  act(() => fieldEditActionRef.current?.('dependent', 'removed-field-id'));
  expect(screen.getByTestId('field-navigation-batch').textContent).toBe(previewBefore);
  expect(onDraftChange).not.toHaveBeenCalled();
});

it('rejects an unavailable generated-output key before opening a source edit', () => {
  const outputEditActionRef: { current: ((stepId: string, outputKey: string) => void) | undefined } = { current: undefined };
  const onBeginEdit = vi.fn(() => true);
  const importedSource = { steps: [{ id: 'catalog', kind: 'CATALOG_PROJECTION', title: 'Patient fields', summary: 'FHIR fields', editable: true,
    parameters: { catalog: { name: 'Patient fields' } } }] } satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;
  renderSourceSteps({ navigatorOnly: true, importedSource, outputEditActionRef, onBeginEdit, catalogOutputsForStep: () => [] });

  act(() => outputEditActionRef.current?.('catalog', 'removed.field.path'));
  expect(onBeginEdit).not.toHaveBeenCalled();
  expect(screen.queryByTestId('imported-source-step-editor')).toBeNull();
});

it('browses all recipe operations in order and disables inclusion at the 128-operation batch limit', () => {
  const editActionRef: { current: ((stepId: string) => void) | undefined } = { current: undefined };
  const steps = Array.from({ length: 129 }, (_, index) => ({
    id: `operation-${index}`, kind: 'FIELD_GROUP', title: `Operation ${index}`,
    summary: `Project fields for operation ${index}`, editable: true,
    parameters: { fieldGroup: { fields: [{ fieldId: `field-${index}`, enabled: true, name: `field_${index}`, expr: { select: 'root.id' } }] } },
  }));
  const navigation = <nav aria-label="Existing traversal operations">{steps.map((step, index) =>
    <button key={step.id} type="button" aria-label={`Navigate ${step.title}`} onClick={() => editActionRef.current?.(step.id)}>{index + 1}</button>)}</nav>;
  renderSourceSteps({ importedSource: { steps }, navigatorOnly: true, editActionRef,
    renderReview: (change, _close, batch) => <pre data-testid="batch">{JSON.stringify(batch ?? [change])}</pre> }, navigation);
  fireEvent.click(screen.getByRole('button', { name: 'Navigate Operation 0' }));
  for (let index = 1; index <= 127; index += 1) {
    fireEvent.click(screen.getByRole('button', { name: `Navigate Operation ${index}` }));
    fireEvent.change(screen.getByRole('combobox', { name: 'Field 1 value policy' }), { target: { value: 'ALL' } });
  }
  expect(JSON.parse(screen.getByTestId('batch').textContent!)).toHaveLength(128);
  fireEvent.click(screen.getByRole('button', { name: 'Navigate Operation 128' }));
  expect(screen.queryByRole('alert')).toBeNull();
  fireEvent.change(screen.getByRole('combobox', { name: 'Field 1 value policy' }), { target: { value: 'ALL' } });
  expect(screen.getByRole('alert').textContent).toContain('up to 128 source operations');
  expect(screen.queryByRole('region', { name: 'Recipe operations' })).toBeNull();
}, 15000);

it('uses a review-owned Cancel once and retains an exit when invalid parameters hide the review', () => {
  const cancel=vi.fn();
  renderSourceSteps({reviewOwnsCancel:true,onCancelEdit:cancel,renderReview:(_change,onCancel)=><button type="button" onClick={onCancel}>Cancel whole proposal</button>});
  fireEvent.click(screen.getByTestId('imported-source-step-traversals[0].pivots[0]'));
  expect(screen.queryByRole('button',{name:'Close'})).toBeNull();
  expect(screen.getByTestId('imported-source-step-editor').className).toContain('construction-inspector-frame');
  fireEvent.change(screen.getByTestId('imported-source-max-columns'),{target:{value:'0'}});
  expect(screen.queryByRole('button',{name:'Cancel whole proposal'})).toBeNull();
  fireEvent.click(screen.getByRole('button',{name:'Close'}));
  expect(cancel).toHaveBeenCalledOnce();
});

it('reselects the same field after internal focus navigation without resetting parameters or its review', () => {
  const frames:FrameRequestCallback[]=[];
  const frame=vi.spyOn(window,'requestAnimationFrame').mockImplementation((callback)=>{frames.push(callback);return frames.length;});
  const fieldEditActionRef:{current:((stepId:string,fieldId:string)=>void)|undefined}={current:undefined};
  const begin=vi.fn(()=>true), changed=vi.fn();
  renderSourceSteps({navigatorOnly:true,fieldEditActionRef,onBeginEdit:begin,onDraftChange:changed,
    importedSource:{steps:[{...sourcePlan.steps[0],parameters:{fieldGroup:{fields:[{fieldId:'first',name:'first',enabled:true,expr:{select:'root.id'}},{fieldId:'second',name:'second',enabled:true,expr:{select:'root.status'}}]}}}]},
    renderReview:(change)=><pre data-testid="field-focus-review">{JSON.stringify(change)}</pre>});
  try {
    act(()=>fieldEditActionRef.current?.('fields','first'));
    act(()=>frames.splice(0).forEach((callback)=>callback(0)));
    const first=screen.getByRole('combobox',{name:'Field 1 value policy'});
    expect(document.activeElement).toBe(first);
    fireEvent.change(first,{target:{value:'ALL'}});
    const reviewed=screen.getByTestId('field-focus-review').textContent;
    fireEvent.click(screen.getByText('second'));
    act(()=>screen.getByRole('combobox',{name:'Field 2 value policy'}).focus());
    expect(document.activeElement).not.toBe(first);
    act(()=>fieldEditActionRef.current?.('fields','first'));
    act(()=>frames.splice(0).forEach((callback)=>callback(0)));
    expect(document.activeElement).toBe(first);
    expect((first as HTMLSelectElement).value).toBe('ALL');
    expect(screen.getByTestId('field-focus-review').textContent).toBe(reviewed);
    expect(begin).toHaveBeenCalledOnce();expect(changed).toHaveBeenCalledOnce();
  } finally {frame.mockRestore();}
});

it('loads legacy editable descriptors explicitly once and routes the returned stable field identity', async () => {
  const fieldEditActionRef:{current:((stepId:string,fieldId:string)=>void)|undefined}={current:undefined};
  const legacy={steps:[{...sourcePlan.steps[0],editable:false}]};
  const hydrated={steps:[{...sourcePlan.steps[0],parameters:{fieldGroup:{fields:[{fieldId:'stable-original-field',name:'record_id',enabled:true,expr:{select:'root.id'}}]}}}]};
  const refresh=vi.fn(), apply=vi.fn();
  function Harness(){
    const[plan,setPlan]=React.useState<NonNullable<ExplorerBuilderDocument['importedSource']>>(legacy);
    return <ImportedSourceSteps outputId="patients" navigatorOnly importedSource={plan} fieldEditActionRef={fieldEditActionRef}
      onRefresh={async()=>{refresh();setPlan(hydrated);return true;}} onApply={apply}/>;
  }
  render(<Harness/>);expect(refresh).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button',{name:'Load editable source settings'}));
  await waitFor(()=>expect(screen.queryByRole('button',{name:'Load editable source settings'})).toBeNull());
  expect(refresh).toHaveBeenCalledOnce();expect(apply).not.toHaveBeenCalled();
  act(()=>fieldEditActionRef.current?.('fields','stable-original-field'));
  expect((screen.getByRole('combobox',{name:'Field 1 value policy'}) as HTMLSelectElement).value).toBe('');
});

it('does not add a recovery action or request for already hydrated source settings', () => {
  const {onRefresh}=renderSourceSteps({navigatorOnly:true,importedSource:{steps:[{...sourcePlan.steps[0],parameters:{fieldGroup:{fields:[]}}}]}});
  expect(screen.queryByRole('button',{name:'Load editable source settings'})).toBeNull();
  expect(onRefresh).not.toHaveBeenCalled();
});

it.each([undefined,sourcePlan])('keeps legacy recovery disabled under its existing mutation guard', (importedSource) => {
  const {onRefresh}=renderSourceSteps({navigatorOnly:true,importedSource,disabled:true});
  const button=screen.getByRole('button',{name:'Load editable source settings'}) as HTMLButtonElement;
  expect(button.disabled).toBe(true);fireEvent.click(button);expect(onRefresh).not.toHaveBeenCalled();
});

it('reports a failed legacy refresh and retains the explicit retry action', async () => {
  const onRefresh=vi.fn().mockResolvedValue(false);
  renderSourceSteps({navigatorOnly:true,onRefresh});
  fireEvent.click(screen.getByRole('button',{name:'Load editable source settings'}));
  expect((await screen.findByRole('alert')).textContent).toContain('Source steps did not load');
  expect(onRefresh).toHaveBeenCalledOnce();
  await waitFor(()=>expect((screen.getByRole('button',{name:'Load editable source settings'}) as HTMLButtonElement).disabled).toBe(false));
});


it('keeps an enabled-only primary available to edit after staging a parameterized operation', () => {
  const editActionRef: { current: ((stepId: string) => void) | undefined } = { current: undefined };
  const importedSource = { steps: [
    sourcePlan.steps[0],
    { id: 'identity', kind: 'IDENTITY', title: 'Row identity', summary: 'Stable identity', editable: true, parameters: { identity: { name: 'id', expr: { select: 'root.id' } } } },
  ] } satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;
  const navigation=<nav aria-label="Existing traversal operations"><button type="button" onClick={()=>editActionRef.current?.('fields')}>Patient fields</button><button type="button" onClick={()=>editActionRef.current?.('identity')}>Row identity</button></nav>;
  renderSourceSteps({ importedSource, navigatorOnly: true, editActionRef, renderReview: (change, _close, batch) => <pre>{JSON.stringify(batch ?? [change])}</pre> },navigation);
  act(() => editActionRef.current?.('fields'));
  fireEvent.click(screen.getByRole('button', { name: 'Row identity' }));
  fireEvent.click(screen.getByRole('button', { name: 'Patient fields' }));
  expect(screen.queryByRole('checkbox', { name: /Run operation/ })).toBeNull();
});

it('opens source operation settings directly and preserves actual edits across operations', () => {
  const dirty = vi.fn();
  const editActionRef: { current: ((id: string) => void) | undefined } = { current: undefined };
  renderSourceSteps({ navigatorOnly: true, editActionRef, onDraftChange: dirty,
    importedSource: { steps: [
      { id: 'fields', kind: 'FIELD_GROUP', title: 'Fields from Patient', summary: '', editable: true, parameters: { fieldGroup: { fields: [{ fieldId: 'id', name: 'id', enabled: true, expr: { select: 'root.id' } }] } } },
      { id: 'keys', kind: 'DYNAMIC_COLUMN', title: 'Keyed fields', summary: '', editable: true, maxColumns: 20, parameters: { dynamic: { name: 'keys', source: { select: 'root.identifier[]' } } } },
    ] }, renderReview: (change, _cancel, batch) => <pre data-testid="tab-review">{JSON.stringify(batch ?? [change])}</pre> });
  act(() => editActionRef.current?.('fields'));
  const original = screen.getByTestId('tab-review').textContent;
  act(() => editActionRef.current?.('keys'));
  expect(dirty).not.toHaveBeenCalled();
  expect(screen.getByTestId('tab-review').textContent).toBe(original);
  expect(screen.queryByRole('list', { name: 'Projected fields' })).toBeNull();
  fireEvent.change(screen.getByTestId('imported-source-max-columns'), { target: { value: '12' } });
  expect(dirty).toHaveBeenCalledOnce();
  expect(JSON.parse(screen.getByTestId('tab-review').textContent!)).toEqual([{ stepId: 'fields' }, { stepId: 'keys', maxColumns: 12 }]);
  act(() => editActionRef.current?.('fields'));
  expect(screen.getByRole('list', { name: 'Projected fields' })).toBeInTheDocument();
  act(() => editActionRef.current?.('keys'));
  expect(screen.getByTestId('imported-source-max-columns')).toHaveProperty('value', '12');
  expect(dirty).toHaveBeenCalledOnce();
});

it('omits the settings tab when the field source has no editable settings', () => {
  const editActionRef: { current: ((id: string) => void) | undefined } = { current: undefined };
  renderSourceSteps({ navigatorOnly: true, editActionRef, importedSource: { steps: [
    { id: 'fields', kind: 'FIELD_GROUP', title: 'Fields from Patient', summary: '', editable: true,
      parameters: { fieldGroup: { fields: [{ fieldId: 'id', name: 'id', enabled: true, expr: { select: 'root.id' } }] } } },
  ] } });
  act(() => editActionRef.current?.('fields'));
  expect(screen.getByRole('list', { name: 'Projected fields' })).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: 'Source settings' })).toBeNull();
});

it('keeps each occurrence source editor and pending values independently', () => {
  const editActionRef: { current: ((id: string) => void) | undefined } = { current: undefined };
  const switchOccurrenceActionRef: { current: ((id: string) => boolean) | undefined } = { current: undefined };
  const dirty = vi.fn();
  const begin = vi.fn(() => true);
  const suspend = vi.fn(() => true);
  renderSourceSteps({ navigatorOnly: true, editActionRef, switchOccurrenceActionRef, onDraftChange: dirty, onBeginEdit: begin, onSuspendEdit: suspend,
    occurrenceForStep: (id) => id === 'a' ? 'first-patient' : 'second-patient',
    importedSource: { steps: ['a', 'b'].map((id) => ({ id, kind: 'DYNAMIC_COLUMN', title: `${id} fields`, summary: '', editable: true,
      maxColumns: 20, parameters: { dynamic: { name: id, source: { select: 'root.identifier[]' } } } })) },
    renderReview: (change) => <pre data-testid="occurrence-review">{JSON.stringify(change)}</pre> });
  act(() => editActionRef.current?.('a'));
  fireEvent.change(screen.getByTestId('imported-source-max-columns'), { target: { value: '12' } });
  act(() => { switchOccurrenceActionRef.current?.('second-patient'); });
  expect(screen.queryByTestId('imported-source-step-editor')).toBeNull();
  act(() => editActionRef.current?.('b'));
  expect(screen.getByTestId('imported-source-max-columns')).toHaveProperty('value', '20');
  act(() => { switchOccurrenceActionRef.current?.('first-patient'); });
  expect(screen.queryByRole('tab', { name: 'Source settings' })).toBeNull();
  expect(screen.getByTestId('imported-source-max-columns')).toHaveProperty('value', '12');
  expect(JSON.parse(screen.getByTestId('occurrence-review').textContent!)).toMatchObject({ stepId: 'a', maxColumns: 12 });
  act(() => { switchOccurrenceActionRef.current?.('second-patient'); });
  expect(screen.getByTestId('imported-source-max-columns')).toHaveProperty('value', '20');
  expect(JSON.parse(screen.getByTestId('occurrence-review').textContent!)).toEqual({ stepId: 'b' });
  expect(suspend).toHaveBeenCalledTimes(3);
});
