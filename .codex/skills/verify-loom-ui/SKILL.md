---
name: verify-loom-ui
description: Drive the locally deployed authenticated Calypr Explorer Builder through Chrome and diagnose frontend-to-Loom V2 contract failures without manual screenshots.
---

# Verify Loom UI

Use this for the local `caliper-training.ohsu.edu` Builder surface. It drives a disposable Chrome profile through the real reverse proxy and preserves sanitized evidence under `.artifacts/loom-ui/`.

## Launch

The harness uses the already deployed local Kubernetes stack and installed Google Chrome. Do not start a second frontend or Loom instance.

Provide a short-lived token through `LOOM_E2E_TOKEN` or a mode `0600` file passed with `--token-file`. Never paste the token into a prompt or command output.

## Doctor

Run the authenticated Builder API minimum-envelope probe before opening Chrome:

```bash
node .codex/skills/verify-loom-ui/scripts/verify.mjs doctor --token-file /path/to/token
```

Proceed only when it reports `DOCTOR_PASSED`. `AUTH_REQUIRED`, `FORBIDDEN`, `API_FAILED`, and `CONTRACT_FAILED` identify the boundary that failed. Doctor checks the stable state, draft, workspace, and catalog envelopes. It does not duplicate the frontend's complete nested Zod schema. A `drive` result is the authoritative frontend compatibility proof.

## Drive

Run the real browser path:

```bash
node .codex/skills/verify-loom-ui/scripts/verify.mjs drive --token-file /path/to/token
```

Override `--page-url` and `--api-url` together for another project or Explorer. The browser sends the bearer credential through the same host used by the deployed frontend. It probes the V2 Builder API envelope, loads the page through the frontend's real runtime decoder, detects authentication redirects and the no-V1-fallback error, and records browser warnings.

Read [features/README.md](features/README.md) before testing a different Builder feature.

## Evidence

Each run creates a permission-restricted timestamped directory containing:

- `report.json` with the API contract summary, final browser URL, console errors, failed requests, and relevant response statuses.
- `builder-response.json` with the credential-sanitized V2 response.
- `page.png` and `page.html` for browser runs.

The report never includes the bearer credential. Do not add raw HAR or trace files because they can retain authorization headers and cookies. A valid proof uses the API result and rendered page together. A 200 response from Next.js `builder.json` alone is not proof that Loom Builder state loaded.

## Cleanup

The helper terminates only the Chrome process it starts and removes its temporary profile. It never modifies the project, Explorer, databases, or Kubernetes resources. Evidence survives cleanup.

## Helpers

Run deterministic helper tests with:

```bash
node --test .codex/skills/verify-loom-ui/scripts/verify.test.mjs
```
