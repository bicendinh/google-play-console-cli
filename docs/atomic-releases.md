# Atomic releases (v0.2)

Use a reviewed manifest to upload an AAB, release notes, listing text, screenshots,
feature graphics and optional debug symbols in **one edit**. The transaction is
validated and committed once, only after every required step and readback succeeds.
Google's acceptance of a commit is not review approval or confirmed publication.

## Capture, plan, apply

Copy and adapt [the sample release manifest](examples/release.yaml).
Use a new signed bundle/version code and your original full-resolution assets.
Manifest paths are relative to the manifest file, not your current directory.

```sh
playops releases snapshot --manifest release.yaml --out snapshot.json --confirm
playops releases plan --manifest release.yaml --snapshot snapshot.json \
  --out release.plan.json --dry-run
playops releases preflight --manifest release.yaml --snapshot snapshot.json
playops releases apply --plan release.plan.json --dry-run

# After reviewing the diff and intended release/review status:
playops releases apply --plan release.plan.json --confirm --timeout 30m
playops releases status --track internal --version-code 123
```

Output files must be new paths; they are created with restrictive permissions.
Repeat capture/planning with new filenames if a plan needs to change. Output is
JSON by default, including the full diff and `removedVersionCodes`.

Select the manifest profile with `--profile NAME` or `PLAYOPS_PROFILE`. An explicit
`--app` must match the manifest; configuration cannot silently redirect a plan.
Profiles retain credential paths only; neither snapshots nor plans contain keys.

Snapshot deliberately opens and discards a temporary edit. This can invalidate
an existing edit by the same identity, so it requires consent. Local locks prevent
overlapping playops edit operations for the same app and config directory; they
cannot coordinate other computers, other config directories or Play Console.

Planning and preflight make **no Google API calls**. They use saved snapshots,
local files and optional local Android tools. Apply checks file hashes and parses
the original manifest again, stages verified private copies for upload, opens a
new edit, and compares the relevant track/listing/image state with the snapshot
before the first content mutation. Changed state is a conflict, not an automatic
replan or retry. Plan digests detect accidental modifications, not authenticity:
review plans from trusted sources before authorizing them.
Plans bind absolute input paths in the current workspace; regenerate a plan when
moving the release to another workspace instead of editing paths in reviewed JSON.

## Manifest behavior

- `app`, `profile`, `track`: explicit target identity and track. Default profile
  and status are `default` (or the selected profile) and `draft` respectively.
- `fromTrack`: optional source for promotion of an existing `versionCode` without
  re-uploading its bundle. The source must differ from the target and cannot be
  combined with `bundle`. Snapshot and apply verify both tracks; the complete
  release artifact set/name/notes are copied and the source track stays unchanged.
- `bundle`, `versionCode`, `name`: a signed AAB, expected version code and explicit
  name. The returned upload version code must match. A name is required for
  deterministic readback instead of relying on a generated name.
- `status`: `draft`, `inProgress`, or `completed`. Non-draft releases require an
  explicit `rollout` from 1–100. `inProgress` requires less than 100; `completed`
  requires 100; a draft must omit rollout. Keep a draft when deployment is not
  yet intended. An existing draft is published using `releases publish`.
- `notes`: unique `{language, text}` entries, 1–500 characters each.
- `requiredLocales`: configurable coverage requirement; all need listings and,
  when uploading a bundle, notes. The sample uses two; adapt the list to your app.
- `requiredImageTypes`: image coverage checked for each required locale. An
  explicit confirmed inheritance group can satisfy that coverage; it is never
  inferred from empty API results.
- `listings`: per-locale patches, inline or in a JSON/YAML `file` (not both).
  Fields are `title`, `shortDescription`, `fullDescription`, `video`. Unspecified
  fields are preserved. A new locale needs complete title/short/full text.
- `images`: ordered groups described below. Types supported: phone/tablet/TV/Wear
  screenshots, `featureGraphic`, `icon`, `tvBanner`.
- `symbols`: `{type: nativeCode|proguard, file, optional}` tied to `versionCode`.
  Native symbols require a nonempty ZIP; mapping files must be nonempty. Local
  validation does not establish that symbols match the compiled binary. Required
  failures abort; optional Google 400 validation rejections become explicit
  warnings. Permission, transport and invalid-edit errors always abort.
- `replaceExisting`: explicit permission to supersede conflicting active releases.
  The exact artifacts removed appear in the plan. Unrelated drafts and, for a
  staged release, the existing completed fallback are preserved.
- `review.inReview`: `error` (default) or explicit `cancel`. `cancel` maps to
  `CANCEL_IN_REVIEW_AND_SUBMIT`; its effect is visible in the manifest/plan.
- `review.changesNotSentForReview`: defaults false. This is Google's
  rejection-specific behavior, not a general save-draft or managed-publishing
  switch. No automatic fallback changes these options after an error.

Omit the bundle, release name, notes and rollout for metadata-only plans. Such
plans still capture the selected track as a baseline. To publish or complete an
**existing** draft/staged release without re-uploading its AAB, omit `bundle` and
set `versionCode`, `status: inProgress|completed` and `rollout`. Existing release
name, notes and artifact set are preserved unless explicitly replaced. Rollout
cannot decrease. For example, complete an existing 10% rollout with this manifest
and the same snapshot/plan/apply commands:

```yaml
app: com.example.app
profile: default
track: production
versionCode: 123
status: completed
rollout: 100
replaceExisting: true
```

