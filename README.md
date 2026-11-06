# playops

A small, scriptable Google Play CLI for AI agents and developers. Upload signed
Android App Bundles as drafts, publish or promote releases, control staged
rollouts, and read installs, ratings, crash rates and ANR rates.

Version 0.2 adds [atomic releases](docs/atomic-releases.md): one reviewed plan for
AAB + notes + listing text + ordered images + symbols, applied in one edit. It
also adds local preflight and [read-only product/subscription catalogs](docs/catalog.md).

Start with the [sample release manifest](docs/examples/release.yaml) and adapt its
package name, locales and asset paths for your app.

One binary. Official Google APIs. JSON by default. No embedded AI or hosted service.

## Build

From this checkout, with Go 1.27.3 or newer:

```sh
make build
./bin/playops version
./bin/playops schema
```

Put `bin/playops` on your PATH, or keep using its full path. Released binaries
do not require Go, Node, Java, Ruby or gcloud to run. Source dependencies are
pinned in `go.mod` and `go.sum`.

## Configure your own Google credentials

Follow [Google Cloud and Play Console setup](docs/setup.md), then:

```sh
playops auth login --profile personal --service-account /secure/play-key.json
playops config set app com.example.app --profile personal
playops config set reports-bucket gs://pubsite_prod_rev_123 --profile personal
playops doctor --profile personal
```

Every command accepts `--profile NAME`, `--app PACKAGE`, `--credentials FILE`,
`--config FILE`, `--timeout 5m`, `--request-timeout 0` and `--output json|table`. Set `PLAYOPS_PROFILE`
to avoid repeating a profile, or `PLAYOPS_CONFIG` to change the config path.
Profiles store credential **paths**, never copies of keys or access tokens.
The default config lives under the operating system's user config directory:
`~/Library/Application Support/playops/config.json` on macOS,
`$XDG_CONFIG_HOME/playops/config.json` (or `~/.config/playops/config.json`) on
Linux, and `%AppData%\playops\config.json` on Windows.

For ADC, including service account impersonation through workload identity:

```sh
playops auth login --adc
playops config set app com.example.app
playops doctor
```

## Release an existing app

```sh
# Preview local inputs; makes no Google API calls.
playops releases upload --file app-release.aab --track internal --dry-run

# Upload and save a draft; this does not publish the app.
playops releases upload --file app-release.aab --track internal \
  --name "1.2.0" --notes-file release-notes.json --confirm

# Use the versionCode returned by upload.
playops releases list --track internal
playops releases publish --track internal --version-code 123 --confirm

# Start a production rollout after testing the build.
playops releases promote --from internal --to production \
  --version-code 123 --rollout 10 --confirm

playops rollout get --track production --confirm
playops rollout set --track production --version-code 123 --rollout 25 --confirm
playops rollout halt --track production --version-code 123 --confirm
playops rollout resume --track production --version-code 123 --confirm
```

Release notes are a JSON array:

```json
[{"language":"en-US","text":"Bug fixes and improvements."}]
```

When replacing active releases, use the manifest workflow to review the exact
artifact diff and verify the remote baseline before applying. Set
`replaceExisting: true` in the manifest only when supersession is intended:

```sh
playops releases snapshot --manifest release.yaml --out snapshot.json --confirm
playops releases plan --manifest release.yaml --snapshot snapshot.json \
  --out release.plan.json --dry-run
playops releases apply --plan release.plan.json --confirm
```

Other drafts are retained. During a staged rollout, the existing completed
release remains as the fallback. Explicit replacement can remove older active
artifacts, including artifacts serving other devices; it is never inferred.
Legacy `--replace-existing` commands redirect to the reviewed manifest workflow.

Read [release semantics and recovery](docs/releases.md) before automating writes.
Initial app creation, the first upload, signing enrollment and required legal
declarations are performed in Play Console. Use track identifiers as configured
in your account; existing custom and form-factor tracks are accepted.

## Read metrics

```sh
playops apps list
playops stats installs --from 2026-10-01 --to 2026-10-31 --group-by country
playops stats ratings --group-by app-version
playops vitals crashes --version-code 123 --country US
playops vitals anrs --group-by app-version
```

Dates are inclusive. Both `--from` and `--to` are required when overriding the
default window of the previous seven complete days in `America/Los_Angeles`.
Day labels from exported reports are preserved. Vitals queries use Google's
daily aggregation timezone. See [data definitions and freshness](docs/metrics.md).

## AI and CI

- Run `playops schema` to discover commands, flags, defaults and exit codes.
- Read [the agent workflow guide](docs/agents.md) for release and recovery rules.
- Use the [GitHub Actions example](docs/examples/github-actions.yml) in an app
  repository, adapting the artifact and pinned CLI source reference.
- `--output table` prints a flattened table; metadata is printed to stderr.
- Success and error JSON use the same versioned [output contract](docs/output.md).

## Development

```sh
make check
make test
make build
make release VERSION=v0.2.0
```

The tests use local fake Google HTTP APIs and synthetic bundles; no credentials
or Google account are required. [Optional live checks](docs/testing.md) are
explicitly opt-in and only upload drafts on internal testing.

The release workflow tests before building macOS, Linux and Windows binaries
for arm64 and amd64, creates archives with SHA-256 checksums, and publishes a
GitHub Release when a maintainer pushes a `v*` tag. Nothing is uploaded to
Google Play by this repository's CI or release workflow.

Reviews, catalog mutations, browser OAuth, MCP, automatic rollout monitoring and
app build/signing remain out of scope. Deep local preflight optionally requires
Java/bundletool/jarsigner/keytool; missing checks are reported, not silently passed.

MIT licensed. Independent project; not affiliated with Google.
