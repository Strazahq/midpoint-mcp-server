# Build a fully static binary, then ship it on scratch.
#
# The build stage runs on the build machine's own platform and cross-compiles
# for the target platform, so a multi-arch build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off → static, pure-Go binary that runs on scratch.
ARG TARGETOS TARGETARCH
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" -o /out/midpoint-mcp-server .

# Runtime: scratch (nothing but the binary and CA certs).
FROM scratch

# CA certificates so the server can reach midPoint over HTTPS.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/midpoint-mcp-server /midpoint-mcp-server

# Run unprivileged (scratch has no users; use the conventional "nobody" uid).
USER 65534:65534

# Default transport is stdio (personal mode). Configuration is via MIDPOINT_*
# environment variables at runtime. --http accepts a bare port or a loopback
# address without OIDC, and any other address only when MIDPOINT_MCP_OIDC_ISSUER
# and MIDPOINT_MCP_OIDC_AUDIENCE are both set. A bare port binds 127.0.0.1,
# which inside a container is the container's own loopback. To publish the port,
# pass an address such as 0.0.0.0:3001 with both OIDC settings, or use host
# networking for personal mode.
ENTRYPOINT ["/midpoint-mcp-server"]
