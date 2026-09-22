# Skills

Executable procedures that go with the patterns. A pattern doc says what correct
code looks like; a skill is a procedure an agent runs.

| Skill | Purpose | When |
|-------|---------|------|
| [blueprint-go-audit](blueprint-go-audit/SKILL.md) | Independent adversarial review of changed Go code against the blueprint, with a scored conformance table | **Mandatory** after every implementation, before reporting the work complete |

The name carries `-go` because a repo can mount this blueprint and
`blueprint-python` at once, and two skills cannot share a name.

## Using them

Skills work two ways, and the first needs no setup.

**By path (always available).** Every skill is a Markdown file in this
submodule. An agent reads `<blueprint>/skills/<name>/SKILL.md` and follows it.
The blueprint's [AI agent instructions](../claude.md#ai-agent-instructions)
require exactly this for the audit, so the gate holds in a fresh clone with
nothing installed.

**As a slash command (opt in).** From the project root:

```bash
make skills
```

That symlinks each skill into `.claude/skills/`, making it invocable as
`/blueprint-go-audit`. Symlinks, not copies — the skill tracks the submodule, so
a `git submodule update` picks up changes with no reinstall. The target links
from every blueprint submodule it finds, so a repo carrying both the Go and
Python blueprints gets both audits.

If your project has no `make skills` target yet, copy it from the
[Makefile in claude.md](../claude.md#makefile).

## The audit is not optional

Go's toolchain is good enough that a green build feels like a passing review. It
is not. Read the blueprint's own framing of its [non-negotiables](../claude.md#non-negotiables):
**each of them fails silently.** A handler on `db.Get()` instead of
`db.WithTenant` compiles, vets clean, passes its tests, and leaks or returns
nothing in production. `gofmt`, `go vet` and `go test` cannot see any of it.

The audit is what does, and it is run by a **separate agent** that never saw the
plan, because the agent that wrote the code can justify every shortcut it took.

## Adding a skill

One directory per skill, named for the skill, holding a `SKILL.md` with YAML
frontmatter:

```markdown
---
name: kebab-case-name
description: One paragraph. Says what it does, what it reports, and when to run it — this is what an agent matches against, so be concrete.
---
```

The directory name and the `name:` field must match; the installer symlinks by
directory name.

Keep skills project-agnostic. This submodule is shared across every project on
the blueprint, so resolve the blueprint path rather than hardcoding it, and read
the project's own configuration rather than assuming a layer is enabled.
