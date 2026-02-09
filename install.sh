#!/usr/bin/env bash
set -euo pipefail

# Defaults
DASHBOARD_IMAGE="glukw/claworc-dashboard"
AGENT_IMAGE="glukw/openclaw-vnc-chrome"
TAG="latest"
CONTAINER_NAME="claworc-dashboard"

# --- Helpers -----------------------------------------------------------------

prompt() {
    local var="$1" prompt_text="$2" default="$3"
    printf "%s [%s]: " "$prompt_text" "$default"
    read -r input </dev/tty
    eval "$var=\"\${input:-$default}\""
}

confirm() {
    printf "\nProceed? [Y/n]: "
    read -r answer </dev/tty
    case "${answer:-y}" in
        [Yy]*) return 0 ;;
        *)     echo "Aborted."; exit 1 ;;
    esac
}

# --- Mode selection ----------------------------------------------------------

echo ""
echo "=== Claworc Installer ==="
echo ""
echo "How would you like to deploy?"
echo "  1) Docker      - Run on a single machine using Docker"
echo "  2) Kubernetes  - Deploy to a Kubernetes cluster using Helm"
echo ""
printf "Select deployment mode [1]: "
read -r MODE </dev/tty
MODE="${MODE:-1}"

# =============================================================================
# Docker path
# =============================================================================

install_docker() {
    echo ""
    echo "--- Docker Deployment ---"
    echo ""

    # --- Prerequisites -------------------------------------------------------

    if ! command -v docker &>/dev/null; then
        echo "Error: docker not found. Install Docker Desktop or Docker Engine."
        exit 1
    fi

    if ! docker info &>/dev/null 2>&1; then
        echo "Error: Docker daemon is not running. Start Docker and try again."
        exit 1
    fi

    echo "Docker is available."
    echo ""

    # --- Configuration -------------------------------------------------------

    prompt PORT        "Dashboard port"      "8000"
    prompt DATA_DIR    "Data directory"       "$HOME/.claworc/data"
    echo ""

    # Resolve to absolute path (mkdir first since parent may not exist yet)
    mkdir -p "$DATA_DIR"
    DATA_DIR="$(cd "$DATA_DIR" && pwd)"

    # --- Detect existing installation ----------------------------------------

    if docker container inspect "$CONTAINER_NAME" &>/dev/null; then
        echo "Existing installation detected ($CONTAINER_NAME)."
        printf "Remove and reinstall? [Y/n]: "
        read -r answer </dev/tty
        case "${answer:-y}" in
            [Yy]*) docker rm -f "$CONTAINER_NAME" >/dev/null ;;
            *)     echo "Aborted."; exit 1 ;;
        esac
        echo ""
    fi

    # --- Summary -------------------------------------------------------------

    echo "=== Configuration ==="
    echo "  Port:       $PORT"
    echo "  Data dir:   $DATA_DIR"
    echo "  Dashboard:  $DASHBOARD_IMAGE:$TAG"
    echo "  Agent:      $AGENT_IMAGE:$TAG"

    confirm

    # --- Pull images ---------------------------------------------------------

    echo ""
    echo "Pulling dashboard image..."
    docker pull "$DASHBOARD_IMAGE:$TAG"

    echo ""
    echo "Pulling agent image..."
    docker pull "$AGENT_IMAGE:$TAG"

    # --- Launch --------------------------------------------------------------

    echo ""
    echo "Starting dashboard..."
    docker run -d \
        --name "$CONTAINER_NAME" \
        -p "$PORT:8000" \
        -v /var/run/docker.sock:/var/run/docker.sock \
        -v "$DATA_DIR":"$DATA_DIR" \
        -e CLAWORC_DATABASE_PATH="$DATA_DIR/claworc.db" \
        --restart unless-stopped \
        "$DASHBOARD_IMAGE:$TAG" >/dev/null

    # --- Initial Admin Setup -------------------------------------------------

    echo ""
    echo "--- Initial Admin Setup ---"
    printf "Admin username [admin]: "
    read -r ADMIN_USER </dev/tty
    ADMIN_USER="${ADMIN_USER:-admin}"

    while true; do
        printf "Admin password: "
        read -rs ADMIN_PASS </dev/tty
        echo
        printf "Confirm password: "
        read -rs ADMIN_PASS_CONFIRM </dev/tty
        echo
        if [ "$ADMIN_PASS" = "$ADMIN_PASS_CONFIRM" ] && [ -n "$ADMIN_PASS" ]; then
            break
        fi
        echo "Passwords do not match or are empty. Try again."
    done

    echo "Creating admin user..."
    docker exec "$CONTAINER_NAME" /app/claworc --create-admin --username "$ADMIN_USER" --password "$ADMIN_PASS"

    echo ""
    echo "=== Claworc is running ==="
    echo ""
    echo "  Dashboard:  http://localhost:$PORT"
    echo "  Container:  $CONTAINER_NAME"
    echo "  Data:       $DATA_DIR"
    echo ""
    echo "Commands:"
    echo "  docker logs -f $CONTAINER_NAME     # View logs"
    echo "  docker stop $CONTAINER_NAME        # Stop"
    echo "  docker start $CONTAINER_NAME       # Start again"
    echo "  docker rm -f $CONTAINER_NAME       # Remove"
}

# =============================================================================
# Kubernetes path
# =============================================================================

