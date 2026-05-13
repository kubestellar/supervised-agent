FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY v2/ .
ARG GIT_HASH=unknown
ARG GIT_SHORT=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.gitHash=${GIT_HASH} -X main.gitShort=${GIT_SHORT}" -o /hive ./cmd/hive

FROM node:24-alpine

ARG CLAUDE_CODE_VERSION=latest

RUN apk add --no-cache git ca-certificates bash curl tzdata tmux libuv \
 && npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
 && npm install -g @github/copilot \
 && npm install -g @google/gemini-cli \
 && npm cache clean --force

# IBM Bob Shell (curl installer)
RUN curl -fsSL https://bob.ibm.com/download/bobshell.sh | bash

# Goose CLI (Rust binary — not available via npm)
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH=x86_64; \
    elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH=aarch64; \
    else GOOSE_ARCH=x86_64; fi && \
    curl -sL -o /tmp/goose.tar.bz2 \
      "https://github.com/block/goose/releases/latest/download/goose-${GOOSE_ARCH}-unknown-linux-musl.tar.bz2" && \
    tar -xjf /tmp/goose.tar.bz2 -C /usr/local/bin/ && \
    rm /tmp/goose.tar.bz2 && \
    chmod +x /usr/local/bin/goose

RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then TTYD_ARCH=x86_64; \
    elif [ "$ARCH" = "aarch64" ]; then TTYD_ARCH=aarch64; \
    else TTYD_ARCH=x86_64; fi && \
    curl -sL -o /usr/local/bin/ttyd \
      "https://github.com/tsl0922/ttyd/releases/latest/download/ttyd.${TTYD_ARCH}" && \
    chmod +x /usr/local/bin/ttyd

ENV HOME=/data/home
RUN mkdir -p /data/home

COPY --from=builder /hive /usr/local/bin/hive

COPY v2/proxy/ /opt/hive/proxy/
RUN cd /opt/hive/proxy && npm install --omit=dev

COPY v2/pkg/dashboard/static/index.html /opt/hive/proxy/public/

COPY v2/deploy/ttyd-tmux.sh /usr/local/bin/ttyd-tmux.sh
RUN chmod +x /usr/local/bin/ttyd-tmux.sh

EXPOSE 3001 3002 7681
VOLUME ["/data", "/secrets"]

COPY v2/deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["entrypoint.sh"]
CMD ["--config", "/etc/hive/hive.yaml"]
