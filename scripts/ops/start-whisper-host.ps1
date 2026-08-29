[CmdletBinding()]
param(
    [string]$ComposeFile = "",
    [string]$Image = "hackwerk-whisper:small",
    [Parameter(Mandatory = $true)]
    [string]$BindAddress,
    [ValidateRange(1, 16)]
    [int]$Threads = 2
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$parsedBindAddress = $null
if (-not [Net.IPAddress]::TryParse($BindAddress, [ref]$parsedBindAddress)) {
    throw "Whisper bind address must be a numeric IPv4 address."
}
$bytes = $parsedBindAddress.GetAddressBytes()
$isIPv4 = $parsedBindAddress.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork
$isTailscaleIPv4 = $isIPv4 -and $bytes[0] -eq 100 -and $bytes[1] -ge 64 -and $bytes[1] -le 127
if (-not $isTailscaleIPv4) {
    throw "Whisper bind address must be an IPv4 address in 100.64.0.0/10."
}

if (-not $ComposeFile) {
    $ComposeFile = Join-Path $PSScriptRoot "..\..\compose.voice-host.example.yaml"
}
$ComposeFile = [IO.Path]::GetFullPath($ComposeFile)
if (-not (Test-Path -LiteralPath $ComposeFile -PathType Leaf)) {
    throw "Voice-host Compose file is missing."
}

$env:WHISPER_IMAGE = $Image
$env:WHISPER_BIND_ADDRESS = $parsedBindAddress.ToString()
$env:WHISPER_THREADS = [string]$Threads

& docker info --format '{{.ServerVersion}}' *> $null
if ($LASTEXITCODE -ne 0) {
    throw "Docker Engine is unavailable."
}
& docker image inspect $Image *> $null
if ($LASTEXITCODE -ne 0) {
    throw "Whisper image is not loaded on this host."
}
& docker compose -f $ComposeFile config --quiet
if ($LASTEXITCODE -ne 0) {
    throw "Voice-host Compose configuration is invalid."
}
& docker compose -f $ComposeFile up -d --force-recreate whisper
if ($LASTEXITCODE -ne 0) {
    throw "Unable to start the Whisper host container."
}

for ($attempt = 0; $attempt -lt 90; $attempt++) {
    $containerID = (& docker compose -f $ComposeFile ps -q whisper).Trim()
    if ($containerID) {
        $health = (& docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $containerID).Trim()
        if ($health -eq "healthy") {
            exit 0
        }
        if ($health -in @("unhealthy", "exited", "dead")) {
            throw "Whisper host container became unhealthy."
        }
    }
    Start-Sleep -Seconds 2
}
throw "Whisper host container did not become healthy in time."
