import { test as base } from '@playwright/test';
import {
  failDevJourney,
  finishDevJourney,
  prepareDevJourney,
  recordDevJourneyPreparationFailure,
  sanitizeDevJourneyDiagnostic,
  verifyBrowserScenario,
  verifyCurrentBuilderDOM,
  verifyJ01BrowserScenario,
  verifyJ02BrowserScenario,
  verifyJ03BrowserScenario,
  verifyJ04BrowserScenario,
  verifyJ04PatientOperatorScenario,
  verifyJ05BrowserScenario,
} from '../../loom-dev.mjs';

const MAX_DIAGNOSTICS = 100;

const test = base.extend({
  devDiagnostics: async ({ page }, use) => {
    const diagnostics = {
      console: [],
      pageErrors: [],
      networkFailures: [],
      httpFailures: [],
      assetFailures: [],
      actions: [],
      droppedDiagnostics: 0,
      ownedOrigins: [],
      captureScreenshots: process.env.LOOM_DEV_CAPTURE_SCREENSHOTS === '1',
    };
    const ownedOrigins = new Set();
    const pendingBodyCaptures = new Set();
    const push = (collection, item) => {
      if (collection.length < MAX_DIAGNOSTICS) collection.push(item);
      else diagnostics.droppedDiagnostics += 1;
    };
    const flushPending = async () => {
      let timer;
      await Promise.race([
        Promise.allSettled([...pendingBodyCaptures]),
        new Promise((resolve) => { timer = setTimeout(resolve, 1100); }),
      ]);
      if (timer) clearTimeout(timer);
    };
    diagnostics.flushPending = flushPending;
    const safeURL = (raw) => {
      try {
        const url = new URL(raw);
        return sanitizeDevJourneyDiagnostic(`${url.origin}${url.pathname}`);
      } catch {
        return sanitizeDevJourneyDiagnostic(String(raw ?? ''));
      }
    };
    diagnostics.setOrigins = (urls) => {
      for (const raw of urls) {
        if (raw) ownedOrigins.add(new URL(raw).origin);
      }
      diagnostics.ownedOrigins = [...ownedOrigins];
    };
    const isOwned = (raw) => {
      try { return ownedOrigins.has(new URL(raw).origin); }
      catch { return false; }
    };
    const onConsole = (message) => {
      if (message.type() !== 'error') return;
      const location = message.location().url;
      if (location && !isOwned(location)) return;
      if (location && new URL(location).pathname === '/favicon.ico' && message.text().includes('404')) {
        push(diagnostics.assetFailures, { kind: 'console', url: safeURL(location), status: 404 });
        return;
      }
      push(diagnostics.console, {
        type: message.type(),
        text: sanitizeDevJourneyDiagnostic(message.text()),
        location: location ? safeURL(location) : undefined,
      });
    };
    const onPageError = (error) => push(diagnostics.pageErrors, {
      name: error.name,
      message: sanitizeDevJourneyDiagnostic(error.message),
      stack: sanitizeDevJourneyDiagnostic(error.stack),
    });
    const onRequestFailed = (request) => {
      if (!isOwned(request.url())) return;
      const headers = request.headers();
      push(diagnostics.networkFailures, {
        url: safeURL(request.url()),
        method: request.method(),
        failure: sanitizeDevJourneyDiagnostic(request.failure()?.errorText),
        observedAt: Date.now(),
        ...(headers['x-request-id'] ? { requestId: headers['x-request-id'] } : {}),
        ...(diagnostics.activeAction ? { triggerAction: diagnostics.activeAction.label } : {}),
      });
    };
    const onResponse = (response) => {
      if (!isOwned(response.url()) || response.status() < 400) return;
      const entry = {
        url: safeURL(response.url()),
        status: response.status(),
        method: response.request().method(),
        observedAt: Date.now(),
      };
      if (new URL(response.url()).pathname.endsWith('/favicon.ico') && response.status() === 404) {
        push(diagnostics.assetFailures, entry);
        return;
      }
      push(diagnostics.httpFailures, entry);
      let timer;
      let capture;
      const timeout = new Promise((resolve) => {
        timer = setTimeout(() => resolve(undefined), 1000);
      });
      capture = Promise.race([response.text(), timeout])
        .then((body) => {
          if (body === undefined) entry.bodyError = 'response body capture timed out after 1000 ms';
          else entry.body = sanitizeDevJourneyDiagnostic(body);
        })
        .catch((error) => { entry.bodyError = sanitizeDevJourneyDiagnostic(error.message); })
        .finally(() => {
          if (timer) clearTimeout(timer);
          pendingBodyCaptures.delete(capture);
        });
      pendingBodyCaptures.add(capture);
    };

    page.on('console', onConsole);
    page.on('pageerror', onPageError);
    page.on('requestfailed', onRequestFailed);
    page.on('response', onResponse);
    try {
      await use(diagnostics);
    } finally {
      await flushPending();
      page.removeListener('console', onConsole);
      page.removeListener('pageerror', onPageError);
      page.removeListener('requestfailed', onRequestFailed);
      page.removeListener('response', onResponse);
    }
  },
});

