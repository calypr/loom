import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, readdirSync, existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root, tools, digest } from './formal-tools.mjs';
import { apiDoctor, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const coreProtocolModel = 'CoreAuthoringProtocol';
export const coreProtocolAdapter = 'internal/server/core_authoring_protocol_formal_replay_test.go';
export const coreProtocolTest = 'TestCoreAuthoringProtocolModelReplay';
export const coreProtocolConcreteTest = 'TestCoreAuthoringProtocolConcretePrefixReplay';
export const coreProtocolWitnessTest = 'TestCoreAuthoringProtocolVerticalWitness';
const modelDirectory = join(root, 'verification/builder-interactions');
const initialLabel = 'Patient ID';
const competingLabel = 'Patient Competing B';

// The ten invariants of CoreAuthoringProtocol.cfg in declaration order (TypeOK excluded).
export const coreProtocolProperties = ['Inv_EntryCASDecidesCompilation', 'Inv_CaptureEqualsCommittedHead', 'Inv_ReceiptPersistedForCapture', 'Inv_PreviewRequiresPersistedReceipt', 'Inv_ServerEchoesRequestedReceipt', 'Inv_NoAdmissionWithoutExecution', 'Inv_StaleAdmissionIsUnreachable', 'Inv_HeadAdvancedOnlyByCommittedSave', 'Inv_LostAckCannotAdmitAdvancedHead', 'Inv_StaleSaveDoesNotMoveHead'];

const files = () => ['go.mod', 'go.sum', coreProtocolAdapter, 'internal/server/core_authoring_protocol_test.go', ...['internal/explorer/lifecycle', 'internal/explorer'].flatMap(directory => readdirSync(join(root, directory)).filter(file => file.endsWith('.go') && !file.endsWith('_test.go')).map(file => `${directory}/${file}`))].sort();
export const coreProtocolFile = files();
export const coreProtocolSourceHashes = () => Object.fromEntries(files().map(file => [file, digest(join(root, file))]));

const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const key = value => JSON.stringify(canonical(value));
const capLabel = target => target === 1 ? initialLabel : competingLabel;
const labelForCap = receipt => receipt === 'cap1' ? initialLabel : competingLabel;
const capForLabel = label => label === initialLabel ? 'cap1' : label === competingLabel ? 'cap2' : undefined;
const cap = value => value === 1 ? 'cap1' : 'cap2';
const content = receipt => receipt === 'cap1' ? 1 : 2;
const caps = ['cap1', 'cap2'];

// The event-log taxonomy the Go adapter records at real call boundaries.
// Every name except 'Observe' corresponds to exactly one named model action
// (or branch); 'Observe' is the documented stutter (see checkCoreProtocolEventPath).
export const coreProtocolReplayEvents = ['SaveDispatch', 'SaveAck', 'REntryAccept', 'REntryReject', 'RCapture', 'RPersist', 'RComplete', 'PreviewExec', 'Observe'];

// The eight replay schedules the model explores at the production boundary:
// reconcile of head rTarget versus a competing save of head sTarget, with the
// save issued before reconcile entry, while compile is suspended, or omitted,
// optionally re-previewing after the head observably advanced. The client's
// save acknowledgement is not wire-observable and has no field here; the two
// mounted-driver vitest proofs own the acknowledgement/admission identities.
export const coreProtocolCases = [
  { id: 'S1', rTarget: 1, sTarget: 1, savePosition: 'before-entry', previewAfterAdvance: false },
  { id: 'S2', rTarget: 1, sTarget: 1, savePosition: 'in-compile', previewAfterAdvance: false },
  { id: 'S3', rTarget: 2, sTarget: 1, savePosition: 'before-entry', previewAfterAdvance: false },
  { id: 'S4', rTarget: 1, sTarget: 1, savePosition: 'none', previewAfterAdvance: false },
  { id: 'S5', rTarget: 2, sTarget: 1, savePosition: 'none', previewAfterAdvance: false },
  { id: 'S6', rTarget: 1, sTarget: 2, savePosition: 'before-entry', previewAfterAdvance: false },
  { id: 'S7', rTarget: 1, sTarget: 2, savePosition: 'in-compile', previewAfterAdvance: false },
  { id: 'S8', rTarget: 1, sTarget: 1, savePosition: 'in-compile', previewAfterAdvance: true },
];

// Plan C14: the model's head channels merge the two value-equal heads of an
// A -> B -> A prefix, so the merge's justification cannot come from the
// abstract schedules themselves (head identity is all they carry). The
// concrete-prefix lane replays exactly that prefix through the production
// boundary and records raw wire facts; the named claims below, adjudicated
// here, are what the C14 row requires: the revert's value identity is
// witnessed by the emitted receipt label while the stored digests stay
// pairwise distinct (ApplyCommands canonicalizes through migration and
// normalization on every save, so a value-equal revert legitimately stores a
// new digest — the model merges values, not stored forms), a stale client
// holding that value-equal head identity cannot own the current head, a
// fresh request at the real head completes with a persisted receipt, the
// same-request replay does not move or diverge the head, the preview echoes
// the persisted receipt, and the store persisted each replaced head as a
// distinct revision.
export const coreProtocolConcretePrograms = [{ id: 'C14-A-B-A' }];
export const coreProtocolConcreteProperties = ['C14_ValueMergeZoneRealized', 'C14_StaleValueEqualReconcileCannotOwnHead', 'C14_StaleDigestAtCurrentVersionCannotOwnHead', 'C14_StaleVersionWithCurrentDigestCannotOwnHead', 'C14_ValidFreshReconcileCompletes', 'C14_SameRequestReplayPreservesHeadAndReceipt', 'C14_PreviewEchoesPersistedValidReceipt', 'C14_PersistedHistoryHasDistinctHeadRevisions', 'C14_NoRecordedWireAnomalies'];

const schedule = replayCase => {
  const saveAttempted = replayCase.savePosition !== 'none';
  const saveCommitted = saveAttempted && replayCase.sTarget === 1;
  const entryHead = replayCase.savePosition === 'before-entry' && saveCommitted ? 2 : 1;
  const completed = replayCase.rTarget === entryHead;
  return { saveAttempted, saveCommitted, entryHead, completed };
};

const expectedObservation = replayCase => {
  const { saveAttempted, saveCommitted, completed } = schedule(replayCase);
  return {
    id: replayCase.id,
    reconcileStatus: completed ? 200 : 409,
    reconcilePhase: completed ? 'completed' : 'rejected',
    receiptReported: completed, receiptEmitted: completed,
    receiptLabel: completed ? capLabel(replayCase.rTarget) : '',
    previewStatus: completed ? 200 : 0, previewOk: completed,
    previewLabel: completed ? capLabel(replayCase.rTarget) : '',
    saveStatus: !saveAttempted ? 0 : saveCommitted ? 200 : 409,
    saveApplied: saveCommitted,
    headVersion: saveCommitted ? 2 : 1,
    headLabel: saveCommitted ? competingLabel : initialLabel,
    error: '',
  };
};

const initialStates = () => [1, 2].flatMap(rTarget => [1, 2].map(sTarget => ({
  headVersion: 1, rStatus: 'pending', rTarget, rCaptured: 0, sStatus: 'pending', sTarget,
  receipts: [], previewExecuted: [], previewEcho: 'none', clientCurrent: 1, admitted: 'none',
})));

function successors(state) {
  const next = [];
  const add = (action, edit) => next.push({ action, vars: { ...structuredClone(state), ...edit } });
  if (state.rStatus === 'pending') add('REntry', state.rTarget === state.headVersion
    ? { rStatus: 'entered', rCaptured: state.headVersion }
    : { rStatus: 'rejected409' });
  else if (state.rStatus === 'entered') add('RCapture', { rStatus: 'captured' });
  else if (state.rStatus === 'captured') add('RPersist', { rStatus: 'persisted', receipts: [...new Set([...state.receipts, cap(state.rCaptured)])].sort() });
  else if (state.rStatus === 'persisted') add('RComplete', { rStatus: 'completed' });
  if (state.sStatus === 'pending' && state.sTarget === state.headVersion && state.headVersion < 2) add('SCommit', { headVersion: state.headVersion + 1, sStatus: 'committed', admitted: 'none' });
  if (state.sStatus === 'pending' && state.sTarget !== state.headVersion) add('SCommitStale', { sStatus: 'rejected409', admitted: 'none' });
  if (state.sStatus === 'committed') add('SAck', { sStatus: 'acked', clientCurrent: state.headVersion, admitted: 'none' });
  for (const receipt of caps) if (state.receipts.includes(receipt)) add('Preview', { previewEcho: receipt, previewExecuted: [...new Set([...state.previewExecuted, receipt])].sort() });
  if (state.previewEcho !== 'none' && content(state.previewEcho) === state.clientCurrent) add('Admit', { admitted: state.previewEcho });
  return next;
}

// Mirrors scratchControl in scripts/verify-builder-formal-core.mjs, widened with
// the model's CONSTANT assignments so the worker can evaluate this model, and
// using a generated structural view because the shared statechart view does not
// name CoreAuthoringProtocol variables.
const controlView = "export const title = 'Core authoring protocol controls';\nexport const state = (v) => Object.entries(v).map(([name, value]) => `${name}=${JSON.stringify(value)}`).join('; ');\n";

function scratchControl([property, action, before, after], reportDirectory) {
  const scratch = mkdtempSync(join(tmpdir(), 'loom-core-protocol-control-'));
  try {
    const source = readFileSync(join(modelDirectory, `${coreProtocolModel}.tla`), 'utf8');
    const start = source.search(new RegExp(`\\n${action}(?:\\([^\\n]*\\))? ==`));
    const next = source.slice(start + 1).search(/\n[A-Za-z_]\w*(?:\\([^\n]*\\))? ==/);
    const end = next < 0 ? source.lastIndexOf('\n====') : start + 1 + next;
    assert.ok(start >= 0 && end > start, `Missing model action: ${action}`);
    const body = source.slice(start, end);
    assert.equal(body.split(before).length, 2, `Nonunique model mutation: ${property}`);
    writeFileSync(join(scratch, `${coreProtocolModel}.tla`), source.slice(0, start) + body.replace(before, after) + source.slice(end));
    writeFileSync(join(scratch, `${coreProtocolModel}.cfg`), `SPECIFICATION Spec\nCHECK_DEADLOCK FALSE\nCONSTANT Targets = {1, 2}\nCONSTANT InitialHead = 1\nINVARIANT ${property}\n`);
    writeFileSync(join(scratch, 'core.view.mjs'), controlView);
    const output = join(reportDirectory ?? scratch, 'statechart');
    const { scripts, jar } = tools();
    const result = spawnSync(process.execPath, [join(root, 'scripts/lib/formal-statechart-worker.mjs'), join(scratch, `${coreProtocolModel}.tla`), join(scratch, `${coreProtocolModel}.cfg`), join(scratch, 'core.view.mjs'), output],
      { cwd: root, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true, env: { ...process.env, TLA2TOOLS_JAR: jar, PSTACK_STATECHART_ROOT: scripts } });
    if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (!['ESRCH', 'EPERM'].includes(error.code)) throw error; } }
    assert.ok(existsSync(join(output, 'result.json')), `${property}: control produced no statechart result`);
    const checked = JSON.parse(readFileSync(join(output, 'result.json'), 'utf8'));
    const properties = (checked.counterexamples ?? []).map(edge => edge.property);
    assert.ok(result.status !== 0 && properties.length === 1 && properties[0] === property, `${property}: control must violate only the named invariant (exit ${result.status}: ${JSON.stringify(properties)})`);
    return checked;
  } finally { rmSync(scratch, { recursive: true, force: true }); }
}

