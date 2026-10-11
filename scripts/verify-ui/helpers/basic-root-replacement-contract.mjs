import { isDeepStrictEqual } from 'node:util';

const nonEmptyString = value => typeof value === 'string' && value.trim().length > 0;
const effectKeys = ['preservedFeatureKeys', 'removedFeatureKeys', 'workspaceOutputConsumers', 'sharedFilterBindings'];

export const explicitResourceRefs = ({ project, generation, resourceType, ids }) =>
  ids.map(id => ({ project, generation, resourceType, id }));

export const workspaceTabIdentitySnapshot = builder => {
  const tabs = builder?.workspace?.tabs;
  if (!Array.isArray(tabs)) throw new TypeError('Builder workspace tabs are required for an identity snapshot.');
  return [...tabs]
    .sort((left, right) => left.order - right.order)
    .map(tab => ({
      id: tab.id,
      outputId: tab.outputId,
      order: tab.order,
      visible: tab.visible ?? null,
      title: tab.title,
    }));
};

export const workspaceColumnConsumerEvidence = (builder, { sourceOutputId, columnId } = {}) => {
  if (!nonEmptyString(sourceOutputId) || !nonEmptyString(columnId)) {
    throw new TypeError('Workspace consumer evidence requires the source output and projected column IDs.');
  }
  return (builder?.workspace?.documents ?? []).flatMap(document =>
    (document.construction?.steps ?? []).flatMap(step => {
      const projections = step.operation?.combine?.projections ?? [];
      return (step.inputs ?? []).flatMap((input, inputIndex) => {
        if (input?.kind !== 'WORKSPACE_OUTPUT' || input.outputId !== sourceOutputId) return [];
        const projectsColumn = projections.some(projection =>
          projection.inputIndex === inputIndex && projection.inputColumnId === columnId);
        return projectsColumn ? [{
          outputId: document.output?.id,
          stepId: step.id,
          inputIndex,
          columnId,
        }] : [];
      });
    }),
  );
};

export const replacementAssessmentMatches = (body, expected) => {
  const failures = [];
  if (body?.mode !== 'REPLACE') failures.push('assessment must opt into REPLACE');
  if (body?.outputId !== expected.outputId) failures.push('assessment output differs from the existing table');
  if (body?.rootNodeId !== expected.rootNodeId) failures.push('assessment root differs from the selected replacement type');
  if (body?.snapshotToken !== expected.snapshotToken) failures.push('assessment snapshot differs from the saved source');
  if (body?.draftVersion !== expected.draftVersion) failures.push('assessment draft version differs from the saved source');
  if (body?.draftDigest !== expected.draftDigest) failures.push('assessment draft digest differs from the saved source');
  if (Object.hasOwn(body ?? {}, 'rootOccurrenceId')) failures.push('replacement assessment must not request a preserved occurrence route');
  return { ok: failures.length === 0, failures };
};

export const replacementProposalMatches = (proposal, expected) => {
  const failures = [];
  if (proposal?.mode !== 'REPLACE') failures.push('proposal must retain replacement mode');
  if (proposal?.outputId !== expected.outputId) failures.push('proposal must retain the existing output identity');
  if (proposal?.rootNodeId !== expected.rootNodeId) failures.push('proposal must target the selected replacement type');
  if (proposal?.rootOccurrenceId !== 'base') failures.push('replacement must seed the target root at the base occurrence');
  if (!nonEmptyString(proposal?.sourceDocumentDigest)) failures.push('proposal must bind a non-empty pre-replacement document digest');
  else if (expected.sourceDocumentDigest !== undefined && proposal.sourceDocumentDigest !== expected.sourceDocumentDigest) {
    failures.push('proposal source digest must remain bound to the exact pre-replacement document');
  }
  if (proposal?.catalogSnapshotToken !== expected.snapshotToken) failures.push('proposal must bind the authorized catalog snapshot');
  if (proposal?.catalogGeneration !== expected.catalogGeneration) failures.push('proposal must bind the authorized catalog generation');
  if (!nonEmptyString(proposal?.catalogDigest)) failures.push('proposal must bind a non-empty authorized catalog digest');
  else if (expected.catalogDigest !== undefined && proposal.catalogDigest !== expected.catalogDigest) {
    failures.push('proposal catalog digest must match the exact digest retained from the prior assessment');
  }
  if (!Array.isArray(proposal?.routeRebase) || proposal.routeRebase.length !== 0) {
    failures.push('replacement proposal must clear all routes');
  }
  for (const key of effectKeys) {
    if (!Array.isArray(proposal?.[key])) failures.push(`proposal must include the server-authored ${key} array`);
  }
  if (Array.isArray(proposal?.preservedFeatureKeys) && proposal.preservedFeatureKeys.length !== 0) {
    failures.push('replacement proposal must preserve no source features');
  }
  return { ok: failures.length === 0, failures };
};

export const replacementAssessmentEffectsMatch = (assessment, proposal) => {
  const failures = [];
  for (const key of effectKeys) {
    if (!Array.isArray(assessment?.[key])) failures.push(`assessment must include its ${key} array`);
    else if (!isDeepStrictEqual(assessment[key], proposal?.[key])) {
      failures.push(`assessment ${key} must match the reviewed proposal`);
    }
  }
  return { ok: failures.length === 0, failures };
};

export const replacementApplyCommandFromRequest = body => {
  const commands = Array.isArray(body?.commands) ? body.commands : [];
  const matches = commands.filter(command => command?.type === 'APPLY_TABLE_ROOT_REBASE' && command?.rowChange?.mode === 'REPLACE');
  return { command: matches.length === 1 ? matches[0] : undefined, commandCount: commands.length };
};

export const replacementApplyCommandMatches = (body, proposal) => {
  const { command, commandCount } = replacementApplyCommandFromRequest(body);
  const failures = [];
  if (commandCount !== 1) failures.push('replacement Apply must be the sole command in its atomic request');
  if (!command) failures.push('Apply must use APPLY_TABLE_ROOT_REBASE with a replacement proposal');
  if (!isDeepStrictEqual(command?.rowChange, proposal)) failures.push('Apply must carry the exact reviewed proposal');
  return { ok: failures.length === 0, failures, command };
};
