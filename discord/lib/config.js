const fs = require('fs');
const yaml = require('js-yaml');

const HIVE_CONFIG_PATH = process.env.HIVE_PROJECT_CONFIG || '/etc/hive/hive-project.yaml';

function loadConfig() {
  const config = {
    botToken: process.env.DISCORD_BOT_TOKEN || '',
    channelPrimary: process.env.DISCORD_CHANNEL_PRIMARY || '',
    channelAlerts: process.env.DISCORD_CHANNEL_ALERTS || '',
    adminRoleId: process.env.DISCORD_ADMIN_ROLE_ID || '',
    dashboardUrl: process.env.HIVE_DASHBOARD_URL || 'http://localhost:3001',
    metricsDir: process.env.HIVE_METRICS_DIR || '/var/run/hive-metrics',
    postPipelineResults: true,
    postAgentTransitions: true,
    postGovernorModeChanges: true,
    commandPrefix: '!',
  };

  try {
    const raw = fs.readFileSync(HIVE_CONFIG_PATH, 'utf8');
    const doc = yaml.load(raw);
    if (doc && doc.discord) {
      const d = doc.discord;
      if (d.channels) {
        config.channelPrimary = config.channelPrimary || d.channels.primary || '';
        config.channelAlerts = config.channelAlerts || d.channels.alerts || '';
      }
      if (d.command_prefix) config.commandPrefix = d.command_prefix;
      if (d.admin_role_id) config.adminRoleId = config.adminRoleId || d.admin_role_id;
      if (d.post_pipeline_results === false) config.postPipelineResults = false;
      if (d.post_agent_transitions === false) config.postAgentTransitions = false;
      if (d.post_governor_mode_changes === false) config.postGovernorModeChanges = false;
    }
    if (doc && doc.dashboard) {
      config.dashboardPort = doc.dashboard.port || '3001';
      config.dashboardUrl = process.env.HIVE_DASHBOARD_URL || `http://localhost:${config.dashboardPort}`;
    }
  } catch (_) {
    // config file missing is fine — env vars are primary
  }

  if (!config.channelAlerts) config.channelAlerts = config.channelPrimary;
  return config;
}

module.exports = { loadConfig };
