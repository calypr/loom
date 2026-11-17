import { vi, type Mock } from 'vitest';
import type { LoomClient } from '../src/api';

const never = () => new Promise<never>(() => undefined);

/**
 * Complete typed LoomClient double. Every production client method exists, so a
 * Builder render cannot fail with "method is not a function" when it starts
 * using a method the test does not care about. Override only the methods the
 * case exercises; untouched methods stay pending rather than resolving bogus
 * contract data.
 */
const pending = (): Mock => vi.fn(never) as Mock;

type LoomClientMockDefaults = LoomClient & { [Key in keyof LoomClient]-?: Mock };

const defaults = {
  createSelection: pending(),
  getSelection: pending(),
  proposePopulationMemberRemoval: pending(),
  createExplicitGroupRevision: pending(),
  listExplorers: pending(),
  getBuilder: pending(),
  getProjectViewBuilder: pending(),
  saveProjectViewConfig: pending(),
  deleteProjectViewConfig: pending(),
  saveProjectViewDefaults: pending(),
  adoptProjectViewBuilderCatalogs: pending(),
  getCapability: pending(),
  getExplorer: pending(),
  applyCommands: pending(),
  assessRowChange: pending(),
  reconcile: pending(),
  suggestions: pending(),
  searchSchemaFields: pending(),
  browseSemanticInventory: pending(),
  browseFrameSourceOptions: pending(),
  inspectColumnSource: pending(),
  searchConstructionChoices: pending(),
  searchRelatedExpandChoices: pending(),
  searchRelatedExpandContributors: pending(),
  searchRelatedFieldChoices: pending(),
  searchPopulationRoutes: pending(),
  listRowDefinitionChoices: pending(),
  proposeRowDefinition: pending(),
  getConstructionCapabilities: pending(),
  getConstructionColumnPopulation: pending(),
  discoverConstructionCategories: pending(),
  getConstructionInputs: pending(),
  describeAuthoringNetwork: pending(),
  proposeColumnPolicy: pending(),
  proposeSourceRelationship: pending(),
  proposeConstruction: pending(),
  proposeConstructionChoices: pending(),
  preview: pending(),
  populationMapping: pending(),
  cellTrace: pending(),
  rowLineage: pending(),
  prepareArtifact: pending(),
  artifactDownloadURL: vi.fn(() => '') as Mock,
  listInterpretationLibraries: pending(),
  getInterpretationRevision: pending(),
  createInterpretationRevision: pending(),
  resolveConfiguredColumnContexts: pending(),
  createInterpretationRevisionFromColumn: pending(),
  previewInterpretationCandidate: pending(),
  publish: pending(),
  createExplorer: pending(),
  deleteExplorer: pending(),
  fetchGraphQL: pending(),
  rows: pending(),
  queryOutput: pending(),
  exportOutput: pending(),
  invalidate: vi.fn() as Mock,
} satisfies LoomClientMockDefaults;

export function createLoomClientMock(): LoomClientMockDefaults;
export function createLoomClientMock<Overrides extends Partial<LoomClient>>(
  overrides: Overrides,
): Omit<LoomClientMockDefaults, keyof Overrides> & Overrides;
export function createLoomClientMock(overrides: Partial<LoomClient> = {}) {
  return { ...defaults, ...overrides };
}
