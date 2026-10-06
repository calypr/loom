import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { relatedSourceProposalCandidate } from '../related-source-capture.mjs';

const proposalEntry = (rowValuePolicy) => ({
  path: '/construction-proposals',
  request: { candidateConstruction: { steps: [{
    id: 'step-related',
    operation: { kind: 'RELATED_SOURCE', relatedSource: {
      source: { candidateId: 'candidate-status', resourceType: 'Observation', path: 'status' },
      route: [{ fromResourceType: 'Patient', toResourceType: 'Observation' }],
      form: 'ALL', contributorRule: { policy: 'ALL_MATCHES' },
      ...(rowValuePolicy === undefined ? {} : { rowValuePolicy }),
    } },
  }] } },
});
const sourceFilter = { candidateId: 'candidate-status', resourceType: 'Observation', path: 'status' };
assert.equal(relatedSourceProposalCandidate(proposalEntry('ONE'), sourceFilter).rowValuePolicy, 'ONE');
assert.equal(relatedSourceProposalCandidate(proposalEntry('ALL'), sourceFilter).rowValuePolicy, 'ALL');
assert.equal(relatedSourceProposalCandidate(proposalEntry(undefined), sourceFilter).rowValuePolicy, 'ALL');
assert.equal(relatedSourceProposalCandidate(proposalEntry('ONE'), { ...sourceFilter, candidateId: 'other' }), undefined);

if (process.argv[2]) {
  const report = JSON.parse(await readFile(process.argv[2], 'utf8'));
  const captures = report.nativeRequests
    .map((entry) => ({ entry, match: relatedSourceProposalCandidate(entry, {
      candidateId: report.relatedFieldCandidate?.candidateId,
      resourceType: 'Observation', path: 'status',
    }) }))
    .filter(({ match }) => match);
  assert.equal(captures.length, 2, 'The matcher must find the exact two observed status RELATED_SOURCE proposals');
  for (const { entry, match } of captures) {
    assert.equal(entry.status, 200);
    assert.equal(match.related.form, 'ALL');
    assert.equal(match.related.contributorRule.policy, 'ALL_MATCHES');
    assert.equal(match.rowValuePolicy, 'ALL', 'Omitted policy defaults to ALL in this historical capture');
    assert.deepEqual(match.related.route.map((hop) => [hop.fromResourceType, hop.toResourceType]), [['Patient', 'Observation']]);
  }
}
process.stdout.write('related-source capture matcher: operation-level ONE/ALL/default-ALL classification passed\n');
