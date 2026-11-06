package metrics

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
	gapi "google.golang.org/api/googleapi"
	rep "google.golang.org/api/playdeveloperreporting/v1beta1"

	"playops/internal/fault"
	"playops/internal/model"
)

type ReportAPI interface {
	Report(context.Context, string, string) (io.ReadCloser, string, error)
}
type ReportingAPI interface {
	AppsPage(context.Context, string) ([]*rep.GooglePlayDeveloperReportingV1beta1App, string, error)
	Freshness(context.Context, string, string) (*rep.GooglePlayDeveloperReportingV1beta1FreshnessInfo, error)
	VitalsPage(context.Context, string, string, *rep.GooglePlayDeveloperReportingV1beta1TimelineSpec, []string, string, string) ([]*rep.GooglePlayDeveloperReportingV1beta1MetricsRow, string, error)
}
type Service struct {
	Reports   ReportAPI
	Reporting ReportingAPI
}
type Row struct {
	Date      string            `json:"date"`
	Dimension map[string]string `json:"dimension"`
	Metrics   map[string]any    `json:"metrics"`
	Extra     map[string]string `json:"extra,omitempty"`
}

func (s Service) Apps(ctx context.Context) (model.Result, error) {
	apps := []*rep.GooglePlayDeveloperReportingV1beta1App{}
	token := ""
	seen := map[string]bool{}
	for {
		rows, next, err := s.Reporting.AppsPage(ctx, token)
		if err != nil {
			return model.Result{}, err
		}
		apps = append(apps, rows...)
		if next == "" {
			break
		}
		if seen[next] {
			return model.Result{}, fault.New("PAGINATION_ERROR", "Google returned a repeated page token", 5)
		}
		seen[next] = true
		token = next
	}
	return model.Result{Data: apps, Meta: map[string]any{"source": "play-developer-reporting", "dataStatus": status(len(apps))}}, nil
}
func status(count int) string {
	if count == 0 {
		return "no_data"
	}
	return "available"
}

func (s Service) Stats(ctx context.Context, app, bucket, kind, dimension string, p model.Period) (model.Result, error) {
	if kind != "installs" && kind != "ratings" {
		return model.Result{}, fault.Input("Unknown statistics report")
	}
	if dimension != "country" && dimension != "app-version" {
		return model.Result{}, fault.Input("--group-by must be country or app-version")
	}
	suffix := strings.ReplaceAll(dimension, "-", "_")
	rows := []Row{}
	missing := []string{}
	objects := []map[string]string{}
	availableThrough := ""
	start := p.Start()
	month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	for month.Before(p.EndExclusive()) {
		object := fmt.Sprintf("stats/%s/%s_%s_%s_%s.csv", kind, kind, app, month.Format("200601"), suffix)
		r, updated, err := s.Reports.Report(ctx, bucket, object)
		if err != nil {
			var api *gapi.Error
			if errors.As(err, &api) && api.Code == 404 {
				missing = append(missing, object)
				month = month.AddDate(0, 1, 0)
				continue
			}
			return model.Result{}, err
		}
		parsed, parseErr := ParseCSV(r, app, kind, dimension, p)
		closeErr := r.Close()
		if parseErr != nil {
			return model.Result{}, parseErr
		}
		if closeErr != nil {
			return model.Result{}, closeErr
		}
		rows = append(rows, parsed...)
		objects = append(objects, map[string]string{"object": object, "updatedAt": updated})
		month = month.AddDate(0, 1, 0)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Date != rows[j].Date {
			return rows[i].Date < rows[j].Date
		}
		return rows[i].Dimension[dimension] < rows[j].Dimension[dimension]
	})
	for _, r := range rows {
		if r.Date > availableThrough {
			availableThrough = r.Date
		}
	}
	state := status(len(rows))
	if len(missing) > 0 && len(rows) == 0 {
		state = "not_available"
	} else if len(missing) > 0 || (availableThrough != "" && availableThrough < p.To) {
		state = "partial"
	}
	return model.Result{Data: rows, Meta: map[string]any{"source": "google-play-cloud-storage", "app": app, "report": kind, "bucket": bucket, "period": p, "groupBy": dimension, "dataStatus": state, "availableThrough": availableThrough, "objects": objects, "missingObjects": missing, "notice": "Monthly exports can lag 3–7 days. Missing values are null; rows are never summed or averaged."}}, nil
}

