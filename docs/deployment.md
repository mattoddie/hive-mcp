# Deployment

- [Docker Compose](#docker-compose)
- [The token volume](#the-token-volume)
- [Commands](#commands)
- [Image tags](#image-tags)
- [Upgrading](#upgrading)
- [Building from source](#building-from-source)
- [TLS with a reverse proxy](#tls-with-a-reverse-proxy)
- [Hardening](#hardening)
- [Running without Docker](#running-without-docker)

## Docker Compose

The repository's [`compose.yaml`](../compose.yaml) is a good starting point:

```yaml
services:
  hive-mcp:
    image: ghcr.io/mattoddie/hive-mcp:latest
    # To build from source instead of pulling, replace the line above with:
    # build: .
    container_name: hive-mcp
    restart: unless-stopped
    env_file: .env
    ports:
      - "8080:8080"
    volumes:
      - hive-data:/data
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]

volumes:
  hive-data:
```

```sh
docker compose run --rm hive-mcp login   # once, to enter the SMS code Hive sends
docker compose up -d          # start
docker compose logs -f        # follow logs
docker compose ps             # shows "healthy" once the health check passes
docker compose down           # stop (the volume, and so the login, is kept)
```

`docker compose run` attaches a terminal by default, which the login needs to read the code you type.

The image has a built-in health check that runs `hive-mcp healthcheck` every 30 seconds. The image has no shell or `curl`, so the binary checks its own `/health` endpoint. It only shows that the server is running, not that Hive is reachable or the login is valid.

To listen on one interface only, such as a LAN address or localhost when you use a reverse proxy, change the port mapping to `"192.168.1.20:8080:8080"` or `"127.0.0.1:8080:8080"`.

## The token volume

The server keeps its Hive login in `/data/tokens.json`: the current tokens, plus the remembered device's key and secret password that let it log in again without an SMS code. It changes about once an hour while the server is in use, and it must survive restarts and upgrades.

- Keep the named volume (`hive-data`) or a bind mount at `/data`. The root filesystem is read-only, so `/data` is the only place the server can write.
- If you use a bind mount, make the directory writable by the container's user, UID `65532`: `mkdir data && sudo chown 65532:65532 data`.
- `docker compose down` keeps the volume. `docker compose down -v` deletes it, and with it the login. You'd need a new SMS code to log in again.
- Treat the volume and any backups of it like a password. See [Security](security.md#the-token-file).

## Commands

The image's entrypoint is the `hive-mcp` binary, so you can run any of its commands with `docker compose run --rm hive-mcp <command>`, or `docker compose exec hive-mcp /hive-mcp <command>` while it's running:

| Command | What it does |
|---|---|
| `serve` (the default) | Serves MCP over stdio, or Streamable HTTP when `MCP_TRANSPORT=http` |
| `login` | Logs in to Hive with `HIVE_USERNAME`, `HIVE_PASSWORD` and the SMS code Hive sends, registers this server as a remembered device, and saves the login to `HIVE_TOKEN_FILE`. Needs a terminal (`docker run -it`). |
| `logout` | Asks Hive to forget this server's remembered device and deletes `HIVE_TOKEN_FILE` |
| `status` | Prints the login status as JSON: the account, whether this server is a remembered device, and when the token expires. Never prints tokens or secrets. |
| `healthcheck` | Exits with `0` if the HTTP server answers `/health`. It asks `MCP_HTTP_HOST` (or loopback when that is `0.0.0.0` or `::`) on `MCP_HTTP_PORT`, `_FILE` variants included. Used by the image's `HEALTHCHECK`. |
| `version` | Prints the version |

## Image tags

Images are published to the GitHub Container Registry at **`ghcr.io/mattoddie/hive-mcp`** for `linux/amd64` and `linux/arm64`. Each [GitHub release](https://github.com/mattoddie/hive-mcp/releases) publishes these tags:

| Tag | Example | Moves? |
|---|---|---|
| `X.Y.Z` | `1.4.2` | Never. Pin this for full reproducibility. |
| `X.Y` | `1.4` | Moves to the latest patch release (bug fixes). |
| `X` | `1` | Moves to the latest minor release (new features, no breaking changes). Not published for `0.x`. |
| `latest` | | Moves to the newest stable release. |

Pre-releases, such as `1.5.0-rc.1`, are only published under their exact version, never as `latest`.

The image is built `FROM gcr.io/distroless/static-debian13:nonroot`. It contains the static `hive-mcp` binary, CA certificates and time zone data, and nothing else: no shell and no package manager. It runs as the non-root user `65532` and is about 20 MB.

Every image includes an SBOM and build provenance attestation. To verify that an image was built by this repository's release workflow:

```sh
gh attestation verify oci://ghcr.io/mattoddie/hive-mcp:1.4.2 --owner mattoddie
```

## Upgrading

```sh
docker compose pull
docker compose up -d
```

The login carries over in the volume. Check the [changelog](../CHANGELOG.md) or the release notes before upgrading across a major version. After an upgrade, reconnect or restart your MCP client so it fetches the new tool list.

Because Hive's API is unofficial, a change on Hive's side can break things without warning. If that happens, check the [issues](https://github.com/mattoddie/hive-mcp/issues) and upgrade when a fix is released.

## Building from source

```sh
git clone https://github.com/mattoddie/hive-mcp.git
cd hive-mcp
docker build -t hive-mcp .        # or: make image
```

The tests run as part of the image build, so a broken build never produces an image. The build stage cross-compiles, so building for another architecture doesn't need emulation. To use a local build with Compose, replace `image:` in `compose.yaml` with `build: .` and run `docker compose up -d --build`.

## TLS with a reverse proxy

The server speaks plain HTTP. If it's reachable from outside a trusted network, put a TLS-terminating reverse proxy in front of it and keep `MCP_AUTH_TOKEN` set.

**Caddy**, which gets certificates automatically:

```text
hive-mcp.example.com {
    reverse_proxy hive-mcp:8080
}
```

**nginx:**

```nginx
server {
    listen 443 ssl;
    server_name hive-mcp.example.com;
    ssl_certificate     /etc/ssl/certs/hive-mcp.pem;
    ssl_certificate_key /etc/ssl/private/hive-mcp.key;

    location /mcp {
        proxy_pass http://hive-mcp:8080;
        proxy_set_header Host $host;
        proxy_read_timeout 120s;
    }
}
```

**Traefik** labels:

```yaml
services:
  hive-mcp:
    image: ghcr.io/mattoddie/hive-mcp:latest
    env_file: .env
    volumes: [hive-data:/data]
    labels:
      - traefik.enable=true
      - traefik.http.routers.hive-mcp.rule=Host(`hive-mcp.example.com`)
      - traefik.http.routers.hive-mcp.tls.certresolver=letsencrypt
      - traefik.http.services.hive-mcp.loadbalancer.server.port=8080
```

When the server sits behind a proxy, don't publish its port. Set `MCP_ALLOWED_HOSTS=hive-mcp.example.com` so it only accepts requests for that hostname.

> [!WARNING]
> Don't expose hive-mcp directly to the internet. Anyone with the `MCP_AUTH_TOKEN` can read your home's temperatures and, with writes on, control its heating. For remote access, a VPN (WireGuard, Tailscale) is safer than a public endpoint.

## Hardening

The image already runs as a non-root user with nothing in it but the binary, and the default `compose.yaml` already sets `read_only`, `cap_drop: [ALL]` and `no-new-privileges`. You can lock it down further:

```yaml
services:
  hive-mcp:
    image: ghcr.io/mattoddie/hive-mcp:1.4.2   # pinned
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    mem_limit: 128m
    pids_limit: 100
    env_file: .env
    volumes: [hive-data:/data]
    ports: ["127.0.0.1:8080:8080"]
```

The server only needs outbound HTTPS to `beekeeper.hivehome.com`, `sso.hivehome.com` (read at login) and `cognito-idp.eu-west-1.amazonaws.com` (Hive's login service). See [Security](security.md) for choosing settings and toolsets.

## Running without Docker

Docker is the easiest way to run hive-mcp as a service, but the server is a single static binary with no dependencies. You need [Go](https://go.dev/dl/) 1.25 or later to build it.

With `go install`:

```sh
go install github.com/mattoddie/hive-mcp/cmd/hive-mcp@latest
```

The binary lands in `$(go env GOPATH)/bin`. Built this way it reports its version as `0.0.0-dev`.

Or from a clone, which also stamps the version:

```sh
git clone https://github.com/mattoddie/hive-mcp.git
cd hive-mcp
make build VERSION=1.2.3      # writes bin/hive-mcp
```

Then:

```sh
export HIVE_USERNAME=you@example.com HIVE_PASSWORD_FILE=$HOME/.config/hive-mcp/password
hive-mcp login                # once; type in the code Hive texts you
hive-mcp                      # stdio, for an MCP client to start
MCP_TRANSPORT=http MCP_AUTH_TOKEN=... hive-mcp   # or a long-running HTTP server
```

Without Docker, the token file defaults to `~/.config/hive-mcp/tokens.json` (or `$XDG_CONFIG_HOME/hive-mcp/tokens.json`). To run it as a service, a systemd unit that sets these variables in an `EnvironmentFile` and runs `hive-mcp` as an unprivileged user works well.
