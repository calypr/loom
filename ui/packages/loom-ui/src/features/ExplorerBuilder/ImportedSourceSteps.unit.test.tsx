// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../types';
import { groupSourceSteps, ImportedSourceSteps } from './ImportedSourceSteps';

afterEach(cleanup);

const sourcePlan = {
  steps: [
    {
      id: 'fields',
      kind: 'FIELD_GROUP',
      title: 'Patient fields',
      summary: 'Configure fields from each Patient record',
      enabled: true,
      editable: true,
    },
    {
      id: 'traversals[0]',
      kind: 'TRAVERSAL',
      title: 'Observation',
      summary: 'Traverse from Patient to Observation records',
      enabled: false,
      editable: true,
    },
    {
      id: 'traversals[0].pivots[0]',
      parentId: 'traversals[0]',
      kind: 'PIVOT',
      title: 'Observation values',
      summary: 'Discover columns for Observation values',
      enabled: true,
      editable: true,
      maxColumns: 128,
      valueMode: 'FIRST',
    },
  ],
} satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;

const renderSourceSteps = (
  props: Partial<Parameters<typeof ImportedSourceSteps>[0]> = {},
) => {
  const onRefresh = vi.fn<() => Promise<boolean>>().mockResolvedValue(true);
  const onApply = vi.fn<(command: ExplorerBuilderCommand) => Promise<boolean>>().mockResolvedValue(true);
  render(
    <ImportedSourceSteps
      outputId="patients"
      importedSource={sourcePlan}
      onRefresh={onRefresh}
      onApply={onApply}
      {...props}
    />,
  );
  for (const group of screen.queryAllByTestId(/^source-step-group-/)) {
    if (group instanceof HTMLDetailsElement) group.open = true;
  }
  return { onRefresh, onApply };
};

