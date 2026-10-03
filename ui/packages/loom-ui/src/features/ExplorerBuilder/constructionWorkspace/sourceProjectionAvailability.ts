import type { ConstructionStageDescriptor } from '../../../types';

export type SourceProjectionAvailability = {
  readonly available: boolean;
  readonly reason: string;
};

export const sourceProjectionAvailability = (
  stages: ReadonlyArray<ConstructionStageDescriptor> | undefined,
): SourceProjectionAvailability => {
  if (!stages) {
    return {
      available: false,
      reason: 'Loom is checking whether this table can accept source columns.',
    };
  }
  const source = stages.find((stage) => stage.id === 'source_projection');
  const finalStage = stages.at(-1);
  if (!source || !source.rowIdentityColumn || !finalStage?.rowIdentityColumn) {
    return {
      available: false,
      reason: 'Loom could not confirm that source columns preserve this table’s row identity.',
    };
  }
  if (finalStage.capabilities.some((capability) => capability.kind === 'ROW_VALUES' && capability.supported)) {
    return { available: true, reason: 'Source values are populated from the records contributing to each grouped row.' };
  }
  if (source.rowIdentityColumn !== finalStage.rowIdentityColumn) {
    return {
      available: false,
      reason: 'A saved reshape changes this table’s row identity. Remove or revise that step before adding source columns.',
    };
  }
  return {
    available: true,
    reason: 'The compiler confirms the current steps preserve source row identity.',
  };
};
