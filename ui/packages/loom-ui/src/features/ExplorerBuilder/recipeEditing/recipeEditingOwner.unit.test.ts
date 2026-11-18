// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import type { ExplorerBuilderDocument } from '../../../types';
import { createRecipeEditingState, projectRecipeCandidate, recipeEditingReducer, useRecipeEditingOwner,
  type RecipeEditingIdentity, type RecipeEditingState, type RecipeEditToken, type RecipeValidatedCandidate } from './recipeEditingOwner';
const identity: RecipeEditingIdentity = { project: 'p', explorerId: 'e', outputId: 'subjects', snapshotToken: 'snapshot', draftVersion: 2, draftDigest: 'draft' };
const token: RecipeEditToken = { identity, requestId: 1 };
const previewToken: RecipeEditToken = { identity, requestId: 2 };
const candidate: RecipeValidatedCandidate = { receiptId: 'receipt', candidateWorkspaceDigest: 'candidate', preview: {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2', kind: 'ExplorerBuilderPreview', receiptId: 'receipt', outputId: 'subjects',
  columns: [], rows: [{ observations: [7, 9] }], rowCount: 1, diagnostics: [], rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
} };
const saved: ExplorerBuilderDocument = { kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' },
  rootResourceType: 'ResearchSubject', route: { occurrenceId: 'base', resourceType: 'ResearchSubject' }, rows: { kind: 'RECORDS', records: {} }, columns: [],
  importedRecipe: { name: 'default', translationVersion: 'v1', digest: 'a'.repeat(64), output: 'subjects' },
  fixedFilters: [{ column: 'status', values: ['active'] }], actions: [{ type: 'download', title: 'Download' }],
};
const editing = (): RecipeEditingState => recipeEditingReducer(createRecipeEditingState(identity), {
  type: 'begin', token, target: { kind: 'operation', operationId: 'group_1', stageId: 'source' },
});
const previewing = () => recipeEditingReducer(editing(), { type: 'previewing', token, nextToken: previewToken });
const ready = () => recipeEditingReducer(previewing(), { type: 'ready', token: previewToken, candidate });
describe('shared recipe editing ownership', () => {
  it('locks one mutation while keeping its intended operation and stage', () => {
    const state = editing();
    expect(recipeEditingReducer(state, { type: 'begin', token: previewToken, target: { kind: 'sourceRows' } })).toBe(state);
    expect(state.target).toEqual({ kind: 'operation', operationId: 'group_1', stageId: 'source' });
  });
  it('cannot acquire another output, snapshot, draft, or project', () => {
    for (const change of [{ outputId: 'other' }, { snapshotToken: 'other' }, { draftDigest: 'other' }, { project: 'other' }]) {
      const state = createRecipeEditingState(identity);
      expect(recipeEditingReducer(state, { type: 'begin', token: { ...token, identity: { ...identity, ...change } }, target: { kind: 'sourceRows' } })).toBe(state);
    }
  });
  it('revokes canceled and earlier preview responses', () => {
    const secondToken = { identity, requestId: 3 };
    const second = recipeEditingReducer(previewing(), { type: 'previewing', token: previewToken, nextToken: secondToken });
    expect(recipeEditingReducer(second, { type: 'ready', token: previewToken, candidate })).toBe(second);
    const canceled = recipeEditingReducer(second, { type: 'cancel', token: secondToken });
    expect(recipeEditingReducer(canceled, { type: 'ready', token: secondToken, candidate })).toBe(canceled);
  });
  it('enables Apply only for the current receipt and output', () => {
    const state = previewing();
    for (const preview of [{ ...candidate.preview, outputId: 'other' }, { ...candidate.preview, receiptId: 'other' }]) {
      expect(recipeEditingReducer(state, { type: 'ready', token: previewToken, candidate: { ...candidate, preview } })).toBe(state);
    }
    expect(recipeEditingReducer(editing(), { type: 'applying', token }).status).toBe('editing');
    expect(recipeEditingReducer(ready(), { type: 'applying', token: previewToken }).status).toBe('applying');
  });
  it('rejects a candidate document for a different output', () => {
    const state = previewing();
    expect(recipeEditingReducer(state, { type: 'ready', token: previewToken,
      candidate: { ...candidate, candidateDocument: { ...saved, output: { id: 'other', title: 'Other' } } } })).toBe(state);
  });
  it('turns changed draft or table into an explicit stale lock and rejects late results', () => {
    const state = recipeEditingReducer(ready(), { type: 'identity', identity: { ...identity, draftVersion: 3, draftDigest: 'new' } });
    expect(state.status).toBe('stale'); expect(state.candidate).toBeUndefined();
    expect(recipeEditingReducer(state, { type: 'applying', token: previewToken })).toBe(state);
    expect(recipeEditingReducer(state, { type: 'ready', token: previewToken, candidate })).toBe(state);
    expect(recipeEditingReducer(state, { type: 'cancel', token: previewToken }).status).toBe('saved');
  });
  it('does not let Cancel race an acknowledged Apply', () => {
    const applying = recipeEditingReducer(ready(), { type: 'applying', token: previewToken });
    expect(recipeEditingReducer(applying, { type: 'cancel', token: previewToken })).toBe(applying);
    expect(recipeEditingReducer(applying, { type: 'applied', token: previewToken }).status).toBe('saved');
    const refreshed = recipeEditingReducer(applying, { type: 'identity', identity: { ...identity, draftVersion: 3 } });
    expect(recipeEditingReducer(refreshed, { type: 'applied', token: previewToken }).identity.draftVersion).toBe(3);
  });
  it('removes Apply authorization after a failed or stale request', () => {
    for (const stale of [true, false]) {
      const state = recipeEditingReducer(ready(), { type: 'failed', token: previewToken, error: 'Conflict', stale });
      expect(state.status).toBe(stale ? 'stale' : 'failed'); expect(state.candidate).toBeUndefined();
      expect(recipeEditingReducer(state, { type: 'applying', token: previewToken })).toBe(state);
    }
  });
  it('projects exact source candidate data and preserves unrelated document facts', () => {
    const sourceDocument = { ...saved, rootResourceType: 'Observation' };
    expect(projectRecipeCandidate(saved, { ...candidate, candidateDocument: sourceDocument })).toBe(sourceDocument);
    const construction = { version: 1, steps: [] };
    expect(projectRecipeCandidate(saved, { ...candidate, candidateConstruction: construction })).toEqual({ ...saved, construction });
    expect(saved.construction).toBeUndefined(); expect(projectRecipeCandidate(saved, candidate)).toBeUndefined();
  });
  it('synchronously prevents two starts in one event and preserves inspection renders', () => {
    const { result, rerender } = renderHook(() => useRecipeEditingOwner(identity));
    let first: RecipeEditToken | undefined; let second: RecipeEditToken | undefined;
    act(() => { first = result.current.begin({ kind: 'relationship', occurrenceId: 'patient_a' }); second = result.current.begin({ kind: 'sourceRows' }); });
    expect(first).toBeDefined(); expect(second).toBeUndefined(); rerender();
    expect(result.current.state.target).toEqual({ kind: 'relationship', occurrenceId: 'patient_a' });
  });
  it('guards callbacks retained before a table switch', () => {
    const { result, rerender } = renderHook(({ owner }) => useRecipeEditingOwner(owner), { initialProps: { owner: identity } });
    let active: RecipeEditToken | undefined;
    act(() => { active = result.current.begin({ kind: 'sourceRows' }); });
    const oldOwner = result.current; rerender({ owner: { ...identity, outputId: 'other' } });
    expect(result.current.state.status).toBe('stale');
    act(() => { expect(oldOwner.previewing(active!)).toBeUndefined(); });
    expect(result.current.state.identity.outputId).toBe('other');
  });
});

