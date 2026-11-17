# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For hive-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

## [0.7.0] - 2026-11-17

This release renames the auth tools verb-first, matching crunch-mcp. hive-mcp has 29 tools (12 read, 15 write and 2 login) and 5 prompts. Read the **Breaking** entries before upgrading.

### Changed

- **Breaking:** the login tools are named verb-first like the rest: `hive_auth_start` is now `hive_start_auth`, `hive_auth_complete` is now `hive_complete_auth` and `hive_auth_status` is now `hive_get_auth_status`. The old names are gone.

## [0.6.0] - 2026-11-17

This release changes list and dry-run output to match the other mattoddie MCP servers ahead of 1.0. hive-mcp has 29 tools (12 read, 15 write and 2 login) and 5 prompts. Read the **Breaking** entries before upgrading.

### Breaking changes

- List output is `{total, returned, items}`, as in the other mattoddie MCP servers: `hive_list_devices` (was `devices`), `hive_list_actions` (was `actions`), `hive_get_heating` and `hive_list_trvs` (were `zones`), and `hive_list_smart_devices` (was separate `plugs`, `lights` and `sensors` lists; each item now has a `kind` of `plug`, `light` or `sensor`).
- `dry_run: true` returns `{dry_run, would_send: {method, path, query, body}, current, changes, note}` for the first request the call would send, as the other mattoddie MCP servers do, instead of a list of every request. `current` is the state the request would change and `changes` lists each field that would change (`changed` with `from` and `to`, and `added`). When a call would send more than one request, the note says how many more would follow. A call that would send nothing returns `would_send: null` with its `result`.

## [0.5.0] - 2026-11-15

This release adds schedule editing, heat on demand, bulb colours, Hive actions and dry runs, and follows a code review. hive-mcp now has 29 tools (12 read, 15 write and 2 login) and 5 prompts. Read **Breaking changes** before upgrading.

### Breaking changes

- The prompts are offered only when the tools they use are: `warm_up`, `hot_water_now` and `going_away` need writes enabled, and a prompt is left out when its toolset is turned off.

### Added

