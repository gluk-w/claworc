# Load .env.development defaults, then .env for personal overrides
include .env.development
-include .env
export

AGENT_IMAGE := glukw/claworc-agent
BROWSER_BASE_IMAGE := glukw/claworc-browser-base
BROWSER_CHROMIUM_IMAGE := glukw/claworc-browser-chromium
BROWSER_CHROME_IMAGE := glukw/claworc-browser-chrome
BROWSER_BRAVE_IMAGE := glukw/claworc-browser-brave
# Legacy aliases retained for targets that still use the old naming below.
AGENT_BASE_IMAGE := $(BROWSER_BASE_IMAGE)
AGENT_IMAGE_NAME := claworc-browser-chromium
DASHBOARD_IMAGE := glukw/claworc
TAG := latest
PLATFORMS := linux/amd64,linux/arm64
NATIVE_ARCH := $(shell uname -m | sed 's/x86_64/amd64/')

CACHE_ARGS ?=

KUBECONFIG := ../kubeconfig
HELM_RELEASE := claworc
HELM_NAMESPACE := claworc

.PHONY: agent agent-ci agent-base agent-base-china agent-build agent-test agent-push agent-exec dashboard docker-prune release \
	helm-install helm-upgrade helm-uninstall helm-template install-dev dev dev-docs \
	pull-agent local-build local-up local-down local-logs local-clean control-plane \
	ssh-integration-test ssh-file-integration-test test-integration-backend extract-models scrape-models test \
	worker-deploy worker-test worker-build-models site-dev site-build site-deploy \
	e2e e2e-debug e2e-install

agent: agent-base agent-push

# CI entry point: build base image, build all variants locally, run vitest
# against the loaded images, and only push if tests pass. Used by
# .github/workflows/agent.yml on the main branch.
agent-ci: agent-base agent-build agent-test agent-push
	@echo "Agent images built, tested, and pushed."

agent-base:
	@echo "Building and pushing browser-base image..."
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) -t $(BROWSER_BASE_IMAGE):$(TAG) -f agent/browser/Dockerfile.base --push agent/browser/

agent-base-china:
	@echo "Building and pushing browser-base image (China mirrors)..."
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) --build-arg USE_CHINA_MIRRORS=true -t $(BROWSER_BASE_IMAGE):$(TAG) -f agent/browser/Dockerfile.base --push agent/browser/

agent-build:
	@echo "Building images locally (agent + browser variants)..."
	docker buildx build --platform linux/$(NATIVE_ARCH) $(CACHE_ARGS) -t $(AGENT_IMAGE):$(TAG) -f agent/instance/Dockerfile --load agent/instance/
	docker buildx build --platform linux/$(NATIVE_ARCH) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(BROWSER_BASE_IMAGE):$(TAG) -t $(BROWSER_CHROMIUM_IMAGE):$(TAG) -f agent/browser/Dockerfile.chromium --load agent/browser/
	docker buildx build --platform linux/amd64 $(CACHE_ARGS) --build-arg BASE_IMAGE=$(BROWSER_BASE_IMAGE):$(TAG) -t $(BROWSER_CHROME_IMAGE):$(TAG) -f agent/browser/Dockerfile.chrome --load agent/browser/
	docker buildx build --platform linux/$(NATIVE_ARCH) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(BROWSER_BASE_IMAGE):$(TAG) -t $(BROWSER_BRAVE_IMAGE):$(TAG) -f agent/browser/Dockerfile.brave --load agent/browser/

agent-test:
	cd agent/tests && AGENT_INSTANCE_TEST_IMAGE=$(AGENT_IMAGE):$(TAG) \
		AGENT_TEST_IMAGE=$(BROWSER_CHROMIUM_IMAGE):$(TAG) \
		AGENT_CHROME_TEST_IMAGE=$(BROWSER_CHROME_IMAGE):$(TAG) \
		AGENT_BRAVE_TEST_IMAGE=$(BROWSER_BRAVE_IMAGE):$(TAG) \
		npm run test


agent-push:
	@echo "Pushing all agent + browser images in parallel..."
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) -t $(AGENT_IMAGE):$(TAG) -f agent/instance/Dockerfile --push agent/instance/ & \
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(BROWSER_BASE_IMAGE):$(TAG) -t $(BROWSER_CHROMIUM_IMAGE):$(TAG) -f agent/browser/Dockerfile.chromium --push agent/browser/ & \
	docker buildx build --platform linux/amd64 $(CACHE_ARGS) --build-arg BASE_IMAGE=$(BROWSER_BASE_IMAGE):$(TAG) -t $(BROWSER_CHROME_IMAGE):$(TAG) -f agent/browser/Dockerfile.chrome --push agent/browser/ & \
	docker buildx build --platform $(PLATFORMS) $(CACHE_ARGS) --build-arg BASE_IMAGE=$(BROWSER_BASE_IMAGE):$(TAG) -t $(BROWSER_BRAVE_IMAGE):$(TAG) -f agent/browser/Dockerfile.brave --push agent/browser/ & \
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
	@cd agent/tests && npm install

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
	CLAWORC_AUTH_DISABLED=true CLAWORC_LLM_RESPONSE_LOG=$(CURDIR)/llm-responses.log goreman -set-ports=false start

ssh-integration-test:
	docker build -f agent/instance/Dockerfile -t claworc-agent:local agent/instance/
	cd control-plane && go test -tags docker_integration -v -timeout 300s ./internal/sshproxy/ -run TestIntegration

ssh-file-integration-test:
	docker build -f agent/instance/Dockerfile -t claworc-agent:local agent/instance/
	cd agent/tests && npm run test:ssh -- --testPathPattern file.test

test-integration-backend:
	cd control-plane && CLAWORC_LLM_GATEWAY_PORT=40001 go test -tags docker_integration -v -timeout 600s -count=1 \
		./internal/handlers/ -run TestIntegration

e2e-install:
	cd e2e && npm install && npx playwright install --with-deps chromium

e2e:
	./e2e/run.sh $(KUBECONFIG)

e2e-debug:
	E2E_KEEP=1 ./e2e/run.sh $(KUBECONFIG)

test:
	cd control-plane && go test ./internal/...

extract-models:
	python3 scripts/extract_models.py

scrape-models:
	python3 scripts/scrape_provider_docs.py

dev-docs:
	cd website_docs && npx mint dev

worker-build-models:
	cd website/worker && node build-models.mjs

worker-deploy: worker-build-models
	cd website/worker && npx wrangler deploy

worker-test:
	cd website/worker && npm install && npx vitest run

# Astro marketing site (claworc.com). Deployed as a Cloudflare Worker via
# website/wrangler.toml — independent of website/worker/ (the providers API).
site-dev:
	cd website && npm install && npx astro dev

site-build:
	cd website && npm install && npx astro build

site-deploy:
	cd website && npm install && npx astro build && npx wrangler deploy
