import type { Construction, ConstructionStageDescriptor, ConstructionStep, ExplorerBuilderDocument } from '../../../types';

type ConstructionInputStage = Pick<ConstructionStageDescriptor, 'id' | 'operation'>;

export const isSourceProjectionStage = (stage: ConstructionInputStage): boolean =>
  stage.id === 'source_projection' &&
  (stage.operation === undefined || stage.operation === 'SOURCE_PROJECTION');

export const constructionInputForStage = (stage: ConstructionInputStage): ConstructionStep['inputs'][number] =>
  isSourceProjectionStage(stage)
    ? { kind: 'SOURCE_PROJECTION' }
    : { kind: 'STEP_OUTPUT', stepId: stage.id };

export const sourceInputMatchesStage = (
  sourceInputStageId: string | undefined,
  stage: ConstructionInputStage,
): boolean => isSourceProjectionStage(stage) && sourceInputStageId === stage.id;

export const constructionInputStageFor = (
  construction: Construction | undefined,
  stepId: string,
): string => {
  const step = construction?.steps.find((candidate) => candidate.id === stepId);
  // A terminal Combine is compiled as its own receipt stage. Its inputs are
  // published/workspace outputs, so there is no source_projection stage in
  // that output to use as the predecessor for its saved editor capabilities.
  if (step?.operation.kind === 'COMBINE') return step.id;
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
