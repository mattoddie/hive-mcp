# syntax=docker/dockerfile:1

# The binary is static Go, so the build stage runs on the build machine's own
# platform and cross-compiles: multi-arch builds don't need emulation.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go test ./...
# Release builds pass the version from the Git tag (e.g. 1.2.3).
ARG VERSION=0.0.0-dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/mattoddie/hive-mcp/internal/tools.Version=${VERSION}" \
      -o /out/hive-mcp ./cmd/hive-mcp \
 && mkdir -p /out/data

# Distroless: no shell, no package manager, just CA certificates, tzdata and a
# non-root user (65532), so there is very little for CVE scanners to find.
FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.title="hive-mcp" \
      org.opencontainers.image.description="Unofficial MCP server for Hive smart heating" \
      org.opencontainers.image.source="https://github.com/mattoddie/hive-mcp" \
      org.opencontainers.image.documentation="https://hive-mcp.mattoddie.dev/docs/" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/hive-mcp /hive-mcp
# The login lives here (tokens and the remembered device); mount a volume so it survives restarts.
COPY --from=build --chown=65532:65532 /out/data /data
ENV MCP_TRANSPORT=http \
    MCP_HTTP_HOST=0.0.0.0 \
    MCP_HTTP_PORT=8080 \
    HIVE_TOKEN_FILE=/data/tokens.json
USER nonroot:nonroot
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/hive-mcp", "healthcheck"]
ENTRYPOINT ["/hive-mcp"]
