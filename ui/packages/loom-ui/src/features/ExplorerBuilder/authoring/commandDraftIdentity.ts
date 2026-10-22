export interface ServerDraftIdentity {
  readonly version: number;
  readonly digest: string;
}

export interface CommandDraftIdentity {
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest?: string;
}

export const commandDraftIdentity = (
  state: { readonly draftVersion: number },
  serverDraft: ServerDraftIdentity,
): CommandDraftIdentity => {
  const expectedDraftVersion = serverDraft.version > 0
    ? serverDraft.version
    : state.draftVersion;
  return {
    expectedDraftVersion,
    expectedDraftDigest: serverDraft.digest || undefined,
  };
};
