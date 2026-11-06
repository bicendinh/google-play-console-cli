package metrics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"playops/internal/metrics"
	"playops/internal/model"
	"playops/internal/testutil"
)

func utf16(t *testing.T, s string) string {
	t.Helper()
	encoded, _, err := transform.String(unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder(), s)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
func TestCSVNullZeroAndQuotedCells(t *testing.T) {
	p := model.Period{From: "2026-10-30", To: "2026-11-02", Timezone: "America/Los_Angeles"}
	csv := "Date,Package Name,Country,Daily User Installs,Current User Installs,Future Column\n2026-10-31,com.example.app,US,0,100,\"hello, world\"\n2026-11-01,com.example.app,VN,,100,\"two\nlines\"\n"
	rows, err := metrics.ParseCSV(strings.NewReader(utf16(t, csv)), "com.example.app", "installs", "country", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Metrics["dailyUserInstalls"] != json.Number("0") || rows[1].Metrics["dailyUserInstalls"] != nil {
		t.Fatalf("null/zero lost: %+v", rows)
	}
	if rows[0].Extra["Future Column"] != "hello, world" || rows[1].Extra["Future Column"] != "two\nlines" {
		t.Fatal("quoted fields damaged")
	}
	if rows[0].Metrics["currentUserInstalls"] != json.Number("100") || rows[1].Metrics["currentUserInstalls"] != json.Number("100") {
		t.Fatal("cumulative values changed")
	}
	ratings := "Date,Package Name,App Version Code,Daily Average Rating,Total Average Rating\n2026-11-01,com.example.app,123,,4.16\n"
	r, err := metrics.ParseCSV(strings.NewReader(ratings), "com.example.app", "ratings", "app-version", p)
	if err != nil || r[0].Metrics["dailyAverageRating"] != nil || r[0].Metrics["totalAverageRating"] != json.Number("4.16") {
		t.Fatalf("ratings: %+v %v", r, err)
	}
	for _, bad := range []string{"Date,Country\n2026-11-01,US\n", strings.Replace(csv, "com.example.app", "com.other.app", 1), strings.Replace(csv, ",0,", ",null,", 1), strings.Replace(csv, ",0,", ",1.2,", 1)} {
		if _, err = metrics.ParseCSV(strings.NewReader(bad), "com.example.app", "installs", "country", p); err == nil {
			t.Fatal("accepted malformed report")
		}
	}
}
func TestMonthlyReportsMissingDataAndGeneration(t *testing.T) {
	seen := []string{}
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		if strings.Contains(r.URL.Path, "202611") {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":404,"message":"missing"}}`)
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			if r.URL.Query().Get("generation") != "7" {
				t.Error("download must pin generation")
			}
			fmt.Fprint(w, utf16(t, "Date,Package Name,Country,Daily User Installs\n2026-10-31,com.example.app,US,0\n"))
			return
		}
		fmt.Fprint(w, `{"generation":"7","updated":"2026-11-04T00:00:00Z"}`)
	})
	r, err := (metrics.Service{Reports: client}).Stats(context.Background(), "com.example.app", "reports", "installs", "country", model.Period{From: "2026-10-30", To: "2026-11-02", Timezone: "America/Los_Angeles"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Meta["dataStatus"] != "partial" || len(r.Data.([]metrics.Row)) != 1 || len(seen) != 3 {
		t.Fatalf("unexpected report: %+v seen %v", r, seen)
	}
	for _, kind := range []string{"installs", "ratings"} {
		r, err = (metrics.Service{Reports: client}).Stats(context.Background(), "com.example.app", "reports", kind, "country", model.Period{From: "2026-11-01", To: "2026-11-02"})
		if err != nil || r.Meta["dataStatus"] != "not_available" || len(r.Data.([]metrics.Row)) != 0 {
			t.Fatalf("missing report turned into zero: %+v %v", r, err)
		}
	}
}
func TestReportingPaginationFiltersAndFreshness(t *testing.T) {
	apps, vitals := 0, 0
	client := testutil.Google(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "apps:search"):
			apps++
			if r.URL.Query().Get("pageToken") == "" {
				fmt.Fprint(w, `{"apps":[{"name":"apps/com.example.one"}],"nextPageToken":"next"}`)
			} else {
				fmt.Fprint(w, `{"apps":[{"name":"apps/com.example.two"}]}`)
			}
		case strings.HasSuffix(r.URL.Path, "MetricSet"):
			fmt.Fprint(w, `{"freshnessInfo":{"freshnesses":[{"aggregationPeriod":"DAILY","latestEndTime":{"year":2026,"month":11,"day":5}}]}}`)
		case strings.HasSuffix(r.URL.Path, ":query"):
			vitals++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["filter"] != `versionCode = 123 AND countryCode = "US"` {
				t.Errorf("filter: %v", body["filter"])
			}
			timeline := body["timelineSpec"].(map[string]any)
			end := timeline["endTime"].(map[string]any)
			if end["day"] != float64(5) {
				t.Error("must bound query by freshness")
			}
			if body["pageToken"] == nil {
				fmt.Fprint(w, `{"rows":[{"metrics":[{"metric":"crashRate","decimalValue":{"value":"0"}}]}],"nextPageToken":"page-2"}`)
			} else {
				fmt.Fprint(w, `{"rows":[{"metrics":[{"metric":"crashRate","decimalValue":{"value":"0.5"}}]}]}`)
			}
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	})
	s := metrics.Service{Reporting: client}
	r, err := s.Apps(context.Background())
	if err != nil || apps != 2 {
		t.Fatalf("apps %v %v", r, err)
	}
	r, err = s.Vitals(context.Background(), "com.example.app", "crashes", model.Period{From: "2026-11-01", To: "2026-11-06", Timezone: "America/Los_Angeles"}, 123, "US", "country")
	if err != nil || vitals != 2 || r.Meta["dataStatus"] != "partial" || r.Meta["availableThrough"] != "2026-11-04" {
		t.Fatalf("vitals %+v %v calls%d", r, err, vitals)
	}
}
func TestDateRangeAcrossDST(t *testing.T) {
	now := time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)
	p, err := model.DateRange("", "", "America/Los_Angeles", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.From != "2026-10-26" || p.To != "2026-11-01" {
		t.Fatalf("wrong calendar window %+v", p)
	}
	for _, dates := range [][2]string{{"2026-11-02", ""}, {"2026-11-02", "2026-11-01"}, {"bad", "date"}} {
		if _, err = model.DateRange(dates[0], dates[1], "America/Los_Angeles", now); err == nil {
			t.Fatal("bad range accepted")
		}
	}
}
