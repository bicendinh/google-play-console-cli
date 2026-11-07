# Release preparation and verification

These commands prepare and inspect releases without embedding an AI model.
Publishing still requires a reviewed plan and explicit `--confirm`. All examples
use a fictional package and assets; keep your real release workspace outside a
public source checkout.

## Generate a manifest locally

Use this directory convention:

```text
release/
  app-release-example.aab
  notes/
    en-US.txt
    de-DE.txt
  shots/
    en-US/
      01-home.png
      02-settings.png
```

```sh
playops releases init --dir release --track internal --out release.yaml
playops releases preflight --manifest release.yaml
playops releases snapshot --manifest release.yaml --out snapshot.json --confirm
playops releases plan --manifest release.yaml --snapshot snapshot.json \
  --out plan.json --flatten-alpha auto
playops releases apply --plan plan.json --dry-run
# Review the complete diff and obtain the release owner's authorization.
playops releases apply --plan plan.json --confirm
```

`init` is offline and never overwrites an existing output. Exactly one
`app-release-*.aab` is required. Package, version code/name and SDK values come
from the bundle, and the release name becomes `123 (1.2.3)`. Notes become the
required locales. Terminal CR/LF and a UTF-8 BOM are removed from note files;
other text is preserved. Screenshot names use unique one-based positions 1–8.
Listing locales must already exist, or be added with complete listing fields.

The default status is `draft`. Supplying `--rollout 10` chooses `inProgress`;
`--rollout 100` chooses `completed`. An explicit `--status` is also accepted.
Production percentages remain explicit; completed manifests must say
`rollout: 100`. Creating a manifest does not publish anything.

For partial replacement files such as `05-new.png` and `07-new.png`, supply
`--source-dir originals`, containing every original image under
`originals/<locale>/NN-name.png`. `init` generates `sourceFiles` plus `replace`.
Without a source directory, files must form a complete final sequence starting
at 01. Planning verifies originals against snapshot hashes before conversion;
preview URLs are never used to reconstruct missing original assets.

## PNG normalization and provenance

`flattenAlpha: auto` is the manifest default. Preflight and planning accept an
8-bit PNG that is fully opaque, including an RGBA file whose alpha values are all
255. A PNG requiring normalization is converted to 24-bit RGB for upload.
Images with transparent pixels fail; no background color is invented. Icons
retain their separate PNG/alpha rules.

`releases plan`, `releases preflight` and `listings images plan` accept
`--flatten-alpha auto|never|error`. `never` and `error` are strict aliases: no
conversion, and the original must already be a compliant RGB PNG or JPEG.
The policy is stored in the plan; sync/apply cannot silently override it.

Original files are never overwritten. Plan image files contain the upload
`sha256`/`size` and, when converted, `sourceSha256`, `sourceSize` and
`transform: opaque-png-rgb-v1`. Apply stages verified original bytes and
reproduces the conversion before opening an edit. A changed source or different
converted hash stops the operation. Original and converted hashes serve
different purposes and are never substituted for each other.

## Native AAB checks and Java tools

The CLI reads AAPT2 protobuf XML directly from
`base/manifest/AndroidManifest.xml`, without Java. Package/version code must match
the manifest. Optional `versionName`, `minSdk` and `targetSdk` fields add exact
expectations; observed values appear in the `bundleMetadata` check. Unresolved
version-name resource references are reported as unavailable; `init` requires a
resolvable version name rather than guessing one.

PKCS#7 blocks in `META-INF` supply actual signer certificate fingerprints.
Extracting a certificate is **not** signature verification. Certificate chains
are not treated as additional signers; ambiguous signer blocks fail. Configure
`uploadCertificateSha256` from a trusted upload-certificate source to check
identity. Without an expected fingerprint that comparison remains `not_run`.

`bundletool validate` is an optional additional structural check. `jarsigner`
performs full JAR signature/integrity verification. Missing Java/tools leave
those checks `not_run`, while native metadata and signer inspection still run.
`preflight.required: true` fails if any check cannot run and requires a snapshot
for complete preflight coverage. The legacy `preflight.keytool` field remains
accepted for compatibility; fingerprint extraction no longer invokes keytool.

Independent note and asset errors are collected in `error.details.checks` with
`field`, `locale`, `file` and one-based `index` where applicable. Semantic
manifest errors use `error.details.issues`. `meta.verificationComplete: false`
means some checks did not run, even when optional preflight exits successfully.
Local success is never called server validation or a policy-compliance verdict.