The plan shows the older fallback artifacts being superseded. A no-change plan
can be inspected but apply refuses to open/commit an edit for it.
To promote that existing version from internal testing instead, add
`fromTrack: internal`; the version must exist in the source and not the target.

## Ordered images

Provide either `files` with the complete final ordered set, or `sourceFiles` with
the complete baseline set plus a one-based `replace` map:

```yaml
images:
  - locale: es-419
    type: phoneScreenshots
    mode: replace
    sourceFiles: [assets/01.png, assets/02.png, assets/03.png, assets/04.png,
                  assets/05.png, assets/06.png, assets/07.png, assets/08.png]
    replace:
      5: assets/new-05.png
      7: assets/new-07.png
```

Every baseline source file must match its snapshot SHA-256 at its exact position,
including the files being replaced. Missing originals stop planning. The API's
preview URL is never downloaded as a substitute for an original asset.

Within the uncommitted edit, playops deletes that image group and uploads the
complete new set sequentially. It checks each upload hash, then reads the group's
count, order and hashes. It verifies all affected resources again before commit.
Any mismatch aborts. Image IDs can change; the invariant is content and order.

PNG screenshots/graphics must be 24-bit RGB without alpha; JPEG is accepted.
Phone screenshots need 2–8 images, dimensions 320–3840 and aspect ratio at most
2:1; other screenshot groups support 1–8. Feature graphics require 1024×500, icons
512×512 PNG, and TV banners 1280×720. These local checks do not guarantee Google
will accept an asset. Order/hash behavior must still be verified on a dedicated
test app before production use.

`mode: preserve` performs no image mutation. `mode: inherit` also performs no
mutation and requires `inheritFrom` plus `inheritanceConfirmed: true`, an empty
local/effective image set and a nonempty source snapshot group. This is an explicit
attestation of verified Console behavior, not automatic inheritance detection.
It does not remove an existing explicit set or configure inheritance on Google.

## Preflight and optional Android tools

Local planning always validates manifest fields, asset formats, coverage and
hashes against the snapshot. If available, `bundletool` validates the bundle and
reads package/versionCode/targetSdk; `jarsigner` verifies JAR integrity and rejects
unsigned entries; `keytool` compares the upload signer to the configured
`uploadCertificateSha256`. Compare the **upload** certificate, not the certificate
of an APK re-signed by Google. Upload certificates can be legitimately reset.

```yaml
uploadCertificateSha256: "YOUR_64_HEX_CHARACTER_UPLOAD_CERTIFICATE_SHA256"
preflight:
  required: true
  bundletool: /tools/bundletool.jar
  java: java
  jarsigner: jarsigner
  keytool: keytool
```

Tool paths/commands are explicit executable arguments; no shell or downloads are
used. A `.jar` bundletool requires Java. Missing checks are reported as `not_run`,
never passed. `preflight.required: true` fails on any missing check, including a
missing expected signer fingerprint; standalone strict preflight also requires
`--snapshot` for coverage verification. A configured fingerprint always requires
successful comparison. A performed check that fails blocks planning even when
tools are optional. Java/bundletool are not required for ordinary CLI commands.

## Advanced caller-owned edits

Most users should use `releases apply`. These helpers accept an edit created by
another orchestrator and **never validate, commit or delete it**:

```sh
playops listings get --locale pt-BR --edit-id EDIT
playops listings update --locale pt-BR --file listing.json --edit-id EDIT --confirm
playops listings images plan --manifest images.yaml --snapshot snapshot.json \
  --out images.plan.json --dry-run
playops listings images sync --plan images.plan.json --edit-id EDIT --confirm
playops releases symbols upload --version-code 123 --type proguard \
  --file mapping.txt --edit-id EDIT --confirm
```

An image-only plan may alternatively use `--locale`, `--type`, repeated
`--replace POSITION=FILE` and `--source-files` in complete snapshot order. The
`--manifest` form accepts an image-only manifest, not a full bundle release.

The caller must stop and abandon its edit after a helper mutation fails; error
details state that responsibility. A successful helper is still uncommitted.
Opening a temporary edit from another command can invalidate the caller's edit.
`listings get --locale LOCALE --confirm` without `--edit-id` explicitly opens and
discards its own temporary edit. These helpers do not create a persistent CLI
session and there is no CLI command that blindly commits an arbitrary edit.

## Upload deadlines and recovery

`--timeout` bounds the whole command. `--request-timeout` bounds each HTTP request;
its default 0 uses the command context. The fixed two-minute HTTP cap is removed.
Large AAB/symbol payloads use the official SDK's resumable upload with 8 MiB
chunks; small payloads may use multipart. Chunk retries stay inside that upload
session and command. Upload sessions are not persisted for a later process.
Commit is never automatically retried.

An ambiguous commit returns `COMMIT_OUTCOME_UNKNOWN`, edit/app/track/version and
plan identifiers, and does not discard the edit. Inspect `releases status` and
Console before any further write. A version missing from the 20-item lifecycle
listing is not proof of failure. Lifecycle status still has no rollout fraction;
`rollout get --confirm` remains necessary for full track details.

Legacy release commands accept explicit `--in-review` and
`--changes-not-sent-for-review`. Legacy `--replace-existing` is redirected to the
snapshot/plan/apply workflow, so artifacts cannot be dropped without a reviewed
diff and drift check.
