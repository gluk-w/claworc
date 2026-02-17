# Load .env.development defaults, then .env for personal overrides
include .env.development
-include .env
export

# Ensure Homebrew and Go binaries are on PATH
export PATH := /opt/homebrew/bin:$(HOME)/go/bin:$(PATH)

# Auto-detect Docker socket (if not set in env)
ifndef DOCKER_HOST
    ifneq ("",$(wildcard $(HOME)/.docker/run/docker.sock))
        export DOCKER_HOST := unix://$(HOME)/.docker/run/docker.sock
    endif
endif

AGENT_IMAGE := glukw/openclaw-vnc-chrome
DASHBOARD_IMAGE := glukw/claworc
TAG := latest
PLATFORM := linux/amd64,linux/arm64

KUBECONFIG := ../kubeconfig
HELM_RELEASE := claworc
HELM_NAMESPACE := claworc

.PHONY: agent dashboard docker-prune release \
	helm-install helm-upgrade helm-uninstall helm-template install-dev dev \
	pull-agent local-build local-up local-down local-logs local-clean control-plane

agent:
	docker buildx build --platform $(PLATFORM) -t $(AGENT_IMAGE):$(TAG) --push agent/

control-plane:
	docker buildx build --platform $(PLATFORM) -t $(DASHBOARD_IMAGE):$(TAG) --push control-plane/

release: agent control-plane
	@echo "Released $(AGENT_IMAGE):$(TAG) and $(DASHBOARD_IMAGE):$(TAG)"

docker-prune:
	docker system prune -af

helm-install:
	helm install $(HELM_RELEASE) helm/ --namespace $(HELM_NAMESPACE) --create-namespace --kubeconfig $(KUBECONFIG)

helm-upgrade:
	helm upgrade $(HELM_RELEASE) helm/ --namespace $(HELM_NAMESPACE) --kubeconfig $(KUBECONFIG)

helm-uninstall:
	helm uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE) --kubeconfig $(KUBECONFIG)

helm-template:
	helm template $(HELM_RELEASE) helm/ --namespace $(HELM_NAMESPACE) --kubeconfig $(KUBECONFIG)

install-dev:
	@echo "Installing development dependencies..."
	@echo "Installing goreman (process manager)..."
	@which goreman > /dev/null || go install github.com/mattn/goreman@latest
	@echo "Installing air (live reload)..."
	@which air > /dev/null || go install github.com/air-verse/air@latest
	@echo "Installing frontend dependencies (npm)..."
	@cd control-plane/frontend && npm install
	@echo "Creating admin user (if not exists)..."
	@cd control-plane && go run main.go --create-admin --username admin --password admin || echo "Admin user likely exists or db not init yet, ignoring error"
	@echo "All dependencies installed successfully!"

dev:
	@echo "=== Development Config ==="
	@echo "  DATABASE_PATH: $(CLAWORC_DATABASE_PATH)"
	@echo ""
	@echo "Control plane: http://localhost:8000"
	@echo "Frontend:      http://localhost:5173"
	@echo ""
	cd control-plane && goreman -f Procfile.dev start

# --- Local Docker testing ---------------------------------------------------

local-build:
	docker build -t claworc-agent:local agent/
	docker build -t claworc-dashboard:local control-plane/

local-up:
	@mkdir -p "$(CURDIR)/data/configs"
	CLAWORC_DATA_DIR=$(CURDIR)/data docker compose up -d
	@echo ""
	@echo "Dashboard: http://localhost:8000"
	@echo "Data dir:  $(CURDIR)/data"

local-down:
	docker compose down

local-logs:
	docker compose logs -f

local-clean:
	docker compose down --rmi local -v
	rm -rf "$(CURDIR)/data"

e2e-docker-tests:
	./scripts/run_tests.sh

dev-docker:
	CLAWORC_ORCHESTRATOR=docker $(MAKE) dev

dev-k8s:
	CLAWORC_ORCHESTRATOR=kubernetes $(MAKE) dev
	