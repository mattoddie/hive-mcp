package tools

import (
	"context"
	"fmt"
	"math"

	"github.com/mattoddie/hive-mcp/internal/hive"
)

type listSmartArgs struct {
	Kind string `json:"kind,omitempty" jsonschema:"Only plugs, lights or sensors (default all)"`
	rawArgs
	freshArgs
}

type setPlugArgs struct {
	Plug string `json:"plug,omitempty" jsonschema:"Smart plug, by name or ID (see hive_list_smart_devices). Can be left out when there's only one"`
	On   bool   `json:"on" jsonschema:"true to switch the plug on, false to switch it off"`
}

type setLightArgs struct {
	Light             string `json:"light,omitempty" jsonschema:"Light, by name or ID (see hive_list_smart_devices). Can be left out when there's only one"`
	On                *bool  `json:"on,omitempty" jsonschema:"true to switch on, false to switch off. Setting a brightness, colour temperature or colour also switches the light on"`
	Brightness        *int   `json:"brightness,omitempty" jsonschema:"Brightness in percent, 5 to 100 (rounded to the nearest 5, as the Hive app does). With hue or saturation, the colour's brightness"`
	ColourTemperature *int   `json:"colour_temperature,omitempty" jsonschema:"White colour temperature in kelvin, for tuneable and colour bulbs: about 2700 (warm) to 6500 (cool)"`
	Hue               *int   `json:"hue,omitempty" jsonschema:"Colour, for colour bulbs: hue in degrees, 0 to 359 (0 red, 120 green, 240 blue). Switches the bulb to colour mode; the saturation is kept unless given"`
	Saturation        *int   `json:"saturation,omitempty" jsonschema:"Colour, for colour bulbs: saturation in percent, 0 (white) to 100 (full colour). Switches the bulb to colour mode; the hue is kept unless given"`
}

