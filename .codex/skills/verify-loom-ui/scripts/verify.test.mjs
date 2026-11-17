import assert from "node:assert/strict";
import test from "node:test";

import { classifyResult, inspectBuilderLoadFailure, normalizeAuthorization, sanitizeValue, summarizeBuilder } from "./verify.mjs";

test("normalizes raw and prefixed bearer tokens", () => {
  assert.equal(normalizeAuthorization("secret"), "Bearer secret");
  assert.equal(normalizeAuthorization("bearer secret"), "bearer secret");
  assert.throws(() => normalizeAuthorization("\n"));
  const jwtHeader = Buffer.from(JSON.stringify({ alg: "RS256", typ: "JWT" })).toString("base64url");
  assert.throws(() => normalizeAuthorization(`${jwtHeader}.truncated`), /truncated JWT/);
  assert.throws(() => normalizeAuthorization(`${jwtHeader}.not-json.signature`), /malformed JWT payload/);
  const sessionPayload = Buffer.from(JSON.stringify({ pur: "session" })).toString("base64url");
  assert.throws(() => normalizeAuthorization(`${jwtHeader}.${sessionPayload}.signature`), /expected a Fence access token, found purpose session/);
});

test("redacts credentials without removing snapshot identity", () => {
  assert.deepEqual(sanitizeValue({ authorization: "Bearer secret", snapshotToken: "sha256:catalog", nested: { access_token: "secret" } }, ["Bearer secret", "secret"]), {
    authorization: "[REDACTED]",
    snapshotToken: "sha256:catalog",
    nested: { access_token: "[REDACTED]" },
  });
});

test("summarizes a valid builder response", () => {
  const summary = summarizeBuilder(200, {
    apiVersion: "loom.calypr.org/explorer-authoring/v2",
    kind: "ExplorerBuilderState",
    lifecycleState: "READY",
    draftVersion: 1,
    draftDigest: "sha256:draft",
    workspace: {
      apiVersion: "loom.calypr.org/explorer-authoring/v2",
      kind: "ExplorerBuilderWorkspace",
      explorer: { title: "Test" },
      documents: [],
      tabs: [],
    },
    catalog: { complete: true, generation: "g1", snapshotToken: "sha256:x", routePolicy: {}, nodes: [{}], edges: [{}, {}], candidates: [{}, {}, {}] },
  });
  assert.equal(summary.result, "PASSED");
  assert.deepEqual(summary.catalog, { complete: true, generation: "g1", snapshotToken: "sha256:x", nodes: 1, edges: 2, candidates: 3 });
});

test("rejects builder responses with missing or invalid draft identity", () => {
  const state = {
    apiVersion: "loom.calypr.org/explorer-authoring/v2",
    kind: "ExplorerBuilderState",
    lifecycleState: "NEW",
    workspace: null,
    catalog: { complete: true, generation: "g1", snapshotToken: "sha256:x", routePolicy: {}, nodes: [], edges: [], candidates: [] },
  };

  for (const invalidIdentity of [
    {},
    { draftVersion: -1, draftDigest: "sha256:x" },
    { draftVersion: 1.5, draftDigest: "sha256:x" },
    { draftVersion: 1, draftDigest: null },
  ]) {
    assert.equal(summarizeBuilder(200, { ...state, ...invalidIdentity }).result, "CONTRACT_FAILED");
  }
});

test("rejects lifecycle and top-level shapes the frontend rejects", () => {
  const state = {
    apiVersion: "loom.calypr.org/explorer-authoring/v2",
    kind: "ExplorerBuilderState",
    lifecycleState: "NEW",
    draftVersion: 0,
    draftDigest: "sha256:draft",
    workspace: null,
    catalog: { complete: true, generation: "g1", snapshotToken: "sha256:x", routePolicy: {}, nodes: [], edges: [], candidates: [] },
  };

  assert.equal(summarizeBuilder(200, { ...state, lifecycleState: "DRAFT" }).result, "CONTRACT_FAILED");
  assert.equal(summarizeBuilder(200, { ...state, workspace: {} }).result, "CONTRACT_FAILED");
  assert.equal(summarizeBuilder(200, { ...state, catalog: [] }).result, "CONTRACT_FAILED");
  assert.equal(summarizeBuilder(200, { ...state, unexpected: true }).result, "CONTRACT_FAILED");
});

test("classifies authentication, contract, and visible UI failures", () => {
  assert.equal(summarizeBuilder(401, {}).result, "AUTH_REQUIRED");
  assert.equal(summarizeBuilder(200, { kind: "legacy" }).result, "CONTRACT_FAILED");
  assert.equal(classifyResult({ result: "PASSED" }, { authRedirected: false, authPromptVisible: false, loadFailure: { textVisible: true, alertVisible: true, retryVisible: true, retryEnabled: true, complete: true }, failedRequests: [], consoleErrors: [] }), "UI_FAILED");
  assert.equal(classifyResult({ result: "PASSED" }, { authRedirected: false, authPromptVisible: true, loadFailure: { textVisible: false, alertVisible: false, retryVisible: false, retryEnabled: false, complete: false }, failedRequests: [], consoleErrors: [] }), "BROWSER_AUTH_REQUIRED");
});


