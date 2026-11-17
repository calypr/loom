---
name: verify-loom-ui
description: Check authenticated Builder loading on an external Calypr deployment through real Chrome, preserving sanitized API and browser evidence.
---

# Verify authenticated Calypr Builder loading

Use the existing external Calypr deployment at `caliper-training.ohsu.edu`.
That deployment is not managed in this repository. Do not start, redeploy, or
modify it as part of this read-only check. The helper launches its own temporary
Chrome profile and uses the deployed frontend and reverse proxy.

## Credentials and prerequisites

Provide a short-lived access token through `LOOM_E2E_TOKEN` or a mode `0600`
file. `LOOM_E2E_TOKEN_FILE` supplies the file default; `--token-file` overrides
it. Never put a token in a prompt, command output, or committed artifact.
Node and an installed Chrome/Chromium executable are required.

| Option | Environment/default |
|---|---|
| `--chrome PATH` | `LOOM_E2E_CHROME`, otherwise the macOS Google Chrome path. Pass `/usr/bin/chromium` on Linux when appropriate. |
| `--artifacts PATH` | `LOOM_E2E_ARTIFACTS`, otherwise `.artifacts/loom-ui`. |
| `--settle-ms N` | Browser settle interval, default 3000; allowed range 0–30000. |
| `--page-url URL` | External Builder page; defaults to the HTAN_INT/BForePC project and Explorer `test`. |
| `--api-url URL` | Matching Builder V2 API; override together with the page URL. |

The default API project segment is `HTAN_INT%252FBForePC`. The frontend's
`canonicalProject` and `encodedProject` functions in `ui/packages/loom-ui/src/api.ts`
canonicalize the project and encode its slash twice for proxy routing. Commit
`646988017` preserves the supplied ingest storage identity; it does not change
this frontend route encoding. Verify the API and page refer to the same project
and Explorer when overriding either URL.

## Doctor

```sh
node .codex/skills/verify-loom-ui/scripts/verify.mjs doctor --token-file /path/to/token
```

Require `DOCTOR_PASSED` before drive. `AUTH_REQUIRED`, `FORBIDDEN`, `API_FAILED`,
and `CONTRACT_FAILED` identify API boundary failures. Doctor checks the stable
state, draft, workspace, and catalog envelopes; it does not duplicate the full
frontend decoder or prove rendered compatibility.

## Drive

```sh
node .codex/skills/verify-loom-ui/scripts/verify.mjs drive --token-file /path/to/token
```

The browser sends the bearer credential through the deployed frontend's host,
loads Builder, and records authentication redirects, document errors, failed
requests, console errors, and the rendered load-failure UI.

`Couldn’t load this dataset.` must appear in a visible `role="alert"` with an
enabled `Try again` button inside that same alert to form a complete failure UI.
A load failure always produces `UI_FAILED`, including text without the alert,
an alert without retry, a visible retry outside the failure alert, and disabled
retry controls.
`browser.loadFailure.complete` distinguishes complete error recovery controls
from partial error UI. Drive does not click retry or mutate the Explorer.

Require `PASSED` for a clean browser-load result. `UI_WARNINGS` is recorded
separately and exits zero; inspect its failed requests and console errors rather
than reporting it as a clean pass. A Next.js `builder.json` HTTP 200 alone does
not establish that Loom Builder state loaded. See [coverage](features/README.md).

## Evidence and cleanup

Each run writes a permission-restricted timestamped directory with `report.json`,
credential-sanitized `builder-response.json`, and browser `page.png`/`page.html`.
Reports retain target and snapshot identity without bearer credentials. Keep
artifacts local. Do not add raw HAR files or traces containing headers/cookies.

The helper terminates only its own Chrome process and removes its temporary
profile. Evidence survives. It does not change projects, Explorers, databases,
or external infrastructure. Record unavailable authenticated browser acceptance
as unverified; deterministic helper tests cannot substitute for it.

```sh
node --check .codex/skills/verify-loom-ui/scripts/verify.mjs
node --test .codex/skills/verify-loom-ui/scripts/verify.test.mjs
```
