// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { AuthoredTraversalEditor } from './AuthoredTraversalEditor';
import { authoredStages, authoredTraversal } from './authoredTraversal';
import { TraversalScopeNavigator } from './TraversalScopeNavigator';

vi.mock('@xyflow/react', () => ({
  MarkerType: { ArrowClosed: 'arrowclosed' }, Background: () => null, Controls: () => null,
  ReactFlow: ({ nodes, edges, onNodeClick, onEdgeClick, onKeyDown }: {
    nodes: Array<{ id: string; data: { label: React.ReactNode } }>;
    edges: Array<{ id: string; source: string; target: string }>;
    onNodeClick: (event: React.MouseEvent, node: { id: string }) => void;
    onEdgeClick: (event: React.MouseEvent, edge: { id: string }) => void;
    onKeyDown: (event: React.KeyboardEvent) => void;
  }) => <div data-testid="flow" onKeyDown={onKeyDown}>
    {nodes.map((node) => <button className="react-flow__node" data-id={node.id} key={node.id} data-testid={`node-${node.id}`} onClick={(event) => onNodeClick(event, node)}>{node.data.label}</button>)}
    {edges.map((edge) => <span className="react-flow__edge" data-id={edge.id} tabIndex={0} key={edge.id} data-testid={edge.id} data-source={edge.source} data-target={edge.target} onClick={event=>onEdgeClick(event,edge)} />)}
  </div>,
}));
vi.mock('./GuidedGraphWorkspace', () => ({ GuidedGraphWorkspace: (props: Record<string, unknown>) => {
  const currentTable = props.table as DraftTable | undefined;
  const currentCatalog = props.catalog as ExplorerBuilderCatalog;
  const occurrences = currentTable ? authoredTraversal(currentTable, currentCatalog) : [];
  return <section aria-label="Record network">
    {<div><p>Available catalog connections</p><div data-testid="flow">{occurrences.map((occurrence) => <button className="react-flow__node" data-id={occurrence.id} data-testid={`node-${occurrence.id}`} key={occurrence.id} onClick={() => (props.onSelectOccurrence as (id: string) => void)(occurrence.id)}>{occurrence.path}</button>)}{occurrences.slice(1).map((occurrence) => <span className="react-flow__edge" data-id={`authored:${occurrence.id}`} key={occurrence.id} data-testid={`authored:${occurrence.id}`} data-source={occurrence.parentId} data-target={occurrence.id} />)}</div></div>}
  </section>;
} }));
const catalog: ExplorerBuilderCatalog = { snapshotToken: 's', generation: 'g', routePolicy: { allowRepeatedEdges: true, allowSelfLoops: true },
  nodes: [
    { nodeId: 'subject', resourceType: 'ResearchSubject', rowRootEligible: true, populated: true, documentCount: 4 },
    { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 4 },
    { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: true, populated: true, documentCount: 5 },
  ], edges: [
    { edgeId: 'subject-patient', fromNodeId: 'subject', toNodeId: 'patient', label: 'subject' },
    { edgeId: 'investigator-patient', fromNodeId: 'subject', toNodeId: 'patient', label: 'investigator' },
  ], candidates: [] };
const table: DraftTable = { outputId: 'subjects', tabId: 'subjects', title: 'Subjects', document: {
  kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject',
  route: { occurrenceId: 'root', resourceType: 'ResearchSubject', children: [
    { occurrenceId: 'subjectPatient', resourceType: 'Patient', catalogEdgeId: 'subject-patient', relationship: 'subject', matchMode: 'OPTIONAL' },
    { occurrenceId: 'investigatorPatient', resourceType: 'Patient', catalogEdgeId: 'investigator-patient', relationship: 'investigator', matchMode: 'REQUIRED' },
  ] }, rows: { kind: 'RECORDS', records: {} }, columns: [],
} };
const callbacks = () => ({ onSelectOccurrence: vi.fn(), onSetBase: vi.fn(), onChangeBase: vi.fn(), onAppendEdge: vi.fn(), onChangeEdge: vi.fn(), onChangeMatchMode: vi.fn(), onTruncate: vi.fn(), onInspectValues: vi.fn(), onInspectRelatedRows: vi.fn(), onInspectOperation: vi.fn(), onDefineRow: vi.fn() });

