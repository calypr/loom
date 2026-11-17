import { readFileSync } from 'node:fs';
import { parse } from 'yaml';
import { describe, expect, it } from 'vitest';
import { z } from 'zod';
import * as runtimeTypes from './types';

const openApiSchema = z.object({
  $ref: z.string().optional(),
  type: z.string().optional(),
  properties: z.record(z.string(), z.unknown()).optional(),
  required: z.array(z.string()).optional(),
}).passthrough();

const openApiDocumentSchema = z.object({
  components: z.object({
    schemas: z.record(z.string(), openApiSchema),
  }),
}).passthrough();

const document = openApiDocumentSchema.parse(parse(readFileSync(
  new URL('../../../../openapi/openapi.yaml', import.meta.url),
  'utf8',
)));

const componentNameFor = (schemaName: string): string => {
  const name = schemaName.slice(0, -'Schema'.length);
  return name.replace(/^[a-z]/, (first) => first.toUpperCase());
};

const openApiObjectShapeFor = (
  component: z.infer<typeof openApiSchema>,
  seen: ReadonlySet<string> = new Set(),
): { readonly properties: ReadonlyArray<string>; readonly required: ReadonlyArray<string> } | undefined => {
  if (component.$ref) {
    const targetName = component.$ref.split('/').at(-1);
    if (!targetName || seen.has(targetName)) return undefined;
    const target = document.components.schemas[targetName];
    return target ? openApiObjectShapeFor(target, new Set([...seen, targetName])) : undefined;
  }
  if (component.type !== 'object' || !component.properties) return undefined;
  return {
    properties: Object.keys(component.properties).sort(),
    required: [...(component.required ?? [])].sort(),
  };
};

const isZodSchema = (value: unknown): value is z.ZodTypeAny => value instanceof z.ZodType;

describe('Zod schemas and OpenAPI components', () => {
  it('keeps matching object properties and required sets in sync', () => {
    const matchedSchemas = Object.entries(runtimeTypes).flatMap(([schemaName, schema]) => {
      if (!schemaName.endsWith('Schema') || !(schema instanceof z.ZodObject)) return [];
      const componentName = componentNameFor(schemaName);
      const component = document.components.schemas[componentName];
      if (!component) return [];
      return [{ schemaName, componentName, schema, component }];
    });

    expect(matchedSchemas.length, 'at least one runtime schema should match an OpenAPI object component').toBeGreaterThan(0);

    const mismatches: string[] = [];
    for (const { schemaName, componentName, schema, component } of matchedSchemas) {
      const openApiShape = openApiObjectShapeFor(component);
      if (!openApiShape) {
        mismatches.push(`${schemaName}/${componentName}: OpenAPI component is not an object with properties`);
        continue;
      }
      const zodProperties = Object.keys(schema.shape).sort();
      const zodRequired = Object.entries(schema.shape)
        .filter(([, propertySchema]) => isZodSchema(propertySchema) && !propertySchema.safeParse(undefined).success)
        .map(([propertyName]) => propertyName)
        .sort();

      if (JSON.stringify(zodProperties) !== JSON.stringify(openApiShape.properties)) {
        mismatches.push(`${schemaName}/${componentName}: properties Zod=${JSON.stringify(zodProperties)} OpenAPI=${JSON.stringify(openApiShape.properties)}`);
      }
      if (JSON.stringify(zodRequired) !== JSON.stringify(openApiShape.required)) {
        mismatches.push(`${schemaName}/${componentName}: required Zod=${JSON.stringify(zodRequired)} OpenAPI=${JSON.stringify(openApiShape.required)}`);
      }
    }
    expect(mismatches).toEqual([]);
  });
});