install_kubernetes() {
    echo ""
    echo "--- Kubernetes Deployment ---"
    echo ""

    # --- Prerequisites -------------------------------------------------------

    local missing=0

    if ! command -v helm &>/dev/null; then
        echo "Error: helm not found. Install Helm: https://helm.sh/docs/intro/install/"
        missing=1
    fi

    if ! command -v kubectl &>/dev/null; then
        echo "Error: kubectl not found. Install kubectl: https://kubernetes.io/docs/tasks/tools/"
        missing=1
    fi

    [[ "$missing" -eq 1 ]] && exit 1

    echo "Prerequisites OK (helm + kubectl found)."
    echo ""

    # --- Configuration -------------------------------------------------------

    prompt KUBECONFIG_PATH "Kubeconfig path"           "$HOME/.kube/config"
    prompt NAMESPACE       "Namespace"                  "claworc"
    echo ""

    echo "How should the dashboard be accessed?"
    echo "  1) NodePort          - Expose on a port on the cluster node"
    echo "  2) kubectl port-forward - Keep service cluster-internal, access locally"
    echo ""
    printf "Select access method [1]: "
    read -r ACCESS_METHOD </dev/tty
    ACCESS_METHOD="${ACCESS_METHOD:-1}"

    SERVICE_HELM_ARGS=""
    if [[ "$ACCESS_METHOD" == "2" ]]; then
        SERVICE_TYPE="ClusterIP"
        SERVICE_HELM_ARGS="--set service.type=ClusterIP"
    else
        SERVICE_TYPE="NodePort"
        prompt NODE_PORT "Node port" "30000"
        SERVICE_HELM_ARGS="--set service.type=NodePort --set service.nodePort=$NODE_PORT"
    fi
    echo ""

    # --- Detect existing installation ----------------------------------------

    if helm list --kubeconfig "$KUBECONFIG_PATH" -n "$NAMESPACE" 2>/dev/null | grep -q claworc; then
        echo "Existing Helm release detected."
        printf "Upgrade existing installation? [Y/n]: "
        read -r answer </dev/tty
        case "${answer:-y}" in
            [Yy]*) HELM_CMD="upgrade" ;;
            *)     echo "Aborted."; exit 1 ;;
        esac
    else
        HELM_CMD="install"
    fi

    # --- Locate chart --------------------------------------------------------

    if [[ -d "./helm" ]]; then
        CHART_PATH="./helm"
    else
        echo "Helm chart not found locally. Cloning repository..."
        TMPDIR="$(mktemp -d)"
        git clone --depth 1 https://github.com/gluk-w/claworc.git "$TMPDIR/claworc"
        CHART_PATH="$TMPDIR/claworc/helm"
        trap "rm -rf '$TMPDIR'" EXIT
    fi

    # --- Summary -------------------------------------------------------------

    echo ""
    echo "=== Configuration ==="
    echo "  Action:      helm $HELM_CMD"
    echo "  Kubeconfig:  $KUBECONFIG_PATH"
    echo "  Namespace:   $NAMESPACE"
    echo "  Chart:       $CHART_PATH"
    if [[ "$SERVICE_TYPE" == "NodePort" ]]; then
        echo "  Access:      NodePort ($NODE_PORT)"
    else
        echo "  Access:      kubectl port-forward"
    fi

    confirm

    # --- Deploy --------------------------------------------------------------

    echo ""
    echo "Running helm ${HELM_CMD}..."
    # shellcheck disable=SC2086
    helm "$HELM_CMD" claworc "$CHART_PATH" \
        --namespace "$NAMESPACE" \
        --create-namespace \
        --kubeconfig "$KUBECONFIG_PATH" \
        $SERVICE_HELM_ARGS

    # Wait for pod to be ready
    echo ""
    echo "Waiting for pod to be ready..."
    kubectl wait --for=condition=ready pod -l app=claworc -n "$NAMESPACE" --kubeconfig "$KUBECONFIG_PATH" --timeout=120s 2>/dev/null || true

    # --- Initial Admin Setup -------------------------------------------------

    echo ""
    echo "--- Initial Admin Setup ---"
    printf "Admin username [admin]: "
    read -r ADMIN_USER </dev/tty
    ADMIN_USER="${ADMIN_USER:-admin}"

    while true; do
        printf "Admin password: "
        read -rs ADMIN_PASS </dev/tty
        echo
        printf "Confirm password: "
        read -rs ADMIN_PASS_CONFIRM </dev/tty
        echo
        if [ "$ADMIN_PASS" = "$ADMIN_PASS_CONFIRM" ] && [ -n "$ADMIN_PASS" ]; then
            break
        fi
        echo "Passwords do not match or are empty. Try again."
    done

    echo "Creating admin user..."
    kubectl exec deploy/claworc -n "$NAMESPACE" --kubeconfig "$KUBECONFIG_PATH" -- /app/claworc --create-admin --username "$ADMIN_USER" --password "$ADMIN_PASS"

    echo ""
    echo "=== Claworc deployed to Kubernetes ==="
    echo ""
    if [[ "$SERVICE_TYPE" == "NodePort" ]]; then
        echo "  Dashboard:  http://<node-ip>:$NODE_PORT"
    else
        echo "  To access the dashboard, run:"
        echo "    kubectl port-forward svc/claworc 8000:8001 -n $NAMESPACE"
        echo ""
        echo "  Dashboard:  http://localhost:8000"
    fi
    echo ""
    echo "Commands:"
    echo "  kubectl get pods -n $NAMESPACE                     # Check pods"
    echo "  kubectl logs -f deploy/claworc -n $NAMESPACE       # View logs"
    echo "  helm upgrade claworc $CHART_PATH -n $NAMESPACE     # Upgrade"
    echo "  helm uninstall claworc -n $NAMESPACE               # Remove"
}

# --- Dispatch ----------------------------------------------------------------

case "$MODE" in
    1) install_docker ;;
    2) install_kubernetes ;;
    *) echo "Invalid selection: $MODE"; exit 1 ;;
esac
