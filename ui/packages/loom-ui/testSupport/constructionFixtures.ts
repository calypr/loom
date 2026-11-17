import type {
  Construction,
  ConstructionCapabilitiesResponse,
  ConstructionStageDescriptor,
} from '../src/types';

type SourceStageOverrides = Partial<Omit<ConstructionStageDescriptor, 'id' | 'inputStageId' | 'columns' | 'capabilities'>> & {
  readonly id?: string;
  readonly inputStageId?: string;
  readonly operation?: string;
  readonly omitOperation?: boolean;
  readonly capabilities?: ConstructionStageDescriptor['capabilities'];
  readonly unavailableReason?: string;
};

export const sourceStage = (
  columns: ConstructionStageDescriptor['columns'],
  overrides: SourceStageOverrides = {},
): ConstructionStageDescriptor => {
  const {
    id = 'source_projection',
    inputStageId = '',
    operation = 'SOURCE_PROJECTION',
    omitOperation = false,
    capabilities = [
      { kind: 'FILTER', supported: true },
      { kind: 'DERIVE', supported: true },
      { kind: 'PIVOT', supported: false, reason: overrides.unavailableReason ?? 'Not needed by this test.' },
      { kind: 'UNPIVOT', supported: false, reason: overrides.unavailableReason ?? 'Not needed by this test.' },
    ],
    unavailableReason: _unavailableReason,
    omitOperation: _omitOperation,
    ...optionalFields
  } = overrides;
  return {
    id,
    inputStageId,
    ...(!omitOperation ? { operation } : {}),
    columns,
    capabilities,
    ...optionalFields,
  };
};

type ConstructionCapabilitiesFixtureOptions = {
  readonly selectedStage: ConstructionStageDescriptor;
  readonly stages?: ReadonlyArray<ConstructionStageDescriptor>;
  readonly stageId?: string;
  readonly copyStages?: boolean;
  readonly baseConstruction?: Construction;
  readonly snapshotToken?: string;
  readonly draftVersion?: number;
  readonly draftDigest?: string;
  readonly outputId?: string;
  readonly groupFieldChoices?: ReadonlyArray<ConstructionCapabilitiesResponse['groupFieldChoices'][number]>;
  readonly groupRowValueChoices?: ReadonlyArray<ConstructionCapabilitiesResponse['groupRowValueChoices'][number]>;
  readonly sourceInput?: ConstructionCapabilitiesResponse['sourceInput'];
  readonly workspaceInputs?: ReadonlyArray<ConstructionCapabilitiesResponse['workspaceInputs'][number]>;
  readonly overrides?: Partial<ConstructionCapabilitiesResponse>;
};

export const capabilitiesFor = ({
  selectedStage,
  stages = [selectedStage],
  stageId = selectedStage.id,
  copyStages = true,
  baseConstruction = { version: 1, steps: [] },
  snapshotToken = 'snapshot-1',
  draftVersion = 1,
  draftDigest = 'draft-digest-1',
  outputId = 'table-1',
  groupFieldChoices = [],
  groupRowValueChoices = [],
  sourceInput = { supported: false, stageId, choices: [] },
  workspaceInputs = [],
  overrides = {},
}: ConstructionCapabilitiesFixtureOptions): ConstructionCapabilitiesResponse => ({
  snapshotToken,
  draftVersion,
  draftDigest,
  outputId,
  stageId,
  baseConstruction,
  stages: copyStages ? [...stages] : stages as ConstructionCapabilitiesResponse['stages'],
  selectedStage,
  groupFieldChoices: [...groupFieldChoices],
  groupRowValueChoices: [...groupRowValueChoices],
  sourceInput,
  workspaceInputs: [...workspaceInputs],
  ...overrides,
});
