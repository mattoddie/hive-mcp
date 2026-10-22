# Releasing

Releases are driven by **GitHub releases**. Publishing a release with a tag like `v1.2.3` triggers the [Release workflow](../.github/workflows/release.yml). That workflow builds the Docker image for `linux/amd64` and `linux/arm64` and pushes it to `ghcr.io/mattoddie/hive-mcp`. It then builds the MCP Bundles (`.mcpb`) for Claude Desktop and attaches them to the release, and for stable releases publishes the new version to the official [MCP Registry](https://registry.modelcontextprotocol.io). Nothing is published by merging to `main` alone.

- [Versioning](#versioning)
- [Release checklist](#release-checklist)
- [Pre-releases](#pre-releases)
- [What the workflow does](#what-the-workflow-does)
- [The MCP Registry](#the-mcp-registry)
- [If something goes wrong](#if-something-goes-wrong)
- [Repository setup](#repository-setup)

## Versioning

The project follows [Semantic Versioning](https://semver.org). Its public API is the set of **tool names, their arguments, prompts and the environment variables**:

| Change | Bump | Examples |
|---|---|---|
| Breaking | **major** (`2.0.0`) | Renaming or removing a tool, argument, prompt or variable; moving a tool to a less restrictive gate; changing a default so that more is allowed |
| New features | **minor** (`1.3.0`) | New tools, toolsets, arguments or prompts |
| Fixes | **patch** (`1.2.4`) | Bug fixes, compatibility fixes, docs, dependency updates |

While the version is `0.x`, minor bumps may contain breaking changes. Call them out in the release notes.

The version comes **from the tag**. There's no version file in the repository: builds default to `0.0.0-dev`, and release builds stamp the tag's version into `internal/tools.Version` with `-ldflags`. The server reports it to MCP clients, prints it on its first log line, and `hive-mcp version` prints it.

## Release checklist

1. **Check `main` is green** on [CI](https://github.com/mattoddie/hive-mcp/actions/workflows/ci.yml).
2. **Update the changelog.** In a small PR titled `chore(release): v1.2.0`, move the **Unreleased** entries in [`CHANGELOG.md`](../CHANGELOG.md) under a new heading, `## [1.2.0] - YYYY-MM-DD`, and update the comparison links at the bottom. Fill in the final tool and prompt counts from the top of [`docs/tools.md`](tools.md). Merge it.
3. **Publish the release.** Use either the CLI or the web UI.

   With the CLI:

   ```sh
   gh release create v1.2.0 --target main --title "v1.2.0" --generate-notes
   ```

   Or in the web UI: **Releases → Draft a new release → Choose a tag → type `v1.2.0` → Create new tag on publish**, target `main`. Click **Generate release notes**, edit them as needed, then **Publish release**.

4. **Watch the workflow.** Open **Actions → Release**. It takes a few minutes. When it finishes, the release notes gain a *Container image* section with the pull command, digest and tags, and a *Claude Desktop* section listing the bundles, which are attached as release assets. For a stable release, the *Publish to the MCP Registry* job then lists the version in the registry.
5. **Check the image:** `docker pull ghcr.io/mattoddie/hive-mcp:1.2.0`, then `docker run --rm ghcr.io/mattoddie/hive-mcp:1.2.0 version`. If you can, install the bundle for your computer in Claude Desktop too. Check the registry entry too: `curl -s "https://registry.modelcontextprotocol.io/v0/servers/io.github.mattoddie%2Fhive-mcp/versions/latest" | jq .server.version`.

Release notes are generated from merged PR titles and grouped by label. The groups are set in [`.github/release.yml`](../.github/release.yml). Label PRs `breaking`, `enhancement`, `new-tool`, `bug`, `compatibility`, `documentation` or `dependencies` to sort them, or `skip-changelog` to leave a PR out.

## Pre-releases

To test a release candidate, use a pre-release tag and tick **Set as a pre-release**:

```sh
gh release create v1.3.0-rc.1 --target main --prerelease --generate-notes
```

Pre-releases are published only under their exact version (`ghcr.io/mattoddie/hive-mcp:1.3.0-rc.1`). They never move `latest`, `1` or `1.3`. They aren't published to the MCP Registry.

## What the workflow does

| Step | Detail |
|---|---|
| Validate the tag | Must match `vMAJOR.MINOR.PATCH` with an optional `-prerelease` suffix, otherwise the workflow fails before building. |
| Build | `docker buildx` for `linux/amd64` and `linux/arm64`, from scratch with no build cache. The Go build cross-compiles, so no emulation is needed for the binary. The test suite runs inside the build, so failing tests mean no image. |
| Tag | `X.Y.Z` always. For stable releases also `X.Y`, `X` (from `1.0.0` on) and `latest`. |
| Push | To `ghcr.io/mattoddie/hive-mcp` using the workflow's `GITHUB_TOKEN`. No personal tokens are needed. |
| Supply chain | Attaches an SBOM and SLSA provenance to the image, and pushes a signed build provenance attestation (`gh attestation verify`). |
| Release notes | Appends the pull command, digest and tags. |
| MCP Bundles | After the image, runs `make mcpb VERSION=X.Y.Z`: one `hive-mcp-X.Y.Z-<os>-<arch>.mcpb` for each of `darwin`, `windows` and `linux` on `amd64` and `arm64`. It validates each manifest with the official `@anthropic-ai/mcpb` CLI (pinned in the workflow), attests their build provenance, uploads them to the release and lists them in the notes. |
| MCP Registry | Stable releases only, after the image and the bundles. Stamps the version, the image tag and the bundles' SHA-256s into [`server.json`](../server.json), then publishes it with `mcp-publisher`, logging in with GitHub OIDC. See [The MCP Registry](#the-mcp-registry). |

### About the bundles

An [MCP Bundle](https://github.com/modelcontextprotocol/mcpb) is a zip file with a `manifest.json`, the icon, the licence and the server binary. The manifest lives in [`mcpb/manifest.json`](../mcpb/manifest.json): edit its descriptions and settings by hand, while `make docs` regenerates its `tools` and `prompts` from the server (CI checks they're current). [`cmd/mcpb`](../cmd/mcpb/main.go) stamps the version and platform into a copy and writes the zip.

There's one bundle per OS and architecture because a manifest can only choose the command by OS, not by CPU. Try one locally with `make mcpb` (the bundles land in `dist/`).

The bundles aren't signed. The macOS binaries carry the Go linker's ad-hoc signature, which Apple silicon requires, but they aren't signed with a Developer ID or notarised, so macOS may ask the user to allow them (see [Claude Desktop](clients.md#if-it-doesnt-start)). `mcpb sign` can add a signature to the bundle itself, but that needs a code-signing certificate and isn't set up.

## The MCP Registry

hive-mcp is listed in the official [MCP Registry](https://registry.modelcontextprotocol.io) as `io.github.mattoddie/hive-mcp`. The entry is [`server.json`](../server.json) at the root of the repository. It describes two kinds of package:

- **The Docker image**, run in stdio mode: `MCP_TRANSPORT` is fixed to `stdio`, the login lives in the `hive-data` volume, and the variables a user sets match the Claude Desktop extension's settings.
- **The six MCP Bundles**, one for each OS and processor. The registry can't say which platform a bundle is for, but each file name ends in `<os>-<arch>.mcpb`, so all six are listed.

The committed file carries the `0.0.0-dev` placeholder version, like the bundle manifest, with placeholder SHA-256s. The release job fills in the real values, so there's nothing to edit by hand for a release.

The registry proves the image belongs to us through its `io.modelcontextprotocol.server.name` label (set in the [Dockerfile](../Dockerfile)), and the `io.github.mattoddie/` namespace through the workflow's GitHub OIDC token. No secrets are needed.

**Keeping it in step.** `make docs-check` (run by CI) fails if `server.json`'s environment variables, defaults, and required and secret flags differ from the bundle manifest's settings, so change both together. CI also checks `server.json` with `mcp-publisher validate` and checks the image's label.

**The `mcp-publisher` version** is pinned, with its SHA-256, in both [`release.yml`](../.github/workflows/release.yml) and [`ci.yml`](../.github/workflows/ci.yml). To update it, take the new version and the `mcp-publisher_linux_amd64.tar.gz` line from the `registry_<version>_checksums.txt` file on [its release page](https://github.com/modelcontextprotocol/registry/releases), and change both files together. If the registry moves to a new `server.json` schema, update `$schema` too, following the [schema changelog](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/CHANGELOG.md).

### Re-running the registry step

If only the *Publish to the MCP Registry* job failed, open the run under **Actions → Release** and choose **Re-run failed jobs**. The image and the bundles are already published, so only this job runs again. A version can only be published once: if it's already in the registry, the job says so and stops.

### Publishing by hand

If the job can't be made to work, publish from your own machine. You need to be an owner of the `mattoddie` GitHub organisation.

1. Install `mcp-publisher` from its [releases](https://github.com/modelcontextprotocol/registry/releases) (check the SHA-256 against the release's checksums file).
2. Fill in `server.json` for the release, for example `1.2.0`: set `version` to `1.2.0`, the image tag to `ghcr.io/mattoddie/hive-mcp:1.2.0`, and each bundle's URL and `fileSha256`:

   ```sh
   gh release download v1.2.0 --pattern '*.mcpb' --dir /tmp/bundles
   sha256sum /tmp/bundles/*.mcpb
   ```

   The release workflow's *Stamp* step does all of this with `jq`: you can run its commands as they are with `TAG=v1.2.0`, `VERSION=1.2.0` and `RUNNER_TEMP` (an empty directory) set.
3. Check, log in and publish:

   ```sh
   mcp-publisher validate server.json
   mcp-publisher login github     # opens a browser to authorise
   mcp-publisher publish server.json
   ```

4. Don't commit the stamped `server.json`: `main` keeps the placeholders.

## If something goes wrong

- **The workflow failed** for a transient reason: re-run it from the Actions tab. It's safe to re-run, and it pushes the same tags again. For the registry step, see [Re-running the registry step](#re-running-the-registry-step).
- **The release was wrong** (bad code or wrong tag): don't move or reuse a published tag, because people may already have pulled it. Fix the problem on `main` and publish the next patch version. You can delete the bad GitHub release and mark it as such in the changelog, but leave the image tag alone.
- **Wrong tag format** (e.g. `1.2.0` without the `v`): delete the release and tag (`gh release delete 1.2.0 --cleanup-tag`) and publish again with the right tag.

## Repository setup

### Rulesets

Two rulesets should protect the repository. Create them under **Settings → Rules → Rulesets** when setting up the repository (or a fresh copy), and edit them there.

**Protect main** (the default branch):

- Changes land only through pull requests. Nobody can push directly, force-push or delete the branch, and there are no bypasses.
- The CI checks must pass (tests, lint, the generated-docs check, govulncheck and the Docker image build), and the branch must be up to date with `main`.
- Review threads must be resolved before merging. The only merge method is squash.
- No approving review is required yet, because GitHub doesn't let authors approve their own pull requests and the project has a single maintainer. Raise this when there are more maintainers.

**Protect release tags** (`v*`):

- Only repository admins can create, move or delete version tags, so only maintainers can publish releases. Published versions can't be re-pointed.

> [!NOTE]
> Required checks are matched by job name. If you rename a job in [`ci.yml`](../.github/workflows/ci.yml), update the *Protect main* ruleset in the same change. Otherwise pull requests wait forever for a check that never reports.

### Container package visibility

New GHCR packages normally start private, and GitHub doesn't offer an API to change this. A package linked to a public repository usually becomes public automatically. If it starts private, open the package page (**Your profile → Packages → hive-mcp → Package settings**) and choose **Change visibility → Public** under **Danger Zone**.

The package links to the repository automatically through the image's `org.opencontainers.image.source` label, which makes it show in the repository sidebar.
