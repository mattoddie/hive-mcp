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
	On                *bool  `json:"on,omitempty" jsonschema:"true to switch on, false to switch off. Setting a brightness or colour temperature also switches the light on"`
	Brightness        *int   `json:"brightness,omitempty" jsonschema:"Brightness in percent, 5 to 100 (rounded to the nearest 5, as the Hive app does)"`
	ColourTemperature *int   `json:"colour_temperature,omitempty" jsonschema:"White colour temperature in kelvin, for tuneable and colour bulbs: about 2700 (warm) to 6500 (cool)"`
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
		out := Obj{}
		add := func(key string, types []string, f func(Obj) Obj) {
			if a.Kind != "" && a.Kind != key {
				return
			}
			items := []Obj{}
			for _, p := range n.Products(types...) {
				if a.Raw {
					items = append(items, p)
				} else {
					items = append(items, f(p))
				}
			}
			out[key] = items
		}
		add("plugs", hive.PlugTypes, n.SummarizePlug)
		add("lights", hive.LightTypes, n.SummarizeLight)
		add("sensors", hive.SensorTypes, n.SummarizeSensor)
		return out, nil
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
		Description: "Switch a Hive light on or off, and set its brightness or (for tuneable and colour bulbs) its white colour temperature.",
		Gate:        Write, Idempotent: true,
		Ranges: map[string][2]float64{"brightness": {5, 100}, "colour_temperature": {2700, 6535}},
	}, func(ctx context.Context, a setLightArgs) (any, error) {
		if a.On == nil && a.Brightness == nil && a.ColourTemperature == nil {
			return nil, errNoChange
		}
		if a.On != nil && !*a.On && (a.Brightness != nil || a.ColourTemperature != nil) {
			return nil, fmt.Errorf("a light that's being switched off can't also take a brightness or colour temperature")
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

		var bodies []Obj
		switch {
		case a.On != nil && !*a.On:
			bodies = append(bodies, Obj{"status": "OFF"})
		case a.Brightness != nil:
			if *a.Brightness < 5 || *a.Brightness > 100 {
				return nil, fmt.Errorf("brightness must be between 5 and 100")
			}
			b := int(math.Round(float64(*a.Brightness)/5) * 5)
			bodies = append(bodies, Obj{"status": "ON", "brightness": b})
		case a.On != nil:
			bodies = append(bodies, Obj{"status": "ON"})
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
		for _, b := range bodies {
			if out, err = change(ctx, cl, p, b, lightSummary); err != nil {
				return nil, err
			}
		}
		if len(bodies) > 1 {
			o := out.(Obj)
			o["sent"] = bodies
		}
		return out, nil
	})
}