var installColumns = map[string]string{
	"Current Device Installs": "currentDeviceInstalls", "Installs on active devices": "installsOnActiveDevices",
	"Daily Device Installs": "dailyDeviceInstalls", "Daily Device Uninstalls": "dailyDeviceUninstalls", "Daily Device Upgrades": "dailyDeviceUpgrades",
	"Current User Installs": "currentUserInstalls", "Total User Installs": "totalUserInstalls", "Daily User Installs": "dailyUserInstalls", "Daily User Uninstalls": "dailyUserUninstalls",
}
var ratingColumns = map[string]string{"Daily Average Rating": "dailyAverageRating", "Total Average Rating": "totalAverageRating"}

func ParseCSV(input io.Reader, app, kind, dimension string, p model.Period) ([]Row, error) {
	buffer := bufio.NewReader(input)
	peek, _ := buffer.Peek(3)
	var reader io.Reader = buffer
	if len(peek) >= 2 && ((peek[0] == 0xff && peek[1] == 0xfe) || (peek[0] == 0xfe && peek[1] == 0xff)) {
		reader = transform.NewReader(buffer, unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder())
	} else if len(peek) >= 2 && peek[1] == 0 {
		reader = transform.NewReader(buffer, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder())
	} else if len(peek) >= 3 && peek[0] == 0xef && peek[1] == 0xbb && peek[2] == 0xbf {
		_, _ = buffer.Discard(3)
	}
	csvr := csv.NewReader(reader)
	header, err := csvr.Read()
	if err == io.EOF {
		return []Row{}, nil
	}
	if err != nil {
		return nil, fault.New("REPORT_FORMAT", "Cannot read report header", 2)
	}
	indexes := map[string]int{}
	for i, h := range header {
		if _, exists := indexes[h]; exists {
			return nil, fault.New("REPORT_FORMAT", "Duplicate CSV column", 2)
		}
		indexes[h] = i
	}
	dimHeader := "Country"
	if dimension == "app-version" {
		dimHeader = "App Version Code"
	}
	for _, required := range []string{"Date", "Package Name", dimHeader} {
		if _, ok := indexes[required]; !ok {
			return nil, fault.New("REPORT_FORMAT", "Missing CSV column: "+required, 2)
		}
	}
	columns := installColumns
	if kind == "ratings" {
		columns = ratingColumns
	}
	known := false
	for h := range columns {
		if _, ok := indexes[h]; ok {
			known = true
		}
	}
	if !known {
		return nil, fault.New("REPORT_FORMAT", "Report has no recognized metric columns", 2)
	}
	rows := []Row{}
	for {
		record, e := csvr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fault.New("REPORT_FORMAT", "Malformed CSV row", 2)
		}
		date := record[indexes["Date"]]
		if _, e = time.Parse(time.DateOnly, date); e != nil {
			return nil, fault.New("REPORT_FORMAT", "Invalid report date", 2)
		}
		if record[indexes["Package Name"]] != app {
			return nil, fault.New("REPORT_FORMAT", "Report contains a different package name", 2)
		}
		if date < p.From || date > p.To {
			continue
		}
		row := Row{Date: date, Dimension: map[string]string{dimension: record[indexes[dimHeader]]}, Metrics: map[string]any{}, Extra: map[string]string{}}
		for i, h := range header {
			if h == "Date" || h == "Package Name" || h == dimHeader {
				continue
			}
			if key, ok := columns[h]; ok {
				v := strings.TrimSpace(record[i])
				if v == "" {
					row.Metrics[key] = nil
					continue
				}
				var number json.Number
				if json.Unmarshal([]byte(v), &number) != nil {
					return nil, fault.New("REPORT_FORMAT", "Invalid number in "+h, 2)
				}
				if _, e = number.Float64(); e != nil {
					return nil, fault.New("REPORT_FORMAT", "Invalid number in "+h, 2)
				}
				if kind == "installs" {
					if _, e = number.Int64(); e != nil {
						return nil, fault.New("REPORT_FORMAT", "Expected integer in "+h, 2)
					}
				}
				row.Metrics[key] = number
			} else {
				row.Extra[h] = record[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func dateTime(t time.Time) *rep.GoogleTypeDateTime {
	return &rep.GoogleTypeDateTime{Year: int64(t.Year()), Month: int64(t.Month()), Day: int64(t.Day()), TimeZone: &rep.GoogleTypeTimeZone{Id: "America/Los_Angeles"}}
}
func (s Service) Vitals(ctx context.Context, app, kind string, p model.Period, version int64, country, group string) (model.Result, error) {
	if kind != "crashes" && kind != "anrs" {
		return model.Result{}, fault.Input("Unknown vitals metric set")
	}
	dimensions := []string{}
	switch group {
	case "":
	case "country":
		dimensions = append(dimensions, "countryCode")
	case "app-version":
		dimensions = append(dimensions, "versionCode")
	default:
		return model.Result{}, fault.Input("--group-by must be country or app-version")
	}
	filters := []string{}
	if version > 0 {
		filters = append(filters, fmt.Sprintf("versionCode = %d", version))
	}
	if country != "" {
		filters = append(filters, fmt.Sprintf("countryCode = %q", country))
	}
	fresh, err := s.Reporting.Freshness(ctx, app, kind)
	if err != nil {
		return model.Result{}, err
	}
	end := p.EndExclusive()
	availableThrough := ""
	dataEnd := end
	if fresh != nil {
		for _, f := range fresh.Freshnesses {
			if f.AggregationPeriod == "DAILY" && f.LatestEndTime != nil {
				v := f.LatestEndTime
				t := time.Date(int(v.Year), time.Month(v.Month), int(v.Day), 0, 0, 0, 0, time.UTC)
				availableThrough = t.AddDate(0, 0, -1).Format(time.DateOnly)
				if t.Before(dataEnd) {
					dataEnd = t
				}
			}
		}
	}
	rows := []*rep.GooglePlayDeveloperReportingV1beta1MetricsRow{}
	if dataEnd.After(p.Start()) {
		timeline := &rep.GooglePlayDeveloperReportingV1beta1TimelineSpec{AggregationPeriod: "DAILY", StartTime: dateTime(p.Start()), EndTime: dateTime(dataEnd)}
		token := ""
		seen := map[string]bool{}
		for {
			page, next, e := s.Reporting.VitalsPage(ctx, app, kind, timeline, dimensions, strings.Join(filters, " AND "), token)
			if e != nil {
				return model.Result{}, e
			}
			rows = append(rows, page...)
			if next == "" {
				break
			}
			if seen[next] {
				return model.Result{}, fault.New("PAGINATION_ERROR", "Google returned a repeated page token", 5)
			}
			seen[next] = true
			token = next
		}
	}
	state := status(len(rows))
	if !dataEnd.After(p.Start()) {
		state = "not_available"
	} else if dataEnd.Before(end) {
		state = "partial"
	}
	return model.Result{Data: rows, Meta: map[string]any{"source": "play-developer-reporting", "app": app, "metricSet": kind, "groupBy": group, "filter": strings.Join(filters, " AND "), "period": p, "dataStatus": state, "availableThrough": availableThrough, "freshnessInfo": fresh, "notice": "API decimal values are preserved as reported. Empty rows are not zero. Daily rates and distinct users are not aggregated across days."}}, nil
}
