# Changelog

All notable changes to this project are documented here. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For hive-mcp, the "public API" is the set of tool names, their arguments, the prompts and the configuration variables. Renaming or removing any of these is a breaking change.

## [Unreleased]

This release follows a security review. Read **Breaking changes** before upgrading.

### Breaking changes

- `hive_auth_start` and `hive_auth_complete` are only registered with the new `HIVE_ALLOW_LOGIN_TOOLS=true`. They log in with the password and can make Hive text an SMS code, so a model calling them in a loop could text you repeatedly or get the account locked. `hive-mcp login` in a terminal works as before.

### Security

- `hive_auth_start` (and `hive-mcp login`) won't ask Hive for another SMS code within a minute of the last one, or try again within five minutes of a login Hive refused. Five wrong codes end a pending login.

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
