import { expect, it } from 'vitest';
import { importedSourceParametersSchema } from './contracts/sourceRecipeParameters';

it('accepts exact catalog output overrides and rejects duplicate or invalid identities', () => {
  const valid = importedSourceParametersSchema.safeParse({ catalog: {
    name: 'Patient fields', outputs: [{ fieldPath: 'identifier[].value', enabled: false }, { fieldPath: 'name[0].text', enabled: true, name: 'patient_name' }],
  } });
  expect(valid.success).toBe(true);

  const duplicate = importedSourceParametersSchema.safeParse({ catalog: {
    name: 'Patient fields', outputs: [{ fieldPath: 'identifier[].value', enabled: false }, { fieldPath: 'identifier[].value', enabled: true }],
  } });
  expect(duplicate.success).toBe(false);

  const invalidName = importedSourceParametersSchema.safeParse({ catalog: {
    name: 'Patient fields', outputs: [{ fieldPath: 'identifier[].value', enabled: true, name: 'Patient name' }],
  } });
  expect(invalidName.success).toBe(false);
});

it('accepts exact pivot output overrides including explicit alias clearing and rejects duplicate keys', () => {
  const valid = importedSourceParametersSchema.safeParse({ pivot: {
    name: 'observation_values', outputs: [{ key: 'valueDateTime', enabled: false }, { key: 'valueInteger', enabled: true, name: '' }],
  } });
  expect(valid.success).toBe(true);

  const duplicate = importedSourceParametersSchema.safeParse({ pivot: {
    name: 'observation_values', outputs: [{ key: 'valueString', enabled: false }, { key: 'valueString', enabled: true }],
  } });
  expect(duplicate.success).toBe(false);
});

it('preserves raw pivot keys and enforces the backend override limit', () => {
  const parsed = importedSourceParametersSchema.parse({ pivot: {
    name: 'values', outputs: [{ key: ' code ', enabled: false }, { key: 'code', enabled: true }],
  } });
  expect(parsed.pivot?.outputs?.map((output) => output.key)).toEqual([' code ', 'code']);
  expect(importedSourceParametersSchema.safeParse({ pivot: {
    name: 'values', outputs: Array.from({ length: 257 }, (_, index) => ({ key: `key-${index}`, enabled: true })),
  } }).success).toBe(false);
});

it('accepts exact generated field overrides and rejects duplicate identities', () => {
  const key = `ext-v1:${'a'.repeat(64)}`;
  const extension = { name: 'extensions', source: { select: 'root.extension[]' } };
  expect(importedSourceParametersSchema.safeParse({ extension: { ...extension, outputs: [{ key, enabled: false }] } }).success).toBe(true);
  expect(importedSourceParametersSchema.safeParse({ extension: { ...extension, outputs: [{ key, enabled: false }, { key, enabled: true }] } }).success).toBe(false);
  expect(importedSourceParametersSchema.safeParse({ extension: { ...extension, outputs: [{ key: 'extension-url', enabled: false }] } }).success).toBe(false);
  expect(importedSourceParametersSchema.safeParse({ dynamic: { name: 'keys', source: { select: 'root.category[]' }, outputs: [{ key: 'key', enabled: false }] } }).success).toBe(true);
});
