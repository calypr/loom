import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import ts from 'typescript';
import { it } from 'vitest';

const { JSDOM } = createRequire(import.meta.url)('jsdom');
const source = readFileSync(new URL('./BuilderWorkspace.tsx', import.meta.url), 'utf8');
// Execute the actual action helpers without mounting unrelated Builder services.
const focus = source.slice(source.indexOf('  const rememberRecipeFocus ='), source.indexOf('  rootRecipeActions.current ='));
const network = source.slice(source.indexOf('  const completePendingRecipeFocus ='), source.indexOf('  const pendingRelationship ='));
assert(focus.includes('const restoreRecipeFocus') && network.includes('describeAuthoringNetwork'));
const code = ts.transpileModule(focus + network, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText;
const names = ['document', 'HTMLElement', 'HTMLDetailsElement', 'window', 'recipeInitiator', 'recipeInitiatorFact', 'pendingRecipeFocus', 'recipeOwner', 'latestState', 'table', 'networkRequestOwner', 'relationshipOwner', 'loomClient', 'projectId', 'state', 'authResourcePath', 'setSavedNetwork', 'recipeFocusGeneration', 'workspaceAttached', 'firstTableProgress'];
function setup(policyId = 'shared-source-policy-value', firstTableProgress: {kind: 'idle' | 'running'; phase?: string} = {kind: 'idle'}, deferNetworkDOM = false) {
  const { window } = new JSDOM(`<button data-occurrence-id="obs" id="nav">Observation path</button><button id="next">Another task</button><section data-occurrence-id="obs"><button id="old" data-testid="${policyId}">Edit shared source policy</button></section>`);
  const queue: Array<() => void> = [];
  window.requestAnimationFrame = (callback: () => void) => queue.push(callback);
  const pending = { current: undefined }, latest = { current: { selectedOutputId: 'tableA' } }, attached = { current: true };
  let resolve!: (value: unknown) => void;
  const reads: unknown[] = [];
  const requestOwner = { current: '' };
  const state = { catalog: { snapshotToken: 'snapshot' }, draftVersion: 2, draftDigest: 'draft-2', explorerId: 'e' };
  const renderNetwork = () => window.document.body.insertAdjacentHTML('beforeend', `<section data-occurrence-id="obs"><button id="restored" data-testid="${policyId}">Edit shared source policy</button></section>`);
  const client = { describeAuthoringNetwork: (identity: unknown) => { reads.push(identity); return new Promise((done) => { resolve = done; }); } };
  const args = [window.document, window.HTMLElement, window.HTMLDetailsElement, window, { current: undefined }, { current: undefined }, pending, { state: { identity: { outputId: 'tableA' } } }, latest, { outputId: 'tableA' }, requestOwner, 'owner', client, 'p', state, undefined, () => {if (!deferNetworkDOM) renderNetwork();}, { current: 0 }, attached, firstTableProgress];
  const actions = new Function(...names, `${code};return {rememberRecipeFocus,restoreRecipeFocus,loadRecipeNetwork};`)(...args) as {
    rememberRecipeFocus(): void; restoreRecipeFocus(): void; loadRecipeNetwork(): void;
  };
  const old = window.document.getElementById('old'); old.focus(); actions.rememberRecipeFocus(); old.parentElement.remove();
  return { window, actions, pending, latest, attached, reads, requestOwner, firstTableProgress, renderNetwork,
    flush() { while (queue.length) queue.shift()!(); },
    async bindings(result = { draftVersion: 2, draftDigest: 'draft-2', snapshotToken: 'snapshot', network: { outputId: 'tableA' } }) { resolve(result); await Promise.resolve(); await Promise.resolve(); },
  };
}

it('restores the same source-policy control when current bindings arrive', async () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.flush(); assert.equal(t.window.document.activeElement.id, 'nav');
  t.actions.loadRecipeNetwork(); await t.bindings(); t.flush(); assert.equal(t.window.document.activeElement.id, 'restored');
});
it('respects focus navigation before late bindings complete', async () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.flush(); t.actions.loadRecipeNetwork(); t.window.document.getElementById('next').focus();
  await t.bindings(); t.flush(); assert.equal(t.window.document.activeElement.id, 'next');
});
it('does not restore a control in another output after late bindings', async () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.flush(); t.actions.loadRecipeNetwork(); t.latest.current.selectedOutputId = 'tableB';
  await t.bindings(); t.flush(); assert.notEqual(t.window.document.activeElement.id, 'restored');
});
it('a new edit revokes already registered pending restoration', async () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.flush(); t.actions.loadRecipeNetwork(); t.window.document.getElementById('next').focus(); t.actions.rememberRecipeFocus();
  await t.bindings(); t.flush(); assert.equal(t.window.document.activeElement.id, 'next'); assert.equal(t.pending.current, undefined);
});
it('respects user navigation before the initial restoration frame', () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.window.document.getElementById('next').focus(); t.flush();
  assert.equal(t.window.document.activeElement.id, 'next');
});
it('a queued old frame cannot recreate pending restoration after a new edit', () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.window.document.getElementById('next').focus(); t.actions.rememberRecipeFocus(); t.flush();
  assert.equal(t.window.document.activeElement.id, 'next'); assert.equal(t.pending.current, undefined);
});
it('duplicate Apply cleanup preserves the first normal restoration', async () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.actions.restoreRecipeFocus(); t.flush(); assert.equal(t.window.document.activeElement.id, 'nav');
  t.actions.loadRecipeNetwork(); await t.bindings(); t.flush(); assert.equal(t.window.document.activeElement.id, 'restored');
});
it('unmount revokes the initial frame before pending work is created', () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.attached.current = false; t.flush();
  assert.equal(t.window.document.activeElement, t.window.document.body); assert.equal(t.pending.current, undefined);
});
it('unmount suppresses late bindings focus', async () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.flush(); t.actions.loadRecipeNetwork(); t.attached.current = false;
  await t.bindings(); t.flush(); assert.equal(t.window.document.activeElement.id, 'nav');
});
it('switching output before the first frame cannot recreate pending work', () => {
  const t = setup(); t.actions.restoreRecipeFocus(); t.latest.current.selectedOutputId = 'tableB'; t.flush();
  assert.equal(t.window.document.activeElement, t.window.document.body); assert.equal(t.pending.current, undefined);
});