const runJourney = async (command, { page, devDiagnostics }, testInfo) => {
  testInfo.setTimeout(600_000);
  let journey;
  try {
    journey = await prepareDevJourney(command);
    devDiagnostics.setOrigins([journey.target.uiUrl, journey.target.apiUrl, journey.entryTarget.uiUrl]);
    const { target, report, verificationTarget, entryTarget, externalManifest, fixture } = journey;
    if (command === 'verify-current') {
      await verifyCurrentBuilderDOM(target, report, journey.explorerId, journey.builderState, page, devDiagnostics);
    } else if (command === 'verify-fast' || command === 'verify-full') {
      await verifyBrowserScenario(verificationTarget, report, command === 'verify-full', entryTarget, page, devDiagnostics);
    } else if (command === 'verify-j01') {
      await verifyJ01BrowserScenario(verificationTarget, report, entryTarget, externalManifest, page, devDiagnostics);
    } else if (command === 'verify-j02') {
      await verifyJ02BrowserScenario(verificationTarget, report, entryTarget, page, devDiagnostics);
    } else if (command === 'verify-j03') {
      await verifyJ03BrowserScenario(verificationTarget, report, entryTarget, page, devDiagnostics);
    } else if (command === 'verify-j04-patient') {
      await verifyJ04PatientOperatorScenario(verificationTarget, report, entryTarget, fixture, page, devDiagnostics);
    } else if (command === 'verify-j04') {
      await verifyJ04PatientOperatorScenario(verificationTarget, report, entryTarget, fixture, page, devDiagnostics);
      await verifyJ04BrowserScenario(verificationTarget, report, entryTarget, fixture, page, devDiagnostics);
    } else if (command === 'verify-j05') {
      await verifyJ05BrowserScenario(verificationTarget, report, entryTarget, page, devDiagnostics);
    }
    await devDiagnostics.flushPending();
    if (devDiagnostics.droppedDiagnostics > 0) {
      throw new Error(`native browser diagnostics dropped ${devDiagnostics.droppedDiagnostics} entries at the ${MAX_DIAGNOSTICS}-entry limit`);
    }
    report.browserLifecycle = { kind: 'official-playwright-test-page', status: 'passed' };
    await finishDevJourney(journey, testInfo);
  } catch (error) {
    if (journey) {
      journey.report.browserLifecycle = { kind: 'official-playwright-test-page', status: 'failed' };
      await failDevJourney(journey, error, testInfo);
    } else {
      try { await recordDevJourneyPreparationFailure(command, error, process.env, testInfo); } catch {}
    }
    throw error;
  } finally {
    await journey?.dispose();
  }
};

for (const command of [
  'verify-current', 'verify-fast', 'verify-full', 'verify-j01', 'verify-j02',
  'verify-j03', 'verify-j04', 'verify-j04-patient', 'verify-j05',
]) {
  test(`Loom development journey ${command} @dev-journey:${command}`, async ({ page, devDiagnostics }, testInfo) => {
    await runJourney(command, { page, devDiagnostics }, testInfo);
  });
}
