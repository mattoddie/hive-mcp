# Security policy

hive-mcp holds a login to a Hive account and can control the heating in someone's home, so security reports are taken seriously and handled as a priority.

## Supported versions

Security fixes go into the latest release. Please upgrade to the newest version before reporting.

| Version | Supported |
|---|---|
| Latest release | ✅ |
| Older releases | ❌ |

## Reporting a vulnerability

**Please don't report security vulnerabilities in public issues, discussions or pull requests.**

Report them privately through GitHub's **[private vulnerability reporting](https://github.com/mattoddie/hive-mcp/security/advisories/new)**: go to the repository's **Security** tab and click **Report a vulnerability**.

Please include:

- A description of the issue and its impact
- Steps to reproduce, or a proof of concept
- The affected version(s) and configuration, such as transport, `HIVE_ALLOW_WRITES` and toolsets
- Any suggested fix, if you have one

Please don't include real passwords, tokens or token files. Use the mock Hive in the test suite where you can.

## What to expect

- An acknowledgement within **3 working days**.
- An initial assessment within **7 days**, including whether the report is accepted and a rough timeline.
- A fix released as soon as practical, with a GitHub security advisory and credit to you, unless you'd rather stay anonymous.

Please give us a reasonable amount of time to fix the issue before you disclose it publicly.

## Scope

In scope:

- Bypassing `MCP_AUTH_TOKEN`, `MCP_ALLOWED_HOSTS`, or the write or toolset gating. An example would be changing the heating through the raw API tool without `HIVE_ALLOW_WRITES`.
- Leaking the Hive password, tokens or the remembered device's secret through tool output, logs or files
- Weaknesses in the login, such as the SRP implementation, the device registration, or handling the token file in a way that exposes it
- Getting the server to send SMS codes or make login attempts that could lock an account, without the user asking
- Vulnerabilities in the Docker image or the release pipeline

Out of scope:

- Issues that need an attacker who already holds a valid `MCP_AUTH_TOKEN` and uses only the tools that configuration allows. That's intended behaviour.
- Prompt injection that leads the model to call tools the configuration allows. This is a known limitation of AI assistants. [docs/security.md](docs/security.md) explains how to limit the impact.
- Vulnerabilities in Hive, its app, its API or its login service. Report those to Hive (Centrica) directly. hive-mcp is unofficial and not affiliated with them.

For guidance on deploying hive-mcp securely, see [docs/security.md](docs/security.md).