- `hive_set_heating_schedule` (heating zones and TRV zones) and `hive_set_hot_water_schedule` change the weekly schedule. Give periods in `days` for only the days that change, keyed by day or by `weekdays`, `weekend` or `every_day`; the rest of the week is kept. Each day takes one to six periods, temperatures are checked against `HIVE_MAX_TEMP`, and the result shows the schedule Hive then has. Both need `HIVE_ALLOW_WRITES`.
- `hive_set_heat_on_demand` turns heat on demand on or off for a heating zone, so radiator valves can (or can't) turn the boiler on when their rooms need heat. It needs `HIVE_ALLOW_WRITES`.
- `hive_set_light` sets the colour of colour bulbs with the new `hue` (0–359) and `saturation` (0–100) arguments, with `brightness` as the colour's brightness.
- `hive_list_actions` lists the Hive actions (automations from the Hive app) with what starts each one and what it does, and `hive_set_action` turns one on or off (with `HIVE_ALLOW_WRITES`).
- Every tool that changes something takes `dry_run: true`: the call is validated as usual, and the exact requests it would send to Hive are returned instead of sent. See [Previewing a change](docs/usage.md#previewing-a-change).
- With writes on, the `schedule_review` prompt offers to apply the changes you agree to.

### Changed

- `hive_set_holiday_mode` and `hive_set_heating_mode` are annotated as destructive, since they can turn the heating down or off, and `hive_set_holiday_mode` is no longer marked idempotent, since its start defaults to now.
- Building from source needs Go 1.26 or later.

### Fixed

- Times are no longer shown in UTC on Windows. The time zone database is now built in, since Windows has none, and a time zone that can't be loaded is logged instead of silently replaced.
- A reading of the home that was under way when a change was made, or that was made in the few seconds after it, is no longer cached, so tools don't show the state from before the change for up to `HIVE_CACHE_SECONDS`.
- When another process (such as `hive-mcp login`) saves a new login while the server is refreshing its tokens, the server keeps the new login instead of overwriting it with the old one.
- `hive_set_light` with both a brightness and a colour temperature reports what changed when only the first change goes through, instead of just the error.
- Holiday mode's 60-day limit counts calendar days in the home's time zone, so 60 days across the October clock change is no longer refused.
- `hive_api_request` refuses paths with control characters, percent signs left after decoding (double encoding) or a trailing `/.`.
- The docs now say the raw API tool skips `HIVE_MAX_TEMP` and the holiday limit, and describe correctly when the client refreshes the login and retries.

## [0.4.0] - 2026-11-15

### Added

- When Hive boosts a thermostat because radiator valves are calling for heat (heat on demand), `hive_get_overview` and `hive_get_heating` say so with `boost_reason` and list the rooms asking for heat in `heat_requested_by`, so it isn't mistaken for a boost someone started.

## [0.3.2] - 2026-11-13

### Added

- Each release now has plain binaries for macOS, Linux and Windows on `amd64` and `arm64`, with a `SHA256SUMS` file and build provenance attestations, so you can run hive-mcp without Docker. See [Running without Docker](docs/deployment.md#running-without-docker).

### Fixed

- Smart meters are no longer reported as offline. Hive reads them through the smart meter network rather than the hub, so it always marks them offline, and `hive_get_overview` listed them under "needs attention" even when their readings were coming through. `hive_list_devices` now shows them with a `connection` note instead of an online status.

### Security

- Built with Go 1.27.4, which fixes an HTTP/1 server crash in the standard library's `net/http` (GO-2026-6859).

## [0.3.1] - 2026-10-22

### Added

- hive-mcp is published to the official [MCP Registry](https://registry.modelcontextprotocol.io) as `io.github.mattoddie/hive-mcp`, with the Docker image (run in stdio mode) and the MCP Bundles. Each stable release publishes the new version automatically. See [From the MCP Registry](docs/clients.md#from-the-mcp-registry).

## [0.3.0] - 2026-10-22

### Added

- **Claude Desktop extension.** Each release has an MCP Bundle (`.mcpb`) for macOS, Windows and Linux on x64 and Arm. Open it to install hive-mcp in Claude Desktop with a settings form, without Docker. Logging in from Claude is on by default in the extension, so the one-time SMS login needs no terminal. See [Claude Desktop](docs/clients.md#claude-desktop).
- `HIVE_AUDIT_LOG` records every call of a write tool, a login tool or a non-`GET` `hive_api_request` as a JSON line in a file (created with mode `0600`) or on standard error: the time, tool, arguments (with the SMS code and any secrets redacted) and outcome. Failed and refused calls are logged too. See [Audit log](docs/configuration.md#audit-log).
- A variable set to exactly `${user_config.<key>}`, as Claude Desktop passes an extension setting left empty, counts as unset.
- [Configuration](docs/configuration.md#keeping-the-tool-list-small) lists the size of the tool list for each toolset combination.

### Security

- Built with Go 1.27.3, which fixes a `crypto/x509` vulnerability in the standard library (GO-2026-6809).

## [0.2.0] - 2026-10-20

This release follows a security review. Read **Breaking changes** before upgrading.

### Breaking changes

- `HIVE_TOOLSETS=all` (the default) no longer includes the `raw` toolset (`hive_api_request`). List it by name to keep it, for example `HIVE_TOOLSETS=all,raw`.
- `HIVE_API_URL`, `HIVE_SSO_URL` and `HIVE_COGNITO_URL` must be `https://` unless they point at a loopback address.
- `MCP_AUTH_TOKEN` must be at least 16 characters, and `MCP_ALLOW_UNAUTHENTICATED=true` now also needs `MCP_ALLOWED_HOSTS`. The server refuses to start otherwise.
- With `MCP_ALLOWED_HOSTS` set, browser requests whose `Origin` isn't on the list are refused. An entry without a port matches only the default port (80 or 443) in an `Origin`, so list `host:port` for a page on another port.
- `/mcp` only accepts `POST` with a JSON body (other methods get `405`, bodies over 1 MiB `413`).
- `hive_auth_start` and `hive_auth_complete` are only registered with the new `HIVE_ALLOW_LOGIN_TOOLS=true`. They log in with the password and can make Hive text an SMS code, so a model calling them in a loop could text you repeatedly or get the account locked. `hive-mcp login` in a terminal works as before.
- Heating targets above 25 °C are refused unless you raise the new `HIVE_MAX_TEMP` (it was 32 °C).
- `hive_set_heating_mode` with `mode: manual`, `hive_set_heating_temperature` with `mode: manual` (or on a zone already in manual) and `hive_set_hot_water_mode` with `mode: on` need the new `confirm: true` argument, because those settings last indefinitely. `hive_set_holiday_mode` refuses an end more than 60 days after the start.

### Security

- `hive_auth_start` (and `hive-mcp login`) won't ask Hive for another SMS code within a minute of the last one, or try again within five minutes of a login Hive refused. Five wrong codes end a pending login.
- `hive-mcp logout` now also revokes the refresh token with Cognito's `RevokeToken`, and renews an expired access token first so it can still ask Hive to forget the remembered device. Before, it only forgot the device if the access token hadn't expired.
- A running server treats a deleted token file as logged out and drops the tokens and device it holds in memory. Before, a refresh could write the deleted file back.
- Heating targets are capped by the new `HIVE_MAX_TEMP` (default 25 °C; Hive allows up to 32) for every mode, boost and holiday mode. Holiday mode can last at most 60 days.
- The HTTP server checks `Origin` against `MCP_ALLOWED_HOSTS` (rejecting `null` and empty origins), logs a warning when running unauthenticated, and sets read and idle timeouts.
- `hive-mcp healthcheck` checks the configured `MCP_HTTP_HOST` and port (including `MCP_HTTP_PORT_FILE`) instead of always `127.0.0.1` and `MCP_HTTP_PORT`.
- `HIVE_API_URL`, `HIVE_SSO_URL` and `HIVE_COGNITO_URL` must use `https://` (plain `http://` only to a loopback address) and can't contain credentials. Requests to Hive and Cognito never follow redirects, which could otherwise carry tokens to another host.
- Only a `401` from Hive refreshes the login (a `403` did too). A `POST` is retried only after a `429`, never after a `502` or `503`. Responses over 64 MiB fail with a clear error instead of being truncated.
- The token file is saved through a uniquely named temporary file (`os.CreateTemp`), synced before the rename. It used to be `<file>.<pid>.tmp`, which two containers (both PID 1) sharing a volume could write at the same time.
- The image creates `/data` with mode `0700`, as the docs said; it was `0755`.
- Raw output (`raw: true` and `hive_api_request`) redacts the account's user profile (name, email, phone, address, postcode), keeping only fields like its ID, time zone and temperature unit. `hive_auth_status`, `hive_auth_start` and `hive_auth_complete` mask the email address and leave out the token file's path. `hive-mcp status` in a terminal still shows both.
- Build with Go 1.27.2 (`toolchain go1.27.2`, `golang:1.27` in the Dockerfile), and pin the Docker base images (`golang`, `distroless/static-debian13`) by digest; Dependabot keeps the digests current.
- CI runs `govulncheck` (pinned to v1.9.0).

## [0.1.1] - 2026-10-16

### Security

- Update `golang.org/x/sys` to v0.44.0 for GO-2026-5024. The advisory is Windows-only and not reachable from this code, so the Linux images weren't affected.
- Build with Go 1.26.9 (`toolchain go1.26.9`), which fixes standard-library advisories in `net/http`, `mime/multipart` and `crypto/tls`.

## [0.1.0] - 2026-10-15

First release, not yet tested against a live Hive account. Hive's API is unofficial, so expect rough edges, and please [report](https://github.com/mattoddie/hive-mcp/issues/new/choose) anything Hive rejects.

### Added

- Unofficial MCP server for Hive (British Gas) smart heating with 24 tools in 5 toolsets: home, heating, hotwater, devices and raw.
- Read tools for a one-call home overview, heating zones and radiator valve (TRV) zones, heating and hot water schedules (with what's scheduled now and next), the device list with batteries, signal and online status, plugs, lights and sensors, and holiday mode.
- Write tools, behind `HIVE_ALLOW_WRITES`, to set a zone's temperature and mode (schedule, manual or off), boost and cancel a heating boost, switch the hot water on, off or to its schedule, boost and cancel a hot water boost, set and cancel holiday mode, switch plugs, and switch lights with brightness and colour temperature. Heating targets are validated to 5–32 °C in steps of 0.5.
- Five built-in prompts: `home_check`, `warm_up`, `hot_water_now`, `going_away` and `schedule_review`.
- Login through AWS Cognito with SRP, implemented with the standard library and checked against test vectors from pyhiveapi's own code: `hive-mcp login` asks for the SMS code once and registers the server as a remembered device, so later logins (`REFRESH_TOKEN_AUTH`, then device SRP) never need a code. The Cognito pool is read from Hive's sign-in page, as pyhiveapi does. The same login is available from the chat with `hive_auth_start` and `hive_auth_complete`.
- `hive-mcp logout`, which asks Hive to forget the remembered device and deletes the token file.
- The login, including the remembered device's secret, is saved atomically to `HIVE_TOKEN_FILE` (mode `0600`), and a running server picks up a new login written by another process. The server never triggers an SMS code by itself and backs off after a failed automatic login.
- A short cache of Hive's whole-home reading (`HIVE_CACHE_SECONDS`, default 30), cleared by every change, plus retries when Hive rate-limits.
- Tools are annotated as read-only, destructive or open-world, and `HIVE_TOOLSETS` limits the tools exposed.
- Streamable HTTP (stateless) and stdio transports, with bearer-token auth and a `Host` header allow-list for HTTP.
- `serve`, `login`, `logout`, `status`, `healthcheck` and `version` commands.
- `_FILE` variants of every variable for Docker secrets.
- Multi-arch distroless Docker image (`linux/amd64`, `linux/arm64`) published to GHCR on each release, with SBOM and provenance attestation.
- A mock Hive for tests (sign-in page, a Cognito that checks SRP proofs, SMS codes and remembered devices, and the API), and documentation including a tool reference generated from the code.

[Unreleased]: https://github.com/mattoddie/hive-mcp/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/mattoddie/hive-mcp/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/mattoddie/hive-mcp/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/mattoddie/hive-mcp/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/mattoddie/hive-mcp/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/mattoddie/hive-mcp/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/mattoddie/hive-mcp/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/mattoddie/hive-mcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/mattoddie/hive-mcp/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/mattoddie/hive-mcp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mattoddie/hive-mcp/releases/tag/v0.1.0