export const coreProtocolModelControls = [
  [coreProtocolProperties[0], 'REntry', "rCaptured' = headVersion", "rCaptured' = 3 - headVersion"],
  [coreProtocolProperties[1], 'REntry', "rCaptured' = headVersion", "rCaptured' = 0"],
  [coreProtocolProperties[2], 'RPersist', "receipts' = receipts \\cup {Cap(rCaptured)}", "receipts' = receipts \\cup {Cap(3 - rCaptured)}"],
  [coreProtocolProperties[3], 'Preview', '/\\ r \\in receipts', '/\\ TRUE'],
  [coreProtocolProperties[4], 'Preview', "previewEcho' = r", "previewEcho' = IF r = \"cap1\" THEN \"cap2\" ELSE \"cap1\""],
  [coreProtocolProperties[5], 'Admit', "admitted' = previewEcho", "admitted' = IF previewEcho = \"cap1\" THEN \"cap2\" ELSE \"cap1\""],
  [coreProtocolProperties[6], 'Admit', '/\\ Content(previewEcho) = clientCurrent', '/\\ TRUE'],
  [coreProtocolProperties[7], 'SCommit', "/\\ sStatus' = \"committed\"", '/\\ UNCHANGED sStatus'],
  [coreProtocolProperties[8], 'Admit', '/\\ Content(previewEcho) = clientCurrent', '/\\ TRUE'],
  [coreProtocolProperties[9], 'SCommitStale', ' /\\ admitted\' = "none"\n /\\ UNCHANGED <<headVersion,', ' /\\ admitted\' = "none"\n /\\ headVersion\' = 2\n /\\ UNCHANGED <<'],
];

export function checkCoreProtocolModelControls(reportDirectory) {
  return coreProtocolModelControls.map((control, index) => {
    const checked = scratchControl(control, reportDirectory && join(reportDirectory, `control-${index}`));
    return { property: control[0], action: control[1], counterexample: checked.counterexamples[0].states.length };
  });
}

export function coreProtocolFrontier() {
  const states = new Map(), steps = [], queue = initialStates();
  while (queue.length) {
    const value = queue.shift(), id = key(value);
    if (states.has(id)) continue;
    states.set(id, value);
    for (const edge of successors(value)) { steps.push([id, edge.action, key(edge.vars)]); queue.push(edge.vars); }
  }
  return { states: [...states.values()], steps };
}

// --- Replay event-path validation -------------------------------------------------
// The Go adapter records one event per real call boundary in production call
// order. Here each event must extend the replica along EXACTLY ONE named model
// action's legal successor edge (single source of truth: the successors()
// function shared with the frontier — no duplicated step logic), and its claims
// must agree with the replica variables of the state reached. 'Observe' events
// (getBuilder reads) are the documented stutter: the model's Next has no stutter
// action over vars, so an Observe is allowed only when it changes no model
// variable and its claims (headVersion, hasReceipts) match the current replica
// state exactly. Wrong order, omitted relevant transitions and substituted
// identities therefore have no legal walk and are rejected as FORMAL_CONFORMANCE
// before any terminal comparison could mask them.
function eventPathError(message, replayCase, eventIndex, event, expectedAction, replica) {
  const available = [...new Set(successors(replica).map(edge => edge.action))];
  const witness = { schemaVersion: 1, kind: 'core-protocol-event-path', model: coreProtocolModel, case: replayCase, eventIndex, event: event ?? null, expectedAction: expectedAction ?? null, availableActions: available, replicaState: replica };
  const error = new Error(`FORMAL_CONFORMANCE: ${message}: case ${replayCase.id} event ${eventIndex}${expectedAction ? ` expected action ${expectedAction}` : ''}${available.length ? ` available ${available.join('|')}` : ''} (${key(event ?? null)})`);
  Object.assign(error, { code: 'FORMAL_CONFORMANCE', witness });
  return error;
}

function eventActionName(event) {
  switch (event.name) {
    case 'SaveDispatch': return event.claims.applied ? 'SCommit' : 'SCommitStale';
    case 'SaveAck': return 'SAck';
    case 'REntryAccept': case 'REntryReject': return 'REntry';
    case 'RCapture': return 'RCapture';
    case 'RPersist': return 'RPersist';
    case 'RComplete': return 'RComplete';
    case 'PreviewExec': return 'Preview';
    default: return undefined;
  }
}

function eventClaimsAgree(event, from, to) {
  const claims = event.claims;
  switch (event.name) {
    case 'SaveDispatch':
      return typeof claims.status === 'number' && typeof claims.applied === 'boolean' &&
        claims.status === (claims.applied ? 200 : 409) && claims.applied === (to.sStatus === 'committed');
    case 'SaveAck':
      return to.clientCurrent === claims.headVersion && to.headVersion === claims.headVersion;
    case 'REntryAccept':
      return [1, 2].includes(claims.captured) && to.rCaptured === claims.captured && to.rStatus === 'entered';
    case 'REntryReject':
      return claims.status === 409 && to.rStatus === 'rejected409';
    case 'RCapture':
      return to.rStatus === 'captured';
    case 'RPersist': {
      const receipt = capForLabel(claims.receiptLabel);
      return receipt !== undefined && to.rStatus === 'persisted' && to.receipts.includes(receipt) && receipt === cap(to.rCaptured);
    }
    case 'RComplete':
      return claims.status === 200 && to.rStatus === 'completed';
    case 'PreviewExec': {
      const receipt = capForLabel(claims.receiptLabel);
      return receipt !== undefined && claims.status === 200 && claims.previewLabel === claims.receiptLabel &&
        to.previewEcho === receipt && to.previewExecuted.includes(receipt);
    }
    default: return false;
  }
}

