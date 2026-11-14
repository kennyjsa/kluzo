---
version: 1
protocol: 1
description: Implements the node and its tests
harness: claude-code
model: sonnet
# advisor is refused by claude-code (ADR 0024)
# subagents is refused by claude-code (ADR 0024)
effort: extreme
tools: [read, write]
skills: [testing-conventions]
mcp: [kluzo_decision, kluzo_everything, kluzo_result]
output: node-resultt
budget: { minutes: 45, usd: 4 }
max_turns: 80
prompt_limits: { findings: 10, log_tail: 60 }
fallback:
  - { model: opus }
  - { harness: opencode, model: openrouter/qwen3-coder }
---
You implement the node described in the task. Work test-first, and finish
by calling kluzo_result. Record choices with kluzo_decision.
