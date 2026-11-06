package model

import (
	"time"
	_ "time/tzdata"

	"playops/internal/fault"
)

type Result struct {
	Data any
	Meta map[string]any
}
type Period struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
}

func DateRange(from, to, zone string, now time.Time) (Period, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Period{}, err
	}
	if (from == "") != (to == "") {
		return Period{}, fault.Input("--from and --to must be provided together")
	}
	if from == "" {
		n := now.In(loc)
		today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
		from = today.AddDate(0, 0, -7).Format(time.DateOnly)
		to = today.AddDate(0, 0, -1).Format(time.DateOnly)
	}
	f, e1 := time.Parse(time.DateOnly, from)
	t, e2 := time.Parse(time.DateOnly, to)
	if e1 != nil || e2 != nil || t.Before(f) {
		return Period{}, fault.Input("Use --from YYYY-MM-DD --to YYYY-MM-DD with from <= to")
	}
	if t.After(f.AddDate(3, 0, 0)) {
		return Period{}, fault.Input("Date range cannot exceed three years")
	}
	return Period{From: from, To: to, Timezone: zone}, nil
}
func (p Period) Start() time.Time { t, _ := time.Parse(time.DateOnly, p.From); return t }
func (p Period) EndExclusive() time.Time {
	t, _ := time.Parse(time.DateOnly, p.To)
	return t.AddDate(0, 0, 1)
}