export function checkCoreProtocolEventPath(replayCase, events) {
  const seed = initialStates().find(state => state.rTarget === replayCase.rTarget && state.sTarget === replayCase.sTarget);
  assert.ok(seed, `No canonical replica initial state for case ${replayCase.id}`);
  assert.ok(Array.isArray(events), `Replay case ${replayCase.id} carries no event log`);
  let replica = structuredClone(seed);
  const names = [];
  for (let index = 0; index < events.length; index++) {
    const event = events[index];
    assert.ok(event && typeof event === 'object' && coreProtocolReplayEvents.includes(event.name) && event.claims && typeof event.claims === 'object' && !Array.isArray(event.claims),
      `Malformed core protocol replay event ${index} in case ${replayCase.id}`);
    names.push(event.name);
    if (event.name === 'Observe') {
      // Documented stutter: an observation may not change model variables and
      // must match the replica state it names exactly. A headVersion the
      // replica never reached has no stutter witness and is rejected.
      const matches = event.claims.headVersion === replica.headVersion &&
        (!Object.hasOwn(event.claims, 'hasReceipts') || event.claims.hasReceipts === (replica.receipts.length > 0)) &&
        Object.keys(event.claims).every(name => ['headVersion', 'hasReceipts'].includes(name));
      if (!matches) throw eventPathError('event claims diverge from model state (stutter abuse)', replayCase, index, event, 'Observe', replica);
      continue;
    }
    const expected = eventActionName(event);
    const candidates = successors(replica).filter(edge => edge.action === expected);
    if (!candidates.length) throw eventPathError('illegal replay prefix', replayCase, index, event, expected, replica);
    const agreeing = candidates.filter(edge => eventClaimsAgree(event, replica, edge.vars));
    if (!agreeing.length) throw eventPathError('event claims diverge from model state', replayCase, index, event, expected, replica);
    replica = structuredClone(agreeing[0].vars);
  }
  const count = name => names.filter(candidate => candidate === name).length;
  const rejected = (missing, detail) => { throw eventPathError(`illegal replay prefix (${missing}: ${detail})`, replayCase, -1, null, null, replica); };
  const { saveAttempted, saveCommitted, completed } = schedule(replayCase);
  if (!saveAttempted) {
    if (count('SaveDispatch') || count('SaveAck')) rejected('unrequested save', 'the schedule omits the save but the event stream dispatches one');
  } else {
    if (count('SaveDispatch') !== 1) rejected('omitted relevant transition', 'the schedule dispatches a save but the event stream lacks exactly one SaveDispatch');
    if (count('SaveAck') !== (saveCommitted ? 1 : 0)) rejected('omitted relevant transition', 'the acknowledgement events do not match the committed save outcome');
  }
  if (count('REntryAccept') + count('REntryReject') !== 1) rejected('omitted relevant transition', 'the scheduled reconcile has exactly one entry outcome');
  if (completed) {
    if (count('REntryAccept') !== 1 || count('REntryReject') !== 0 || count('RCapture') !== 1 || count('RPersist') !== 1 || count('RComplete') !== 1)
      rejected('omitted relevant transition', 'an accepted reconcile must show REntryAccept, RCapture, RPersist and RComplete in order');
    if (count('PreviewExec') !== (replayCase.previewAfterAdvance ? 2 : 1)) rejected('omitted relevant transition', 'a completed reconcile previews its persisted receipt the scheduled number of times');
    if (names.indexOf('RComplete') > names.indexOf('PreviewExec')) rejected('wrong order', 'previews execute only after the reconcile completed');
  } else if (count('REntryReject') !== 1 || count('RCapture') || count('RPersist') || count('RComplete') || count('PreviewExec')) {
    rejected('omitted relevant transition', 'a rejected reconcile ends at its entry rejection with no capture, persist or completion');
  }
  return replica;
}

function checkCoreProtocolTerminalConformance(replayCase, row, replica) {
  const derived = {
    headVersion: replica.headVersion,
    reconcilePhase: replica.rStatus === 'completed' ? 'completed' : 'rejected',
    reconcileStatus: replica.rStatus === 'completed' ? 200 : 409,
    receiptReported: replica.rStatus === 'completed',
    receiptEmitted: replica.receipts.length > 0,
    previewOk: replica.previewExecuted.length > 0,
    saveApplied: ['committed', 'acked'].includes(replica.sStatus),
    saveStatus: replica.sStatus === 'pending' ? 0 : replica.sStatus === 'rejected409' ? 409 : 200,
  };
  for (const name of Object.keys(derived)) if (row[name] !== derived[name])
    throw eventPathError(`event claims diverge from model state (terminal ${name})`, replayCase, -1, null, null, replica);
  if (replica.receipts.length && row.receiptLabel !== labelForCap(cap(replica.rCaptured)))
    throw eventPathError('event claims diverge from model state (receiptLabel)', replayCase, -1, null, null, replica);
  if (replica.previewExecuted.length && row.previewLabel !== labelForCap(replica.previewEcho))
    throw eventPathError('event claims diverge from model state (previewLabel)', replayCase, -1, null, null, replica);
}

function validateProductionSource(productionSource) {
  if (productionSource === undefined) return;
  assert.ok(productionSource !== null && typeof productionSource === 'object' && !Array.isArray(productionSource), 'Production source override must be a file map');
  for (const [file, text] of Object.entries(productionSource)) {
    assert.ok(files().includes(file), `Production source override outside the replay source set: ${file}`);
    assert.ok(typeof text === 'string' && text.trim().length > 0, `Production source override must be nonempty text: ${file}`);
    assert.ok(file.endsWith('.go'), `Production source override must be a Go source file: ${file}`);
  }
}

export function validateCoreProtocolGraph(graph, { productionSource } = {}) {
  validateProductionSource(productionSource);
  assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, coreProtocolModel);
  assert.equal(graph.modelSha256, digest(join(modelDirectory, `${coreProtocolModel}.tla`)), 'Stale core protocol model');
  assert.equal(graph.configSha256, digest(join(modelDirectory, `${coreProtocolModel}.cfg`)), 'Stale core protocol config');
  assert.deepEqual(Object.keys(graph.productionSha256).sort(), files(), 'Changed core protocol source set');
  for (const file of files()) if (!(productionSource !== undefined && Object.hasOwn(productionSource, file))) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production source: ${file}`);
  const expected = coreProtocolFrontier(), expectedStates = new Set(expected.states.map(key));
  assert.equal(graph.states.length, expected.states.length, 'Incomplete core protocol frontier');
  const states = new Map();
  for (const state of graph.states) {
    assert.equal(typeof state.id, 'string'); assert.equal(typeof state.start, 'boolean');
    assert.ok(!states.has(state.id), 'Duplicate state ID');
    states.set(state.id, state);
    assert.ok(expectedStates.delete(key(state.vars)), 'Unknown or duplicate core protocol state');
  }
  assert.equal(expectedStates.size, 0);
  const starts = graph.states.filter(state => state.start);
  assert.equal(starts.length, 4, 'Core protocol frontier must start at the four schedule seeds');
  const seeds = new Set(initialStates().map(key));
  for (const state of starts) assert.ok(seeds.has(key(state.vars)), 'Unexpected core protocol start state');
  const edges = new Set(expected.steps.map(key));
  assert.equal(graph.steps.length, edges.size, 'Incomplete core protocol edges');
  for (const edge of graph.steps) {
    assert.ok(states.has(edge.from) && states.has(edge.to), 'Dangling core protocol edge');
    assert.ok(edges.delete(key([key(states.get(edge.from).vars), edge.action, key(states.get(edge.to).vars)])), 'Unknown or duplicate core protocol edge');
  }
  assert.equal(edges.size, 0);
  for (const replayCase of coreProtocolCases) {
    const { saveAttempted, saveCommitted, completed } = schedule(replayCase);
    const saveStatuses = !saveAttempted ? ['pending'] : saveCommitted ? ['committed', 'acked'] : ['rejected409'];
    const admitted = states.values().some(state => {
      const vars = state.vars;
      return vars.rTarget === replayCase.rTarget && vars.sTarget === replayCase.sTarget
        && vars.headVersion === (saveCommitted ? 2 : 1)
        && vars.rStatus === (completed ? 'completed' : 'rejected409')
        && vars.rCaptured === (completed ? replayCase.rTarget : 0)
        && saveStatuses.includes(vars.sStatus)
        && (!completed || vars.receipts.includes(cap(replayCase.rTarget)));
    });
    assert.ok(admitted, `Core protocol schedule ${replayCase.id} has no reachable model terminal`);
  }
  return coreProtocolCases.map(replayCase => ({ ...replayCase, expected: expectedObservation(replayCase) }));
}

export async function coreProtocolGraph(output) {
  const { jar } = tools(), scratch = mkdtempSync(join(tmpdir(), 'loom-core-protocol-graph-'));
  try {
    const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(scratch, 'states'), '-config', `${coreProtocolModel}.cfg`, '-dump', 'dot,actionlabels', join(scratch, 'graph'), `${coreProtocolModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 });
    assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
    const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(tools().scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(tools().scripts, 'frontends/view.ts')).href)]);
    const parsed = parseDot(readFileSync(join(scratch, 'graph.dot'), 'utf8'));
    const graph = {
      schemaVersion: 1, model: coreProtocolModel,
      modelSha256: digest(join(modelDirectory, `${coreProtocolModel}.tla`)),
      configSha256: digest(join(modelDirectory, `${coreProtocolModel}.cfg`)),
      productionSha256: coreProtocolSourceHashes(),
      states: [...parsed.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([name, value]) => [name, json(value)])) })),
      steps: parsed.steps,
    };
    for (const state of graph.states) for (const name of ['receipts', 'previewExecuted']) if (Array.isArray(state.vars[name])) state.vars[name] = [...state.vars[name]].sort();
    validateCoreProtocolGraph(graph);
    if (output) writeFileSync(output, JSON.stringify(graph));
    return graph;
  } finally { rmSync(scratch, { recursive: true, force: true }); }
}

function propertyError(property, replayCase, observation) {
  const witness = { schemaVersion: 1, kind: 'core-protocol', model: coreProtocolModel, property, case: { id: replayCase.id, rTarget: replayCase.rTarget, sTarget: replayCase.sTarget, savePosition: replayCase.savePosition, previewAfterAdvance: replayCase.previewAfterAdvance }, expected: expectedObservation(replayCase), observation };
  const error = new Error(`${property}: ${key({ case: witness.case, observation })}`);
  Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness });
  return error;
}