func registerDevices(c *Context) {
	cl := c.Client
	plugSummary := func(n *hive.Nodes, p Obj) Obj { return n.SummarizePlug(p) }
	lightSummary := func(n *hive.Nodes, p Obj) Obj { return n.SummarizeLight(p) }

	add(c, Def{
		Toolset: "devices", Name: "hive_list_smart_devices", Title: "List plugs, lights and sensors",
		Description: "List Hive smart plugs (on or off, power use), lights (on or off, brightness, colour temperature) and sensors (window and door sensors open or closed, motion sensors), with online status and batteries.",
		Enums:       map[string][]string{"kind": {"plugs", "lights", "sensors"}},
	}, func(ctx context.Context, a listSmartArgs) (any, error) {
		n, err := c.nodes(ctx, a.Fresh)
		if err != nil {
			return nil, err
		}
		items := []Obj{}
		add := func(key, kind string, types []string, f func(Obj) Obj) {
			if a.Kind != "" && a.Kind != key {
				return
			}
			for _, p := range n.Products(types...) {
				var item Obj
				if a.Raw {
					item = redactObj(p)
				} else {
					item = f(p)
				}
				item["kind"] = kind
				items = append(items, item)
			}
		}
		add("plugs", "plug", hive.PlugTypes, n.SummarizePlug)
		add("lights", "light", hive.LightTypes, n.SummarizeLight)
		add("sensors", "sensor", hive.SensorTypes, n.SummarizeSensor)
		return listResult(items), nil
	})

	add(c, Def{
		Toolset: "devices", Name: "hive_set_plug", Title: "Switch a smart plug",
		Description: "Switch a Hive smart plug on or off. Check what's plugged in with the user before switching it off.",
		Gate:        Write, Idempotent: true,
	}, func(ctx context.Context, a setPlugArgs) (any, error) {
		n, err := c.nodes(ctx, false)
		if err != nil {
			return nil, err
		}
		p, err := find(n.Products(hive.PlugTypes...), a.Plug, "smart plug")
		if err != nil {
			return nil, err
		}
		status := "OFF"
		if a.On {
			status = "ON"
		}
		return change(ctx, cl, p, Obj{"status": status}, plugSummary)
	})

	add(c, Def{
		Toolset: "devices", Name: "hive_set_light", Title: "Control a light",
		Description: "Switch a Hive light on or off, and set its brightness, its white colour temperature (tuneable and colour bulbs) or its colour as hue and saturation (colour bulbs).",
		Gate:        Write, Idempotent: true,
		Ranges: map[string][2]float64{"brightness": {5, 100}, "colour_temperature": {2700, 6535}, "hue": {0, 359}, "saturation": {0, 100}},
	}, func(ctx context.Context, a setLightArgs) (any, error) {
		colour := a.Hue != nil || a.Saturation != nil
		if a.On == nil && a.Brightness == nil && a.ColourTemperature == nil && !colour {
			return nil, errNoChange
		}
		if a.On != nil && !*a.On && (a.Brightness != nil || a.ColourTemperature != nil || colour) {
			return nil, fmt.Errorf("a light that's being switched off can't also take a brightness, colour temperature or colour")
		}
		if colour && a.ColourTemperature != nil {
			return nil, fmt.Errorf("give either a colour temperature (white) or a hue and saturation (colour), not both")
		}
		if (a.Hue != nil && (*a.Hue < 0 || *a.Hue > 359)) || (a.Saturation != nil && (*a.Saturation < 0 || *a.Saturation > 100)) {
			return nil, fmt.Errorf("hue must be 0 to 359 and saturation 0 to 100")
		}
		n, err := c.nodes(ctx, false)
		if err != nil {
			return nil, err
		}
		p, err := find(n.Products(hive.LightTypes...), a.Light, "light")
		if err != nil {
			return nil, err
		}
		typ := hive.Str(p, "type")

		if a.Brightness != nil && (*a.Brightness < 5 || *a.Brightness > 100) {
			return nil, fmt.Errorf("brightness must be between 5 and 100")
		}
		var bodies []Obj
		switch {
		case a.On != nil && !*a.On:
			bodies = append(bodies, Obj{"status": "OFF"})
		case a.Brightness != nil && !colour:
			b := int(math.Round(float64(*a.Brightness)/5) * 5)
			bodies = append(bodies, Obj{"status": "ON", "brightness": b})
		case a.On != nil && !colour:
			bodies = append(bodies, Obj{"status": "ON"})
		}
		if colour {
			if typ != "colourtuneablelight" {
				return nil, fmt.Errorf("%s isn't a colour bulb, so it can't take a hue or saturation", hive.Name(p))
			}
			// The colour's brightness is its "value". Hue, saturation and value go
			// together, as strings, as pyhiveapi's set_color sends them.
			hsv := [3]float64{0, 100, 100}
			for i, k := range []string{"hue", "saturation", "value"} {
				if v, ok := hive.Num(p, "state", k); ok {
					hsv[i] = v
				}
			}
			if a.Hue != nil {
				hsv[0] = float64(*a.Hue)
			}
			if a.Saturation != nil {
				hsv[1] = float64(*a.Saturation)
			}
			if a.Brightness != nil {
				hsv[2] = float64(*a.Brightness)
			}
			body := Obj{"colourMode": "COLOUR", "hue": fmt.Sprint(hsv[0]), "saturation": fmt.Sprint(hsv[1]), "value": fmt.Sprint(hsv[2])}
			if len(bodies) == 0 {
				body["status"] = "ON"
			}
			bodies = append(bodies, body)
		}
		if a.ColourTemperature != nil {
			if typ == "warmwhitelight" {
				return nil, fmt.Errorf("%s is a warm white bulb, so its colour temperature can't be changed", hive.Name(p))
			}
			lo, hi := 2700.0, 6535.0
			if v, ok := hive.Num(p, "props", "colourTemperature", "min"); ok {
				lo = v
			}
			if v, ok := hive.Num(p, "props", "colourTemperature", "max"); ok {
				hi = v
			}
			k := float64(*a.ColourTemperature)
			if k < lo || k > hi {
				return nil, fmt.Errorf("%s takes colour temperatures from %g to %g K", hive.Name(p), lo, hi)
			}
			body := Obj{"colourTemperature": *a.ColourTemperature}
			if typ != "tuneablelight" {
				body["colourMode"] = "WHITE"
			}
			if len(bodies) == 0 {
				body["status"] = "ON"
			}
			bodies = append(bodies, body)
		}
		// One change per request, as the Hive app and pyhiveapi send them.
		var out any
		for i, b := range bodies {
			next, err := change(ctx, cl, p, b, lightSummary)
			if err != nil {
				if i == 0 {
					return nil, err
				}
				// The first change went through: say so, with the light's state.
				o := out.(Obj)
				o["done"] = "partly"
				o["not_sent"] = bodies[i:]
				return o, fmt.Errorf("%s took %v, but the next change failed: %w", hive.Name(p), bodies[0], err)
			}
			out = next
		}
		if len(bodies) > 1 {
			o := out.(Obj)
			o["sent"] = bodies
		}
		return out, nil
	})
}
