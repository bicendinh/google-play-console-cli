# Metrics and freshness

`stats installs` and `stats ratings` read monthly files from your Play reports
bucket. `vitals crashes` and `vitals anrs` query the separate Reporting API.
Neither source provides a real-time, complete mirror of the Console.

## Date windows

`--from YYYY-MM-DD --to YYYY-MM-DD` includes both endpoint dates. Without either
flag, the window is the previous seven complete calendar days in
`America/Los_Angeles`. A maximum three-year window is accepted. The API's exclusive
end date is calculated internally. Report day labels are preserved.

Vitals also accepts `--days N` for 1–1095 complete source-calendar days, including
across daylight-saving transitions. It cannot be combined with `--from` or `--to`.
For example: `playops vitals crashes --version-code 123 --days 7`. Freshness can
still shorten the available data within that requested window.

Vitals freshness is fetched first. The query is capped at the latest available
daily endpoint while metadata preserves your requested window. It does not
silently shift the window backward to fill seven available days.

## Exported statistics

Objects use these documented patterns:

```text
stats/installs/installs_PACKAGE_YYYYMM_country.csv
stats/installs/installs_PACKAGE_YYYYMM_app_version.csv
stats/ratings/ratings_PACKAGE_YYYYMM_country.csv
stats/ratings/ratings_PACKAGE_YYYYMM_app_version.csv
```

The reader supports BOM-marked little/big-endian UTF-16 and UTF-8; it also handles
unmarked little-endian UTF-16 exports. CSV quoting, embedded commas and newlines
are preserved. Each download is pinned to the generation obtained with its
metadata, so `updatedAt` and the CSV refer to the same object version.

Rows have `date`, `dimension`, `metrics` and optional `extra` fields. Install
metrics include `dailyUserInstalls`, `dailyUserUninstalls`, `dailyDeviceInstalls`,
`dailyDeviceUninstalls`, `dailyDeviceUpgrades`, `currentUserInstalls`,
`currentDeviceInstalls`, `totalUserInstalls`, and `installsOnActiveDevices` when
present in the export. Rating metrics are `dailyAverageRating` and
`totalAverageRating`. Unknown source columns are retained under `extra`.

Blank metric cells are JSON null; numeric zero remains zero. Missing columns
are absent. An unexpected format returns `REPORT_FORMAT` rather than fabricated
values. The CLI never sums stock/cumulative values over dates, averages rating
averages, or combines dimensions into an app-wide rating. These exports do not
provide enough information for that rating calculation.

## Android vitals

Both queries return Google's daily rows and decimal strings unchanged, including
dimension values and confidence intervals when present. Crash metrics are
`crashRate`, `userPerceivedCrashRate`, `distinctUsers`; ANR metrics are `anrRate`,
`userPerceivedAnrRate`, `distinctUsers`. These are rates, not absolute crash or
ANR counts. The CLI does not rescale or aggregate them.

Use `--group-by country|app-version` to request a breakdown, or omit it for an
app-wide daily row. Filters accept `--country US` and `--version-code 123`.
Distinct user counts are not additive over days. API pagination is followed
automatically. No rows may reflect privacy thresholds or lack of observations;
it does not establish a zero crash rate.

## Metadata

- `period`: requested inclusive dates and source timezone.
- `availableThrough`: latest exported row date in the requested statistics
  window, or the Reporting API's latest available daily date for vitals.
- `objects`: downloaded CSV object paths and their modification timestamps.
- `missingObjects`: expected monthly files which returned 404.
- `freshnessInfo`: original Reporting API freshness response, when applicable.
- `dataStatus`: `available`, `partial`, `no_data`, or `not_available`.

An `available` status means the response has rows and is not known to be missing
a trailing period or monthly file; it is not a completeness guarantee for every
country, version or date. Missing rows are never filled with zeros. Incomplete
or absent data returns exit 0 with metadata. Permission failures return errors,
not empty results. Export publication can lag 3–7 days and old reports can be
updated later; no fixed delivery time is assumed.

Sources: [report exports](https://support.google.com/googleplay/android-developer/answer/6135870?hl=en),
[crash metrics](https://developers.google.com/play/developer/reporting/reference/rest/v1beta1/vitals.crashrate),
[ANR metrics](https://developers.google.com/play/developer/reporting/reference/rest/v1beta1/vitals.anrrate).
