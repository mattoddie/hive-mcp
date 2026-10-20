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
			walk(p)
			walk(p.Items)
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
	tool := &mcp.Tool{Name: d.Name, Title: d.Title, Description: d.Description, Annotations: ann, InputSchema: schemaFor[In](d)}
	mcp.AddTool(c.Server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := h(ctx, in)
		if err != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Error: " + err.Error()}}, IsError: true}, nil, nil
		}
		return textResult(out), nil, nil
	})
	if c.Registered != nil {
		c.Registered[d.Name] = Info{Toolset: d.Toolset, Gate: d.Gate}
	}
}

// NoArgs is the input of tools without arguments.
type NoArgs struct{}
