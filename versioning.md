# Blueprint Versioning Strategy

## The Problem

When using blueprint-go as a git submodule, updates to the blueprint can create conflicts with existing projects.

**Scenario:**
1. You build `myapp` with blueprint v1.0 as a submodule
2. Blueprint gets updated to v2.0 (better patterns, different structure)
3. You run `cd blueprint && git pull` in your app
4. Now your app code (built for v1.0) references a v2.0 blueprint that may have:
   - Different file paths (`reference/auth.md` moved to `reference/security/auth.md`)
   - Updated code examples that don't match your implementation
   - Changed conventions or patterns
   - Conflicting advice

**The issue isn't code breaking** (your app code lives outside `blueprint/`), but:
- **Documentation references breaking** - Links in `claude.md` point to moved/renamed files
- **Cognitive dissonance** - Blueprint shows patterns your codebase doesn't follow
- **Confusion for AI agents** - Claude reads the blueprint and suggests changes that don't fit your v1.0-based code

## Solutions

### Option 1: **Semantic Versioning + Pinned Versions (Recommended)**

```bash
# When setting up project, pin to specific version
cd blueprint
git checkout v1.0.0
cd ..
git add blueprint
git commit -m "Pin blueprint to v1.0.0"
```

**Blueprint versioning strategy:**
- `v1.x.x` - Patch/minor updates (safe to update)
  - Fix typos, clarify docs
  - Add new optional sections
  - Never break existing references
- `v2.0.0` - Major version (breaking changes)
  - Restructured docs
  - Changed patterns
  - Projects stay on v1.x.x unless they explicitly migrate

**In README, document:**
```markdown
## Versioning

- **v1.x** - Stable, production-ready (current projects)
- **v2.x** - Next generation (new projects only)

### Upgrading Between Major Versions
Migration guides provided in `MIGRATION.md` for each major version.
```

### Option 2: **Immutable Snapshots**

Instead of one evolving repo, create dated snapshots:
```
blueprint-go-2024     # Frozen forever
blueprint-go-2025     # New year, new patterns
```

Projects always reference the year they started with.

**Pros:** Zero breaking changes ever
**Cons:** Fragments the ecosystem, harder to maintain

### Option 3: **Backwards-Compatible Evolution Only**

Blueprint follows strict rules:
- ✅ Can add new files (`reference/advanced-patterns.md`)
- ✅ Can append to existing files (new sections at end)
- ✅ Can add optional examples
- ❌ Cannot move/rename files
- ❌ Cannot change existing code examples
- ❌ Cannot restructure TOC

**This limits evolution significantly.**

### Option 4: **Migration Layer**

Keep old versions accessible:
```
blueprint-go/
├── v1/
│   ├── claude.md
│   └── reference/
├── v2/
│   ├── claude.md
│   └── reference/
└── latest -> v2
```

Projects specify version:
```bash
git submodule add https://github.com/remarqable/blueprint-go.git blueprint
cd blueprint && git checkout v1-stable
```

## Recommended Approach

**Combine Option 1 (Semantic Versioning) with a strict policy:**

### Blueprint Evolution Policy

1. **Version pinning by default**
   - README instructs users to pin to specific version
   - Add to `.gitmodules` example

2. **Backwards compatibility within major versions**
   - v1.x updates never break existing projects
   - Only additions and clarifications

3. **Migration guides for major versions**
   - `MIGRATION-v1-to-v2.md` with step-by-step upgrade path
   - Clearly mark "for existing projects" vs "for new projects"

4. **Change categories in releases:**
   ```markdown
   ## v1.2.0
   - ✅ Safe updates (existing projects can pull)
   - ⚠️  Optional improvements (consider adopting)
   - 🔴 Breaking (ignore if on v1.x)
   ```

## Proposed README Section

Add this section to README.md:

```markdown
## 🔄 Updating the Blueprint

### Version Pinning (Recommended)

Pin your project to a specific version to avoid unexpected changes:

```bash
cd blueprint
git fetch --tags
git checkout v1.0.0  # Pin to specific version
cd ..
git add blueprint
git commit -m "Pin blueprint to v1.0.0"
```

### Semantic Versioning

- **v1.x.x** - Backwards compatible updates (safe to update)
  - Documentation improvements
  - New optional patterns
  - Bug fixes
- **v2.0.0+** - Major changes (requires migration)
  - Restructured documentation
  - Changed conventions
  - See MIGRATION.md before upgrading

### When to Update

- **Patch versions (v1.0.1)** - Always safe, update anytime
- **Minor versions (v1.1.0)** - New features, safe to update
- **Major versions (v2.0.0)** - Only when you're ready to migrate your codebase

### Checking for Updates

```bash
cd blueprint
git fetch --tags
git tag  # See available versions
git log HEAD..origin/main --oneline  # See what changed
```
```

## Implementation Checklist

- [ ] Create initial v1.0.0 tag
- [ ] Add versioning section to README.md
- [ ] Update Quick Start to show version pinning
- [ ] Create MIGRATION.md template for future major versions
- [ ] Document release process with change categories
- [ ] Add GitHub release workflow with semantic versioning
