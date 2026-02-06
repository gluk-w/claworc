AGENT_IMAGE := glukw/openclaw-vnc-chrome
DASHBOARD_IMAGE := glukw/claworc-dashboard
TAG := latest
PLATFORM := linux/amd64

KUBECONFIG := --kubeconfig ../kubeconfig
HELM_RELEASE := claworc
HELM_NAMESPACE := claworc

.PHONY: agent-build agent-push agent dashboard-build dashboard-push dashboard docker-prune \
	helm-install helm-upgrade helm-uninstall helm-template install-dev dev dev-stop

agent-build:
	docker build --platform $(PLATFORM) -t $(AGENT_IMAGE):$(TAG) agent/

agent-push:
	docker push $(AGENT_IMAGE):$(TAG)

agent: agent-build agent-push

dashboard-build:
	docker build --platform $(PLATFORM) -t $(DASHBOARD_IMAGE):$(TAG) dashboard/

dashboard-push:
	docker push $(DASHBOARD_IMAGE):$(TAG)

dashboard: dashboard-build dashboard-push

docker-prune:
	docker system prune -af

helm-install:
	helm install $(HELM_RELEASE) helm/ --namespace $(HELM_NAMESPACE) --create-namespace $(KUBECONFIG)

helm-upgrade:
	helm upgrade $(HELM_RELEASE) helm/ --namespace $(HELM_NAMESPACE) $(KUBECONFIG)

helm-uninstall:
	helm uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE) $(KUBECONFIG)

helm-template:
	helm template $(HELM_RELEASE) helm/ --namespace $(HELM_NAMESPACE) $(KUBECONFIG)

install-dev:
	@echo "Installing development dependencies..."
	@echo "Installing backend dependencies (Poetry)..."
	@cd dashboard && poetry install
	@echo "Installing frontend dependencies (npm)..."
	@cd dashboard/frontend && npm install
	@echo "All dependencies installed successfully!"

dev:
	@echo "Starting local development servers..."
	@echo "Backend will run on http://localhost:8000"
	@echo "Frontend will run on http://localhost:5173"
	@echo ""
	@(cd dashboard && CLAWORC_DATABASE_PATH=/tmp/claworc.db poetry run uvicorn backend.app:app --reload --port 8000) & \
	(cd dashboard/frontend && npm run dev) & \
	wait

dev-stop:
	@echo "Stopping development servers..."
	@-pkill -f "uvicorn backend.app:app" 2>/dev/null || true
	@-lsof -ti:8000 | xargs kill 2>/dev/null || true
	@-lsof -ti:5173 | xargs kill 2>/dev/null || true
	@echo "Development servers stopped"
