<div align="center">
<img src="assets/logo.svg" alt="hive-mcp logo" width="80" height="80">

# hive-mcp documentation
</div>

**New here?** Start with [Getting started](getting-started.md). It goes from nothing to your first question in about ten minutes, with your phone to hand for the one-time SMS code.

hive-mcp is unofficial and not affiliated with Hive, British Gas or Centrica. It uses the undocumented API behind the Hive app. See [Compatibility](compatibility.md) for what that means.

## Using hive-mcp

| Guide | What's in it |
|---|---|
| [Getting started](getting-started.md) | Running the container, the one-time SMS login, connecting Claude Code, first questions |
| [Connecting MCP clients](clients.md) | Claude Code, Claude Desktop, VS Code, Cursor, Codex, generic clients, stdio mode |
| [Configuration](configuration.md) | Every environment variable, how login and remembered devices work, Docker secrets, choosing toolsets, example configs |
| [Using hive-mcp](usage.md) | Example conversations, built-in prompts, how changes and boosts work, caching, the raw API tool |
| [Tool reference](tools.md) | Every tool and argument, generated from the code |

## Running it well

| Guide | What's in it |
|---|---|
| [Deployment](deployment.md) | Compose, image tags, upgrades, reverse proxies with TLS, hardening, running without Docker |
| [Security](security.md) | Threat model, layers of protection, what the token file holds, recommended setups, checklist |
| [Compatibility](compatibility.md) | Supported Hive devices, discontinued products, the unofficial API and its limits |
| [Troubleshooting](troubleshooting.md) | Login, SMS code and remembered-device problems, error messages and how to fix them |

## Contributing

| Guide | What's in it |
|---|---|
| [Contributing guide](../CONTRIBUTING.md) | How to report issues, set up, and open pull requests |
| [Development](development.md) | Architecture, the login flow, adding a tool, the mock Hive, testing |
| [Releasing](releasing.md) | Versioning, cutting a release, what the release workflow does |
| [Changelog](../CHANGELOG.md) | What changed in each version |
