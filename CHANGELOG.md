# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For hive-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

This release follows a security review. Read **Breaking changes** before upgrading.

### Breaking changes

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

[Unreleased]: https://github.com/mattoddie/hive-mcp/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/mattoddie/hive-mcp/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/mattoddie/hive-mcp/releases/tag/v0.1.0
