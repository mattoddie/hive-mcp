<div align="center">

<img src="docs/assets/logo.svg" alt="hive-mcp logo" width="112" height="112">

# hive-mcp

**Talk to your heating.**

An open-source [Model Context Protocol](https://modelcontextprotocol.io) server for **Hive**, British Gas's smart heating. It lets Claude and other AI assistants check your Hive thermostats, radiator valves, hot water, plugs and lights, and, if you allow it, boost the heating, set temperatures, switch the hot water on and set holiday mode, all from one small Docker container.

[![CI](https://github.com/mattoddie/hive-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mattoddie/hive-mcp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mattoddie/hive-mcp?sort=semver)](https://github.com/mattoddie/hive-mcp/releases)
[![Container image](https://img.shields.io/badge/ghcr.io-mattoddie%2Fhive--mcp-2496ED?logo=docker&logoColor=white)](https://github.com/mattoddie/hive-mcp/pkgs/container/hive-mcp)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**[hive-mcp.mattoddie.dev](https://hive-mcp.mattoddie.dev)**

[Quick start](#quick-start) · [Documentation](https://hive-mcp.mattoddie.dev/docs/) · [Tool reference](docs/tools.md) · [Contributing](CONTRIBUTING.md)

</div>

---

> [!IMPORTANT]
> hive-mcp is an **unofficial** project. It isn't made, endorsed or supported by Hive, British Gas or Centrica. Hive has no public API: hive-mcp uses the same undocumented API as the Hive app and the Home Assistant integration, which Hive can change at any time.

> **"It's freezing in here. Can you warm the house up?"**
> *The living room is 17.5 °C and the heating is on its schedule, which has it at 16 °C until 17:00. I can boost it to 21 °C for an hour, after which it goes back to the schedule. Shall I?*
> **"Yes, and put the hot water on too, I want a bath."**
> *Done: the heating is boosting to 21 °C until 15:40, and the hot water is boosting for 30 minutes. By the way, the bedroom radiator valve's battery is down to 20%.*

Ask about your home in plain English. The assistant picks from **24 tools** to look things up and, if you allow it, make changes. Some examples:

- 🌡️ *"How warm is each room, and is the heating on?"*
- 🔥 *"Boost the heating to 21 for an hour."*
- 🛁 *"I want a bath in half an hour. Is the hot water on?"*
- 🧳 *"We're away from Saturday until the 28th. Set holiday mode."*
- 🔋 *"Do any of my Hive devices need new batteries?"*
- 💡 *"Turn the living room light down to 30% and switch the lamp plug off."*

## Highlights

- **🐳 Docker-first and tiny.** One image (`ghcr.io/mattoddie/hive-mcp`) for `linux/amd64` and `linux/arm64`, about 20 MB. It's a single static Go binary on a distroless base, with no shell or package manager, running as a non-root user. It serves Streamable HTTP for a long-running service, or stdio for `docker run -i`. You can also run the binary on its own.
- **🛡️ Safe by default.** The server is read-only unless you turn on writes. Tools that aren't allowed aren't registered at all, and every change is checked first: heating targets must be 5–25 °C in steps of 0.5 (raise the top with `HIVE_MAX_TEMP`), boosts are limited to six hours, holiday mode to 60 days, and settings that last indefinitely (manual heating, hot water on) need an explicit confirmation.
- **📱 One SMS code, once.** Hive logins need a code sent by text message. You enter it once with `hive-mcp login`, and the server registers itself as a *remembered device*, so it can log in again on its own without another code. Passwords, tokens and device secrets never appear in tool output or logs.
- **🔐 Real Cognito SRP.** It logs in the way the Hive app does (AWS Cognito with SRP, which never sends your password), checked against the exact maths of the client Home Assistant uses.
- **🐢 Gentle on Hive.** Readings are cached for 30 seconds, so a conversation doesn't hammer an API Hive never meant for us, and it backs off when rate-limited or when a login fails.
- **🎛️ Tunable.** Expose only the toolsets you need, which keeps the model's context small and focused.
- **🧪 Tested.** The test suite runs against a mock Hive, including a fake Cognito that really checks the SRP proofs, SMS codes and remembered devices.

## Quick start

You need Docker, a Hive account that works in the Hive app, the phone that gets your Hive text messages, and an MCP client such as [Claude Code](https://claude.com/claude-code).

**1. Set up the server.**

```sh
mkdir hive-mcp && cd hive-mcp
curl -fsSLO https://raw.githubusercontent.com/mattoddie/hive-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/mattoddie/hive-mcp/main/example.env -o .env
# Edit .env: set HIVE_USERNAME and HIVE_PASSWORD (your Hive app login)
# and a random MCP_AUTH_TOKEN (e.g. `openssl rand -hex 32`)
```

**2. Log in to Hive once and start the server.**

```sh
docker compose run --rm hive-mcp login   # type in the code Hive texts you
docker compose up -d
```

**3. Connect your assistant.**

```sh
claude mcp add --transport http hive http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <MCP_AUTH_TOKEN>"
```

Now ask *"How warm is the house?"* in Claude Code.

Using **Claude Desktop, VS Code, Cursor or Codex**? See [Connecting MCP clients](docs/clients.md). Prefer not to run a service? Use [stdio mode](docs/clients.md#stdio-mode) with `docker run -i` or the local binary.

## Turning on changes

Out of the box the assistant can look but not touch. To let it change your heating, hot water, plugs and lights, add this to `.env` and restart:

```sh
HIVE_ALLOW_WRITES=true
HIVE_TOOLSETS=home,heating,hotwater   # optional: expose fewer tools
```

```sh
docker compose up -d
```

Read [Security](docs/security.md) before turning on writes. A heating system is a real thing in a real home.

## What it can do

| Toolset | What's covered |
|---|---|
| `home` | Login, a one-call overview of the home, the list of Hive devices with batteries and signal, and holiday mode |
| `heating` | Heating zones and radiator valve (TRV) zones: temperatures, modes and schedules; set the temperature or mode, boost and cancel a boost |
| `hotwater` | Hot water: status and schedule; switch it on, off or back to its schedule, boost and cancel a boost |
| `devices` | Smart plugs, lights and sensors: on or off, power use, brightness, colour temperature, open or closed |
| `raw` | An escape hatch that calls any path of Hive's API. Off unless you list it (`HIVE_TOOLSETS=all,raw`) |

See the **[tool reference](docs/tools.md)** for every tool and argument. Ready-made prompts like `home_check`, `warm_up` and `going_away` chain the tools together for common jobs. Changing schedules isn't supported yet; see [Compatibility](docs/compatibility.md).

## Documentation

The docs are also published at **[hive-mcp.mattoddie.dev/docs](https://hive-mcp.mattoddie.dev/docs/)**, with search.

| | |
|---|---|
| 🚀 [Getting started](docs/getting-started.md) | Step-by-step setup, including the one-time SMS login |
| 🔌 [Connecting MCP clients](docs/clients.md) | Claude Code, Claude Desktop, VS Code, Cursor, Codex, stdio mode |
| ⚙️ [Configuration](docs/configuration.md) | Every environment variable, how login and remembered devices work, Docker secrets, toolsets |
| 🐳 [Deployment](docs/deployment.md) | Compose, image tags, upgrades, TLS reverse proxy, hardening, running without Docker |
| 💬 [Using hive-mcp](docs/usage.md) | Example conversations, prompts, how changes and boosts work |
| 🧰 [Tool reference](docs/tools.md) | Every tool and prompt, generated from the code |
| 🛡️ [Security](docs/security.md) | Threat model, what the token file holds, safe write setups |
| 📡 [Compatibility](docs/compatibility.md) | Supported Hive devices, discontinued products, and how the unofficial API is used |
| 🩹 [Troubleshooting](docs/troubleshooting.md) | Login and SMS problems, common errors and how to fix them |
| 🛠️ [Development](docs/development.md) | Architecture, the mock Hive, adding a tool, testing |

## Contributing

Contributions are very welcome, whether that's bug reports, notes on how Hive's API behaves, docs or new tools. Read the [contributing guide](CONTRIBUTING.md) to get started, and please follow the [code of conduct](CODE_OF_CONDUCT.md). To report a vulnerability, see the [security policy](SECURITY.md). Please don't open a public issue for one.

## License

[MIT](LICENSE) © Matt Oddie

*hive-mcp is an independent community project. It is not affiliated with, endorsed by or sponsored by Hive, British Gas or Centrica plc. "Hive" and "British Gas" are trademarks of Centrica plc or its subsidiaries. hive-mcp builds on what the community learned writing [pyhiveapi](https://github.com/Pyhass/Pyhive), the library behind the Home Assistant Hive integration.*
