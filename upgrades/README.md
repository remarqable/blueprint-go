# Upgrades

One file per released version of the blueprint. Each file holds the steps that
move a project from the version before it to that version, and nothing else.
They work like database migrations: applied once each, in order.

The blueprint itself is not copied per version. The repository holds the current
blueprint, [`VERSION`](../VERSION) names it, and the git tag `v<version>` marks
the commit.

## Version numbers

| Change | Example | What a project does |
|--------|---------|---------------------|
| Patch (`x.y.Z`) | Wording, fixed links, clearer examples | Nothing |
| Minor (`x.Y.0`) | A new pattern doc, a new optional setting that defaults to off | Nothing required |
| Major (`X.0.0`) | A renamed file, a changed rule, a changed setting | Follow the upgrade file |

Every version has a file here, including the ones that need no action. Those
say so in one line.

## Upgrading a project

The project records the version it was built against as `blueprint_version` in
its own root `claude.md`, beside its configuration answers.

Upgrade only when the user asks. Then:

1. Read `blueprint_version` from the project's root `claude.md`. If there is
   none, the project predates versioning: start at the oldest file here.
2. Read `blueprint/VERSION`.
3. Work on a branch. Apply every file here that is newer than the project's
   version, up to and including `VERSION`, oldest first.
4. Within a file, apply the steps in order, one commit per step. Run the
   project's tests after each.
5. A step marked **ask before applying** touches stored data, a public route, or
   something else that cannot be undone by reverting a commit. Stop, show the
   user the step, and wait for a yes. A no skips the step: list it under
   `blueprint_skipped` in the project's root `claude.md` so the next upgrade
   and the audit know it was a decision.
6. Run the [blueprint audit](../skills/blueprint-go-audit/SKILL.md) over the files the
   upgrade changed.
7. Set `blueprint_version` to the new version.

Do not change anything the upgrade files do not name. Code that conformed to the
old version and is not covered by a step stays as it is.

## Writing an upgrade file

Name it `<version>.md`. Keep it short enough to read in a minute.

```markdown
# 2.0.0

Action required: yes

## 1. Short name of the change

Why it changed, in a sentence.

- **Find:** how to locate the old form in a project
- **Change:** the exact edit
- **Check:** how to know it worked

## 2. Another change (ask before applying)

...
```

A version that needs nothing is three lines:

```markdown
# 1.1.0

Action required: no

Adds `patterns/example.md`. Existing projects are unaffected.
```

## Releasing a version

1. Put the new number in `VERSION`.
2. Add `upgrades/<version>.md`.
3. Run `make check`.
4. Merge, then tag the merge commit `v<version>` and push the tag.
