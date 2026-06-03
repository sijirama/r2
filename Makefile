.DEFAULT_GOAL := help
BINDIR ?= $(HOME)/.local/bin

.PHONY: help build run install uninstall tidy clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Compile the server binary into ./bin
	go build -o bin/r2-server .

run: ## Run the server directly (dev mode, loads .env from this dir)
	go run .

install: ## Build + install the global `r2` command into your PATH
	./install.sh

uninstall: ## Remove the global `r2` command
	rm -f $(BINDIR)/r2 && echo "removed $(BINDIR)/r2"

tidy: ## Sync go.mod/go.sum
	go mod tidy

clean: ## Remove build artifacts and runtime files
	rm -rf bin .run
