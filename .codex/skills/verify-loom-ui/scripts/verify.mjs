#!/usr/bin/env node

import { spawn } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const DEFAULT_PAGE_URL = "https://caliper-training.ohsu.edu/org/HTAN_INT/project/BForePC/explorers/builder?explorerId=test";
const DEFAULT_API_URL = "https://caliper-training.ohsu.edu/loom/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/builder";
const DEFAULT_CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const FAILURE_TEXT = "Loom’s V2 Builder state could not be loaded. This Builder has no V1 fallback.";
const SECRET_KEYS = new Set(["authorization", "cookie", "set-cookie", "access_token", "refresh_token", "id_token"]);
const BUILDER_STATE_KEYS = new Set(["apiVersion", "kind", "lifecycleState", "draftVersion", "draftDigest", "workspace", "catalog"]);
const BUILDER_WORKSPACE_KEYS = new Set(["apiVersion", "kind", "explorer", "documents", "tabs", "sharedFilters", "fileActions"]);
const BUILDER_CATALOG_KEYS = new Set(["snapshotToken", "generation", "resolvedSchemaDigest", "authorizationScopeDigest", "complete", "routePolicy", "nodes", "edges", "candidates"]);

class UsageError extends Error {}

export function normalizeAuthorization(value) {
  const token = value?.trim();
  if (!token || /[\r\n]/.test(token)) {
    throw new UsageError("LOOM_E2E_TOKEN or --token-file must contain one non-empty token");
  }
  const credential = token.replace(/^bearer\s+/i, "");
  const segments = credential.split(".");
  if (segments.length >= 2) {
    try {
      const header = JSON.parse(Buffer.from(segments[0], "base64url").toString("utf8"));
      if (header?.typ === "JWT" && !header.enc) {
        if (segments.length !== 3) {
          throw new UsageError(`token appears to be a truncated JWT: expected 3 segments, found ${segments.length}`);
        }
        let payload;
        try {
          payload = JSON.parse(Buffer.from(segments[1], "base64url").toString("utf8"));
        } catch {
          throw new UsageError("token has a malformed JWT payload; copy the complete access_token cookie value");
        }
        if (payload?.pur && payload.pur !== "access") {
          throw new UsageError(`expected a Fence access token, found purpose ${payload.pur}`);
        }
        if (payload?.exp && payload.exp <= Math.floor(Date.now() / 1000)) {
          throw new UsageError("Fence access token is expired");
        }
      }
    } catch (error) {
      if (error instanceof UsageError) throw error;
    }
  }
  return /^bearer\s+/i.test(token) ? token : `Bearer ${token}`;
}

export function sanitizeValue(value, secrets = []) {
  if (Array.isArray(value)) {
    return value.map((item) => sanitizeValue(item, secrets));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [
      key,
      SECRET_KEYS.has(key.toLowerCase()) ? "[REDACTED]" : sanitizeValue(item, secrets),
    ]));
  }
  if (typeof value !== "string") {
    return value;
  }
  return secrets.filter(Boolean).reduce((text, secret) => text.split(secret).join("[REDACTED]"), value);
}

