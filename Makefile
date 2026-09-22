# The blueprint itself is documentation plus a compiling examples/ module.
# A project built FROM the blueprint gets its Makefile from claude.md.
.PHONY: help skills check examples

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  skills     Link blueprint skills into .claude/skills"
	@echo "  check      Verify internal links and heading anchors"
	@echo "  examples   Build and test the examples module"

skills:
	@set -e; \
	found=0; \
	for BP in . $$(git config -f .gitmodules --get-regexp '\.path$$' 2>/dev/null | awk '{print $$2}'); do \
	  [ -d "$$BP/skills" ] || continue; \
	  mkdir -p .claude/skills; \
	  for d in "$$BP"/skills/*/; do \
	    [ -f "$$d/SKILL.md" ] || continue; \
	    n=$$(basename "$$d"); \
	    rm -rf ".claude/skills/$$n"; \
	    ln -s "$$(cd "$$d" && pwd)" ".claude/skills/$$n"; \
	    echo "  linked /$$n"; \
	    found=1; \
	  done; \
	done; \
	if [ "$$found" = "0" ]; then \
	  echo "No blueprint skills found."; \
	  echo "The blueprint submodule is missing or not initialized. Run:"; \
	  echo "  git submodule update --init --recursive"; \
	  exit 1; \
	fi; \
	if [ -f .gitignore ] && ! grep -qxF '.claude/skills/' .gitignore; then \
	  echo '.claude/skills/' >> .gitignore; \
	fi; \
	echo "Skills linked. Restart Claude Code to pick them up."

check:
	./scripts/check-links.py

examples:
	cd examples && gofmt -l . && go build ./... && go vet ./... && go test ./... -race -count=1
