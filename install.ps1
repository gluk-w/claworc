#Requires -Version 5.1
<#
.SYNOPSIS
    Claworc installer for Windows
.DESCRIPTION
    Installs Claworc using Docker or deploys to Kubernetes using Helm
#>

param(
    [string]$DashboardImage = "glukw/claworc",
    [string]$Tag = "latest",
    [string]$ContainerName = "claworc"
)

$ErrorActionPreference = "Stop"

# --- Helpers -----------------------------------------------------------------

function Prompt-Value {
    param(
        [string]$PromptText,
        [string]$Default
    )
    $input = Read-Host "$PromptText [$Default]"
    if ([string]::IsNullOrWhiteSpace($input)) {
        return $Default
    }
    return $input
}

function Prompt-SecureValue {
    param(
        [string]$PromptText
    )
    $secureString = Read-Host $PromptText -AsSecureString
    $BSTR = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureString)
    try {
        return [System.Runtime.InteropServices.Marshal]::PtrToStringAuto($BSTR)
    }
    finally {
        [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($BSTR)
    }
}

function Confirm-Proceed {
    $answer = Read-Host "`nProceed? [Y/n]"
    if ([string]::IsNullOrWhiteSpace($answer)) { $answer = "y" }
    if ($answer -notmatch '^[Yy]') {
        Write-Host "Aborted."
        exit 1
    }
}

function Test-Command {
    param([string]$Command)
    try {
        Get-Command $Command -ErrorAction Stop | Out-Null
        return $true
    }
    catch {
        return $false
    }
}

# --- Mode selection ----------------------------------------------------------

Write-Host ""
Write-Host "=== Claworc Installer ==="
Write-Host ""
Write-Host "How would you like to deploy?"
Write-Host "  1) Docker      - Run on a single machine using Docker"
Write-Host "  2) Kubernetes  - Deploy to a Kubernetes cluster using Helm"
Write-Host ""

$mode = Read-Host "Select deployment mode [1]"
if ([string]::IsNullOrWhiteSpace($mode)) { $mode = "1" }

# =============================================================================
# Docker path
# =============================================================================

