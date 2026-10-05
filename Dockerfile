# syntax=docker/dockerfile:1.6
#
# chora-companion-chat Dockerfile — Go service (subscriber-only ADK agent).
#
# Build context = this repository. The service is standalone; shared Chora
# modules (chora-adk-common, chora-common, chora-contracts) are resolved through
# the Go module proxy, not a workspace.
#
# Standard invocation:
#   docker buildx build --platform=linux/amd64 \
#     -f Dockerfile \
#     --build-arg SERVICE_NAME=chora-companion-chat \
#     --build-arg GIT_SHA=$(git rev-parse --short HEAD) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#     -t walfa/chora-companion-chat:latest \
#     .

ARG GO_VERSION=1.26.6
ARG ALPINE_VERSION=3.23
ARG SERVICE_NAME=chora-companion-chat
ARG GIT_SHA=unknown
ARG BUILD_TIME=unknown

############################
# Stage 1 — build + test
############################
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY . .

RUN go mod download

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# Build the binary and run the suite in the builder: an image that ships with
# a failing test is worse than no image.
RUN go build -trimpath -ldflags "-s -w" -o /out/companion_chat ./cmd/companion_chat \
    && go vet ./... && go test ./...

############################
# Stage 2 — runtime
############################
FROM alpine:${ALPINE_VERSION}

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S -G app app

LABEL org.opencontainers.image.title="${SERVICE_NAME}" \
      org.opencontainers.image.source="https://github.com/apollo-chora/chora-companion-chat" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.vendor="Chora Platform" \
      org.opencontainers.image.licenses="UNLICENSED" \
      io.chora.service="${SERVICE_NAME}" \
      io.chora.git-sha="${GIT_SHA}" \
      io.chora.build-time="${BUILD_TIME}"

WORKDIR /

COPY --from=builder /out/companion_chat /companion_chat

# ADR-254 D6: subscriber-only binary, NO arguments. The dispatch subscriber is
# its only transport; a subscriber that cannot start exits non-zero.
USER app:app
ENTRYPOINT ["/companion_chat"]
