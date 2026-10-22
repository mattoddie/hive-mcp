package tools

// The audit log (HIVE_AUDIT_LOG): one JSON line for every call of a tool that
// changes something in the home or the login.

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/hive-mcp/internal/config"
)

// auditEntry is one line of the audit log.
type auditEntry struct {
	Time      string `json:"time"`
	Tool      string `json:"tool"`
	Arguments any    `json:"arguments"`
	Outcome   string `json:"outcome"`
	Error     string `json:"error,omitempty"`
}

// auditLog writes one JSON line per call of a tool that changes things.
type auditLog struct {
	target string
	stderr io.Writer
	mu     sync.Mutex
	warned sync.Once
}

func newAuditLog(target string) *auditLog {
	if target == "" {
		return nil
	}
	return &auditLog{target: target, stderr: os.Stderr}
}

// middleware records the calls of the tools in audited whose predicate says
// the call changes something. It sits in front of the SDK's own handling, so
// calls refused for invalid arguments are recorded too.
func (l *auditLog) middleware(audited map[string]func(json.RawMessage) bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || call.Params == nil {
				return next(ctx, method, req)
			}
			changes := audited[call.Params.Name]
			if changes == nil || !changes(call.Params.Arguments) {
				return next(ctx, method, req)
			}
			res, err := next(ctx, method, req)
			l.record(call.Params.Name, call.Params.Arguments, res, err)
			return res, err
		}
	}
}

func (l *auditLog) record(tool string, raw json.RawMessage, res mcp.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			l.warn(r)
		}
	}()
	e := auditEntry{
		Time:      time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Tool:      tool,
		Arguments: map[string]any{},
		Outcome:   "ok",
	}
	if len(raw) > 0 {
		var args any
		if json.Unmarshal(raw, &args) == nil {
			e.Arguments = redactSecrets(args)
		} else {
			e.Arguments = "[not valid JSON]"
		}
	}
	switch r, _ := res.(*mcp.CallToolResult); {
	case err != nil:
		e.Outcome, e.Error = "error", err.Error()
	case r != nil && r.IsError:
		e.Outcome = "error"
		if len(r.Content) > 0 {
			if t, ok := r.Content[0].(*mcp.TextContent); ok {
				e.Error = strings.TrimPrefix(t.Text, "Error: ")
			}
		}
	}
	line, merr := json.Marshal(e)
	if merr != nil {
		l.warn(merr)
		return
	}
	l.write(string(line) + "\n")
}

func (l *auditLog) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.target == config.AuditLogStderr {
		_, err = io.WriteString(l.stderr, line)
	} else {
		var f *os.File
		// Opened for each entry, so a rotated or deleted log is recreated.
		if f, err = config.OpenAuditLog(l.target); err == nil {
			_, err = f.WriteString(line)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
	}
	if err != nil {
		l.warn(err)
	}
}

// warn reports the first audit log failure; tool calls carry on regardless.
func (l *auditLog) warn(err any) {
	l.warned.Do(func() {
		log.Printf("hive-mcp: WARNING: could not write the audit log (%s): %v. Tool calls carry on; this warning is not repeated.", l.target, err)
	})
}

// redactSecrets copies decoded JSON arguments with passwords, tokens, secrets
// and the SMS code (hive_auth_complete's code) replaced by "[redacted]".
func redactSecrets(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if secretKey(k) {
				out[k] = "[redacted]"
			} else {
				out[k] = redactSecrets(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redactSecrets(val)
		}
		return out
	}
	return v
}

func secretKey(k string) bool {
	k = strings.ToLower(k)
	if k == "code" {
		return true
	}
	for _, s := range []string{"password", "secret", "token", "apikey", "api_key"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// rawRequestChanges says whether a hive_api_request call changes something:
// any method but GET. Arguments that can't be read count as a change.
func rawRequestChanges(raw json.RawMessage) bool {
	var a struct {
		Method string `json:"method"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &a) != nil {
		return true
	}
	m := strings.ToUpper(strings.TrimSpace(a.Method))
	return m != "" && m != "GET"
}
