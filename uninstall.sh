#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="claworc-dashboard"
DASHBOARD_IMAGE="glukw/claworc"
AGENT_IMAGE="glukw/openclaw-vnc-chromium"

confirm() {
    printf "%s [Y/n]: " "$1"
    read -r answer
    case "${answer:-y}" in
        [Yy]*) return 0 ;;
        *)     return 1 ;;
    esac
}

echo ""
echo "=== Claworc Uninstaller ==="
echo ""

# --- Detect deployment type --------------------------------------------------

FOUND_DOCKER=0
FOUND_K8S=0

if docker container inspect "$CONTAINER_NAME" &>/dev/null 2>&1; then
    FOUND_DOCKER=1
fi

if command -v helm &>/dev/null && helm list -A 2>/dev/null | grep -q claworc; then
    FOUND_K8S=1
fi

if [[ "$FOUND_DOCKER" -eq 0 && "$FOUND_K8S" -eq 0 ]]; then
    echo "No Claworc installation detected."
    echo ""
    echo "If you installed manually, use:"
    echo "  Docker:      docker rm -f $CONTAINER_NAME"
    echo "  Kubernetes:  helm uninstall claworc -n claworc"
    exit 0
fi

# --- Docker uninstall --------------------------------------------------------

if [[ "$FOUND_DOCKER" -eq 1 ]]; then
    echo "Docker installation detected."
    echo ""

    if confirm "Remove dashboard container ($CONTAINER_NAME)?"; then
        docker rm -f "$CONTAINER_NAME" >/dev/null
        echo "  Removed $CONTAINER_NAME"
    fi

    # Find and remove agent containers
    AGENTS=$(docker ps -a --filter "name=bot-" --format '{{.Names}}' 2>/dev/null || true)
    if [[ -n "$AGENTS" ]]; then
        COUNT=$(echo "$AGENTS" | wc -l | tr -d ' ')
        echo ""
        if confirm "Remove $COUNT agent container(s)?"; then
            echo "$AGENTS" | xargs docker rm -f >/dev/null
            echo "  Removed $COUNT agent container(s)"
        fi
    fi

    # Remove data directory
    DATA_DIR="$HOME/.claworc/data"
    if [[ -d "$DATA_DIR" ]]; then
        echo ""
        if confirm "Remove data directory ($DATA_DIR)?"; then
            rm -rf "$DATA_DIR"
            echo "  Removed $DATA_DIR"
        fi
    fi

    # Remove images
    echo ""
    if confirm "Remove Docker images ($DASHBOARD_IMAGE, $AGENT_IMAGE)?"; then
        docker rmi "$DASHBOARD_IMAGE:latest" 2>/dev/null || true
        docker rmi "$AGENT_IMAGE:latest" 2>/dev/null || true
        echo "  Removed images"
    fi
fi

# --- Kubernetes uninstall ----------------------------------------------------

if [[ "$FOUND_K8S" -eq 1 ]]; then
    echo "Kubernetes installation detected."
    echo ""

    # Detect namespace from helm
    NAMESPACE=$(helm list -A 2>/dev/null | grep claworc | awk '{print $2}')
    NAMESPACE="${NAMESPACE:-claworc}"

    if confirm "Uninstall Helm release 'claworc' from namespace '$NAMESPACE'?"; then
        helm uninstall claworc -n "$NAMESPACE"
        echo "  Helm release removed"
    fi

    echo ""
    if confirm "Delete namespace '$NAMESPACE'?"; then
        kubectl delete namespace "$NAMESPACE" --ignore-not-found
        echo "  Namespace removed"
    fi
fi

echo ""
echo "Uninstall complete."
