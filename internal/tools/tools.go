// Package tools defines hive-mcp's MCP tools and prompts.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
	"github.com/mattoddie/hive-mcp/internal/hive"
)

// Obj is a JSON object from the API.
type Obj = hive.Obj

// Gate is the setting a tool needs before it is registered.
type Gate int

const (
	Read  Gate = iota // always registered
	Write             // HIVE_ALLOW_WRITES
	Login             // HIVE_ALLOW_LOGIN_TOOLS: logs in with the password and can text an SMS code
)

func (g Gate) String() string {
	return [...]string{"read", "write", "login"}[g]
}

// Context is shared by the tool definitions.
type Context struct {
	Server *mcp.Server
	Client *hive.Client
	config.Tools
	// Registered records each registered tool's toolset and gate, for the docs generator.
	Registered map[string]Info
	// audited maps each tool whose calls can change things to a test of
	// whether a call does, for the audit log.
	audited map[string]func(json.RawMessage) bool
	// tools records the registered tools, so prompts are only offered when
	// the tools they use are.
	tools map[string]bool
}

// Info describes a registered tool.
type Info struct {
	Toolset string
	Gate    Gate
}

// Def describes a tool.
type Def struct {
	Toolset     string
	Name        string
	Title       string
	Description string
	Gate        Gate
	// ReadOnly overrides the readOnlyHint annotation (defaults to Gate == Read).
	ReadOnly *bool
	// Destructive sets the destructiveHint annotation for tools that aren't read-only (default false).
	Destructive bool
	Idempotent  bool
	// OpenWorld marks tools that reach outside Hive's API, such as sending an SMS.
	OpenWorld bool
	// Enums restricts string arguments to fixed values, keyed by argument name.
	Enums map[string][]string
	// Ranges sets the minimum and maximum of number arguments, keyed by argument name.
	Ranges map[string][2]float64
	// Keys restricts the keys of object (map) arguments, keyed by argument name.
	Keys map[string][]string
	// Changes, for a Read tool that can still change things (the raw tool),
	// says whether a call with these arguments does, so the audit log records
	// it. Calls of Write and Login tools are always recorded.
	Changes func(args json.RawMessage) bool
}

func boolPtr(b bool) *bool        { return &b }
func floatPtr(f float64) *float64 { return &f }

// schemaFor infers the input schema from In: descriptions come from
// `jsonschema` tags, optional fields have `omitempty`, and Enums and Ranges
// become enum lists and minimum/maximum.
func schemaFor[In any](d Def) *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: %v", d.Name, err))
	}
	var walk func(*jsonschema.Schema)
	walk = func(s *jsonschema.Schema) {
		if s == nil {
			return
		}
		// Optional pointer and slice fields infer as ["null", T]; plain T reads better, and omitting the argument is how to leave it out.
		if len(s.Types) == 2 && contains(s.Types, "null") {
			for _, t := range s.Types {
				if t != "null" {
					s.Type, s.Types = t, nil
				}
			}
		}
		for name, p := range s.Properties {
			if values, ok := d.Enums[name]; ok {
				p.Enum = nil
				for _, v := range values {
					p.Enum = append(p.Enum, v)
				}
			}
			if r, ok := d.Ranges[name]; ok {
				p.Minimum, p.Maximum = floatPtr(r[0]), floatPtr(r[1])
			}
			if keys, ok := d.Keys[name]; ok {
				names := &jsonschema.Schema{Type: "string"}
				for _, k := range keys {
					names.Enum = append(names.Enum, k)
				}
				p.PropertyNames = names
			}
			walk(p)
			walk(p.Items)
			walk(p.AdditionalProperties)
		}
	}
	walk(s)
	return s
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// textResult renders a handler's value as indented JSON.
func textResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b = []byte(fmt.Sprintf("%v", v))
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// add registers a tool if its toolset and gate are enabled.
func add[In any](c *Context, d Def, h func(ctx context.Context, in In) (any, error)) {
	if !c.Toolsets[d.Toolset] {
		return
	}
	if d.Gate == Write && !c.AllowWrites {
		return
	}
	if d.Gate == Login && !c.AllowLoginTools {
		return
	}
	readOnly := d.Gate == Read
	if d.ReadOnly != nil {
		readOnly = *d.ReadOnly
	}
	ann := &mcp.ToolAnnotations{Title: d.Title, ReadOnlyHint: readOnly, IdempotentHint: d.Idempotent, OpenWorldHint: boolPtr(d.OpenWorld)}
	if !readOnly {
		ann.DestructiveHint = boolPtr(d.Destructive)
	}
	schema := schemaFor[In](d)
	// Tools that can change things preview the change with dry_run.
	canDryRun := d.Gate == Write || (d.Changes != nil && c.AllowWrites)
	if canDryRun {
		if schema.Properties == nil {
			schema.Properties = map[string]*jsonschema.Schema{}
		}
		schema.Properties["dry_run"] = &jsonschema.Schema{Type: "boolean", Description: dryRunDescription}
	}
	tool := &mcp.Tool{Name: d.Name, Title: d.Title, Description: d.Description, Annotations: ann, InputSchema: schema}
	mcp.AddTool(c.Server, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		var dry *hive.DryRun
		if canDryRun && dryRunRequested(req) {
			ctx, dry = hive.WithDryRun(ctx)
		}
		out, err := h(ctx, in)
		if dry != nil && err == nil {
			return textResult(dryRunResult(dry.Requests(), out)), nil, nil
		}
		if err != nil {
			// A handler that fails part way returns what it did as well, so the
			// model knows what changed.
			res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}}, IsError: true}
			if out != nil {
				res.Content = append(res.Content, textResult(out).Content...)
			}
			return res, nil, nil
		}
		return textResult(out), nil, nil
	})
	c.tools[d.Name] = true
	if c.Registered != nil {
		c.Registered[d.Name] = Info{Toolset: d.Toolset, Gate: d.Gate}
	}
	switch {
	case d.Gate != Read:
		c.audited[d.Name] = func(json.RawMessage) bool { return true }
	case d.Changes != nil:
		c.audited[d.Name] = d.Changes
	}
}

const dryRunDescription = "Preview only: check the call and return the exact requests it would send to Hive, without sending them. Nothing is changed"

// dryRunRequested reports whether a call passed dry_run: true.
func dryRunRequested(req *mcp.CallToolRequest) bool {
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return false
	}
	var a struct {
		DryRun bool `json:"dry_run"`
	}
	return json.Unmarshal(req.Params.Arguments, &a) == nil && a.DryRun
}

// dryRunResult reports the requests a dry run stopped. A tool that would
// have sent nothing returns what it would have returned instead.
func dryRunResult(reqs []hive.PlannedRequest, out any) Obj {
	res := Obj{"dry_run": true, "would_send": reqs}
	if len(reqs) == 0 {
		res["result"] = out
		res["note"] = "Nothing was changed: this was a dry run, and the call wouldn't send any change to Hive."
		return res
	}
	res["note"] = "Nothing was changed: this was a dry run. would_send lists the requests, in order, that the call would send to Hive. Call again without dry_run to make the change."
	return res
}

// NoArgs is the input of tools without arguments.
type NoArgs struct{}
