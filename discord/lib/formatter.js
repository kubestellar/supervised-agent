const { EmbedBuilder } = require('discord.js');
const { agentPrefix, agentColor } = require('./agent-identities');

function agentMessage(agentName, text) {
  return `${agentPrefix(agentName)} ${text}`;
}

function governorEmbed(transition, queueDepth, agentStates) {
  const embed = new EmbedBuilder()
    .setTitle('Governor Mode Change')
    .setColor(agentColor('governor'))
    .addFields({ name: 'Transition', value: transition, inline: true })
    .addFields({ name: 'Queue Depth', value: String(queueDepth), inline: true })
    .setTimestamp();

  if (agentStates) {
    const lines = Object.entries(agentStates)
      .map(([name, state]) => `${agentPrefix(name)} ${state}`)
      .join('\n');
    embed.addFields({ name: 'Agent States', value: lines || 'unknown' });
  }
  return embed;
}

function mergeGateEmbed(eligible, notReady) {
  const embed = new EmbedBuilder()
    .setTitle('Merge Gate Results')
    .setColor(eligible.length > 0 ? 0x2ecc71 : 0x95a5a6)
    .setTimestamp();

  if (eligible.length > 0) {
    const lines = eligible.map(p => `#${p.number} — ${p.title}`).join('\n');
    embed.setDescription(`**${eligible.length}** PR(s) eligible for merge`);
    embed.addFields({ name: 'Eligible', value: lines.slice(0, 1024) });
  } else {
    embed.setDescription('No PRs eligible for merge');
  }

  if (notReady.length > 0) {
    const lines = notReady.map(p => {
      const reasons = (p.block_reasons || []).join(', ');
      return `#${p.number} — ${reasons}`;
    }).join('\n');
    embed.addFields({ name: `Not Ready (${notReady.length})`, value: lines.slice(0, 1024) });
  }
  return embed;
}

function conflictSweepEmbed(data) {
  const embed = new EmbedBuilder()
    .setTitle('Conflict Sweep')
    .setColor(agentColor('pipeline'))
    .setTimestamp();

  const rebased = data.rebased || [];
  const closed = data.closed || [];

  if (rebased.length === 0 && closed.length === 0) {
    embed.setDescription('No conflicting PRs found');
  } else {
    if (rebased.length > 0) {
      embed.addFields({ name: `Rebased (${rebased.length})`, value: rebased.map(p => `#${p}`).join(', ') });
    }
    if (closed.length > 0) {
      embed.addFields({ name: `Closed (${closed.length})`, value: closed.map(p => `#${p}`).join(', ') });
    }
  }
  return embed;
}

function commandResponse(success, text) {
  const icon = success ? '✅' : '❌';
  return `${icon} ${text}`;
}

module.exports = { agentMessage, governorEmbed, mergeGateEmbed, conflictSweepEmbed, commandResponse };
