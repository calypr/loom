import { basename } from 'node:path';
import {
  browserEval,
  captureRequests,
  click,
  fill,
  navigate,
  press,
  scrollIntoView,
  selectOption,
  waitForBrowser,
  waitForCapturedResponse,
} from './cda-playwright.mjs';

const CASE_NAME = 'medication-positive-fixture';
const FIXTURE_DIRECTORY = 'cda-zero-column-related-medication-positive';
const GENERATION = 'cda-fhir-v1';

export const createPositiveMedicationCdaContext = ({ page, workflow, testInfo, playwrightTest } = {}) => {
  const target = workflow?.target;
  const report = workflow?.report;
  if (!page || !target || !report || typeof workflow.action !== 'function' || typeof workflow.check !== 'function') {
    throw new TypeError('positive Medication case requires the standard fixture page, target, report, action, and check');
  }
  if (!Array.isArray(report.network)) {
    throw new TypeError('positive Medication case requires the standard fixture report.network diagnostics');
  }
  if (report.case !== CASE_NAME || target.fixtureGeneration !== GENERATION ||
    target.kind !== 'isolated' || !target.fixtureProject?.startsWith('loom_dev_verify_') ||
    basename(target.fixtureDir ?? '') !== FIXTURE_DIRECTORY) {
    throw new Error('positive Medication adapter accepts only its owned fixture case/project/generation');
  }
  if (!testInfo || typeof testInfo.attach !== 'function' || !playwrightTest || typeof playwrightTest.step !== 'function') {
    throw new TypeError('positive Medication case requires native Playwright test reporting');
  }

  report.nativeRequests ??= [];
  report.apiRequests ??= [];
  const cdaTarget = { ...target, arangoContainer: `${target.composeProject}-arangodb-1` };
  const actions = {
    action: workflow.action,
    step: (label, callback) => playwrightTest.step(label, callback),
  };
  const apiOrigin = target.apiUrl.replace(/\/$/, '');
  const uiOrigin = target.uiUrl.replace(/\/$/, '');

  return {
    target: cdaTarget,
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    caseName: report.case,
    apiOrigin,
    uiOrigin,
    report,
    check: workflow.check,
    click: (selector, identity, timeout) => click(page, selector, identity, timeout, actions),
    fill: (selector, value, identity, timeout) => fill(page, selector, value, identity, timeout, actions),
    press: (selector, key, timeout) => press(page, selector, key, timeout, actions),
    selectOption: (selector, value, options) => selectOption(page, selector, value, options, actions),
    scrollIntoView: (selector, identity, timeout) => scrollIntoView(page, selector, identity, timeout, actions),
    navigate: url => navigate(page, url, actions),
    inspect: (callback, args) => browserEval(page, callback, args),
    wait: (callback, args, timeout) => waitForBrowser(page, callback, args, timeout),
    captureRequests: (pathPrefix, options = {}) => captureRequests(page, report, pathPrefix, {
      apiOrigin,
      uiOrigin,
      currentAction: () => report.activeAction?.label ?? report.activeAction?.name,
      ...options,
    }),
    waitForCapturedResponse: (tracker, predicate, timeout) => waitForCapturedResponse(page, tracker, predicate, timeout),
    attachReport: (name, value = report) => testInfo.attach(name, {
      body: `${JSON.stringify(value, null, 2)}\n`,
      contentType: 'application/json',
    }),
  };
};
