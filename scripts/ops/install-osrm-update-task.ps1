[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$DataDir,
    [string]$TaskName = "HackWerk OSRM Update",
    [string]$BindAddress = "127.0.0.1"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

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

$updateScript = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "update-osrm-host.ps1"))
$arguments = @(
    "-NoProfile",
    "-NonInteractive",
    "-File", ('"{0}"' -f $updateScript),
    "-DataDir", ('"{0}"' -f [IO.Path]::GetFullPath($DataDir)),
    "-BindAddress", $parsedBindAddress.ToString()
) -join " "

$powerShell = (Get-Command pwsh.exe -ErrorAction Stop).Source
$action = New-ScheduledTaskAction -Execute $powerShell -Argument $arguments
$trigger = New-ScheduledTaskTrigger -Weekly -DaysOfWeek Sunday -At 2:30am
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew `
    -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 15)
$principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
