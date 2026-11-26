import { useMemo, useSyncExternalStore } from 'react';
import type { AutomaticPreviewOptions } from './automaticPreviewWorkflow';

export const useAutomaticPreview = ({ request, enabled, refresh, owner }: AutomaticPreviewOptions) => {
  owner.update(request, refresh);
  const resource = useMemo(() => owner.createResource(enabled), [owner, request?.key, enabled]);
  const snapshot = useSyncExternalStore(resource.subscribe, resource.getSnapshot, resource.getSnapshot);
  return owner.read(resource, snapshot);
};
