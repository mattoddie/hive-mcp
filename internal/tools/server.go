package tools

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
)

// Name is the MCP server name.
const Name = "hive-mcp"

// Version is set at build time with -ldflags "-X github.com/mattoddie/hive-mcp/internal/tools.Version=1.2.3".
var Version = "0.0.0-dev"

func instructions(opts config.Tools) string {
	s := "Tools for Hive (British Gas's smart heating, unofficial): heating zones (Hive thermostats), radiator valves (TRVs), hot water, holiday mode, smart plugs, lights and sensors in the user's home. " +
		"Start with hive_get_overview for a snapshot. "
	if opts.AllowLoginTools {
		s += "If a tool says the server isn't logged in, tell the user to run `hive-mcp login`, or, if they ask you to, use hive_auth_start and hive_auth_complete (which texts them a code). "
	} else {
		s += "If a tool says the server isn't logged in, tell the user to run `hive-mcp login`. "
	}
	s += fmt.Sprintf("Temperatures are in °C; heating targets go from 5 to %g in steps of 0.5 (HIVE_MAX_TEMP). ", (&Context{Tools: opts}).maxTemp())
	s += "Holiday mode lasts at most 60 days. Times are in the home's local time. Readings can be up to HIVE_CACHE_SECONDS old; pass fresh: true when it matters. "
	if opts.AllowWrites {
		s += "Write tools are enabled and act on a real home: confirm changes with the user (which zone, temperature, mode and for how long) before making them, prefer a boost over manual mode or hot water on for a temporary change (manual and on last indefinitely and need confirm: true, which you should only pass once the user has agreed), and never turn heating off or set holiday mode without being asked."
	} else {
		s += "The server is read-only; tools that change the heating, hot water or devices are disabled (HIVE_ALLOW_WRITES=false)."
	}
	return s
}

// NewServer creates the MCP server with the tools and prompts allowed by opts.
// registered, when non-nil, is filled with each tool's toolset and gate.
func NewServer(client *hive.Client, opts config.Tools, registered map[string]Info) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: Version}, &mcp.ServerOptions{Instructions: instructions(opts)})
	c := &Context{Server: s, Client: client, Tools: opts, Registered: registered}
	registerHome(c)
	registerHeating(c)
	registerHotWater(c)
	registerDevices(c)
	registerRaw(c)
	registerPrompts(s)
	return s
}
