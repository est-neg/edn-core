# AI Agent Orchestration

## Source Of Truth

This setup follows the current VS Code and GitHub Copilot customization model:

- `.github/copilot-instructions.md` for workspace-wide guidance.
- `.github/instructions/*.instructions.md` for targeted rules and conventions.
- `.github/agents/*.agent.md` for orchestrated Copilot agents with model preferences and handoffs.
- `.claude/CLAUDE.md` and `.claude/rules/*.md` for Claude-compatible always-on guidance and scoped rules.

## SAFe-Aligned Roles

| Agent | SAFe intent | Primary responsibility | Preferred model |
| --- | --- | --- | --- |
| `safe-backend-orchestrator` | Flow coordination | Route work, sequence handoffs, manage specialist usage | `GPT-5.4 xhigh` |
| `solution-architect` | System/Solution Architect | Boundaries, contracts, NFRs, sequencing, risks | `GPT-5.4 xhigh` |
| `golang-developer` | Agile Team backend engineer | Implement Go services, workers, adapters, refactors | `Claude Sonnet 4.6` |
| `database-engineer` | Data specialist / platform engineer | MariaDB, MongoDB, Redis design, migrations, caching | `Claude Sonnet 4.6` |
| `security-reviewer` | Built-in security / compliance | Approve, reject, and suggest remediation for risks | `GPT-5.4 xhigh` |
| `qa-tdd` | Built-in quality | Define failing tests, validate behavior, guard regressions | `GPT-5.4 xhigh` |

## Model Policy

- Use `GPT-5.4 xhigh` for roles centered on reasoning, architecture, risk analysis, and creative problem solving.
- Use `Claude Sonnet 4.6` as the default model for implementation-heavy work.
- Use hidden Opus escalation agents when implementation remains blocked after meaningful attempts or when QA repeatedly sends the same correction back.
- If a preferred label is unavailable in the local model picker, the agent falls back to the nearest compatible model declared in its frontmatter.

## Handoff Flow

1. `safe-backend-orchestrator` classifies the request and chooses the smallest specialist set.
2. `solution-architect` defines boundaries, contracts, sequencing, and non-functional constraints.
3. `golang-developer` and `database-engineer` implement the approved design.
4. `security-reviewer` evaluates attack surface, auth, secrets, data exposure, and operational hardening.
5. `qa-tdd` validates the change with tests, regression analysis, and release guidance.
6. If implementation quality stalls, hand off to the relevant Opus escalation agent.

## Escalation Rules

- Escalate from Sonnet to Opus when the same defect returns after QA review.
- Escalate when the fix spans multiple modules, data stores, or concurrency boundaries and the first implementation path is not stabilizing.
- Do not escalate for simple syntax issues or missing context that can be resolved by reading the repository.

## Operating Principles

- Architecture before broad coding.
- TDD and built-in quality over post-hoc inspection.
- Security gates for externally reachable or privilege-changing behavior.
- Explicit data ownership across MariaDB, MongoDB, and Redis.
- Small, reversible increments with observable outcomes.