const navigatorProps = (value: DraftTable, actions = callbacks()) => ({ table: value,
  selectedOccurrenceId: 'root', projectionKind: 'saved' as const,
  onSelectScope: actions.onSelectOccurrence, onEditSourceStep: vi.fn(), onEditConstructionStep: actions.onInspectOperation,
  onEditColumn: vi.fn(), onEditSourceRows: actions.onDefineRow, onAddRelatedValues: actions.onInspectValues,
  onExpandRelatedRows: actions.onInspectRelatedRows, onGroupRows: vi.fn(),
});

describe('authored traversal correspondence', () => {
  it('keeps the graph and selected-fact inspector visible without a redundant heading', () => {
    const props = { ...callbacks(), catalog, table, projectionKind: 'saved' as const, selectedOccurrenceId: 'subjectPatient', disabled: false };
    render(<AuthoredTraversalEditor {...props} />);
    expect(screen.queryByRole('heading', { name: /Related records/ })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Authored traversal' })).toBeNull();
    expect(screen.queryByRole('navigation', { name: 'Current traversal' })).toBeNull();
    expect(screen.queryByText('Current query')).toBeNull();
    expect(screen.getByTestId('authored-occurrence-inspector')).toBeInTheDocument();
    expect(screen.getByText('Available catalog connections')).toBeInTheDocument();
    expect(screen.getByTestId('authored-occurrence-inspector')).toBeInTheDocument();
  });

  it('uses actual distinct occurrence identities rather than merging same resource catalog nodes', () => {
    const projection = authoredTraversal(table, catalog);
    expect(projection.map((item) => [item.id, item.path, item.edgeId])).toEqual([
      ['root', 'ResearchSubject', undefined], ['subjectPatient', 'ResearchSubject → subject → Patient', 'subject-patient'], ['investigatorPatient', 'ResearchSubject → investigator → Patient', 'investigator-patient'],
    ]);
    expect(projection.some((item) => item.resourceType === 'Observation')).toBe(false);
  });
  it('preserves an unresolved authored occurrence without inferring a catalog edge', () => {
    const unresolved: DraftTable = { ...table, document: { ...table.document, route: { ...table.document.route, children: [{ occurrenceId: 'unknown', resourceType: 'Patient', relationship: 'unavailable' }] } } };
    expect(authoredTraversal(unresolved, catalog)[1]).toMatchObject({ id: 'unknown', edgeId: undefined, catalogNodeId: undefined });
  });
  it('selects exact graph occurrences and never emits a row or value mutation on inspection', () => {
    const actions = callbacks(); render(<AuthoredTraversalEditor {...actions} catalog={catalog} table={table} projectionKind="saved" selectedOccurrenceId="subjectPatient" disabled={false} />);
    fireEvent.click(screen.getByTestId('node-investigatorPatient'));
    expect(actions.onSelectOccurrence).toHaveBeenCalledWith('investigatorPatient');
    expect(actions.onInspectValues).not.toHaveBeenCalled(); expect(actions.onInspectRelatedRows).not.toHaveBeenCalled(); expect(actions.onChangeMatchMode).not.toHaveBeenCalled();
    expect(screen.getByTestId('authored:investigatorPatient')).toHaveAttribute('data-source', 'root');
  });
  it('edits the selected occurrence on the ordinary route path', () => {
    const actions = callbacks(); render(<AuthoredTraversalEditor {...actions} catalog={catalog} table={table} projectionKind="saved" selectedOccurrenceId="subjectPatient" disabled={false} />);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'REQUIRED' } });
    expect(actions.onChangeMatchMode).toHaveBeenCalledWith('subjectPatient', 'REQUIRED');
    expect(screen.getByTestId('authored-occurrence-inspector')).toHaveTextContent('ResearchSubject → subject → Patient');
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe('OPTIONAL');
  });
  it('uses imported proposal callbacks exclusively and blocks mutation during proposed inspection', () => {
    const actions = callbacks(); const imported = { ...table, document: { ...table.document, importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' } } };
    const onChangeMatchMode = vi.fn(); const onRemove = vi.fn();
    const view = render(<AuthoredTraversalEditor {...actions} catalog={catalog} table={imported} projectionKind="saved" selectedOccurrenceId="subjectPatient" disabled={false} importedRelationshipActions={{ onAdd: vi.fn(), onChangeMatchMode, onRemove }} />);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'REQUIRED' } });
    expect(onChangeMatchMode).toHaveBeenCalledWith('subjectPatient', 'REQUIRED'); expect(actions.onChangeMatchMode).not.toHaveBeenCalled();
    view.rerender(<AuthoredTraversalEditor {...actions} catalog={catalog} table={imported} projectionKind="proposed" selectedOccurrenceId="subjectPatient" disabled={false} importedRelationshipActions={{ onAdd: vi.fn(), onChangeMatchMode, onRemove }} />);
    expect(screen.getByRole('combobox')).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'ResearchSubject → investigator → Patient' }));
    expect(actions.onSelectOccurrence).toHaveBeenCalledWith('investigatorPatient');
    expect(screen.getByText(/Apply or Cancel the proposed/)).toBeTruthy();
  });
  it('keeps Group tuple, retained values, input scope and operation IDs after catalog navigation', () => {
    const grouped: DraftTable = { ...table, document: { ...table.document, columns: [
      { occurrenceId: 'subjectPatient', column: 'gender', label: 'Gender', logicalType: 'string', source: { kind: 'field', field: { path: 'gender' } } },
      { occurrenceId: 'root', column: 'status', label: 'Status', logicalType: 'string', source: { kind: 'field', field: { path: 'status' } } },
    ], construction: { version: 1, steps: [{ id: 'group1', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'GROUP', group: { constructionId: 'g', keys: [{ inputColumnId: 'gender', outputColumnId: 'g' }, { inputColumnId: 'status', outputColumnId: 's' }], missingKeyPolicy: 'GROUP' } }, rowValues: [{ inputColumnId: 'status', outputColumnId: 'values', policy: 'ALL' }], outputs: [{ id: 'g', name: 'g', label: 'Gender', type: 'string' }, { id: 's', name: 's', label: 'Status', type: 'string' }, { id: 'values', name: 'values', label: 'Statuses', type: 'list<string>' }] }] } } };
    const actions = callbacks(); render(<><AuthoredTraversalEditor {...actions} catalog={catalog} table={grouped} projectionKind="saved" selectedOccurrenceId="root" disabled={false} /><TraversalScopeNavigator {...navigatorProps(grouped, actions)} /></>);
    fireEvent.click(screen.getByText('Resulting table · all 1 operations in execution order'));
    const stage = within(screen.getByRole('list', { name: 'Ordered resulting-table operations' }));
    expect(stage.getByText(/Group rows by Gender, Status/)).toHaveTextContent('Statuses retains all contributor values');
    expect(stage.getByText('Inputs: Source projection')).toBeTruthy();
    fireEvent.click(stage.getByRole('button', { name: 'Edit table operation 1: Group by Gender, Status' }));
    expect(actions.onInspectOperation).toHaveBeenCalledWith('group1');
    expect(authoredStages(grouped).map((item) => item.id)).toEqual(['group1']);
  });
  it('projects construction-authored relationships at their original stage and preserves list policy explanations', () => {
    const expanded: DraftTable = { ...table, document: { ...table.document, construction: { version: 1, steps: [{
      id: 'pairs', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'RELATED_EXPAND', relatedExpand: {
        anchorColumnId: 'subject-id', choiceId: 'observations', targetNodeId: 'observation', targetResourceType: 'Observation',
        route: [{ edgeId: 'patient-observation', fromNodeId: 'patient', toNodeId: 'observation', fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject', storageDirection: 'INBOUND', matchMode: 'OPTIONAL' }],
        contributorRule: { policy: 'ALL_MATCHES' }, emptyPolicy: 'PRESERVE_PARENT', relatedRecordColumnId: 'observation-id',
      } }, outputs: [{ id: 'observation-id', name: 'observation_id', label: 'Observation ID', type: 'string' }],
    }] } } };
    const actions = callbacks();
    render(<><AuthoredTraversalEditor {...actions} catalog={catalog} table={expanded} projectionKind="saved" selectedOccurrenceId="root" disabled={false} /><TraversalScopeNavigator {...navigatorProps(expanded,actions)} /></>);
    fireEvent.click(screen.getByText('Resulting table · all 1 operations in execution order'));
    const stages = screen.getByRole('list', { name: 'Ordered resulting-table operations' });
    expect(stages).toHaveTextContent('Inputs: Source projection');
    fireEvent.click(within(stages).getByRole('button'));
    expect(actions.onInspectOperation).toHaveBeenCalledExactlyOnceWith('pairs');
    expect(authoredTraversal(expanded, catalog)).toHaveLength(3);
    expect(authoredStages(expanded)[0]).toMatchObject({ id: 'pairs', edgeIds: ['patient-observation'], anchorColumnId: 'subject-id',
      traversal: 'Patient <-[subject]- Observation', input: 'Source', listPreservation: true, missingPolicy: 'PRESERVE_PARENT' });
    expect(expanded.document.construction!.steps[0].operation).toEqual(expect.objectContaining({relatedExpand:expect.objectContaining({anchorColumnId:'subject-id',emptyPolicy:'PRESERVE_PARENT'})}));
    expect(actions.onChangeMatchMode).not.toHaveBeenCalled();
    expect(actions.onInspectValues).not.toHaveBeenCalled();
  });
  it('represents a 100-operation history in original order inside bounded navigation', () => {
    const history: DraftTable = { ...table, document: { ...table.document, construction: { version: 1, steps: Array.from({ length: 100 }, (_, index) => ({
      id: `filter-${index}`, inputs: index ? [{ kind: 'STEP_OUTPUT' as const, stepId: `filter-${index - 1}` }] : [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: { kind: 'FILTER' as const, filter: { columnId: 'subject-id', operator: 'EXISTS' as const } }, outputs: [],
    })) } } };
    const before=structuredClone(history.document);
    const actions=callbacks();
    render(<><AuthoredTraversalEditor {...actions} catalog={catalog} table={history} projectionKind="saved" selectedOccurrenceId="root" disabled={false} /><TraversalScopeNavigator {...navigatorProps(history,actions)} /></>);
    fireEvent.click(screen.getByText('Resulting table · all 100 operations in execution order'));
    const list = screen.getByRole('list', { name: 'Ordered resulting-table operations' });
    expect(list.className).toContain('max-h-56');
    expect([...list.querySelectorAll('[data-operation-id]')].map((item) => item.getAttribute('data-operation-id'))).toEqual(Array.from({ length: 100 }, (_, index) => `filter-${index}`));
    const final=within(list).getAllByRole('button')[99]!;
    fireEvent.click(final);
    expect(actions.onInspectOperation).toHaveBeenCalledExactlyOnceWith('filter-99');
    expect(history.document).toEqual(before);
    expect(authoredStages(history).map(step=>step.id)).toEqual(before.construction!.steps.map(step=>step.id));
  });

});

