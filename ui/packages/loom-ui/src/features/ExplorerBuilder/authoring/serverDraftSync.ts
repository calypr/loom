export type ServerDraftIdentity = {
  readonly draftVersion: number;
  readonly draftDigest: string;
};

export const shouldSyncServerDraft = (
  builderData: ServerDraftIdentity | null | undefined,
  currentKey: string,
  nextKey: string,
): builderData is ServerDraftIdentity =>
  Boolean(builderData) && currentKey !== nextKey;
