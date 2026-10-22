# Configuration

hive-mcp is configured entirely through environment variables. With Docker Compose, put them in `.env`. Copy [`example.env`](../example.env) to get started.

- [Hive account and login](#hive-account-and-login)
- [How login works](#how-login-works)
- [What the assistant may do](#what-the-assistant-may-do)
- [Audit log](#audit-log)
- [Caching and timeouts](#caching-and-timeouts)
- [MCP transport](#mcp-transport)
- [Docker secrets (`_FILE` variables)](#docker-secrets-_file-variables)
- [Choosing toolsets](#choosing-toolsets)
- [Keeping the tool list small](#keeping-the-tool-list-small)
- [Advanced: Hive's endpoints](#advanced-hives-endpoints)
- [Example configurations](#example-configurations)

The server checks the configuration at startup. If something is wrong, it exits with a clear message such as `HIVE_ALLOW_WRITES must be true or false, got "maybe"`. If the container keeps restarting, check `docker compose logs`. The server starts without a login, though: tools then explain how to log in.

## Hive account and login

| Variable | Default | Description |
|---|---|---|
| `HIVE_USERNAME` | **required to log in** | The email address you log in to the Hive app with. It's matched without regard to case. |
| `HIVE_PASSWORD` | **required to log in** | Your Hive password. `hive-mcp login` needs it, and the server needs it later to log in again by itself (as a remembered device, so without an SMS code) when its refresh token runs out. Use `HIVE_PASSWORD_FILE` with Docker secrets where you can. |
| `HIVE_TOKEN_FILE` | `~/.config/hive-mcp/tokens.json` (`/data/tokens.json` in the image) | Where the login is saved: tokens, and the remembered device's key and secret password. It must be writable and must survive restarts. In Docker, keep a volume mounted at `/data`. If `XDG_CONFIG_HOME` is set, the default is `$XDG_CONFIG_HOME/hive-mcp/tokens.json`. |
| `HIVE_DEVICE_NAME` | `hive-mcp` | The name this server is registered under as a remembered device on your Hive account. |

## How login works

Hive logs in through **AWS Cognito**, Amazon's sign-in service, with the **SRP** protocol: the app proves it knows your password without ever sending it. Hive accounts also have **two-step verification**, so a new login needs a code sent by text message. hive-mcp does exactly what the Hive app does:

1. **`hive-mcp login`** (once) reads Hive's sign-in page to find the Cognito pool, then logs in with your username and password. Hive texts a code to your phone, and you type it in.
2. Cognito then offers to **remember this device**. hive-mcp accepts: it makes up a long random device password, registers it with Cognito (only a verifier derived from it is sent, never the password itself), and saves the device's key and password in the token file.
3. The login gives an **ID token** (valid for an hour), which Hive's API takes, and a **refresh token**. hive-mcp refreshes the ID token before it runs out.
4. When the refresh token itself runs out, the server **logs in again as the remembered device**: username and password, then a second SRP exchange proving it holds the device password. Hive doesn't send a code for a remembered device, so nobody needs to be at the keyboard.

You do step 1. Everything else is automatic. There are two ways to do step 1:

| Method | How | Good for |
|---|---|---|
| `hive-mcp login` in a terminal | `docker compose run --rm hive-mcp login`, or `docker run -it --rm --env-file .env -v hive-data:/data ghcr.io/mattoddie/hive-mcp:latest login`, or `hive-mcp login` with the binary. It asks for the code. | The compose setup and most others. |
| From the assistant | Set `HIVE_ALLOW_LOGIN_TOOLS=true` and restart, then ask it to *"log in to Hive"*. It calls `hive_auth_start` (Hive texts you), you tell it the code, and it calls `hive_auth_complete`. | Logging in without a terminal, for example with Claude Desktop in stdio mode. |

Running `login` again later doesn't send a code if the server is still a remembered device. It just logs in afresh.

> [!NOTE]
> A running server notices when the token file changes, so you can log in again with the `login` command without restarting it.

### What the server will and won't do on its own

- It **never triggers an SMS code by itself**. If it would need one (for example, because the remembered device was removed from your account), it stops and tools tell you to run `hive-mcp login` again.
- After a failed automatic login it **waits five minutes** before trying again, so a wrong password can't lock your account through repeated attempts.
- The login tools (`HIVE_ALLOW_LOGIN_TOOLS`) won't ask Hive for another SMS code within a minute of the last one, or try again within five minutes of a login Hive refused, however often the assistant calls them. Five wrong codes end a pending login.
- Without `HIVE_PASSWORD`, the server works until the refresh token runs out, then needs a new `hive-mcp login`.

### Logging out

`hive-mcp logout` asks Hive to forget this server's remembered device and to revoke its refresh token (renewing an expired login first so it can), then deletes the token file. A running server notices the file has gone and drops the login it holds in memory too, so it can't log itself back in or write the file again. If you change your Hive password, update `HIVE_PASSWORD` too, or the server can't log in again by itself. If it then asks for a new login, run `hive-mcp login` again.

## What the assistant may do

| Variable | Default | Description |
|---|---|---|
| `HIVE_ALLOW_WRITES` | `false` | Register tools that change things: heating temperature and mode, boosts, hot water, holiday mode, plugs and lights, and non-GET requests in the raw API tool. |
| `HIVE_ALLOW_LOGIN_TOOLS` | `false` | Register `hive_auth_start` and `hive_auth_complete`, so the assistant can log in for you. They log in with `HIVE_PASSWORD` and can make Hive text an SMS code, so they're off unless you ask for them. `hive_auth_status` is always available. |
| `HIVE_MAX_TEMP` | `25` | The highest heating target, in °C, that any tool will set: temperature, manual mode, boosts and holiday mode. 5 to 32 in steps of 0.5 (Hive itself allows up to 32). |
| `HIVE_TOOLSETS` | `all` | Comma-separated list of [toolsets](#choosing-toolsets) to expose. `all` means every toolset except `raw`, which has to be named, for example `all,raw`. |

Boolean variables accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. Anything else stops the server with an error.

A variable set to an empty value counts as unset and gets its default. So does one set to exactly `${user_config.<key>}`, which is how Claude Desktop passes an [extension setting](clients.md#claude-desktop) that was left empty.

The [tool reference](tools.md) labels every tool with the setting it needs:

| Label | Registered when |
|---|---|
| 🟢 read | Always |
| 🟠 write | `HIVE_ALLOW_WRITES=true` |
| 🔵 login | `HIVE_ALLOW_LOGIN_TOOLS=true` |

Tools that aren't allowed are **not registered at all**. The assistant can't see them, so it can't call them or be talked into calling them. After changing `HIVE_ALLOW_WRITES` or `HIVE_ALLOW_LOGIN_TOOLS`, restart the server and reconnect your MCP client so it fetches the new tool list.

Unlike an OAuth service, Hive's login has no read-only scope: whatever the switch says, the login itself can do anything the Hive app can. The switch is what keeps the assistant read-only. See [Security](security.md).

## Audit log

| Variable | Default | Description |
|---|---|---|
| `HIVE_AUDIT_LOG` | (off) | Record every call of a tool that changes things. A file path appends JSON Lines to that file, which is created with mode `0600` if it doesn't exist. `stderr` writes the lines to standard error, next to the server's other logs. |

Each line is one call of a write tool (🟠 in the [tool reference](tools.md)), a login tool (`hive_auth_start` or `hive_auth_complete`), or `hive_api_request` with a method other than `GET`. Calls that failed, and calls refused for invalid arguments, are logged too. Read tools aren't.

```json
{"time":"2026-10-22T08:14:03.120Z","tool":"hive_boost_heating","arguments":{"minutes":60,"temperature":21,"zone":"Living room"},"outcome":"ok"}
```

| Field | Meaning |
|---|---|
| `time` | When the call finished, in UTC (RFC 3339). |
| `tool` | The tool name, such as `hive_boost_heating`. |
| `arguments` | The arguments as the assistant sent them, with the SMS code, passwords, tokens and secrets `[redacted]`. |
| `outcome` | `ok` or `error`. |
| `error` | The error message, when `outcome` is `error`. |

The server checks at startup that it can write to the file, and refuses to start if the directory doesn't exist or isn't writable. A write that fails later doesn't stop the tool call: the server logs one warning to standard error and carries on. The file is opened for each entry, so you can rotate or delete it while the server runs.

In Docker, put the file on a volume:

```yaml
services:
  hive-mcp:
    environment:
      HIVE_AUDIT_LOG: /audit/hive-mcp.jsonl
    volumes:
      - ./audit:/audit
```

Create the directory first and let UID 65532 write to it, for example `mkdir audit && sudo chown 65532 audit`. `HIVE_AUDIT_LOG=stderr` needs no volume: the lines appear in `docker compose logs`.

## Caching and timeouts

| Variable | Default | Description |
|---|---|---|
| `HIVE_CACHE_SECONDS` | `30` | How long a reading of the whole home is reused before asking Hive again. A conversation often makes several calls in a row, and this keeps them to one request. Every change clears the cache, and tools take `fresh: true` to skip it. `0` turns the cache off. |
| `HIVE_TIMEOUT_MS` | `30000` | Timeout for each request to Hive, in milliseconds. |

hive-mcp also retries a few times, with increasing waits, when Hive answers `429 Too Many Requests`, and for reads also `502` or `503`. A change (a `POST`) is never repeated after a `502` or `503`, since Hive may already have made it. Only a `401` makes it refresh the login. It never follows redirects, and refuses responses larger than 64 MiB.

## MCP transport

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `http` in the Docker image (`stdio` for the binary) | `http` for Streamable HTTP, or `stdio`. |
| `MCP_HTTP_HOST` | `127.0.0.1` (`0.0.0.0` in the Docker image) | Address to listen on (HTTP only). |
| `MCP_HTTP_PORT` | `8080` | Port to listen on (HTTP only). |
| `MCP_AUTH_TOKEN` | (required for http) | Every request to `/mcp` must send `Authorization: Bearer <token>`. The server refuses to start in http mode without it, or if it's shorter than 16 characters. Generate one with `openssl rand -hex 32`. |
| `MCP_ALLOW_UNAUTHENTICATED` | `false` | Start the http transport without `MCP_AUTH_TOKEN`. Only for a server that sits behind something else that authenticates every request. It also needs `MCP_ALLOWED_HOSTS`, and the server logs a warning at startup. |
| `MCP_ALLOWED_HOSTS` | | Comma-separated allow-list of `host` or `host:port` entries, such as `hive-mcp.lan,hive-mcp.lan:8080`. Requests whose `Host` header isn't listed are rejected (an entry without a port matches any port), and so are requests from browser pages whose `Origin` isn't listed (an entry without a port matches only the default port, 80 or 443; list `host:port` for a page on another port). `Origin: null` or an empty `Origin` is always rejected. This protects against DNS-rebinding and cross-site attacks from web pages in your browser. |

The HTTP server exposes these endpoints:

| Path | Method | Purpose |
|---|---|---|
| `/mcp` | `POST` | The MCP endpoint (stateless Streamable HTTP, JSON responses). Other methods get `405`. Bodies must be JSON and at most 1 MiB. |
| `/health`, `/healthz` | `GET` | Liveness check. Returns `{"status":"ok"}` and doesn't contact Hive. |

## Docker secrets (`_FILE` variables)

Every variable can also be read from a file: set `<NAME>_FILE` to the file's path. Leading and trailing whitespace is trimmed. This works with [Docker secrets](https://docs.docker.com/compose/how-tos/use-secrets/) and Kubernetes secret volumes:

```yaml
services:
  hive-mcp:
    image: ghcr.io/mattoddie/hive-mcp:latest
    environment:
      HIVE_USERNAME: you@example.com
      HIVE_PASSWORD_FILE: /run/secrets/hive_password
      MCP_AUTH_TOKEN_FILE: /run/secrets/mcp_auth_token
    secrets: [hive_password, mcp_auth_token]
    volumes: [hive-data:/data]
    ports: ["8080:8080"]

secrets:
  hive_password:
    file: ./secrets/hive_password.txt
  mcp_auth_token:
    file: ./secrets/mcp_auth_token.txt

volumes:
  hive-data:
```

If both `NAME` and `NAME_FILE` are set, the file wins. The tokens themselves don't belong in a secret: they change, so they live in `HIVE_TOKEN_FILE` on a writable volume.

## Choosing toolsets

Every tool's name, description and argument schema is sent to the model, which uses up context. Exposing only what you need keeps the assistant faster and more focused:

| Toolset | Use it for |
|---|---|
| `home` | Login, the overview, the device list and holiday mode. **Always include it.** |
| `heating` | Heating zones and radiator valves |
| `hotwater` | Hot water |
| `devices` | Smart plugs, lights and sensors |
| `raw` | Any API path. Handy, but gives broad access, so it isn't part of `all`: list it by name to enable it. The account's user profile (name, email, phone, address) is redacted from its output. |

The [tool reference](tools.md) lists the tools in each toolset, and [Keeping the tool list small](#keeping-the-tool-list-small) suggests some combinations.

## Keeping the tool list small

Most MCP clients send every tool's name, description and argument schema to the model with each request. hive-mcp's list is small by MCP standards: about 7 KB for the 10 read-only tools of the default `all`, and 16 KB for 21 tools with writes on. Even so, a narrower list leaves more room for your conversation, helps the model pick the right tool, and keeps the rest out of its reach.

Clients with their own tool search, such as Claude Code, load tool definitions only when the model needs them, so they handle the full list fine. `HIVE_TOOLSETS` still limits what the model can reach.

The sizes are the `tools/list` result the client receives, measured by starting the server. "With writes" is `HIVE_ALLOW_WRITES=true`.

| Goal | `HIVE_TOOLSETS` | Read-only | With writes |
|---|---|---:|---:|
| Overview, devices and holiday mode | `home` | 4 tools, 3 KB | 6 tools, 4 KB |
| Heating only | `home,heating` | 7 tools, 5 KB | 13 tools, 10 KB |
| Heating and hot water | `home,heating,hotwater` | 9 tools, 6 KB | 18 tools, 13 KB |
| Lights and plugs | `home,devices` | 5 tools, 3 KB | 9 tools, 6 KB |
| Everything except the raw API tool (default) | `all` | 10 tools, 7 KB | 21 tools, 16 KB |
| Everything, including the raw API tool | `all,raw` | 11 tools, 8 KB | 22 tools, 17 KB |

The login tools (`HIVE_ALLOW_LOGIN_TOOLS=true`) add 2 tools and about 1.4 KB to any of these.

## Advanced: Hive's endpoints

You shouldn't need these. They exist in case Hive moves something before hive-mcp catches up. They must be `https://` URLs (plain `http://` is only accepted for `localhost` or a loopback address, such as a local mock) and can't contain a username or password.

| Variable | Default | Description |
|---|---|---|
| `HIVE_API_URL` | `https://beekeeper.hivehome.com/1.0` | Hive's API (called "beekeeper"), as the Hive app and pyhiveapi use it. |
| `HIVE_SSO_URL` | `https://sso.hivehome.com` | Hive's sign-in page, which names the Cognito pool and app client to log in with. |
| `HIVE_COGNITO_POOL_ID`, `HIVE_COGNITO_CLIENT_ID` | read from the sign-in page | The Cognito user pool (like `eu-west-1_AbC123`) and app client. Set both to skip reading the sign-in page. |
| `HIVE_COGNITO_URL` | `https://cognito-idp.<region>.amazonaws.com`, from the pool's region | The Cognito endpoint. |

The token file remembers which pool and client it was issued by, so a server keeps using them until you log in again.

## Example configurations

**Read-only** (the safest setup):

```sh
HIVE_USERNAME=you@example.com
HIVE_PASSWORD=...
MCP_AUTH_TOKEN=...
```

**Heating control.** The assistant can boost and set the heating and hot water, but can't touch plugs, lights or the raw API:

```sh
HIVE_USERNAME=you@example.com
HIVE_PASSWORD=...
MCP_AUTH_TOKEN=...
HIVE_ALLOW_WRITES=true
HIVE_TOOLSETS=home,heating,hotwater
```

**Everything**, for a single trusted user:

```sh
HIVE_USERNAME=you@example.com
HIVE_PASSWORD=...
MCP_AUTH_TOKEN=...
HIVE_ALLOW_WRITES=true
```