function isRecord(value) {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

function hasOnlyKeys(value, keys) {
  return isRecord(value) && Object.keys(value).every((key) => keys.has(key));
}

function isNonEmptyString(value) {
  return typeof value === "string" && value.trim().length > 0;
}

function hasValidWorkspaceEnvelope(workspace) {
  return hasOnlyKeys(workspace, BUILDER_WORKSPACE_KEYS)
    && workspace.apiVersion === "loom.calypr.org/explorer-authoring/v2"
    && workspace.kind === "ExplorerBuilderWorkspace"
    && hasOnlyKeys(workspace.explorer, new Set(["title", "description"]))
    && isNonEmptyString(workspace.explorer.title)
    && Array.isArray(workspace.documents)
    && Array.isArray(workspace.tabs);
}

function hasValidCatalogEnvelope(catalog) {
  return hasOnlyKeys(catalog, BUILDER_CATALOG_KEYS)
    && isNonEmptyString(catalog.snapshotToken)
    && isNonEmptyString(catalog.generation)
    && isRecord(catalog.routePolicy)
    && Array.isArray(catalog.nodes)
    && Array.isArray(catalog.edges)
    && (catalog.candidates === undefined || Array.isArray(catalog.candidates));
}

export function summarizeBuilder(status, body, headers = {}) {
  const catalog = isRecord(body) ? body.catalog : undefined;
  const error = isRecord(body) ? body.error : undefined;
  const hasValidStateShape = isRecord(body)
    && Object.keys(body).every((key) => BUILDER_STATE_KEYS.has(key))
    && hasValidCatalogEnvelope(body.catalog)
    && (body.lifecycleState === "NEW"
      ? body.workspace === null
      : body.lifecycleState === "READY"
        && hasValidWorkspaceEnvelope(body.workspace));
  const hasValidDraftIdentity = Number.isInteger(body?.draftVersion)
    && body.draftVersion >= 0
    && typeof body.draftDigest === "string";
  const summary = {
    httpStatus: status,
    requestId: headers["x-request-id"] ?? headers["x-reqid"] ?? null,
    apiVersion: body?.apiVersion ?? null,
    kind: body?.kind ?? null,
    lifecycleState: body?.lifecycleState ?? null,
    hasWorkspace: body?.workspace != null,
    errorCode: error && typeof error === "object" ? error.code ?? body?.code ?? null : body?.code ?? null,
    errorMessage: typeof error === "string" ? error : error?.message ?? body?.message ?? null,
    catalog: null,
  };
  if (isRecord(catalog)) {
    summary.catalog = {
      complete: catalog.complete ?? null,
      generation: catalog.generation ?? null,
      snapshotToken: catalog.snapshotToken ?? null,
      nodes: Array.isArray(catalog.nodes) ? catalog.nodes.length : null,
      edges: Array.isArray(catalog.edges) ? catalog.edges.length : null,
      candidates: Array.isArray(catalog.candidates) ? catalog.candidates.length : null,
    };
  }
  if (status === 401) summary.result = "AUTH_REQUIRED";
  else if (status === 403) summary.result = "FORBIDDEN";
  else if (status < 200 || status >= 300) summary.result = "API_FAILED";
  else if (summary.apiVersion !== "loom.calypr.org/explorer-authoring/v2" || summary.kind !== "ExplorerBuilderState" || !hasValidStateShape || !hasValidDraftIdentity || !summary.catalog) summary.result = "CONTRACT_FAILED";
  else summary.result = "PASSED";
  return summary;
}

export function classifyResult(api, browser) {
  if (api.result !== "PASSED") return api.result;
  if (!browser) return "DOCTOR_PASSED";
  if (browser.authRedirected || browser.authPromptVisible) return "BROWSER_AUTH_REQUIRED";
  if (browser.fallbackVisible) return "UI_FAILED";
  if (browser.failedRequests.length || browser.consoleErrors.length) return "UI_WARNINGS";
  return "PASSED";
}

function parseArgs(argv) {
  const options = {
    command: "drive",
    pageUrl: DEFAULT_PAGE_URL,
    apiUrl: DEFAULT_API_URL,
    chrome: process.env.LOOM_E2E_CHROME || DEFAULT_CHROME,
    tokenFile: process.env.LOOM_E2E_TOKEN_FILE || "",
    artifacts: process.env.LOOM_E2E_ARTIFACTS || path.resolve(".artifacts/loom-ui"),
    settleMs: 3000,
  };
  const args = [...argv];
  if (args[0] === "doctor" || args[0] === "drive") options.command = args.shift();
  while (args.length) {
    const flag = args.shift();
    const value = args.shift();
    if (!value) throw new UsageError(`missing value for ${flag}`);
    if (flag === "--page-url") options.pageUrl = value;
    else if (flag === "--api-url") options.apiUrl = value;
    else if (flag === "--chrome") options.chrome = value;
    else if (flag === "--token-file") options.tokenFile = value;
    else if (flag === "--artifacts") options.artifacts = path.resolve(value);
    else if (flag === "--settle-ms") options.settleMs = Number(value);
    else throw new UsageError(`unknown option ${flag}`);
  }
  if (!Number.isFinite(options.settleMs) || options.settleMs < 0 || options.settleMs > 30000) {
    throw new UsageError("--settle-ms must be between 0 and 30000");
  }
  return options;
}

async function loadAuthorization(options) {
  if (process.env.LOOM_E2E_TOKEN) return normalizeAuthorization(process.env.LOOM_E2E_TOKEN);
  if (!options.tokenFile) throw new UsageError("set LOOM_E2E_TOKEN or pass --token-file PATH");
  const info = await stat(options.tokenFile);
  if ((info.mode & 0o077) !== 0) throw new UsageError(`token file must not be accessible by group or others: ${options.tokenFile}`);
  return normalizeAuthorization(await readFile(options.tokenFile, "utf8"));
}

async function fetchBuilder(apiUrl, authorization) {
  const response = await fetch(apiUrl, {
    headers: { authorization, accept: "application/json" },
    redirect: "manual",
  });
  const text = await response.text();
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    body = { message: text.slice(0, 4000) };
  }
  const headers = Object.fromEntries(response.headers.entries());
  return { summary: summarizeBuilder(response.status, body, headers), body: sanitizeValue(body, [authorization]) };
}

