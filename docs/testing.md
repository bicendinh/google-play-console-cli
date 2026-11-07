# Validation

`make test` runs HTTP fixture tests against the official Google SDK adapters.
They cover transaction sequencing, draft persistence, preservation of unrelated
drafts/fallbacks, explicit supersession, promotion, staged rollout transitions,
used version codes, invalidated edits, review conflicts, ambiguous commit
responses, CSV encodings/quoting/nulls, cross-month exports, API pagination,
freshness boundaries, JSON/exit contracts, profiles and offline dry runs.

`make check` checks formatting, runs vet and verifies downloaded dependency
checksums. CI additionally builds and runs race tests on macOS, Linux and Windows.
`make release VERSION=v0.3.0` cross-builds six archives and a `SHA256SUMS` file.

v0.2 fixtures additionally exercise eight locales in one edit/commit, positional
image replacement and unchanged content, last-locale/no-op/order failures,
manifest and remote drift, review options and ambiguous commit handling, scoped
caller-owned image helpers, local tool/certificate preflight, optional/required
symbols, read-only catalog pagination/regional prices and resumable chunk retry.
No live Play account is accessed by these tests. Hash/order/inheritance behavior
still needs live verification on a dedicated app before production rollout.

## Optional live checks

No live Google calls are required by the default test suite. A separate
integration test is opt-in through **all** of these environment variables:

```sh
export PLAYOPS_LIVE_TEST=1
export PLAYOPS_LIVE_APP=com.example.dedicatedtestapp
export PLAYOPS_LIVE_AAB=/absolute/path/to/a-new-signed-bundle.aab
export GOOGLE_APPLICATION_CREDENTIALS=/secure/test-service-account.json
go test -tags=integration ./integration -v
```

Use a dedicated existing test app and a fresh version code. This test performs
a real upload, creates a committed draft in the internal track, then reads
release summaries. It never publishes or promotes the draft. The draft is
intentionally left for inspection in Console; each subsequent upload needs a
new version code. Do not enable this test in CI by default.

To compare metrics manually, run `stats installs`, `stats ratings`, `vitals
crashes` and `vitals anrs` for a fixed historical window and match the source
dates, dimensions and values to the exported CSVs/Console. Account for export
latency and freshness; missing rows must not be treated as zero.

## Release preparation regression coverage

Native AAB fixtures exercise protobuf XML metadata, PKCS#7 signer selection,
invalid/duplicate manifests and package/version mismatches with no Java tools.
Forced color-type-6 PNG fixtures retain alpha=255 instead of letting Go's encoder
optimize them into RGB; tests cover source/output hash provenance, immutable
staging, transparent-pixel rejection, strict policies, copyFrom expansion and
cycles, and the complete conversion/copy/apply/verify path.

CLI fixtures check offline init, partial-set guards, aggregated locale/file/index
errors, profile context on flag errors, scoped lifecycle states, incomplete
snapshots and lifecycle-only commands that never create edits. Verification
covers match/drift/unknown and runs after original assets have been removed,
without uploads, validation or commits. Additional checks cover ordered image
URLs with unknown inheritance, reports-bucket verification before saving and
vitals --days/date conflicts with version filters.

Review-v3 regressions cover filename warnings, blocked image-count reduction,
explicit per-group consent (including copyFrom destination isolation), one note
issue for 501 Unicode characters and acceptance at 500, and expected/actual
values for all configured AAB metadata fields.

Original-image regressions cover compressed previews falling back to exact-byte
original-size downloads, preserved URL queries and hash rejection across all
candidates. Baseline RGBA originals keep their hashes, identical replacements
produce no image mutations, and one source can be staged both unchanged and
normalized for separate locales without mixing upload payloads.
