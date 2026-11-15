# syntax=docker/dockerfile:1

# The demo program's image, built from the repository root.
#
# The build context is the root rather than this directory because the image
# compiles two workspaces that sit outside it: the frontend package workspace
# under web/ (whose members the demo frontend resolves from source) and the Go
# workspace go.work declares, which holds pkg/ and the program's own module.
# `flyctl deploy .` passes the root as the working directory and points at
# fly.toml beside this file; the Dockerfile path in that file is relative to
# the configuration file.
#
# The build order is the one the repository fixes: the frontend first, because
# the Go program embeds its output with go:embed.

FROM node:26-bookworm-slim AS web

WORKDIR /src

# The workspace root and its external member (starters/demo/web). Both trees
# are needed before install: the workspace lists the member, and the member's
# dependencies on @speed/* are resolved through the workspace's alias map at
# build time.
COPY web/ /src/web/
COPY starters/demo/web/ /src/starters/demo/web/

# The workspace's package manager, at the version web/package.json pins. The
# version is read from that file rather than repeated here, so the one
# authority the repository keeps for it stays the only one. It is installed
# through npm rather than corepack, whose presence in a Node release is not a
# contract this file can rest on.
RUN PNPM_VERSION="$(node -e 'process.stdout.write(require("/src/web/package.json").packageManager.replace(/^pnpm@/, ""))')" \
    && npm install --global "pnpm@${PNPM_VERSION}"

RUN pnpm -C /src/web install --frozen-lockfile

# vite writes the bundle into ../server/dist, the directory the Go program
# embeds. The directory does not exist in this stage's copy of the tree, so it
# is created here rather than left to the bundler. The build is selected from
# the workspace root: the demo host is an external member, so pnpm resolves its
# dependencies against the workspace's one lockfile only when the root is the
# directory pnpm runs from.
RUN mkdir -p /src/starters/demo/server/dist \
    && pnpm -C /src/web --filter demo-web run build

FROM golang:1.27.0-bookworm AS build

WORKDIR /src

COPY go.work /src/go.work
COPY pkg/ /src/pkg/
COPY starters/demo/server/ /src/starters/demo/server/
COPY --from=web /src/starters/demo/server/dist/ /src/starters/demo/server/dist/

WORKDIR /src/starters/demo/server

# CGO is off: the SQLite driver is pure Go, and a static binary leaves the
# runtime image with no libc of its own to carry. go.work.sum is not committed
# -- it is a workspace-mode artifact -- and the Go toolchain writes it during
# this build.
# The image is the default build. Every capability -- the social login methods,
# the SMS family, the mail carriers and the verification code sender -- is
# compiled in; which ones start is decided at run time from the configuration
# this image's entrypoint renders. The relay's values come from Fly secrets,
# never baked into this layer. The review-only test controls and loopback guard
# are not part of this build.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/demo .

FROM alpine:3.24

# su-exec drops from the entrypoint's root to the unprivileged user that runs
# the program, after the entrypoint has taken ownership of the mounted volume.
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -S -g 10001 demo \
    && adduser -S -D -H -u 10001 -G demo demo

COPY --from=build /out/demo /usr/local/bin/demo
COPY starters/demo/deploy/ /app/deploy/
RUN chmod 0755 /app/deploy/entrypoint.sh

# The program resolves the token signing key, the object store's root and the
# SQLite file against its working directory, and its first-run key generation
# writes var/ before any configuration is read. The working directory is
# therefore the mounted volume, so all three outlive the container.
WORKDIR /data

ENTRYPOINT ["/app/deploy/entrypoint.sh"]