class CDPClient {
  constructor(socket) {
    this.socket = socket;
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Map();
    socket.addEventListener("message", (event) => this.receive(event.data));
  }

  static async connect(url) {
    const socket = new WebSocket(url);
    await new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", reject, { once: true });
    });
    return new CDPClient(socket);
  }

  receive(raw) {
    const message = JSON.parse(raw);
    if (message.id) {
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(`${pending.method}: ${message.error.message}`));
      else pending.resolve(message.result ?? {});
      return;
    }
    for (const listener of this.listeners.get(message.method) ?? []) listener(message.params ?? {});
  }

  send(method, params = {}) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { method, resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  on(method, listener) {
    const listeners = this.listeners.get(method) ?? [];
    listeners.push(listener);
    this.listeners.set(method, listeners);
  }

  close() {
    this.socket.close();
  }
}

async function waitForFile(file, process, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (process.exitCode != null) throw new Error(`Chrome exited before DevTools was ready (${process.exitCode})`);
    try {
      return await readFile(file, "utf8");
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Chrome did not publish its DevTools port within 10 seconds");
}

async function runBrowser(options, authorization, artifactDir) {
  const profile = await mkdtemp(path.join(tmpdir(), "loom-ui-chrome-"));
  const chrome = spawn(options.chrome, [
    "--headless=new",
    "--remote-debugging-port=0",
    `--user-data-dir=${profile}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-background-networking",
    "--disable-component-update",
    "--disable-sync",
    "--ignore-certificate-errors",
    "about:blank",
  ], { stdio: "ignore" });
  let client;
  try {
    const activePort = await waitForFile(path.join(profile, "DevToolsActivePort"), chrome);
    const port = activePort.split(/\r?\n/, 1)[0];
    const target = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: "PUT" }).then((response) => response.json());
    client = await CDPClient.connect(target.webSocketDebuggerUrl);
    const consoleErrors = [];
    const failedRequests = [];
    const responses = [];
    const requests = new Map();
    const secrets = [authorization, authorization.replace(/^Bearer\s+/i, "")];
    const pageOrigin = new URL(options.pageUrl).origin;

    client.on("Runtime.consoleAPICalled", ({ type, args }) => {
      if (type !== "error" && type !== "warning") return;
      consoleErrors.push(sanitizeValue({ type, text: args.map((arg) => arg.value ?? arg.description ?? "").join(" ") }, secrets));
    });
    client.on("Runtime.exceptionThrown", ({ exceptionDetails }) => {
      consoleErrors.push(sanitizeValue({ type: "exception", text: exceptionDetails?.exception?.description ?? exceptionDetails?.text ?? "unknown exception" }, secrets));
    });
    client.on("Network.requestWillBeSent", ({ requestId, request, type }) => {
      requests.set(requestId, { url: request.url, type });
    });
    client.on("Network.loadingFailed", ({ requestId, errorText, canceled, type }) => {
      if (!canceled) failedRequests.push({ requestId, type, url: requests.get(requestId)?.url ?? null, errorText });
    });
    client.on("Network.responseReceived", ({ requestId, response, type }) => {
      if (!["Document", "Fetch", "XHR"].includes(type)) return;
      responses.push({
        requestId,
        type,
        url: response.url,
        status: response.status,
        mimeType: response.mimeType,
        requestIdHeader: response.headers?.["x-request-id"] ?? response.headers?.["x-reqid"] ?? null,
      });
    });

    await Promise.all([
      client.send("Page.enable"),
      client.send("Runtime.enable"),
      client.send("Network.enable"),
      client.send("Fetch.enable", { patterns: [{ urlPattern: `${pageOrigin}/*` }] }),
    ]);
    client.on("Fetch.requestPaused", ({ requestId, request }) => {
      const headers = Object.entries(request.headers)
        .filter(([name]) => name.toLowerCase() !== "authorization")
        .map(([name, value]) => ({ name, value: String(value) }));
      headers.push({ name: "Authorization", value: authorization });
      client.send("Fetch.continueRequest", { requestId, headers }).catch((error) => {
        failedRequests.push({ requestId, type: "Fetch", url: request.url, errorText: error.message });
      });
    });
    const loaded = new Promise((resolve) => client.on("Page.loadEventFired", resolve));
    await client.send("Page.navigate", { url: options.pageUrl });
    let loadTimeout;
    try {
      await Promise.race([loaded, new Promise((_, reject) => {
        loadTimeout = setTimeout(() => reject(new Error("page load timed out after 30 seconds")), 30000);
      })]);
    } finally {
      clearTimeout(loadTimeout);
    }
    await new Promise((resolve) => setTimeout(resolve, options.settleMs));

    const evaluated = await client.send("Runtime.evaluate", {
      expression: `JSON.stringify({url: location.href, title: document.title, text: document.body?.innerText ?? "", html: document.documentElement?.outerHTML ?? ""})`,
      returnByValue: true,
    });
    const page = JSON.parse(evaluated.result.value);
    const screenshot = await client.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
    await writeFile(path.join(artifactDir, "page.png"), Buffer.from(screenshot.data, "base64"));
    await writeFile(path.join(artifactDir, "page.html"), sanitizeValue(page.html, secrets));

    return {
      finalUrl: page.url,
      title: page.title,
      authRedirected: /\/login|\/user\/login|oauth|authorize/i.test(page.url),
      authPromptVisible: /Protected Content/i.test(page.text) && /log in to your account/i.test(page.text),
      fallbackVisible: page.text.includes(FAILURE_TEXT),
      bodyPreview: sanitizeValue(page.text.slice(0, 4000), secrets),
      consoleErrors,
      failedRequests,
      responses: sanitizeValue(responses, secrets),
      screenshot: "page.png",
      dom: "page.html",
    };
  } finally {
    client?.close();
    if (chrome.exitCode == null) chrome.kill("SIGTERM");
    await new Promise((resolve) => {
      if (chrome.exitCode != null) resolve();
      else {
        chrome.once("exit", resolve);
        setTimeout(() => {
          if (chrome.exitCode == null) chrome.kill("SIGKILL");
          resolve();
        }, 3000).unref();
      }
    });
    await rm(profile, { recursive: true, force: true });
  }
}

async function createArtifactDir(root) {
  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  const directory = path.join(root, stamp);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  await chmod(directory, 0o700);
  return directory;
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const authorization = await loadAuthorization(options);
  const artifactDir = await createArtifactDir(options.artifacts);
  const startedAt = new Date().toISOString();
  const api = await fetchBuilder(options.apiUrl, authorization);
  await writeFile(path.join(artifactDir, "builder-response.json"), `${JSON.stringify(api.body, null, 2)}\n`, { mode: 0o600 });
  const browser = options.command === "drive" ? await runBrowser(options, authorization, artifactDir) : null;
  const result = {
    schemaVersion: 1,
    result: classifyResult(api.summary, browser),
    startedAt,
    finishedAt: new Date().toISOString(),
    target: { pageUrl: options.pageUrl, apiUrl: options.apiUrl },
    api: api.summary,
    browser,
  };
  await writeFile(path.join(artifactDir, "report.json"), `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
  console.log(`loom-ui ${result.result} artifacts=${artifactDir}`);
  console.log(`api status=${result.api.httpStatus} kind=${result.api.kind ?? "none"} request_id=${result.api.requestId ?? "none"}`);
  if (browser) console.log(`browser url=${browser.finalUrl} auth_prompt=${browser.authPromptVisible} fallback=${browser.fallbackVisible} console_errors=${browser.consoleErrors.length} failed_requests=${browser.failedRequests.length}`);
  if (!["PASSED", "DOCTOR_PASSED", "UI_WARNINGS"].includes(result.result)) process.exitCode = 1;
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    const prefix = error instanceof UsageError ? "usage" : "loom-ui";
    console.error(`${prefix}: ${error.message}`);
    process.exitCode = error instanceof UsageError ? 2 : 1;
  });
}
