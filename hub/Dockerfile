# agent-keepalive-hub image. Build from the repository root:
#   docker build -f hub/Dockerfile --build-arg VERSION=1.4.0 -t agent-keepalive-hub .

FROM node:24-alpine AS web
WORKDIR /src/hub/web
COPY hub/web/package.json hub/web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY hub/web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY cmd/agent-keepalive-hub/ cmd/agent-keepalive-hub/
COPY hub/web/embed.go hub/web/embed.go
COPY --from=web /src/hub/web/dist/ hub/web/dist/
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/agent-keepalive-hub ./cmd/agent-keepalive-hub && \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/agent-keepalive-hub /usr/local/bin/agent-keepalive-hub
COPY --from=go --chown=nonroot:nonroot /out/data /data
ENV HUB_DATA_DIR=/data \
    HUB_LISTEN=:8080
EXPOSE 8080
VOLUME /data
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/agent-keepalive-hub", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/agent-keepalive-hub"]
CMD ["serve"]
