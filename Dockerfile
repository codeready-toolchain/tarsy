# Stage 1: Build Go binary
FROM mirror.gcr.io/library/golang:1.25-alpine AS go-builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/
COPY ent/ ent/
COPY proto/ proto/

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X github.com/codeready-toolchain/tarsy/pkg/version.gitCommitOverride=${VERSION}" \
    -o /tarsy ./cmd/tarsy

# Stage 2: Build dashboard
FROM mirror.gcr.io/library/node:24-alpine AS dashboard-builder

WORKDIR /build

COPY web/dashboard/package*.json ./
RUN npm ci --include=dev

COPY web/dashboard/ .

ARG VERSION=dev
ENV VITE_APP_VERSION=${VERSION}

RUN npm run build

# Stage 3: Runtime image
FROM mirror.gcr.io/library/alpine:3.21 AS runtime

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -g 65532 -S tarsy \
    && adduser -u 65532 -S tarsy -G tarsy -s /sbin/nologin

WORKDIR /app

COPY --from=go-builder /tarsy /app/bin/tarsy
COPY --from=dashboard-builder /build/dist /app/dashboard

RUN mkdir -p /app/config /app/data \
    && chown -R tarsy:tarsy /app \
    && chgrp -R 0 /app && chmod -R g=u /app

USER 65532:65532

ENV HOME=/app/data \
    HTTP_PORT=8080 \
    DASHBOARD_DIR=/app/dashboard \
    CONFIG_DIR=/app/config \
    LLM_SERVICE_ADDR=llm-service:50051

EXPOSE 8080

CMD ["/app/bin/tarsy", "--config-dir=/app/config", "--dashboard-dir=/app/dashboard"]
