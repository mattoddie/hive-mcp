# Security

hive-mcp gives an AI assistant access to your home's heating. This page explains what that means and how to set it up safely. To report a vulnerability, see the [security policy](../SECURITY.md).

- [Threat model](#threat-model)
- [Layers of protection](#layers-of-protection)
- [The token file](#the-token-file)
- [Your Hive password](#your-hive-password)
- [Recommended setups](#recommended-setups)
- [Network exposure](#network-exposure)
- [Checklist](#checklist)

## Threat model

Five things need protecting:

1. **The MCP endpoint.** Anyone who can send requests to `/mcp` can use every registered tool with your Hive login. That includes reading the temperature of each room (which can show whether anyone's home), and, if you've allowed it, changing the heating, hot water, holiday mode, plugs and lights.
2. **The login.** The token file holds tokens and a remembered-device secret. With your password, that's enough to log in to your Hive account without the SMS code. Anyone who copies it can do anything the Hive app can.
3. **Your Hive password**, which hive-mcp needs in its environment to log in again by itself.
4. **The assistant's judgement.** A model can misunderstand a request, pick the wrong zone, or be steered by malicious text it reads (*prompt injection*), such as a device name, or a document or web page pasted into the same conversation. A home can be uncomfortable, or expensive, if the heating is left on full or turned off while you're away.
5. **Your data, on its way to the model.** Whatever the tools return, such as temperatures, schedules, when doors were opened and your home's time zone and postcode, is sent to your AI provider as part of the conversation.

The best defence is to limit what's possible, not to rely on the model behaving.

## Layers of protection

| Layer | Setting | What it does |
|---|---|---|
| Write switch | `HIVE_ALLOW_WRITES` (off by default) | When off, no tools that change anything are registered and the raw API tool only allows `GET`. This is the main control: Hive's login has no read-only mode. |
| Toolsets | `HIVE_TOOLSETS` | Tools outside the listed toolsets don't exist. The model can't see or call them. |
| Validation | Built in | Heating targets must be 5–32 °C in steps of 0.5, boosts last at most six hours, brightness is 5–100% and colour temperatures stay within the bulb's range. Bad values are refused before anything reaches Hive. |
| Tool annotations | Built in | Each tool is marked read-only or not, so MCP clients can ask for your confirmation before changes. With writes on, the raw API tool is marked destructive. |
| Server instructions | Built in | With writes on, the model is told to confirm changes with you, prefer boosts to manual mode for temporary changes, and never turn heating off or set holiday mode without being asked. This helps, but it isn't a security boundary. |
| No secret output | Built in | No tool or log line includes the password, tokens or device secret. `hive_auth_status` and `hive-mcp status` describe the login without showing them. |
| No surprise texts | Built in | The server never starts a login that would send an SMS code by itself, and it backs off for five minutes after a failed login, so it can't lock your account. |
| Endpoint auth | `MCP_AUTH_TOKEN` | Bearer token required on every request, checked in constant time. The http transport refuses to start without one. |
| Host allow-list | `MCP_ALLOWED_HOSTS` | Rejects requests with an unexpected `Host` header. This blocks DNS-rebinding attacks from browser pages. |
| Container | Built in | A distroless image with no shell, running as a non-root user. The default `compose.yaml` uses a read-only root filesystem, `cap_drop: [ALL]` and `no-new-privileges`. |

## The token file

`HIVE_TOKEN_FILE` (`/data/tokens.json` in the image) holds:

- the current ID and access tokens (valid for about an hour) and the refresh token,
- the remembered device's key, group key and **device password**: a random secret hive-mcp made up and registered with Hive's login service, which lets it skip the SMS code,
- the account's username and the Cognito pool it belongs to.

It's written atomically with mode `0600` in a directory with mode `0700`, owned by the container's non-root user.

- Treat the file, the volume and any backups of it like a password. Don't commit it, copy it to other machines, or put it in a shared folder. `tokens.json` is in `.gitignore` and `.dockerignore`.
- To log hive-mcp out, run `hive-mcp logout`. It asks Hive to forget the remembered device and deletes the file. Changing your Hive password means a copy of the file can't be used to log in again, though its current tokens may keep working until they expire.
- If you think the file has leaked, change your Hive password, update `HIVE_PASSWORD`, and log in again with `hive-mcp login`. Whether a password change also cancels existing tokens is up to Hive and hasn't been confirmed.

## Your Hive password

hive-mcp needs `HIVE_PASSWORD` for the first login and, later, to log in again as the remembered device when the refresh token runs out. It's never sent to Hive in the clear: SRP proves the password without sending it.

- Prefer `HIVE_PASSWORD_FILE` with Docker secrets to putting it in `.env`, and keep `.env` readable only by you.
- Use a unique password for your Hive account.
- If you'd rather not keep the password on the server at all, leave `HIVE_PASSWORD` out after logging in. The server then works until the refresh token runs out, and you run `hive-mcp login` again when tools say so.

## Recommended setups

**Look, don't touch** (lowest risk):

- Writes off (the default)
- Leave out `raw` if you don't need it

**Heating control:**

- `HIVE_ALLOW_WRITES=true`
- Only the toolsets you use, for example `home,heating,hotwater`, and leave `raw` out
- Keep tool confirmations on in your MCP client, so you approve each change

**Everything** (for a single trusted user, working interactively):

- Writes on and all toolsets
- Don't mix it with untrusted content, such as pasted emails or web pages, in the same conversation as instructions to change things

## Network exposure

- Set `MCP_AUTH_TOKEN` to a long random value, even on a home network.
- Publish the port only where it's needed: on a LAN address, or on `127.0.0.1` behind a reverse proxy.
- **Never expose port 8080 directly to the internet.** If traffic crosses a network you don't trust, put TLS in front of it. See [Deployment](deployment.md#tls-with-a-reverse-proxy). Prefer a VPN to a public endpoint.
- Set `MCP_ALLOWED_HOSTS` to the hostname clients use.
- The server only needs outbound HTTPS to `beekeeper.hivehome.com`, `sso.hivehome.com` and `cognito-idp.eu-west-1.amazonaws.com`. If your firewall allows, restrict it to those.

## Checklist

- [ ] `MCP_AUTH_TOKEN` is set to a long random value (`openssl rand -hex 32`).
- [ ] The port is only reachable from machines that need it, or published on `127.0.0.1` behind a proxy.
- [ ] TLS terminates at a reverse proxy if traffic crosses an untrusted network. Prefer a VPN to a public endpoint.
- [ ] Writes are only enabled if you need them.
- [ ] `HIVE_TOOLSETS` lists only the toolsets you use.
- [ ] The token file is on a persistent volume that only the server can read, and isn't copied anywhere else.
- [ ] The Hive password is in a secret file or a private `.env`, and isn't committed anywhere.
- [ ] The image is pinned to a version, and you update when security releases come out (watch the repository's releases).
