# Development

This page is for people working on hive-mcp itself. Start with [CONTRIBUTING.md](../CONTRIBUTING.md) for the workflow, and use this page for how the code fits together.

- [Setup](#setup)
- [Project layout](#project-layout)
- [How a request flows](#how-a-request-flows)
- [How login works in the code](#how-login-works-in-the-code)
- [Adding a tool](#adding-a-tool)
- [Tool design guidelines](#tool-design-guidelines)
- [Testing](#testing)
- [Running against a real Hive account](#running-against-a-real-hive-account)
- [Docs that are generated](#docs-that-are-generated)
- [The website](#the-website)

## Setup

You need Go 1.25 or later, and Docker for building the image.

```sh
git clone https://github.com/mattoddie/hive-mcp.git
cd hive-mcp
make test           # runs all tests against a mock Hive
```

| Target | What it does |
|---|---|
| `make build` | Build a static binary to `bin/hive-mcp`. Pass `VERSION=1.2.3` to stamp a version. |
| `make test` | `go test ./...` |
| `make lint` | Fail if any file needs `gofmt`, then run `go vet ./...` |
| `make docs` | Regenerate [`docs/tools.md`](tools.md) from the tool definitions |
| `make docs-check` | Fail if `docs/tools.md` is out of date (CI runs this) |
| `make image` | Build the Docker image as `hive-mcp:$(VERSION)` |

The project deliberately has only two direct dependencies: the official MCP Go SDK, [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk), and [`github.com/google/jsonschema-go`](https://github.com/google/jsonschema-go), which the SDK uses for input schemas. Everything else, including the Cognito SRP login, is the standard library. Please discuss before adding a dependency.

## Project layout

```text
cmd/
├── hive-mcp/            # the binary: serve, login, logout, status, healthcheck and version commands
└── gen-tool-docs/       # writes docs/tools.md from the registered tools and prompts
internal/
├── config/              # environment variables → typed Config (validation lives here)
├── hive/
│   ├── srp.go           # Cognito's SRP-6a: user and device password claims, device verifiers
│   ├── cognito.go       # the few Cognito calls the login needs, as plain JSON over HTTP; reading Hive's sign-in page
│   ├── auth.go          # login with SMS code, remembered device, refresh, device re-login, the token file
│   ├── client.go        # Client: ID token auth, refresh on 401/403, retries on 429/503, the /nodes/all cache
│   ├── format.go        # helpers to read Hive's objects, schedules, and compact summaries
│   ├── testdata/        # SRP test vectors generated with pyhiveapi's own code
│   └── hivetest/        # an in-process fake Hive: sign-in page, Cognito (with real SRP checks) and the API
├── httpserver/          # stateless Streamable HTTP, bearer auth, Host allow-list, /health
└── tools/
    ├── tools.go         # the registration framework: Def, Gate, add[In](), schemas with enums and ranges
    ├── server.go        # NewServer(): server instructions, registers every toolset and the prompts
    ├── helpers.go       # finding zones by name, temperature validation, dates, sending a change
    ├── prompts.go       # the built-in MCP prompts
    └── <toolset>.go     # one file per toolset: home, heating, hotwater, devices, raw
```

## How a request flows

1. An MCP client calls a tool, for example `hive_boost_heating {zone: "Heating", temperature: 21, minutes: 60}`.
2. The MCP SDK validates the arguments against the tool's JSON Schema, which was inferred from its Go input struct (with enums and minimum/maximum from the `Def`), and decodes them into that struct.
3. The handler registered by `add()` runs. It validates further (steps of 0.5 °C), reads the home with `Client.Nodes()`, which may come from the short cache, and finds the zone by name.
4. `change()` sends `POST /nodes/heating/<id>` with `{"mode":"BOOST","boost":60,"target":21}`, which clears the cache, then reads the home again so the result shows the new state.
5. `hive.Client` gets an ID token from `Auth` (refreshing it first if it's about to expire), sends it as the bare `Authorization` header, and unwraps the JSON. On `401` or `403` it refreshes once and retries. On `429`, `502` or `503` it waits and retries up to three times. Other failures become a `hive.Error` with Hive's message and a hint.
6. `add()` serialises the value to indented JSON text. A returned error becomes `isError: true` with the message, so the model can see what went wrong and adjust.

## How login works in the code

Hive logs in through AWS Cognito. hive-mcp talks to Cognito directly with JSON over HTTP (`X-Amz-Target: AWSCognitoIdentityProviderService.<Action>`), because every call it needs is unauthenticated or takes the user's access token in the body: no AWS SDK or AWS credentials are involved.

- **`srp.go`** implements the client side of Cognito's SRP-6a exactly as amazon-cognito-identity-js and pyhiveapi do: the 3072-bit group, `k = H(N, g)`, `u = H(A, B)`, `x = H(salt, H(<pool name><user>:<password>))`, HKDF with "Caldera Derived Key", and the HMAC claim over `<pool name><user><secret block><timestamp>`. Device authentication uses the same maths with the device group key, device key and device password. `testdata/srp_vectors.json` holds values computed with pyhiveapi's own helpers, and `srp_test.go` checks this code produces identical signatures and verifiers.
- **`auth.go`** runs the flows:
  - `StartLogin`: `InitiateAuth` (`USER_SRP_AUTH`, with `DEVICE_KEY` if there's a remembered device), then `PASSWORD_VERIFIER`. Cognito answers with tokens, `DEVICE_SRP_AUTH` (a remembered device: `srpLogin` continues with `DEVICE_SRP_AUTH` and `DEVICE_PASSWORD_VERIFIER`) or `SMS_MFA` (the session is kept for `CompleteLogin`).
  - `CompleteLogin`: answers `SMS_MFA`. When the result offers `NewDeviceMetadata`, `registerDevice` makes a random device password and calls `ConfirmDevice` (with its verifier and salt) and `UpdateDeviceStatus` (`remembered`).
  - `Refresh`: `REFRESH_TOKEN_AUTH` with the device key; if Cognito refuses the refresh token, it's dropped and `relogin` logs in as the remembered device. `relogin` never starts a login that would send an SMS, and backs off for five minutes after a failure.
  - Everything is saved to the token file (atomically, mode `0600`) before the new ID token is handed out, and a running server reloads the file when another process, such as `hive-mcp login`, changes it.

## Adding a tool

Tools are defined with the generic `add[In]()` in the file for their toolset. Here's a complete example from `devices.go`:

```go
type setPlugArgs struct {
	Plug string `json:"plug,omitempty" jsonschema:"Smart plug, by name or ID (see hive_list_smart_devices). Can be left out when there's only one"`
	On   bool   `json:"on" jsonschema:"true to switch the plug on, false to switch it off"`
}

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
```

### The input struct

The input struct **is** the tool's argument schema:

- The `json` tag names the argument. Use `snake_case`.
- `omitempty` makes it optional. Without it, the argument is required.
- The `jsonschema` tag is the argument's description, which the model reads. Every argument needs one, and a test checks this.
- Use pointers (`*int`, `*bool`, `*float64`) for optional values where zero means something, so "not given" and "set to zero" differ.
- Embedded structs, such as `rawArgs`, `freshArgs` or `zoneArgs`, add their fields to the tool.
- Tools without arguments take `NoArgs`.

### The `Def`

| Field | Effect |
|---|---|
| `Toolset` | Required. The tool is only registered when this toolset is enabled. |
| `Name`, `Title`, `Description` | The tool's name (`hive_<verb>_<thing>`), a short human title, and the description the model reads. |
| `Gate` | `Read` (the default, always registered), `Write` (needs `HIVE_ALLOW_WRITES`) or `Login` (needs `HIVE_ALLOW_LOGIN_TOOLS`; only for tools that log in with the password). |
| `ReadOnly` | Overrides the `readOnlyHint` annotation, which defaults to `Gate == Read`. |
| `Destructive` | Sets the `destructiveHint` annotation for tools that aren't read-only. |
| `Idempotent` | Repeating the call with the same arguments has no further effect. Sets `idempotentHint`. |
| `OpenWorld` | The tool reaches outside Hive's API, such as sending a text message. Sets `openWorldHint`. |
| `Enums` | Restricts string arguments to fixed values, keyed by argument name. |
| `Ranges` | Sets the minimum and maximum of number arguments, keyed by argument name. Validate in the handler too. |

### Helpers

| Helper | Use it for |
|---|---|
| `c.nodes(ctx, fresh)` | The whole home, from the cache unless `fresh`. |
| `n.Products(types...)`, `n.Devices(types...)`, `n.Device(id)` | Picking products (what you control) and devices (the hardware) out of a `hive.Nodes`. |
| `find(list, ref, what)` | Resolve a name or ID, with errors that list the choices. |
| `checkTemperature()` | 5–32 °C in steps of 0.5. |
| `parseWhen()` | Dates and times in the home's time zone. |
| `change()` | Send a state change and return what was sent and the new state. |
| `n.Summarize*()`, `n.ScheduleNow()`, `hive.FormatSchedule()` | Compact summaries and readable schedules. |

### Checklist for a new tool

1. Put it in the right `internal/tools/<toolset>.go`. If it needs a new toolset, add it to `Toolsets` in `internal/config/config.go`, describe it in `toolsetInfo` in `cmd/gen-tool-docs/main.go`, and register it in `NewServer()`.
2. Set its `Gate` correctly: anything that changes the home is `Write`.
3. Validate every value before it reaches Hive, and say what's allowed in the description.
4. Return a **compact summary** and offer `raw: true` for the full object. Never return tokens or secrets.
5. Write descriptions for the model: what the tool does, when to use it, units, and side effects.
6. Add tests in `internal/tools/<toolset>_test.go` using the mock.
7. Run `make docs` and commit the updated `docs/tools.md`. If it's a notable feature, update the README table and [CHANGELOG](../CHANGELOG.md).

## Tool design guidelines

- **Fewer, broader tools beat many narrow ones.** One light tool with optional brightness and colour temperature is easier for a model than a tool per setting.
- **Never surprise.** Changes are explicit and validated. Boosts are preferred to permanent changes. Nothing sends a text message unless the user asked to log in.
- **Be gentle on Hive.** Reads go through the cache; only use `fresh` where it matters, such as cancelling a boost.
- **Errors should teach.** Return messages that tell the model, or the user, how to fix the call, such as the list of zone names.
- **Mirror the Hive app.** Hive's API is undocumented, so send what the app and pyhiveapi send, one change per request.

## Testing

Tests use Go's `testing` package against `hivetest.Mock` (`internal/hive/hivetest`), an `httptest` server that imitates:

- **Hive's sign-in page** at `/sso/`, naming a test Cognito pool and client.
- **Cognito** at `/cognito/`: `InitiateAuth` (`USER_SRP_AUTH`, `REFRESH_TOKEN_AUTH`), `RespondToAuthChallenge` (`PASSWORD_VERIFIER`, `SMS_MFA`, `DEVICE_SRP_AUTH`, `DEVICE_PASSWORD_VERIFIER`), `ConfirmDevice`, `UpdateDeviceStatus` and `ForgetDevice`. It computes the server side of SRP independently (`S = (A·v^u)^b`) and checks every signature, so a mistake in the client's maths fails the tests. The test account is `hivetest.Username` / `hivetest.Password`, and the SMS code is `hivetest.SMSCode`. Switches such as `MFA` and `DeviceTracking`, and `ExpireTokens()`, `ExpireRefreshTokens()` and `ForgetDevices()`, set up each login path. `SMSSent` and `Cognito` record what happened.
- **The API** at `/api`, with a small home (`hivetest.SampleNodes()`): a heating zone and thermostat, hot water, a TRV zone with a low-battery valve, a plug, two bulbs, a door sensor and a discontinued keypad. `POST /nodes/<type>/<id>` changes its state much as Hive does (boosts remember the previous mode), and `/holiday-mode` works too. `On(method, pattern, handler)` overrides a route, and `Requests()`, `Last(method)` and `Count()` show what was sent.

`newEnv(t)` in `internal/tools/helpers_test.go` starts the mock, a client already logged in to it and an in-memory MCP session with **every** tool enabled. `env.obj()` runs a tool and decodes its JSON (failing on a tool error), `env.callErr()` returns a tool's error text, and `env.lastBody()` shows the last change sent. A typical test:

```go
func TestBoostHeating(t *testing.T) {
	env := newEnv(t)
	env.obj("hive_boost_heating", map[string]any{"zone": "Heating", "temperature": 22, "minutes": 30})
	if got := env.lastBody(); !strings.HasSuffix(got, `{"boost":30,"mode":"BOOST","target":22}`) {
		t.Errorf("sent %s", got)
	}
}
```

To test that a tool is gated, pass different options: `newEnv(t, opts)` or `connect(t, env.client, opts)` with a `config.Tools`, and check the tool isn't listed with `toolNames()`. `TestGatesControlWhichToolsExist` already checks every tool against its `Gate`, and `TestSchemasDescribeEveryArgument` checks that every argument has a description.

Run one package or test with `go test ./internal/tools -run TestBoostHeating -v`.

## Running against a real Hive account

There's no Hive sandbox, so anything you try acts on a real home. Keep writes off until you're sure, and use a boost (which ends by itself) rather than changes that stick.

With the [MCP Inspector](https://github.com/modelcontextprotocol/inspector):

```sh
make build
export HIVE_USERNAME=you@example.com HIVE_PASSWORD=... HIVE_TOKEN_FILE=$PWD/tokens.json
./bin/hive-mcp login
npx @modelcontextprotocol/inspector ./bin/hive-mcp
```

Or build the image and point Claude Code at it:

```sh
make image
docker run --rm -it -v hive-dev:/data --env-file .env hive-mcp:0.0.0-dev login
docker run --rm -p 8080:8080 -v hive-dev:/data --env-file .env hive-mcp:0.0.0-dev
claude mcp add --transport http hive-dev http://localhost:8080/mcp --header "Authorization: Bearer $MCP_AUTH_TOKEN"
```

`tokens.json` and `.env` are in `.gitignore`. Keep it that way, and never paste real tokens, passwords, device IDs or addresses into issues, tests or fixtures. If you capture a real `/nodes/all` response to improve the mock, anonymise it first.

## Docs that are generated

[`docs/tools.md`](tools.md) is generated by `cmd/gen-tool-docs`. The program builds the server with every tool enabled, records each tool's toolset and gate as it's registered, reads the tools and prompts back over MCP, and writes the reference. Don't edit `docs/tools.md` by hand. Change the tool's title, description or `jsonschema` tags and run `make docs`. CI runs `make docs-check` and fails if the committed file is stale.

## The website

The site at [hive-mcp.mattoddie.dev](https://hive-mcp.mattoddie.dev) lives in `website/`. It has a hand-built landing page plus these docs rendered with Astro Starlight. It has its own `package.json`, so its dependencies never reach the server or the Docker image.

The Markdown in `docs/` is the single source: the site copies it in at build time. It turns each page's first `# Heading` into the page title, drops hand-written "Contents" lists, rewrites links between `.md` files to site URLs and converts GitHub alerts (`> [!NOTE]`) to Starlight asides. So write docs as normal GitHub Markdown, start each page with a `# Title`, and link between pages with relative `.md` links.

To preview the site locally:

```sh
cd website
npm ci
npm run dev
```
