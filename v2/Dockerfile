FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY v2/ .
ARG GIT_HASH=unknown
ARG GIT_SHORT=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.gitHash=${GIT_HASH} -X main.gitShort=${GIT_SHORT}" -o /hive ./cmd/hive

FROM node:24-alpine

# --- Layer 1: system packages (rarely changes) ---
RUN apk add --no-cache git ca-certificates bash curl tzdata tmux libuv bzip2

# --- Layer 2: ttyd (stable, infrequent releases) ---
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then TTYD_ARCH=x86_64; \
    elif [ "$ARCH" = "aarch64" ]; then TTYD_ARCH=aarch64; \
    else TTYD_ARCH=x86_64; fi && \
    curl -sL -o /usr/local/bin/ttyd \
      "https://github.com/tsl0922/ttyd/releases/latest/download/ttyd.${TTYD_ARCH}" && \
    chmod +x /usr/local/bin/ttyd

# --- Layer 3: Goose CLI (Rust binary, moderate release cadence) ---
ARG GOOSE_VERSION=latest
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH=x86_64; \
    elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH=aarch64; \
    else GOOSE_ARCH=x86_64; fi && \
    curl -sL -o /tmp/goose.tar.bz2 \
      "https://github.com/block/goose/releases/latest/download/goose-${GOOSE_ARCH}-unknown-linux-musl.tar.bz2" && \
    tar -xjf /tmp/goose.tar.bz2 -C /usr/local/bin/ && \
    rm /tmp/goose.tar.bz2 && \
    chmod +x /usr/local/bin/goose

# --- Layer 4: IBM Bob Shell (moderate release cadence) ---
ARG BOB_VERSION=latest
RUN curl -fsSL https://bob.ibm.com/download/bobshell.sh | bash -s -- --pm npm

# --- Layer 5: Gemini CLI (moderate release cadence) ---
ARG GEMINI_CLI_VERSION=latest
RUN npm install -g @google/gemini-cli@${GEMINI_CLI_VERSION} && npm cache clean --force

# --- Layer 6: GitHub Copilot CLI (frequent updates) ---
ARG COPILOT_VERSION=latest
RUN npm install -g @github/copilot@${COPILOT_VERSION} && npm cache clean --force

# --- Layer 7: Claude Code (most frequent updates) ---
ARG CLAUDE_CODE_VERSION=latest
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} && npm cache clean --force

# --- Application layers ---
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
