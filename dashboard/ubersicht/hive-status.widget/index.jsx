// Hive Status — Übersicht Widget
// Install: copy hive-status.widget/ to ~/Library/Application Support/Übersicht/widgets/
// Requires: hive dashboard running at HIVE_URL below

const HIVE_URL = "http://192.168.4.56:3001";
const REFRESH_MS = 5000;

export const refreshFrequency = REFRESH_MS;

export const command = `curl -sf ${HIVE_URL}/api/status 2>/dev/null || echo '{"error":true}'`;

const stateColor = (s) => {
  if (s === "running") return "#22c55e";
  if (s === "idle" || s === "stopped") return "#64748b";
  return "#eab308";
};

const busyIcon = (b) => {
  if (b === "working") return "🔄";
  if (b === "idle") return "💤";
  return "⏸";
};

const govColor = (g) => (g === "active" ? "#22c55e" : "#ef4444");

export const render = ({ output }) => {
  let data;
  try {
    data = JSON.parse(output);
  } catch {
    return <div style={styles.container}><span style={styles.err}>⚠ parse error</span></div>;
  }

  if (data.error) {
    return (
      <div style={styles.container}>
        <div style={styles.header}>🐝 HIVE</div>
        <span style={styles.err}>dashboard offline</span>
      </div>
    );
  }

  const agents = data.agents || [];
  const gov = data.governor || {};
  const repo = data.repo || {};

  return (
    <div style={styles.container}>
      <div style={styles.header}>
        🐝 HIVE
        <span style={{ ...styles.govBadge, background: govColor(gov.timer) }}>
          GOV {gov.timer || "?"}
        </span>
        {gov.mode && <span style={styles.mode}>{gov.mode}</span>}
      </div>

      <div style={styles.grid}>
        {agents.map((a) => (
          <div key={a.name} style={styles.agent}>
            <div style={styles.agentHeader}>
              <span style={{ ...styles.dot, background: stateColor(a.state) }} />
              <span style={styles.agentName}>{a.name}</span>
              <span style={styles.busyIcon}>{busyIcon(a.busy)}</span>
            </div>
            <div style={styles.agentMeta}>
              {a.cli} · {a.cadence}
            </div>
            {a.doing && <div style={styles.doing}>{a.doing}</div>}
          </div>
        ))}
      </div>

      <div style={styles.footer}>
        <span>📋 {repo.openIssues ?? "?"} issues</span>
        <span style={styles.sep}>·</span>
        <span>🔀 {repo.openPRs ?? "?"} PRs</span>
        {gov.queueDepth != null && (
          <>
            <span style={styles.sep}>·</span>
            <span>📥 {gov.queueDepth} queued</span>
          </>
        )}
      </div>
    </div>
  );
};

const styles = {
  container: {
    position: "fixed",
    bottom: 20,
    left: 20,
    width: 280,
    background: "rgba(15, 15, 25, 0.85)",
    backdropFilter: "blur(12px)",
    WebkitBackdropFilter: "blur(12px)",
    borderRadius: 12,
    border: "1px solid rgba(255,255,255,0.08)",
    padding: 14,
    fontFamily: "'SF Mono', 'JetBrains Mono', 'Fira Code', monospace",
    fontSize: 11,
    color: "#e2e8f0",
    lineHeight: 1.4,
    zIndex: 9999,
  },
  header: {
    display: "flex",
    alignItems: "center",
    gap: 8,
    fontSize: 13,
    fontWeight: 700,
    marginBottom: 10,
    letterSpacing: 1,
  },
  govBadge: {
    fontSize: 9,
    padding: "2px 6px",
    borderRadius: 4,
    color: "#fff",
    fontWeight: 600,
    marginLeft: "auto",
  },
  mode: {
    fontSize: 9,
    color: "#94a3b8",
    textTransform: "uppercase",
  },
  grid: {
    display: "grid",
    gridTemplateColumns: "1fr 1fr",
    gap: 6,
  },
  agent: {
    background: "rgba(255,255,255,0.04)",
    borderRadius: 6,
    padding: "6px 8px",
  },
  agentHeader: {
    display: "flex",
    alignItems: "center",
    gap: 4,
  },
  dot: {
    width: 6,
    height: 6,
    borderRadius: "50%",
    display: "inline-block",
    flexShrink: 0,
  },
  agentName: {
    fontWeight: 600,
    fontSize: 10,
    textTransform: "capitalize",
  },
  busyIcon: {
    marginLeft: "auto",
    fontSize: 10,
  },
  agentMeta: {
    fontSize: 9,
    color: "#64748b",
    marginTop: 2,
  },
  doing: {
    fontSize: 9,
    color: "#facc15",
    marginTop: 2,
    whiteSpace: "nowrap",
    overflow: "hidden",
    textOverflow: "ellipsis",
  },
  footer: {
    marginTop: 10,
    fontSize: 10,
    color: "#94a3b8",
    display: "flex",
    alignItems: "center",
    gap: 4,
  },
  sep: {
    color: "#475569",
  },
  err: {
    color: "#ef4444",
    fontSize: 11,
  },
};
