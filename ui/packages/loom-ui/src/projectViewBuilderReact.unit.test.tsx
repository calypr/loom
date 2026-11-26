// @vitest-environment jsdom
import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from './client/api';
import { LoomProvider, useQuery } from './react';

const pendingResponses: Array<(response: Response) => void> = [];
const makeClient = () => createLoomClient({
  fetch: vi.fn(() => new Promise<Response>((resolve) => pendingResponses.push(resolve))),
});

const StateProbe = ({ client, project }: { readonly client: ReturnType<typeof createLoomClient>; readonly project: string }) => {
  const state = useQuery((signal) => client.getProjectViewBuilder({ project }, { signal }), [client, project]);
  return <output>{state.data?.settings.project ?? 'loading'}</output>;
};

const stateFor = (project: string) => ({
  settings: { project, version: 1, defaultConfigId: '', defaultFrameId: '', catalogs: {} },
  configs: [], catalogs: [], canEdit: false,
});

describe('saved view query ownership', () => {
  it('does not publish an old project/client response after the host switches owners', async () => {
    const firstClient = makeClient();
    const secondClient = makeClient();
    const { rerender } = render(<LoomProvider client={firstClient}><StateProbe client={firstClient} project="project-a" /></LoomProvider>);
    rerender(<LoomProvider client={secondClient}><StateProbe client={secondClient} project="project-b" /></LoomProvider>);
    await waitFor(() => expect(pendingResponses).toHaveLength(2));
    await act(async () => pendingResponses[1]?.(new Response(JSON.stringify(stateFor('project-b')))));
    expect(screen.getByText('project-b')).toBeInTheDocument();
    await act(async () => pendingResponses[0]?.(new Response(JSON.stringify(stateFor('project-a')))));
    expect(screen.getByText('project-b')).toBeInTheDocument();
    expect(screen.queryByText('project-a')).not.toBeInTheDocument();
  });
});
