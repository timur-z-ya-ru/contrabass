---
max_concurrency: 3
poll_interval_ms: 5000
max_retry_backoff_ms: 30000
model: test-model
agent_timeout_ms: 30000
stall_timeout_ms: 10000
tracker:
  type: internal
agent:
  type: codex
codex:
  binary_path: bash /tmp/contrabass-web-team-integration/testdata/mock-agent.sh
---
# Test Workflow

Issue: {{ issue.title }}
{{ issue.description }}
