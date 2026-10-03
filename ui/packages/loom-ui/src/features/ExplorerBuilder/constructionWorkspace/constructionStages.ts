import type { Construction, ExplorerBuilderDocument } from '../../../types';

export const constructionInputStageFor = (
  construction: Construction | undefined,
  stepId: string,
): string => {
  const step = construction?.steps.find((candidate) => candidate.id === stepId);
  const input = step?.inputs[0];
  return input?.kind === 'STEP_OUTPUT' ? input.stepId : 'source_projection';
};

export const constructionAppendStageFor = (
  construction: Construction | undefined,
  rows: ExplorerBuilderDocument['rows'] | undefined,
): string => {
  const lastStep = construction?.steps.at(-1);
  if (rows?.kind === 'GROUPS' && rows.groups.source.kind === 'EXPLICIT' &&
      (!lastStep || rows.groups.afterStepId === lastStep.id)) {
    return 'group_rows';
  }
  return lastStep?.id ?? 'source_projection';
};
