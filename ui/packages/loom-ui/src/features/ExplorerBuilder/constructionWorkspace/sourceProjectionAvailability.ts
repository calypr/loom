import type { ConstructionCapabilitiesResponse, ConstructionStageDescriptor } from '../../../types';

export type SourceProjectionAvailability = {
  readonly available: boolean;
  readonly reason: string;
};

export const sourceProjectionAvailability = (
  stages: ReadonlyArray<ConstructionStageDescriptor> | undefined,
  summary?: ConstructionCapabilitiesResponse['sourceProjectionAvailability'],
  selectedStageId?: string,
): SourceProjectionAvailability => {
  if (!stages) {
    return {
      available: false,
      reason: 'Loom is checking whether this table can accept source columns.',
    };
  }
  const source = stages.find((stage) => stage.id === 'source_projection');
  const finalStage = stages.at(-1);
  const selectedStage = selectedStageId
    ? stages.find((stage) => stage.id === selectedStageId)
    : finalStage;
  if (selectedStage?.capabilities.some((capability) => capability.kind === 'ROW_VALUES' && capability.supported)) {
    return { available: true, reason: 'Source values are populated from the records contributing to each grouped row.' };
  }
  if (summary) {
    if (!selectedStage || summary.stageId !== selectedStage.id) {
      return {
        available: false,
        reason: 'The starting-record field check does not match the current table stage. Reload the table and try again.',
      };
    }
    return summary.supported
      ? { available: true, reason: 'The compiler confirms starting-record fields can be carried into these rows.' }
      : { available: false, reason: 'Starting-record fields are not available for the current row shape.' };
  }
  if (!source || !source.rowIdentityColumn || !selectedStage?.rowIdentityColumn) {
    return {
      available: false,
      reason: 'Loom could not confirm that source columns preserve this table’s row identity.',
    };
  }
  if (selectedStage.id !== 'source_projection' || source.rowIdentityColumn !== selectedStage.rowIdentityColumn) {
    return {
      available: false,
      reason: 'Loom has not confirmed that starting-record fields can be carried into the current rows.',
    };
  }
  return {
    available: true,
    reason: 'Starting-record fields can be added to this table.',
  };
};