test("accepts current workspace and catalog envelope keys while rejecting unknown keys", () => {
  const state = {
    apiVersion: "loom.calypr.org/explorer-authoring/v2", kind: "ExplorerBuilderState",
    lifecycleState: "READY", draftVersion: 1, draftDigest: "sha256:draft",
    workspace: {
      apiVersion: "loom.calypr.org/explorer-authoring/v2", kind: "ExplorerBuilderWorkspace",
      semanticsVersion: 11, migrationDecisions: [], explorer: { title: "Default" }, documents: [], tabs: [],
    },
    catalog: {
      snapshotToken: "sha256:catalog", generation: "generation", complete: true,
      availability: { readiness: { status: "COMPLETE", resourceInventory: { status: "COMPLETE" }, relationships: { status: "COMPLETE" }, fieldProfiles: { status: "COMPLETE" } }, resources: [], fields: [], relationships: [] }, routePolicy: {}, nodes: [], edges: [], candidates: [],
    },
  };
  assert.equal(summarizeBuilder(200, state).result, "PASSED");
  assert.equal(summarizeBuilder(200, { ...state, workspace: { ...state.workspace, unexpected: true } }).result, "CONTRACT_FAILED");
  assert.equal(summarizeBuilder(200, { ...state, catalog: { ...state.catalog, unexpected: true } }).result, "CONTRACT_FAILED");
});

test("fails an HTTP error document even without browser console or network failures", () => {
  const finalUrl = "https://example.test/builder";
  const browser = {
    finalUrl, authRedirected: false, authPromptVisible: false, loadFailure: { textVisible: false, alertVisible: false, retryVisible: false, retryEnabled: false, complete: false },
    failedRequests: [], consoleErrors: [], responses: [],
  };
  for (const status of [400, 404, 500, 504]) {
    assert.equal(classifyResult({ result: "PASSED" }, { ...browser,
      responses: [{ type: "Document", url: finalUrl, status }],
    }), "UI_FAILED");
  }
  for (const responses of [[], [{ type: "Document", url: finalUrl, status: 200 }],
    [{ type: "Fetch", url: finalUrl, status: 504 }],
    [{ type: "Document", url: "https://other.test/frame", status: 504 }]]) {
    assert.equal(classifyResult({ result: "PASSED" }, { ...browser, responses }), "PASSED");
  }
});

function failureDocument({ text = "", alertText, retry = false, outsideRetry = false, disabled = false, hidden = false } = {}) {
  const element = (innerText) => ({
    innerText,
    getClientRects: () => hidden ? [] : [{}],
    matches: () => disabled,
  });
  const button = element("Try again");
  const alert = { ...element(alertText ?? ""), querySelectorAll: () => retry ? [button] : [] };
  return {
    body: { innerText: text },
    defaultView: { getComputedStyle: () => ({ display: "block", visibility: "visible" }) },
    querySelectorAll: (selector) => selector === "button"
      ? retry || outsideRetry ? [button] : []
      : alertText !== undefined || retry ? [alert] : [],
  };
}

test("load failure requires the actual visible alert and its enabled scoped retry", () => {
  const text = "Couldn’t load this dataset.";
  const complete = inspectBuilderLoadFailure(failureDocument({ text, alertText: text, retry: true }));
  assert.deepEqual(complete, { textVisible: true, alertVisible: true, retryVisible: true, retryEnabled: true, complete: true });
  const browser = { failedRequests: [], consoleErrors: [] };
  for (const document of [
    failureDocument({ text, alertText: text, retry: true }),
    failureDocument({ text }),
    failureDocument({ text, alertText: text }),
    failureDocument({ alertText: "", retry: true }),
    failureDocument({ outsideRetry: true }),
    failureDocument({ text, alertText: text, retry: true, disabled: true }),
  ]) {
    const loadFailure = inspectBuilderLoadFailure(document);
    assert.equal(classifyResult({ result: "PASSED" }, { ...browser, loadFailure }), "UI_FAILED");
  }
  assert.equal(inspectBuilderLoadFailure(failureDocument({ text, alertText: text })).complete, false);
  assert.equal(inspectBuilderLoadFailure(failureDocument({ text, alertText: text, retry: true, disabled: true })).complete, false);
  const unrelatedRetry = inspectBuilderLoadFailure(failureDocument({ text, alertText: "Other alert", retry: true }));
  assert.equal(unrelatedRetry.retryEnabled, false, "retry outside the failure alert cannot complete the failure UI");
  for (const document of [failureDocument(), failureDocument({ alertText: text, retry: true, hidden: true })]) {
    const loadFailure = inspectBuilderLoadFailure(document);
    assert.equal(classifyResult({ result: "PASSED" }, { ...browser, loadFailure }), "PASSED");
  }
});
