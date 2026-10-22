#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ROOT, readJson } from './lib/builder-route-evidence.mjs';

const ROUTE_ACTIONS = 'verification/builder-interactions/route-action-proposals.json';
const CONTRACT_PROOFS = 'verification/builder-interactions/contract-proof-proposals.json';
const OVERRIDES = 'verification/builder-interactions/builder-route-overrides.json';
const DECISIONS = 'verification/builder-interactions/proposal-decisions.json';

const args = process.argv.slice(2);
const check = args.includes('--check');
const apply = args.includes('--apply');
const init = args.includes('--init');
const force = args.includes('--force');
const selfTest = args.includes('--self-test');

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const digest = (value) => sha256(JSON.stringify(value));
const routeActionDigest = (entry) => digest({
  routeId: entry.routeId,
  proposedRouteActions: entry.proposedRouteActions,
  confidence: entry.confidence,
});
const contractProofDigest = (entry) => digest({
  routeId: entry.routeId,
  proposedStatus: entry.proposedStatus,
  proposedRouteActions: entry.proposedRouteActions,
  confidence: entry.confidence,
  browserCase: entry.browserCase,
  goTest: entry.goTest,
  uiTest: entry.uiTest,
});
const routeKey = (entry) => entry.method + ' ' + entry.path;

function emptyDecisions(routeActions, contractProofs) {
  return {
    schemaVersion: 1,
    generatedFrom: { routeActions: ROUTE_ACTIONS, contractProofs: CONTRACT_PROOFS },
    routeActionDecisions: routeActions.proposals.map((entry) => ({
      routeId: entry.routeId,
      decision: 'pending',
      actions: entry.proposedRouteActions,
      reason: '',
      reviewer: null,
      reviewedAt: null,
      proposalDigest: routeActionDigest(entry),
    })),
    contractProofDecisions: contractProofs.proposals.map((entry) => ({
      routeId: entry.routeId,
      decision: 'pending',
      status: entry.proposedStatus,
      browserCase: entry.browserCase,
      goTest: entry.goTest,
      uiTest: entry.uiTest,
      contractTest: null,
      reason: '',
      reviewer: null,
      reviewedAt: null,
      proposalDigest: contractProofDigest(entry),
    })),
  };
}

function validateDecisions(routeActions, contractProofs, decisions) {
  const errors = [];
  const counts = {
    routeActionPending: 0,
    routeActionAccepted: 0,
    routeActionRejected: 0,
    contractPending: 0,
    contractAccepted: 0,
    contractRejected: 0,
  };
  const routeProposals = new Map(routeActions.proposals.map((entry) => [entry.routeId, entry]));
  const contractProposals = new Map(contractProofs.proposals.map((entry) => [entry.routeId, entry]));
  const routeDecisions = new Map((decisions.routeActionDecisions ?? []).map((entry) => [entry.routeId, entry]));
  const contractDecisions = new Map((decisions.contractProofDecisions ?? []).map((entry) => [entry.routeId, entry]));

  for (const [routeId, proposal] of routeProposals) {
    const decision = routeDecisions.get(routeId);
    if (!decision) {
      errors.push('route action proposal has no decision: ' + routeId);
      continue;
    }
    if (decision.proposalDigest !== routeActionDigest(proposal)) errors.push('route action proposal changed: ' + routeId);
    if (!['pending', 'accept', 'reject'].includes(decision.decision)) errors.push('route action decision is invalid: ' + routeId);
    if (decision.decision === 'pending') counts.routeActionPending += 1;
    if (decision.decision === 'accept') {
      counts.routeActionAccepted += 1;
      if (!Array.isArray(decision.actions) || decision.actions.some((action) => typeof action !== 'string' || !action.trim())) {
        errors.push('route action accept needs a string action list: ' + routeId);
      }
      if ((decision.actions ?? []).length === 0 && !decision.reason) errors.push('empty route action accept needs a reason: ' + routeId);
    }
    if (decision.decision === 'reject') {
      counts.routeActionRejected += 1;
      if (!decision.reason) errors.push('route action reject needs a reason: ' + routeId);
    }
    if (decision.decision !== 'pending' && (!decision.reviewer || !decision.reviewedAt)) {
      errors.push('route action decision needs reviewer and reviewedAt: ' + routeId);
    }
  }
  for (const routeId of routeDecisions.keys()) {
    if (!routeProposals.has(routeId)) errors.push('route action decision has no proposal: ' + routeId);
  }

  for (const [routeId, proposal] of contractProposals) {
    const decision = contractDecisions.get(routeId);
    if (!decision) {
      errors.push('contract proof proposal has no decision: ' + routeId);
      continue;
    }
    if (decision.proposalDigest !== contractProofDigest(proposal)) errors.push('contract proof proposal changed: ' + routeId);
    if (!['pending', 'accept', 'reject'].includes(decision.decision)) errors.push('contract proof decision is invalid: ' + routeId);
    if (decision.decision === 'pending') counts.contractPending += 1;
    if (decision.decision === 'accept') {
      counts.contractAccepted += 1;
      if (!['contract-tested', 'unproven'].includes(decision.status)) errors.push('contract proof accept needs contract-tested or unproven: ' + routeId);
      if (decision.status === 'contract-tested' && !(decision.browserCase || decision.goTest || decision.uiTest || decision.contractTest)) {
        errors.push('contract-tested accept needs evidence: ' + routeId);
      }
      if (decision.status === 'unproven' && !decision.reason) errors.push('unproven accept needs a reason: ' + routeId);
      const routeDecision = routeDecisions.get(routeId);
      if (decision.status === 'contract-tested' && proposal.proposedRouteActions.length > 0 && routeDecision?.decision !== 'reject') {
        errors.push('contract-tested accept conflicts with pending or accepted route actions: ' + routeId);
      }
    }
    if (decision.decision === 'reject') {
      counts.contractRejected += 1;
      if (!decision.reason) errors.push('contract proof reject needs a reason: ' + routeId);
    }
    if (decision.decision !== 'pending' && (!decision.reviewer || !decision.reviewedAt)) {
      errors.push('contract proof decision needs reviewer and reviewedAt: ' + routeId);
    }
  }
  for (const routeId of contractDecisions.keys()) {
    if (!contractProposals.has(routeId)) errors.push('contract proof decision has no proposal: ' + routeId);
  }
  return { errors, counts };
}

