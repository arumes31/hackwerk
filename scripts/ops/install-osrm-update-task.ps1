[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$DataDir,
    [string]$TaskName = "HackWerk OSRM Update"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ([WildcardPattern]::ContainsWildcardCharacters($DataDir) -or -not [IO.Path]::IsPathFullyQualified($DataDir)) {
    throw "OSRM data directory must be an absolute path without wildcard characters."
}

$updateScript = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "update-osrm-host.ps1"))
$arguments = @(
    "-NoProfile",
    "-NonInteractive",
    "-File", ('"{0}"' -f $updateScript),
    "-DataDir", ('"{0}"' -f [IO.Path]::GetFullPath($DataDir))
) -join " "

$powerShell = (Get-Command pwsh.exe -ErrorAction Stop).Source
$action = New-ScheduledTaskAction -Execute $powerShell -Argument $arguments
$trigger = New-ScheduledTaskTrigger -Weekly -DaysOfWeek Sunday -At 2:30am
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew `
    -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 15)
$principal = New-ScheduledTaskPrincipal -UserId ([Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
