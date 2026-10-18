import { describe, expect, it } from 'vitest';
import fixtureJson from '../../../../testdata/openapi/accepted-construction-response.json?raw';

import {
  explorerBuilderAcceptedConstructionSchema,
  explorerBuilderCommandsResultSchema,
} from './types';

const fixture = JSON.parse(fixtureJson) as unknown;

describe('accepted construction response contract', () => {
  it('accepts the shared Apply response fixture with the strict Zod schema', () => {
    const parsed = explorerBuilderCommandsResultSchema.parse(fixture);
    expect(parsed.acceptedConstruction?.preview.rows).toEqual([{ patient_id: 'p1' }]);
    expect(parsed.acceptedConstruction?.previewLimit).toBe(100);
    expect(parsed.acceptedConstruction?.receipt.intentDigest).toBe(parsed.draftDigest);
    expect(parsed.acceptedConstruction?.preview.receiptId).toBe(
      parsed.acceptedConstruction?.receipt.receiptId,
    );
    expect('builder' in (parsed.acceptedConstruction?.receipt ?? {})).toBe(false);
  });

  it('rejects duplicated builder data, private proposal bindings, and invalid preview limits', () => {
    const value = JSON.parse(JSON.stringify(fixture)) as Record<string, unknown>;
    const withBuilder = structuredClone(value);
    const withBuilderAccepted = withBuilder.acceptedConstruction as Record<string, unknown>;
    const withBuilderReceipt = withBuilderAccepted.receipt as Record<string, unknown>;
    withBuilderReceipt.builder = withBuilder.workspace;
    expect(explorerBuilderCommandsResultSchema.safeParse(withBuilder).success).toBe(false);

    const withPrivateBinding = structuredClone(value);
    const withPrivateAccepted = withPrivateBinding.acceptedConstruction as Record<string, unknown>;
    const withPrivateReceipt = withPrivateAccepted.receipt as Record<string, unknown>;
    withPrivateReceipt.constructionProposal = {};
    expect(explorerBuilderCommandsResultSchema.safeParse(withPrivateBinding).success).toBe(false);

    const accepted = value.acceptedConstruction as Record<string, unknown>;
    expect(
      explorerBuilderAcceptedConstructionSchema.safeParse({
        ...accepted,
        previewLimit: 1001,
      }).success,
    ).toBe(false);
  });
});