it('keeps exact source policy editing in the traversal navigator across proposed state without duplicating graph actions', () => {
  const imported: DraftTable = {...table,document:{...table.document,
    importedRecipe:{name:'default',translationVersion:'1',digest:'a'.repeat(64),output:'subjects'},
    importedSource:{steps:[
      {id:'patient_traversal',kind:'TRAVERSAL',occurrenceId:'subjectPatient',title:'Related Patient',summary:'Subject Patient',enabled:true,editable:true},
      {id:'patient_fields',parentId:'patient_traversal',kind:'CATALOG_PROJECTION',title:'Patient fields',summary:'Patient names',enabled:true,editable:true,valueMode:'ALL'},
    ]},columns:[{columnId:'patient_names',column:'names',label:'Patient names',occurrenceId:'subjectPatient',source:{kind:'imported',imported:{outputColumn:'names'}}}],
  }};
  const network: import('../../../types').AuthoringNetworkResponse['network']={owner:'IMPORTED_SOURCE',outputId:'subjects',sourceRows:imported.document.rows,
    occurrences:[],operations:[],bindings:[{columnId:'patient_names',occurrenceId:'subjectPatient',sourceStepId:'patient_fields',valueMode:'ALL',status:'EXACT'}]};
  const values={...navigatorProps(imported),selectedOccurrenceId:'subjectPatient',network};
  const graph={...callbacks(),table:imported,catalog,disabled:false,projectionKind:'saved' as const,selectedOccurrenceId:'subjectPatient',network};
  const view=render(<><AuthoredTraversalEditor {...graph}/><TraversalScopeNavigator {...values}/></>);
  const sourceList=screen.getByRole('list',{name:'Source operations in selected scope'});
  const exact=within(sourceList).getByRole('button',{name:'Edit source operation 2: Patient fields'});
  fireEvent.click(exact);expect(values.onEditSourceStep).toHaveBeenCalledExactlyOnceWith('patient_fields');
  expect(exact.closest('[data-operation-id]')).toHaveAttribute('data-operation-id','patient_fields');
  expect(exact.closest('li')).toHaveTextContent('all values as a list');
  expect(within(screen.getByTestId('authored-traversal-editor')).queryByLabelText('Shared source policies')).toBeNull();
  const before=structuredClone(imported.document);
  view.rerender(<><AuthoredTraversalEditor {...graph} projectionKind="proposed"/><TraversalScopeNavigator {...values} projectionKind="proposed"/></>);
  expect(within(screen.getByRole('list',{name:'Source operations in selected scope'})).getByRole('button',{name:'Edit source operation 2: Patient fields'})).toBe(exact);
  expect(exact).toBeDisabled();expect(imported.document).toEqual(before);
});

