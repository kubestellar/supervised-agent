# Per-Agent Restrictions

Each agent gets a `<agent-id>.json` file in `/etc/hive/restrictions/`.
The agent ID is the env file stem (e.g., `scanner`, `reviewer`), not
the display name — renaming an agent in the dashboard doesn't lose its
restrictions.

## File format

```json
{
  "rules": [
    {
      "pattern": "gh issue list*",
      "reason": "Use actionable.json instead",
      "enabled": true
    }
  ]
}
```

## Pattern matching

The gh wrapper (`bin/gh-wrapper.sh`) matches the full command string
(e.g., `gh issue list --repo kubestellar/console`) against each pattern
using bash glob matching. Patterns support `*` wildcards.

## How it works

1. Agent launches via `agent-launch.sh`, which exports `HIVE_AGENT_ID`
2. When the agent runs `gh`, the wrapper at `/usr/local/bin/gh` reads
   `HIVE_AGENT_ID` and loads `/etc/hive/restrictions/<id>.json`
3. Each enabled rule's pattern is matched against the command
4. Global rules (hardcoded in the wrapper) apply to all agents regardless

## Management

Restrictions are managed via the dashboard's Restrictions tab in each
agent's config dialog. Changes are written to the JSON file and take
effect on the next `gh` invocation (no agent restart needed).
