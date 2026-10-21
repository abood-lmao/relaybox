# syntax=docker/dockerfile:1

# 1. Dashboard
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# Vite writes to ../internal/webui/static/dist
RUN mkdir -p /src/internal/webui/static && npm run build

# 2. Binary
FROM golang:1.27-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webui/static/dist internal/webui/static/dist
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN go build -trimpath \
      -ldflags "-s -w -X github.com/abood-lmao/relaybox/internal/version.Version=${VERSION} -X github.com/abood-lmao/relaybox/internal/version.Commit=${COMMIT} -X github.com/abood-lmao/relaybox/internal/version.Date=${DATE}" \
      -o /out/relaybox ./cmd/relaybox \
 && mkdir -p /out/data

# 3. Runtime: no shell, no package manager, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/relaybox /relaybox
COPY --from=build --chown=65532:65532 /out/data /data
ENV RELAYBOX_HOST=0.0.0.0 \
    RELAYBOX_PORT=8787 \
    RELAYBOX_DATABASE_PATH=/data/relaybox.db
VOLUME ["/data"]
EXPOSE 8787
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/relaybox", "healthcheck"]
ENTRYPOINT ["/relaybox"]
CMD ["serve"]
