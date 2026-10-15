# Configuration

hive-mcp is configured entirely through environment variables. With Docker Compose, put them in `.env`. Copy [`example.env`](../example.env) to get started.

- [Hive account and login](#hive-account-and-login)
- [How login works](#how-login-works)
- [What the assistant may do](#what-the-assistant-may-do)
- [Caching and timeouts](#caching-and-timeouts)
- [MCP transport](#mcp-transport)
- [Docker secrets (`_FILE` variables)](#docker-secrets-_file-variables)
- [Choosing toolsets](#choosing-toolsets)
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
| From the assistant | Ask it to *"log in to Hive"*. It calls `hive_auth_start` (Hive texts you), you tell it the code, and it calls `hive_auth_complete`. | Logging in without a terminal, for example with Claude Desktop in stdio mode. |

Running `login` again later doesn't send a code if the server is still a remembered device. It just logs in afresh.

> [!NOTE]
> A running server notices when the token file changes, so you can log in again with the `login` command without restarting it.

### What the server will and won't do on its own

- It **never triggers an SMS code by itself**. If it would need one (for example, because the remembered device was removed from your account), it stops and tools tell you to run `hive-mcp login` again.
- After a failed automatic login it **waits five minutes** before trying again, so a wrong password can't lock your account through repeated attempts.
- Without `HIVE_PASSWORD`, the server works until the refresh token runs out, then needs a new `hive-mcp login`.

### Logging out

`hive-mcp logout` asks Hive to forget this server's remembered device (if the login is still valid) and deletes the token file. If you change your Hive password, update `HIVE_PASSWORD` too, or the server can't log in again by itself. If it then asks for a new login, run `hive-mcp login` again.

## What the assistant may do

| Variable | Default | Description |
|---|---|---|
| `HIVE_ALLOW_WRITES` | `false` | Register tools that change things: heating temperature and mode, boosts, hot water, holiday mode, plugs and lights, and non-GET requests in the raw API tool. |
| `HIVE_TOOLSETS` | `all` | Comma-separated list of [toolsets](#choosing-toolsets) to expose. |

Boolean variables accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. Anything else stops the server with an error.

The [tool reference](tools.md) labels every tool with the setting it needs:

| Label | Registered when |
|---|---|
| 🟢 read | Always |
| 🟠 write | `HIVE_ALLOW_WRITES=true` |

Tools that aren't allowed are **not registered at all**. The assistant can't see them, so it can't call them or be talked into calling them. After changing `HIVE_ALLOW_WRITES`, restart the server and reconnect your MCP client so it fetches the new tool list.

Unlike an OAuth service, Hive's login has no read-only scope: whatever the switch says, the login itself can do anything the Hive app can. The switch is what keeps the assistant read-only. See [Security](security.md).

## Caching and timeouts

| Variable | Default | Description |
|---|---|---|
| `HIVE_CACHE_SECONDS` | `30` | How long a reading of the whole home is reused before asking Hive again. A conversation often makes several calls in a row, and this keeps them to one request. Every change clears the cache, and tools take `fresh: true` to skip it. `0` turns the cache off. |
| `HIVE_TIMEOUT_MS` | `30000` | Timeout for each request to Hive, in milliseconds. |

hive-mcp also retries a few times, with increasing waits, when Hive answers `429 Too Many Requests` or `503`.

## MCP transport

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `http` in the Docker image (`stdio` for the binary) | `http` for Streamable HTTP, or `stdio`. |
| `MCP_HTTP_HOST` | `127.0.0.1` (`0.0.0.0` in the Docker image) | Address to listen on (HTTP only). |
| `MCP_HTTP_PORT` | `8080` | Port to listen on (HTTP only). |
| `MCP_AUTH_TOKEN` | (required for http) | Every request to `/mcp` must send `Authorization: Bearer <token>`. The server refuses to start in http mode without it. Generate one with `openssl rand -hex 32`. |
| `MCP_ALLOW_UNAUTHENTICATED` | `false` | Start the http transport without `MCP_AUTH_TOKEN`. Only for a server that sits behind something else that authenticates every request. |
| `MCP_ALLOWED_HOSTS` | | Comma-separated allow-list of `Host` header values, such as `hive-mcp.lan,hive-mcp.lan:8080`. Requests with any other `Host` are rejected, which protects against DNS-rebinding attacks from web pages in your browser. |

The HTTP server exposes these endpoints:

| Path | Method | Purpose |
|---|---|---|
| `/mcp` | `POST` | The MCP endpoint (stateless Streamable HTTP, JSON responses). Request bodies are limited to 1 MiB. |
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
| `raw` | Any API path. Handy, but gives broad access. |

The [tool reference](tools.md) lists the tools in each toolset. Some suggested sets:

| Goal | `HIVE_TOOLSETS` |
|---|---|
| Heating and hot water only | `home,heating,hotwater` |
| Lights and plugs only | `home,devices` |
| Everything (default) | `all` |

## Advanced: Hive's endpoints

You shouldn't need these. They exist in case Hive moves something before hive-mcp catches up.

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
