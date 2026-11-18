import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';
import { z } from 'zod';

const documentSchema = z.object({
  paths: z.record(z.string(), z.record(z.string(), z.unknown())),
  components: z.object({ schemas: z.record(z.string(), z.unknown()) }).passthrough(),
}).passthrough();
const contractPath = process.env.LOOM_OPENAPI_CONTRACT_PATH ?? new URL('../../../../api/openapi.yaml', import.meta.url);
const document = documentSchema.parse(parse(readFileSync(contractPath, 'utf8')));

describe('saved view client and OpenAPI contract', () => {
  it('pins every method used by the portable saved view client', () => {
    const cases = [
      ['/api/v1/projects/{project}/view-builder', 'get', 'getProjectViewBuilder'],
      ['/api/v1/projects/{project}/view-builder/adopt', 'post', 'adoptProjectViewBuilder'],
      ['/api/v1/projects/{project}/view-builder/defaults', 'put', 'saveProjectViewDefaults'],
      ['/api/v1/projects/{project}/view-builder/configs/{explorerId}', 'put', 'saveProjectViewConfig'],
      ['/api/v1/projects/{project}/view-builder/configs/{explorerId}', 'delete', 'deleteProjectViewConfig'],
    ] as const;
    for (const [path, method, operationId] of cases) {
      const route = document.paths[path];
      expect(route?.[method], `${method.toUpperCase()} ${path} must remain in OpenAPI`).toMatchObject({ operationId });
    }
    expect(document.components.schemas).toHaveProperty('RawJSON');
  });
});