it('forwards exact graph occurrence selection without mutating the recipe', () => {
  const actions=callbacks();const before=structuredClone(table.document);
  render(<AuthoredTraversalEditor {...actions} table={table} catalog={catalog} disabled={false} projectionKind="saved" selectedOccurrenceId="root"/>);
  const branch=screen.getByRole('button',{name:'ResearchSubject → investigator → Patient'});
  fireEvent.click(branch);
  expect(actions.onSelectOccurrence).toHaveBeenCalledExactlyOnceWith('investigatorPatient');
  expect(actions.onChangeMatchMode).not.toHaveBeenCalled();expect(actions.onTruncate).not.toHaveBeenCalled();
  expect(actions.onInspectValues).not.toHaveBeenCalled();expect(table.document).toEqual(before);
});

it('portals selected facts into the shared inspection host and preserves catalog exclusion', () => {
 const host=document.createElement('div'); document.body.appendChild(host);
 const props={table,catalog,projectionKind:'saved' as const,selectedOccurrenceId:'root',disabled:false,...callbacks()};
 const view=render(<AuthoredTraversalEditor {...props} inspectorHost={host}/>);
 expect(within(host).getByTestId('authored-occurrence-inspector')).not.toHaveClass('h-full');
 expect(within(screen.getByRole('region',{name:'Record network'})).queryByTestId('authored-occurrence-inspector')).toBeNull();
 view.rerender(<AuthoredTraversalEditor {...props} inspectorHost={host}/>);
 expect(within(host).getByTestId('authored-occurrence-inspector')).toBeInTheDocument();
 view.rerender(<AuthoredTraversalEditor {...props} inspectorHost={host} showInspector={false}/>);
 expect(within(host).queryByTestId('authored-occurrence-inspector')).toBeNull();
 view.unmount();host.remove();
});

