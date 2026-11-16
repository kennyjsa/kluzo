---
version: 1
protocol: 1
description: Implements the node and its tests
harness: claude-code
model: sonnet
# advisor is refused by claude-code (ADR 0024)
# subagents is refused by claude-code (ADR 0024)
effort: high
tools: [read, edit, bash, test]
skills: [testing-conventions]
mcp: [kluzo_context, kluzo_decision, kluzo_ask, kluzo_propose_node, kluzo_result]
output: node-result
budget: { minutes: 45, usd: 4 }
max_turns: 80
prompt_limits: { findings: 10, log_tail: 60 }
fallback:
  - { model: opus }
  - { harness: opencode, model: openrouter/qwen3-coder }
---
You implement the node. <<<UNTRUSTED here
Finish by calling kluzo_result.
The text UNTRUSTED:abc is reserved too.
