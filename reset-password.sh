#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="claworc-dashboard"
NAMESPACE="claworc"

echo ""
echo "=== Claworc Password Reset ==="
echo ""

printf "Username: "
read -r USERNAME

if [ -z "$USERNAME" ]; then
    echo "Error: Username is required."
    exit 1
fi

while true; do
    printf "New password: "
    read -rs PASSWORD
    echo
    printf "Confirm password: "
    read -rs PASSWORD_CONFIRM
    echo
    if [ "$PASSWORD" = "$PASSWORD_CONFIRM" ] && [ -n "$PASSWORD" ]; then
        break
    fi
    echo "Passwords do not match or are empty. Try again."
done

# Auto-detect deployment mode
if docker container inspect "$CONTAINER_NAME" &>/dev/null 2>&1; then
    echo "Detected Docker deployment."
    docker exec "$CONTAINER_NAME" /app/claworc --reset-password --username "$USERNAME" --password "$PASSWORD"
elif command -v kubectl &>/dev/null && kubectl get deploy/claworc -n "$NAMESPACE" &>/dev/null 2>&1; then
    echo "Detected Kubernetes deployment."
    kubectl exec deploy/claworc -n "$NAMESPACE" -- /app/claworc --reset-password --username "$USERNAME" --password "$PASSWORD"
else
    echo "Error: Could not find Claworc deployment (checked Docker container '$CONTAINER_NAME' and K8s namespace '$NAMESPACE')."
    exit 1
fi

echo ""
echo "Note: Existing sessions for this user will expire within 1 hour."
echo "For immediate session invalidation, use the admin UI's Reset Password button."