const observationKeys = ['error', 'events', 'headLabel', 'headVersion', 'id', 'previewLabel', 'previewOk', 'previewStatus', 'receiptEmitted', 'receiptLabel', 'receiptReported', 'reconcilePhase', 'reconcileStatus', 'saveApplied', 'saveStatus'];

export function checkCoreProtocolResults(record, cases, { property } = {}) {
  assert.deepEqual(Object.keys(record).sort(), ['results', 'schemaVersion'].sort());
  assert.equal(record.schemaVersion, 1);
  assert.ok(Array.isArray(record.results));
  // When Arango mode is active the Go lane tags every witness row with its store
  // mode (dual replay); the store-less shape is the map-only witness. Every
  // store present must cover every case, and store values are closed.
  const dual = record.results.some(row => row && Object.hasOwn(row, 'store'));
  const rowKeys = dual ? [...observationKeys, 'store'] : observationKeys;
  const stores = [...new Set(record.results.map(row => row && row.store).filter(value => value !== undefined))];
  if (dual) assert.deepEqual([...stores].sort(), ['arango', 'map'], 'Dual replay must carry witness rows for BOTH store modes');
  assert.equal(record.results.length, cases.length * (dual ? stores.length : 1), 'Missing Go observations');
  const results = new Map();
  for (const row of record.results) {
    assert.deepEqual(Object.keys(row).sort(), [...rowKeys].sort(), 'Malformed core protocol observation');
    const store = row.store ?? 'map';
    if (dual) assert.ok(store === 'map' || store === 'arango', `Unknown store mode ${JSON.stringify(store)}`);
    assert.ok(!results.has(store + '/' + row.id), 'Duplicate observation');
    assert.ok(['completed', 'rejected'].includes(row.reconcilePhase));
    for (const name of ['previewOk', 'receiptEmitted', 'receiptReported', 'saveApplied']) assert.equal(typeof row[name], 'boolean', `Observation.${name}`);
    for (const name of ['headVersion', 'previewStatus', 'reconcileStatus', 'saveStatus']) assert.equal(typeof row[name], 'number', `Observation.${name}`);
    for (const name of ['error', 'headLabel', 'previewLabel', 'receiptLabel']) assert.equal(typeof row[name], 'string', `Observation.${name}`);
    assert.ok(row.headVersion >= 1 && row.headVersion <= 2, 'Head version outside the modeled range');
    assert.ok(Array.isArray(row.events), 'Core protocol observations must carry an ordered event log');
    for (const event of row.events) assert.ok(event && typeof event === 'object' && coreProtocolReplayEvents.includes(event.name) && event.claims && typeof event.claims === 'object' && !Array.isArray(event.claims), 'Malformed core protocol replay event');
    results.set(store + '/' + row.id, row);
  }
  const failures = [], conformance = [];
  for (const store of stores.length ? stores : ['map']) {
  for (const replayCase of cases) {
    const actual = results.get(store + '/' + replayCase.id);
    assert.ok(actual, 'Missing case');
    const { entryHead, completed } = schedule(replayCase);
    const local = [], fail = name => local.push(propertyError(name, replayCase, actual));
    if (actual.reconcilePhase === 'completed') {
      if (!(actual.receiptEmitted && actual.receiptLabel === capLabel(replayCase.rTarget))) fail(coreProtocolProperties[0]);
      if (!(actual.receiptEmitted && actual.receiptLabel === capLabel(entryHead))) fail(coreProtocolProperties[1]);
      if (!actual.receiptEmitted) fail(coreProtocolProperties[2]);
    }
    if ((actual.previewOk && !actual.receiptEmitted) || (actual.reconcilePhase === 'completed' && actual.receiptEmitted && !actual.previewOk)) fail(coreProtocolProperties[3]);
    if ((actual.previewOk && actual.previewLabel !== actual.receiptLabel) || (actual.reconcilePhase === 'completed' && actual.receiptEmitted && !actual.previewOk)) fail(coreProtocolProperties[4]);
    if (actual.previewOk && !(actual.receiptEmitted && actual.receiptLabel === capLabel(replayCase.rTarget))) fail(coreProtocolProperties[5]);
    if (actual.previewOk && actual.previewLabel === capLabel(2) && !(actual.headVersion === 2 && actual.saveApplied)) fail(coreProtocolProperties[6]);
    if (actual.headVersion === 2 && !actual.saveApplied) fail(coreProtocolProperties[7]);
    if (actual.previewOk && actual.receiptLabel === capLabel(2) && !actual.saveApplied) fail(coreProtocolProperties[8]);
    if (actual.saveStatus === 409 && !(actual.headVersion === 1 || replayCase.sTarget !== actual.headVersion)) fail(coreProtocolProperties[9]);
    failures.push(...local);
    try {
      const expected = expectedObservation(replayCase);
      for (const name of observationKeys.filter(name => name !== 'id' && name !== 'events')) assert.equal(actual[name], expected[name], `FORMAL_CONFORMANCE: ${name}`);
      // Replaying the recorded call boundaries against the model's legal
      // transitions is the conformance gate: terminal compatibility alone
      // could let an illegal production prefix land on a compatible final
      // state. Terminal fields are then re-checked against the replica
      // terminal state the event stream actually derived.
      const replica = checkCoreProtocolEventPath(replayCase, actual.events);
      checkCoreProtocolTerminalConformance(replayCase, actual, replica);
    } catch (error) { error.code = 'FORMAL_CONFORMANCE'; conformance.push(error); }
  }
  }
  const selected = property ? failures.find(error => error.property === property) : failures[0];
  if (selected) { selected.violations = failures.map(error => error.witness); throw selected; }
  if (conformance.length) throw conformance[0];
  if (failures.length) { const error = new Error(`FORMAL_CONFORMANCE: selected property ${property} did not fail; other properties failed`); error.code = 'FORMAL_CONFORMANCE'; throw error; }
  return {
    cases: record.results.length,
    stores: (stores.length ? stores : ['map']).length,
    completed: record.results.filter(row => row.reconcilePhase === 'completed').length,
    rejected: record.results.filter(row => row.reconcilePhase === 'rejected').length,
    previews: record.results.filter(row => row.previewOk).length,
    saveConflicts: record.results.filter(row => row.saveStatus === 409).length,
    saveAdvances: record.results.filter(row => row.saveApplied).length,
  };
}

// --- Concrete-prefix lane (plan C14) -------------------------------------------
// The Go lane records raw wire facts from the production handlers and never
// asserts, so every C14 guarantee is adjudicated here against the recorded
// row. A semantically wrong server therefore lands as a named red claim with
// a witness, not as an aborted or self-certifying witness.
const concreteRowKeys = ['id', 'seedVersion', 'seedDigest', 'saveBStatus', 'saveBDigest', 'saveAStatus', 'saveAVersion', 'saveADigest', 'mergeValueZone', 'staleAttempt', 'stale2Attempt', 'stale3Attempt', 'validAttempt', 'replayAttempt', 'previewStatus', 'previewReceipt', 'validLabel', 'revisions', 'staleError', 'stale2Error', 'stale3Error', 'staleNotes', 'notes'];
// The Go wire record omits these when empty (omitempty), so absence is a
// legal shape for them while every other key must always be present.
const concreteRowOptionalKeys = ['previewReceipt', 'validLabel', 'staleError', 'stale2Error', 'stale3Error', 'staleNotes', 'notes'];
const concreteAttemptKeys = ['requestId', 'draftVersion', 'draftDigest', 'success', 'headVersion', 'headDigest', 'hasReceipt', 'receiptId'];
const concreteRevisionKeys = ['draftDigest', 'draftVersion', 'found', 'revisionId'];

function concretePropertyError(propertyName, program, observation) {
  const witness = { schemaVersion: 1, kind: 'core-protocol-concrete', model: coreProtocolModel, property: propertyName, program: { id: program.id }, prefix: 'A -> B -> A (value-equal revert over the seed head)', observation };
  const error = new Error(`${propertyName}: ${key({ program: program.id, observation })}`);
  Object.assign(error, { code: 'FORMAL_PROPERTY', property: propertyName, witness });
  return error;
}