function proofEntry(overrides, proposal) {
  overrides.proofs ??= [];
  const key = routeKey(proposal);
  let entry = overrides.proofs.find((candidate) => routeKey(candidate) === key);
  if (!entry) {
    entry = { method: proposal.method, path: proposal.path, proof: {} };
    overrides.proofs.push(entry);
  }
  entry.proof ??= {};
  return entry.proof;
}

function applyDecisions(overrides, decisions, routeActions, contractProofs) {
  const routeDecisions = new Map((decisions.routeActionDecisions ?? []).map((entry) => [entry.routeId, entry]));
  const contractDecisions = new Map((decisions.contractProofDecisions ?? []).map((entry) => [entry.routeId, entry]));
  let applied = 0;
  for (const proposal of routeActions.proposals) {
    const decision = routeDecisions.get(proposal.routeId);
    if (decision?.decision !== 'accept') continue;
    const proof = proofEntry(overrides, proposal);
    proof.routeActions = [...decision.actions];
    proof.routeActionReason = decision.reason || 'Reviewer accepted the route-action proposal.';
    applied += 1;
  }
  for (const proposal of contractProofs.proposals) {
    const decision = contractDecisions.get(proposal.routeId);
    if (decision?.decision !== 'accept') continue;
    const proof = proofEntry(overrides, proposal);
    proof.status = decision.status;
    proof.reason = decision.reason || 'Reviewer accepted the contract-proof proposal.';
    if (decision.status === 'unproven') {
      proof.browserCase = null;
      proof.goTest = null;
      proof.uiTest = null;
      proof.contractTest = null;
    } else {
      proof.browserCase = decision.browserCase || null;
      proof.goTest = decision.goTest || null;
      proof.uiTest = decision.uiTest || null;
      proof.contractTest = decision.contractTest || null;
    }
    applied += 1;
  }
  overrides.proofs.sort((left, right) => left.path.localeCompare(right.path) || left.method.localeCompare(right.method));
  return applied;
}

function checkApplied(overrides, decisions, routeActions, contractProofs) {
  const errors = [];
  const proofs = new Map((overrides.proofs ?? []).map((entry) => [routeKey(entry), entry.proof ?? {}]));
  const routeDecisions = new Map((decisions.routeActionDecisions ?? []).map((entry) => [entry.routeId, entry]));
  const contractDecisions = new Map((decisions.contractProofDecisions ?? []).map((entry) => [entry.routeId, entry]));
  for (const proposal of routeActions.proposals) {
    const decision = routeDecisions.get(proposal.routeId);
    if (decision?.decision !== 'accept') {
      const proof = proofs.get(routeKey(proposal));
      if (proof && proposal.proposedRouteActions.length > 0
        && JSON.stringify(proof.routeActions ?? []) === JSON.stringify(proposal.proposedRouteActions)) {
        errors.push('pending or rejected route action is already applied: ' + proposal.routeId);
      }
      continue;
    }
    const proof = proofs.get(routeKey(proposal));
    if (!proof) errors.push('accepted route action is not in overrides: ' + proposal.routeId);
    else {
      if (JSON.stringify(proof.routeActions ?? []) !== JSON.stringify(decision.actions)) errors.push('accepted route actions are not applied: ' + proposal.routeId);
      if (!proof.routeActionReason) errors.push('accepted route action has no reason in overrides: ' + proposal.routeId);
    }
  }
  for (const proposal of contractProofs.proposals) {
    const decision = contractDecisions.get(proposal.routeId);
    if (decision?.decision !== 'accept') continue;
    const proof = proofs.get(routeKey(proposal));
    if (!proof) {
      errors.push('accepted contract proof is not in overrides: ' + proposal.routeId);
      continue;
    }
    if (proof.status !== decision.status) errors.push('accepted contract status is not applied: ' + proposal.routeId);
    for (const field of ['browserCase', 'goTest', 'uiTest', 'contractTest']) {
      if ((proof[field] ?? null) !== (decision[field] ?? null)) errors.push('accepted contract ' + field + ' is not applied: ' + proposal.routeId);
    }
  }
  return errors;
}

