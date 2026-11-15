package tools

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/mattoddie/hive-mcp/internal/hive"
)

// Schedule editing. Hive only takes a whole week at once, as
// POST /nodes/<type>/<id> {"schedule": {"monday": [{"start": <minutes>, "value": {...}}, ...], ...}},
// which is what pyhiveapi's set_schedule sends. The tools take only the days
// that change and fill in the rest from the current schedule.

// maxPeriods is the most periods a day can have, as in the Hive app.
const maxPeriods = 6

var scheduleDays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

type heatingPeriod struct {
	From        string  `json:"from" jsonschema:"Start time, HH:MM in the home's local time. Lasts until the next period starts"`
	Temperature float64 `json:"target_temperature" jsonschema:"Target temperature in °C, from 5 in steps of 0.5, up to HIVE_MAX_TEMP"`
}

type hotWaterPeriod struct {
	From   string `json:"from" jsonschema:"Start time, HH:MM in the home's local time. Lasts until the next period starts"`
	Status string `json:"status" jsonschema:"on or off"`
}

// dayKeys are the keys of a schedule tool's days argument: a day, or a
// group of days. More specific keys win: a day over weekdays or weekend,
// and those over every_day.
var dayKeys = map[string][]string{
	"every_day": scheduleDays, "weekdays": scheduleDays[:5], "weekend": scheduleDays[5:],
	"monday": {"monday"}, "tuesday": {"tuesday"}, "wednesday": {"wednesday"}, "thursday": {"thursday"},
	"friday": {"friday"}, "saturday": {"saturday"}, "sunday": {"sunday"},
}

// dayKeyNames lists dayKeys in order, for the schema.
var dayKeyNames = []string{"every_day", "weekdays", "weekend", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// expandDays resolves the groups into the periods for each day that changes.
func expandDays[P any](in map[string][]P) (map[string][]P, error) {
	out := map[string][]P{}
	for _, group := range dayKeyNames {
		if p, ok := in[group]; ok {
			for _, d := range dayKeys[group] {
				out[d] = p
			}
		}
	}
	for k := range in {
		if dayKeys[k] == nil {
			return nil, fmt.Errorf("%q isn't a day: use monday to sunday, weekdays, weekend or every_day", k)
		}
	}
	return out, nil
}

// parseClock reads HH:MM as minutes past midnight.
func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || len(m) != 2 || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q is not a time of day; use HH:MM from 00:00 to 23:59", s)
	}
	return hh*60 + mm, nil
}

type slot struct {
	start int
	value Obj
}

// daySlots validates one day's periods and returns them as Hive's slots, in order.
func daySlots(day string, periods []slot) ([]any, error) {
	if len(periods) == 0 {
		return nil, fmt.Errorf("%s: give at least one period", day)
	}
	if len(periods) > maxPeriods {
		return nil, fmt.Errorf("%s: at most %d periods a day, got %d", day, maxPeriods, len(periods))
	}
	sort.SliceStable(periods, func(i, j int) bool { return periods[i].start < periods[j].start })
	out := make([]any, len(periods))
	for i, p := range periods {
		if i > 0 && p.start == periods[i-1].start {
			return nil, fmt.Errorf("%s: two periods start at %02d:%02d", day, p.start/60, p.start%60)
		}
		out[i] = Obj{"start": p.start, "value": p.value}
	}
	return out, nil
}

// buildSchedule merges the changed days into the product's current
// schedule, returning the whole week to send and the days that changed.
func buildSchedule(p Obj, changed map[string][]slot) (Obj, []string, error) {
	if len(changed) == 0 {
		return nil, nil, errNoChange
	}
	current, _ := hive.Get(p, "state", "schedule").(Obj)
	week := Obj{}
	var days []string
	for _, d := range scheduleDays {
		if periods, ok := changed[d]; ok {
			slots, err := daySlots(d, periods)
			if err != nil {
				return nil, nil, err
			}
			week[d] = slots
			days = append(days, d)
			continue
		}
		cur, ok := current[d].([]any)
		if !ok || len(cur) == 0 {
			return nil, nil, fmt.Errorf("%s has no %s schedule to keep, so give periods for every day (for example with every_day)", hive.Name(p), d)
		}
		week[d] = cur
	}
	return week, days, nil
}

// sameSchedule reports whether Hive stored the schedule that was sent.
func sameSchedule(stored, sent Obj) bool {
	norm := func(o Obj) map[string][][2]string {
		out := map[string][][2]string{}
		for _, d := range scheduleDays {
			for _, s := range hive.Objs(o[d]) {
				start, _ := hive.Num(s, "start")
				v, _ := s["value"].(Obj)
				out[d] = append(out[d], [2]string{strconv.FormatFloat(start, 'f', -1, 64), fmt.Sprint(normValue(v))})
			}
		}
		return out
	}
	return reflect.DeepEqual(norm(stored), norm(sent))
}

func normValue(v Obj) any {
	if t, ok := hive.Num(v, "target"); ok {
		return t
	}
	return strings.ToUpper(hive.Str(v, "status"))
}

// setSchedule sends the new week and reports it as Hive now has it.
func setSchedule(ctx context.Context, c *Context, p Obj, week Obj, days []string) (any, error) {
	out, err := change(ctx, c.Client, p, Obj{"schedule": week}, func(n *hive.Nodes, q Obj) Obj {
		stored, _ := hive.Get(q, "state", "schedule").(Obj)
		res := Obj{"week": hive.FormatSchedule(stored)}
		if !sameSchedule(stored, week) {
			res["warning"] = "Hive accepted the schedule but doesn't show it yet. Check again in a minute with the get schedule tool."
		}
		return res
	})
	if err != nil {
		return nil, err
	}
	o := out.(Obj)
	// The whole week was sent; say which days changed rather than repeating it.
	delete(o, "sent")
	o["days_changed"] = days
	return o, nil
}
