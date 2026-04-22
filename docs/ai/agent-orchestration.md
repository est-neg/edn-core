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
| `social-media-growth-manager` | Growth and channel operations | Own Facebook, Instagram, WhatsApp, Meta Business workflows, organic growth, and social analytics | `GPT-5.4 xhigh` |
| `solution-architect` | System/Solution Architect | Boundaries, contracts, NFRs, sequencing, risks | `GPT-5.4 xhigh` |
| `golang-developer` | Agile Team backend engineer | Implement Go services, workers, adapters, refactors | `Claude Sonnet 4.6` |
| `database-engineer` | Data specialist / platform engineer | MariaDB, MongoDB, Redis design, migrations, caching | `Claude Sonnet 4.6` |
| `security-reviewer` | Built-in security / compliance | Approve, reject, and suggest remediation for risks | `GPT-5.4 xhigh` |
| `qa-tdd` | Built-in quality | Define failing tests, validate behavior, guard regressions | `GPT-5.4 xhigh` |

## Model Policy

- Use `GPT-5.4 xhigh` for roles centered on reasoning, architecture, risk analysis, and creative problem solving.
- Use `GPT-5.4 xhigh` for social channel strategy, Meta asset diagnosis, organic growth design, and cross-functional measurement planning.
- Use `Claude Sonnet 4.6` as the default model for implementation-heavy work.
- Use hidden Opus escalation agents when implementation remains blocked after meaningful attempts or when QA repeatedly sends the same correction back.
- If a preferred label is unavailable in the local model picker, the agent falls back to the nearest compatible model declared in its frontmatter.

## Handoff Flow

1. `safe-backend-orchestrator` classifies the request and chooses the smallest specialist set.
2. `social-media-growth-manager` leads Facebook, Instagram, WhatsApp, Meta Business, content-system, organic-growth, and social-analytics requests.
3. `solution-architect` defines boundaries, contracts, sequencing, and non-functional constraints when social or backend work needs product changes, APIs, or new event flows.
4. `golang-developer` and `database-engineer` implement the approved Meta integration, analytics capture, and data design.
5. `security-reviewer` evaluates permissions, auth, secrets, data exposure, privacy, and operational hardening.
6. `qa-tdd` validates the change with tests, regression analysis, tracking checks, and release guidance.
7. If implementation quality stalls, hand off to the relevant Opus escalation agent.

## Escalation Rules

- Escalate from Sonnet to Opus when the same defect returns after QA review.
- Escalate when the fix spans multiple modules, data stores, or concurrency boundaries and the first implementation path is not stabilizing.
- Do not escalate for simple syntax issues or missing context that can be resolved by reading the repository.

## Operating Principles

- Architecture before broad coding.
- TDD and built-in quality over post-hoc inspection.
- Security gates for externally reachable or privilege-changing behavior.
- Explicit data ownership across MariaDB, MongoDB, and Redis.
- Channel strategy stays aligned with product truth, consent posture, and measurable conversion paths.
- Social-media agents can only consult the collaborators declared in their frontmatter; `social-media-growth-manager` is intentionally granted visibility into the current specialist set to unblock cross-domain questions.
- Small, reversible increments with observable outcomes.
