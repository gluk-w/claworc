# Load .env.development defaults, then .env for personal overrides
include .env.development
-include .env
export

AGENT_BASE_IMAGE := glukw/openclaw-vnc-base
AGENT_IMAGE_NAME := openclaw-vnc-chromium
AGENT_IMAGE := glukw/$(AGENT_IMAGE_NAME)
AGENT_CHROME_IMAGE_NAME := openclaw-vnc-chrome
AGENT_CHROME_IMAGE := glukw/$(AGENT_CHROME_IMAGE_NAME)
AGENT_BRAVE_IMAGE_NAME := openclaw-vnc-brave
AGENT_BRAVE_IMAGE := glukw/$(AGENT_BRAVE_IMAGE_NAME)
DASHBOARD_IMAGE := glukw/claworc
TAG := latest
PLATFORMS := linux/amd64,linux/arm64
NATIVE_ARCH := $(shell uname -m | sed 's/x86_64/amd64/')

CACHE_ARGS ?=

KUBECONFIG := ../kubeconfig
HELM_RELEASE := claworc
HELM_NAMESPACE := claworc

.PHONY: agent agent-base agent-build agent-test agent-push agent-exec dashboard docker-prune release \
	helm-install helm-upgrade helm-uninstall helm-template install-dev dev \
	pull-agent local-build local-up local-down local-logs local-clean control-plane \
	ssh-integration-test ssh-file-integration-test

agent: agent-base agent-build agent-test agent-push

agent-base:
	@echo "Building and pushing base image..."
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) -t $(AGENT_BASE_IMAGE):$(TAG) --push agent/

agent-build:
	@echo "Building agent images (chromium, chrome, brave) in parallel..."
	docker buildx build --platform linux/$(NATIVE_ARCH) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):$(TAG) -t $(AGENT_IMAGE):$(TAG) -f agent/Dockerfile.chromium --load agent/
	docker buildx build --platform linux/amd64 $(CACHE_ARGS) --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):$(TAG) -t $(AGENT_CHROME_IMAGE):$(TAG) -f agent/Dockerfile.chrome --load agent/
	docker buildx build --platform linux/$(NATIVE_ARCH) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):$(TAG) -t $(AGENT_BRAVE_IMAGE):$(TAG) -f agent/Dockerfile.brave --load agent/

agent-test:
	cd tests && AGENT_TEST_IMAGE=$(AGENT_IMAGE):$(TAG) \
		AGENT_CHROME_TEST_IMAGE=$(AGENT_CHROME_IMAGE):$(TAG) \
		AGENT_BRAVE_TEST_IMAGE=$(AGENT_BRAVE_IMAGE):$(TAG) \
		npm run test:agent

agent-push:
	@echo "Pushing all agent images in parallel..."
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):$(TAG) -t $(AGENT_IMAGE):$(TAG) -f agent/Dockerfile.chromium --push agent/ & \
	docker buildx build --platform linux/amd64 $(CACHE_ARGS) --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):$(TAG) -t $(AGENT_CHROME_IMAGE):$(TAG) -f agent/Dockerfile.chrome --push agent/ & \
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):$(TAG) -t $(AGENT_BRAVE_IMAGE):$(TAG) -f agent/Dockerfile.brave --push agent/ & \
	wait

AGENT_CONTAINER := claworc-agent-exec
AGENT_SSH_PORT := 2222

agent-exec:
	@echo "Stopping existing container (if any)..."
	@-docker rm -f $(AGENT_CONTAINER) 2>/dev/null || true
	@echo "Starting $(AGENT_IMAGE_NAME):test in background..."
	docker run -d --name $(AGENT_CONTAINER) -p $(AGENT_SSH_PORT):22 $(AGENT_IMAGE_NAME):test
	@echo "Installing SSH public key..."
	@docker exec $(AGENT_CONTAINER) bash -c 'mkdir -p /root/.ssh && chmod 700 /root/.ssh'
	@docker cp $(CURDIR)/ssh_key.pub $(AGENT_CONTAINER):/root/.ssh/authorized_keys
	@docker exec $(AGENT_CONTAINER) chown root:root /root/.ssh/authorized_keys
	@docker exec $(AGENT_CONTAINER) chmod 600 /root/.ssh/authorized_keys
	# @docker exec openclaw config set gateway.auth.token the-token-does-not-matter
	@echo ""
	@echo "=== Container Running ==="
	@echo "  Name:  $(AGENT_CONTAINER)"
	@echo "  Image: $(AGENT_IMAGE_NAME):test"
	@echo ""
	@echo "=== SSH Access ==="
	@echo "  ssh -i ./ssh_key -o StrictHostKeyChecking=no -p $(AGENT_SSH_PORT) root@localhost"
	@echo ""
	@echo "  Or exec directly:"
	@echo "  docker exec -it $(AGENT_CONTAINER) bash"

control-plane:
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) -t $(DASHBOARD_IMAGE):$(TAG) --push control-plane/

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
	@echo "Installing Go dependencies..."
	@cd control-plane && go mod download
	@echo "Installing air (live-reload)..."
	@go install github.com/air-verse/air@latest
	@echo "Installing goreman (process manager)..."
	@go install github.com/mattn/goreman@latest
	@echo "Installing frontend dependencies (npm)..."
	@cd control-plane/frontend && npm install
	@echo "All dependencies installed successfully!"

dev:
	@echo "=== Development Config ==="
	@echo "  DATA_PATH: $(CLAWORC_DATA_PATH)"
	@echo ""
	@echo "Control plane: http://localhost:8000"
	@echo "Frontend:      http://localhost:5173"
	@echo ""
	CLAWORC_AUTH_DISABLED=true goreman -set-ports=false start

# --- Local Docker testing ---------------------------------------------------

local-build:
	docker build -t $(AGENT_BASE_IMAGE):local agent/
	docker build --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):local -f agent/Dockerfile.chromium -t claworc-agent:local agent/
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

ssh-integration-test:
	docker build -t $(AGENT_BASE_IMAGE):local agent/
	docker build --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):local -f agent/Dockerfile.chromium -t claworc-agent:local agent/
	cd control-plane && go test -tags docker_integration -v -timeout 300s ./internal/sshproxy/ -run TestIntegration

ssh-file-integration-test:
	docker build -t $(AGENT_BASE_IMAGE):local agent/
	docker build --build-arg BASE_IMAGE=$(AGENT_BASE_IMAGE):local -f agent/Dockerfile.chromium -t claworc-agent:local agent/
	cd tests && npm run test:ssh -- --testPathPattern file.test

e2e-docker-tests:
	./scripts/run_tests.sh
	