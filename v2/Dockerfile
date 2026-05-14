FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY v2/ .
ARG GIT_HASH=unknown
ARG GIT_SHORT=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.gitHash=${GIT_HASH} -X main.gitShort=${GIT_SHORT}" -o /hive ./cmd/hive

FROM node:24-slim

# --- Layer 1: system packages (rarely changes) ---
RUN apt-get update && apt-get install -y --no-install-recommends \
    git ca-certificates bash curl tmux bzip2 \
 && rm -rf /var/lib/apt/lists/*

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
    curl -sL -o /tmp/goose.tar.gz \
      "https://github.com/block/goose/releases/latest/download/goose-${GOOSE_ARCH}-unknown-linux-gnu.tar.gz" && \
    tar -xzf /tmp/goose.tar.gz -C /usr/local/bin/ && \
    rm /tmp/goose.tar.gz && \
    chmod +x /usr/local/bin/goose

# --- Layer 4: IBM Bob Shell (moderate release cadence) ---
ARG BOB_VERSION=latest
RUN curl -fsSL --connect-timeout 10 https://bob.ibm.com/download/bobshell.sh | bash -s -- --pm npm || echo "WARN: bob shell install skipped (network unavailable)"

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

COPY examples/kubestellar/hive-project.yaml /etc/hive/hive-project.yaml
COPY v2/deploy/data/ /opt/hive/seed-data/
COPY v2/deploy/ttyd-tmux.sh /usr/local/bin/ttyd-tmux.sh
COPY bin/gh-app-token.sh bin/hive-config.sh bin/agent-launch.sh /usr/local/bin/
COPY bin/gh-wrapper.sh /usr/local/bin/gh
RUN chmod +x /usr/local/bin/ttyd-tmux.sh /usr/local/bin/gh-app-token.sh \
    /usr/local/bin/hive-config.sh /usr/local/bin/agent-launch.sh /usr/local/bin/gh

# --- Nous framework (strategist experiment engine) ---
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 python3-venv python3-pip jq && rm -rf /var/lib/apt/lists/*
COPY bin/nous-runner.sh bin/nous-sync.py bin/nous-hive-gate.py bin/nous-install.sh /tmp/hive/bin/
RUN chmod +x /tmp/hive/bin/*.sh
COPY examples/kubestellar/nous-campaign.yaml examples/kubestellar/nous-governor-campaign.yaml examples/kubestellar/nous-repo-campaign.yaml /etc/hive/
RUN mkdir -p /var/run/nous/governor /var/run/nous/repo /var/run/nous/snapshots /opt/nous
RUN git clone https://github.com/AI-native-Systems-Research/agentic-strategy-evolution /opt/nous && \
    python3 -m venv /opt/nous/venv && \
    /opt/nous/venv/bin/pip install --no-cache-dir -e /opt/nous 2>&1 || \
    echo "WARN: Nous pip install failed — run_campaign.py may not work"

EXPOSE 3001 3002 7681
VOLUME ["/data", "/secrets"]

COPY v2/deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["entrypoint.sh"]
CMD ["--config", "/etc/hive/hive.yaml"]