function validateConcreteRow(record) {
  assert.deepEqual(Object.keys(record).sort(), ['results', 'schemaVersion'].sort());
  assert.equal(record.schemaVersion, 1);
  assert.ok(Array.isArray(record.results));
  // Arango mode tags every concrete wire row with its store mode (dual
  // replay); the store-less shape is the map-only witness. Store values are
  // closed and handled per (store, program) below.
  const dual = record.results.some(row => row && typeof row === 'object' && Object.hasOwn(row, 'store'));
  const allowedKeys = dual ? [...concreteRowKeys, 'store'] : concreteRowKeys;
  const stores = [...new Set(record.results.map(row => row && typeof row === 'object' ? row.store : undefined).filter(value => value !== undefined))];
  if (dual) assert.deepEqual([...stores].sort(), ['arango', 'map'], 'Concrete dual replay must carry wire rows for BOTH store modes');
  const rows = new Map();
  for (const row of record.results) {
    assert.ok(row && typeof row === 'object' && !Array.isArray(row), 'Malformed concrete observation');
    const names = Object.keys(row);
    for (const name of concreteRowKeys) if (!concreteRowOptionalKeys.includes(name)) assert.ok(names.includes(name), `Concrete row omits ${name}`);
    for (const name of names) assert.ok(allowedKeys.includes(name), `Unexpected concrete row key ${name}`);
    const store = row.store ?? 'map';
    if (dual) assert.ok(store === 'map' || store === 'arango', `Unknown concrete store mode ${JSON.stringify(store)}`);
    for (const name of ['seedVersion', 'saveBStatus', 'saveAStatus', 'saveAVersion', 'previewStatus']) assert.equal(typeof row[name], 'number', `Concrete row ${name}`);
    for (const name of ['seedDigest', 'saveBDigest', 'saveADigest']) assert.equal(typeof row[name], 'string', `Concrete row ${name}`);
    assert.equal(typeof row.mergeValueZone, 'boolean');
    for (const name of ['previewReceipt', 'validLabel', 'staleError']) assert.ok(row[name] === undefined || typeof row[name] === 'string', `Concrete row ${name}`);
    for (const name of ['staleNotes', 'notes']) assert.ok(row[name] === undefined || (Array.isArray(row[name]) && row[name].every(entry => typeof entry === 'string')), `Concrete row ${name}`);
    for (const attempt of ['staleAttempt', 'stale2Attempt', 'stale3Attempt', 'validAttempt', 'replayAttempt']) {
      const value = row[attempt];
      assert.ok(value && typeof value === 'object' && !Array.isArray(value), `Concrete row ${attempt}`);
      const attemptNames = Object.keys(value);
      for (const name of concreteAttemptKeys) if (name !== 'receiptId') assert.ok(attemptNames.includes(name), `Concrete ${attempt} omits ${name}`);
      for (const name of attemptNames) assert.ok(concreteAttemptKeys.includes(name), `Unexpected ${attempt} key ${name}`);
      assert.ok(value.receiptId === undefined || typeof value.receiptId === 'string', `Concrete ${attempt}.receiptId`);
      assert.equal(typeof value.success, 'boolean', `Concrete ${attempt}.success`);
      assert.equal(typeof value.hasReceipt, 'boolean', `Concrete ${attempt}.hasReceipt`);
      for (const name of ['draftVersion', 'headVersion']) assert.equal(typeof value[name], 'number', `Concrete ${attempt}.${name}`);
      for (const name of ['requestId', 'draftDigest', 'headDigest']) assert.equal(typeof value[name], 'string', `Concrete ${attempt}.${name}`);
    }
    assert.ok(Array.isArray(row.revisions), 'Concrete row revisions');
    for (const revision of row.revisions) {
      assert.ok(revision && typeof revision === 'object' && !Array.isArray(revision), 'Malformed concrete revision record');
      assert.deepEqual(Object.keys(revision).sort(), concreteRevisionKeys, 'Malformed concrete revision keys');
      assert.equal(typeof revision.found, 'boolean');
      assert.equal(typeof revision.draftVersion, 'number');
      for (const name of ['revisionId', 'draftDigest']) assert.equal(typeof revision[name], 'string', `Concrete revision ${name}`);
    }
    assert.ok(!rows.has(store + '/' + row.id), 'Duplicate concrete observation');
    rows.set(store + '/' + row.id, row);
  }
  return { rows, stores: dual ? stores : ['map'] };
}

export function checkCoreProtocolConcreteResults(record, programs, { property } = {}) {
  const { rows, stores } = validateConcreteRow(record);
  assert.equal(record.results.length, programs.length * stores.length, 'Missing concrete observations');
  const failures = [];
  for (const store of stores) {
  for (const program of programs) {
    const row = rows.get(store + '/' + program.id);
    assert.ok(row, 'Missing concrete program');
    const local = [], fail = name => local.push(concretePropertyError(name, program, row));
    const valid = row.validAttempt, stale = row.staleAttempt, replay = row.replayAttempt;
    // Merge zone (value level): the two stored A digests are distinct from B
    // and from the seed's stored digest (normalizing save path), mergeValueZone
    // pins the digest-level fact as false, and the emitted receipt label of
    // the head-3 compile equals the seed column label (claimed under
    // ValidFreshReconcileCompletes) — value identity, not stored form, is
    // what the model's head channels merge.
    const digestsPairwiseDistinct = row.seedDigest !== '' && row.saveBDigest !== '' && row.saveADigest !== '' &&
      row.seedDigest !== row.saveBDigest && row.saveADigest !== row.seedDigest && row.saveADigest !== row.saveBDigest;
    if (!(row.seedVersion === 1 && row.mergeValueZone === false && digestsPairwiseDistinct && row.saveAStatus === 200 && row.saveAVersion === 3 && row.saveBStatus === 200)) fail(coreProtocolConcreteProperties[0]);
    if (!(stale.success === false && stale.draftVersion === 1 && stale.draftDigest === row.seedDigest && stale.headVersion === 3 && stale.headDigest === row.saveADigest && stale.headDigest !== stale.draftDigest && stale.hasReceipt === false && row.staleError === 'DRAFT_CONFLICT')) fail(coreProtocolConcreteProperties[1]);
    // Single-coordinate stale probes: with only the entry CAS version clause
    // removed, [2] stays green and [3] alone is admitted; with only the
    // digest clause removed, [2] alone is admitted; removing both turns all
    // three red. Each claim is therefore the unique falsifier of one clause.
    // The single-coordinate claims deliberately omit a hasReceipt condition
    // (unlike the first, both-stale probe, which is scheduled first and can
    // therefore witness receipt absence): under a single-clause fault the
    // admitted probe persists a receipt before the later probe's wire read,
    // and only the presented-identity comparisons are clause-deterministic.
    const stale2 = row.stale2Attempt, stale3 = row.stale3Attempt;
    if (!(stale2.success === false && stale2.draftVersion === 3 && stale2.draftDigest === row.seedDigest && stale2.headVersion === 3 && stale2.headDigest === row.saveADigest && row.stale2Error === 'DRAFT_CONFLICT')) fail(coreProtocolConcreteProperties[2]);
    if (!(stale3.success === false && stale3.draftVersion === 1 && stale3.draftDigest === row.saveADigest && stale3.headVersion === 3 && stale3.headDigest === row.saveADigest && row.stale3Error === 'DRAFT_CONFLICT')) fail(coreProtocolConcreteProperties[3]);
    if (!(valid.success === true && valid.draftVersion === 3 && valid.draftDigest === row.saveADigest && valid.headVersion === 3 && valid.headDigest === row.saveADigest && valid.hasReceipt === true && typeof valid.receiptId === 'string' && valid.receiptId !== '' && row.validLabel === initialLabel)) fail(coreProtocolConcreteProperties[4]);
    if (!(replay.success === true && replay.draftVersion === 3 && replay.draftDigest === row.saveADigest && replay.headVersion === 3 && valid.success === true && replay.headDigest === valid.headDigest && typeof replay.receiptId === 'string' && replay.receiptId !== '' && replay.receiptId === valid.receiptId)) fail(coreProtocolConcreteProperties[5]);
    if (!(row.previewStatus === 200 && typeof valid.receiptId === 'string' && valid.receiptId !== '' && row.previewReceipt === valid.receiptId)) fail(coreProtocolConcreteProperties[6]);
    if (!(row.revisions.length === 2
      && row.revisions[0].found === true && row.revisions[0].revisionId !== '' && row.revisions[0].draftVersion === 1 && row.revisions[0].draftDigest === row.seedDigest
      && row.revisions[1].found === true && row.revisions[1].revisionId !== '' && row.revisions[1].draftVersion === 2 && row.revisions[1].draftDigest === row.saveBDigest
      && row.revisions[0].revisionId !== row.revisions[1].revisionId)) fail(coreProtocolConcreteProperties[7]);
    if (!((row.notes ?? []).length === 0 && (row.staleNotes ?? []).length === 0)) fail(coreProtocolConcreteProperties[8]);
    failures.push(...local);
  }
  }
  const selected = property ? failures.find(error => error.property === property) : failures[0];
  if (selected) { selected.violations = failures.map(error => error.witness); throw selected; }
  if (failures.length) { const error = new Error(`FORMAL_CONFORMANCE: selected concrete claim ${property} did not fail; other claims failed`); error.code = 'FORMAL_CONFORMANCE'; throw error; }
  return { programs: record.results.length / stores.length, stores: stores.length, claims: coreProtocolConcreteProperties.length };
}

function runGoSchedule({ rows, test, inputEnv, outputEnv, observationName }, { productionSource, output, evidencePrefix = '' } = {}) {
  validateProductionSource(productionSource);
  const scratch = mkdtempSync(join(tmpdir(), 'loom-core-protocol-go-')), tool = apiDoctor(), before = coreProtocolSourceHashes();
  try {
    const input = join(scratch, 'input.json'), out = join(scratch, 'output.json');
    writeFileSync(input, JSON.stringify(rows));
    const inputSha = digest(input);
    const args = ['test', '-json', `-timeout=${goTestTimeout}`, '-count=1', '-run', `^${test}$`];
    if (productionSource !== undefined) {
      const overlay = join(scratch, 'overlay.json'), replacements = {};
      for (const [file, text] of Object.entries(productionSource)) {
        const replacement = join(scratch, `replacement-${Object.keys(replacements).length}.go`);
        writeFileSync(replacement, text);
        replacements[join(root, file)] = replacement;
      }
      writeFileSync(overlay, JSON.stringify({ Replace: replacements }));
      args.push('-overlay', overlay);
    }
    args.push('./internal/server');
    const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', [inputEnv]: input, [outputEnv]: out };
    if (tool.executable === '/workspace/.loom-setup/go/bin/go') { if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod'; if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache'; }
    const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true });
    if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (!['ESRCH', 'EPERM'].includes(error.code)) throw error; } }
    if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, `${evidencePrefix}go.jsonl`), result.stdout || ''); writeFileSync(join(output, `${evidencePrefix}process.json`), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); }
    assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
    checkApiEvents(result.stdout, { tests: [test] });
    assert.equal(digest(input), inputSha, 'Input changed during replay');
    assert.deepEqual(coreProtocolSourceHashes(), before, 'Production changed during replay');
    assert.ok(existsSync(out), 'No Go observations');
    const record = JSON.parse(readFileSync(out, 'utf8'));
    if (output) writeFileSync(join(output, observationName), JSON.stringify(record, null, 2));
    return record;
  } finally { rmSync(scratch, { recursive: true, force: true }); }
}