function runSelfTest() {
  const assert = (condition, message) => {
    if (!condition) throw new Error('proposal decision self-test: ' + message);
  };
  const routeProposal = {
    routeId: 'post:test',
    method: 'POST',
    path: '/api/test',
    proposedRouteActions: ['DoThing'],
    confidence: 'high',
  };
  const contractProposal = {
    routeId: 'post:test',
    method: 'POST',
    path: '/api/test',
    proposedStatus: 'modeled-only',
    proposedRouteActions: ['DoThing'],
    confidence: 'high',
    browserCase: null,
    goTest: null,
    uiTest: null,
  };
  const routeActions = { proposals: [routeProposal] };
  const contractProofs = { proposals: [contractProposal] };
  const decisions = emptyDecisions(routeActions, contractProofs);
  decisions.routeActionDecisions[0] = {
    ...decisions.routeActionDecisions[0],
    decision: 'accept',
    actions: ['DoThing'],
    reason: 'Handler review complete.',
    reviewer: 'self-test',
    reviewedAt: '2026-01-01T00:00:00Z',
  };
  const validation = validateDecisions(routeActions, contractProofs, decisions);
  assert(validation.errors.length === 0, validation.errors.join('; '));
  const overrides = { proofs: [] };
  assert(applyDecisions(overrides, decisions, routeActions, contractProofs) === 1, 'did not apply the accepted route action');
  assert(overrides.proofs[0].proof.routeActions[0] === 'DoThing', 'did not write route actions');
  assert(checkApplied(overrides, decisions, routeActions, contractProofs).length === 0, 'applied decision did not verify');

  const stale = structuredClone(decisions);
  stale.routeActionDecisions[0].proposalDigest = 'stale';
  assert(validateDecisions(routeActions, contractProofs, stale).errors.some((error) => error.includes('changed')), 'did not detect stale proposal digest');

  const noEvidence = structuredClone(decisions);
  noEvidence.contractProofDecisions[0] = {
    ...noEvidence.contractProofDecisions[0],
    decision: 'accept',
    status: 'contract-tested',
    reviewer: 'self-test',
    reviewedAt: '2026-01-01T00:00:00Z',
  };
  assert(validateDecisions(routeActions, contractProofs, noEvidence).errors.some((error) => error.includes('needs evidence')), 'did not require contract evidence');

  const missingCoverage = structuredClone(decisions);
  missingCoverage.contractProofDecisions = [];
  assert(validateDecisions(routeActions, contractProofs, missingCoverage).errors.some((error) => error.includes('has no decision')), 'did not require decision coverage');
  console.log('PASS proposal decision self-test: coverage, digest, apply, and evidence checks');
}

if (selfTest) {
  runSelfTest();
  process.exit(0);
}

const routeActions = readJson(ROUTE_ACTIONS);
const contractProofs = readJson(CONTRACT_PROOFS);
if (init) {
  if (existsSync(resolve(ROOT, DECISIONS)) && !force) {
    console.error('FAIL ' + DECISIONS + ' exists; pass --force to replace every review decision');
    process.exit(1);
  }
  writeFileSync(resolve(ROOT, DECISIONS), JSON.stringify(emptyDecisions(routeActions, contractProofs), null, 2) + '\n');
  console.log('wrote ' + DECISIONS + ' with ' + routeActions.proposals.length + ' route action decisions and ' + contractProofs.proposals.length + ' contract proof decisions');
  process.exit(0);
}

const decisions = readJson(DECISIONS);
const overrides = readJson(OVERRIDES);
const validation = validateDecisions(routeActions, contractProofs, decisions);
if (validation.errors.length > 0) {
  for (const error of validation.errors) console.error('FAIL ' + error);
  process.exit(1);
}
if (apply) {
  const applied = applyDecisions(overrides, decisions, routeActions, contractProofs);
  writeFileSync(resolve(ROOT, OVERRIDES), JSON.stringify(overrides, null, 2) + '\n');
  console.log('applied ' + applied + ' accepted proposal decision(s) to ' + OVERRIDES + '; run node scripts/generate-builder-route-ledger.mjs');
  process.exit(0);
}
const appliedErrors = checkApplied(overrides, decisions, routeActions, contractProofs);
if (appliedErrors.length > 0) {
  for (const error of appliedErrors) console.error('FAIL ' + error);
  process.exit(1);
}
console.log('PASS proposal decisions: ' + validation.counts.routeActionPending + ' route-action pending, ' +
  validation.counts.routeActionAccepted + ' accepted, ' + validation.counts.routeActionRejected + ' rejected; ' +
  validation.counts.contractPending + ' contract-proof pending, ' + validation.counts.contractAccepted + ' accepted, ' +
  validation.counts.contractRejected + ' rejected');
