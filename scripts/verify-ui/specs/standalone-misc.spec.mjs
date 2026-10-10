import { readFileSync } from 'node:fs';
import { expect, test as basicTest } from '../helpers/fixtures.mjs';
import { test as cdaTest } from '../helpers/cda-fixtures.mjs';
import { verifyBaseSettings } from '../workflows/verify-base-settings.mjs';
import { verifyBrowserDisclosure } from '../helpers/verify-browser-disclosure.mjs';
import { verifyCompoundCodedGroup } from '../workflows/verify-compound-coded-group.mjs';
import { verifyConstructionRemovalUI } from '../workflows/verify-construction-removal-ui.mjs';
import { verifyPopulationRowUI } from '../workflows/verify-population-row-ui.mjs';
import { verifyRowActionsClarity } from '../workflows/verify-row-actions-clarity.mjs';
import { verifyRowDefinitionUI } from '../workflows/verify-row-definition-ui.mjs';
import { verifyRowRebaseUI } from '../workflows/verify-row-rebase-ui.mjs';
import { verifyUpstreamEdits } from '../workflows/verify-upstream-edits.mjs';
import { verifyContributorExistsBrowser } from '../../../ui/packages/loom-ui/scripts/verify-cda-contributor-exists-browser.mjs';
import { captureCDARequests } from '../helpers/cda-playwright-requests.mjs';
import { sanitizePayload } from '../helpers/playwright-browser.mjs';

const project = process.env.LOOM_CDA_PROJECT ?? 'loom_dev_cda_fhir';
const generation = process.env.LOOM_CDA_GENERATION ?? 'cda-fhir-v1';
const cdaDefaults = {
  cdaProject: project,
  cdaGeneration: generation,
  cdaScenarioID: 'standalone-misc-cda-unmapped',
  cdaRequireClickhouse: false,
};

basicTest('native disclosure controls reject hidden edits and accept visible typing', async ({ page }) => {
  const report = await verifyBrowserDisclosure({ page });
  expect(report.status).toBe('passed');
});

const cdaCase = (caseName, title, define, options = {}) => cdaTest.describe(title, () => {
  cdaTest.use({ ...cdaDefaults, ...options, cdaCaseName: caseName });
  define();
});
const existingExplorer = process.env.LOOM_CDA_EXPLORER_ID ?? process.env.LOOM_CDA_EXPLORER;
const selectionEvidencePath = process.env.LOOM_CDA_SELECTION_EVIDENCE;
const selectionEvidence = selectionEvidencePath ? JSON.parse(readFileSync(selectionEvidencePath, 'utf8')) : undefined;
const selectionTarget = selectionEvidence?.target ?? {};
const selectionFixtureOptions = {
  cdaProject: selectionTarget.project ?? project,
  cdaApiOrigin: selectionTarget.apiUrl ?? process.env.LOOM_CDA_API_ORIGIN,
  cdaUiOrigin: selectionTarget.uiUrl ?? process.env.LOOM_CDA_UI_ORIGIN,
  cdaGeneration: selectionTarget.generation ?? selectionEvidence?.generation ?? selectionEvidence?.expectedRefs?.[0]?.generation ?? generation,
  cdaExplorer: selectionEvidence?.explorerId ?? existingExplorer,
};

cdaCase('base-settings', 'Native standalone base row settings', () => {
  cdaTest('preserves authored construction through cancel, apply, and reload', async ({ page, cda }) => {
    await verifyBaseSettings({ page, cda });
  });
});