function runGo(cases, { productionSource, output } = {}) {
  return runGoSchedule({ rows: cases.map(({ id, rTarget, sTarget, savePosition, previewAfterAdvance }) => ({ id, rTarget, sTarget, savePosition, previewAfterAdvance })), test: coreProtocolTest, inputEnv: 'LOOM_FORMAL_CORE_PROTOCOL_INPUT', outputEnv: 'LOOM_FORMAL_CORE_PROTOCOL_OUTPUT', observationName: 'observations.json' }, { productionSource, output });
}

function runConcreteGo(programs, { productionSource, output } = {}) {
  return runGoSchedule({ rows: programs.map(({ id }) => ({ id })), test: coreProtocolConcreteTest, inputEnv: 'LOOM_FORMAL_CORE_PROTOCOL_CONCRETE_INPUT', outputEnv: 'LOOM_FORMAL_CORE_PROTOCOL_CONCRETE_OUTPUT', observationName: 'concrete-observations.json' }, { productionSource, output, evidencePrefix: 'concrete-' });
}

export async function checkCoreProtocolGraph(graph, { productionSource, output, witnessFile } = {}) {
  const cases = validateCoreProtocolGraph(graph, { productionSource });
  try {
    const result = checkCoreProtocolResults(runGo(cases, { productionSource, output }), cases);
    const concrete = checkCoreProtocolConcreteResults(runConcreteGo(coreProtocolConcretePrograms, { productionSource, output }), coreProtocolConcretePrograms);
    return { ...result, concrete, states: graph.states.length, transitions: graph.steps.length, boundary: 'generated strict public routes, real lifecycle service and CAS store with persisted receipts (map store by default; every schedule also replays against real ArangoDB when LOOM_TEST_ARANGO_URL is set); the compile/preview execution boundary is the repository test seam; client acknowledgement and admission identities are proved by the mounted-driver vitest suite; real-Arango head/receipt durability is proved by TestCoreAuthoringProtocolAgainstArango; the C14 value-identity prefix A -> B -> A (value merge zone, three stale probes isolating each reconcile entry-CAS clause, fresh completion, replay, preview echo, persisted distinct revisions) is replayed through the same production boundary by the concrete-prefix lane with claims adjudicated here' };
  } catch (error) {
    if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2));
    throw error;
  }
}

export async function checkCoreProtocolGraphControls(graph) {
  const mutations = [
    value => value.states.pop(),
    value => value.steps.pop(),
    value => value.steps.push({ ...value.steps[0] }),
    value => { value.steps[0].action = 'Unknown'; },
    value => { value.steps[0].to = 'missing'; },
    value => { value.modelSha256 = 'stale'; },
    value => { value.configSha256 = 'stale'; },
    value => { value.productionSha256[coreProtocolAdapter] = 'stale'; },
    value => { for (const state of value.states) state.start = false; },
    value => { const state = value.states.find(candidate => candidate.vars.receipts.includes('cap1')); state.vars.receipts = ['cap2']; },
  ];
  for (const mutation of mutations) { const changed = structuredClone(graph); mutation(changed); assert.throws(() => validateCoreProtocolGraph(changed)); }
  return mutations.length + checkCoreProtocolEventControls().length;
}

// Case-level controls proving the event validator bites: fabricated order,
// substituted identity, omitted transition and stutter abuse must all be
// rejected by the pure validator, and the legal stream must be accepted.
// These run over hand-built events, without touching Go.
function expectEventPath(control, replayCase, events, expectation) {
  let error;
  try { checkCoreProtocolEventPath(replayCase, structuredClone(events)); } catch (caught) { error = caught; }
  if (expectation === 'rejected') {
    assert.ok(error && error.code === 'FORMAL_CONFORMANCE', `Event-path control must reject ${control} with FORMAL_CONFORMANCE`);
    assert.ok(/illegal replay prefix|event claims diverge/.test(error.message), `Event-path control must name the prefix violation for ${control}: ${error?.message}`);
    assert.ok(error.witness && error.witness.kind === 'core-protocol-event-path', `Event-path control must attach a witness for ${control}`);
  } else assert.ifError(error, `Event-path control wrongly rejected the legal stream ${control}`);
  return { control, expectation };
}

export function checkCoreProtocolEventControls() {
  const replayCase = coreProtocolCases.find(candidate => candidate.id === 'S4');
  const legal = () => structuredClone([
    { name: 'Observe', claims: { headVersion: 1 } },
    { name: 'REntryAccept', claims: { captured: 1 } },
    { name: 'RCapture', claims: {} },
    { name: 'RPersist', claims: { receiptLabel: initialLabel } },
    { name: 'RComplete', claims: { status: 200 } },
    { name: 'PreviewExec', claims: { receiptLabel: initialLabel, status: 200, previewLabel: initialLabel } },
    { name: 'Observe', claims: { headVersion: 1, hasReceipts: true } },
  ]);
  const swapped = legal();
  [swapped[3], swapped[4]] = [swapped[4], swapped[3]];
  const substituted = legal();
  substituted[4] = { name: 'RPersist', claims: { receiptLabel: initialLabel } };
  substituted[5] = { name: 'PreviewExec', claims: { receiptLabel: competingLabel, status: 200, previewLabel: competingLabel } };
  const omitted = legal().filter((_, index) => index !== 2);
  const stutterAbuse = legal();
  stutterAbuse[6] = { name: 'Observe', claims: { headVersion: 2, hasReceipts: true } };
  return [
    expectEventPath('legal accepted stream', replayCase, legal(), 'accepted'),
    expectEventPath('fabricated order (RComplete before RPersist)', replayCase, swapped, 'rejected'),
    expectEventPath('substituted identity (PreviewExec claims a receipt never persisted)', replayCase, substituted, 'rejected'),
    expectEventPath('omitted transition (RCapture dropped from an accepted case)', replayCase, omitted, 'rejected'),
    expectEventPath('stutter abuse (Observe claims a head the replica never reached)', replayCase, stutterAbuse, 'rejected'),
  ];
}

function validateAbstractState(value) {
  assert.deepEqual(Object.keys(value).sort(), ['admitted', 'clientCurrent', 'headVersion', 'previewEcho', 'previewExecuted', 'rCaptured', 'rStatus', 'rTarget', 'receipts', 'sStatus', 'sTarget'].sort(), 'Malformed core protocol state');
  assert.ok([1, 2].includes(value.headVersion) && [1, 2].includes(value.clientCurrent));
  assert.ok(['pending', 'entered', 'captured', 'persisted', 'completed', 'rejected409'].includes(value.rStatus));
  assert.ok([1, 2].includes(value.rTarget) && [1, 2].includes(value.sTarget));
  assert.ok([0, 1, 2].includes(value.rCaptured));
  assert.ok(['pending', 'committed', 'acked', 'rejected409'].includes(value.sStatus));
  assert.ok(Array.isArray(value.receipts) && value.receipts.every(receipt => caps.includes(receipt)));
  assert.ok(Array.isArray(value.previewExecuted) && value.previewExecuted.every(receipt => caps.includes(receipt)));
  assert.ok(['none', ...caps].includes(value.previewEcho) && ['none', ...caps].includes(value.admitted));
}

