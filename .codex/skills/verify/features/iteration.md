# Verify development iteration

Use `make dev` for the Go/Vite watcher stack and `make dev-doctor` to validate
its owned Compose identity, source/build identity, fixture, API, and UI.
Use `make dev-rebuild` after dependency, toolchain, or development image changes.

There is no HMR/build-failure/recovery acceptance spec in `tests/e2e/`.
Doctor and a passing Builder flow do not establish those guarantees. When a
change requires HMR evidence, record the source edit, observed browser update,
build identity, and restored source explicitly. Freeze source again before
running final browser acceptance. Follow [verify](../SKILL.md) for cleanup and
performance diagnosis.
