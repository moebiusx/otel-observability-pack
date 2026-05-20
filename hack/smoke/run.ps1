# Drives an end-to-end smoke of the ObservabilityPack meta-operator
# against a kind cluster. Idempotent: re-running uses the existing
# cluster unless -Recreate is passed.
#
# Steps:
#   1. Ensure kind cluster exists.
#   2. Build operator image, load into the cluster.
#   3. Install our Pack CRD + RBAC + Deployment.
#   4. Install minimal stand-in CRDs for the third-party tool kinds.
#   5. Generate a Pack CR from examples/payment-service.pack.yaml.
#   6. Apply the Pack and wait for status.phase == Ready.
#   7. Confirm child PrometheusRule / GrafanaDashboard /
#      OpenTelemetryCollector objects exist.
#
# Usage:
#   pwsh hack/smoke/run.ps1
#   pwsh hack/smoke/run.ps1 -Recreate     # tear down + rebuild cluster
#   pwsh hack/smoke/run.ps1 -KeepCluster  # leave cluster running on failure
#   pwsh hack/smoke/run.ps1 -Cleanup      # delete the cluster and exit
[CmdletBinding()]
param(
    [string]$ClusterName = 'obs-pack-smoke',
    [string]$Image = 'ghcr.io/moebiusx/observability-pack-operator:dev',
    [switch]$Recreate,
    [switch]$KeepCluster,
    [switch]$Cleanup
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$kind     = Join-Path $env:USERPROFILE 'go\bin\kind.exe'
if (-not (Test-Path $kind)) {
    throw "kind not found at $kind. Install with: go install sigs.k8s.io/kind@v0.24.0"
}

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Sub($msg)  { Write-Host "    $msg" -ForegroundColor DarkGray }

Push-Location $repoRoot
try {
    if ($Cleanup) {
        Step "Deleting kind cluster '$ClusterName'"
        & $kind delete cluster --name $ClusterName
        return
    }

    if ($Recreate) {
        & $kind delete cluster --name $ClusterName 2>$null
    }

    $existing = & $kind get clusters 2>$null
    if (-not ($existing -contains $ClusterName)) {
        Step "Creating kind cluster '$ClusterName'"
        & $kind create cluster --name $ClusterName --wait 90s
    } else {
        Step "Using existing kind cluster '$ClusterName'"
    }

    $kctx = "kind-$ClusterName"

    Step "Building operator image $Image"
    docker build -t $Image .
    if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

    Step "Loading image into kind"
    & $kind load docker-image $Image --name $ClusterName
    if ($LASTEXITCODE -ne 0) { throw "kind load failed" }

    Step "Applying tool stand-in CRDs"
    kubectl --context $kctx apply -f hack/smoke/crds-tools.yaml | Out-Null

    Step "Applying Pack CRD"
    kubectl --context $kctx apply -f config/crd/bases/observability.platform_packs.yaml | Out-Null

    Step "Applying RBAC + Deployment"
    kubectl --context $kctx apply -f config/rbac/role.yaml | Out-Null
    kubectl --context $kctx apply -f config/manager/manager.yaml | Out-Null

    # If the deployment was already running with the previous image
    # build, force a roll so the new bits land.
    kubectl --context $kctx -n observability-pack-system rollout restart deploy/observability-pack-operator | Out-Null
    Sub "Waiting for operator deployment to be Ready"
    kubectl --context $kctx -n observability-pack-system rollout status deploy/observability-pack-operator --timeout=180s

    Step "Creating namespace 'obs'"
    kubectl --context $kctx create namespace obs --dry-run=client -o yaml | kubectl --context $kctx apply -f - | Out-Null

    Step "Generating Pack CR from examples/payment-service.pack.yaml"
    $packCR = Join-Path $env:TEMP 'pack-cr.yaml'
    go run ./hack/smoke/wrap.go -f examples/payment-service.pack.yaml -name payments -namespace obs -target ske > $packCR
    if ($LASTEXITCODE -ne 0) { throw "wrap.go failed" }
    Sub "Wrote $packCR"

    Step "Applying Pack CR"
    kubectl --context $kctx apply -f $packCR | Out-Null

    Step "Waiting for Pack status.phase=Ready (up to 60s)"
    $deadline = (Get-Date).AddSeconds(60)
    $phase = ''
    while ((Get-Date) -lt $deadline) {
        $phase = kubectl --context $kctx -n obs get pack payments -o jsonpath='{.status.phase}' 2>$null
        if ($phase -eq 'Ready') { break }
        Start-Sleep -Seconds 2
    }
    if ($phase -ne 'Ready') {
        Write-Host ''
        Write-Host '--- Pack status ---' -ForegroundColor Yellow
        kubectl --context $kctx -n obs get pack payments -o yaml
        Write-Host '--- Operator logs (last 100 lines) ---' -ForegroundColor Yellow
        kubectl --context $kctx -n observability-pack-system logs deploy/observability-pack-operator --tail=100
        throw "Pack did not reach Ready (last phase: '$phase')"
    }
    Sub "Phase=Ready"

    Step "Verifying child objects"
    $kinds = @(
        @{ Resource = 'prometheusrules.monitoring.coreos.com';        Label = 'PrometheusRule' },
        @{ Resource = 'grafanadashboards.grafana.integreatly.org';    Label = 'GrafanaDashboard' },
        @{ Resource = 'opentelemetrycollectors.opentelemetry.io';     Label = 'OpenTelemetryCollector' }
    )
    foreach ($k in $kinds) {
        $items = kubectl --context $kctx -n obs get $k.Resource -o name 2>$null
        if (-not $items) { throw "no $($k.Label) objects found" }
        $count = ($items | Measure-Object).Count
        Sub "$($k.Label): $count object(s)"
    }

    Step 'Smoke OK'
}
catch {
    Write-Host "Smoke FAILED: $_" -ForegroundColor Red
    if (-not $KeepCluster) {
        Sub 'Pass -KeepCluster to leave the cluster up for inspection.'
    }
    throw
}
finally {
    Pop-Location
}
