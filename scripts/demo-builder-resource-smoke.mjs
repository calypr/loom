import { mkdir, writeFile } from 'node:fs/promises';
import { chromium } from 'playwright';
import { verifyBuilderResourceChoices } from './demo-builder-resource-check.mjs';

const [url, chromePath, outputDirectory, ...expectedResources] = process.argv.slice(2);
if (!url || !chromePath || !outputDirectory || expectedResources.length === 0) {
  throw new Error('usage: demo-builder-resource-smoke.mjs <url> <chrome> <output-directory> <resource>...');
}

const DOM_CAPTURE_TIMEOUT_MS = 5_000;
const bounded = async (promise, timeoutMs) => {
  let timer;
  try {
    return await Promise.race([
      Promise.resolve(promise).then((value) => ({ state: 'resolved', value }), (error) => ({ state: 'rejected', error })),
      new Promise((resolve) => { timer = setTimeout(() => resolve({ state: 'timeout' }), timeoutMs); }),
    ]);
  } finally {
    clearTimeout(timer);
  }
};
const alive = (child) => child && child.exitCode === null && child.signalCode === null;
const waitForExit = async (child, timeoutMs) => {
  if (!alive(child)) return true;
  const result = await bounded(new Promise((resolve) => child.once('exit', () => resolve(true))), timeoutMs);
  return result.state === 'resolved' || !alive(child);
};
const closeServer = async (server) => {
  const child = server.process();
  const closeResult = await bounded(Promise.resolve().then(() => server.close()), 3_000);
  if (!alive(child)) return;
  if (closeResult.state === 'resolved' && await waitForExit(child, 0)) return;

  if (alive(child)) child.kill('SIGTERM');
  if (!(await waitForExit(child, 2_000)) && alive(child)) child.kill('SIGKILL');
  if (!(await waitForExit(child, 2_000))) {
    throw new Error('Browser server process remained alive after SIGTERM and SIGKILL teardown bounds');
  }
};
const captureDOM = async (page, label) => {
  const result = await bounded(Promise.resolve().then(() => page.content()), DOM_CAPTURE_TIMEOUT_MS);
  if (result.state === 'resolved') return { html: result.value };
  const detail = result.state === 'timeout'
    ? `timed out after ${DOM_CAPTURE_TIMEOUT_MS} ms`
    : `failed: ${result.error instanceof Error ? result.error.message : String(result.error)}`;
  return { error: `Capture ${label} Builder DOM ${detail}` };
};

await mkdir(outputDirectory, { recursive: true });
const errors = [];
let stage = 'launch';
let server;
let browser;
let page;
let initialDOM = '';
let finalDOM = '';
let failure;
try {
  server = await chromium.launchServer({ headless: true, executablePath: chromePath, args: ['--no-sandbox'] });
  browser = await chromium.connect(server.wsEndpoint());
  page = await browser.newPage();
  page.on('pageerror', (error) => errors.push(error.message));
  stage = 'load';
  await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 30_000 });
  const capture = await captureDOM(page, 'initial');
  if (capture.error) throw new Error(capture.error);
  initialDOM = capture.html;
  stage = 'verify-resource-choices';
  await verifyBuilderResourceChoices(page, expectedResources, initialDOM);
  if (errors.length > 0) throw new Error(`Builder browser errors: ${errors.join('; ')}`);
  stage = 'passed';
} catch (error) {
  failure = error;
  errors.push(error instanceof Error ? error.message : String(error));
}

const cleanupErrors = [];
if (page) {
  const capture = await captureDOM(page, 'final');
  if (capture.error) cleanupErrors.push(capture.error);
  else finalDOM = capture.html;
}
try {
  if (browser) await bounded(browser.close(), 1_000);
} catch (error) {
  cleanupErrors.push(`disconnect browser: ${error instanceof Error ? error.message : String(error)}`);
}
try {
  if (server) await closeServer(server);
} catch (error) {
  cleanupErrors.push(`stop browser server: ${error instanceof Error ? error.message : String(error)}`);
}
errors.push(...cleanupErrors);
if (!failure && cleanupErrors.length > 0) {
  stage = 'failed';
  failure = new Error(cleanupErrors.join('; '));
}

try {
  await mkdir(outputDirectory, { recursive: true });
} catch (error) {
  cleanupErrors.push(`create evidence directory: ${error instanceof Error ? error.message : String(error)}`);
}
for (const [path, contents] of [
  [`${outputDirectory}/builder-initial.html`, initialDOM],
  [`${outputDirectory}/builder.html`, finalDOM],
]) {
  try {
    await writeFile(path, contents);
  } catch (error) {
    cleanupErrors.push(`write ${path}: ${error instanceof Error ? error.message : String(error)}`);
  }
}
errors.push(...cleanupErrors.filter((error) => !errors.includes(error)));
if (!failure && cleanupErrors.length > 0) {
  stage = 'failed';
  failure = new Error(cleanupErrors.join('; '));
}
try {
  await writeFile(`${outputDirectory}/builder-smoke.json`, JSON.stringify({ url, expectedResources, stage, errors }, null, 2));
} catch (error) {
  const evidenceError = new Error(`write browser smoke evidence: ${error instanceof Error ? error.message : String(error)}`);
  if (!failure) failure = evidenceError;
  else process.stderr.write(`${evidenceError.message}\n`);
}
if (failure) throw failure;