function Install-Docker {
    Write-Host ""
    Write-Host "--- Docker Deployment ---"
    Write-Host ""

    # --- Prerequisites -------------------------------------------------------

    if (-not (Test-Command "docker")) {
        Write-Host "Error: docker not found. Install Docker Desktop for Windows."
        Write-Host "Download from: https://www.docker.com/products/docker-desktop"
        exit 1
    }

    try {
        docker info 2>$null | Out-Null
    }
    catch {
        Write-Host "Error: Docker daemon is not running. Start Docker Desktop and try again."
        exit 1
    }

    Write-Host "Docker is available."
    Write-Host ""

    # --- Configuration -------------------------------------------------------

    $port = Prompt-Value -PromptText "Dashboard port" -Default "8000"
    $defaultDataDir = Join-Path $env:USERPROFILE ".claworc\data"
    $dataDir = Prompt-Value -PromptText "Data directory" -Default $defaultDataDir
    Write-Host ""

    # Resolve to absolute path and create directory
    if (-not (Test-Path $dataDir)) {
        New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
    }
    $dataDir = (Resolve-Path $dataDir).Path

    # --- Detect existing installation ----------------------------------------

    try {
        docker container inspect $ContainerName 2>$null | Out-Null
        $exists = $true
    }
    catch {
        $exists = $false
    }

    if ($exists) {
        Write-Host "Existing installation detected ($ContainerName)."
        $answer = Read-Host "Remove and reinstall? [Y/n]"
        if ([string]::IsNullOrWhiteSpace($answer)) { $answer = "y" }
        if ($answer -match '^[Yy]') {
            docker rm -f $ContainerName | Out-Null
        }
        else {
            Write-Host "Aborted."
            exit 1
        }
        Write-Host ""
    }

    # --- Summary -------------------------------------------------------------

    Write-Host "=== Configuration ==="
    Write-Host "  Port:       $port"
    Write-Host "  Data dir:   $dataDir"
    Write-Host "  Dashboard:  ${DashboardImage}:${Tag}"

    Confirm-Proceed

    # --- Pull images ---------------------------------------------------------

    Write-Host ""
    Write-Host "Pulling dashboard image..."
    docker pull --platform linux/amd64 "${DashboardImage}:${Tag}"

    # --- Launch --------------------------------------------------------------

    Write-Host ""
    Write-Host "Starting dashboard..."

    # Convert Windows path to Unix-style for Docker
    $dataDirUnix = $dataDir -replace '\\', '/' -replace '^([A-Za-z]):', '/$1'

    docker run -d `
        --platform linux/amd64 `
        --name $ContainerName `
        -p "${port}:8000" `
        -v /var/run/docker.sock:/var/run/docker.sock `
        -v "${dataDirUnix}:${dataDirUnix}" `
        -e "CLAWORC_DATABASE_PATH=${dataDirUnix}/claworc.db" `
        --restart unless-stopped `
        "${DashboardImage}:${Tag}" | Out-Null

    # --- Initial Admin Setup -------------------------------------------------

    Write-Host ""
    Write-Host "--- Initial Admin Setup ---"
    $adminUser = Prompt-Value -PromptText "Admin username" -Default "admin"

    while ($true) {
        $adminPass = Prompt-SecureValue -PromptText "Admin password"
        $adminPassConfirm = Prompt-SecureValue -PromptText "Confirm password"

        if (($adminPass -eq $adminPassConfirm) -and (-not [string]::IsNullOrWhiteSpace($adminPass))) {
            break
        }
        Write-Host "Passwords do not match or are empty. Try again."
    }

    Write-Host "Creating admin user..."
    docker exec $ContainerName /app/claworc --create-admin --username $adminUser --password $adminPass

    Write-Host ""
    Write-Host "=== Claworc is running ==="
    Write-Host ""
    Write-Host "  Dashboard:  http://localhost:$port"
    Write-Host "  Container:  $ContainerName"
    Write-Host "  Data:       $dataDir"
    Write-Host ""
    Write-Host "Commands:"
    Write-Host "  docker logs -f $ContainerName     # View logs"
    Write-Host "  docker stop $ContainerName        # Stop"
    Write-Host "  docker start $ContainerName       # Start again"
    Write-Host "  docker rm -f $ContainerName       # Remove"
}

# =============================================================================
# Kubernetes path
# =============================================================================

