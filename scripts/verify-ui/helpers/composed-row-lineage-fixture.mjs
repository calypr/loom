const rowStages = ['stage1', 'stage2', 'stage3'];

export const selectBoundedComposedRootCandidates = (candidates, previewLimit = 25) => {
  if (!Array.isArray(candidates)) throw new TypeError('Composed root candidates must be an array');
  const eligible = candidate => candidate.hasRepeatedPatientLeaf === true
    && Array.isArray(candidate.overflowStages)
    && candidate.overflowStages.length === 0
    && rowStages.every(stage => Number.isInteger(candidate.stageCounts?.[stage])
      && candidate.stageCounts[stage] > 0 && candidate.stageCounts[stage] < previewLimit);
  const member = candidates.find(eligible);
  if (!member) throw new Error(`No repeated-Patient root has 1..${previewLimit - 1} exact rows at all three stages within the bounded candidate scan`);

  const decoy = candidates.find(candidate => candidate.rootID !== member.rootID && eligible(candidate));
  if (!decoy) throw new Error('The bounded candidate scan must include a distinct repeated-Patient root with complete preview-sized rows at all three stages');
  return { member, decoy };
};