// Model-fault successors per property, mirroring coreProtocolModelControls so a
// counterexample edge is exactly one supported model fault away from healthy.
function modelFaultSuccessors(state, property) {
  const healthy = successors(state);
  const edges = healthy.map(edge => ({ action: edge.action, vars: structuredClone(edge.vars) }));
  if (property === coreProtocolProperties[0] && state.rStatus === 'pending' && state.rTarget === state.headVersion) edges.find(edge => edge.action === 'REntry').vars.rCaptured = 3 - state.headVersion;
  if (property === coreProtocolProperties[1] && state.rStatus === 'pending' && state.rTarget === state.headVersion) edges.find(edge => edge.action === 'REntry').vars.rCaptured = 0;
  if (property === coreProtocolProperties[2] && state.rStatus === 'captured') edges.find(edge => edge.action === 'RPersist').vars.receipts = [...new Set([...state.receipts, cap(3 - state.rCaptured)])].sort();
  if (property === coreProtocolProperties[3]) for (const receipt of caps.filter(receipt => !state.receipts.includes(receipt))) edges.push({ action: 'Preview', vars: { ...structuredClone(state), previewEcho: receipt, previewExecuted: [...new Set([...state.previewExecuted, receipt])].sort() } });
  if (property === coreProtocolProperties[4]) for (const edge of edges.filter(edge => edge.action === 'Preview')) edge.vars.previewEcho = edge.vars.previewEcho === 'cap1' ? 'cap2' : 'cap1';
  if (property === coreProtocolProperties[5]) { const edge = edges.find(edge => edge.action === 'Admit'); if (edge) edge.vars.admitted = edge.vars.admitted === 'cap1' ? 'cap2' : 'cap1'; }
  if (property === coreProtocolProperties[6] || property === coreProtocolProperties[8]) if (state.previewEcho !== 'none' && content(state.previewEcho) !== state.clientCurrent) edges.push({ action: 'Admit', vars: { ...structuredClone(state), admitted: state.previewEcho } });
  if (property === coreProtocolProperties[7] && state.sStatus === 'pending' && state.sTarget === state.headVersion && state.headVersion < 2) { const edge = edges.find(edge => edge.action === 'SCommit'); if (edge) edge.vars.sStatus = 'pending'; }
  if (property === coreProtocolProperties[9] && state.sStatus === 'pending' && state.sTarget !== state.headVersion) { const edge = edges.find(edge => edge.action === 'SCommitStale'); if (edge) edge.vars.headVersion = 2; }
  return edges;
}

function violates(property, state) {
  if (property === coreProtocolProperties[0]) return state.rStatus === 'completed' && state.rCaptured !== state.rTarget;
  if (property === coreProtocolProperties[1]) return ['captured', 'persisted', 'completed'].includes(state.rStatus) && ![1, 2].includes(state.rCaptured);
  if (property === coreProtocolProperties[2]) return ['persisted', 'completed'].includes(state.rStatus) && !state.receipts.includes(cap(state.rCaptured));
  if (property === coreProtocolProperties[3]) return !state.previewExecuted.every(receipt => state.receipts.includes(receipt));
  if (property === coreProtocolProperties[4]) return state.previewEcho !== 'none' && !state.previewExecuted.includes(state.previewEcho);
  if (property === coreProtocolProperties[5]) return state.admitted !== 'none' && !state.previewExecuted.includes(state.admitted);
  if (property === coreProtocolProperties[6]) return state.admitted !== 'none' && content(state.admitted) !== state.clientCurrent;
  if (property === coreProtocolProperties[7]) return state.headVersion !== 1 && !['committed', 'acked'].includes(state.sStatus);
  if (property === coreProtocolProperties[8]) return state.sStatus === 'committed' && state.admitted === 'cap2';
  if (property === coreProtocolProperties[9]) return state.sStatus === 'rejected409' && !(state.headVersion === 1 || state.sTarget !== state.headVersion);
  return false;
}

function derivedCase(state) {
  const saveAttempted = state.sStatus !== 'pending';
  return {
    id: 'derived',
    rTarget: state.rTarget,
    sTarget: state.sTarget,
    savePosition: !saveAttempted ? 'none' : state.rStatus === 'pending' ? 'before-entry' : 'in-compile',
    previewAfterAdvance: false,
  };
}

export function coreProtocolCounterexampleWitness(counterexample) {
  assert.ok(counterexample && coreProtocolProperties.includes(counterexample.property), 'Unknown core protocol property');
  assert.ok(Array.isArray(counterexample.states) && counterexample.states.length > 1);
  const property = counterexample.property;
  assert.equal(counterexample.states[0].action, 'Initial', 'Counterexample must start at initial action');
  assert.ok(initialStates().some(seed => key(seed) === key(counterexample.states[0].vars)), 'Counterexample must start at a schedule seed');
  let changed = false;
  for (let index = 1; index < counterexample.states.length; index++) {
    const before = counterexample.states[index - 1].vars, row = counterexample.states[index];
    validateAbstractState(row.vars);
    assert.ok(modelFaultSuccessors(before, property).some(edge => edge.action === row.action && key(edge.vars) === key(row.vars)), 'Unsupported or fabricated core protocol counterexample edge');
    if (!successors(before).some(edge => edge.action === row.action && key(edge.vars) === key(row.vars))) changed = true;
  }
  const last = counterexample.states.at(-1).vars;
  assert.ok(changed, 'Counterexample contains no supported model fault');
  assert.ok(violates(property, last), `Counterexample does not violate the named core protocol invariant ${property}`);
  const replayCase = derivedCase(last);
  const known = coreProtocolCases.find(candidate => candidate.rTarget === replayCase.rTarget && candidate.sTarget === replayCase.sTarget && candidate.savePosition === replayCase.savePosition);
  return { schemaVersion: 1, kind: 'core-protocol', model: coreProtocolModel, property, case: known ?? replayCase, expected: expectedObservation(known ?? replayCase), violated: last };
}

export async function replayCoreProtocolWitness(witness, { productionSource, output } = {}) {
  assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'core-protocol'); assert.equal(witness.model, coreProtocolModel);
  assert.ok(coreProtocolProperties.includes(witness.property), 'Unknown core protocol property');
  const candidate = witness.case;
  assert.ok(typeof candidate.id === 'string' && candidate.id.length > 0);
  assert.ok([1, 2].includes(candidate.rTarget) && [1, 2].includes(candidate.sTarget));
  assert.ok(['before-entry', 'in-compile', 'none'].includes(candidate.savePosition));
  assert.equal(typeof candidate.previewAfterAdvance, 'boolean');
  const known = coreProtocolCases.find(replayCase => replayCase.id === candidate.id && replayCase.rTarget === candidate.rTarget && replayCase.sTarget === candidate.sTarget && replayCase.savePosition === candidate.savePosition && replayCase.previewAfterAdvance === candidate.previewAfterAdvance);
  if (known) assert.deepEqual(candidate, known, 'Witness schedule drifted from a known replay case');
  const expected = expectedObservation(candidate);
  assert.deepEqual(witness.expected, expected, 'Witness changed model expectation');
  const row = known ?? { ...candidate, expected };
  try {
    checkCoreProtocolResults(runGo([row], { productionSource, output }), [row], { property: witness.property });
    return { status: 'property-held', property: witness.property };
  } catch (error) {
    if (error.code !== 'FORMAL_PROPERTY') throw error;
    assert.equal(error.property, witness.property, 'Witness violated a different property');
    return { status: 'reproduced', ...error.witness };
  }
}

const reconcilingFile = 'internal/explorer/lifecycle/authoring.go', previewFile = 'internal/explorer/lifecycle/preview.go', saveFile = 'internal/explorer/service.go';

