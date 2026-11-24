# syntax=docker/dockerfile:1


FROM node:26-bookworm-slim AS web

WORKDIR /src

COPY web/ /src/web/
COPY starters/demo/web/ /src/starters/demo/web/

RUN PNPM_VERSION="$(node -e 'process.stdout.write(require("/src/web/package.json").packageManager.replace(/^pnpm@/, ""))')" \
    && npm install --global "pnpm@${PNPM_VERSION}"

RUN pnpm -C /src/web install --frozen-lockfile

RUN mkdir -p /src/starters/demo/server/dist \
    && pnpm -C /src/web --filter demo-web --fail-if-no-match run build

FROM golang:1.27.4-bookworm AS build

WORKDIR /src

COPY go.work /src/go.work
COPY pkg/ /src/pkg/
COPY starters/demo/server/ /src/starters/demo/server/
COPY --from=web /src/starters/demo/server/dist/ /src/starters/demo/server/dist/

WORKDIR /src/starters/demo/server

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/demo .

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -S -g 10001 demo \
    && adduser -S -D -H -u 10001 -G demo demo

COPY --from=build /out/demo /usr/local/bin/demo
COPY starters/demo/deploy/ /app/deploy/
RUN chmod 0755 /app/deploy/entrypoint.sh

WORKDIR /data

ENTRYPOINT ["/app/deploy/entrypoint.sh"]
