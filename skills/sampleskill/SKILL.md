---
name: sampleskill
description: "Sample skill that echoes a greeting via a configured provider. Use when demonstrating required env var gating in Claworc."
required_env_vars:
  - API_KEY
  - PROVIDER_NAME
---

# Sample Skill

Demonstration skill used by the control-plane parser integration test.

## Usage

Given `API_KEY` and `PROVIDER_NAME` in the instance environment, the skill calls
the provider's API and prints a greeting.
