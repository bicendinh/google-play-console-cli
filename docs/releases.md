# Release semantics and recovery

## Transactions

Each write creates an edit, reads current tracks, applies a targeted change,
validates and commits. Upload always creates a persisted draft. No edit ID is
kept locally as a session for the next command.

Commits default to `changesInReviewBehavior=ERROR_IF_IN_REVIEW`. The CLI never
automatically cancels an existing review or toggles review flags to get around a
rejection, and never automatically retries a release workflow. Explicit review
options and the [atomic manifest workflow](atomic-releases.md) are available in v0.2.

After a definitive failure before commit, the CLI attempts to discard the edit.
If that cleanup fails, error details include the edit ID and cleanup status.
Google may have already invalidated the edit; otherwise it will expire.

Commit success means the edit was accepted, not that Google approved or published
the app. Use `releases list --track TRACK` to read `releaseLifecycleState` from
the edit-free release summary endpoint: draft, not sent for review, in review,
approved but not published, rejected or published. At most 20 non-obsolete
releases are returned by Google's endpoint; this is not a complete history.

`rollout get --track TRACK --confirm` shows the track's independent rollout
statuses (`draft`, `inProgress`, `halted`, `completed`) and `userFraction` (0–1).
It opens a temporary edit and discards it without committing. Consent is required
because opening an edit can invalidate an existing edit by the same identity.
Local edit operations for the same app are serialized with an OS file lock;
this cannot prevent another machine or a Play Console user changing the app.

## Publish, promote and replace

`publish` accepts a draft identified by `--version-code`. `promote` copies the
entire source release containing that version code, including its version code
set, name, notes and update priority; it leaves the source track unchanged.
Country targeting is not carried between tracks. A target release already
containing that version code is a conflict, not a successful replay.

Production and form-factor production tracks require explicit `--rollout 1–100`.
Testing tracks default to 100 when publishing. A value below 100 sets
`inProgress`; 100 sets `completed` and omits `userFraction` from the request.
The CLI does not create missing tracks.

A staged release retains the previous completed release. Other existing drafts
are preserved. Replacing an active staged/ halted release, or completing a
release while another active release remains, requires explicit replacement.
In v0.2 use `replaceExisting: true` in a reviewed manifest plan. The plan lists
the conflicting active release entries removed from the target update; legacy
commands with `--replace-existing` are redirected to this workflow.
Inspect their artifacts first, particularly when using multiple APK/device
targeting. In v0.1, `halt` and `resume` support staged rollouts only; a completed
release cannot be halted through these commands.

## Unknown commit outcome

A timeout, dropped connection, malformed success response or server error during
commit can occur after Google has applied the edit. The CLI returns exit 4,
`COMMIT_OUTCOME_UNKNOWN`, `retryable: false`, and app/track/version/edit details.
It neither replays nor discards that edit.

1. Read `releases list --track TRACK` for the affected app.
2. Compare the returned version and lifecycle state with the intended action.
3. If rollout details are needed, inspect Play Console first. A new `rollout get`
   opens an edit and may invalidate an older edit; use it deliberately.
4. Resolve any ambiguity in Console before issuing another write. If a release
   has not appeared yet, that alone is not proof that commit failed.

For `CONFLICT`, fetch fresh state and reconsider the operation. Do not assume
that repeating the same command is safe. A used version code cannot be uploaded
again; build a new version only after establishing what happened.

`--dry-run` requires no credentials, opens no edit, and uploads nothing. It checks
local inputs and AAB structure/signature-file presence only; it does not verify
cryptographic signing, package identity, version availability, permissions or
server policy. The separate optional preflight can inspect local metadata and
signatures; Google remains authoritative for acceptance and review.

Sources: [edits](https://developers.google.com/android-publisher/edits),
[tracks](https://developers.google.com/android-publisher/tracks),
[commit behavior](https://developers.google.com/android-publisher/api-ref/rest/v3/edits/commit),
[release summaries](https://developers.google.com/android-publisher/api-ref/rest/v3/applications.tracks.releases).
