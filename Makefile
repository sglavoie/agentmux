PYTHON_SRC    := kittens/
SHELL_SCRIPTS := bin/cc-launch bin/cc-status bin/cc-toggle-star bin/ccm \
                 lib/cc-common.sh lib/cc-title-icons.sh lib/install-hooks.sh \
                 lib/sync-preambles.sh
RYE           := $(HOME)/.rye/shims/rye
SHELLCHECK    := /opt/homebrew/bin/shellcheck
SHFMT         := /opt/homebrew/bin/shfmt

PREFIX_BIN   := $(HOME)/.local/bin
PREFIX_LIB   := $(HOME)/.local/lib/cc-manager
PREFIX_KITTY := $(HOME)/.config/kitty
PREFIX_ZFUNC := $(HOME)/.zfunc

SYMLINKS := \
	$(PREFIX_BIN)/ccm:bin/ccm \
	$(PREFIX_BIN)/cc-launch:bin/cc-launch \
	$(PREFIX_BIN)/cc-status:bin/cc-status \
	$(PREFIX_BIN)/cc-toggle-star:bin/cc-toggle-star \
	$(PREFIX_BIN)/cc-hook-indicator:hooks/cc-hook-indicator \
	$(PREFIX_LIB)/cc-common.sh:lib/cc-common.sh \
	$(PREFIX_LIB)/cc-title-icons.sh:lib/cc-title-icons.sh \
	$(PREFIX_LIB)/palette.json:data/palette.json \
	$(PREFIX_LIB)/icons.json:data/icons.json \
	$(PREFIX_KITTY)/cc_dashboard:kittens/cc_dashboard \
	$(PREFIX_KITTY)/claude_dashboard.py:kittens/claude_dashboard.py \
	$(PREFIX_KITTY)/cc_session_picker.py:kittens/cc_session_picker.py \
	$(PREFIX_KITTY)/cc_quick_switch.py:kittens/cc_quick_switch.py \
	$(PREFIX_KITTY)/cc_keybindings.py:kittens/cc_keybindings.py \
	$(PREFIX_KITTY)/cc_reorder_tabs.py:kittens/cc_reorder_tabs.py \
	$(PREFIX_ZFUNC)/_ccm:completions/_ccm \
	$(PREFIX_ZFUNC)/_cc-launch:completions/_cc-launch \
	$(PREFIX_ZFUNC)/_cc-status:completions/_cc-status \
	$(PREFIX_KITTY)/cc-manager.conf:cc-manager.conf

.PHONY: install uninstall install-hooks sync-preambles lint format format-check typecheck check

install:
	@mkdir -p $(PREFIX_BIN) $(PREFIX_LIB) $(PREFIX_KITTY) $(PREFIX_ZFUNC)
	@$(foreach pair,$(SYMLINKS),\
		ln -sf $(CURDIR)/$(word 2,$(subst :, ,$(pair))) $(word 1,$(subst :, ,$(pair)));)
	@echo "Symlinks created."
	@echo ""
	@echo "If you haven't already, add this line to your kitty.conf:"
	@echo "  include cc-manager.conf"
	@echo ""
	@echo "Then reload Kitty config with ctrl+shift+f5."
	@echo ""
	@$(MAKE) --no-print-directory sync-preambles
	@$(MAKE) --no-print-directory install-hooks

sync-preambles:
	@echo "Syncing config documentation..."
	@bash $(CURDIR)/lib/sync-preambles.sh "$(CURDIR)"

install-hooks:
	@echo "Installing Claude Code hooks..."
	@bash $(CURDIR)/lib/install-hooks.sh "$(CURDIR)/config/hooks.example.json"
	@echo "Hooks installation complete."

uninstall:
	@$(foreach pair,$(SYMLINKS),\
		target=$(word 1,$(subst :, ,$(pair))); \
		if [ -L "$$target" ] && [ "$$(readlink "$$target")" = "$(CURDIR)/$(word 2,$(subst :, ,$(pair)))" ]; then \
			rm "$$target"; \
			echo "Removed $$target"; \
		fi;)
	@echo "Done."
	@echo ""
	@echo "Note: Claude Code hooks in settings.json were not removed."
	@echo "To remove them manually, edit each account's settings.json and"
	@echo "delete the cc-hook-indicator entries from the hooks section."

lint:
	$(RYE) run ruff check $(PYTHON_SRC)
	$(SHELLCHECK) $(SHELL_SCRIPTS)

format:
	$(RYE) run ruff format $(PYTHON_SRC)
	$(SHFMT) -w $(SHELL_SCRIPTS)

format-check:
	$(RYE) run ruff format --check $(PYTHON_SRC)
	$(SHFMT) -d $(SHELL_SCRIPTS)

typecheck:
	$(RYE) run mypy $(PYTHON_SRC)

check: lint format-check typecheck
