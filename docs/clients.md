# Connecting MCP clients

hive-mcp supports both standard MCP transports:

| Transport | How it runs | Best for |
|---|---|---|
| **Streamable HTTP** (the default in the image) | One long-running container. Clients connect to `http://<host>:8080/mcp`. | Most setups. One server can be shared by several clients, sessions and machines. |
| **stdio** (the default for the binary) | The client starts the server for each session, with `docker run -i` or the local `hive-mcp` binary. | A single machine, no network service, or clients that only support stdio. |

The HTTP endpoint is **stateless**: each request is handled on its own, so you can restart the container at any time. When `MCP_AUTH_TOKEN` is set, every request must send `Authorization: Bearer <token>`.

In the examples below, replace `<docker-host>` with the machine running the container and `<token>` with your `MCP_AUTH_TOKEN`. The server is called `hive` in every example. If you use a different name, the prompt slash commands change to match.

- [Claude Code](#claude-code)
- [Claude Desktop](#claude-desktop)
- [VS Code (GitHub Copilot)](#vs-code-github-copilot)
- [Cursor](#cursor)
- [OpenAI Codex CLI](#openai-codex-cli)
- [Other clients](#other-clients)
- [stdio mode](#stdio-mode)

## Claude Code

```sh
claude mcp add --transport http hive http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <token>"
```

Add `--scope user` to make it available in every project, or `--scope project` to save it in a `.mcp.json` you can share. Don't commit the token: in `.mcp.json`, reference an environment variable instead:

```json
{
  "mcpServers": {
    "hive": {
      "type": "http",
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer ${HIVE_MCP_TOKEN}" }
    }
  }
}
```

Run `claude mcp list` to check the connection, and `/mcp` inside Claude Code to see the tools. The built-in prompts are available as slash commands, such as `/mcp__hive__home_check` or `/mcp__hive__warm_up 21 60`.

To use stdio instead, with Docker or the local binary:

```sh
# Docker
claude mcp add hive -- docker run -i --rm \
  -e MCP_TRANSPORT=stdio --env-file /path/to/hive.env \
  -v hive-data:/data ghcr.io/mattoddie/hive-mcp:latest

# The local binary (see "Running without Docker" in the deployment guide)
claude mcp add hive -e HIVE_USERNAME=you@example.com -e HIVE_PASSWORD_FILE=/path/to/hive-password -- hive-mcp
```

Read the notes under [stdio mode](#stdio-mode) first.

## Claude Desktop

The easiest way is the **extension bundle**: a single `.mcpb` file that installs hive-mcp into Claude Desktop with a settings form. It needs no Docker, no service and no token, because Claude Desktop runs the server itself over stdio.

### Install the extension

1. Open the [latest release](https://github.com/mattoddie/hive-mcp/releases/latest) and download the bundle for your computer:

   | Computer | Bundle |
   |---|---|
   | Mac with Apple silicon (M1 and later) | `hive-mcp-<version>-darwin-arm64.mcpb` |
   | Mac with an Intel processor | `hive-mcp-<version>-darwin-amd64.mcpb` |
   | Windows on x64 (most PCs) | `hive-mcp-<version>-windows-amd64.mcpb` |
   | Windows on Arm | `hive-mcp-<version>-windows-arm64.mcpb` |

   There are Linux bundles too (`linux-amd64`, `linux-arm64`) for other clients that install MCP Bundles.

2. Double-click the file, or drag it onto **Settings → Extensions** in Claude Desktop, and click **Install**.
3. Fill in the settings and turn the extension on. The tools appear under the tools (🔨) menu, and the prompts under the **+** menu. You can allow or block each tool separately there.
4. **Log in once.** Have your phone to hand and ask Claude to *"log in to Hive"*. It calls `hive_auth_start`, Hive texts you a code, and you tell Claude the code so it can call `hive_auth_complete`. The server is then a remembered device and logs in again by itself, so you can turn **Allow logging in from Claude** off afterwards if you like.

To update, install the bundle from a newer release the same way. The login is kept.

### Settings

| Setting | Variable | What to enter |
|---|---|---|
| **Hive email address** (required) | `HIVE_USERNAME` | The email address you log in to the Hive app with. |
| **Hive password** (required) | `HIVE_PASSWORD` | Your Hive password. The server needs it to log in, and later to log in again by itself without an SMS code. |
| **Allow logging in from Claude** | `HIVE_ALLOW_LOGIN_TOOLS` | On by default in the extension, so you can log in without a terminal. It registers `hive_auth_start` and `hive_auth_complete`, which can make Hive text you a code. |
| **Allow changes** | `HIVE_ALLOW_WRITES` | Off by default, which keeps the assistant read-only. Read [Security](security.md) before turning it on. |
| **Maximum temperature (°C)** | `HIVE_MAX_TEMP` | The highest heating target Claude may set. 25 by default. |
| **Toolsets** | `HIVE_TOOLSETS` | Optional, such as `home,heating,hotwater`. Empty means every toolset except `raw`. See [Keeping the tool list small](configuration.md#keeping-the-tool-list-small). |
| **Audit log file** | `HIVE_AUDIT_LOG` | Optional full path to a file, such as `/Users/you/hive-mcp-audit.jsonl`, that records every change Claude makes and every login it starts. The folder must exist; the file is created. `stderr` writes the entries to the extension's log instead. Off when empty. See [Audit log](configuration.md#audit-log). |

Settings you leave empty use their defaults, as described in [Configuration](configuration.md). The login is saved in `~/.config/hive-mcp/tokens.json` (`%USERPROFILE%\.config\hive-mcp\tokens.json` on Windows), the same file `hive-mcp login` writes, so a login made with the command-line binary works for the extension too.

### If it doesn't start

- **Read the error.** Claude Desktop shows that the extension failed to start. The server's log, which says what's wrong, is in `~/Library/Logs/Claude/` on macOS and `%APPDATA%\Claude\logs\` on Windows, in a file named after the extension.
- **macOS blocks the server.** The binary in the bundle isn't notarised by Apple. If macOS says it can't check it for malicious software, open **System Settings → Privacy & Security** and click **Open Anyway** next to the message about `hive-mcp`, then turn the extension off and on again.
- **A password with `$` in it fails to log in.** Some Claude Desktop versions alter setting values that contain `$`. Change the Hive password to one without `$`, or use the Docker setup below with `HIVE_PASSWORD_FILE`.
- **Tools say the server isn't logged in.** Ask Claude to *"log in to Hive"* (with **Allow logging in from Claude** on). See [Troubleshooting](troubleshooting.md) for SMS code problems.

### Or run it with Docker

If you'd rather run the container image, use [stdio mode](#stdio-mode). Open **Settings → Developer → Edit Config** and add:

```json
{
  "mcpServers": {
    "hive": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "MCP_TRANSPORT=stdio",
        "-e", "HIVE_USERNAME", "-e", "HIVE_PASSWORD",
        "-v", "hive-data:/data",
        "ghcr.io/mattoddie/hive-mcp:latest"
      ],
      "env": {
        "HIVE_USERNAME": "you@example.com",
        "HIVE_PASSWORD": "your-hive-password"
      }
    }
  }
}
```

Or, with the local binary:

```json
{
  "mcpServers": {
    "hive": {
      "command": "/usr/local/bin/hive-mcp",
      "env": { "HIVE_USERNAME": "you@example.com", "HIVE_PASSWORD_FILE": "/Users/you/.config/hive-mcp/password" }
    }
  }
}
```

Restart Claude Desktop. The tools appear under the tools (🔨) menu, and the prompts appear under **+ → Add from hive**. If you haven't logged in yet, either run `hive-mcp login` once, or add `"-e", "HIVE_ALLOW_LOGIN_TOOLS=true"` to `args` and ask Claude to *"log in to Hive"*: it uses `hive_auth_start`, Hive texts you a code, and you tell Claude the code so it can call `hive_auth_complete`.

To turn on writes, add `"-e", "HIVE_ALLOW_WRITES=true"` to `args` (or `"HIVE_ALLOW_WRITES": "true"` to `env` for the binary).

## VS Code (GitHub Copilot)

Create `.vscode/mcp.json` in your workspace, or run **MCP: Add Server** from the command palette:

```json
{
  "inputs": [
    { "id": "hive-token", "type": "promptString", "description": "hive-mcp token", "password": true }
  ],
  "servers": {
    "hive": {
      "type": "http",
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer ${input:hive-token}" }
    }
  }
}
```

VS Code asks for the token once and stores it securely. Use the tools from Copilot Chat in **Agent** mode.

## Cursor

Add to `~/.cursor/mcp.json` for all projects, or `.cursor/mcp.json` for one project:

```json
{
  "mcpServers": {
    "hive": {
      "url": "http://<docker-host>:8080/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

## OpenAI Codex CLI

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.hive]
url = "http://<docker-host>:8080/mcp"
bearer_token_env_var = "HIVE_MCP_TOKEN"
```

Then `export HIVE_MCP_TOKEN=<token>` before running `codex`.

## Other clients

Any client that supports MCP **Streamable HTTP** works. Point it at `http://<docker-host>:8080/mcp` and send the bearer token header.

If your client only supports stdio, you have two options. You can use [stdio mode](#stdio-mode), or you can keep the shared HTTP server and connect through a stdio-to-HTTP bridge such as [`mcp-remote`](https://www.npmjs.com/package/mcp-remote):

```json
{
  "command": "npx",
  "args": ["-y", "mcp-remote", "http://<docker-host>:8080/mcp", "--header", "Authorization: Bearer <token>"]
}
```

## stdio mode

In stdio mode the MCP client runs the server itself and talks to it over stdin/stdout. No port is opened, and no `MCP_AUTH_TOKEN` is needed because only the client can reach the server.

With Docker, the general shape is:

```sh
docker run -i --rm \
  -e MCP_TRANSPORT=stdio \
  --env-file /path/to/hive.env \
  -v hive-data:/data \
  ghcr.io/mattoddie/hive-mcp:latest
```

To log in for stdio use, run the login command against the same volume once. It needs `-it` so you can type the SMS code:

```sh
docker run -it --rm --env-file /path/to/hive.env -v hive-data:/data ghcr.io/mattoddie/hive-mcp:latest login
```

With the local binary, run `hive-mcp login` with the same environment instead. The login goes to `~/.config/hive-mcp/tokens.json`. With `HIVE_ALLOW_LOGIN_TOOLS=true` you can also log in from the chat with the `hive_auth_start` and `hive_auth_complete` tools.

Tips:

- **Always mount the token volume** (`-v hive-data:/data`). Without it, each session starts with an empty `/data`, and the login, including the remembered device, is lost when the container exits. Every new session would then need an SMS code.
- **Use the same volume name everywhere.** Docker Compose prefixes volume names with the project name, so the volume from the [compose setup](deployment.md#docker-compose) is usually `hive-mcp_hive-data`, not `hive-data`. Run `docker volume ls` to check.
- **Several sessions at once are fine.** They share the token file and pick up each other's logins. For many clients or machines, the [HTTP server](#claude-code) is still simpler, and shares one cache between them.
- **Keep secrets out of the client config.** Pass `-e NAME` without a value so Docker copies the variable from the client's environment, as in the Claude Desktop example, or use `--env-file`.
- **Always pass `-i` and never `-t`** when serving. A TTY corrupts the MCP message stream. (The `login` command is the exception: it needs `-it` so you can type the code.)
- **Docker on another machine** works too. Set `DOCKER_HOST` in the client's `env`. The volume lives on that machine.
- **Pin a version** (for example `:1.2.3`) so a new `latest` doesn't surprise you. See [Deployment](deployment.md#image-tags).
