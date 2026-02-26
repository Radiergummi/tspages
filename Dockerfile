# syntax=docker/dockerfile:1
FROM node:24-alpine AS frontend
WORKDIR /src

COPY --link package.json package-lock.json ./

RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY --link vite.config.ts tsconfig.json ./
COPY --link web/ web/

RUN npx vite build

FROM golang:1.25-alpine AS build
WORKDIR /src

COPY --link go.mod go.sum ./
COPY --link "cmd/" "cmd/"
COPY --link config/ config/
COPY --link internal/ internal/
COPY --link --from=frontend /src/internal/admin/assets/dist internal/admin/assets/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
 go build -o /tspages "./cmd/tspages"

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /tspages /usr/local/bin/tspages

ENV TSPAGES_HEALTH_ADDR=":9091"
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:9091/healthz || exit 1

VOLUME ["/data", "/state"]
ENTRYPOINT ["tspages"]
