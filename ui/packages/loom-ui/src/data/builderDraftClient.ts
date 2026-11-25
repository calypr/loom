import { useMemo } from 'react';
import type { LoomClient } from '../client/api';
import { useLoomClient } from './provider';

export type BuilderDraftClient = Pick<LoomClient, 'getBuilder'>;

export function useBuilderDraftClient(): BuilderDraftClient {
  const client = useLoomClient();
  return useMemo(() => ({ getBuilder: client.getBuilder.bind(client) }), [client]);
}
