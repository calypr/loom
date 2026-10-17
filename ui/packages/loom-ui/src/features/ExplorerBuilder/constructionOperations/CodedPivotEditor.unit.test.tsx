// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { LoomClient } from '../../../api';
import type { Construction, FrameSourceOption } from '../../../types';
import { CodedPivotEditor } from './CodedPivotEditor';

const directSourceOption = (overrides: Partial<FrameSourceOption> = {}): FrameSourceOption => ({
  choiceId: 'signed-family', title: 'Observation components', description: 'Codes and paired values',
  resourceType: 'Observation', sourcePath: 'component[]', bindingId: 'component-binding',
  owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'component[].valueString',
  logicalType: 'string', exampleConcept: 'specimen_type', observedOccurrences: 12, route: [],
  forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'one value' }], defaultForm: 'VALUE',
  ...overrides,
});

const browseDirectSourceOptions = () => vi.fn().mockResolvedValue({
  sources: [directSourceOption()],
});

describe('CodedPivotEditor', () => {
  it('explains the related-source prerequisite when no direct coded source is available', async () => {
    const browseFrameSourceOptions = vi.fn().mockResolvedValue({ sources: [] });
    const browseSemanticInventory = vi.fn();
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    expect(await screen.findByText(/No direct coded source is available on these starting records/)).toBeInTheDocument();
    expect(screen.getByText(/Use category and value fields already in this table, or add a coded-value column from a related record/)).toBeInTheDocument();
    expect(browseSemanticInventory).not.toHaveBeenCalled();
  });

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
      }], state: 'complete', sourceAvailability: 'verified',
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

  it('distinguishes coded sources that share a path by paired value type and example', async () => {
    const stringSource = directSourceOption({
      choiceId: 'string-family',
      exampleConcept: 'specimen_type',
      logicalType: 'string',
    });
    const integerSource = directSourceOption({
      choiceId: 'integer-family',
      exampleConcept: 'days_to_collection',
      logicalType: 'integer',
      valuePath: 'component[].valueInteger',
    });
    const browseFrameSourceOptions = vi.fn().mockResolvedValue({ sources: [stringSource, integerSource] });
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory: vi.fn() } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    const sourceChoices = await screen.findAllByRole('radio', { name: /Observation components/ });
    expect(sourceChoices).toHaveLength(2);
    const stringDescription = screen.getByText('Value type string · example specimen_type · 12 observed source occurrences');
    const integerDescription = screen.getByText('Value type integer · example days_to_collection · 12 observed source occurrences');
    expect(stringDescription.id.length).toBeGreaterThan(0);
    expect(integerDescription.id.length).toBeGreaterThan(0);
    expect(sourceChoices[0]?.getAttribute('aria-describedby')).toBe(stringDescription.id);
    expect(sourceChoices[1]?.getAttribute('aria-describedby')).toBe(integerDescription.id);
    expect(screen.queryByText('component[].valueString')).not.toBeInTheDocument();
    expect(screen.queryByText('component[].valueInteger')).not.toBeInTheDocument();
  });

  it('shows code identity before selecting ambiguous labels and preserves the same output labels', async () => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const categories = [
      {
        conceptId: 'observation-specimen-type', bindingId: 'component-binding', resourceType: 'Observation',
        sourcePath: 'component[]', system: 'https://example.org/observation-codes', code: 'specimen_type',
        codingVersion: '', display: 'Specimen type', valueSelector: 'component[].valueString', valueType: 'string',
        owningScope: 'component[]', occurrences: 12, examplesTruncated: false, observedUnitsTruncated: false,
        readiness: { status: 'READY' as const, code: 'READY', message: 'Ready' },
        constructionChoice: { choiceId: 'observation-choice' },
      },
      {
        conceptId: 'panel-specimen-type', bindingId: 'component-binding', resourceType: 'Observation',
        sourcePath: 'component[]', system: 'https://example.org/panel-codes', code: 'specimen_type',
        codingVersion: '', display: 'Specimen type', valueSelector: 'component[].valueString', valueType: 'string',
        owningScope: 'component[]', occurrences: 7, examplesTruncated: false, observedUnitsTruncated: false,
        readiness: { status: 'READY' as const, code: 'READY', message: 'Ready' },
        constructionChoice: { choiceId: 'panel-choice' },
      },
      {
        conceptId: 'observation-specimen-type-alternate', bindingId: 'component-binding', resourceType: 'Observation',
        sourcePath: 'component[]', system: 'https://example.org/observation-codes', code: 'specimen-type',
        codingVersion: '', display: 'Specimen type', valueSelector: 'component[].valueString', valueType: 'string',
        owningScope: 'component[]', occurrences: 3, examplesTruncated: false, observedUnitsTruncated: false,
        readiness: { status: 'READY' as const, code: 'READY', message: 'Ready' },
        constructionChoice: { choiceId: 'observation-alternate-choice' },
      },
    ];
    const onCandidateChange = vi.fn();
    render(<CodedPivotEditor
      client={{
        browseFrameSourceOptions,
        browseSemanticInventory: vi.fn().mockResolvedValue({
          entries: categories, state: 'complete', sourceAvailability: 'verified',
        }),
      } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    const categoryChoices = await screen.findAllByRole('checkbox', { name: /Specimen type/ });
    expect(categoryChoices).toHaveLength(3);
    expect(screen.getByText('System https://example.org/observation-codes · Code specimen_type')).toBeInTheDocument();
    expect(screen.getByText('System https://example.org/panel-codes · Code specimen_type')).toBeInTheDocument();
    expect(screen.getByText('Code specimen-type')).toBeInTheDocument();
    fireEvent.click(categoryChoices[0]!);
    fireEvent.click((await screen.findAllByRole('checkbox', { name: /Specimen type/ }))[1]!);
    fireEvent.click((await screen.findAllByRole('checkbox', { name: /Specimen type/ }))[2]!);

    expect(screen.getAllByText('System https://example.org/observation-codes · Code specimen_type')).toHaveLength(2);
    expect(screen.getAllByText('System https://example.org/panel-codes · Code specimen_type')).toHaveLength(2);
    expect(screen.getAllByText('Code specimen-type')).toHaveLength(2);
    await waitFor(() => expect(onCandidateChange).toHaveBeenLastCalledWith(expect.objectContaining({
      candidateConstruction: expect.objectContaining({ steps: [expect.objectContaining({
        operation: expect.objectContaining({ codedPivot: expect.objectContaining({
          categories: [
            expect.objectContaining({ choiceId: 'observation-choice' }),
            expect.objectContaining({ choiceId: 'panel-choice' }),
            expect.objectContaining({ choiceId: 'observation-alternate-choice' }),
          ],
        }) }),
        outputs: [
          expect.objectContaining({ name: 'specimen_type', label: 'Specimen type' }),
          expect.objectContaining({ name: 'specimen_type_2', label: 'Specimen type' }),
          expect.objectContaining({ name: 'specimen_type_3', label: 'Specimen type' }),
        ],
      })] }),
    })));
    const candidate = onCandidateChange.mock.calls.at(-1)?.[0];
    const step = candidate?.candidateConstruction.steps[0];
    expect(step?.operation.kind).toBe('CODED_PIVOT');
    if (step?.operation.kind === 'CODED_PIVOT') {
      expect(step.operation.codedPivot.categories.map(({ outputColumnId }) => outputColumnId))
        .toEqual(step.outputs.map(({ id }) => id));
      expect(new Set(step.outputs.map(({ id }) => id)).size).toBe(3);
    }
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
        browseSemanticInventory: vi.fn().mockResolvedValue({
          entries: [], state: 'complete', sourceAvailability: 'verified',
        }),
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
        outputs: [{ id: 'specimen-column', name: 'specimen_type', label: 'Specimen type', type: 'INFER' }],
      })] }),
    })));
    const savedSelection = screen.getByRole('listitem', {
      name: 'Specimen type; system https://cda.readthedocs.io; code specimen_type',
    });
    expect(savedSelection).toHaveAttribute('title', 'Specimen type; system https://cda.readthedocs.io; code specimen_type');
    expect(screen.queryByText('Code specimen_type')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Coded values as columns', exact: true })).not.toBeInTheDocument();
    const editor = screen.getByRole('region', { name: 'Coded values as columns' });
    expect([...editor.querySelectorAll('p')].every((paragraph) => paragraph.classList.contains('m-0'))).toBe(true);
  });

  it('finds legal coded values on later pages without requiring search or a More click', async () => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const unsupported = {
      conceptId: 'unsupported-category', bindingId: 'component-binding', resourceType: 'Observation',
      sourcePath: 'component[]', system: 'https://example.org/codes', code: 'legacy-value', codingVersion: '',
      display: 'Legacy category', valueSelector: 'component[].valueString', valueType: 'string',
      owningScope: 'component[]', occurrences: 4, examplesTruncated: false, observedUnitsTruncated: false,
      readiness: { status: 'UNSUPPORTED', code: 'COMPILER_PROOF_UNAVAILABLE', message: 'The compiler could not confirm this value.' },
    };
    const legal = {
      conceptId: 'legal-category', bindingId: 'component-binding', resourceType: 'Observation',
      sourcePath: 'component[]', system: 'https://example.org/codes', code: 'specimen-type', codingVersion: '',
      display: 'Specimen type', valueSelector: 'component[].valueString', valueType: 'string',
      owningScope: 'component[]', occurrences: 12, examplesTruncated: false, observedUnitsTruncated: false,
      readiness: { status: 'READY', code: 'READY', message: 'Ready' },
      constructionChoice: { choiceId: 'legal-choice' },
    };
    let releaseSecondPage!: (response: {
      readonly entries: ReadonlyArray<typeof legal>;
      readonly state: 'complete';
      readonly sourceAvailability: 'verified';
    }) => void;
    const secondPage = new Promise<{
      readonly entries: ReadonlyArray<typeof legal>;
      readonly state: 'complete';
      readonly sourceAvailability: 'verified';
    }>((resolve) => {
      releaseSecondPage = resolve;
    });
    const browseSemanticInventory = vi.fn((args: { readonly cursor?: string }) => args.cursor
      ? secondPage
      : Promise.resolve({ entries: [unsupported], nextCursor: 'next-page', state: 'complete', sourceAvailability: 'verified' }));
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    await waitFor(() => expect(browseSemanticInventory).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('status')).toHaveTextContent('Finding coded values…');
    expect((screen.getByPlaceholderText('Search coded values') as HTMLInputElement).value).toBe('');
    expect(screen.queryByText('No coded values are available for this source.')).not.toBeInTheDocument();
    releaseSecondPage({ entries: [legal], state: 'complete', sourceAvailability: 'verified' });

    const legalChoice = await screen.findByRole('checkbox', { name: /Specimen type/ });
    expect(legalChoice).toBeEnabled();
    expect(screen.getByRole('checkbox', { name: /Legacy category/ })).toBeDisabled();
    expect(screen.getByText('The compiler could not confirm this value.')).toBeInTheDocument();
    expect(browseSemanticInventory).toHaveBeenCalledTimes(2);
    expect(browseSemanticInventory).toHaveBeenNthCalledWith(1,
      expect.objectContaining({ sourceChoiceId: 'signed-family', rowRoot: 'Observation', limit: 50 }),
      expect.any(AbortSignal));
    expect(browseSemanticInventory.mock.calls[0]?.[0]).not.toHaveProperty('query');
    expect(browseSemanticInventory).toHaveBeenNthCalledWith(2,
      expect.objectContaining({ sourceChoiceId: 'signed-family', rowRoot: 'Observation', cursor: 'next-page', limit: 50 }),
      expect.any(AbortSignal));
    expect(screen.queryByRole('button', { name: 'More coded values' })).not.toBeInTheDocument();
  });

  it('keeps first-page choices selectable when a later inventory page fails', async () => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const legal = {
      conceptId: 'legal-category', bindingId: 'component-binding', resourceType: 'Observation',
      sourcePath: 'component[]', system: 'https://example.org/codes', code: 'specimen-type', codingVersion: '',
      display: 'Specimen type', valueSelector: 'component[].valueString', valueType: 'string',
      owningScope: 'component[]', occurrences: 12, examplesTruncated: false, observedUnitsTruncated: false,
      readiness: { status: 'READY', code: 'READY', message: 'Ready' },
      constructionChoice: { choiceId: 'legal-choice' },
    };
    let rejectSecondPage!: (error: Error) => void;
    const secondPage = new Promise<never>((_resolve, reject) => {
      rejectSecondPage = reject;
    });
    const browseSemanticInventory = vi.fn((args: { readonly cursor?: string }) => args.cursor
      ? secondPage
      : Promise.resolve({ entries: [legal], nextCursor: 'page-two', state: 'complete', sourceAvailability: 'verified' }));
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    const firstPageChoice = await screen.findByRole('checkbox', { name: /Specimen type/ });
    expect(firstPageChoice).toBeEnabled();
    expect(screen.getByRole('status')).toHaveTextContent('Finding coded values…');
    expect(browseSemanticInventory).toHaveBeenCalledTimes(2);
    rejectSecondPage(new Error('Page two is unavailable.'));

    expect(await screen.findByRole('alert')).toHaveTextContent('Page two is unavailable.');
    const retainedChoice = screen.getByRole('checkbox', { name: /Specimen type/ });
    expect(retainedChoice).toBeEnabled();
    fireEvent.click(retainedChoice);
    expect(await screen.findByText('1 of 50 columns selected')).toBeInTheDocument();
    expect(screen.queryByText('No coded values are available for this source.')).not.toBeInTheDocument();
    expect(screen.queryByText(/Search for a category and try again/)).not.toBeInTheDocument();
  });

  it('keeps More coded values for results beyond the first 50 legal choices', async () => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const firstPage = Array.from({ length: 50 }, (_, index) => ({
      conceptId: `legal-${index}`, bindingId: 'component-binding', resourceType: 'Observation',
      sourcePath: 'component[]', system: 'https://example.org/codes', code: `code-${index}`, codingVersion: '',
      display: `Available category ${index}`, valueSelector: 'component[].valueString', valueType: 'string',
      owningScope: 'component[]', occurrences: 1, examplesTruncated: false, observedUnitsTruncated: false,
      readiness: { status: 'READY' as const, code: 'READY', message: 'Ready' },
      constructionChoice: { choiceId: `choice-${index}` },
    }));
    const laterChoice = {
      ...firstPage[0]!, conceptId: 'later-legal', code: 'later-code', display: 'Later legal category',
      constructionChoice: { choiceId: 'later-choice' },
    };
    const browseSemanticInventory = vi.fn((args: { readonly cursor?: string }) => args.cursor
      ? Promise.resolve({ entries: [laterChoice], state: 'complete' as const, sourceAvailability: 'verified' as const })
      : Promise.resolve({ entries: firstPage, nextCursor: 'next-page', state: 'complete' as const, sourceAvailability: 'verified' as const }));
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    expect(await screen.findByRole('checkbox', { name: /Available category 0/ })).toBeEnabled();
    expect(browseSemanticInventory).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'More coded values' }));
    expect(await screen.findByRole('checkbox', { name: /Later legal category/ })).toBeEnabled();
    expect(browseSemanticInventory).toHaveBeenCalledTimes(2);
    expect(browseSemanticInventory).toHaveBeenLastCalledWith(
      expect.objectContaining({ cursor: 'next-page', sourceChoiceId: 'signed-family' }),
      expect.any(AbortSignal));
    expect(screen.getByRole('checkbox', { name: /Available category 0/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'More coded values' })).not.toBeInTheDocument();
  });

  it('drops carried and selected categories when the inventory request scope changes', async () => {
    const option = (choiceId: string, title: string) => ({
      choiceId, title, description: title,
      resourceType: 'Observation', sourcePath: 'component[]', bindingId: `${choiceId}-binding`,
      owningScope: 'component[]', keyPath: 'component[].code.coding[]', valuePath: 'component[].valueString',
      logicalType: 'string', exampleConcept: 'specimen_type', observedOccurrences: 12, route: [],
      forms: [{ form: 'VALUE', zeroPolicy: 'NULL', manyPolicy: 'INVALID_MULTIPLE_VALUES', decision: 'one value' }], defaultForm: 'VALUE',
    });
    const oldSource = option('old-signed-family', 'Old source');
    const newSource = option('new-signed-family', 'New source');
    const browseFrameSourceOptions = vi.fn((args: { readonly project: string }) => Promise.resolve({
      sources: [args.project === 'cda' ? oldSource : newSource],
    }));
    const oldCategory = (index: number) => ({
      conceptId: `old-${index}`, bindingId: 'old-signed-family-binding', resourceType: 'Observation',
      sourcePath: 'component[]', system: 'https://example.org/old-codes', code: `old-code-${index}`, codingVersion: '',
      display: `Old category ${index}`, valueSelector: 'component[].valueString', valueType: 'string',
      owningScope: 'component[]', occurrences: 1, examplesTruncated: false, observedUnitsTruncated: false,
      readiness: { status: 'READY' as const, code: 'READY', message: 'Ready' },
      constructionChoice: { choiceId: `old-choice-${index}` },
    });
    const firstOldCategory = oldCategory(0);
    const oldPage = Array.from({ length: 50 }, (_, index) => oldCategory(index));
    const oldMore = {
      ...firstOldCategory, conceptId: 'old-more', code: 'old-more-code', display: 'More old category',
      constructionChoice: { choiceId: 'old-more-choice' },
    };
    const newCategory = {
      ...firstOldCategory, conceptId: 'new-category', bindingId: 'new-signed-family-binding',
      system: 'https://example.org/new-codes', code: 'new-code', display: 'New category',
      constructionChoice: { choiceId: 'new-choice' },
    };
    const browseSemanticInventory = vi.fn((args: { readonly project: string; readonly outputId: string; readonly cursor?: string }) => {
      if (args.project !== 'cda') {
        return Promise.resolve({ entries: [newCategory], state: 'complete' as const, sourceAvailability: 'verified' as const });
      }
      return args.cursor
        ? Promise.resolve({ entries: [oldMore], state: 'complete' as const, sourceAvailability: 'verified' as const })
        : Promise.resolve({ entries: oldPage, nextCursor: 'old-next-page', state: 'complete' as const, sourceAvailability: 'verified' as const });
    });
    const onCandidateChange = vi.fn();
    const initialProps = {
      client: { browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>,
      project: 'cda', explorerId: 'builder', authResourcePath: '/auth/old', snapshotToken: 'snapshot-old',
      outputId: 'observations-old', rowRoot: 'Observation',
      construction: { version: 1, steps: [] } satisfies Construction,
      disabled: false, onCandidateChange,
    };
    const view = render(<CodedPivotEditor {...initialProps} />);

    expect(await screen.findByRole('checkbox', { name: /Old category 0/ })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'More coded values' }));
    expect(await screen.findByRole('checkbox', { name: /More old category/ })).toBeEnabled();
    const firstOldChoice = screen.getByRole('checkbox', { name: /Old category 0/ });
    fireEvent.click(firstOldChoice);
    await waitFor(() => expect(onCandidateChange).toHaveBeenLastCalledWith(expect.objectContaining({
      candidateConstruction: expect.objectContaining({ steps: [expect.objectContaining({
        operation: expect.objectContaining({ codedPivot: expect.objectContaining({
          sourceChoiceId: 'old-signed-family', categories: [expect.objectContaining({ choiceId: 'old-choice-0' })],
        }) }),
      })] }),
    })));
    const callsBeforeScopeChange = onCandidateChange.mock.calls.length;

    view.rerender(<CodedPivotEditor {...initialProps}
      project="cda-next" explorerId="builder-next" authResourcePath="/auth/next"
      snapshotToken="snapshot-next" outputId="observations-next" />);

    expect(await screen.findByRole('checkbox', { name: /New category/ })).toBeEnabled();
    expect(screen.queryByRole('checkbox', { name: /Old category 0/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /More old category/ })).not.toBeInTheDocument();
    expect(browseSemanticInventory).toHaveBeenLastCalledWith(expect.objectContaining({
      project: 'cda-next', explorerId: 'builder-next', authResourcePath: '/auth/next',
      snapshotToken: 'snapshot-next', outputId: 'observations-next', sourceChoiceId: 'new-signed-family',
    }), expect.any(AbortSignal));
    await waitFor(() => expect(onCandidateChange.mock.calls.length).toBeGreaterThan(callsBeforeScopeChange));
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    expect(onCandidateChange.mock.calls.slice(callsBeforeScopeChange).every(([intent]) => intent === undefined)).toBe(true);
  });

  it('keeps search optional and distinguishes an empty source from an empty search', async () => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const browseSemanticInventory = vi.fn().mockResolvedValue({
      entries: [], state: 'complete', sourceAvailability: 'verified',
    });
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    expect(await screen.findByText('No coded values are available for this source.')).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText('Search coded values'), { target: { value: 'missing-code' } });
    expect(await screen.findByText('No coded values match this search.')).toBeInTheDocument();
    expect(browseSemanticInventory).toHaveBeenLastCalledWith(
      expect.objectContaining({ sourceChoiceId: 'signed-family', query: 'missing-code' }),
      expect.any(AbortSignal));
  });

  it.each([
    { state: 'running', sourceAvailability: 'unknown' },
    { state: 'complete', sourceAvailability: 'unproven' },
  ] as const)('does not present unconfirmed inventory as an empty source', async ({ state, sourceAvailability }) => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const browseSemanticInventory = vi.fn().mockResolvedValue({
      entries: [], state, sourceAvailability,
    });
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    const inventoryStatus = await screen.findByText('Coded-value availability has not been confirmed for this source.');
    expect(inventoryStatus.getAttribute('role')).toBe('status');
    expect(screen.queryByText('No coded values are available for this source.')).not.toBeInTheDocument();
    expect(screen.queryByText('No coded values match this search.')).not.toBeInTheDocument();
  });

  it('shows the inventory request failure without suggesting a search retry', async () => {
    const browseFrameSourceOptions = browseDirectSourceOptions();
    const browseSemanticInventory = vi.fn().mockRejectedValue(new Error('Inventory service timed out.'));
    render(<CodedPivotEditor
      client={{ browseFrameSourceOptions, browseSemanticInventory } as unknown as Pick<LoomClient, 'browseFrameSourceOptions' | 'browseSemanticInventory'>}
      project="cda" explorerId="builder" snapshotToken="snapshot" outputId="observations"
      rowRoot="Observation" construction={{ version: 1, steps: [] } satisfies Construction}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    expect(await screen.findByRole('alert')).toHaveTextContent('Inventory service timed out.');
    expect(screen.queryByText(/Search for a category and try again/)).not.toBeInTheDocument();
    expect(screen.queryByText('No coded values are available for this source.')).not.toBeInTheDocument();
  });
});
