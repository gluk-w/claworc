# Load .env.development defaults, then .env for personal overrides
include .env.development
-include .env
export

AGENT_IMAGE_NAME := openclaw-vnc-chromium2
AGENT_IMAGE := glukw/$(AGENT_IMAGE_NAME)
DASHBOARD_IMAGE := glukw/claworc
TAG := latest
PLATFORMS := linux/amd64,linux/arm64

KUBECONFIG := ../kubeconfig
HELM_RELEASE := claworc
HELM_NAMESPACE := claworc

.PHONY: agent agent-build agent-test agent-push dashboard docker-prune release \
	helm-install helm-upgrade helm-uninstall helm-template install-dev dev dev-stop \
	pull-agent local-build local-up local-down local-logs local-clean control-plane

agent: agent-build agent-test agent-push

agent-build:
	docker buildx build --platform linux/amd64 --load -t $(AGENT_IMAGE_NAME):test agent/

agent-test:
	cd tests && AGENT_TEST_IMAGE=$(AGENT_IMAGE_NAME):test npm run test:agent

agent-push:
	docker buildx build --platform $(PLATFORMS) -t $(AGENT_IMAGE):$(TAG) --push agent/

control-plane:
	docker buildx build --platform $(PLATFORMS) -t $(DASHBOARD_IMAGE):$(TAG) --push control-plane/

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

install-test:
	@echo "Installing test dependencies (npm)"
	@cd tests && npm install

install-dev: install-test
	@echo "Installing development dependencies..."
	@echo "Installing frontend dependencies (npm)..."
	@cd control-plane/frontend && npm install
	@echo "All dependencies installed successfully!"

dev:
	@echo "=== Development Config ==="
	@echo "  DATABASE_PATH: $(CLAWORC_DATABASE_PATH)"
	@echo ""
	@echo "Control plane: http://localhost:8000"
	@echo "Frontend:      http://localhost:5173"
	@echo ""
	@(cd control-plane && $(shell go env GOPATH)/bin/air) & \
	(cd control-plane/frontend && npm run dev) & \
	wait

dev-stop:
	@echo "Stopping development servers..."
	@-pkill -f "air" 2>/dev/null || true
	@-lsof -ti:8000 | xargs kill -9 2>/dev/null || true
	@-lsof -ti:5173 | xargs kill -9 2>/dev/null || true
	@echo "Development servers stopped"

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
	