# Troubleshooting

Start with the logs: `docker compose logs hive-mcp`. Configuration errors stop the server at startup with a clear message. Hive errors show up in the tool results the assistant sees, and you can usually ask it *"what error did you get?"*. To check the login, run `docker compose exec hive-mcp /hive-mcp status` or ask the assistant to call `hive_auth_status`.

- [The container keeps restarting](#the-container-keeps-restarting)
- [The client can't connect to the server](#the-client-cant-connect-to-the-server)
- [Logging in and SMS codes](#logging-in-and-sms-codes)
- ["Not logged in" and expired logins](#not-logged-in-and-expired-logins)
- [Errors when changing things](#errors-when-changing-things)
- [Readings look wrong or stale](#readings-look-wrong-or-stale)
- [Tools are missing](#tools-are-missing)
- [Hive changed something](#hive-changed-something)
- [Still stuck?](#still-stuck)

## The container keeps restarting

Run `docker compose logs hive-mcp`. The last line names the problem, for example:

| Message | Fix |
|---|---|
| `HIVE_ALLOW_WRITES must be true or false, got "…"` | Fix the value. Booleans accept `true`/`false`, `1`/`0`, `yes`/`no` and `on`/`off`. |
| `unknown toolset "x" in HIVE_TOOLSETS; valid: …` | Fix the typo. The message lists the valid names. |
| `MCP_TRANSPORT must be stdio or http, got "…"` | Fix the value. |
| `MCP_HTTP_PORT must be a positive integer, got "…"` | Fix the value. The same applies to `HIVE_TIMEOUT_MS`. |
| `HIVE_CACHE_SECONDS must be zero or a positive integer, got "…"` | Fix the value. |
| `set both HIVE_COGNITO_POOL_ID and HIVE_COGNITO_CLIENT_ID, or neither` | Remove both unless you know you need them. |
| `could not read HIVE_PASSWORD_FILE (…)` | The secret file isn't mounted where the variable points. |
| `the token file /data/tokens.json is not valid JSON; delete it and log in again` | The file was damaged or edited by hand. Delete it and [log in](getting-started.md#2-log-in-to-hive) again. |
| `could not read the token file …: permission denied` | The container's user (UID `65532`) can't read `/data`. With a bind mount, `chown 65532:65532` the directory. |

## The client can't connect to the server

1. **Is it running?** `curl http://<docker-host>:8080/health` should return `{"status":"ok"}`.
2. **Right host?** The port is published on the machine running Docker. If Docker runs on another machine (for example with `DOCKER_HOST`), `localhost` won't work.
3. **Right path?** The endpoint is `/mcp`, so the URL is `http://<docker-host>:8080/mcp`.
4. **`401 Unauthorized`:** the client isn't sending `Authorization: Bearer <MCP_AUTH_TOKEN>`, or the token doesn't match. Watch for stray quotes or spaces in `.env`.
5. **`403 Host … is not allowed`:** add the hostname the client uses to `MCP_ALLOWED_HOSTS`, or unset it. **`403 Origin … is not allowed`** means a browser-based client sent an `Origin` that isn't listed: add its `host` (or `host:port` if it isn't on port 80 or 443).
6. **The client expects SSE:** the server is stateless and answers `POST` requests with JSON. Make sure the client is set to *Streamable HTTP* (often called `http`), not `sse`.
7. **stdio clients show nothing:** check that the command has `-i` and not `-t`, and that `MCP_TRANSPORT=stdio` is set when using the image, which defaults to HTTP.

## Logging in and SMS codes

**`set HIVE_USERNAME (the email address you use in the Hive app) and HIVE_PASSWORD first`**

Add both to `.env`. Then run the login again.

**`hive rejected the login. Check HIVE_USERNAME and HIVE_PASSWORD …`**

Hive (Cognito) said the username or password is wrong. Log in to the Hive app or website with the same details to be sure, and watch for stray quotes or spaces in `.env`. Usernames are matched in lower case, as Hive needs.

**`hive doesn't recognise that username …`**

`HIVE_USERNAME` must be the email address you log in to the Hive app with.

**`hive has temporarily blocked logins after too many attempts …`**

Too many failed logins in a short time. Wait a while before trying again, then try once with the right details.

**No text message arrives**

- Codes go to the phone number on your Hive account. The login prints a masked version (`+*******1234`), so check it's the right phone.
- Texts can take a minute. If nothing arrives, press Ctrl+C and run the login again to get a new code.
- If your Hive account doesn't use SMS for two-step verification (for example, an authenticator app), hive-mcp can't log in yet: it says which kind of code Hive asked for. Please [open an issue](https://github.com/mattoddie/hive-mcp/issues/new/choose).

**`that code isn't right. Check the text message and try again`**

You get three tries per login. Use the newest code if you've had more than one.

**`the code has expired …` or `hive didn't accept the code, and the login has to start again`**

Codes and the login session last about three minutes. Run the login again and enter the new code promptly.

**`no code entered. In Docker, run the login with a terminal …`**

The login couldn't read what you typed because the container had no terminal. Use `docker compose run --rm hive-mcp login` (which has one), or `docker run -it …`. Don't run it with `docker compose up` or without `-it`.

**`hive's login asked for a new password (NEW_PASSWORD_REQUIRED) …`**

Hive wants you to set a new password. Log in to the Hive app or website once, set it, update `HIVE_PASSWORD`, and try again.

**`could not find Hive's login settings (HiveSSOPoolId and HiveSSOPublicCognitoClientId) on https://sso.hivehome.com`**

Hive has changed its sign-in page, so hive-mcp can't find which login service to use. Check for a newer hive-mcp release. As a workaround, if you know the values (pyhiveapi and Home Assistant users may have them), set `HIVE_COGNITO_POOL_ID` and `HIVE_COGNITO_CLIENT_ID`.

**`logged in, but could not register this server as a remembered device …`**

The login worked, but Hive didn't accept the device registration. The server works for now, but when its refresh token runs out it'll need `hive-mcp login` and an SMS code again. Try the login once more; if it keeps happening, please report it.

## "Not logged in" and expired logins

**`not logged in to Hive. Run hive-mcp login once …`**

The server has no usable login. Either nobody has logged in, or the token file isn't where the server looks:

- Log in as in [Getting started](getting-started.md#2-log-in-to-hive).
- **You logged in, but the server still says this.** The login was saved somewhere the server doesn't look. Check that `login` ran with the same `HIVE_TOKEN_FILE` and, in Docker, the same volume (`docker compose run` uses the Compose volume, but a plain `docker run` needs `-v` with the same volume name).
- **It forgets the login after every restart.** The token file isn't on a persistent volume. Check that `compose.yaml` mounts a volume at `/data`, and for stdio, that you pass `-v hive-data:/data` with the same volume name you logged in with.

**`the Hive login has expired, and logging in again needs the account password …`**

The refresh token ran out, and `HIVE_PASSWORD` isn't set, so the server can't log in again as its remembered device. Set `HIVE_PASSWORD` (or `HIVE_PASSWORD_FILE`) and restart. No SMS code is needed.

**`hive asked for an SMS code: this server is no longer a remembered device. Run hive-mcp login again`**

Hive has forgotten this server as a remembered device, for example because it was removed in your account's settings, `hive-mcp logout` was run elsewhere, or Hive reset remembered devices. Run `hive-mcp login` and enter the code. The server never asks for a code on its own, so it won't text you unexpectedly.

**`logging in to Hive failed recently; not retrying until … to avoid locking the account`**

An automatic login failed (see the earlier error in `hive_auth_status`), so the server waits five minutes before trying again. Fix the cause, such as a changed password, or run `hive-mcp login`, which works straight away.

**`could not save tokens to /data/tokens.json: … They will be lost on restart; mount a writable volume there`**

The server is working from memory because it can't write the token file. With the default read-only root filesystem, `/data` must be a volume. With a bind mount, make it writable by UID `65532`. Fix it before the next restart, or you'll need to log in again with an SMS code.

## Errors when changing things

**`… must be between 5 and 25 °C` or `… must be in steps of 0.5 °C`**

hive-mcp sets heating targets from 5 °C up to `HIVE_MAX_TEMP` (25 °C by default; Hive allows up to 32), in steps of 0.5. Round the temperature, or raise `HIVE_MAX_TEMP` if you really need a higher target.

**`… lasts indefinitely, until someone changes it`**

Manual heating and hot water `on` stay that way until something changes them, so the tools need `confirm: true`. Ask for a boost instead, or tell the assistant you do want it left on.

**`holiday mode can last at most 60 days`**

Pick an earlier end date, and set holiday mode again nearer the time if you're away longer.

**`no heating zone called "…"; the choices are …`, or `… matches more than one …`**

Use one of the names listed, or enough of a name to be unique.

**`… isn't boosting`**

There's no boost to cancel. Check with `hive_get_heating` or `hive_get_hot_water`.

**`This zone is off, so it won't heat to the new target …`** (a warning)

The zone is in `off` mode, so changing the target alone doesn't heat anything. Switch it to `manual` or `schedule` with `hive_set_heating_mode`.

**`… failed (HTTP 400)` or `(HTTP 404)`**

Hive refused the change. For `404`, the zone or device may have been removed: ask again with `fresh: true`. For `400`, the message from Hive usually says why. If a tool keeps failing for a device the Hive app controls fine, Hive may have changed its API; please report it with the error.

**`… failed (HTTP 429)`** or **`(HTTP 5xx)`**

Hive is rate limiting or having problems. hive-mcp has already retried a few times. Wait a minute and try again.

**`… could not reach the Hive API: …` or timeouts**

- Check the server has outbound internet access, and that `beekeeper.hivehome.com` resolves inside the container.
- Give slow requests longer with `HIVE_TIMEOUT_MS`, for example `60000`.
- Check whether the Hive app works. If it doesn't, Hive has an outage.

## Readings look wrong or stale

- Readings are cached for `HIVE_CACHE_SECONDS` (30 seconds by default). Ask for a fresh reading (*"check again"*), which uses `fresh: true`.
- Hive only hears from battery devices every so often. `hive_list_devices` shows when each was last seen and whether it's online.
- After a change, Hive can take a few seconds to report the new state, and the boiler or device a little longer to respond.
- Schedule times are in your home's time zone, from your Hive account. Check the time zone in the Hive app if they look an hour out.

## Tools are missing

Tools are only registered when they're allowed:

- Write tools need `HIVE_ALLOW_WRITES=true`.
- Each tool's toolset must be listed in `HIVE_TOOLSETS`.
- Restart the container after changing `.env` with `docker compose up -d`, and reconnect or restart your MCP client so it fetches the new tool list.
- Check the first log line: it shows whether writes are enabled and how many toolsets are loaded.

## Hive changed something

Because the API is unofficial, Hive can change it. Signs of that are logins that used to work failing with unexpected errors, or every tool failing with `400`/`404`. Check the [issues](https://github.com/mattoddie/hive-mcp/issues) and [pyhiveapi's](https://github.com/Pyhass/Pyhive/issues) for reports, and upgrade when a fix is out.

## Still stuck?

Search the [existing issues](https://github.com/mattoddie/hive-mcp/issues) or [open a bug report](https://github.com/mattoddie/hive-mcp/issues/new?template=bug_report.yml). Include the hive-mcp version (the first log line), the transport, the tool or command involved and the exact error. **Remove your email address, password, tokens, the token file's contents, device IDs and postcode first.**
