import type { ExplorerBuilderCatalog, ExplorerBuilderState } from '../../../contracts/types';
import { stateFromBuilder, type BuilderAuthoringState } from '../model/authoring';

const emptyCatalog = (): ExplorerBuilderCatalog => ({
  snapshotToken: '',
  generation: '',
  routePolicy: {},
  nodes: [],
  edges: [],
  candidates: [],
});
const emptyBuilderState = (project: string): BuilderAuthoringState => ({
  project,
  explorerId: 'dataframe-default',
  catalog: emptyCatalog(),
  workspace: null,
  draftVersion: 0,
  draftDigest: '',
  previewRequestVersion: 0,
  tables: [],
  selectedOccurrenceId: 'base',
  diagnostics: [],
  dirty: false,
  reconciliation: 'idle',
});

export const builderDataKeyFor = (
  ownerKey: string,
  value: {
    readonly catalog: ExplorerBuilderCatalog;
    readonly workspace: unknown;
    readonly draftVersion: number;
    readonly draftDigest: string;
  },
) =>
  `${ownerKey}:${value.catalog.snapshotToken}:${value.draftVersion}:${value.draftDigest}:${JSON.stringify(value.workspace)}`;

export type LocalDraft = {
  readonly key: string;
  readonly value: BuilderAuthoringState;
};

type DraftScope = {
  readonly projectId: string;
  readonly effectiveExplorerId: string;
  readonly ownerKey: string;
  readonly rememberedOutputId?: string;
};

export function hydrateDraft(
  server: ExplorerBuilderState | undefined,
  local: LocalDraft | undefined,
  { projectId, effectiveExplorerId, ownerKey, rememberedOutputId }: DraftScope,
): BuilderAuthoringState {
  if (!server) return emptyBuilderState(projectId);
  return stateFromBuilder(server, {
    project: projectId,
    explorerId: effectiveExplorerId,
    selectedOutputId: local?.key.startsWith(`${ownerKey}:`)
      ? local.value.selectedOutputId
      : rememberedOutputId,
  });
}
