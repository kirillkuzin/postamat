# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS recipient-assets
WORKDIR /src/web/recipient
COPY web/recipient/package.json web/recipient/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/recipient/ ./
RUN npm run build

FROM golang:1.26.3-bookworm AS go-builder
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=recipient-assets /src/web/recipient/dist ./web/recipient/dist
RUN go build -o /out/postamat-server ./cmd/server && \
    go build -o /out/postamat-agentd ./cmd/agentd && \
    go build -o /out/postamat ./cmd/postamat

FROM alpine:3.22
RUN addgroup -S postamat && adduser -S -G postamat postamat
WORKDIR /app
COPY --from=go-builder /out/postamat-server /usr/local/bin/postamat-server
COPY --from=go-builder /out/postamat-agentd /usr/local/bin/postamat-agentd
COPY --from=go-builder /out/postamat /usr/local/bin/postamat
ENV POSTAMAT_HTTP_ADDR=:8080
EXPOSE 8080
USER postamat
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/postamat-server"]