describe('ImportedSourceSteps', () => {
  it('uses related-record wording and keeps the exact technical route available in details', () => {
    renderSourceSteps({ importedSource: { steps: [{
      id: 'patient-route', kind: 'TRAVERSAL', title: 'Traversal to Patient via subject_Patient',
      summary: 'Optional relationship with nested source steps.', enabled: true, editable: true,
    }] } });
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Related Patient records' }));
    expect(screen.getByText('Use fields from these related records. A match is optional.')).toBeTruthy();
    const technical = screen.getByText('Traversal to Patient via subject_Patient');
    expect(technical.closest('details')).toHaveProperty('open', false);
    expect(screen.getByText('Relationship details')).toBeTruthy();
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

  it('keeps descriptions in one selected inspector while every recipe step stays selectable', () => {
    renderSourceSteps();
    expect(screen.getAllByRole('button', { name: /^Source step / })).toHaveLength(3);
    expect(screen.queryByText(sourcePlan.steps[0].summary)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Patient fields' }));
    expect(screen.getByText(sourcePlan.steps[0].summary)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Source step 3: Observation values' }));
    expect(screen.queryByText(sourcePlan.steps[0].summary)).toBeNull();
    expect(screen.getByText(sourcePlan.steps[2].summary)).toBeTruthy();
    expect(screen.getByText('Under Observation')).toBeTruthy();
    expect(screen.getAllByTestId('imported-source-step-editor')).toHaveLength(1);
  });

  it('shows numbered recipe summaries and inactive descendants without dropping their settings', async () => {
    const { onApply } = renderSourceSteps();

    expect(screen.getByRole('button', { name: 'Source step 1: Patient fields' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Source step 2: Observation' }).textContent).toContain('Disabled');
    expect(screen.getByRole('button', { name: 'Source step 3: Observation values' }).textContent).toContain('Inactive · parent off');

    fireEvent.click(screen.getByTestId('imported-source-step-traversals[0].pivots[0]'));
    expect(screen.getByRole('status').textContent).toContain('Inactive because Observation is disabled');
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
        enabled: true,
        maxColumns: 64,
        valueMode: 'ALL',
      },
    }));
    await waitFor(() => expect(screen.queryByTestId('imported-source-step-editor')).toBeNull());
  });

  it('keeps the editor open when the command wrapper reports failure', async () => {
    const onApply = vi.fn<(command: ExplorerBuilderCommand) => Promise<boolean>>().mockResolvedValue(false);
    renderSourceSteps({ onApply });

    fireEvent.click(screen.getByTestId('imported-source-step-fields'));
    fireEvent.click(screen.getByLabelText('Enable this source step'));
    fireEvent.click(screen.getByTestId('imported-source-step-apply'));

    await waitFor(() => expect(onApply).toHaveBeenCalledWith({
      type: 'UPDATE_IMPORTED_SOURCE_STEP',
      outputId: 'patients',
      sourceStep: { stepId: 'fields', enabled: false },
    }));
    expect(screen.getByTestId('imported-source-step-editor')).toBeTruthy();
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

 it.each(['Optional relationship selection follows an external policy.', 'Required relationship selection follows an external policy.'])(
  'preserves an unfamiliar relationship explanation: %s', (summary) => {
    renderSourceSteps({ importedSource: { steps: [{
      id: 'unknown', kind: 'TRAVERSAL', title: 'Unfamiliar relationship description',
      summary, enabled: true, editable: true,
    }] } });
    fireEvent.click(screen.getByRole('button', { name: 'Source step 1: Unfamiliar relationship description' }));
    const editor = screen.getByTestId('imported-source-step-editor');
    const primarySummary = [...(editor.querySelector('.construction-inspector-body') ?? editor).children].find((child) => child.tagName === 'P');
    expect(primarySummary?.textContent).toBe(summary);
  },
);

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
  screen.getByText('1. id').closest('details')!.open=true;
  fireEvent.change(screen.getByRole('textbox',{name:'Field 1 name'}),{target:{value:'record_id'}});
  expect(changed).toHaveBeenCalledOnce();
  const review=JSON.parse(screen.getByTestId('reviewed-parameters').textContent!);
  expect(review).toEqual({stepId:'fields',enabled:true,parameters:{fieldGroup:{fields:[{...parameters.fieldGroup.fields[0],name:'record_id'},parameters.fieldGroup.fields[1]]}}});
  const reviewedBeforeNavigation=screen.getByTestId('reviewed-parameters').textContent;
  act(()=>fieldEditActionRef.current?.('fields','fields[1]'));
  expect(screen.getByRole('textbox',{name:'Field 2 name'}).closest('details')?.open).toBe(true);
  expect(screen.getByTestId('reviewed-parameters').textContent).toBe(reviewedBeforeNavigation);
  expect(changed).toHaveBeenCalledOnce();
  act(()=>editActionRef.current?.('traversals[0]'));
  expect((screen.getByRole('textbox',{name:'Field 1 name'}) as HTMLInputElement).value).toBe('record_id');
  expect(parameters.fieldGroup.fields[0].name).toBe('id');
  fireEvent.click(screen.getByRole('button',{name:'Close'}));
  expect(canceled).toHaveBeenCalledOnce();expect(screen.queryByTestId('imported-source-step-editor')).toBeNull();
});

it('stages an expansion alias and explicit dependent selector edits in one review without retargeting the primary owner', () => {
  const editActionRef: {current: ((stepId:string)=>void)|undefined}={current:undefined};
  const fieldEditActionRef: {current: ((stepId:string,fieldId:string)=>void)|undefined}={current:undefined};
  const begin=vi.fn(()=>true), draftChange=vi.fn(), cancel=vi.fn();
  const source = {steps:[
    {id:'expand',kind:'EXPANSION',title:'Expand Group members',summary:'One row per member',enabled:true,editable:true,parameters:{expansion:{from:{select:'root.member[]'},as:'member'}}},
    {id:'identity',kind:'IDENTITY',title:'Member identity',summary:'Stable member row identity',enabled:true,editable:true,parameters:{identity:{name:'id',expr:{select:'member.entity.reference'}}}},
    {id:'fields',kind:'FIELD_GROUP',title:'Member fields',summary:'Projected member fields',enabled:true,editable:true,parameters:{fieldGroup:{fields:[{fieldId:'member_id',enabled:true,name:'member_id',expr:{select:'member.entity.reference'}}]}}},
  ]} satisfies NonNullable<ExplorerBuilderDocument['importedSource']>;
  renderSourceSteps({importedSource:source,navigatorOnly:true,editActionRef,fieldEditActionRef,onBeginEdit:begin,onDraftChange:draftChange,onCancelEdit:cancel,
    renderReview:(change,_close,batch)=><pre data-testid="batch-review">{JSON.stringify(batch ?? [change])}</pre>});
  act(()=>editActionRef.current?.('expand'));
  fireEvent.change(screen.getByRole('textbox',{name:'Expanded item alias'}),{target:{value:'participant'}});
  const chooser=screen.getByRole('combobox',{name:'Also edit a dependent operation'});
  fireEvent.change(chooser,{target:{value:'identity'}});
  expect((screen.getByRole('textbox',{name:'Row identity expression source path'}) as HTMLInputElement).value).toBe('member.entity.reference');
  fireEvent.change(screen.getByRole('textbox',{name:'Row identity expression source path'}),{target:{value:'participant.entity.reference'}});
  fireEvent.change(chooser,{target:{value:'fields'}});
  act(()=>fieldEditActionRef.current?.('fields','member_id'));
  expect((screen.getByRole('textbox',{name:'Field 1 expression source path'}) as HTMLInputElement).value).toBe('member.entity.reference');
  fireEvent.change(screen.getByRole('textbox',{name:'Field 1 expression source path'}),{target:{value:'participant.entity.reference'}});
  const batch=JSON.parse(screen.getByTestId('batch-review').textContent!);
  expect(batch.map((change:{stepId:string})=>change.stepId)).toEqual(['expand','identity','fields']);
  expect(batch[0].parameters.expansion).toEqual({...source.steps[0].parameters.expansion,as:'participant'});
  expect(batch[1].parameters.identity.expr).toEqual({select:'participant.entity.reference'});
  expect(batch[2].parameters.fieldGroup.fields[0]).toEqual({...source.steps[2].parameters.fieldGroup!.fields[0],expr:{select:'participant.entity.reference'}});
  expect(begin).toHaveBeenCalledTimes(1);
  expect(source.steps[1].parameters.identity!.expr).toEqual({select:'member.entity.reference'});
  fireEvent.click(screen.getByRole('button',{name:'Close'}));
  expect(cancel).toHaveBeenCalledOnce();expect(screen.queryByTestId('batch-review')).toBeNull();
  act(()=>editActionRef.current?.('expand'));
  expect(screen.queryByRole('list',{name:'Staged source operations'})).toBeNull();
  expect((screen.getByRole('textbox',{name:'Expanded item alias'}) as HTMLInputElement).value).toBe('member');
});

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
    const first=screen.getByRole('textbox',{name:'Field 1 name'});
    expect(document.activeElement).toBe(first);
    fireEvent.change(first,{target:{value:'edited_first'}});
    const reviewed=screen.getByTestId('field-focus-review').textContent;
    fireEvent.click(screen.getByText('2. second'));
    act(()=>screen.getByRole('textbox',{name:'Field 2 name'}).focus());
    expect(document.activeElement).not.toBe(first);
    act(()=>fieldEditActionRef.current?.('fields','first'));
    act(()=>frames.splice(0).forEach((callback)=>callback(0)));
    expect(document.activeElement).toBe(first);
    expect((first as HTMLInputElement).value).toBe('edited_first');
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
  expect((screen.getByRole('textbox',{name:'Field 1 name'}) as HTMLInputElement).value).toBe('record_id');
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
