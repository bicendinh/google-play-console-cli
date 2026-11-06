package googleapi

import (
	"context"
	"io"

	rep "google.golang.org/api/playdeveloperreporting/v1beta1"
)

func (c *Client) AppsPage(ctx context.Context, token string) ([]*rep.GooglePlayDeveloperReportingV1beta1App, string, error) {
	r, err := c.Reporting.Apps.Search().PageSize(1000).PageToken(token).Context(ctx).Do()
	if err != nil {
		return nil, "", err
	}
	return r.Apps, r.NextPageToken, nil
}
func (c *Client) Freshness(ctx context.Context, app, kind string) (*rep.GooglePlayDeveloperReportingV1beta1FreshnessInfo, error) {
	if kind == "crashes" {
		r, err := c.Reporting.Vitals.Crashrate.Get("apps/" + app + "/crashRateMetricSet").Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return r.FreshnessInfo, nil
	}
	r, err := c.Reporting.Vitals.Anrrate.Get("apps/" + app + "/anrRateMetricSet").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return r.FreshnessInfo, nil
}
func (c *Client) VitalsPage(ctx context.Context, app, kind string, timeline *rep.GooglePlayDeveloperReportingV1beta1TimelineSpec, dimensions []string, filter, token string) ([]*rep.GooglePlayDeveloperReportingV1beta1MetricsRow, string, error) {
	if kind == "crashes" {
		r, err := c.Reporting.Vitals.Crashrate.Query("apps/"+app+"/crashRateMetricSet", &rep.GooglePlayDeveloperReportingV1beta1QueryCrashRateMetricSetRequest{TimelineSpec: timeline, Dimensions: dimensions, Filter: filter, PageToken: token, PageSize: 1000, Metrics: []string{"crashRate", "userPerceivedCrashRate", "distinctUsers"}}).Context(ctx).Do()
		if err != nil {
			return nil, "", err
		}
		return r.Rows, r.NextPageToken, nil
	}
	r, err := c.Reporting.Vitals.Anrrate.Query("apps/"+app+"/anrRateMetricSet", &rep.GooglePlayDeveloperReportingV1beta1QueryAnrRateMetricSetRequest{TimelineSpec: timeline, Dimensions: dimensions, Filter: filter, PageToken: token, PageSize: 1000, Metrics: []string{"anrRate", "userPerceivedAnrRate", "distinctUsers"}}).Context(ctx).Do()
	if err != nil {
		return nil, "", err
	}
	return r.Rows, r.NextPageToken, nil
}
func (c *Client) Report(ctx context.Context, bucket, object string) (io.ReadCloser, string, error) {
	info, err := c.Storage.Objects.Get(bucket, object).Context(ctx).Do()
	if err != nil {
		return nil, "", err
	}
	// Pin the generation so metadata and downloaded CSV describe the same export.
	response, err := c.Storage.Objects.Get(bucket, object).Generation(info.Generation).Context(ctx).Download()
	if err != nil {
		return nil, "", err
	}
	return response.Body, info.Updated, nil
}
func (c *Client) CheckReports(ctx context.Context, bucket, app string) error {
	_, err := c.Storage.Objects.List(bucket).Prefix("stats/installs/installs_" + app + "_").MaxResults(1).Context(ctx).Do()
	return err
}
