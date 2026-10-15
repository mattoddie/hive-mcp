package hive

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Obj is a JSON object from the API. Objects are passed around generically
// so fields this server doesn't know about survive, and raw output shows them.
type Obj = map[string]any

// Product and device types. Hive calls the things you control "products"
// (a heating zone, the hot water, a plug) and the hardware "devices" (a
// thermostat, a boiler module, a TRV, a hub).
var (
	HeatingTypes = []string{"heating", "trvcontrol"}
	HotWaterType = "hotwater"
	PlugTypes    = []string{"activeplug"}
	LightTypes   = []string{"warmwhitelight", "tuneablelight", "colourtuneablelight"}
	SensorTypes  = []string{"motionsensor", "contactsensor"}
)

// LowBattery is the battery percentage at or below which a device is flagged.
const LowBattery = 25

// Compact drops nil, "" and empty slices/maps so summaries stay small.
// Booleans are kept: "online": false matters.
func Compact(o Obj) Obj {
	for k, v := range o {
		switch x := v.(type) {
		case nil:
			delete(o, k)
		case string:
			if x == "" {
				delete(o, k)
			}
		case []any:
			if len(x) == 0 {
				delete(o, k)
			}
		case []string:
			if len(x) == 0 {
				delete(o, k)
			}
		case []Obj:
			if len(x) == 0 {
				delete(o, k)
			}
		case Obj:
			if len(x) == 0 {
				delete(o, k)
			}
		}
	}
	return o
}

