import { describe, expect, it, vi } from 'vitest';
import { createLoomClient, getProjectViewBuilderState, projectViewBuilderPath } from './api';
import type { ProjectViewBuilderState } from './projectViewBuilder';

const state: ProjectViewBuilderState = {
  settings: { project: 'HTAN_INT/BForePC', version: 7, defaultConfigId: 'metadata', defaultFrameId: 'clinical_metadata/Specimen', catalogs: { clinical_metadata: 'catalog-r3' } },
  configs: [],
  catalogs: [],
  canEdit: true,
};

const response = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { 'content-type': 'application/json' } });

describe('saved view client', () => {
  it('uses the authenticated Loom transport and GET has no adoption side effect', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockResolvedValue(response(state));
    const client = createLoomClient({ fetch, headers: { 'X-CSRF-Token': 'csrf' } });
    await expect(client.getProjectViewBuilder({ project: state.settings.project })).resolves.toEqual(state);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/projects/HTAN_INT%252FBForePC/view-builder');
    expect(fetch.mock.calls[0]?.[1]?.method).toBeUndefined();
    expect(new Headers(fetch.mock.calls[0]?.[1]?.headers).get('X-CSRF-Token')).toBe('csrf');
    expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining('/adopt'), expect.anything());
  });

  it('invalidates the project-scoped GET snapshot after a successful mutation', async () => {
    const updated = { ...state, settings: { ...state.settings, version: 8 } };
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(response(state))
      .mockResolvedValueOnce(response(updated))
      .mockResolvedValueOnce(response(updated));
    const client = createLoomClient({ fetch });
    await expect(client.getProjectViewBuilder({ project: state.settings.project })).resolves.toEqual(state);
    await client.saveProjectViewDefaults({ project: state.settings.project, request: { expectedVersion: 7, defaultConfigId: '', defaultFrameId: '' } });
    await expect(client.getProjectViewBuilder({ project: state.settings.project })).resolves.toEqual(updated);
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it('retains the version and revision compare-and-swap values on writes', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => response(state));
    const client = createLoomClient({ fetch });
    await client.saveProjectViewConfig({ project: state.settings.project, configId: 'metadata', request: { expectedRevisionId: 'config-r4', catalogRevisionId: 'catalog-r3', config: { id: 'metadata', title: 'Metadata', recipeRef: { name: 'clinical_metadata' }, views: [], sharedFilters: {}, fileActions: { extensions: {}, actions: {} } } } });
    await client.saveProjectViewDefaults({ project: state.settings.project, request: { expectedVersion: 7, defaultConfigId: 'metadata', defaultFrameId: 'clinical_metadata/Specimen' } });
    await client.deleteProjectViewConfig({ project: state.settings.project, configId: 'legacy', expectedRevisionId: 'config-r2' });
    await client.adoptProjectViewBuilderCatalogs({ project: state.settings.project });
    const calls = fetch.mock.calls;
    expect(calls.map(([url, init]) => [String(url), init?.method])).toEqual([
      ['/api/v1/projects/HTAN_INT%252FBForePC/view-builder/configs/metadata', 'PUT'],
      ['/api/v1/projects/HTAN_INT%252FBForePC/view-builder/defaults', 'PUT'],
      ['/api/v1/projects/HTAN_INT%252FBForePC/view-builder/configs/legacy?expectedRevisionId=config-r2', 'DELETE'],
      ['/api/v1/projects/HTAN_INT%252FBForePC/view-builder/adopt', 'POST'],
    ]);
    expect(JSON.parse(String(calls[0]?.[1]?.body))).toMatchObject({ expectedRevisionId: 'config-r4', catalogRevisionId: 'catalog-r3' });
    expect(JSON.parse(String(calls[1]?.[1]?.body))).toMatchObject({ expectedVersion: 7 });
  });

  it('shares path encoding and parses the host SSR get envelope', async () => {
    expect(projectViewBuilderPath(state.settings.project, '/programs/HTAN_INT/projects/BForePC')).toBe('/api/v1/projects/HTAN_INT%252FBForePC/view-builder?auth_resource_path=%2Fprograms%2FHTAN_INT%2Fprojects%2FBForePC');
    await expect(getProjectViewBuilderState(async () => ({ data: state }), state.settings.project)).resolves.toEqual(state);
  });
});
