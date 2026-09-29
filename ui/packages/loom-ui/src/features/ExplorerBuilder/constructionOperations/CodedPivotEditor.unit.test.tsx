// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { LoomClient } from '../../../api';
import type { Construction } from '../../../types';
import { CodedPivotEditor } from './CodedPivotEditor';

describe('CodedPivotEditor', () => {
  it('turns a signed direct-source category into a row-first construction without raw columns', async () => {
    const browseFrameSourceOptions = vi.fn().mockResolvedValue({
      sources: [{
        choiceId: 'signed-family', title: 'Observation components', description: 'Codes and paired values',
        resourceType: 'Observation', sourcePath: 'component[]', bindingId: 'component-binding',
        owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'component[].valueString',
        logicalType: 'string', exampleConcept: 'specimen_type', observedOccurrences: 12, route: [],
        forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'one value' }], defaultForm: 'VALUE',
      }],
    });
    const browseSemanticInventory = vi.fn().mockResolvedValue({
      entries: [{
        conceptId: 'specimen-type', bindingId: 'component-binding', resourceType: 'Observation',
        sourcePath: 'component[]', system: 'https://cda.readthedocs.io', code: 'specimen_type',
        codingVersion: '', display: 'specimen_type', valueSelector: 'component[].valueString',
        valueType: 'string', owningScope: 'component[]', occurrences: 12, examplesTruncated: false,
        observedUnitsTruncated: false, readiness: { status: 'READY', code: 'READY', message: 'Ready' },
        constructionChoice: { choiceId: 'signed-category' },
      }],
    });
    const onCandidateChange = vi.fn();
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    const choice = await screen.findByRole('checkbox', { name: /Specimen type/ });
    fireEvent.click(choice);
    await waitFor(() => expect(onCandidateChange).toHaveBeenCalledWith(expect.objectContaining({
      candidateConstruction: expect.objectContaining({ steps: [expect.objectContaining({
        inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: expect.objectContaining({
          kind: 'CODED_PIVOT',
          codedPivot: expect.objectContaining({
            sourceChoiceId: 'signed-family',
            categories: [expect.objectContaining({ choiceId: 'signed-category' })],
            duplicatePolicy: 'ERROR', missingCellPolicy: 'NULL',
          }),
        }),
        outputs: [expect.objectContaining({ name: 'specimen_type', label: 'Specimen type' })],
      })] }),
    })));
    expect(browseSemanticInventory).toHaveBeenCalledWith(expect.objectContaining({
      sourceChoiceId: 'signed-family', rowRoot: 'Observation', outputId: 'observations',
    }), expect.any(AbortSignal));
    expect(browseFrameSourceOptions).toHaveBeenCalledWith(expect.objectContaining({
      resourceType: 'Observation', outputId: 'observations',
    }), expect.any(AbortSignal));
  });

  it('reopens a saved pivot using durable category keys without requiring a newly browsed category', async () => {
    const step = {
      id: 'saved-pivot', inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: { kind: 'CODED_PIVOT' as const, codedPivot: {
        constructionId: 'saved-pivot',
        source: {
          family: { bindingId: 'component-binding', resourceType: 'Observation', sourcePath: 'component[]',
            owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'component[].valueString',
            logicalType: 'string', ruleVersion: '4', schemaVersion: 3 },
          candidateId: 'candidate', nodeId: 'observation', fieldPath: 'component[]', route: [],
        },
        categories: [{ system: 'https://cda.readthedocs.io', code: 'specimen_type', outputColumnId: 'specimen-column' }],
        duplicatePolicy: 'ERROR' as const, missingCellPolicy: 'NULL' as const,
      } },
      outputs: [{ id: 'specimen-column', name: 'specimen_type', label: 'Specimen type' }],
    };
    const onCandidateChange = vi.fn();
    render(<CodedPivotEditor
      client={{
        browseFrameSourceOptions: vi.fn().mockResolvedValue({ sources: [{
          choiceId: 'signed-family', title: 'Observation components', description: 'Codes and paired values',
          resourceType: 'Observation', sourcePath: 'component[]', bindingId: 'component-binding',
          owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'component[].valueString',
          logicalType: 'string', exampleConcept: 'specimen_type', observedOccurrences: 12, route: [],
          forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'one value' }], defaultForm: 'VALUE',
        }] }),
        browseSemanticInventory: vi.fn().mockResolvedValue({ entries: [] }),
      } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [step] }} editingStep={step}
      disabled={false} onCandidateChange={onCandidateChange}
    />);
    await waitFor(() => expect(onCandidateChange).toHaveBeenCalledWith(expect.objectContaining({
      changedStepId: 'saved-pivot',
      candidateConstruction: expect.objectContaining({ steps: [expect.objectContaining({
        operation: expect.objectContaining({ codedPivot: expect.objectContaining({
          sourceChoiceId: 'signed-family',
          categories: [{ system: 'https://cda.readthedocs.io', code: 'specimen_type', outputColumnId: 'specimen-column' }],
        }) }),
      })] }),
    })));
  });
});