// Get walks nested objects: Get(o, "props", "temperature").
func Get(o any, path ...string) any {
	cur := o
	for _, p := range path {
		m, ok := cur.(Obj)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// Str returns a nested string (or a number formatted as a string), or "".
func Str(o any, path ...string) string {
	switch v := Get(o, path...).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Num returns a nested number (or numeric string) and whether there was one.
func Num(o any, path ...string) (float64, bool) {
	switch v := Get(o, path...).(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// NumOrNil returns a nested number, or nil so Compact drops it.
func NumOrNil(o any, path ...string) any {
	if f, ok := Num(o, path...); ok {
		return f
	}
	return nil
}

// Bool returns a nested boolean, or false.
func Bool(o any, path ...string) bool {
	b, _ := Get(o, path...).(bool)
	return b
}

// BoolOrNil returns a nested boolean, or nil when it's missing.
func BoolOrNil(o any, path ...string) any {
	if b, ok := Get(o, path...).(bool); ok {
		return b
	}
	return nil
}

// Objs converts a JSON array to objects, skipping anything else.
func Objs(v any) []Obj {
	list, _ := v.([]any)
	out := make([]Obj, 0, len(list))
	for _, x := range list {
		if o, ok := x.(Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Nodes is a snapshot of everything on the account, from GET /nodes/all.
type Nodes struct {
	Raw     Obj
	Fetched time.Time
}

// Products returns the products of the given types (all with none).
func (n *Nodes) Products(types ...string) []Obj {
	var out []Obj
	for _, p := range Objs(n.Raw["products"]) {
		if len(types) == 0 || contains(types, Str(p, "type")) {
			out = append(out, p)
		}
	}
	sortNodes(out)
	return out
}

// Devices returns the devices of the given types (all with none).
func (n *Nodes) Devices(types ...string) []Obj {
	var out []Obj
	for _, d := range Objs(n.Raw["devices"]) {
		if len(types) == 0 || contains(types, Str(d, "type")) {
			out = append(out, d)
		}
	}
	sortNodes(out)
	return out
}

func sortNodes(list []Obj) {
	sort.SliceStable(list, func(i, j int) bool {
		a, _ := Num(list[i], "sortOrder")
		b, _ := Num(list[j], "sortOrder")
		if a != b {
			return a < b
		}
		return strings.ToLower(Str(list[i], "state", "name")) < strings.ToLower(Str(list[j], "state", "name"))
	})
}

// Device finds a device by ID.
func (n *Nodes) Device(id string) Obj {
	for _, d := range Objs(n.Raw["devices"]) {
		if Str(d, "id") == id {
			return d
		}
	}
	return nil
}

// Homes lists the homes on the account.
func (n *Nodes) Homes() []Obj {
	return Objs(Get(n.Raw, "homes", "homes"))
}

// Location is the primary home's time zone, which Hive schedules run in (Europe/London if unknown).
func (n *Nodes) Location() *time.Location {
	homes := n.Homes()
	for _, h := range homes {
		if Bool(h, "primary") || len(homes) == 1 {
			if loc, err := time.LoadLocation(Str(h, "location", "timeZone")); err == nil && Str(h, "location", "timeZone") != "" {
				return loc
			}
		}
	}
	if loc, err := time.LoadLocation("Europe/London"); err == nil {
		return loc
	}
	return time.UTC
}

// When formats a Hive timestamp (Unix milliseconds) in the home's time zone.
func (n *Nodes) When(ms any) string {
	f, ok := ms.(float64)
	if !ok || f <= 0 {
		return ""
	}
	return time.UnixMilli(int64(f)).In(n.Location()).Format(time.RFC3339)
}

// Name is a product or device's name.
func Name(o Obj) string {
	if s := Str(o, "state", "name"); s != "" {
		return s
	}
	return Str(o, "props", "zoneName")
}

func lower(s string) string { return strings.ToLower(s) }

// ---------------------------------------------------------------- schedules

var days = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

func weekday(t time.Time) int { return (int(t.Weekday()) + 6) % 7 } // Monday = 0

func clock(minutes float64) string {
	m := int(minutes)
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// ScheduleSlot is a schedule entry: from a time of day, a value (a target temperature or ON/OFF).
type scheduleSlot struct {
	day   int
	start float64
	value Obj
}

func slotsOf(schedule Obj, day int) []scheduleSlot {
	var out []scheduleSlot
	for _, s := range Objs(schedule[days[day]]) {
		start, _ := Num(s, "start")
		v, _ := s["value"].(Obj)
		out = append(out, scheduleSlot{day: day, start: start, value: v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// scheduleAt finds the schedule entry in force at t, and the next change after it.
func scheduleAt(schedule Obj, t time.Time) (current, next *scheduleSlot) {
	if len(schedule) == 0 {
		return nil, nil
	}
	today := weekday(t)
	minutes := float64(t.Hour()*60 + t.Minute())
	for _, s := range slotsOf(schedule, today) {
		if s.start <= minutes {
			s := s
			current = &s
		} else if next == nil {
			s := s
			next = &s
		}
	}
	for i := 1; i <= 7 && current == nil; i++ {
		if prev := slotsOf(schedule, (today+7-i)%7); len(prev) > 0 {
			current = &prev[len(prev)-1]
		}
	}
	for i := 1; i <= 7 && next == nil; i++ {
		if later := slotsOf(schedule, (today+i)%7); len(later) > 0 {
			next = &later[0]
		}
	}
	return current, next
}

func slotValue(v Obj) any {
	if t, ok := Num(v, "target"); ok {
		return t
	}
	if s := Str(v, "status"); s != "" {
		return lower(s)
	}
	return v
}

// ScheduleNow describes what the schedule asks for now and when it next changes.
func (n *Nodes) ScheduleNow(p Obj, now time.Time) Obj {
	sched, _ := Get(p, "state", "schedule").(Obj)
	cur, next := scheduleAt(sched, now.In(n.Location()))
	if cur == nil {
		return nil
	}
	key := "target_temperature"
	if _, ok := Num(cur.value, "target"); !ok {
		key = "status"
	}
	out := Obj{key: slotValue(cur.value), "since": days[cur.day] + " " + clock(cur.start)}
	if next != nil {
		out["next_change"] = Obj{"at": days[next.day] + " " + clock(next.start), key: slotValue(next.value)}
	}
	return out
}

// FormatSchedule turns Hive's schedule into readable periods per day.
func FormatSchedule(schedule Obj) Obj {
	out := Obj{}
	for d := range days {
		slots := slotsOf(schedule, d)
		var periods []Obj
		for i, s := range slots {
			end := "24:00"
			if i+1 < len(slots) {
				end = clock(slots[i+1].start)
			}
			p := Obj{"from": clock(s.start), "until": end}
			if t, ok := Num(s.value, "target"); ok {
				p["target_temperature"] = t
			} else if st := Str(s.value, "status"); st != "" {
				p["status"] = lower(st)
			} else {
				p["value"] = s.value
			}
			periods = append(periods, p)
		}
		if periods != nil {
			out[days[d]] = periods
		}
	}
	return out
}

// ---------------------------------------------------------------- summaries

// zoneDevices are the devices behind a heating product: the thermostat for
// a heating zone, the TRVs for a TRV zone.
func (n *Nodes) zoneDevices(p Obj) []Obj {
	var out []Obj
	switch Str(p, "type") {
	case "trvcontrol":
		ids, _ := Get(p, "props", "trvs").([]any)
		for _, id := range ids {
			if d := n.Device(fmt.Sprint(id)); d != nil {
				out = append(out, d)
			}
		}
	default:
		zone := Str(p, "props", "zone")
		for _, d := range n.Devices("thermostatui") {
			if zone != "" && Str(d, "props", "zone") == zone {
				out = append(out, d)
			}
		}
	}
	return out
}

func batterySummary(devs []Obj) (lowest any, details []Obj) {
	min := math.Inf(1)
	for _, d := range devs {
		b, ok := Num(d, "props", "battery")
		if !ok || Str(d, "props", "power") == "mains" {
			continue
		}
		min = math.Min(min, b)
		details = append(details, Obj{"device": Name(d), "type": Str(d, "type"), "battery": b})
	}
	if math.IsInf(min, 1) {
		return nil, nil
	}
	return min, details
}

func online(o Obj) any { return BoolOrNil(o, "props", "online") }

// boosting reports whether a heating or hot water product is boosting.
func boosting(p Obj) bool {
	if Str(p, "state", "mode") == "BOOST" {
		return true
	}
	switch b := Get(p, "state", "boost").(type) {
	case float64:
		return b > 0
	case bool:
		return b
	}
	return false
}

// SummarizeHeating describes a heating zone or TRV zone.
func (n *Nodes) SummarizeHeating(p Obj, now time.Time) Obj {
	kind := "heating zone"
	if Str(p, "type") == "trvcontrol" {
		kind = "TRV zone"
	}
	mode := lower(Str(p, "state", "mode"))
	out := Obj{
		"id": Str(p, "id"), "name": Name(p), "type": Str(p, "type"), "kind": kind,
		"online":                       online(p),
		"current_temperature":          NumOrNil(p, "props", "temperature"),
		"target_temperature":           NumOrNil(p, "state", "target"),
		"mode":                         mode,
		"heating_active":               BoolOrNil(p, "props", "working"),
		"frost_protection_temperature": NumOrNil(p, "state", "frostProtection"),
	}
	if boosting(p) {
		out["mode"] = "boost"
		out["boost_minutes_left"] = NumOrNil(p, "state", "boost")
		out["mode_after_boost"] = lower(Str(p, "props", "previous", "mode"))
	}
	if mode == "schedule" || (boosting(p) && Str(p, "props", "previous", "mode") == "SCHEDULE") {
		out["schedule_now"] = n.ScheduleNow(p, now)
	}
	if Bool(p, "props", "holidayMode", "active") {
		out["holiday_mode"] = true
	}
	if ab := Str(p, "state", "autoBoost"); ab != "" {
		out["heat_on_demand"] = ab == "ENABLED"
	}
	devs := n.zoneDevices(p)
	if low, details := batterySummary(devs); low != nil {
		out["lowest_battery"] = low
		out["batteries"] = details
	}
	for _, d := range devs {
		if online(d) == false {
			out["offline_devices"] = append(asList(out["offline_devices"]), Name(d)+" ("+Str(d, "type")+")")
		}
	}
	if Str(p, "type") == "trvcontrol" {
		out["trvs"] = len(devs)
	}
	return Compact(out)
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// SummarizeHotWater describes the hot water.
func (n *Nodes) SummarizeHotWater(p Obj, now time.Time) Obj {
	mode := lower(Str(p, "state", "mode"))
	out := Obj{
		"id": Str(p, "id"), "name": Name(p), "online": online(p),
		"mode": map[string]string{"manual": "on", "schedule": "schedule", "off": "off", "boost": "boost"}[mode],
		"on":   Str(p, "state", "status") == "ON",
	}
	if out["mode"] == "" {
		out["mode"] = mode
	}
	if boosting(p) {
		out["mode"] = "boost"
		out["boost_minutes_left"] = NumOrNil(p, "state", "boost")
		prev := lower(Str(p, "props", "previous", "mode"))
		if prev == "manual" {
			prev = "on"
		}
		out["mode_after_boost"] = prev
	}
	if mode == "schedule" || (boosting(p) && Str(p, "props", "previous", "mode") == "SCHEDULE") {
		out["schedule_now"] = n.ScheduleNow(p, now)
	}
	if Bool(p, "props", "holidayMode", "active") {
		out["holiday_mode"] = true
	}
	return Compact(out)
}

// SummarizePlug describes a smart plug.
func (n *Nodes) SummarizePlug(p Obj) Obj {
	o := Compact(Obj{
		"id": Str(p, "id"), "name": Name(p), "type": Str(p, "type"), "online": online(p),
		"mode": lower(Str(p, "state", "mode")), "power_watts": NumOrNil(p, "props", "powerConsumption"),
		"in_use": BoolOrNil(p, "props", "inUse"), "on_since": n.When(Get(p, "props", "onSince")),
	})
	o["on"] = Str(p, "state", "status") == "ON"
	if !o["on"].(bool) {
		delete(o, "on_since")
	}
	return o
}

// SummarizeLight describes a light.
func (n *Nodes) SummarizeLight(p Obj) Obj {
	o := Compact(Obj{
		"id": Str(p, "id"), "name": Name(p), "type": Str(p, "type"), "online": online(p),
		"mode": lower(Str(p, "state", "mode")), "brightness": NumOrNil(p, "state", "brightness"),
		"colour_temperature_kelvin": NumOrNil(p, "state", "colourTemperature"),
		"colour_mode":               lower(Str(p, "state", "colourMode")),
	})
	if lo, ok := Num(p, "props", "colourTemperature", "min"); ok {
		hi, _ := Num(p, "props", "colourTemperature", "max")
		o["colour_temperature_range"] = []float64{lo, hi}
	}
	if Str(p, "state", "colourMode") == "COLOUR" {
		o["hue"], o["saturation"], o["value"] = NumOrNil(p, "state", "hue"), NumOrNil(p, "state", "saturation"), NumOrNil(p, "state", "value")
	}
	o["on"] = Str(p, "state", "status") == "ON"
	return o
}

// SummarizeSensor describes a motion or contact sensor.
func (n *Nodes) SummarizeSensor(p Obj) Obj {
	o := Obj{
		"id": Str(p, "id"), "name": Name(p), "type": Str(p, "type"), "online": online(p),
		"battery": NumOrNil(p, "props", "battery"), "temperature": NumOrNil(p, "props", "temperature"),
		"last_changed": n.When(Get(p, "props", "statusChanged")),
	}
	switch Str(p, "type") {
	case "motionsensor":
		o["motion"] = Bool(p, "props", "motion", "status")
	case "contactsensor":
		o["open"] = Str(p, "props", "status") == "OPEN"
	}
	return Compact(o)
}

// SummarizeDevice describes a piece of hardware: thermostat, boiler module, TRV, hub, plug, bulb or sensor.
func (n *Nodes) SummarizeDevice(d Obj) Obj {
	o := Obj{
		"id": Str(d, "id"), "name": Name(d), "type": Str(d, "type"), "online": online(d),
		"model": Str(d, "props", "model"), "manufacturer": Str(d, "props", "manufacturer"),
		"firmware": Str(d, "props", "version"), "power": Str(d, "props", "power"),
		"signal": NumOrNil(d, "props", "signal"), "last_seen": n.When(d["lastSeen"]),
	}
	if Str(d, "props", "power") != "mains" {
		o["battery"] = NumOrNil(d, "props", "battery")
	}
	if Str(d, "type") == "trv" {
		o["zone"] = Str(d, "state", "zoneName")
		o["child_lock"] = BoolOrNil(d, "state", "childLock")
		o["calibration"] = lower(Str(d, "state", "calibrationStatus"))
		o["mounting"] = lower(Str(d, "state", "mountingMode"))
		o["controlled_by"] = Str(d, "state", "control")
	}
	if zone := Str(d, "state", "zoneName"); zone != "" && zone != Name(d) {
		o["zone"] = zone
	}
	return Compact(o)
}

// Discontinued names device types of products Hive has stopped supporting.
// They still show up on accounts that had them, but hive-mcp only reports them.
var Discontinued = map[string]string{
	"keypad": "HomeShield keypad (Hive ended HomeShield support on 1 August 2025)",
	"siren":  "HomeShield siren (Hive ended HomeShield support on 1 August 2025)",
}
