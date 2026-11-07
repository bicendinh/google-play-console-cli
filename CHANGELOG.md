# Changelog

## v0.3.0

- Read AAB metadata and PKCS#7 signer fingerprints with Go, independently of Java;
  separate certificate extraction, expected identity and signature verification.
- Normalize fully opaque RGBA PNGs with reviewed source/output hashes and verified
  staging; reject actual transparency and support explicit strict PNG policies.
- Generate release manifests from conventional directories and complete or
  positional screenshot sets; support independent locale uploads with copyFrom.
- Add remote plan verification, ordered image inspection, scoped lifecycle review
  state and lifecycle-only rollout/incomplete snapshot modes without opening edits.
- Collect structured preflight input errors and preserve profile diagnostics on
  flag failures; keep production rollout percentages explicit.
- Add vitals --days and reports-bucket --verify with actionable doctor guidance.
- Add regression fixtures for the preparation/apply/verify workflow and uncertainty
  guards. No live Google Play mutations were performed for this implementation.

## v0.2.0

- Add snapshot/offline-plan/apply with file digests, remote drift detection,
  private staged inputs, exact artifact diffs and one validated commit.
- Plan transitions for existing drafts/staged releases, including completion to
  100%, without requiring an AAB re-upload; preserve their names, notes and artifacts.
- Support reviewed promotion from another track, including source drift checks
  and explicit target artifact replacement, without modifying the source track.
- Add listing patches, ordered image replacement/readback, feature graphics,
  explicit inheritance preservation and caller-owned edit helpers.
- Add lifecycle status filtering and optional bundletool/JAR/upload-certificate
  preflight, with explicit failed/not-run checks.
- Add resumable AAB/symbol uploads and separate command/request deadlines.
- Add explicit review behavior, sanitized API validation errors and optional or
  required native symbols/ProGuard mapping upload.
- Add read-only subscriptions/base plans/offers and modern one-time products/
  purchase options/offers, with explicit legacy catalog access.
- Require the manifest plan workflow for legacy replacement requests to avoid
  removing active artifacts without a reviewed diff and remote baseline.
- Add a generic release-manifest example and workflow acceptance criteria.

Live Google Play integration has not been run as part of this local release.
