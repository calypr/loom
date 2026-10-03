import assert from "node:assert/strict";
import test from "node:test";

import { classifyResult, normalizeAuthorization, sanitizeValue, summarizeBuilder } from "./verify.mjs";

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
  assert.equal(classifyResult({ result: "PASSED" }, { authRedirected: false, authPromptVisible: false, fallbackVisible: true, failedRequests: [], consoleErrors: [] }), "UI_FAILED");
  assert.equal(classifyResult({ result: "PASSED" }, { authRedirected: false, authPromptVisible: true, fallbackVisible: false, failedRequests: [], consoleErrors: [] }), "BROWSER_AUTH_REQUIRED");
});
