# Google Cloud and Play Console setup

Use your own Google Cloud project, service account and Play developer account.
playops runs locally or in your CI and sends requests directly to Google. It
does not provide shared credentials, collect keys or proxy developer accounts.

## Service account

1. Select or create a project in Google Cloud Console.
2. Enable **Google Play Android Developer API** (`androidpublisher.googleapis.com`)
   and **Google Play Developer Reporting API** (`playdeveloperreporting.googleapis.com`).
   Enable the Cloud Storage API for report access if it is disabled.
3. Create a service account. For local key-based use, create a JSON key and keep
   it outside your source checkout with restrictive file permissions. If your
   organization disables key creation, use workload identity with impersonation.
4. In Play Console > Users and permissions, invite the service account email.
   Grant access to the apps you want to manage. Give read-only app information
   access for vitals and the release permission appropriate to your test or
   production track. Publishing rights are separate from read access.
5. In Play Console > Play apps reports, copy the Cloud Storage URI for your
   account. Configure the bucket name with `config set reports-bucket`. Do not
   derive it from the developer ID or a Cloud project name. Report access is
   granted through Play Console; the report bucket is Google managed.
6. Run `auth login --service-account /absolute/key.json`, `config set app PACKAGE`
   and `doctor`. No account-to-Cloud-project linking is required by current APIs.

`auth login` checks local credentials configuration and stores only the path.
`auth status` does not fetch an access token or prove network access. `doctor`
tests each data source independently and returns exit code 3 with all check
results in `error.details.checks` if any source fails.

The publishing doctor probe calls the read-only release listing endpoint, so it
cannot establish that the identity can perform all publishing mutations. Those
permissions are checked by Google when a real edit is attempted. Missing reports
do not prevent using release or vitals commands separately.

## ADC and CI

`auth login --adc` selects Application Default Credentials. Google's auth library
looks for `GOOGLE_APPLICATION_CREDENTIALS`, local ADC, then the runtime identity.
For GitHub Actions workload identity, configure a provider bound to your repo
and impersonate a service account invited into Play Console. The CLI also works
with an existing service account key exposed as `GOOGLE_APPLICATION_CREDENTIALS`.
Interactive user OAuth login is not implemented.

Requested scopes:

- `https://www.googleapis.com/auth/androidpublisher`
- `https://www.googleapis.com/auth/playdeveloperreporting`
- `https://www.googleapis.com/auth/devstorage.read_only`

Credentials precedence is `--credentials`, the selected profile's service account
path, then ADC. Profile selection is `--profile`, `PLAYOPS_PROFILE`, then `default`.
An explicit profile key takes precedence over `GOOGLE_APPLICATION_CREDENTIALS`;
select ADC to use that environment variable instead.

## Sources

- [Publisher setup](https://developers.google.com/android-publisher/getting_started)
- [Reporting setup](https://developers.google.com/play/developer/reporting/overview)
- [Report exports and access](https://support.google.com/googleplay/android-developer/answer/6135870?hl=en)
- [Application Default Credentials](https://docs.cloud.google.com/docs/authentication/application-default-credentials)
- [Google Play Developer API terms](https://developers.google.com/android-publisher/terms)
