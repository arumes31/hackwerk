[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$DataDir,
    [string]$ComposeFile = "",
    [string]$Image = "hackwerk-osrm-tools:v26.7.3-2",
    [string]$BindAddress = "127.0.0.1"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

if ([WildcardPattern]::ContainsWildcardCharacters($DataDir) -or -not [IO.Path]::IsPathFullyQualified($DataDir)) {
    throw "OSRM data directory must be an absolute path without wildcard characters."
}

$parsedBindAddress = $null
if (-not [Net.IPAddress]::TryParse($BindAddress, [ref]$parsedBindAddress)) {
    throw "OSRM bind address must be a numeric IPv4 address."
}
$bindBytes = $parsedBindAddress.GetAddressBytes()
$isIPv4 = $parsedBindAddress.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork
$isLoopbackIPv4 = $isIPv4 -and $bindBytes[0] -eq 127
$isTailscaleIPv4 = $isIPv4 -and $bindBytes[0] -eq 100 -and $bindBytes[1] -ge 64 -and $bindBytes[1] -le 127
if (-not $isLoopbackIPv4 -and -not $isTailscaleIPv4) {
    throw "OSRM bind address must be IPv4 loopback or an IPv4 address in 100.64.0.0/10."
}

if (-not $ComposeFile) {
    $ComposeFile = Join-Path $PSScriptRoot "..\..\compose.routing-host.example.yaml"
}
$ComposeFile = [IO.Path]::GetFullPath($ComposeFile)
$DataDir = [IO.Path]::GetFullPath($DataDir)

$dataRoot = [IO.Path]::GetPathRoot($DataDir)
$dataDrive = [IO.DriveInfo]::new($dataRoot)
if ($DataDir.StartsWith("\\", [StringComparison]::Ordinal) -or $dataDrive.DriveType -ne [IO.DriveType]::Fixed -or
    $DataDir -match '(?i)[\\/](OneDrive|Nextcloud)[\\/]') {
    throw "OSRM data directory must be on a fixed, non-synchronized local drive."
}

if (-not (Test-Path -LiteralPath $ComposeFile -PathType Leaf)) {
    throw "Routing-host Compose file is missing."
}
if (-not (Test-Path -LiteralPath $DataDir)) {
    New-Item -ItemType Directory -Path $DataDir | Out-Null
}
$dataItem = Get-Item -LiteralPath $DataDir -Force
if (-not $dataItem.PSIsContainer -or ($dataItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw "OSRM data directory must be a real local directory."
}
$ancestor = $dataItem.Parent
while ($null -ne $ancestor) {
    if ($ancestor.Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw "OSRM data directory must not be below a reparse point."
    }
    $ancestor = $ancestor.Parent
}

$env:OSRM_DATA_DIR = $DataDir
$env:OSRM_IMAGE = $Image
$env:OSRM_BIND_ADDRESS = $parsedBindAddress.ToString()

function Invoke-Compose {
    param([Parameter(Mandatory = $true)][string[]]$ComposeArgs)
    & docker compose -f $ComposeFile @ComposeArgs
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed with exit code $LASTEXITCODE."
    }
}

function Invoke-UpdateMode {
    param([Parameter(Mandatory = $true)][ValidateSet("update", "rollback", "prune")][string]$Mode)
    Invoke-Compose -ComposeArgs @(
        "--profile", "osrm-ops", "run", "--rm", "--entrypoint", "ionice", "osrm-update",
        "-c", "3", "nice", "-n", "15", "/usr/local/bin/hackwerk-osrm-update", $Mode
    )
}

function Wait-ServiceHealthy {
    param([Parameter(Mandatory = $true)][string]$Service)
    for ($attempt = 0; $attempt -lt 100; $attempt++) {
        $containerID = (& docker compose -f $ComposeFile --profile routing ps -q $Service).Trim()
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to inspect OSRM container."
        }
        if ($containerID) {
            $health = (& docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $containerID).Trim()
            if ($health -eq "healthy") {
                return $true
            }
            if ($health -in @("unhealthy", "exited", "dead")) {
                return $false
            }
        }
        Start-Sleep -Seconds 2
    }
    return $false
}

function Wait-DockerEngine {
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        & docker info --format '{{.ServerVersion}}' *> $null
        if ($LASTEXITCODE -eq 0) {
            return
        }
        Start-Sleep -Seconds 10
    }
    throw "Docker Desktop did not become ready within 10 minutes."
}

Wait-DockerEngine
& docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) {
    throw "OSRM image is not loaded on this host: $Image"
}

$sentinel = ".hackwerk-write-test-$PID"
Invoke-Compose -ComposeArgs @(
    "--profile", "osrm-ops", "run", "--rm", "--entrypoint", "sh", "osrm-update",
    "-c", "touch /data/$sentinel && rm /data/$sentinel"
)

Invoke-UpdateMode -Mode update
$newGenerationHealthy = $false
try {
    Invoke-Compose -ComposeArgs @("--profile", "routing", "up", "-d", "--force-recreate", "osrm", "osrm-loopback")
    $newGenerationHealthy = (Wait-ServiceHealthy -Service "osrm") -and (Wait-ServiceHealthy -Service "osrm-loopback")
} catch {
    Write-Warning $_.Exception.Message
}
if ($newGenerationHealthy) {
    Invoke-UpdateMode -Mode prune
    exit 0
}

Write-Warning "New OSRM generation is unhealthy; restoring previous generation."
Invoke-UpdateMode -Mode rollback
Invoke-Compose -ComposeArgs @("--profile", "routing", "up", "-d", "--force-recreate", "osrm", "osrm-loopback")
if (-not ((Wait-ServiceHealthy -Service "osrm") -and (Wait-ServiceHealthy -Service "osrm-loopback"))) {
    throw "Previous OSRM generation also failed health validation."
}
exit 1
