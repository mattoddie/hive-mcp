# Getting started

This guide takes you from nothing to asking your first question about your Hive heating. It takes about ten minutes. Have your phone to hand: Hive texts you a code once, during the login.

- [What you need](#what-you-need)
- [1. Set up the server](#1-set-up-the-server)
- [2. Log in to Hive](#2-log-in-to-hive)
- [3. Start the server](#3-start-the-server)
- [4. Connect your assistant](#4-connect-your-assistant)
- [5. Ask something](#5-ask-something)
- [Next steps](#next-steps)

## What you need

- **A Hive account** that you can log in to in the Hive app, with at least one Hive product, such as Hive Active Heating. See [Compatibility](compatibility.md) for the devices hive-mcp knows about.
- **The phone that receives your Hive text messages.** Hive accounts use two-step verification, so the first login needs a code Hive sends by SMS.
- **A machine that runs Docker.** This could be a NAS, a home server, a small VM or your laptop. The image supports both `amd64` and `arm64`. If you'd rather not use Docker, there's a single binary too. See [Running without Docker](deployment.md#running-without-docker).
- **An MCP client**, such as [Claude Code](https://claude.com/claude-code), Claude Desktop, VS Code, Cursor or Codex.

> [!NOTE]
> hive-mcp is unofficial. It isn't made or supported by Hive, British Gas or Centrica, and it uses Hive's undocumented API, the same one the Hive app uses. It works today, but Hive could change that API at any time. Please don't contact Hive's support about hive-mcp.

## 1. Set up the server

Create a folder and download the compose file and the example settings:

```sh
mkdir hive-mcp && cd hive-mcp
curl -fsSLO https://raw.githubusercontent.com/mattoddie/hive-mcp/main/compose.yaml
curl -fsSL https://raw.githubusercontent.com/mattoddie/hive-mcp/main/example.env -o .env
```

Edit `.env` and set at least:

```sh
HIVE_USERNAME=you@example.com            # the email address you log in to the Hive app with
HIVE_PASSWORD=your-hive-password
MCP_AUTH_TOKEN=a-long-random-string      # generate one with: openssl rand -hex 32
```

Leave everything else for now. The server starts **read-only**, which is the right way to begin.

Why the password? The first login uses it with the SMS code. After that the server logs in with a refresh token, and when that runs out it logs in again by itself, using the password and the *remembered device* it registers during the first login, without another SMS code. Keep `.env` private: see [Security](security.md).

The compose file keeps the login in a Docker volume called `hive-data`, mounted at `/data`. Keep it: it holds the tokens and the remembered device's secret. See [How login works](configuration.md#how-login-works).

## 2. Log in to Hive

Run the login command once. It needs a terminal, because it asks you for the code:

```sh
docker compose run --rm hive-mcp login
```

It logs in with your username and password, and Hive texts a code to the phone on your account:

```text
Logging in to Hive as you@example.com ...

Hive has sent a code by text message to +*******1234.
Enter the code:
```

Type the code and press Enter. Codes expire after about three minutes. If you mistype it, you get two more tries. The command finishes with something like:

```text
Logged in to Hive as Sam Smith (you@example.com): 2 heating zone(s), 9 device(s).
Tokens saved to /data/tokens.json.
This server is now a remembered device ("hive-mcp"), so it can log in again without an SMS code.
```

That's the only time you need the code. If you run `login` again later, it uses the remembered device and doesn't send another text.

Without Compose, the same login with plain Docker is:

```sh
docker run -it --rm --env-file .env -v hive-data:/data ghcr.io/mattoddie/hive-mcp:latest login
```

The `-it` matters: without it the command can't read the code you type.

> [!TIP]
> You can also log in from the chat. Set `HIVE_ALLOW_LOGIN_TOOLS=true`, restart, and ask the assistant to *"log in to Hive"*. It calls `hive_auth_start`, Hive texts you, you tell the assistant the code, and it calls `hive_auth_complete`. That's handy for stdio setups such as Claude Desktop.

## 3. Start the server

```sh
docker compose up -d
docker compose logs -f hive-mcp
```

You should see lines like these:

```text
hive-mcp 1.0.0: https://beekeeper.hivehome.com/1.0, auth token file /data/tokens.json (remembered device), writes disabled, toolsets 5, transport http
hive-mcp listening on http://0.0.0.0:8080/mcp (bearer auth enabled)
```

If the first line says `not logged in`, go back to step 2. You can log in again at any time without restarting the server: it picks up the new login from the token file.

Check that it's up from another machine:

```sh
curl http://<docker-host>:8080/health
# {"status":"ok"}
```

> [!TIP]
> `/health` only shows that the server is running. It doesn't contact Hive. To check the login, run `docker compose exec hive-mcp /hive-mcp status`, or ask the assistant to call `hive_auth_status`. Step 5 is the real test.

## 4. Connect your assistant

For **Claude Code**:

```sh
claude mcp add --transport http hive http://<docker-host>:8080/mcp \
  --header "Authorization: Bearer <MCP_AUTH_TOKEN>"
```

Run `claude mcp list` to check that `hive` shows as connected. For other clients, see [Connecting MCP clients](clients.md).

## 5. Ask something

Start a new conversation and try:

- *"How warm is the house?"*
- *"What's the heating schedule today?"*
- *"Do any of my Hive devices need new batteries?"*

The assistant calls tools like `hive_get_overview` and `hive_get_heating_schedule` and summarises the results. In Claude Code you can also run the built-in prompts as slash commands, for example `/mcp__hive__home_check`.

If something goes wrong, the tool's error message usually says what to fix. [Troubleshooting](troubleshooting.md) covers the common errors.

## Next steps

- **Let it make changes.** Set `HIVE_ALLOW_WRITES=true` and restart with `docker compose up -d`. Read [Security](security.md) first.
- **Expose fewer tools** with `HIVE_TOOLSETS` for a faster, more focused assistant. See [Configuration](configuration.md#choosing-toolsets).
- **Put it behind HTTPS** if it's reachable outside your LAN. See [Deployment](deployment.md#tls-with-a-reverse-proxy).
- **Learn what it can do** in [Using hive-mcp](usage.md) and the [tool reference](tools.md).