it('keeps row definition, row-start and alternative relationship controls in the selected inspector', () => {
 const single = { ...table, document: { ...table.document, route: { ...table.document.route, children: [table.document.route.children![0]] } } };
 const actions = callbacks();
 const view = render(<AuthoredTraversalEditor {...actions} table={single} catalog={catalog} disabled={false} projectionKind="saved" selectedOccurrenceId="subjectPatient" />);
 const inspector = within(screen.getByTestId('authored-occurrence-inspector'));
 fireEvent.change(inspector.getByRole('combobox', { name: 'Relationship for ResearchSubject → subject → Patient' }), { target: { value: 'investigator-patient' } });
 expect(actions.onChangeEdge).toHaveBeenCalledExactlyOnceWith('subjectPatient', 'investigator-patient');
 fireEvent.click(inspector.getByRole('button', { name: 'Make each Patient one row' }));
 expect(actions.onChangeBase).toHaveBeenCalledExactlyOnceWith('patient');
 fireEvent.click(inspector.getByRole('button', { name: 'Change row definition' }));
 expect(actions.onDefineRow).toHaveBeenCalledExactlyOnceWith();
 view.rerender(<AuthoredTraversalEditor {...actions} table={single} catalog={catalog} disabled={false} projectionKind="proposed" selectedOccurrenceId="subjectPatient" />);
 expect(inspector.getByRole('button', { name: 'Change row definition' })).toBeDisabled();
 expect(inspector.getByRole('button', { name: 'Make each Patient one row' })).toBeDisabled();
 expect(inspector.getByRole('combobox', { name: 'Relationship for ResearchSubject → subject → Patient' })).toBeDisabled();
 fireEvent.click(inspector.getByRole('button', { name: 'Change row definition' }));
 expect(actions.onDefineRow).toHaveBeenCalledTimes(1);
});
