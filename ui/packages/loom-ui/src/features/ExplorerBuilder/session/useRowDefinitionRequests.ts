import { useMemo, useState, useSyncExternalStore } from 'react';
import type { LoomClient } from '../../../client/api';
import type { RowDefinitionChoicesResponse, RowDefinitionSelection } from '../../../contracts/types';
import { createStore } from '../../../data/createStore';
import { RequestOwner } from '../../../data/requests';

export type SelectionOption = {
  readonly value: string;
  readonly shapeValue: string;
  readonly shapeLabel: string;
  readonly policyLabel?: string;
  readonly selection: RowDefinitionSelection;
};

type SettingsState =
  | { readonly kind: 'closed' }
  | { readonly kind: 'loading' }
  | {
      readonly kind: 'editing';
      readonly choices: RowDefinitionChoicesResponse;
      readonly options: ReadonlyArray<SelectionOption>;
      readonly selectionId: string;
    }
  | { readonly kind: 'error'; readonly message: string };

export type RowDefinitionTransport = Pick<LoomClient,
  'listRowDefinitionChoices' | 'proposeRowDefinition' | 'getSelection' | 'createExplicitGroupRevision'
> & Partial<Pick<LoomClient, 'assessRowChange' | 'preview'>>;

function createRowDefinitionRequests() {
  const settings = createStore<SettingsState>({ kind: 'closed' });
  const requests = new RequestOwner();
  let previewTimer: ReturnType<typeof setTimeout> | undefined;
  let observedIdentity: string | undefined;
  let settingsLease = {};
  const clearTimer = () => {
    if (previewTimer !== undefined) clearTimeout(previewTimer);
    previewTimer = undefined;
  };

  return {
    getSnapshot: settings.getSnapshot,
    setSettings: settings.setSnapshot,
    subscribeInScope: (identity: string) => (listener: () => void) => {
      const unsubscribe = settings.subscribe(listener);
      if (observedIdentity !== identity) {
        clearTimer();
        requests.setIdentity(identity);
        observedIdentity = identity;
      }
      return () => {
        unsubscribe();
        queueMicrotask(() => {
          if (settings.hasSubscribers()) return;
          clearTimer();
          requests.cancelAll();
        });
      };
    },
    startProposal: (identity: string) => requests.start('proposal', identity),
    startReplacement: (identity: string) => requests.start('replacement', identity),
    cancelPendingPreview() {
      clearTimer();
      requests.cancel('proposal');
    },
    cancelReplacementRequest: () => requests.cancel('replacement'),
    schedulePreview(start: () => void) {
      previewTimer = setTimeout(() => {
        previewTimer = undefined;
        start();
      }, 300);
    },
    invalidateSettings() { settingsLease = {}; },
    captureSettings() {
      const lease = settingsLease;
      return () => settingsLease === lease;
    },
  };
}

export function useRowDefinitionRequests(client: RowDefinitionTransport, previewIdentity: string) {
  const [scope] = useState(createRowDefinitionRequests);
  const subscribe = useMemo(() => scope.subscribeInScope(previewIdentity), [scope, previewIdentity]);
  const settings = useSyncExternalStore(subscribe, scope.getSnapshot, scope.getSnapshot);
  return {
    settings,
    setSettings: scope.setSettings,
    cancelPendingPreview: scope.cancelPendingPreview,
    cancelReplacementRequest: scope.cancelReplacementRequest,
    schedulePreview: scope.schedulePreview,
    invalidateSettings: scope.invalidateSettings,
    captureSettings: scope.captureSettings,
    startProposal: () => scope.startProposal(previewIdentity),
    startReplacement: () => scope.startReplacement(previewIdentity),
    loadChoices: (args: Parameters<LoomClient['listRowDefinitionChoices']>[0]) => client.listRowDefinitionChoices(args),
    proposeRowDefinition: (args: Parameters<LoomClient['proposeRowDefinition']>[0], signal: AbortSignal) =>
      client.proposeRowDefinition(args, signal),
    loadCandidatePreview: (args: Parameters<LoomClient['preview']>[0], signal: AbortSignal) => {
      if (!client.preview) throw new Error('The candidate preview is unavailable.');
      return client.preview(args, signal);
    },
    replacementTransport: (): Required<Pick<RowDefinitionTransport, 'assessRowChange' | 'preview'>> | undefined => {
      const assessRowChange = client.assessRowChange;
      const preview = client.preview;
      return assessRowChange && preview ? { assessRowChange, preview } : undefined;
    },
  };
}