it('revokes a validated source-step candidate when its form changes, including late responses', () => {
  const previous = ready();
  const changedToken = { identity, requestId: 3 };
  const changed = recipeEditingReducer(previous, { type: 'changed', token: previewToken, nextToken: changedToken });
  expect(changed.status).toBe('editing');
  expect(changed.candidate).toBeUndefined();
  expect(recipeEditingReducer(changed, { type: 'ready', token: previewToken, candidate })).toBe(changed);
  expect(recipeEditingReducer(changed, { type: 'applying', token: previewToken })).toBe(changed);
});

it('never authorizes an unavailable preview while permitting a valid empty result', () => {
 const state=previewing();
 expect(recipeEditingReducer(state,{type:'ready',token:previewToken,candidate:{...candidate,preview:{...candidate.preview,rows:null}}})).toBe(state);
 expect(recipeEditingReducer(state,{type:'ready',token:previewToken,candidate:{...candidate,preview:{...candidate.preview,rows:[],rowCount:0}}}).status).toBe('preview-ready');
});
it('supports an authoritative root-scoped candidate without inventing a workspace digest', () => {
 const start=recipeEditingReducer(createRecipeEditingState(identity),{type:'begin',token,target:{kind:'root',mode:'PRESERVE',nodeId:'patient-node'}});
 const state=recipeEditingReducer(start,{type:'previewing',token,nextToken:previewToken});
 const {candidateWorkspaceDigest: _digest,...rootCandidate}=candidate;
 const ready=recipeEditingReducer(state,{type:'ready',token:previewToken,candidate:{...rootCandidate,candidateDocument:saved}});
 expect(ready.status).toBe('preview-ready');
 expect(ready.candidate?.candidateWorkspaceDigest).toBeUndefined();
 expect(projectRecipeCandidate(saved,ready.candidate!)).toBe(saved);
});
