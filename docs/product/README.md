# Current dataframe work

Use [Build dataframes from the schema](ML_DATAFRAMER_DELIVERY_PLAN.md). It is the only active product execution plan.

- [Acceptance protocol](ml-dataframer/ACCEPTANCE.md) defines the live journeys, literal checks, and performance targets.
- [Execution ledger](ml-dataframer/execution.json) records S01-S05 status. All new packages are planned, not accepted.
- [Frontend delivery map](ML_DATAFRAMER_DELIVERY_PLAN.md#deliver-the-frontend-with-each-backend-capability) pairs UI01-UI05 with the same S packages, tasks, and live journeys. Backend-only completion does not close a package.
- [Superseded plans and evidence](history/20260919-superseded/README.md) preserve earlier decisions and completed work. They are not execution instructions.

The implementation base is `arch/integration`. The currently active worktree is `/private/tmp/loom-arch-integration`; the Desktop checkout may be on an older branch. Confirm the branch and working-tree state before editing or launching the app.