export const coreProtocolSourceProofs = [
  {
    model: coreProtocolModel, property: coreProtocolProperties[0],
    checkedProperties: [coreProtocolProperties[0], coreProtocolProperties[1], coreProtocolProperties[5]],
    kind: 'core-protocol-replay', branch: 'capture-revalidation-ignores-head-advance', edits: [[reconcilingFile, '\t\tif compileErr != nil {\n\t\t\treturn ReconcileResult{}, compileErr\n\t\t}\n\t\treturn ReconcileResult{Receipt: receipt, Workspace: workspace}, nil', '\t\tif compileErr != nil {\n\t\t\treturn ReconcileResult{}, compileErr\n\t\t}\n\t\tif current, reloadErr := s.store.Get(ctx, request.Project, request.ExplorerID); reloadErr == nil {\n\t\t\tif recomputed, decodeErr := authoringv2.DecodeWorkspace(current.DraftConfig); decodeErr == nil {\n\t\t\t\tif refreshed, refreshErr := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: recomputed, SnapshotToken: request.SnapshotToken}); refreshErr == nil {\n\t\t\t\t\treturn ReconcileResult{Receipt: refreshed, Workspace: recomputed}, nil\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t\treturn ReconcileResult{Receipt: receipt, Workspace: workspace}, nil']],
  },
  {
    model: coreProtocolModel, property: coreProtocolProperties[0],
    kind: 'core-protocol-replay', branch: 'entry-cas-ignored', edits: [[reconcilingFile, '\tif owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest {\n\t\treturn ReconcileResult{}, conflict("reconcile", "DRAFT_CONFLICT"', '\tif false && (owner.DraftVersion != request.DraftVersion || owner.DraftDigest != request.DraftDigest) {\n\t\treturn ReconcileResult{}, conflict("reconcile", "DRAFT_CONFLICT"']],
  },
  {
    model: coreProtocolModel, property: coreProtocolProperties[2],
    kind: 'core-protocol-replay', branch: 'publish-unpersisted-receipt', edits: [[reconcilingFile, '\t\tif compileErr != nil {\n\t\t\treturn ReconcileResult{}, compileErr\n\t\t}\n\t\treturn ReconcileResult{Receipt: receipt, Workspace: workspace}, nil\n\t}', '\t\tif compileErr != nil {\n\t\t\treturn ReconcileResult{}, compileErr\n\t\t}\n\t\treturn ReconcileResult{Receipt: &explorer.CompilationReceipt{ID: receipt.ID + "-unpersisted"}, Workspace: workspace}, nil\n\t}']],
  },
  {
    model: coreProtocolModel, property: coreProtocolProperties[3],
    checkedProperties: [coreProtocolProperties[3], coreProtocolProperties[4]],
    kind: 'core-protocol-replay', branch: 'preview-refuses-persisted-receipt', edits: [[previewFile, '\tcolumns := emittedColumnsForOutput(receipt, request.OutputID)\n\tresult := PreviewResult{Receipt: receipt, Columns: columns}', '\tcolumns := emittedColumnsForOutput(receipt, request.OutputID)\n\tcurrent, currentErr := s.store.Get(ctx, request.Project, request.ExplorerID)\n\tif currentErr != nil {\n\t\treturn PreviewResult{}, currentErr\n\t}\n\tfor _, emitted := range columns {\n\t\tif !strings.Contains(string(current.DraftConfig), emitted.Label) {\n\t\t\treturn PreviewResult{}, conflict("preview", "RECEIPT_STALE", "the receipt column is no longer in the current draft", nil, nil)\n\t\t}\n\t}\n\tresult := PreviewResult{Receipt: receipt, Columns: columns}']],
  },
  {
    model: coreProtocolModel, property: coreProtocolProperties[7],
    checkedProperties: [coreProtocolProperties[7], coreProtocolProperties[9]],
    kind: 'core-protocol-replay', branch: 'commit-before-cas', edits: [[saveFile, '\tif owner.DraftVersion != request.ExpectedDraftVersion || (request.ExpectedDraftDigest != "" && owner.DraftDigest != request.ExpectedDraftDigest) {\n\t\treturn nil, ErrDraftConflict\n\t}', '\tif owner.DraftVersion != request.ExpectedDraftVersion || (request.ExpectedDraftDigest != "" && owner.DraftDigest != request.ExpectedDraftDigest) {\n\t\t_, _ = s.store.SaveDraft(ctx, *owner, owner.DraftVersion, owner.DraftDigest)\n\t\treturn nil, ErrDraftConflict\n\t}']],
  },
  {
    model: coreProtocolModel, property: coreProtocolProperties[8], kind: 'vitest',
    suite: 'src/features/ExplorerBuilder/BuilderWorkspace.coreProtocol.formal.test.tsx',
    pattern: 'never admits an in-flight reconcile whose identity was superseded by a save, and previews only the current head',
    edits: [
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileAcceptance.ts', "  if (!reconcileResultIsCurrent(identity)) return 'obsolete';\n", ''],
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx', '        const value = await request.unwrap();\n        if (owner?.cancelled || !requestIsCurrent()) return undefined;\n', '        const value = await request.unwrap();\n'],
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx', '        if (execution.cancelled || !receipt || !identityIsCurrent()) return undefined;\n', '        if (!receipt) return undefined;\n'],
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx', '        if (owner?.cancelled || generation !== previewGeneration.current) return undefined;\n        const previewRequest = previewBuilder({\n', '        if (generation !== previewGeneration.current) return undefined;\n        const previewRequest = previewBuilder({\n'],
    ],
  },
  {
    model: coreProtocolModel, property: coreProtocolProperties[6], kind: 'vitest',
    suite: 'src/features/ExplorerBuilder/BuilderWorkspace.coreProtocol.formal.test.tsx',
    pattern: 'discards a preview whose echoed receipt identity does not match the accepted receipt',
    edits: [
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx', '            value.receiptId !== activeReceipt.receiptId ||\n', ''],
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx', '          preview.receiptId !== receipt.receiptId ||\n', ''],
      ['ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reducer.ts', '        preview.receiptId !== receipt.receiptId ||\n', ''],
    ],
  },
];

export async function checkCoreProtocolSourceProof(proof, graph, output) {
  const known = proof.branch !== undefined
    ? coreProtocolSourceProofs.find(row => row.kind !== 'vitest' && row.branch === proof.branch)
    : coreProtocolSourceProofs.find(row => row.kind === 'vitest' && row.property === proof.property && row.pattern === proof.pattern);
  assert.ok(known);
  assert.deepEqual(proof, known);
  if (proof.kind === 'vitest') {
    // The shared vitest harness registers these proofs in the
    // formal-source-proofs registry, which statically imports this module.
    // A cyclic dynamic import from within this module's own evaluation would
    // deadlock (Node ESM), so hand the proof to a child process whose entry
    // is not part of this module graph; the harness itself is unchanged.
    const sourceProofModule = pathToFileURL(join(root, 'scripts/lib/formal-source-proofs.mjs')).href;
    const script = `const { checkSourceProof } = await import(${JSON.stringify(sourceProofModule)}); await checkSourceProof(JSON.parse(process.env.LOOM_CORE_PROTOCOL_VITEST_PROOF), undefined, process.env.LOOM_CORE_PROTOCOL_VITEST_OUTPUT);`;
    const result = spawnSync(process.execPath, ['--input-type=module', '-e', script], { cwd: root, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true, env: { ...process.env, LOOM_CORE_PROTOCOL_VITEST_PROOF: JSON.stringify(proof), LOOM_CORE_PROTOCOL_VITEST_OUTPUT: output } });
    if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (!['ESRCH', 'EPERM'].includes(error.code)) throw error; } }
    assert.ifError(result.error);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    const evidenceFile = join(output, 'proof.json');
    assert.ok(existsSync(evidenceFile), `Vitest source proof child produced no evidence: ${proof.property}`);
    return JSON.parse(readFileSync(evidenceFile, 'utf8'));
  }
  mkdirSync(output, { recursive: true });
  const [[file, before, after]] = proof.edits;
  const original = readFileSync(join(root, file), 'utf8');
  assert.equal(original.split(before).length, 2, `Nonunique core protocol source fault: ${proof.branch}`);
  const broken = original.replace(before, after);
  const productionSource = { [file]: broken };
  await checkCoreProtocolGraph(graph, { output: join(output, 'positive') });
  const cases = validateCoreProtocolGraph(graph);
  const record = runGo(cases, { productionSource, output: join(output, 'negative') }), checked = [];
  for (const property of proof.checkedProperties || [proof.property]) {
    let failure;
    try { checkCoreProtocolResults(record, cases, { property }); } catch (error) { failure = error; }
    assert.ok(failure && failure.code === 'FORMAL_PROPERTY' && failure.property === property, `Fault did not yield named semantic violation ${property}`);
    const witness = failure.witness, filename = property === proof.property ? 'counterexample.json' : `${property}.json`;
    writeFileSync(join(output, filename), JSON.stringify(witness, null, 2));
    assert.equal((await replayCoreProtocolWitness(witness)).status, 'property-held');
    assert.equal((await replayCoreProtocolWitness(witness, { productionSource })).status, 'reproduced');
    checked.push(property);
  }
  await checkCoreProtocolGraph(graph, { output: join(output, 'restored') });
  const evidence = { model: coreProtocolModel, property: proof.property, checkedProperties: checked, kind: proof.kind, branch: proof.branch, sourceFiles: [proof.edits[0][0]], positive: true, negative: true, restored: true, witness: 'counterexample.json' };
  writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2));
  return evidence;
}

export async function selfTestCoreProtocolReplay(graph, output) {
  assert.deepEqual([...new Set(coreProtocolModelControls.map(control => control[0]))].sort(), coreProtocolProperties.slice().sort(), 'Model controls must cover every invariant');
  assert.deepEqual([...new Set(coreProtocolSourceProofs.flatMap(proof => proof.checkedProperties || [proof.property]))].sort(), coreProtocolProperties.slice().sort(), 'Source proofs must cover every invariant');
  assert.ok(coreProtocolSourceProofs.filter(proof => proof.kind !== 'vitest').every(proof => proof.edits.length === 1 && proof.edits[0][1] !== proof.edits[0][2] && !/^[a-z]+$/.test(proof.edits[0][0])), 'Source proof faults must change code and name a real file');
  const controls = checkCoreProtocolModelControls(join(output, 'model-controls'));
  const eventControls = checkCoreProtocolEventControls();
  const sourceControls = [];
  for (const proof of coreProtocolSourceProofs) sourceControls.push(await checkCoreProtocolSourceProof(proof, graph, join(output, proof.kind === 'vitest' ? `client-${proof.property}` : proof.branch)));
  return { modelControls: controls, eventControls, sourceControls };
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const [command, file, extra] = process.argv.slice(2);
  if (command === '--generate') {
    assert.ok(!extra || extra === '--self-test');
    assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Output must be empty');
    mkdirSync(file, { recursive: true });
    const graph = await coreProtocolGraph(join(file, 'graph.json'));
    const replay = await checkCoreProtocolGraph(graph, { output: join(file, 'go'), witnessFile: join(file, 'counterexample.json') });
    replay.graphControls = await checkCoreProtocolGraphControls(graph);
    if (extra) Object.assign(replay, await selfTestCoreProtocolReplay(graph, join(file, 'self-test')));
    writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2));
    console.log(JSON.stringify(replay));
  } else if (command === '--witness') {
    const record = JSON.parse(readFileSync(file, 'utf8'));
    const witness = record.counterexamples ? coreProtocolCounterexampleWitness(record.counterexamples.find(value => coreProtocolProperties.includes(value.property))) : record;
    const productionSource = extra !== undefined ? JSON.parse(readFileSync(extra, 'utf8')) : undefined;
    const result = await replayCoreProtocolWitness(witness, { productionSource });
    console.log(JSON.stringify(result));
    process.exitCode = result.status === 'reproduced' ? 1 : 0;
  } else throw new Error('Use --generate DIRECTORY [--self-test] or --witness FILE [PRODUCTION_SOURCE_MAP.json]');
}
