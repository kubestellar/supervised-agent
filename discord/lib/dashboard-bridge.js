const http = require('http');
const { agentMessage } = require('./formatter');

const SSE_RECONNECT_BASE_MS = 5000;
const SSE_RECONNECT_MAX_MS = 60000;
const DASHBOARD_STALE_WARN_MS = 30000;

class DashboardBridge {
  constructor(config, sendMessage, sendEmbed) {
    this.config = config;
    this.sendMessage = sendMessage;
    this.sendEmbed = sendEmbed;
    this.lastState = null;
    this.reconnectDelay = SSE_RECONNECT_BASE_MS;
    this.staleTimer = null;
    this.firstEvent = true;
  }

  start() {
    this._connect();
  }

  stop() {
    if (this.req) {
      this.req.destroy();
      this.req = null;
    }
    clearTimeout(this.staleTimer);
  }

  _connect() {
    const url = new URL('/api/events', this.config.dashboardUrl);
    const req = http.get(url, (res) => {
      if (res.statusCode !== 200) {
        res.resume();
        this._scheduleReconnect();
        return;
      }

      this.reconnectDelay = SSE_RECONNECT_BASE_MS;
      this._resetStaleTimer();
      let buffer = '';

      res.on('data', (chunk) => {
        buffer += chunk.toString();
        const lines = buffer.split('\n\n');
        buffer = lines.pop();
        for (const block of lines) {
          const dataLine = block.split('\n').find(l => l.startsWith('data:'));
          if (dataLine) {
            try {
              const data = JSON.parse(dataLine.slice(5).trim());
              this._onEvent(data);
              this._resetStaleTimer();
            } catch (_) { /* ignore parse errors */ }
          }
        }
      });

      res.on('end', () => this._scheduleReconnect());
      res.on('error', () => this._scheduleReconnect());
    });

    req.on('error', () => this._scheduleReconnect());
    this.req = req;
  }

  _scheduleReconnect() {
    setTimeout(() => this._connect(), this.reconnectDelay);
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, SSE_RECONNECT_MAX_MS);
  }

  _resetStaleTimer() {
    clearTimeout(this.staleTimer);
    this.staleTimer = setTimeout(() => {
      this.sendMessage(agentMessage('pipeline', '⚠️ Dashboard SSE connection lost — commands may not route'), 'alerts');
    }, DASHBOARD_STALE_WARN_MS);
  }

  _onEvent(data) {
    if (this.firstEvent) {
      this.lastState = data;
      this.firstEvent = false;
      return;
    }

    if (!this.lastState) {
      this.lastState = data;
      return;
    }

    this._diffAgents(data);
    this._diffGovernor(data);
    this.lastState = data;
  }

  _diffAgents(data) {
    if (!this.config.postAgentTransitions) return;
    const agents = Array.isArray(data.agents) ? data.agents : [];
    const prevAgents = Array.isArray(this.lastState.agents) ? this.lastState.agents : [];
    const prevMap = {};
    for (const a of prevAgents) { if (a.name) prevMap[a.name] = a; }

    for (const agent of agents) {
      const name = agent.name;
      if (!name) continue;
      const old = prevMap[name];
      if (!old) continue;

      if (old.busy !== agent.busy) {
        const doing = agent.doing ? ` — ${agent.doing.slice(0, 100)}` : '';
        if (agent.busy === 'idle' && old.busy === 'working') {
          const summary = (agent.liveSummary || '').split('\n').slice(0, 3).join('\n').slice(0, 300);
          this.sendMessage(agentMessage(name, `Completed${doing}${summary ? '\n```\n' + summary + '\n```' : ''}`));
        } else if (agent.busy === 'working' && old.busy === 'idle') {
          this.sendMessage(agentMessage(name, `Working${doing}`));
        } else if (agent.cadence === 'paused' && old.cadence !== 'paused') {
          this.sendMessage(agentMessage(name, 'Paused'));
        }
      }

      const oldSummary = (old.liveSummary || '').trim();
      const newSummary = (agent.liveSummary || '').trim();
      if (newSummary && newSummary !== oldSummary) {
        const lines = newSummary.split('\n').slice(0, 4).join('\n').slice(0, 400);
        this.sendMessage(agentMessage(name, `\n\`\`\`\n${lines}\n\`\`\``));
      }
    }
  }

  _diffGovernor(data) {
    if (!this.config.postGovernorModeChanges) return;
    const gov = data.governor || {};
    const prevGov = this.lastState.governor || {};
    const govMode = gov.mode || '';
    const prevMode = prevGov.mode || '';

    if (govMode && prevMode && govMode !== prevMode) {
      const { governorEmbed } = require('./formatter');
      const queueDepth = (gov.issues || 0) + (gov.prs || 0);
      const agentStates = {};
      for (const agent of (Array.isArray(data.agents) ? data.agents : [])) {
        if (agent.name) agentStates[agent.name] = agent.busy || agent.state || 'unknown';
      }
      this.sendEmbed(governorEmbed(`${prevMode} → ${govMode}`, queueDepth, agentStates), 'alerts');
    }
  }
}

module.exports = { DashboardBridge };
