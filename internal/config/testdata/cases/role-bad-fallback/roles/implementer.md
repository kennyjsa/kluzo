---
version: 1
protocol: 1
description: d
harness: claude-code
model: sonnet
fallback:
  - {}
  - { model: gpt-5 }
  - { harness: opencode, model: sonnet }
tools: [read]
mcp: [kluzo_result]
output: node-result
---
You review.
