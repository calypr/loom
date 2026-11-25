import { describe, expect, it } from 'vitest';
import type { ExplorerBuilderCommand, ExplorerBuilderDocument } from '../../../contracts/types';
import { admitAuthoringRequest } from '../model/admission';
import type { DraftTable } from '../model/authoring';

const table = (outputId: string, importedRecipe = false): DraftTable => ({
  outputId,
  tabId: outputId,
  title: outputId,
  document: {
    ...(importedRecipe ? { importedRecipe: { recipeId: 'recipe-1', version: '1' } } : {}),
  } as unknown as ExplorerBuilderDocument,
});

const updateSource = (outputId: string, sourceKind = 'field'): ExplorerBuilderCommand => ({
  type: 'UPDATE_COLUMN_SOURCE',
  outputId,
  column: 'status',
  source: sourceKind === 'imported'
    ? { kind: 'imported', imported: { outputColumn: 'status' } }
    : { kind: 'field', field: { path: 'root.status', projectionMode: 'VALUE' } },
} as ExplorerBuilderCommand);

describe('admitAuthoringRequest', () => {
  it('admits add-columns proposals on imported recipes', () => {
    expect(admitAuthoringRequest([table('patients', true)], {
      kind: 'add-columns-proposal',
      outputId: 'patients',
    })).toEqual({ kind: 'admitted' });
  });

  it('admits add-columns proposals on ordinary tables', () => {
    expect(admitAuthoringRequest([table('patients')], {
      kind: 'add-columns-proposal',
      outputId: 'patients',
    })).toEqual({ kind: 'admitted' });
  });


  it('admits additive construction choices on imported recipes', () => {
    expect(admitAuthoringRequest([table('patients', true)], {
      kind: 'commands',
      commands: [{
        type: 'APPLY_CONSTRUCTION_CHOICE',
        outputId: 'patients',
        constructionChoice: { choiceId: 'field-choice', form: 'VALUE' },
      }],
    })).toEqual({ kind: 'admitted' });
  });

  it('refuses source writes on imported recipes with the command index', () => {
    const admission = admitAuthoringRequest([table('patients', true)], {
      kind: 'commands',
      commands: [{ type: 'UPDATE_COLUMN', outputId: 'patients', column: 'status' }, updateSource('patients')],
    });
    expect(admission.kind).toBe('refused');
    if (admission.kind === 'refused') expect(admission.refusal.commandIndex).toBe(1);
  });

  it('refuses imported source kinds independently of the recipe pin', () => {
    expect(admitAuthoringRequest([table('patients')], {
      kind: 'commands',
      commands: [updateSource('patients', 'imported')],
    }).kind).toBe('refused');
  });

  it('admits non-source commands and unknown output IDs', () => {
    expect(admitAuthoringRequest([table('patients', true)], {
      kind: 'commands',
      commands: [{ type: 'UPDATE_COLUMN', outputId: 'patients', column: 'status' }],
    })).toEqual({ kind: 'admitted' });
    expect(admitAuthoringRequest([table('patients')], {
      kind: 'commands',
      commands: [updateSource('missing')],
    })).toEqual({ kind: 'admitted' });
  });
});