function Install-Kubernetes {
    Write-Host ""
    Write-Host "--- Kubernetes Deployment ---"
    Write-Host ""

    # --- Prerequisites -------------------------------------------------------

    $missing = 0

    if (-not (Test-Command "helm")) {
        Write-Host "Error: helm not found. Install Helm: https://helm.sh/docs/intro/install/"
        $missing = 1
    }

    if (-not (Test-Command "kubectl")) {
        Write-Host "Error: kubectl not found. Install kubectl: https://kubernetes.io/docs/tasks/tools/"
        $missing = 1
    }

    if ($missing -eq 1) { exit 1 }

    Write-Host "Prerequisites OK (helm + kubectl found)."
    Write-Host ""

    # --- Configuration -------------------------------------------------------

    $defaultKubeconfig = Join-Path $env:USERPROFILE ".kube\config"
    $kubeconfigPath = Prompt-Value -PromptText "Kubeconfig path" -Default $defaultKubeconfig
    $namespace = Prompt-Value -PromptText "Namespace" -Default "claworc"
    Write-Host ""

    Write-Host "How should the dashboard be accessed?"
    Write-Host "  1) NodePort          - Expose on a port on the cluster node"
    Write-Host "  2) kubectl port-forward - Keep service cluster-internal, access locally"
    Write-Host ""
    $accessMethod = Read-Host "Select access method [1]"
    if ([string]::IsNullOrWhiteSpace($accessMethod)) { $accessMethod = "1" }

    $serviceHelmArgs = @()
    if ($accessMethod -eq "2") {
        $serviceType = "ClusterIP"
        $serviceHelmArgs += "--set", "service.type=ClusterIP"
    }
    else {
        $serviceType = "NodePort"
        $nodePort = Prompt-Value -PromptText "Node port" -Default "30000"
        $serviceHelmArgs += "--set", "service.type=NodePort", "--set", "service.nodePort=$nodePort"
    }
    Write-Host ""

    # --- Detect existing installation ----------------------------------------

    $helmList = helm list --kubeconfig $kubeconfigPath -n $namespace 2>$null
    $helmCmd = "install"

    if ($helmList -match "claworc") {
        Write-Host "Existing Helm release detected."
        $answer = Read-Host "Upgrade existing installation? [Y/n]"
        if ([string]::IsNullOrWhiteSpace($answer)) { $answer = "y" }
        if ($answer -match '^[Yy]') {
            $helmCmd = "upgrade"
        }
        else {
            Write-Host "Aborted."
            exit 1
        }
    }

    # --- Locate chart --------------------------------------------------------

    if (Test-Path "./helm") {
        $chartPath = "./helm"
    }
    else {
        Write-Host "Helm chart not found locally. Cloning repository..."
        $tmpDir = Join-Path $env:TEMP "claworc-$(Get-Random)"
        git clone --depth 1 https://github.com/gluk-w/claworc.git $tmpDir
        $chartPath = Join-Path $tmpDir "helm"
    }

    # --- Summary -------------------------------------------------------------

    Write-Host ""
    Write-Host "=== Configuration ==="
    Write-Host "  Action:      helm $helmCmd"
    Write-Host "  Kubeconfig:  $kubeconfigPath"
    Write-Host "  Namespace:   $namespace"
    Write-Host "  Chart:       $chartPath"
    if ($serviceType -eq "NodePort") {
        Write-Host "  Access:      NodePort ($nodePort)"
    }
    else {
        Write-Host "  Access:      kubectl port-forward"
    }

    Confirm-Proceed

    # --- Deploy --------------------------------------------------------------

    Write-Host ""
    Write-Host "Running helm ${helmCmd}..."

    $helmArgs = @(
        $helmCmd,
        "claworc",
        $chartPath,
        "--namespace", $namespace,
        "--create-namespace",
        "--kubeconfig", $kubeconfigPath
    ) + $serviceHelmArgs

    & helm @helmArgs

    # Wait for pod to be ready
    Write-Host ""
    Write-Host "Waiting for pod to be ready..."
    kubectl wait --for=condition=ready pod -l app=claworc -n $namespace --kubeconfig $kubeconfigPath --timeout=120s 2>$null

    # --- Initial Admin Setup -------------------------------------------------

    Write-Host ""
    Write-Host "--- Initial Admin Setup ---"
    $adminUser = Prompt-Value -PromptText "Admin username" -Default "admin"

    while ($true) {
        $adminPass = Prompt-SecureValue -PromptText "Admin password"
        $adminPassConfirm = Prompt-SecureValue -PromptText "Confirm password"

        if (($adminPass -eq $adminPassConfirm) -and (-not [string]::IsNullOrWhiteSpace($adminPass))) {
            break
        }
        Write-Host "Passwords do not match or are empty. Try again."
    }

    Write-Host "Creating admin user..."
    kubectl exec deploy/claworc -n $namespace --kubeconfig $kubeconfigPath -- /app/claworc --create-admin --username $adminUser --password $adminPass

    Write-Host ""
    Write-Host "=== Claworc deployed to Kubernetes ==="
    Write-Host ""
    if ($serviceType -eq "NodePort") {
        Write-Host "  Dashboard:  http://<node-ip>:$nodePort"
    }
    else {
        Write-Host "  To access the dashboard, run:"
        Write-Host "    kubectl port-forward svc/claworc 8000:8001 -n $namespace"
        Write-Host ""
        Write-Host "  Dashboard:  http://localhost:8000"
    }
    Write-Host ""
    Write-Host "Commands:"
    Write-Host "  kubectl get pods -n $namespace                     # Check pods"
    Write-Host "  kubectl logs -f deploy/claworc -n $namespace       # View logs"
    Write-Host "  helm upgrade claworc $chartPath -n $namespace      # Upgrade"
    Write-Host "  helm uninstall claworc -n $namespace               # Remove"

    # Clean up temp directory if we cloned
    if ($tmpDir -and (Test-Path $tmpDir)) {
        Remove-Item -Recurse -Force $tmpDir
    }
}

# --- Dispatch ----------------------------------------------------------------

switch ($mode) {
    "1" { Install-Docker }
    "2" { Install-Kubernetes }
    default {
        Write-Host "Invalid selection: $mode"
        exit 1
    }
}
