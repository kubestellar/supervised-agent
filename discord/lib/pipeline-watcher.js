const fs = require('fs');
const path = require('path');
const { mergeGateEmbed, conflictSweepEmbed, agentMessage } = require('./formatter');

const DEBOUNCE_MS = 10000;
const POLL_INTERVAL_MS = 30000;

class PipelineWatcher {
  constructor(config, sendMessage, sendEmbed) {
    this.config = config;
    this.sendMessage = sendMessage;
    this.sendEmbed = sendEmbed;
    this.metricsDir = config.metricsDir;
    this.lastMergeGate = null;
    this.lastConflictSweep = null;
    this.lastQueueCount = null;
    this.debounceTimers = {};
    this.watchers = [];
    this.pollTimer = null;
  }

  start() {
    this._watchFile('merge-eligible.json', () => this._onMergeGate());
    this._watchFile('conflict-sweep.json', () => this._onConflictSweep());
    this._watchFile('actionable.json', () => this._onQueueChange());

    this.pollTimer = setInterval(() => {
      this._onMergeGate();
      this._onConflictSweep();
      this._onQueueChange();
    }, POLL_INTERVAL_MS);
  }

  stop() {
    for (const w of this.watchers) w.close();
    this.watchers = [];
    clearInterval(this.pollTimer);
    for (const t of Object.values(this.debounceTimers)) clearTimeout(t);
  }

  _watchFile(filename, handler) {
    const filePath = path.join(this.metricsDir, filename);
    try {
      const watcher = fs.watch(filePath, () => {
        clearTimeout(this.debounceTimers[filename]);
        this.debounceTimers[filename] = setTimeout(handler, DEBOUNCE_MS);
      });
      this.watchers.push(watcher);
    } catch (_) {
      // file may not exist yet — poll will catch it
    }
  }

  _readJson(filename) {
    try {
      const raw = fs.readFileSync(path.join(this.metricsDir, filename), 'utf8');
      return JSON.parse(raw);
    } catch (_) {
      return null;
    }
  }

  _onMergeGate() {
    if (!this.config.postPipelineResults) return;
    const data = this._readJson('merge-eligible.json');
    if (!data) return;

    const ts = data.generated_at || '';
    if (ts === this.lastMergeGate) return;
    this.lastMergeGate = ts;

    const eligible = data.merge_eligible || [];
    const notReady = data.not_ready || [];
    if (eligible.length > 0 || notReady.length > 0) {
      this.sendEmbed(mergeGateEmbed(eligible, notReady));
    }
  }

  _onConflictSweep() {
    if (!this.config.postPipelineResults) return;
    const data = this._readJson('conflict-sweep.json');
    if (!data) return;

    const ts = data.generated_at || '';
    if (ts === this.lastConflictSweep) return;
    this.lastConflictSweep = ts;

    const rebased = data.rebased || [];
    const closed = data.closed || [];
    if (rebased.length > 0 || closed.length > 0) {
      this.sendEmbed(conflictSweepEmbed(data));
    }
  }

  _onQueueChange() {
    const data = this._readJson('actionable.json');
    if (!data) return;

    const issues = (data.issues && data.issues.items) ? data.issues.items.length : 0;
    const prs = (data.prs && data.prs.items) ? data.prs.items.length : 0;
    const total = issues + prs;

    if (this.lastQueueCount !== null && total !== this.lastQueueCount) {
      const delta = total - this.lastQueueCount;
      const direction = delta > 0 ? `+${delta} new` : `${Math.abs(delta)} resolved`;
      this.sendMessage(agentMessage('pipeline', `Queue: ${total} items (${issues} issues, ${prs} PRs) — ${direction}`));
    }
    this.lastQueueCount = total;
  }
}

module.exports = { PipelineWatcher };
