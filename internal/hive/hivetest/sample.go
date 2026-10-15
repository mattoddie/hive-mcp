package hivetest

import "encoding/json"

// Product and device IDs in the sample home.
const (
	HeatingID  = "heating-0000-4000-8000-000000000001"
	HotWaterID = "hotwater-0000-4000-8000-000000000001"
	TRVZoneID  = "trvcontrol-0000-4000-8000-000000000001"
	TRVID      = "trv-0000-4000-8000-000000000001"
	PlugID     = "plug-0000-4000-8000-000000000001"
	LightID    = "light-0000-4000-8000-000000000001" // warm white, off
	TuneableID = "light-0000-4000-8000-000000000002" // tuneable, on
	SensorID   = "sensor-0000-4000-8000-000000000001"
	BoilerID   = "boilermodule-0000-4000-8000-000000000001"
	HubID      = "hub-0000-4000-8000-000000000001"
)

func schedule(slots string) string {
	days := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	out := "{"
	for i, d := range days {
		if i > 0 {
			out += ","
		}
		out += `"` + d + `":` + slots
	}
	return out + "}"
}

// SampleNodes is a small home in the shape of GET /nodes/all: a heating zone
// with a thermostat, hot water, a TRV zone with one radiator valve (low
// battery), a smart plug, two bulbs, a door sensor and a discontinued
// HomeShield keypad.
func SampleNodes() map[string]any {
	heatingSchedule := schedule(`[{"value":{"target":20},"start":390},{"value":{"target":16},"start":510},{"value":{"target":21},"start":1020},{"value":{"target":16},"start":1350}]`)
	waterSchedule := schedule(`[{"value":{"status":"ON"},"start":360},{"value":{"status":"OFF"},"start":450},{"value":{"status":"ON"},"start":1080},{"value":{"status":"OFF"},"start":1140}]`)
	doc := `{
  "user": {"id": "user-1", "username": "sam@example.com", "email": "sam@example.com", "firstName": "Sam", "lastName": "Smith", "temperatureUnit": "C", "timezone": "Europe/London", "postcode": "AB1 2CD"},
  "homes": {"homes": [{"id": "home-1", "name": "Home", "primary": true, "owner": true, "userType": "OWNER", "homeTypes": ["HIVE"], "location": {"country": "GB", "timeZone": "Europe/London", "postcode": "AB1 2CD"}}]},
  "holidayMode": {"active": false, "enabled": false, "temperature": 7},
  "actions": [{"id": "action-1", "name": "Lights off at bedtime", "enabled": true}],
  "products": [
    {"id": "` + HeatingID + `", "type": "heating", "sortOrder": 1, "lastSeen": 1791100000000, "parent": "` + BoilerID + `",
     "props": {"online": true, "model": "SLR2", "temperature": 18.5, "working": true, "previous": {"mode": "OFF"}, "zone": "` + BoilerID + `",
               "holidayMode": {"active": false}, "autoBoost": {"active": false, "duration": 30}, "capabilities": ["BOOST", "HOLIDAY_MODE"]},
     "state": {"name": "Heating", "mode": "SCHEDULE", "target": 20, "boost": null, "frostProtection": 7, "autoBoost": "DISABLED", "schedule": ` + heatingSchedule + `}},
    {"id": "` + HotWaterID + `", "type": "hotwater", "sortOrder": 2, "parent": "` + BoilerID + `",
     "props": {"online": true, "model": "SLR2", "previous": {"mode": "OFF"}, "zone": "` + BoilerID + `", "holidayMode": {"enabled": false}},
     "state": {"name": "Hot Water", "mode": "SCHEDULE", "status": "OFF", "boost": null, "schedule": ` + waterSchedule + `}},
    {"id": "` + TRVZoneID + `", "type": "trvcontrol", "sortOrder": 3, "parent": "` + HubID + `",
     "props": {"online": true, "temperature": 17.1, "working": false, "trvs": ["` + TRVID + `"], "previous": {}, "zoneName": "Bedroom"},
     "state": {"name": "Bedroom", "mode": "MANUAL", "target": 18, "frostProtection": 7, "schedule": ` + heatingSchedule + `}},
    {"id": "` + PlugID + `", "type": "activeplug", "sortOrder": 5, "parent": "` + HubID + `",
     "props": {"online": true, "model": "SLP2b", "powerConsumption": 12, "inUse": true, "onSince": 1791000000000},
     "state": {"name": "Lamp plug", "status": "ON", "mode": "MANUAL"}},
    {"id": "` + LightID + `", "type": "warmwhitelight", "sortOrder": 7, "parent": "` + HubID + `",
     "props": {"online": true, "model": "FWBulb01"},
     "state": {"name": "Hallway", "status": "OFF", "brightness": 80, "mode": "MANUAL"}},
    {"id": "` + TuneableID + `", "type": "tuneablelight", "sortOrder": 8, "parent": "` + HubID + `",
     "props": {"online": true, "model": "TWBulb01UK", "colourTemperature": {"min": 2700, "max": 6535}},
     "state": {"name": "Living room", "status": "ON", "brightness": 60, "mode": "MANUAL", "colourTemperature": 3000}},
    {"id": "` + SensorID + `", "type": "contactsensor", "sortOrder": 12, "parent": "` + HubID + `",
     "props": {"online": true, "model": "DWS003", "status": "CLOSED", "statusChanged": 1791050000000, "battery": 90},
     "state": {"name": "Back door"}}
  ],
  "devices": [
    {"id": "` + HubID + `", "type": "hub", "sortOrder": 0, "lastSeen": 1791100000000, "props": {"online": true, "model": "NANO2", "manufacturer": "Hive", "version": "1.0.0", "power": "mains", "signal": 100}, "state": {"name": "Hub"}},
    {"id": "thermostatui-0000-4000-8000-000000000001", "type": "thermostatui", "sortOrder": 1, "props": {"online": true, "model": "SLT3", "manufacturer": "Computime", "power": "battery", "battery": 70, "signal": 95, "zone": "` + BoilerID + `"}, "state": {"name": "Heating", "zoneName": "Heating"}},
    {"id": "` + BoilerID + `", "type": "boilermodule", "sortOrder": 1, "props": {"online": true, "model": "SLR2", "manufacturer": "Computime", "power": "mains", "signal": 90, "zone": "` + BoilerID + `"}, "state": {"name": "Heating", "zoneName": "Heating"}},
    {"id": "` + TRVID + `", "type": "trv", "sortOrder": 3, "props": {"online": true, "model": "TRV001", "manufacturer": "Danfoss", "power": "battery", "battery": 20, "signal": 80}, "state": {"name": "Bedroom", "zoneName": "Bedroom", "control": "` + TRVZoneID + `", "childLock": false, "calibrationStatus": "CALIBRATED", "mountingMode": "HORIZONTAL"}},
    {"id": "` + PlugID + `", "type": "activeplug", "sortOrder": 5, "props": {"online": true, "model": "SLP2b", "power": "mains", "signal": 100}, "state": {"name": "Lamp plug"}},
    {"id": "` + LightID + `", "type": "warmwhitelight", "sortOrder": 7, "props": {"online": true, "model": "FWBulb01", "power": "mains"}, "state": {"name": "Hallway"}},
    {"id": "` + TuneableID + `", "type": "tuneablelight", "sortOrder": 8, "props": {"online": true, "model": "TWBulb01UK", "power": "mains"}, "state": {"name": "Living room"}},
    {"id": "` + SensorID + `", "type": "contactsensor", "sortOrder": 12, "props": {"online": true, "model": "DWS003", "power": "battery", "battery": 90}, "state": {"name": "Back door"}},
    {"id": "keypad-0000-4000-8000-000000000001", "type": "keypad", "sortOrder": 15, "props": {"online": false, "model": "KEYPAD001", "power": "battery", "battery": 0}, "state": {"name": "Keypad"}}
  ]
}`
	var out map[string]any
	if err := json.Unmarshal([]byte(doc), &out); err != nil {
		panic(err)
	}
	return out
}