## Copy a set into a separate locale

```yaml
images:
  - locale: en-US
    type: phoneScreenshots
    mode: replace
    files: [assets/01.png, assets/02.png]
  - locale: de-DE
    type: phoneScreenshots
    mode: replace
    copyFrom: en-US
```

`copyFrom` copies the source group's final ordered local file set, including its
positional replacements and conversions. Both locales get independent uploads.
The source group must declare a replacement set of the same image type. Missing
references, cycles and combinations with local files or inheritance fail.
This does not infer or configure Console default-locale inheritance.

## Lifecycle and verification

```sh
playops releases status --track internal --version-code 123
playops releases verify --plan plan.json --no-edit
playops releases verify --plan plan.json --confirm
playops listings images list --locale en-US --type phoneScreenshots --confirm
```

Lifecycle output preserves Google's raw state. `reviewState` describes the
matched release; `globalReviewState` stays `unknown` because pending listing
changes can have a different review lifecycle. `PUBLISHED` means available to
users on that track. Commit acceptance alone is not publication proof.

Full verify compares the selected release/notes, changed listings and
ordered image hashes with the plan. It does not need the original local assets,
and never uploads, validates or commits. It returns `match`, `drift` or `unknown`.
Unrelated historical releases are not compared. Missing versions or hashes,
unverifiable symbol uploads and inheritance behavior are explicitly inconclusive.
`match` describes API state, not review approval or public Store visibility.
Successful inspections return exit 0 for every result status; API failures keep
their normal error/exit code instead of becoming missing data.

`--no-edit` queries lifecycle only. It returns `pending` when the API explicitly
reports in-review or approved-but-not-published, otherwise `unknown`; it cannot
prove agreement of the full plan. It never opens an edit.

After `COMMIT_OUTCOME_UNKNOWN`, start with:

```sh
playops releases verify --plan plan.json --no-edit --commit-outcome-unknown
# Optional inspection of the original edit, if Google still exposes it:
playops releases verify --plan plan.json --edit-id ORIGINAL_EDIT_ID \
  --commit-outcome-unknown
```

The uncertainty flag rejects creation of a new edit even with `--confirm`.
Never replay apply automatically. A new temporary edit can invalidate an old
edit owned by the same identity. Full verification and image listing require
`--confirm` for a temporary edit or an existing `--edit-id`; caller-owned edits
are never committed or deleted by these inspection commands.

Image listing returns one-based positions, SHA-256 and preview URLs.
`inheritance: unknown` and `usesDefaultImages: null` prevent empty API responses
being mistaken for confirmed fallback behavior.

`rollout get --no-edit --track internal` omits the unavailable fraction.
`releases snapshot --no-edit` saves a deliberately incomplete lifecycle snapshot;
it lists missing fields and cannot be used as a planning baseline.

Sources: [AAPT2 schema](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/tools/aapt2/Resources.proto),
[preview asset requirements](https://support.google.com/googleplay/android-developer/answer/9866151?hl=en),
[release lifecycle](https://developers.google.com/android-publisher/api-ref/rest/v3/applications.tracks.releases),
[image lists](https://developers.google.com/android-publisher/api-ref/rest/v3/edits.images/list),
[edit ownership](https://developers.google.com/android-publisher/edits).

## Complete-set replacement safeguards

`files` is the entire final image set, never a list of positional replacements.
Names such as `05-example.png` and `07-example.png` do not select slots 5 and 7;
they would become upload positions 1 and 2. Preflight emits `imageIntent` checks
with `status: warning` when numeric filename prefixes disagree with upload order.
Plans retain these diagnostics in `warnings`. Warnings do not invalidate an
otherwise intentional complete-set replacement.

When a final set is smaller than the snapshot set, planning fails with
`IMAGE_COUNT_DECREASE` (exit 2), including locale/type and before/after counts.
For an intentional reduction, explicitly add `allowImageCountDecrease: true` to
that image group, then generate and review a new plan. The plan records that
consent and warns about the reduction; apply/sync recheck it before mutation.
Each `copyFrom` destination needs its own consent if its count decreases.
For partial edits, use `sourceFiles` plus `replace` instead. A generic `--confirm`
does not bypass this requirement, and older reduction plans must be regenerated.

Failed metadata checks include `details.observed` and a `details.mismatches`
array of `{field, expected, actual}` values, with the same values in the message.
Note issues contain `actualLength` (Unicode characters) and `maxLength`, and
empty text, excessive length and duplicate locales have specific diagnostics.
