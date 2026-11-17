# Load Builder on external Calypr

The helper uses the external Calypr deployment, not infrastructure managed in
this repository. Follow [the skill](../SKILL.md) for credentials, target URLs,
Chrome options, evidence, and cleanup.

Run Doctor, then drive. Require a valid `ExplorerBuilderState`, no authentication
redirect/prompt, no HTTP error document, and no visible load-failure marker.
On failure, inspect `browser.loadFailure`: the complete recovery UI contains
`Couldn’t load this dataset.` in a visible alert and an enabled `Try again`
button within it. Complete and partial error UI both fail the run. The helper
does not activate retry.

Source boundaries:

- `ui/packages/loom-ui/src/Builder.tsx`: `LoomExplorerBuilder` resolves identity and mounts the workspace.
- `ui/packages/loom-ui/src/api.ts`: `getBuilder` loads V2 state; `encodedProject` constructs the project route segment.
- `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx`: `shouldShowBuilderLoadError` gates the alert and retry action.

A successful Next.js `builder.json` response can coexist with a failed Loom
request. Use the API result and rendered-page diagnostics together. This check
does not establish persisted edits or complete nested-contract coverage.