for (const suffix of ['root-value', 'source-value']) {
 it(`restores row policy ${suffix} only after authoritative bindings, with distinct graph identity`, async () => {
  const t=setup(`shared-source-policy-row-${suffix}`);
  t.window.document.body.insertAdjacentHTML('beforeend', `<button data-testid="shared-source-policy-${suffix}" id="graph">Graph policy</button>`);
  t.actions.restoreRecipeFocus();t.flush();assert.equal(t.window.document.activeElement.id,'nav');
  t.actions.loadRecipeNetwork();await t.bindings();t.flush();assert.equal(t.window.document.activeElement.id,'restored');
 });
}
it('row policy late restoration respects user navigation and output changes', async () => {
 for (const mode of ['navigation','output']) {
  const t=setup('shared-source-policy-row-value');t.actions.restoreRecipeFocus();t.flush();t.actions.loadRecipeNetwork();
  if(mode==='navigation')t.window.document.getElementById('next').focus();else t.latest.current.selectedOutputId='other';
  await t.bindings();t.flush();assert.notEqual(t.window.document.activeElement.id,'restored');
 }
});

for (const phase of ['creating', 'adding-id']) {
 it(`defers network metadata during compound first-table ${phase} without claiming an owner`, () => {
  const t=setup('shared-source-policy-value',{kind:'running',phase});
  t.actions.loadRecipeNetwork();assert.equal(t.reads.length,0);assert.equal(t.requestOwner.current,'');
  t.firstTableProgress.kind='idle';t.actions.loadRecipeNetwork();t.actions.loadRecipeNetwork();
  assert.equal(t.reads.length,1);assert.equal(t.requestOwner.current,'owner');
  assert.deepEqual(t.reads[0],{project:'p',explorerId:'e',authResourcePath:undefined,snapshotToken:'snapshot',expectedDraftVersion:2,expectedDraftDigest:'draft-2',outputId:'tableA'});
 });
}
it('loads current metadata once when first-table creation exits to idle after failure', () => {
 const t=setup();t.actions.loadRecipeNetwork();t.actions.loadRecipeNetwork();assert.equal(t.reads.length,1);
});

it('rejects stale metadata identity without publishing bindings or restoring focus', async () => {
 const t=setup();t.actions.restoreRecipeFocus();t.flush();t.actions.loadRecipeNetwork();
 await t.bindings({draftVersion:1,draftDigest:'intermediate',snapshotToken:'snapshot',network:{outputId:'tableA'}});t.flush();
 assert.equal(t.window.document.getElementById('restored'),null);assert.equal(t.window.document.activeElement.id,'nav');
});
it('ignores an old metadata request after its ownership changes', async () => {
 const t=setup();t.actions.loadRecipeNetwork();t.requestOwner.current='new-owner';await t.bindings();t.flush();
 assert.equal(t.window.document.getElementById('restored'),null);
});

it('retains logical focus when current bindings precede the portal DOM commit', async () => {
 const t=setup('shared-source-policy-value',{kind:'idle'},true);
 t.actions.restoreRecipeFocus();t.flush();t.actions.loadRecipeNetwork();await t.bindings();t.flush();
 assert(t.pending.current);assert.equal(t.window.document.activeElement.id,'nav');
 t.renderNetwork();t.actions.loadRecipeNetwork();t.flush();
 assert.equal(t.reads.length,1);assert.equal(t.window.document.activeElement.id,'restored');assert.equal(t.pending.current,undefined);
});
it('completes restoration when pending focus starts after cached bindings arrived', async () => {
 const t=setup('shared-source-policy-row-value',{kind:'idle'},true);
 t.actions.loadRecipeNetwork();await t.bindings();t.flush();
 t.actions.restoreRecipeFocus();t.flush();assert(t.pending.current);
 t.renderNetwork();t.actions.loadRecipeNetwork();t.flush();
 assert.equal(t.reads.length,1);assert.equal(t.window.document.activeElement.id,'restored');
});
for (const mode of ['navigation','output','unmount','new-edit']) {
 it(`late portal completion preserves ${mode} guard`, async () => {
  const t=setup('shared-source-policy-value',{kind:'idle'},true);
  t.actions.restoreRecipeFocus();t.flush();t.actions.loadRecipeNetwork();await t.bindings();t.flush();assert(t.pending.current);
  if(mode==='navigation')t.window.document.getElementById('next').focus();
  if(mode==='output')t.latest.current.selectedOutputId='another';
  if(mode==='unmount')t.attached.current=false;
  if(mode==='new-edit'){t.window.document.getElementById('next').focus();t.actions.rememberRecipeFocus();}
  t.renderNetwork();t.actions.loadRecipeNetwork();t.flush();assert.notEqual(t.window.document.activeElement.id,'restored');assert.equal(t.reads.length,1);
 });
}
