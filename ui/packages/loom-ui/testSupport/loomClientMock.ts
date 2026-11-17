import { vi } from 'vitest';
import type { LoomClient } from '../src/api';

const never = () => new Promise<never>(() => undefined);

/**
 * Complete typed LoomClient double. Every production client method exists, so a
 * Builder render cannot fail with "method is not a function" when it starts
 * using a method the test does not care about. Override only the methods the
 * case exercises; untouched methods stay pending rather than resolving bogus
 * contract data.
 */
const defaults = {
  createSelection: vi.fn(never),
  getSelection: vi.fn(never),
  proposePopulationMemberRemoval: vi.fn(never),
  createExplicitGroupRevision: vi.fn(never),
  listExplorers: vi.fn(never),
  getBuilder: vi.fn(never),
  getCapability: vi.fn(never),
  getExplorer: vi.fn(never),
  applyCommands: vi.fn(never),
  assessRowChange: vi.fn(never),
  reconcile: vi.fn(never),
  suggestions: vi.fn(never),
  searchSchemaFields: vi.fn(never),
  browseSemanticInventory: vi.fn(never),
  browseFrameSourceOptions: vi.fn(never),
  inspectColumnSource: vi.fn(never),
  searchConstructionChoices: vi.fn(never),
  searchRelatedExpandChoices: vi.fn(never),
  searchRelatedExpandContributors: vi.fn(never),
  searchRelatedFieldChoices: vi.fn(never),
  searchPopulationRoutes: vi.fn(never),
  listRowDefinitionChoices: vi.fn(never),
  proposeRowDefinition: vi.fn(never),
  getConstructionCapabilities: vi.fn(never),
  discoverConstructionCategories: vi.fn(never),
  getConstructionInputs: vi.fn(never),
  describeAuthoringNetwork: vi.fn(never),
  proposeColumnPolicy: vi.fn(never),
  proposeSourceRelationship: vi.fn(never),
  proposeConstruction: vi.fn(never),
  proposeConstructionChoices: vi.fn(never),
  preview: vi.fn(never),
  populationMapping: vi.fn(never),
  cellTrace: vi.fn(never),
  rowLineage: vi.fn(never),
  prepareArtifact: vi.fn(never),
  artifactDownloadURL: vi.fn(() => ''),
  listInterpretationLibraries: vi.fn(never),
  getInterpretationRevision: vi.fn(never),
  createInterpretationRevision: vi.fn(never),
  resolveConfiguredColumnContexts: vi.fn(never),
  createInterpretationRevisionFromColumn: vi.fn(never),
  previewInterpretationCandidate: vi.fn(never),
  publish: vi.fn(never),
  createExplorer: vi.fn(never),
  deleteExplorer: vi.fn(never),
  fetchGraphQL: vi.fn(never),
  rows: vi.fn(never),
  queryOutput: vi.fn(never),
  exportOutput: vi.fn(never),
  invalidate: vi.fn(),
} satisfies LoomClient;

export const createLoomClientMock = (overrides: Partial<LoomClient> = {}): LoomClient => ({
  ...defaults,
  ...overrides,
});
