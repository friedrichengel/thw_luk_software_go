# syntax=docker/dockerfile:1
#
# THW LuK Stabsansicht (Go) - Container-Image: ein statisch gelinktes Binary
# auf Alpine, laeuft als unprivilegierter Nutzer "luk". TLS gehoert nicht ins
# Image: davor sitzt ein Reverse-Proxy des Hosts oder der cloudflared-Sidecar
# aus docker/compose.yml.
#
# Build (aus dem Repo-Wurzelverzeichnis):
#   docker build -f docker/Dockerfile -t ghcr.io/friedrichengel/thw_luk_software_go:dev \
#       --build-arg APP_VERSION=$(grep ^APP_VERSION= config/.env.sample | cut -d= -f2) \
#       --build-arg GIT_SHA=$(git rev-parse --short HEAD) .
# Komfortabler: docker/make-archive.sh (baut, speichert als Archiv, signiert).
#
# Volumes: /app/data (SQLite, Secret, STEIN-Cache), /app/updates (Update-Kanal).
# Konfiguration ausschliesslich ueber Umgebungsvariablen (siehe compose.yml).
# Beim Start legt `luk serve` Datenbank und - wenn noch kein Nutzer existiert -
# den Admin an (Initialpasswort in /app/data/admin-initialpasswort.txt, das
# setup.sh anzeigt und loescht).

FROM golang:1.27-alpine AS build

ARG APP_VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_DATE=

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w \
        -X github.com/friedrichengel/thw_luk_software_go/internal/config.Version=${APP_VERSION} \
        -X github.com/friedrichengel/thw_luk_software_go/internal/config.BuildStamp=${GIT_SHA} \
        -X github.com/friedrichengel/thw_luk_software_go/internal/config.BuildDate=${BUILD_DATE}" \
      -o /out/luk ./cmd/luk

FROM alpine:3.21

ARG APP_VERSION=dev
ARG APP_COPYRIGHT_YEAR=2026
ARG GIT_SHA=unknown
ARG BUILD_DATE=

LABEL org.opencontainers.image.title="THW LuK Stabsansicht" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/friedrichengel/thw_luk_software_go"

RUN apk add --no-cache ca-certificates tzdata su-exec \
 && addgroup -g 1000 luk && adduser -D -u 1000 -G luk -h /app luk \
 && mkdir -p /app/data /app/updates \
 && chown -R luk:luk /app

WORKDIR /app
COPY --from=build /out/luk /app/luk
# docker/ liegt mit im Image: setup.sh und der Host-Dienst holen sich daraus
# compose.yml und luk-updater.sh in der zum Image passenden Fassung.
COPY docker /app/docker
COPY docker/entrypoint.sh /usr/local/bin/luk-entrypoint
RUN chmod +x /app/luk /usr/local/bin/luk-entrypoint /app/docker/setup.sh /app/docker/host/luk-updater.sh /app/docker/make-archive.sh \
 && printf '%s %s\n' "${GIT_SHA}" "${BUILD_DATE}" > /app/.deployed_version

ENV APP_VERSION=${APP_VERSION} \
    APP_COPYRIGHT_YEAR=${APP_COPYRIGHT_YEAR} \
    LISTEN_ADDR=0.0.0.0:80 \
    DATA_DIR=/app/data \
    UPDATE_DIR=/app/updates \
    UPDATE_MODE=docker \
    TRUSTED_PROXIES=127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7 \
    TZ=Europe/Berlin

EXPOSE 80
VOLUME ["/app/data", "/app/updates"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/app/luk", "healthcheck"]

ENTRYPOINT ["luk-entrypoint"]
CMD ["serve"]