basicTest.describe('Native standalone compound coded grouping: basic', () => {
  basicTest.use({
    scenarioID: 'standalone-misc',
    caseName: 'compound-coded-group-basic',
    fixtureDir: 'testdata/devloop-fixture',
  });

  basicTest('selects, edits, removes, and reloads the coded group from a fresh synthetic fixture', async ({ page, workflow, loomContext }, testInfo) => {
    const { report, target } = loomContext;
    report.nativeRequests ??= [];
    const apiOrigin = target.apiUrl;
    const uiOrigin = target.uiUrl;
    const ownedTarget = { ...target, arangoContainer: `${target.composeProject}-arangodb-1` };
    let activeAction;
    const diagnostics = () => ({
      console: (report.errors ?? []).filter(entry => entry.kind === 'console').map(entry => ({ text: entry.message, location: entry.location })),
      pageErrors: (report.errors ?? []).filter(entry => entry.kind === 'runtime').map(entry => ({ message: entry.message })),
      networkFailures: (report.errors ?? []).filter(entry => entry.kind === 'network'),
      httpFailures: (report.errors ?? []).filter(entry => entry.kind === 'http'),
      assetFailures: report.assetFailures ?? [],
    });
    const cda = {
      apiOrigin,
      uiOrigin,
      project: target.fixtureProject,
      generation: target.fixtureGeneration,
      target: ownedTarget,
      report,
      get diagnostics() { return diagnostics(); },
      wait: (predicate, args = {}, timeout = 5000) => page.waitForFunction(predicate, args, { timeout: Math.min(timeout, 5000) }),
      inspect: (callback, args = {}) => page.evaluate(callback, args),
      navigate: url => page.goto(url, { waitUntil: 'load', timeout: 5000 }),
      action: async (label, locator, perform, options) => {
        activeAction = label;
        try { return await workflow.action(label, locator, () => perform(locator), options); }
        finally { activeAction = undefined; }
      },
      captureRequests: (ownedPathPrefix, options = {}) => {
        const tracker = captureCDARequests(page, {
          apiOrigin,
          browserRequestOrigin: uiOrigin,
          appOrigins: [apiOrigin, uiOrigin],
          ownedPathPrefix,
          report,
          currentAction: () => activeAction,
          ...options,
        });
        return {
          ...tracker,
          async flush() {
            while (tracker.pendingReads.size) await Promise.allSettled([...tracker.pendingReads]);
            return report.nativeRequests;
          },
        };
      },
      waitForCapturedResponse: (tracker, predicate, timeout) => tracker.waitFor(predicate, { timeoutMs: timeout }),
      includeBrowserDiagnostics: async () => {},
      attachReport: async (name, value) => testInfo.attach(name, {
        body: Buffer.from(`${JSON.stringify(sanitizePayload(value), null, 2)}\n`),
        contentType: 'application/json',
      }),
    };
    await verifyCompoundCodedGroup({ page, cda, check: workflow.check, mode: 'basic' });
  });
});

for (const mode of ['single', 'differential']) {
  cdaCase(`compound-coded-group-${mode}`, `Native standalone compound coded grouping: ${mode}`, () => {
    cdaTest('selects, edits, removes, and reloads the coded group', async ({ page, cda }) => {
      await verifyCompoundCodedGroup({ page, cda, mode });
    });
  });
}

cdaCase('upstream-edits', 'Native standalone upstream edits', () => {
  cdaTest('retains dependent-removal warnings through apply and reload', async ({ page, cda }) => {
    await verifyUpstreamEdits({ page, cda });
  });
});

cdaCase('contributor-exists', 'Native standalone Contributor EXISTS lifecycle', () => {
  cdaTest('preserves, excludes, cancels, applies, and restores exact source rows', async ({ page, cda }) => {
    await verifyContributorExistsBrowser({ page, cda });
  });
});

cdaCase('construction-removal-ui', 'Native standalone construction removal UI', () => {
  cdaTest('previews and cancels or applies removal from an explicitly owned Explorer', async ({ page, cda }) => {
    cdaTest.skip(!existingExplorer || !process.env.LOOM_CDA_OUTPUT_ID || !process.env.LOOM_CDA_STEP_ID,
      'Requires an explicitly owned Explorer, output, and construction step');
    await verifyConstructionRemovalUI({ page, cda });
  });
}, { cdaExplorer: existingExplorer });

cdaCase('population-row-ui', 'Native standalone population row UI', () => {
  cdaTest('renders exact selected resources from explicit selection evidence', async ({ page, cda }) => {
    cdaTest.skip(!process.env.LOOM_CDA_SELECTION_EVIDENCE || !existingExplorer,
      'Requires explicit selection evidence and its owned Explorer');
    await verifyPopulationRowUI({ page, cda, selectionEvidencePath: process.env.LOOM_CDA_SELECTION_EVIDENCE });
  });
}, selectionFixtureOptions);

cdaCase('row-actions-clarity', 'Native standalone row action menu clarity', () => {
  cdaTest('leaves the explicitly owned workspace unchanged', async ({ page, cda }) => {
    cdaTest.skip(!existingExplorer,
      'Requires an explicitly owned group-related-summary-browser or cohort-add-fields-browser Explorer');
    await verifyRowActionsClarity({ page, cda });
  });
}, { cdaExplorer: existingExplorer });

cdaCase('row-definition-ui', 'Native standalone row definition UI', () => {
  cdaTest('preserves source, preview, and persisted population shape', async ({ page, cda }) => {
    cdaTest.skip(!process.env.LOOM_CDA_SELECTION_EVIDENCE,
      'Requires explicit selection-evidence JSON for the disposable row-definition Explorer');
    await verifyRowDefinitionUI({ page, cda, selectionEvidencePath: process.env.LOOM_CDA_SELECTION_EVIDENCE });
  });
}, selectionFixtureOptions);

cdaCase('row-rebase-ui', 'Native standalone row rebase UI', () => {
  cdaTest('preserves feature keys and configured filters', async ({ page, cda }) => {
    cdaTest.skip(!existingExplorer,
      'Requires an explicitly owned Patient to Observation Builder Explorer');
    await verifyRowRebaseUI({ page, cda });
  });
}, { cdaExplorer: existingExplorer });
