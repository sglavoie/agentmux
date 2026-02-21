PREFIX_BIN   := $(HOME)/.local/bin
PREFIX_KITTY := $(HOME)/.config/kitty
PREFIX_ZFUNC := $(HOME)/.zfunc

SYMLINKS := \
	$(PREFIX_BIN)/cc-launch:bin/cc-launch \
	$(PREFIX_BIN)/cc-status:bin/cc-status \
	$(PREFIX_KITTY)/claude_dashboard.py:kittens/claude_dashboard.py \
	$(PREFIX_ZFUNC)/_cc-launch:completions/_cc-launch \
	$(PREFIX_ZFUNC)/_cc-status:completions/_cc-status \
	$(PREFIX_KITTY)/cc-manager.conf:cc-manager.conf

.PHONY: install uninstall

install:
	@mkdir -p $(PREFIX_BIN) $(PREFIX_KITTY) $(PREFIX_ZFUNC)
	@$(foreach pair,$(SYMLINKS),\
		ln -sf $(CURDIR)/$(word 2,$(subst :, ,$(pair))) $(word 1,$(subst :, ,$(pair)));)
	@echo "Symlinks created."
	@echo ""
	@echo "If you haven't already, add this line to your kitty.conf:"
	@echo "  include cc-manager.conf"
	@echo ""
	@echo "Then reload Kitty config with ctrl+shift+f5."

uninstall:
	@$(foreach pair,$(SYMLINKS),\
		target=$(word 1,$(subst :, ,$(pair))); \
		if [ -L "$$target" ] && [ "$$(readlink "$$target")" = "$(CURDIR)/$(word 2,$(subst :, ,$(pair)))" ]; then \
			rm "$$target"; \
			echo "Removed $$target"; \
		fi;)
	@echo "Done."
