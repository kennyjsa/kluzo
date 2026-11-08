---
version: 1
protocol: 1
description: Implements the node and its tests
harness: claude-code
model: sonnet
advisor: opus
subagents: haiku
effort: high
tools: [read, edit, bash, test]
skills: [testing-conventions]
mcp: [kluzo_context, kluzo_changes, kluzo_decisions, kluzo_code_map, kluzo_code_find, kluzo_code_refs, kluzo_decision, kluzo_ask, kluzo_propose_node, kluzo_result]
output: node-result
budget: { minutes: 45, usd: 4 }
max_turns: 80
prompt_limits: { findings: 10, log_tail: 60 }
fallback:
  - { model: opus }
  - { harness: opencode, model: openrouter/qwen3-coder }
---
You implement the node described in the task. Work test-first, and finish
by calling kluzo_result. Record choices with kluzo_decision.
