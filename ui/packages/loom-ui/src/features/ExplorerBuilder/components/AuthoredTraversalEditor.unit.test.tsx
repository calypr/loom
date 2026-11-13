// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { vi } from 'vitest';
import type { ExplorerBuilderCatalog } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { AuthoredTraversalEditor } from './AuthoredTraversalEditor';
import { authoredStages, authoredTraversal } from './authoredTraversal';

vi.mock('@xyflow/react', () => ({
  MarkerType: { ArrowClosed: 'arrowclosed' }, Background: () => null, Controls: () => null,
  ReactFlow: ({ nodes, edges, onNodeClick }: { nodes: Array<{ id: string; data: { label: React.ReactNode } }>; edges: Array<{ id: string; source: string; target: string }>; onNodeClick: (event: React.MouseEvent, node: { id: string }) => void }) => <div data-testid="flow">
    {nodes.map((node) => <button key={node.id} data-testid={`node-${node.id}`} onClick={(event) => onNodeClick(event, node)}>{node.data.label}</button>)}
    {edges.map((edge) => <span key={edge.id} data-testid={edge.id} data-source={edge.source} data-target={edge.target} />)}
  </div>,
}));
vi.mock('./GuidedGraphWorkspace', () => ({ GuidedGraphWorkspace: () => <div>Available catalog connections</div> }));
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

describe('authored traversal correspondence', () => {
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
    expect(screen.getByTestId('authored-occurrence-inspector')).toHaveTextContent('Source stage');
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
    const actions = callbacks(); render(<AuthoredTraversalEditor {...actions} catalog={catalog} table={grouped} projectionKind="saved" selectedOccurrenceId="root" disabled={false} />);
    fireEvent.click(screen.getByRole('button', { name: 'Explore available connections' }));
    const stage = within(screen.getByRole('list', { name: 'Traversal row stages' }));
    expect(stage.getByText(/Group rows by Gender, Status/)).toHaveTextContent('Statuses retains all contributor values');
    expect(stage.getByText('Input: Source')).toBeTruthy();
    fireEvent.click(stage.getByRole('button', { name: '1. Group by Gender, Status' }));
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
    render(<AuthoredTraversalEditor {...callbacks()} catalog={catalog} table={expanded} projectionKind="saved" selectedOccurrenceId="root" disabled={false} />);
    const stages = screen.getByRole('list', { name: 'Traversal row stages' });
    expect(stages).toHaveTextContent('Authored stage traversal: Patient <-[subject]- Observation');
    expect(stages).toHaveTextContent('Input: Source · anchor: subject-id');
    expect(stages).toHaveTextContent('Existing list columns retain their identities and list policies');
    expect(stages).toHaveTextContent('Missing or empty policy: PRESERVE_PARENT');
    expect(authoredTraversal(expanded, catalog)).toHaveLength(3);
    expect(authoredStages(expanded)[0]).toMatchObject({ id: 'pairs', edgeIds: ['patient-observation'], anchorColumnId: 'subject-id' });
  });
  it('represents a 100-operation history in original order inside bounded navigation', () => {
    const history: DraftTable = { ...table, document: { ...table.document, construction: { version: 1, steps: Array.from({ length: 100 }, (_, index) => ({
      id: `filter-${index}`, inputs: index ? [{ kind: 'STEP_OUTPUT' as const, stepId: `filter-${index - 1}` }] : [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: { kind: 'FILTER' as const, filter: { columnId: 'subject-id', operator: 'EXISTS' as const } }, outputs: [],
    })) } } };
    render(<AuthoredTraversalEditor {...callbacks()} catalog={catalog} table={history} projectionKind="saved" selectedOccurrenceId="root" disabled={false} />);
    const list = screen.getByRole('list', { name: 'Traversal row stages' });
    expect(list.className).toContain('max-h-36');
    expect([...list.querySelectorAll('[data-operation-id]')].map((item) => item.getAttribute('data-operation-id'))).toEqual(Array.from({ length: 100 }, (_, index) => `filter-${index}`));
  });

});

it('retains a stable focus identity for a source policy control across proposal remounts', () => {
  const imported: DraftTable = { ...table, document: { ...table.document,
    importedRecipe: { name: 'default', translationVersion: '1', digest: 'a'.repeat(64), output: 'subjects' },
    columns: [{ columnId: 'patient_names', column: 'names', label: 'Patient names', occurrenceId: 'subjectPatient', source: { kind: 'imported', imported: { outputColumn: 'names' } } }],
  } };
  const network: import('../../../types').AuthoringNetworkResponse['network'] = { owner: 'IMPORTED_SOURCE', outputId: 'subjects', sourceRows: imported.document.rows,
    occurrences: [], operations: [], bindings: [{ columnId: 'patient_names', occurrenceId: 'subjectPatient', sourceStepId: 'patient_fields', valueMode: 'ALL', status: 'EXACT' }] };
  const onEditSourceStep = vi.fn();
  const props = { ...callbacks(), table: imported, catalog, disabled: false, projectionKind: 'saved' as const, selectedOccurrenceId: 'subjectPatient', network, onEditSourceStep };
  const { rerender } = render(<AuthoredTraversalEditor {...props} />);
  const original = screen.getByTestId('shared-source-policy-patient_names');
  fireEvent.click(original); expect(onEditSourceStep).toHaveBeenCalledWith('patient_fields');
  rerender(<AuthoredTraversalEditor {...props} showInspector={false} />);
  expect(original.isConnected).toBe(false);
  rerender(<AuthoredTraversalEditor {...props} />);
  expect(screen.getByTestId('shared-source-policy-patient_names')).not.toBe(original);
});

it('portals selected facts into the shared inspection host and preserves catalog exclusion', () => {
 const host=document.createElement('div'); document.body.appendChild(host);
 const props={table,catalog,projectionKind:'saved' as const,selectedOccurrenceId:'root',disabled:false,...callbacks()};
 const view=render(<AuthoredTraversalEditor {...props} inspectorHost={host}/>);
 expect(within(host).getByTestId('authored-occurrence-inspector')).toHaveClass('h-full','overflow-auto');
 expect(within(screen.getByRole('region',{name:'Record network'})).queryByTestId('authored-occurrence-inspector')).toBeNull();
 view.rerender(<AuthoredTraversalEditor {...props} inspectorHost={host} catalogOpen/>);
 expect(within(host).queryByTestId('authored-occurrence-inspector')).toBeNull();
 view.rerender(<AuthoredTraversalEditor {...props} inspectorHost={host} showInspector={false}/>);
 expect(within(host).queryByTestId('authored-occurrence-inspector')).toBeNull();
 view.unmount();host.remove();
});
