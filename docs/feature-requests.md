# Release workflow requirements

Design goals and acceptance criteria for a general-purpose Google Play CLI.
See [atomic releases](atomic-releases.md) and the [sample manifest](examples/release.yaml)
for the implemented interfaces. Locale lists, product identifiers, tracks and
asset counts are supplied by each application; no app-specific configuration is
built into the CLI.

## Atomic release transactions

- Capture relevant remote state in a temporary edit with explicit consent;
  explain that another edit by the same identity may be invalidated.
- Plan offline from a saved snapshot and local inputs. Record file hashes,
  resource diffs, review options and artifacts to be superseded.
- Verify inputs and remote state before content mutations. Apply the bundle,
  release notes, listing patches, images and required symbols in one edit.
- Read back affected resources, validate once, and commit once. A failure in any
  required step must prevent commit.
- Default to `ERROR_IF_IN_REVIEW`. Never change review behavior automatically or
  replay an uncertain commit; retain identifiers needed for recovery.
- Publish, promote or complete an existing release without re-uploading its AAB.
  Preserve its artifact set and metadata; check promotion source drift as well
  as target drift.

## Ordered listing assets

- Support complete image sets and one-based positional replacements.
- Positional replacement requires every original source file to match its
  snapshot hash. Preview URLs are not substitutes for original files.
- Preserve content and order of unchanged images. Verify upload hashes and the
  final count/order before commit; image IDs may change.
- Validate image types, dimensions and alpha requirements. Detect unsupported
  locale uploads that silently make no change.
- Never infer inheritance from empty API results. Preserving inheritance requires
  explicit confirmation and a verified source group.
- Patch only the listing fields supplied; preserve unspecified text/video fields
  and verify the final values by locale.

## Uploads, review and replacement

- Separate whole-command and per-request deadlines; avoid a fixed HTTP cap that
  defeats a longer command deadline.
- Retry resumable upload chunks within the same upload session, not the complete
  release workflow or commit.
- Require a reviewed artifact diff and unchanged baseline before superseding
  active releases. Keep unrelated drafts and staged-release fallbacks.
- Make review cancellation explicit in the plan. Do not treat
  `changesNotSentForReview` as a universal draft or managed-publishing switch.

## Status and local preflight

- Read release lifecycle without creating an edit. Keep detailed rollout reads
  separate because lifecycle summaries contain no rollout fraction.
- Do not infer commit failure from absence in a limited release history.
- Inspect bundle package/version/target SDK and signature integrity with optional
  local Android tools. Compare the upload signer with a trusted configured
  upload certificate, not an APK certificate re-signed by Google.
- Report missing checks as `not_run`; fail required checks that cannot run.
  Local checks cannot guarantee Google policy acceptance or review approval.
- Sanitize sensitive data and control characters in API errors while preserving
  actionable validation details and stable error/exit codes.

## Symbols and read-only catalogs

- Upload native debug symbols or ProGuard mapping for the intended version.
  Required failures abort the transaction; optional validation rejections become
  explicit warnings. Uploading does not generate missing symbols.
- Caller-owned edit helpers must not validate, commit or delete the edit. The
  owning orchestrator must abort after a failed mutation.
- Read subscriptions/base plans/offers and modern one-time products/purchase
  options/offers, with explicit legacy catalog access when needed.
- Follow pagination and preserve regional currencies, prices and eligibility
  configuration. Catalog data does not establish a customer's trial eligibility.

## Validation

Fake API tests cover one-edit transactions across multiple locales, unchanged
image positions, last-step failures, stale inputs/state, source-track drift,
uncertain commits, resumable chunk retry, local signer checks and catalog reads.
Image order, hashes and inheritance behavior still need verification on a
separate test app before production use.
