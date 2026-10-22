package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type prompt struct {
	name, title, description string
	args                     []*mcp.PromptArgument
	text                     func(args map[string]string) string
}

func or(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var prompts = []prompt{
	{
		name: "home_check", title: "Home check",
		description: "A quick check of the home: temperatures, what the heating and hot water are doing, holiday mode, and any low batteries or offline devices.",
		text: func(map[string]string) string {
			return `Check on my home with Hive.
1. hive_get_overview (fresh: true): heating zones, hot water, plugs, lights, sensors, holiday mode and anything needing attention.
2. If any radiator valves are listed, hive_list_trvs for their batteries and calibration.
Report: a one-line verdict, then each room's temperature against its target and what it's doing (schedule, manual, off or boost), the hot water, and a short to-do list (batteries to replace, offline devices, discontinued hardware). Keep healthy things to one line. Don't change anything.`
		},
	},
	{
		name: "warm_up", title: "Warm the house up",
		description: "Boost the heating to a comfortable temperature for a while, checking the current state first.",
		args: []*mcp.PromptArgument{
			{Name: "temperature", Description: "Temperature to boost to in °C (default 21)"},
			{Name: "minutes", Description: "How long for, in minutes (default 60)"},
			{Name: "zone", Description: "Which heating zone (default the main one)"},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`I'd like to warm the house up to %s °C for %s minutes (zone: %s).
1. hive_get_heating: check the current temperature, target and mode for that zone. If it's already at or above that temperature, or already boosting, tell me and ask what I want.
2. Show me what you'll do (zone, temperature, minutes, and what it goes back to afterwards) and wait for my go-ahead.
3. hive_boost_heating, then confirm the new state.`, or(a["temperature"], "21"), or(a["minutes"], "60"), or(a["zone"], "the main heating zone"))
		},
	},
	{
		name: "hot_water_now", title: "Hot water now",
		description: "Get hot water for a bath or shower: check whether it's already on and boost it if not.",
		args:        []*mcp.PromptArgument{{Name: "minutes", Description: "How long to heat the water for, in minutes (default 30)"}},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`I need hot water soon.
1. hive_get_hot_water: is it already heating, or due on shortly in its schedule?
2. If it's off and not due on within 15 minutes, boost it for %s minutes with hive_boost_hot_water. Otherwise tell me when it comes on.
3. Tell me what you did and when it will go back to normal.`, or(a["minutes"], "30"))
		},
	},
	{
		name: "going_away", title: "Going away",
		description: "Set the home up for a trip: holiday mode with a frost-safe temperature, and a check of anything left on.",
		args: []*mcp.PromptArgument{
			{Name: "leave", Description: "When you leave (YYYY-MM-DD or YYYY-MM-DDTHH:MM; default now)"},
			{Name: "return", Description: "When you're back (YYYY-MM-DD or YYYY-MM-DDTHH:MM)", Required: true},
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`I'm going away from %s until %s.
1. hive_get_overview: current heating and hot water, holiday mode, and plugs or lights that are on.
2. Suggest a holiday temperature (usually 7 to 12 °C to protect pipes; higher if there are pets or plants that need it) and a return time a couple of hours before I'm back, so the house is warm when I arrive.
3. Show me the plan, including any plugs or lights you'd switch off, and wait for my go-ahead.
4. hive_set_holiday_mode, then switch off what I agreed to with hive_set_plug or hive_set_light, and confirm.`, or(a["leave"], "now"), a["return"])
		},
	},
	{
		name: "schedule_review", title: "Review heating schedules",
		description: "Review the heating and hot water schedules for comfort and wasted energy, and suggest changes.",
		text: func(map[string]string) string {
			return `Review my heating and hot water schedules.
1. hive_get_heating_schedule for each heating zone and TRV zone (see hive_get_heating for the zones), and hive_get_hot_water_schedule.
2. Look for: heating on when people are usually out or asleep, very high or low targets, zones that heat at the same time as each other for no reason, long hot water periods, and weekdays and weekends that look copied without thought.
3. Summarise each schedule in a short table, then suggest specific changes with the energy saving or comfort reason for each.
Don't change anything: hive-mcp can't edit schedules, so I'll make any changes in the Hive app.`
		},
	},
}

func registerPrompts(s *mcp.Server) {
	for _, p := range prompts {
		p := p
		s.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.description, Arguments: p.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				args := map[string]string{}
				if req.Params != nil {
					args = req.Params.Arguments
				}
				return &mcp.GetPromptResult{
					Description: p.description,
					Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text(args)}}},
				}, nil
			})
	}
}

// PromptNames lists the prompts in the order they are registered.
func PromptNames() []string {
	names := make([]string, len(prompts))
	for i, p := range prompts {
		names[i] = p.name
	}
	return names
}
