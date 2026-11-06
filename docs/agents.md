# Using playops from an AI agent

playops is a deterministic CLI; you provide the reasoning. It does not call an
LLM, run a background monitor or decide when an app should be published.

## Discover and inspect

1. Run `playops version` and `playops schema`.
2. Select the intended `--profile` and app. Never infer a production app from a
   similar package name. Configuration can supply the default package.
3. Run `playops doctor --app PACKAGE`; inspect every capability result. A missing
   reports bucket need not prevent using a functioning release API separately.
4. Read releases with `playops releases list --track TRACK`. This is edit-free.
5. Inspect `meta.dataStatus`, `period` and freshness before interpreting metrics.

## Draft-first release

For combined AAB/listing/image updates, use [snapshot → plan → apply](atomic-releases.md)
instead of separate commits. Review the plan's resource diff, removed artifacts,
image order and review options. Planning is offline; snapshot opens a temporary
edit with consent; apply checks both local hashes and remote drift before writes.

For an authorized release task, obtain a signed AAB from the app's build process.
Never print credentials or place a service account key inside the repository.

```sh
playops releases upload --app com.example.app --track internal \
  --file app-release.aab --dry-run
playops releases upload --app com.example.app --track internal \
  --file app-release.aab --confirm
```

Record the returned `versionCode`. The uploaded release is a draft. Follow the
user's authorized release scope when deciding whether to publish it:

```sh
playops releases publish --app com.example.app --track internal \
  --version-code 123 --confirm
playops releases list --app com.example.app --track internal
```

Production promotion is a separate explicit action:

```sh
playops releases promote --app com.example.app --from internal --to production \
  --version-code 123 --rollout 10 --confirm
```

Do not add replacement permission automatically to get around a conflict. It means
superseding active releases and may remove device-specific artifacts. Use the
manifest's `replaceExisting` with a reviewed diff; legacy replacement flags are
redirected to that workflow. Inspect
the target in Console or with `rollout get --track TRACK --confirm` first.
That inspection command opens and discards an edit and can invalidate another
edit by the same identity. Avoid overlapping Console or remote CLI edits.

## Recover from errors

- `INVALID_ARGUMENT`: correct the command using `schema`; do not guess flags.
- `AUTH_CONFIGURATION` or `AUTH_PERMISSION`: follow setup/doctor diagnostics.
- `PREFLIGHT_FAILED`: inspect the checks; install/configure required local tools
  or correct the bundle, expected upload signer or manifest. `not_run` is not a pass.
- `CONFLICT`: fetch fresh state and reconsider the requested change.
- `COMMIT_OUTCOME_UNKNOWN`: stop writes, inspect release lifecycle/Console, and
  resolve whether Google accepted the commit. Never replay the workflow just
  because the process failed or the release is not visible yet.
- Transient read errors: bounded retry is reasonable when `retryable` is true.
- Missing metric rows: report them as unavailable, not as zero or healthy.

Google review approval, managed publishing and rollout serving are separate
states. A successful commit must never be described as confirmed publication.

Caller-owned `--edit-id` helpers never commit/delete the edit. On any failed
mutation, the owning orchestrator must abort rather than continue to commit.
Catalog commands only read configuration and cannot determine trial eligibility
for an individual user.

Reference: [release recovery](releases.md), [metrics](metrics.md),
[output and exit codes](output.md), [setup](setup.md).
