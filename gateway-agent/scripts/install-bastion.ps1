#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Configures a Windows Server as an RDP Bastion Gateway.
.DESCRIPTION
    Run this script once on a fresh Windows Server 2022 installation.
    It installs all dependencies and configures the server for use as
    a session-recorded RDP bastion.

    Use -Uninstall to reverse the installation and clean up all components
    (except the recordings directory).

    Use -WhatIf to preview changes without applying them.
.PARAMETER InstallDir
    Root directory for gateway files. Default: C:\Gateway
.PARAMETER RecordingsDir
    Directory for session recordings. Default: D:\recordings
.PARAMETER FFmpegVersion
    FFmpeg version to install. Default: 7.1
.PARAMETER AgentPort
    TCP port for the Gateway Agent HTTP API. Default: 8080
.PARAMETER MaxSessions
    Maximum concurrent RDP sessions. Default: 20
.PARAMETER SessionUserPrefix
    Prefix for session user accounts. Default: gwsession
.PARAMETER SessionUserCount
    Number of session user accounts to create. Default: 20
.PARAMETER Uninstall
    When set, removes the gateway installation (service, users, firewall rules,
    install directory). Does NOT remove the recordings directory.
.NOTES
    Requires: Windows Server 2022, Administrator privileges, internet access
.EXAMPLE
    .\install-bastion.ps1
    Full installation with defaults.
.EXAMPLE
    .\install-bastion.ps1 -WhatIf
    Preview installation without making changes.
.EXAMPLE
    .\install-bastion.ps1 -Uninstall
    Remove the gateway installation.
.EXAMPLE
    .\install-bastion.ps1 -Uninstall -WhatIf
    Preview uninstallation without making changes.
#>

[CmdletBinding(SupportsShouldProcess)]
param(
    [string]$InstallDir = "C:\Gateway",
    [string]$RecordingsDir = "C:\Gateway\recordings",
    [string]$FFmpegVersion = "7.1",
    [int]$AgentPort = 8080,
    [int]$MaxSessions = 20,
    [string]$SessionUserPrefix = "gwsession",
    [int]$SessionUserCount = 20,
    [string]$GatewayHostname = "",
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# ==================================================================
# Helper: Build the list of session usernames
# ==================================================================
function Get-SessionUsernames {
    param(
        [string]$Prefix,
        [int]$Count
    )
    $names = @()
    for ($i = 1; $i -le $Count; $i++) {
        $names += "{0}{1:D3}" -f $Prefix, $i
    }
    return $names
}

# ==================================================================
# Validation: Test-Installation
# ==================================================================
function Test-Installation {
    <#
    .SYNOPSIS
        Validates that all bastion components are correctly installed.
    #>
    param(
        [string]$InstallDir,
        [string]$SessionUserPrefix,
        [int]$SessionUserCount,
        [int]$AgentPort
    )

    Write-Host ""
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host "  Post-Install Validation" -ForegroundColor Cyan
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host ""

    $allPassed = $true
    $checks = @()

    # --- Check 1: RDS Session Host role ---
    $checkName = "RDS Session Host role installed"
    try {
        $rdsFeature = Get-WindowsFeature -Name "RDS-RD-Server" -ErrorAction Stop
        if ($rdsFeature.Installed) {
            $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Installed" }
        } else {
            $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Not installed" }
            $allPassed = $false
        }
    } catch {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = $_.Exception.Message }
        $allPassed = $false
    }

    # --- Check 1b: RD Gateway role installed ---
    $checkName = "RD Gateway role installed"
    try {
        $gwFeature = Get-WindowsFeature -Name "RDS-Gateway" -ErrorAction Stop
        if ($gwFeature.Installed) {
            $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Installed" }
        } else {
            $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Not installed" }
            $allPassed = $false
        }
    } catch {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = $_.Exception.Message }
        $allPassed = $false
    }

    # --- Check 1c: TSGateway service running ---
    $checkName = "TSGateway service running"
    $tsgSvc = Get-Service -Name "TSGateway" -ErrorAction SilentlyContinue
    if ($tsgSvc -and $tsgSvc.Status -eq "Running") {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Running" }
    } else {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Not running" }
        $allPassed = $false
    }

    # --- Check 1d: RD Gateway SSL certificate ---
    $checkName = "RD Gateway SSL certificate"
    $gwCert = Get-ChildItem Cert:\LocalMachine\My |
        Where-Object { $_.FriendlyName -like "RD Gateway*" -and $_.NotAfter -gt (Get-Date) } |
        Select-Object -First 1
    if ($gwCert) {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Thumbprint: $($gwCert.Thumbprint.Substring(0,8))..., expires $($gwCert.NotAfter.ToString('yyyy-MM-dd'))" }
    } else {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "No valid certificate found" }
        $allPassed = $false
    }

    # --- Check 2: ffmpeg in PATH and functional ---
    $checkName = "ffmpeg is functional"
    try {
        $ffmpegOut = & ffmpeg -version 2>&1 | Select-Object -First 1
        if ($ffmpegOut -match "ffmpeg version") {
            $checks += @{ Name = $checkName; Status = "PASS"; Detail = $ffmpegOut }
        } else {
            $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "ffmpeg returned unexpected output" }
            $allPassed = $false
        }
    } catch {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "ffmpeg not found in PATH" }
        $allPassed = $false
    }

    # --- Check 3: Session users exist ---
    $checkName = "Session user accounts"
    $usernames = Get-SessionUsernames -Prefix $SessionUserPrefix -Count $SessionUserCount
    $missingUsers = @()
    foreach ($username in $usernames) {
        $user = Get-LocalUser -Name $username -ErrorAction SilentlyContinue
        if (-not $user) {
            $missingUsers += $username
        }
    }
    if ($missingUsers.Count -eq 0) {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "$SessionUserCount users verified" }
    } else {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Missing: $($missingUsers -join ', ')" }
        $allPassed = $false
    }

    # --- Check 4: GatewayAgent service registered ---
    $checkName = "GatewayAgent service registered"
    $svc = Get-Service -Name "GatewayAgent" -ErrorAction SilentlyContinue
    if ($svc) {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Status: $($svc.Status)" }
    } else {
        # Service registration depends on the binary being present; warn rather than fail
        $agentExe = "$InstallDir\bin\gateway-agent.exe"
        if (Test-Path $agentExe) {
            $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Binary exists but service not registered" }
            $allPassed = $false
        } else {
            $checks += @{ Name = $checkName; Status = "WARN"; Detail = "Binary not yet deployed; service will be registered later" }
        }
    }

    # --- Check 5: Config files exist ---
    $checkName = "Configuration files"
    $requiredConfigs = @(
        "$InstallDir\config\agent.json",
        "$InstallDir\config\user-pool.json",
        "$InstallDir\config\credentials.json"
    )
    $missingConfigs = @()
    foreach ($cfg in $requiredConfigs) {
        if (-not (Test-Path $cfg)) {
            $missingConfigs += (Split-Path $cfg -Leaf)
        }
    }
    if ($missingConfigs.Count -eq 0) {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "All config files present" }
    } else {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Missing: $($missingConfigs -join ', ')" }
        $allPassed = $false
    }

    # --- Check 6: Firewall rules ---
    $checkName = "Firewall rules"
    $missingRules = @()
    foreach ($ruleName in @("Gateway-RDP", "Gateway-API", "Gateway-HTTPS")) {
        $rule = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
        if (-not $rule) {
            $missingRules += $ruleName
        }
    }
    if ($missingRules.Count -eq 0) {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Gateway-RDP, Gateway-API, and Gateway-HTTPS rules present" }
    } else {
        $checks += @{ Name = $checkName; Status = "FAIL"; Detail = "Missing: $($missingRules -join ', ')" }
        $allPassed = $false
    }

    # --- Print results ---
    foreach ($check in $checks) {
        $color = switch ($check.Status) {
            "PASS" { "Green" }
            "WARN" { "Yellow" }
            "FAIL" { "Red" }
        }
        $symbol = switch ($check.Status) {
            "PASS" { "[PASS]" }
            "WARN" { "[WARN]" }
            "FAIL" { "[FAIL]" }
        }
        Write-Host "  $symbol $($check.Name)" -ForegroundColor $color
        Write-Host "         $($check.Detail)" -ForegroundColor Gray
    }

    Write-Host ""
    if ($allPassed) {
        Write-Host "  All validation checks passed." -ForegroundColor Green
    } else {
        Write-Host "  Some checks failed. Review the output above." -ForegroundColor Red
    }

    return $allPassed
}

# ==================================================================
# UNINSTALL PATH
# ==================================================================
if ($Uninstall) {
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host "  RDP Bastion Gateway Uninstaller" -ForegroundColor Cyan
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host ""

    $removed = @()
    $skipped = @()

    # --- Stop and remove GatewayAgent service ---
    Write-Host "[1/7] Removing GatewayAgent service..." -ForegroundColor Yellow
    $svc = Get-Service -Name "GatewayAgent" -ErrorAction SilentlyContinue
    if ($svc) {
        if ($PSCmdlet.ShouldProcess("GatewayAgent service", "Stop and remove")) {
            if ($svc.Status -eq "Running") {
                Stop-Service -Name "GatewayAgent" -Force -ErrorAction SilentlyContinue
                Write-Host "  Stopped service" -ForegroundColor Green
            }
            # Use sc.exe to delete the service; Remove-Service requires PS 6+
            & sc.exe delete "GatewayAgent" | Out-Null
            Write-Host "  Removed service: GatewayAgent" -ForegroundColor Green
            $removed += "GatewayAgent service"
        }
    } else {
        Write-Host "  Service not found, skipping" -ForegroundColor Gray
        $skipped += "GatewayAgent service (not found)"
    }

    # --- Remove session user accounts ---
    Write-Host "[2/7] Removing session user accounts..." -ForegroundColor Yellow
    $usernames = Get-SessionUsernames -Prefix $SessionUserPrefix -Count $SessionUserCount
    $usersRemoved = 0
    foreach ($username in $usernames) {
        $user = Get-LocalUser -Name $username -ErrorAction SilentlyContinue
        if ($user) {
            if ($PSCmdlet.ShouldProcess($username, "Remove local user account")) {
                # Log off any active sessions for this user before removal
                try {
                    $sessions = query user $username 2>$null
                    if ($sessions) {
                        $sessionId = ($sessions | Select-Object -Skip 1 | ForEach-Object { ($_ -split '\s+')[3] }) | Select-Object -First 1
                        if ($sessionId) {
                            logoff $sessionId /server:localhost 2>$null
                        }
                    }
                } catch {
                    # Ignore errors from query/logoff -- user may not be logged in
                }
                Remove-LocalUser -Name $username -ErrorAction SilentlyContinue
                $usersRemoved++
            }
        }
    }
    if ($usersRemoved -gt 0) {
        Write-Host "  Removed $usersRemoved session user accounts" -ForegroundColor Green
        $removed += "$usersRemoved session user accounts"
    } else {
        Write-Host "  No session users found, skipping" -ForegroundColor Gray
        $skipped += "Session user accounts (none found)"
    }

    # --- Remove RD Gateway configuration ---
    Write-Host "[3/7] Removing RD Gateway configuration..." -ForegroundColor Yellow
    try {
        Import-Module RemoteDesktopServices -ErrorAction SilentlyContinue
        if (Test-Path "RDS:\GatewayServer\CAP\Gateway-CAP") {
            Remove-Item "RDS:\GatewayServer\CAP\Gateway-CAP" -Force -ErrorAction SilentlyContinue
            Write-Host "  Removed CAP: Gateway-CAP" -ForegroundColor Green
        }
        if (Test-Path "RDS:\GatewayServer\RAP\Gateway-RAP") {
            Remove-Item "RDS:\GatewayServer\RAP\Gateway-RAP" -Force -ErrorAction SilentlyContinue
            Write-Host "  Removed RAP: Gateway-RAP" -ForegroundColor Green
        }
        Stop-Service -Name TSGateway -Force -ErrorAction SilentlyContinue
        Set-Service -Name TSGateway -StartupType Disabled -ErrorAction SilentlyContinue
        Write-Host "  TSGateway service stopped and disabled" -ForegroundColor Green
        $removed += "RD Gateway policies and service"
    } catch {
        Write-Host "  Could not remove RD Gateway config: $($_.Exception.Message)" -ForegroundColor Yellow
        $skipped += "RD Gateway config (error)"
    }

    # Remove RD Gateway SSL certificates
    $gwCerts = Get-ChildItem Cert:\LocalMachine\My | Where-Object { $_.FriendlyName -like "RD Gateway*" }
    foreach ($cert in $gwCerts) {
        if ($PSCmdlet.ShouldProcess("SSL cert $($cert.Thumbprint)", "Remove")) {
            Remove-Item $cert.PSPath -Force -ErrorAction SilentlyContinue
            Write-Host "  Removed SSL certificate: $($cert.Thumbprint)" -ForegroundColor Green
        }
    }
    if ($gwCerts.Count -gt 0) { $removed += "RD Gateway SSL certificate(s)" }

    # --- Remove firewall rules ---
    Write-Host "[4/7] Removing firewall rules..." -ForegroundColor Yellow
    foreach ($ruleName in @("Gateway-RDP", "Gateway-API", "Gateway-HTTPS")) {
        $rule = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
        if ($rule) {
            if ($PSCmdlet.ShouldProcess($ruleName, "Remove firewall rule")) {
                Remove-NetFirewallRule -DisplayName $ruleName
                Write-Host "  Removed firewall rule: $ruleName" -ForegroundColor Green
                $removed += "Firewall rule: $ruleName"
            }
        } else {
            Write-Host "  Rule not found: $ruleName, skipping" -ForegroundColor Gray
            $skipped += "Firewall rule: $ruleName (not found)"
        }
    }

    # --- Remove install directory (NOT recordings) ---
    Write-Host "[5/7] Removing install directory..." -ForegroundColor Yellow
    if (Test-Path $InstallDir) {
        if ($PSCmdlet.ShouldProcess($InstallDir, "Remove directory")) {
            # Remove ffmpeg from system PATH before deleting the directory
            $ffmpegDir = "$InstallDir\bin"
            $currentPath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
            if ($currentPath -like "*$ffmpegDir*") {
                $newPath = ($currentPath -split ";" | Where-Object { $_ -ne $ffmpegDir }) -join ";"
                [Environment]::SetEnvironmentVariable("PATH", $newPath, "Machine")
                Write-Host "  Removed $ffmpegDir from system PATH" -ForegroundColor Green
            }

            Remove-Item -Path $InstallDir -Recurse -Force
            Write-Host "  Removed directory: $InstallDir" -ForegroundColor Green
            $removed += "Directory: $InstallDir"
        }
    } else {
        Write-Host "  Directory not found: $InstallDir, skipping" -ForegroundColor Gray
        $skipped += "Directory: $InstallDir (not found)"
    }
    Write-Host "  NOTE: Recordings directory preserved: $RecordingsDir" -ForegroundColor Yellow

    # --- Remove desktop lockdown policies ---
    Write-Host "[6/7] Removing desktop lockdown policies..." -ForegroundColor Yellow
    if ($PSCmdlet.ShouldProcess("Desktop lockdown policies", "Remove")) {
        $polRemoved = 0
        # Task Manager
        $sysPolPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System"
        if (Get-ItemProperty -Path $sysPolPath -Name "DisableTaskMgr" -ErrorAction SilentlyContinue) {
            Remove-ItemProperty -Path $sysPolPath -Name "DisableTaskMgr" -Force -ErrorAction SilentlyContinue
            $polRemoved++
        }
        # Explorer restrictions
        $explorerPolPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\Explorer"
        foreach ($prop in @("NoDrives", "NoRun", "NoDesktop", "NoFind")) {
            if (Get-ItemProperty -Path $explorerPolPath -Name $prop -ErrorAction SilentlyContinue) {
                Remove-ItemProperty -Path $explorerPolPath -Name $prop -Force -ErrorAction SilentlyContinue
                $polRemoved++
            }
        }
        # Command Prompt
        $winSysPolPath = "HKLM:\SOFTWARE\Policies\Microsoft\Windows\System"
        if (Get-ItemProperty -Path $winSysPolPath -Name "DisableCMD" -ErrorAction SilentlyContinue) {
            Remove-ItemProperty -Path $winSysPolPath -Name "DisableCMD" -Force -ErrorAction SilentlyContinue
            $polRemoved++
        }
        if ($polRemoved -gt 0) {
            Write-Host "  Removed $polRemoved desktop lockdown policies" -ForegroundColor Green
            $removed += "Desktop lockdown policies ($polRemoved settings)"
        } else {
            Write-Host "  No lockdown policies found, skipping" -ForegroundColor Gray
            $skipped += "Desktop lockdown policies (none found)"
        }
    }

    # --- Remove HKLM Run key ---
    $runKeyPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run"
    Remove-ItemProperty -Path $runKeyPath -Name "GatewayLauncher" -Force -ErrorAction SilentlyContinue
    Write-Host "  Removed HKLM Run key (GatewayLauncher)" -ForegroundColor Green

    # --- Remove RemoteApp allowlist (legacy) ---
    $tsAppPath = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Terminal Server\TSAppAllowList"
    Remove-ItemProperty -Path $tsAppPath -Name "fDisabledAllowList" -Force -ErrorAction SilentlyContinue

    # --- Revert NLA setting ---
    Write-Host "[7/7] Reverting RDS NLA setting..." -ForegroundColor Yellow
    $nlaPath = "HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp"
    try {
        $currentNLA = Get-ItemProperty -Path $nlaPath -Name "UserAuthentication" -ErrorAction SilentlyContinue
        if ($currentNLA -and $currentNLA.UserAuthentication -eq 0) {
            if ($PSCmdlet.ShouldProcess("NLA (UserAuthentication)", "Re-enable")) {
                Set-ItemProperty -Path $nlaPath -Name "UserAuthentication" -Value 1
                Write-Host "  Re-enabled NLA (UserAuthentication = 1)" -ForegroundColor Green
                $removed += "NLA disabled setting (reverted to enabled)"
            }
        } else {
            Write-Host "  NLA already enabled, skipping" -ForegroundColor Gray
            $skipped += "NLA setting (already enabled)"
        }
    } catch {
        Write-Host "  Could not read NLA setting: $($_.Exception.Message)" -ForegroundColor Yellow
        $skipped += "NLA setting (could not read registry)"
    }

    # --- Uninstall summary ---
    Write-Host ""
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host "  Uninstall Summary" -ForegroundColor Cyan
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host ""

    if ($WhatIfPreference) {
        Write-Host "  [DRY RUN] No changes were made." -ForegroundColor Yellow
        Write-Host ""
    }

    if ($removed.Count -gt 0) {
        Write-Host "  Removed:" -ForegroundColor Green
        foreach ($item in $removed) {
            Write-Host "    - $item" -ForegroundColor White
        }
    }
    if ($skipped.Count -gt 0) {
        Write-Host "  Skipped:" -ForegroundColor Gray
        foreach ($item in $skipped) {
            Write-Host "    - $item" -ForegroundColor Gray
        }
    }
    Write-Host ""
    Write-Host "  Recordings preserved at: $RecordingsDir" -ForegroundColor Yellow
    Write-Host "  RDS Session Host and RD Gateway roles were NOT removed (manual action if desired)." -ForegroundColor Yellow
    Write-Host ""

    exit 0
}

# ==================================================================
# INSTALL PATH
# ==================================================================

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  RDP Bastion Gateway Installer" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# ------------------------------------------------------------------
# Step 1: Validate Prerequisites
# ------------------------------------------------------------------
Write-Host "[1/13] Validating prerequisites..." -ForegroundColor Yellow

$osInfo = Get-CimInstance Win32_OperatingSystem
if ($osInfo.Caption -notmatch "Server") {
    throw "This script requires Windows Server. Detected: $($osInfo.Caption)"
}
Write-Host "  OS: $($osInfo.Caption)" -ForegroundColor Green

# ------------------------------------------------------------------
# Step 2: Create Directory Structure
# ------------------------------------------------------------------
Write-Host "[2/13] Creating directory structure..." -ForegroundColor Yellow

$dirs = @(
    $InstallDir,
    "$InstallDir\bin",
    "$InstallDir\config",
    "$InstallDir\scripts",
    "$InstallDir\logs",
    $RecordingsDir
)
foreach ($dir in $dirs) {
    if ($PSCmdlet.ShouldProcess($dir, "Create directory")) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        Write-Host "  Created: $dir" -ForegroundColor Green
    }
}

# ------------------------------------------------------------------
# Step 3: Install RDS Session Host Role
# ------------------------------------------------------------------
Write-Host "[3/13] Installing RDS Session Host role..." -ForegroundColor Yellow

$rdsFeature = Get-WindowsFeature -Name "RDS-RD-Server"
if (-not $rdsFeature.Installed) {
    if ($PSCmdlet.ShouldProcess("RDS-RD-Server", "Install Windows feature")) {
        Install-WindowsFeature -Name "RDS-RD-Server" -IncludeManagementTools
        Write-Host "  RDS Session Host installed (reboot may be required)" -ForegroundColor Green
        $needsReboot = $true
    }
} else {
    Write-Host "  RDS Session Host already installed" -ForegroundColor Green
    $needsReboot = $false
}
$needsTermServiceRestart = $false

# ------------------------------------------------------------------
# Step 4: Install RD Gateway Role
# ------------------------------------------------------------------
Write-Host "[4/13] Installing RD Gateway role..." -ForegroundColor Yellow

$gwFeature = Get-WindowsFeature -Name "RDS-Gateway"
if (-not $gwFeature.Installed) {
    if ($PSCmdlet.ShouldProcess("RDS-Gateway", "Install Windows feature")) {
        $result = Install-WindowsFeature -Name "RDS-Gateway" -IncludeManagementTools
        if ($result.RestartNeeded -eq "Yes") {
            $needsReboot = $true
        }
        Write-Host "  RD Gateway role installed" -ForegroundColor Green
    }
} else {
    Write-Host "  RD Gateway already installed" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 5: Create Self-Signed SSL Certificate for RD Gateway
# ------------------------------------------------------------------
Write-Host "[5/13] Creating SSL certificate for RD Gateway..." -ForegroundColor Yellow

# Resolve hostname for the certificate
if ([string]::IsNullOrWhiteSpace($GatewayHostname)) {
    try {
        $GatewayHostname = [System.Net.Dns]::GetHostEntry([System.Net.Dns]::GetHostName()).HostName
    } catch {
        $GatewayHostname = [System.Net.Dns]::GetHostName()
    }
}
Write-Host "  Gateway hostname: $GatewayHostname" -ForegroundColor Gray

# Check if a suitable certificate already exists
$existingCert = Get-ChildItem Cert:\LocalMachine\My |
    Where-Object { $_.Subject -eq "CN=$GatewayHostname" -and $_.NotAfter -gt (Get-Date).AddDays(30) } |
    Sort-Object NotAfter -Descending |
    Select-Object -First 1

if (-not $existingCert) {
    if ($PSCmdlet.ShouldProcess("Self-signed certificate for $GatewayHostname", "Create")) {
        $cert = New-SelfSignedCertificate `
            -DnsName $GatewayHostname `
            -CertStoreLocation Cert:\LocalMachine\My `
            -NotAfter (Get-Date).AddYears(5) `
            -KeyLength 2048 `
            -KeyExportPolicy Exportable `
            -FriendlyName "RD Gateway - $GatewayHostname"
        $certThumbprint = $cert.Thumbprint
        Write-Host "  Certificate created: $($certThumbprint.Substring(0,8))... (CN=$GatewayHostname, expires $($cert.NotAfter.ToString('yyyy-MM-dd')))" -ForegroundColor Green
    }
} else {
    $certThumbprint = $existingCert.Thumbprint
    Write-Host "  Using existing certificate: $($certThumbprint.Substring(0,8))... (expires $($existingCert.NotAfter.ToString('yyyy-MM-dd')))" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 6: Configure RD Gateway (CAP, RAP, SSL binding)
# ------------------------------------------------------------------
Write-Host "[6/13] Configuring RD Gateway..." -ForegroundColor Yellow

if ($PSCmdlet.ShouldProcess("RD Gateway", "Configure SSL binding and policies")) {
    Import-Module RemoteDesktopServices -ErrorAction Stop

    # Bind the SSL certificate to the RD Gateway
    if (Test-Path "RDS:\GatewayServer") {
        Set-Item -Path "RDS:\GatewayServer\SSLCertificate\Thumbprint" -Value $certThumbprint -Force
        Write-Host "  SSL certificate bound to RD Gateway" -ForegroundColor Green
    }

    # Connection Authorization Policy (who can authenticate)
    $capName = "Gateway-CAP"
    $existingCAP = Get-Item "RDS:\GatewayServer\CAP\$capName" -ErrorAction SilentlyContinue
    if (-not $existingCAP) {
        New-Item -Path "RDS:\GatewayServer\CAP" `
            -Name $capName `
            -UserGroups "Remote Desktop Users@BUILTIN" `
            -AuthMethod 1 | Out-Null
        Write-Host "  Created CAP: $capName (Remote Desktop Users, password auth)" -ForegroundColor Green
    } else {
        Write-Host "  CAP already exists: $capName" -ForegroundColor Green
    }

    # Resource Authorization Policy (what they can connect to)
    $rapName = "Gateway-RAP"
    $existingRAP = Get-Item "RDS:\GatewayServer\RAP\$rapName" -ErrorAction SilentlyContinue
    if (-not $existingRAP) {
        New-Item -Path "RDS:\GatewayServer\RAP" `
            -Name $rapName `
            -UserGroups "Remote Desktop Users@BUILTIN" `
            -ComputerGroupType 2 | Out-Null
        Write-Host "  Created RAP: $rapName (Remote Desktop Users, any resource)" -ForegroundColor Green
    } else {
        Write-Host "  RAP already exists: $rapName" -ForegroundColor Green
    }

    # Set max connections to match session count
    Set-Item -Path "RDS:\GatewayServer\MaxConnections" -Value $MaxSessions -Force
    Write-Host "  Max connections set to $MaxSessions" -ForegroundColor Green

    # Ensure the TSGateway service is set to auto-start
    Set-Service -Name TSGateway -StartupType Automatic -ErrorAction SilentlyContinue
    Start-Service -Name TSGateway -ErrorAction SilentlyContinue
    Write-Host "  TSGateway service started" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 7: Download and Install ffmpeg
# ------------------------------------------------------------------
Write-Host "[7/13] Installing ffmpeg..." -ForegroundColor Yellow

$ffmpegDir = "$InstallDir\bin"
$ffmpegExe = "$ffmpegDir\ffmpeg.exe"

if (-not (Test-Path $ffmpegExe)) {
    if ($PSCmdlet.ShouldProcess("ffmpeg $FFmpegVersion", "Download and install")) {
        $ffmpegUrl = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
        $ffmpegZip = "$env:TEMP\ffmpeg.zip"

        Write-Host "  Downloading ffmpeg..." -ForegroundColor Gray
        try {
            Invoke-WebRequest -Uri $ffmpegUrl -OutFile $ffmpegZip -UseBasicParsing
        } catch {
            throw "Failed to download ffmpeg from $ffmpegUrl : $($_.Exception.Message)"
        }

        Write-Host "  Extracting..." -ForegroundColor Gray
        $extractDir = "$env:TEMP\ffmpeg-extract"
        Expand-Archive -Path $ffmpegZip -DestinationPath $extractDir -Force

        # ffmpeg extracts into a versioned subdirectory
        $ffmpegBinDir = Get-ChildItem "$extractDir\ffmpeg-*\bin" -Directory | Select-Object -First 1
        if (-not $ffmpegBinDir) {
            throw "Could not locate ffmpeg bin directory after extraction"
        }
        Copy-Item "$($ffmpegBinDir.FullName)\ffmpeg.exe" $ffmpegExe
        Copy-Item "$($ffmpegBinDir.FullName)\ffprobe.exe" "$ffmpegDir\ffprobe.exe"

        # Clean up temp files
        Remove-Item $ffmpegZip -Force -ErrorAction SilentlyContinue
        Remove-Item $extractDir -Recurse -Force -ErrorAction SilentlyContinue

        Write-Host "  ffmpeg installed: $ffmpegExe" -ForegroundColor Green
    }
} else {
    Write-Host "  ffmpeg already installed" -ForegroundColor Green
}

# Add to system PATH
if ($PSCmdlet.ShouldProcess("System PATH", "Add $ffmpegDir")) {
    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
    if ($currentPath -notlike "*$ffmpegDir*") {
        [Environment]::SetEnvironmentVariable("PATH", "$currentPath;$ffmpegDir", "Machine")
        Write-Host "  Added ffmpeg to system PATH" -ForegroundColor Green
    }
    # Refresh PATH in current session so validation can find ffmpeg
    if ($env:Path -notlike "*$ffmpegDir*") {
        $env:Path = "$env:Path;$ffmpegDir"
    }
}

# ------------------------------------------------------------------
# Step 5: Create Session User Accounts
# ------------------------------------------------------------------
Write-Host "[8/13] Creating session user accounts..." -ForegroundColor Yellow

# Disable Windows password complexity so the Go agent can set numeric
# PINs as session passwords. These are ephemeral single-use credentials
# that get rotated after first connect, so complexity adds no value.
Write-Host "  Disabling password complexity policy for PIN-based auth..." -ForegroundColor Cyan
$secCfg = "$env:TEMP\secpol-export.cfg"
secedit /export /cfg $secCfg /quiet
(Get-Content $secCfg) `
    -replace 'PasswordComplexity\s*=\s*1', 'PasswordComplexity = 0' `
    -replace 'MinimumPasswordLength\s*=\s*\d+', 'MinimumPasswordLength = 4' |
    Set-Content $secCfg
secedit /configure /db "$env:TEMP\secpol-pin.sdb" /cfg $secCfg /areas SECURITYPOLICY /quiet
Remove-Item $secCfg -Force -ErrorAction SilentlyContinue
Remove-Item "$env:TEMP\secpol-pin.sdb" -Force -ErrorAction SilentlyContinue
Write-Host "  Password complexity disabled, minimum length set to 4" -ForegroundColor Green

# These are local accounts that RDS sessions run under.
# Each concurrent session uses a different account to maintain isolation.
for ($i = 1; $i -le $SessionUserCount; $i++) {
    $username = "{0}{1:D3}" -f $SessionUserPrefix, $i
    $userExists = Get-LocalUser -Name $username -ErrorAction SilentlyContinue

    if (-not $userExists) {
        if ($PSCmdlet.ShouldProcess($username, "Create local user account")) {
            # Generate a random password -- the Go agent manages these
            $password = -join ((65..90) + (97..122) + (48..57) | Get-Random -Count 24 | ForEach-Object { [char]$_ })
            $securePass = ConvertTo-SecureString $password -AsPlainText -Force

            New-LocalUser -Name $username `
                -Password $securePass `
                -FullName "Gateway Session $i" `
                -Description "RDP Bastion gateway session account" `
                -PasswordNeverExpires `
                -UserMayNotChangePassword | Out-Null

            # Add to Remote Desktop Users group
            Add-LocalGroupMember -Group "Remote Desktop Users" -Member $username -ErrorAction SilentlyContinue

            Write-Host "  Created user: $username" -ForegroundColor Green
        }
    } else {
        Write-Host "  User exists: $username" -ForegroundColor Gray
    }
}

# Write the user pool config for the Go agent
if ($PSCmdlet.ShouldProcess("$InstallDir\config\user-pool.json", "Write user pool config")) {
    $userPool = @()
    for ($i = 1; $i -le $SessionUserCount; $i++) {
        $userPool += "{0}{1:D3}" -f $SessionUserPrefix, $i
    }
    $userPoolConfig = @{
        users  = $userPool
        prefix = $SessionUserPrefix
        count  = $SessionUserCount
    } | ConvertTo-Json -Depth 3
    $userPoolConfig | Out-File -Encoding UTF8 "$InstallDir\config\user-pool.json"
}

# ------------------------------------------------------------------
# Step 5b: Grant session users access to Gateway directories
# ------------------------------------------------------------------
Write-Host "[8b/13] Setting directory permissions for session users..." -ForegroundColor Yellow

if ($PSCmdlet.ShouldProcess("$InstallDir", "Grant 'Remote Desktop Users' read/execute")) {
    $acl = Get-Acl $InstallDir
    $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
        "Remote Desktop Users",
        "ReadAndExecute",
        "ContainerInherit,ObjectInherit",
        "None",
        "Allow"
    )
    $acl.AddAccessRule($rule)
    Set-Acl -Path $InstallDir -AclObject $acl
    Write-Host "  Granted Remote Desktop Users read/execute on $InstallDir" -ForegroundColor Green
}

if ($PSCmdlet.ShouldProcess("$RecordingsDir", "Grant 'Remote Desktop Users' modify")) {
    $acl = Get-Acl $RecordingsDir
    $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
        "Remote Desktop Users",
        "Modify",
        "ContainerInherit,ObjectInherit",
        "None",
        "Allow"
    )
    $acl.AddAccessRule($rule)
    Set-Acl -Path $RecordingsDir -AclObject $acl
    Write-Host "  Granted Remote Desktop Users modify on $RecordingsDir" -ForegroundColor Green
}

# Session users need write access to the logs directory for session-launch.ps1 logging
$logsDir = "$InstallDir\logs"
if (-not (Test-Path $logsDir)) { New-Item -ItemType Directory -Force -Path $logsDir | Out-Null }
if ($PSCmdlet.ShouldProcess("$logsDir", "Grant 'Remote Desktop Users' modify")) {
    $acl = Get-Acl $logsDir
    $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
        "Remote Desktop Users",
        "Modify",
        "ContainerInherit,ObjectInherit",
        "None",
        "Allow"
    )
    $acl.AddAccessRule($rule)
    Set-Acl -Path $logsDir -AclObject $acl
    Write-Host "  Granted Remote Desktop Users modify on $logsDir" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 6: Configure RDS Policies
# ------------------------------------------------------------------
Write-Host "[9/13] Configuring RDS policies..." -ForegroundColor Yellow

$tsRegPath = "HKLM:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services"

# Ensure the Terminal Services policy key exists
if (-not (Test-Path $tsRegPath)) {
    if ($PSCmdlet.ShouldProcess($tsRegPath, "Create registry key")) {
        New-Item -Path $tsRegPath -Force | Out-Null
    }
}

# Disable NLA on the bastion so credential injection works smoothly
if ($PSCmdlet.ShouldProcess("NLA (UserAuthentication)", "Disable")) {
    Set-ItemProperty -Path "HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp" `
        -Name "UserAuthentication" -Value 0
    Write-Host "  Disabled NLA on bastion" -ForegroundColor Green
}

# Set session limits
if ($PSCmdlet.ShouldProcess("RDS session policies", "Configure timeouts and limits")) {
    # Max idle time: 30 minutes (in milliseconds)
    Set-ItemProperty -Path $tsRegPath -Name "MaxIdleTime" -Value 1800000
    Write-Host "  Set max idle time: 30 minutes" -ForegroundColor Green

    # Max session time: 8 hours
    Set-ItemProperty -Path $tsRegPath -Name "MaxConnectionTime" -Value 28800000
    Write-Host "  Set max session time: 8 hours" -ForegroundColor Green

    # With RemoteApp, session cleanup is handled by session-launch.ps1.
    # fResetBroken=1 ensures disconnected sessions are logged off automatically.
    Set-ItemProperty -Path $tsRegPath -Name "fResetBroken" -Value 1
    Write-Host "  Configured fResetBroken=1 (auto-reset disconnected sessions)" -ForegroundColor Green

    # Disable wallpaper in sessions to reduce recording size
    Set-ItemProperty -Path $tsRegPath -Name "fNoRemoteDesktopWallpaper" -Value 1 -Type DWord -Force
    Write-Host "  Disabled wallpaper in RDS sessions" -ForegroundColor Green

    # Enable UDP transport (reduces perceived input latency significantly)
    Set-ItemProperty -Path $tsRegPath -Name "SelectTransport" -Value 0 -Type DWord -Force
    Write-Host "  Enabled UDP transport (SelectTransport=0)" -ForegroundColor Green

    # Enable RemoteFX / GPU acceleration for RDS sessions
    Set-ItemProperty -Path $tsRegPath -Name "fEnableVirtualizedGraphics" -Value 1 -Type DWord -Force
    Set-ItemProperty -Path $tsRegPath -Name "bEnumerateHWBeforeSW" -Value 1 -Type DWord -Force
    Set-ItemProperty -Path $tsRegPath -Name "VisualExperiencePolicy" -Value 1 -Type DWord -Force
    Set-ItemProperty -Path $tsRegPath -Name "ColorDepth" -Value 5 -Type DWord -Force
    Write-Host "  Enabled RemoteFX / GPU acceleration" -ForegroundColor Green

    # Disable unnecessary visual overhead
    Set-ItemProperty -Path $tsRegPath -Name "fDisableCursorBlinking" -Value 1 -Type DWord -Force
    Set-ItemProperty -Path $tsRegPath -Name "fDisableAeroThemeEnabled" -Value 1 -Type DWord -Force
    Write-Host "  Disabled cursor blinking and Aero theme in sessions" -ForegroundColor Green

    # Clean up legacy InitialProgram / fInheritInitialProgram values
    foreach ($legacyProp in @("fInheritInitialProgram", "InitialProgram", "WorkDirectory")) {
        Remove-ItemProperty -Path $tsRegPath -Name $legacyProp -ErrorAction SilentlyContinue
    }
    $rdpTcpPath = "HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp"
    foreach ($legacyProp in @("fInheritInitialProgram", "InitialProgram", "WorkDirectory")) {
        Remove-ItemProperty -Path $rdpTcpPath -Name $legacyProp -ErrorAction SilentlyContinue
    }
    Write-Host "  Cleaned up legacy InitialProgram registry values" -ForegroundColor Green

    # Remove legacy HKLM Run key (replaced by RemoteApp)
    $runKeyPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run"
    Remove-ItemProperty -Path $runKeyPath -Name "GatewayLauncher" -Force -ErrorAction SilentlyContinue

    # Enable RemoteApp: allow any program to be launched as a RemoteApp.
    # The RDP file specifies session-launch.ps1 as the RemoteApp program.
    # This is how CyberArk PSM works -- no desktop, no shell, just the app.
    $tsAppPath = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Terminal Server\TSAppAllowList"
    if (-not (Test-Path $tsAppPath)) {
        New-Item -Path $tsAppPath -Force | Out-Null
    }
    Set-ItemProperty -Path $tsAppPath -Name "fDisabledAllowList" -Value 1 -Type DWord -Force
    Write-Host "  Enabled RemoteApp allow-all (TSAppAllowList\fDisabledAllowList=1)" -ForegroundColor Green

    # TermService restart is deferred to the end of the script so all
    # registry and configuration changes are applied first.
    $needsTermServiceRestart = $true
}

# --- RDS Security: RDP Security Layer (no NLA) ---
# Disable NLA so the RDP file credential prompt works cleanly with pool user
# tokens. The user enters their token in the mstsc dialog, connects directly.
if ($PSCmdlet.ShouldProcess("RDS security", "Set RDP Security Layer (no NLA)")) {
    $rdpTcpPath = "HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp"
    Set-ItemProperty -Path $rdpTcpPath -Name "SecurityLayer" -Value 0 -Type DWord
    Set-ItemProperty -Path $rdpTcpPath -Name "UserAuthentication" -Value 0 -Type DWord
    Write-Host "  RDP Security Layer (SecurityLayer=0, UserAuthentication=0, no NLA)" -ForegroundColor Green

    # Remove any Group Policy overrides
    $tsPolPath = "HKLM:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services"
    if (Test-Path $tsPolPath) {
        Remove-ItemProperty -Path $tsPolPath -Name "SecurityLayer" -ErrorAction SilentlyContinue
        Remove-ItemProperty -Path $tsPolPath -Name "fPromptForPassword" -ErrorAction SilentlyContinue
        Remove-ItemProperty -Path $tsPolPath -Name "AuthenticationLevel" -ErrorAction SilentlyContinue
        Write-Host "  Removed Group Policy NLA overrides" -ForegroundColor Green
    }
}

# ------------------------------------------------------------------
# Step 6b: Lock down bastion desktop for session users
# ------------------------------------------------------------------
Write-Host "[9b/13] Locking down bastion desktop..." -ForegroundColor Yellow

if ($PSCmdlet.ShouldProcess("Bastion desktop policies", "Restrict Task Manager, Explorer, cmd")) {
    # Disable Task Manager (Ctrl+Shift+Esc)
    $sysPolPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System"
    if (-not (Test-Path $sysPolPath)) { New-Item -Path $sysPolPath -Force | Out-Null }
    Set-ItemProperty -Path $sysPolPath -Name "DisableTaskMgr" -Value 1 -Type DWord -Force
    Write-Host "  Disabled Task Manager" -ForegroundColor Green

    # Restrict Explorer: hide drives, disable Run dialog, desktop, and Find
    $explorerPolPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\Explorer"
    if (-not (Test-Path $explorerPolPath)) { New-Item -Path $explorerPolPath -Force | Out-Null }
    Set-ItemProperty -Path $explorerPolPath -Name "NoDrives" -Value 67108863 -Type DWord -Force
    Set-ItemProperty -Path $explorerPolPath -Name "NoRun" -Value 1 -Type DWord -Force
    Set-ItemProperty -Path $explorerPolPath -Name "NoDesktop" -Value 1 -Type DWord -Force
    Set-ItemProperty -Path $explorerPolPath -Name "NoFind" -Value 1 -Type DWord -Force
    Write-Host "  Restricted Explorer (no drives, no Run, no desktop, no Find)" -ForegroundColor Green

    # Disable Command Prompt (cmd.exe)
    $winSysPolPath = "HKLM:\SOFTWARE\Policies\Microsoft\Windows\System"
    if (-not (Test-Path $winSysPolPath)) { New-Item -Path $winSysPolPath -Force | Out-Null }
    Set-ItemProperty -Path $winSysPolPath -Name "DisableCMD" -Value 1 -Type DWord -Force
    Write-Host "  Disabled Command Prompt" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 7: Configure Firewall
# ------------------------------------------------------------------
Write-Host "[10/13] Configuring firewall rules..." -ForegroundColor Yellow

# RDP (should already be open, but ensure it)
$rdpRule = Get-NetFirewallRule -DisplayName "Gateway-RDP" -ErrorAction SilentlyContinue
if (-not $rdpRule) {
    if ($PSCmdlet.ShouldProcess("Gateway-RDP (TCP 3389)", "Create firewall rule")) {
        New-NetFirewallRule -DisplayName "Gateway-RDP" `
            -Direction Inbound -Protocol TCP -LocalPort 3389 `
            -Action Allow -Profile Any | Out-Null
        Write-Host "  Opened port 3389 (RDP)" -ForegroundColor Green
    }
} else {
    Write-Host "  Firewall rule Gateway-RDP already exists" -ForegroundColor Gray
}

# Gateway Agent HTTP API
$apiRule = Get-NetFirewallRule -DisplayName "Gateway-API" -ErrorAction SilentlyContinue
if (-not $apiRule) {
    if ($PSCmdlet.ShouldProcess("Gateway-API (TCP $AgentPort)", "Create firewall rule")) {
        New-NetFirewallRule -DisplayName "Gateway-API" `
            -Direction Inbound -Protocol TCP -LocalPort $AgentPort `
            -Action Allow -Profile Any | Out-Null
        Write-Host "  Opened port $AgentPort (API)" -ForegroundColor Green
    }
} else {
    Write-Host "  Firewall rule Gateway-API already exists" -ForegroundColor Gray
}

# RD Gateway (HTTPS / port 443)
$gwRule = Get-NetFirewallRule -DisplayName "Gateway-HTTPS" -ErrorAction SilentlyContinue
if (-not $gwRule) {
    if ($PSCmdlet.ShouldProcess("Gateway-HTTPS (TCP 443)", "Create firewall rule")) {
        New-NetFirewallRule -DisplayName "Gateway-HTTPS" `
            -Direction Inbound -Protocol TCP -LocalPort 443 `
            -Action Allow -Profile Any | Out-Null
        Write-Host "  Opened port 443 (RD Gateway)" -ForegroundColor Green
    }
} else {
    Write-Host "  Firewall rule Gateway-HTTPS already exists" -ForegroundColor Gray
}

# ------------------------------------------------------------------
# Step 11: Deploy Session Launch Script
# ------------------------------------------------------------------
Write-Host "[11/13] Deploying session launch script..." -ForegroundColor Yellow

$launchScriptPath = "$InstallDir\scripts\session-launch.ps1"
if ($PSCmdlet.ShouldProcess($launchScriptPath, "Deploy session launch script")) {
    $launchScriptContent = @'
#Requires -Version 5.1
<#
.SYNOPSIS
    Session launch script for the RDP bastion gateway.

.DESCRIPTION
    Runs inside an RDS session as the user's shell. Loads target connection
    configuration, injects credentials via cmdkey, launches mstsc.exe to the
    target host, records the session with ffmpeg, and reports status back to
    the gateway agent via callback URL. On exit, credentials and temp files
    are always cleaned up.

.PARAMETER ConfigPath
    Absolute path to the session-config.json file prepared by the gateway agent.
#>
param(
    [Parameter(Mandatory=$true)]
    [string]$ConfigPath
)

# ======================================================================
# PRE-FLIGHT LOGGING -- set up file logging BEFORE anything that could
# fail, so we always have diagnostics even if the script crashes early.
# This runs before $ErrorActionPreference = "Stop" intentionally.
# ======================================================================
$script:LogFile = "C:\Gateway\logs\session-launch-$($env:USERNAME)-$(Get-Date -Format 'yyyyMMdd-HHmmss').log"
$logDir = Split-Path $script:LogFile -Parent
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Force -Path $logDir -ErrorAction SilentlyContinue | Out-Null }

function Write-Log {
    param([string]$Message)
    $line = "[$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')] [session-launch] $Message"
    Write-Host $line
    try { $line | Out-File -Append -Encoding UTF8 $script:LogFile } catch {}
}

# Log immediately so we know the script was invoked
Write-Log "========== SESSION LAUNCH STARTED =========="
Write-Log "Args: ConfigPath=$ConfigPath"
Write-Log "User: $env:USERNAME | Computer: $env:COMPUTERNAME | PID: $PID"
Write-Log "PowerShell: $($PSVersionTable.PSVersion) | OS: $([Environment]::OSVersion.VersionString)"

$ErrorActionPreference = "Stop"

# ======================================================================
# Hide the PowerShell console window -- user should only see mstsc.
# In RemoteApp mode each top-level window is remoted separately;
# hiding this window means only the mstsc window appears on the client.
# ======================================================================
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class Win32Console {
    [DllImport("kernel32.dll")] public static extern IntPtr GetConsoleWindow();
    [DllImport("user32.dll")]   public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
}
"@ -ErrorAction SilentlyContinue
$consoleHwnd = [Win32Console]::GetConsoleWindow()
if ($consoleHwnd -ne [IntPtr]::Zero) {
    [Win32Console]::ShowWindow($consoleHwnd, 0) | Out-Null  # SW_HIDE
    Write-Log "Console window hidden"
} else {
    Write-Log "No console window to hide"
}

# ======================================================================
# Helper: report status back to the gateway agent
# ======================================================================
function Send-StatusCallback {
    param(
        [string]$CallbackUrl,
        [string]$SessionID,
        [hashtable]$Body
    )
    try {
        Invoke-RestMethod -Uri "$CallbackUrl/internal/sessions/$SessionID/status" `
            -Method POST -ContentType "application/json" `
            -Body ($Body | ConvertTo-Json -Depth 4) `
            -TimeoutSec 5 | Out-Null
    } catch {
        Write-Log "WARNING: Failed to send status callback ($($Body.status)): $_"
    }
}

# ======================================================================
# State variables -- declared up front so the finally block can reference them
# ======================================================================
$credTarget     = $null
$ffmpegProcess  = $null
$rdpFile        = $null
$config         = $null
$sessionID      = $null
$callbackUrl    = $null
$recordingDir   = $null
$ffmpegPath     = $null
$finalMp4       = $null

# ======================================================================
# Register engine-exit handler for graceful shutdown on logoff signal
# ======================================================================
Register-EngineEvent -SourceIdentifier PowerShell.Exiting -Action {
    Write-Host "[$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')] [session-launch] PowerShell.Exiting event received -- cleaning up"

    # Best-effort credential removal
    if ($credTarget) {
        & cmdkey /delete:$credTarget 2>$null
    }

    # Best-effort ffmpeg teardown
    if ($ffmpegProcess -and -not $ffmpegProcess.HasExited) {
        Stop-Process -Id $ffmpegProcess.Id -Force -ErrorAction SilentlyContinue
    }

    # Best-effort temp file cleanup
    if ($rdpFile -and (Test-Path $rdpFile)) {
        Remove-Item $rdpFile -Force -ErrorAction SilentlyContinue
    }
    if ($ConfigPath -and (Test-Path $ConfigPath)) {
        Remove-Item $ConfigPath -Force -ErrorAction SilentlyContinue
    }
} | Out-Null

# ======================================================================
# Main logic -- wrapped in try/finally so credential & temp cleanup is
# guaranteed even on forced termination or unhandled exceptions.
# ======================================================================
try {

    # ------------------------------------------------------------------
    # Load session configuration
    # ------------------------------------------------------------------
    Write-Log "Loading configuration from $ConfigPath"

    if (-not (Test-Path $ConfigPath)) {
        Write-Log "ERROR: Config file not found: $ConfigPath"
        # Cannot send callback without config -- just exit
        exit 1
    }

    try {
        $config = Get-Content $ConfigPath -Raw | ConvertFrom-Json
    } catch {
        Write-Log "ERROR: Failed to parse config file: $_"
        exit 1
    }

    # Validate required fields
    $requiredFields = @('target_host', 'target_port', 'target_user', 'target_pass',
                        'session_id', 'recording_dir', 'ffmpeg_path', 'callback_url')
    foreach ($field in $requiredFields) {
        if (-not $config.PSObject.Properties[$field] -or [string]::IsNullOrWhiteSpace($config.$field)) {
            Write-Log "ERROR: Missing or empty required config field: $field"
            # Attempt callback if we have enough info
            if ($config.callback_url -and $config.session_id) {
                Send-StatusCallback -CallbackUrl $config.callback_url `
                    -SessionID $config.session_id `
                    -Body @{ status = "failed"; error = "Missing config field: $field" }
            }
            exit 1
        }
    }

    $targetHost   = $config.target_host
    $targetPort   = $config.target_port
    $targetUser   = $config.target_user
    $targetPass   = $config.target_pass
    $targetDomain = $config.target_domain
    $sessionID    = $config.session_id
    $recordingDir = $config.recording_dir
    $ffmpegPath   = $config.ffmpeg_path
    $callbackUrl  = $config.callback_url

    Write-Log "Session $sessionID -- target ${targetHost}:${targetPort} as ${targetUser}"

    # ------------------------------------------------------------------
    # Ensure recording directory exists
    # ------------------------------------------------------------------
    if (-not (Test-Path $recordingDir)) {
        New-Item -ItemType Directory -Force -Path $recordingDir | Out-Null
        Write-Log "Created recording directory: $recordingDir"
    }

    # ------------------------------------------------------------------
    # Notify agent: session launching
    # ------------------------------------------------------------------
    Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID `
        -Body @{ status = "launching" }

    # ------------------------------------------------------------------
    # Inject credentials for the target via cmdkey
    # ------------------------------------------------------------------
    $credTarget = "TERMSRV/$targetHost"
    if ($targetDomain) {
        $fullUser = "$targetDomain\$targetUser"
    } else {
        $fullUser = $targetUser
    }

    Write-Log "Storing credentials for $credTarget (user: $fullUser)"

    # Remove any stale entries first
    & cmdkey /delete:$credTarget 2>$null

    # Store credentials
    & cmdkey /generic:$credTarget /user:$fullUser /pass:$targetPass
    if ($LASTEXITCODE -ne 0) {
        Write-Log "ERROR: cmdkey failed to store credentials (exit code: $LASTEXITCODE)"
        Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID `
            -Body @{ status = "failed"; error = "cmdkey failed to store credentials" }
        exit 1
    }

    Write-Log "Credentials stored successfully"

    # ------------------------------------------------------------------
    # Create RDP file for the target connection
    # ------------------------------------------------------------------
    $rdpFile = Join-Path $recordingDir "$sessionID.rdp"

    $rdpContent = @"
full address:s:${targetHost}:${targetPort}
username:s:${fullUser}
prompt for credentials:i:0
authentication level:i:0
screen mode id:i:2
desktopwidth:i:1920
desktopheight:i:1080
use multimon:i:0
redirectclipboards:i:1
redirectdrives:i:0
audiomode:i:0
audiocapturemode:i:0
autoreconnection enabled:i:1
connection type:i:7
networkautodetect:i:1
bandwidthautodetect:i:1
"@

    $rdpContent | Out-File -Encoding ASCII $rdpFile
    Write-Log "RDP file written: $rdpFile"

    # ------------------------------------------------------------------
    # Start ffmpeg recording (HLS)
    # ------------------------------------------------------------------
    $ffmpegStarted = $false
    try {
        $ffmpegArgs = @(
            "-f", "gdigrab",
            "-framerate", "15",
            "-offset_x", "0",
            "-offset_y", "0",
            "-video_size", "1920x1080",
            "-i", "desktop",
            "-c:v", "libx264",
            "-preset", "ultrafast",
            "-tune", "zerolatency",
            "-crf", "23",
            "-pix_fmt", "yuv420p",
            "-f", "hls",
            "-hls_time", "4",
            "-hls_list_size", "0",
            "-hls_flags", "append_list+independent_segments",
            "-hls_segment_filename", (Join-Path $recordingDir "segment_%04d.ts"),
            (Join-Path $recordingDir "playlist.m3u8")
        )

        $ffmpegProcess = Start-Process -FilePath $ffmpegPath `
            -ArgumentList $ffmpegArgs `
            -PassThru -NoNewWindow `
            -RedirectStandardError (Join-Path $recordingDir "ffmpeg.log")

        Write-Log "ffmpeg started (PID: $($ffmpegProcess.Id))"
        $ffmpegStarted = $true

        # Small delay to let ffmpeg initialize
        Start-Sleep -Seconds 2

        # Check if ffmpeg crashed immediately
        if ($ffmpegProcess.HasExited) {
            Write-Log "WARNING: ffmpeg exited early (code: $($ffmpegProcess.ExitCode)) -- session will continue without recording"
            $ffmpegStarted = $false
        }
    } catch {
        Write-Log "WARNING: Failed to start ffmpeg: $_ -- session will continue without recording"
        $ffmpegStarted = $false
    }

    # ------------------------------------------------------------------
    # Notify agent: session active (recording started or not)
    # ------------------------------------------------------------------
    $activeBody = @{ status = "active" }
    if ($ffmpegStarted) {
        $activeBody.ffmpeg_pid = $ffmpegProcess.Id
    } else {
        $activeBody.recording = $false
    }
    Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID -Body $activeBody

    # ------------------------------------------------------------------
    # Monitor ffmpeg health in a background job (log warning if it dies)
    # ------------------------------------------------------------------
    $ffmpegWatcher = $null
    if ($ffmpegStarted) {
        $ffmpegWatcher = Start-Job -ScriptBlock {
            param($Pid, $RecDir)
            $proc = Get-Process -Id $Pid -ErrorAction SilentlyContinue
            if ($proc) {
                $proc.WaitForExit()
                $ts = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
                # Write a marker file so the main script can detect the crash
                "$ts ffmpeg exited unexpectedly (code: $($proc.ExitCode))" |
                    Out-File -Append (Join-Path $RecDir "ffmpeg_crash.log")
            }
        } -ArgumentList $ffmpegProcess.Id, $recordingDir
    }

    # ------------------------------------------------------------------
    # Launch mstsc.exe -- this blocks until the user closes it
    # ------------------------------------------------------------------
    Write-Log "Launching mstsc to $targetHost"
    $mstscStart = Get-Date

    $mstscProcess = Start-Process -FilePath "mstsc.exe" `
        -ArgumentList $rdpFile, "/f" `
        -PassThru

    Write-Log "mstsc launched (PID: $($mstscProcess.Id)) -- waiting for it to exit"

    # Block until mstsc exits
    $mstscProcess.WaitForExit()

    $mstscDuration = (Get-Date) - $mstscStart
    Write-Log "mstsc exited (code: $($mstscProcess.ExitCode), duration: $([math]::Round($mstscDuration.TotalSeconds, 1))s)"

    # ------------------------------------------------------------------
    # mstsc exited -- logoff IMMEDIATELY so user never sees gateway desktop.
    # Send status callback first (fast), stop ffmpeg, clean credentials,
    # then logoff. MP4 concatenation is deferred to the Go agent service.
    # ------------------------------------------------------------------

    # Check for early exit indicating connection failure
    if ($mstscDuration.TotalSeconds -lt 10 -and $mstscProcess.ExitCode -ne 0) {
        Write-Log "ERROR: mstsc exited within 10 seconds with code $($mstscProcess.ExitCode) -- likely failed to connect to target"
        Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID `
            -Body @{ status = "failed"; error = "mstsc connection failed (exit code $($mstscProcess.ExitCode))" }
        $sessionFailed = $true
    } else {
        Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID `
            -Body @{ status = "completed" }
    }

    # Stop ffmpeg (fire-and-forget, no waiting for flush)
    if ($ffmpegProcess -and -not $ffmpegProcess.HasExited) {
        Write-Log "Stopping ffmpeg (PID: $($ffmpegProcess.Id))"
        Stop-Process -Id $ffmpegProcess.Id -Force -ErrorAction SilentlyContinue
    }

    if ($ffmpegWatcher) {
        Stop-Job -Job $ffmpegWatcher -ErrorAction SilentlyContinue
        Remove-Job -Job $ffmpegWatcher -Force -ErrorAction SilentlyContinue
    }

    # Clean up credentials immediately
    if ($credTarget) {
        & cmdkey /delete:$credTarget 2>$null
    }

    Write-Log "Session ended -- logoff DISABLED for debugging (user stays on gateway desktop)"

} catch {
    # ------------------------------------------------------------------
    # Unhandled exception -- report failure and re-throw details to log
    # ------------------------------------------------------------------
    Write-Log "FATAL: Unhandled exception: $_"
    Write-Log "Stack trace: $($_.ScriptStackTrace)"

    if ($callbackUrl -and $sessionID) {
        Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID `
            -Body @{ status = "failed"; error = "Unhandled exception: $($_.Exception.Message)" }
    }

    # Set exit code
    $LASTEXITCODE = 1

} finally {
    # ------------------------------------------------------------------
    # GUARANTEED CLEANUP -- runs even on forced termination
    # ------------------------------------------------------------------
    Write-Log "Running cleanup (finally block)"

    # 1. Always delete stored credentials
    if ($credTarget) {
        Write-Log "Removing credentials for $credTarget"
        & cmdkey /delete:$credTarget 2>$null
    }

    # 2. Stop ffmpeg if still running
    if ($ffmpegProcess -and -not $ffmpegProcess.HasExited) {
        Write-Log "Force-stopping ffmpeg (PID: $($ffmpegProcess.Id))"
        Stop-Process -Id $ffmpegProcess.Id -Force -ErrorAction SilentlyContinue
    }

    # 3. Remove temp RDP file
    if ($rdpFile -and (Test-Path $rdpFile)) {
        Remove-Item $rdpFile -Force -ErrorAction SilentlyContinue
        Write-Log "Removed temp RDP file"
    }

    # 4. Remove config file (contains credentials)
    if ($ConfigPath -and (Test-Path $ConfigPath)) {
        Remove-Item $ConfigPath -Force -ErrorAction SilentlyContinue
        Write-Log "Removed config file"
    }

    # 5. Clean up concat log
    if ($recordingDir) {
        $concatLog = Join-Path $recordingDir "ffmpeg_concat.log"
        if (Test-Path $concatLog) {
            Remove-Item $concatLog -Force -ErrorAction SilentlyContinue
        }
    }

    Write-Log "Cleanup complete"
}

# Exit -- this terminates the RDS session
if ($sessionFailed -or $LASTEXITCODE -eq 1) {
    exit 1
}
exit 0
'@

    $launchScriptContent | Out-File -Encoding UTF8 $launchScriptPath -Force
    Write-Host "  Deployed session-launch.ps1 ($($launchScriptContent.Length) bytes)" -ForegroundColor Green
}

# Clean up legacy session-router.ps1 (not needed with RemoteApp)
$routerPath = "$InstallDir\scripts\session-router.ps1"
if (Test-Path $routerPath) {
    Remove-Item $routerPath -Force -ErrorAction SilentlyContinue
    Write-Host "  Removed legacy session-router.ps1" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 8b: Build & Deploy PIN Credential Provider (native C++ COM DLL)
# ------------------------------------------------------------------
Write-Host "[11b/13] Building PIN credential provider..." -ForegroundColor Yellow

$credProvGuid = "{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}"
$credProvDll  = "$InstallDir\bin\PinCredentialProvider.dll"

if ($PSCmdlet.ShouldProcess($credProvDll, "Build and register PIN credential provider")) {

    # ---- Check if native DLL already exists and is up to date ----
    $skipBuild = $false
    $sourceHashFile = "$InstallDir\credprov\.source_hash"
    if (Test-Path $credProvDll) {
        $isNative = $false
        try {
            [System.Reflection.AssemblyName]::GetAssemblyName($credProvDll)
        } catch {
            # BadImageFormatException means it's a native DLL, not .NET
            $isNative = $true
        }

        if ($isNative) {
            # Check if source code has changed since last build by comparing
            # a hash of the embedded source against the stored hash.
            $currentHash = (Get-FileHash -InputStream (
                [IO.MemoryStream]::new([Text.Encoding]::UTF8.GetBytes($PSCommandPath))
            ) -Algorithm SHA256).Hash
            $storedHash = if (Test-Path $sourceHashFile) { Get-Content $sourceHashFile -ErrorAction SilentlyContinue } else { "" }

            if ($currentHash -ne $storedHash) {
                Write-Host "  Source code changed since last build -- rebuilding" -ForegroundColor Yellow
            } else {
                $cpRegPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Providers\$credProvGuid"
                if (-not (Test-Path $cpRegPath)) {
                    Write-Host "  Native DLL exists but not registered -- registering now" -ForegroundColor Yellow
                    & regsvr32.exe /s $credProvDll
                    New-Item -Path $cpRegPath -Force | Out-Null
                    Set-ItemProperty -Path $cpRegPath -Name "(Default)" -Value "P0rtal PIN Credential Provider"
                    Write-Host "  PIN credential provider registered" -ForegroundColor Green
                } else {
                    Write-Host "  Native DLL up to date and registered -- skipping" -ForegroundColor Green
                }
                $skipBuild = $true
            }
        }
    }

    # ---- Release any existing DLL lock ----
    $needsRebootForCredProv = $false
    $finalCredProvDll = $credProvDll

    if (-not $skipBuild -and (Test-Path $credProvDll)) {
        Write-Host "  Releasing existing DLL..." -ForegroundColor Gray

        # Remove credential provider registry key so LogonUI stops loading it
        $cpRegPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Providers\$credProvGuid"
        if (Test-Path $cpRegPath) {
            Remove-Item -Path $cpRegPath -Force -ErrorAction SilentlyContinue
            Write-Host "  Removed credential provider registry entry" -ForegroundColor Gray
        }

        # Remove COM CLSID registration
        $clsidPath = "HKLM:\SOFTWARE\Classes\CLSID\$credProvGuid"
        if (Test-Path $clsidPath) {
            Remove-Item -Path $clsidPath -Recurse -Force -ErrorAction SilentlyContinue
        }

        # Unregister old COM (native DLL via regsvr32)
        & regsvr32.exe /u /s $credProvDll 2>$null

        # Try to delete the old DLL
        $removed = $false
        try {
            Remove-Item $credProvDll -Force -ErrorAction Stop
            $removed = $true
            Write-Host "  Old DLL removed" -ForegroundColor Green
        } catch {
            Write-Host "  Old DLL is locked (loaded by LogonUI) -- compiling to temp path" -ForegroundColor Yellow
            # Compile to a temp name, schedule replacement on reboot
            $credProvDll = "$finalCredProvDll.new"
            Remove-Item $credProvDll -Force -ErrorAction SilentlyContinue
            $needsRebootForCredProv = $true
        }
    }

  if (-not $skipBuild) {
    # ---- Locate or install MSVC Build Tools ----
    $clExe = $null

    # Check for existing MSVC installation via vswhere
    $vswherePaths = @(
        "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe",
        "${env:ProgramFiles}\Microsoft Visual Studio\Installer\vswhere.exe"
    )
    foreach ($vw in $vswherePaths) {
        if (Test-Path $vw) {
            $vsPath = & $vw -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath 2>$null
            if ($vsPath) {
                $vcToolsDir = Get-ChildItem "$vsPath\VC\Tools\MSVC" -Directory -ErrorAction SilentlyContinue |
                    Sort-Object Name -Descending | Select-Object -First 1
                if ($vcToolsDir) {
                    $candidate = Join-Path $vcToolsDir.FullName "bin\Hostx64\x64\cl.exe"
                    if (Test-Path $candidate) { $clExe = $candidate; break }
                }
            }
        }
    }

    # If cl.exe not found, install VS Build Tools with C++ workload
    if (-not $clExe) {
        Write-Host "  MSVC Build Tools not found -- installing (one-time, ~2 GB download)..." -ForegroundColor Yellow
        $bootstrapperUrl = "https://aka.ms/vs/17/release/vs_BuildTools.exe"
        $bootstrapperPath = "$env:TEMP\vs_BuildTools.exe"

        Write-Host "  Downloading VS Build Tools installer..." -ForegroundColor Cyan
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        (New-Object Net.WebClient).DownloadFile($bootstrapperUrl, $bootstrapperPath)

        Write-Host "  Installing C++ build tools (this may take several minutes)..." -ForegroundColor Cyan
        $installArgs = @(
            "--add", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
            "--add", "Microsoft.VisualStudio.Component.Windows11SDK.22621",
            "--quiet", "--wait", "--norestart"
        )
        $proc = Start-Process -FilePath $bootstrapperPath -ArgumentList $installArgs -Wait -PassThru
        if ($proc.ExitCode -ne 0 -and $proc.ExitCode -ne 3010) {
            Write-Host "  WARNING: VS Build Tools installer returned exit code $($proc.ExitCode)" -ForegroundColor Yellow
        } else {
            Write-Host "  VS Build Tools installed successfully" -ForegroundColor Green
        }
        Remove-Item $bootstrapperPath -Force -ErrorAction SilentlyContinue

        # Re-scan for cl.exe after installation
        $vswherePost = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe"
        if (Test-Path $vswherePost) {
            $vsPath = & $vswherePost -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath 2>$null
            if ($vsPath) {
                $vcToolsDir = Get-ChildItem "$vsPath\VC\Tools\MSVC" -Directory -ErrorAction SilentlyContinue |
                    Sort-Object Name -Descending | Select-Object -First 1
                if ($vcToolsDir) {
                    $candidate = Join-Path $vcToolsDir.FullName "bin\Hostx64\x64\cl.exe"
                    if (Test-Path $candidate) { $clExe = $candidate }
                }
            }
        }
    }

    if (-not $clExe) {
        Write-Host "  ERROR: cl.exe not found after installation -- credential provider skipped" -ForegroundColor Red
        Write-Host "  Install 'Visual Studio Build Tools' with the C++ workload manually, then re-run" -ForegroundColor Yellow
    } else {
        Write-Host "  Found cl.exe: $clExe" -ForegroundColor Green

        # ---- Write C++ source files ----
        $srcDir = "$InstallDir\credprov\src"
        New-Item -ItemType Directory -Force -Path $srcDir | Out-Null

        # common.h
        @'
#pragma once

#include <initguid.h>
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <credentialprovider.h>
#include <wincred.h>
#include <winhttp.h>
#include <ntsecapi.h>
#include <strsafe.h>
#include <new>

// {E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}
DEFINE_GUID(CLSID_PinCredentialProvider,
    0xe4a3c2b1, 0x7d6f, 0x4a8e, 0x9c, 0x5b, 0x1d, 0x2e, 0x3f, 0x4a, 0x5b, 0x6c);

// UI field identifiers.
enum PIN_FIELD_ID {
    PFI_TITLE  = 0,  // Large text:    "P0rtal Gateway"
    PFI_PIN    = 1,  // Password text: PIN input
    PFI_SUBMIT = 2,  // Submit button
    PFI_STATUS = 3,  // Small text:    error messages
    PFI_COUNT  = 4,
};

// Descriptor for each field shown in the credential tile.
static const CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR s_rgFieldDescriptors[PFI_COUNT] = {
    { PFI_TITLE,  CPFT_LARGE_TEXT,    L"P0rtal Gateway" },
    { PFI_PIN,    CPFT_PASSWORD_TEXT,  L"PIN" },
    { PFI_SUBMIT, CPFT_SUBMIT_BUTTON, L"Connect" },
    { PFI_STATUS, CPFT_SMALL_TEXT,    L"" },
};

// Field display states.
struct FIELD_STATE_PAIR {
    CREDENTIAL_PROVIDER_FIELD_STATE        cpfs;
    CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE cpfis;
};

static const FIELD_STATE_PAIR s_rgFieldStates[PFI_COUNT] = {
    { CPFS_DISPLAY_IN_SELECTED_TILE, CPFIS_NONE },     // title
    { CPFS_DISPLAY_IN_SELECTED_TILE, CPFIS_FOCUSED },  // pin (auto-focused)
    { CPFS_DISPLAY_IN_SELECTED_TILE, CPFIS_NONE },     // submit
    { CPFS_HIDDEN,                   CPFIS_NONE },     // status (shown on error)
};

// Duplicate a wide string using CoTaskMemAlloc.
inline HRESULT CoAllocString(LPCWSTR src, LPWSTR* dest) {
    if (!src || !dest) return E_INVALIDARG;
    size_t cb = (wcslen(src) + 1) * sizeof(WCHAR);
    *dest = static_cast<LPWSTR>(CoTaskMemAlloc(cb));
    if (!*dest) return E_OUTOFMEMORY;
    memcpy(*dest, src, cb);
    return S_OK;
}
'@ | Out-File -Encoding ASCII "$srcDir\common.h"

        # credential.h
        @'
#pragma once
#include "common.h"

class CPinCredential : public ICredentialProviderCredential {
public:
    CPinCredential();

    // IUnknown
    IFACEMETHODIMP_(ULONG) AddRef() override;
    IFACEMETHODIMP_(ULONG) Release() override;
    IFACEMETHODIMP QueryInterface(REFIID riid, void** ppv) override;

    // ICredentialProviderCredential
    IFACEMETHODIMP Advise(ICredentialProviderCredentialEvents* pcpce) override;
    IFACEMETHODIMP UnAdvise() override;
    IFACEMETHODIMP SetSelected(BOOL* pbAutoLogon) override;
    IFACEMETHODIMP SetDeselected() override;
    IFACEMETHODIMP GetFieldState(DWORD dwFieldID,
        CREDENTIAL_PROVIDER_FIELD_STATE* pcpfs,
        CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE* pcpfis) override;
    IFACEMETHODIMP GetStringValue(DWORD dwFieldID, PWSTR* ppwsz) override;
    IFACEMETHODIMP GetBitmapValue(DWORD dwFieldID, HBITMAP* phbmp) override;
    IFACEMETHODIMP GetCheckboxValue(DWORD dwFieldID, BOOL* pbChecked, PWSTR* ppwszLabel) override;
    IFACEMETHODIMP GetComboBoxValueCount(DWORD dwFieldID, DWORD* pcItems, DWORD* pdwSelectedItem) override;
    IFACEMETHODIMP GetComboBoxValueAt(DWORD dwFieldID, DWORD dwItem, PWSTR* ppwszItem) override;
    IFACEMETHODIMP GetSubmitButtonValue(DWORD dwFieldID, DWORD* pdwAdjacentTo) override;
    IFACEMETHODIMP SetStringValue(DWORD dwFieldID, PCWSTR pwz) override;
    IFACEMETHODIMP SetCheckboxValue(DWORD dwFieldID, BOOL bChecked) override;
    IFACEMETHODIMP SetComboBoxSelectedValue(DWORD dwFieldID, DWORD dwSelectedItem) override;
    IFACEMETHODIMP CommandLinkClicked(DWORD dwFieldID) override;
    IFACEMETHODIMP GetSerialization(
        CREDENTIAL_PROVIDER_GET_SERIALIZATION_RESPONSE* pcpgsr,
        CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION* pcpcs,
        PWSTR* ppwszOptionalStatusText,
        CREDENTIAL_PROVIDER_STATUS_ICON* pcpsiOptionalStatusIcon) override;
    IFACEMETHODIMP ReportResult(NTSTATUS ntsStatus, NTSTATUS ntsSubstatus,
        PWSTR* ppwszOptionalStatusText,
        CREDENTIAL_PROVIDER_STATUS_ICON* pcpsiOptionalStatusIcon) override;

private:
    ~CPinCredential();
    bool ResolvePinToUsername(LPCWSTR pin, WCHAR* username, DWORD cchUsername);

    LONG                                  _cRef;
    ICredentialProviderCredentialEvents*   _pEvents;
    WCHAR                                 _wszPin[16];
};
'@ | Out-File -Encoding ASCII "$srcDir\credential.h"

        # provider.h
        @'
#pragma once
#include "common.h"

class CPinCredential;

class CPinProvider : public ICredentialProvider {
public:
    CPinProvider();

    // IUnknown
    IFACEMETHODIMP_(ULONG) AddRef() override;
    IFACEMETHODIMP_(ULONG) Release() override;
    IFACEMETHODIMP QueryInterface(REFIID riid, void** ppv) override;

    // ICredentialProvider
    IFACEMETHODIMP SetUsageScenario(CREDENTIAL_PROVIDER_USAGE_SCENARIO cpus, DWORD dwFlags) override;
    IFACEMETHODIMP SetSerialization(const CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION* pcpcs) override;
    IFACEMETHODIMP Advise(ICredentialProviderEvents* pcpe, UINT_PTR upAdviseContext) override;
    IFACEMETHODIMP UnAdvise() override;
    IFACEMETHODIMP GetFieldDescriptorCount(DWORD* pdwCount) override;
    IFACEMETHODIMP GetFieldDescriptorAt(DWORD dwIndex, CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR** ppcpfd) override;
    IFACEMETHODIMP GetCredentialCount(DWORD* pdwCount, DWORD* pdwDefault, BOOL* pbAutoLogonCredential) override;
    IFACEMETHODIMP GetCredentialAt(DWORD dwIndex, ICredentialProviderCredential** ppcpc) override;

private:
    ~CPinProvider();
    LONG            _cRef;
    CPinCredential* _pCredential;
};
'@ | Out-File -Encoding ASCII "$srcDir\provider.h"

        # dllmain.cpp
        @'
#include "provider.h"

static LONG    g_cRef    = 0;
static HMODULE g_hModule = NULL;

class CPinProviderFactory : public IClassFactory {
public:
    IFACEMETHODIMP_(ULONG) AddRef()  override { return 2; }
    IFACEMETHODIMP_(ULONG) Release() override { return 1; }

    IFACEMETHODIMP QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_IClassFactory) {
            *ppv = static_cast<IClassFactory*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }

    IFACEMETHODIMP CreateInstance(IUnknown* pUnkOuter, REFIID riid, void** ppv) override {
        if (pUnkOuter) return CLASS_E_NOAGGREGATION;
        CPinProvider* p = new(std::nothrow) CPinProvider();
        if (!p) return E_OUTOFMEMORY;
        HRESULT hr = p->QueryInterface(riid, ppv);
        p->Release();
        return hr;
    }

    IFACEMETHODIMP LockServer(BOOL bLock) override {
        if (bLock) InterlockedIncrement(&g_cRef);
        else       InterlockedDecrement(&g_cRef);
        return S_OK;
    }
};

static CPinProviderFactory g_factory;

BOOL APIENTRY DllMain(HMODULE hModule, DWORD dwReason, LPVOID) {
    if (dwReason == DLL_PROCESS_ATTACH) {
        g_hModule = hModule;
        DisableThreadLibraryCalls(hModule);
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (rclsid == CLSID_PinCredentialProvider) {
        return g_factory.QueryInterface(riid, ppv);
    }
    *ppv = nullptr;
    return CLASS_E_CLASSNOTAVAILABLE;
}

STDAPI DllCanUnloadNow() {
    return g_cRef == 0 ? S_OK : S_FALSE;
}

STDAPI DllRegisterServer() {
    WCHAR dllPath[MAX_PATH];
    GetModuleFileNameW(g_hModule, dllPath, MAX_PATH);

    HKEY hKey = NULL;
    LSTATUS ls = RegCreateKeyExW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Classes\\CLSID\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}",
        0, NULL, 0, KEY_WRITE, NULL, &hKey, NULL);
    if (ls != ERROR_SUCCESS) return HRESULT_FROM_WIN32(ls);
    RegSetValueExW(hKey, NULL, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(L"P0rtal PIN Credential Provider"),
        sizeof(L"P0rtal PIN Credential Provider"));
    RegCloseKey(hKey);

    ls = RegCreateKeyExW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Classes\\CLSID\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}\\InprocServer32",
        0, NULL, 0, KEY_WRITE, NULL, &hKey, NULL);
    if (ls != ERROR_SUCCESS) return HRESULT_FROM_WIN32(ls);
    RegSetValueExW(hKey, NULL, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(dllPath),
        static_cast<DWORD>((wcslen(dllPath) + 1) * sizeof(WCHAR)));
    LPCWSTR threadModel = L"Apartment";
    RegSetValueExW(hKey, L"ThreadingModel", 0, REG_SZ,
        reinterpret_cast<const BYTE*>(threadModel),
        static_cast<DWORD>((wcslen(threadModel) + 1) * sizeof(WCHAR)));
    RegCloseKey(hKey);

    ls = RegCreateKeyExW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\Credential Providers\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}",
        0, NULL, 0, KEY_WRITE, NULL, &hKey, NULL);
    if (ls != ERROR_SUCCESS) return HRESULT_FROM_WIN32(ls);
    RegSetValueExW(hKey, NULL, 0, REG_SZ,
        reinterpret_cast<const BYTE*>(L"P0rtal PIN Credential Provider"),
        sizeof(L"P0rtal PIN Credential Provider"));
    RegCloseKey(hKey);

    return S_OK;
}

STDAPI DllUnregisterServer() {
    RegDeleteTreeW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Classes\\CLSID\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}");
    RegDeleteTreeW(HKEY_LOCAL_MACHINE,
        L"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\Credential Providers\\{E4A3C2B1-7D6F-4A8E-9C5B-1D2E3F4A5B6C}");
    return S_OK;
}
'@ | Out-File -Encoding ASCII "$srcDir\dllmain.cpp"

        # provider.cpp
        @'
#include "provider.h"
#include "credential.h"

CPinProvider::CPinProvider() :
    _cRef(1),
    _pCredential(nullptr) {}

CPinProvider::~CPinProvider() {
    if (_pCredential) {
        _pCredential->Release();
    }
}

HRESULT CPinProvider::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_INVALIDARG;
    *ppv = nullptr;
    if (riid == IID_IUnknown || riid == IID_ICredentialProvider) {
        *ppv = static_cast<ICredentialProvider*>(this);
        AddRef();
        return S_OK;
    }
    return E_NOINTERFACE;
}

ULONG CPinProvider::AddRef()  { return InterlockedIncrement(&_cRef); }
ULONG CPinProvider::Release() {
    LONG c = InterlockedDecrement(&_cRef);
    if (c == 0) delete this;
    return c;
}

HRESULT CPinProvider::SetUsageScenario(
    CREDENTIAL_PROVIDER_USAGE_SCENARIO cpus, DWORD /*dwFlags*/)
{
    switch (cpus) {
    case CPUS_LOGON:
    case CPUS_UNLOCK_WORKSTATION:
    case CPUS_CREDUI:
        _pCredential = new(std::nothrow) CPinCredential();
        return _pCredential ? S_OK : E_OUTOFMEMORY;
    default:
        return E_NOTIMPL;
    }
}

HRESULT CPinProvider::SetSerialization(
    const CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION* /*pcpcs*/)
{
    return E_NOTIMPL;
}

HRESULT CPinProvider::Advise(ICredentialProviderEvents*, UINT_PTR) { return S_OK; }
HRESULT CPinProvider::UnAdvise() { return S_OK; }

HRESULT CPinProvider::GetFieldDescriptorCount(DWORD* pdwCount) {
    *pdwCount = PFI_COUNT;
    return S_OK;
}

HRESULT CPinProvider::GetFieldDescriptorAt(
    DWORD dwIndex, CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR** ppcpfd)
{
    if (dwIndex >= PFI_COUNT || !ppcpfd) return E_INVALIDARG;

    auto* pfd = static_cast<CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR*>(
        CoTaskMemAlloc(sizeof(CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR)));
    if (!pfd) return E_OUTOFMEMORY;

    *pfd = s_rgFieldDescriptors[dwIndex];
    pfd->pszLabel = nullptr;
    if (s_rgFieldDescriptors[dwIndex].pszLabel) {
        CoAllocString(s_rgFieldDescriptors[dwIndex].pszLabel, &pfd->pszLabel);
    }

    *ppcpfd = pfd;
    return S_OK;
}

HRESULT CPinProvider::GetCredentialCount(
    DWORD* pdwCount, DWORD* pdwDefault, BOOL* pbAutoLogonCredential)
{
    *pdwCount = 1;
    *pdwDefault = 0;
    *pbAutoLogonCredential = FALSE;
    return S_OK;
}

HRESULT CPinProvider::GetCredentialAt(
    DWORD dwIndex, ICredentialProviderCredential** ppcpc)
{
    if (dwIndex != 0 || !ppcpc) return E_INVALIDARG;
    _pCredential->AddRef();
    *ppcpc = _pCredential;
    return S_OK;
}
'@ | Out-File -Encoding ASCII "$srcDir\provider.cpp"

        # credential.cpp
        @'
#include "credential.h"

CPinCredential::CPinCredential() :
    _cRef(1),
    _pEvents(nullptr)
{
    _wszPin[0] = L'\0';
}

CPinCredential::~CPinCredential() {
    SecureZeroMemory(_wszPin, sizeof(_wszPin));
    if (_pEvents) _pEvents->Release();
}

HRESULT CPinCredential::QueryInterface(REFIID riid, void** ppv) {
    if (!ppv) return E_INVALIDARG;
    *ppv = nullptr;
    if (riid == IID_IUnknown || riid == IID_ICredentialProviderCredential) {
        *ppv = static_cast<ICredentialProviderCredential*>(this);
        AddRef();
        return S_OK;
    }
    return E_NOINTERFACE;
}

ULONG CPinCredential::AddRef()  { return InterlockedIncrement(&_cRef); }
ULONG CPinCredential::Release() {
    LONG c = InterlockedDecrement(&_cRef);
    if (c == 0) delete this;
    return c;
}

HRESULT CPinCredential::Advise(ICredentialProviderCredentialEvents* pcpce) {
    if (_pEvents) _pEvents->Release();
    _pEvents = pcpce;
    if (_pEvents) _pEvents->AddRef();
    return S_OK;
}

HRESULT CPinCredential::UnAdvise() {
    if (_pEvents) { _pEvents->Release(); _pEvents = nullptr; }
    return S_OK;
}

HRESULT CPinCredential::SetSelected(BOOL* pbAutoLogon) {
    *pbAutoLogon = FALSE;
    return S_OK;
}

HRESULT CPinCredential::SetDeselected() {
    SecureZeroMemory(_wszPin, sizeof(_wszPin));
    if (_pEvents) {
        _pEvents->SetFieldString(this, PFI_PIN, L"");
    }
    return S_OK;
}

HRESULT CPinCredential::GetFieldState(DWORD dwFieldID,
    CREDENTIAL_PROVIDER_FIELD_STATE* pcpfs,
    CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE* pcpfis)
{
    if (dwFieldID >= PFI_COUNT) return E_INVALIDARG;
    *pcpfs  = s_rgFieldStates[dwFieldID].cpfs;
    *pcpfis = s_rgFieldStates[dwFieldID].cpfis;
    return S_OK;
}

HRESULT CPinCredential::GetStringValue(DWORD dwFieldID, PWSTR* ppwsz) {
    switch (dwFieldID) {
    case PFI_TITLE:  return CoAllocString(L"P0rtal Gateway", ppwsz);
    case PFI_PIN:    return CoAllocString(L"", ppwsz);
    case PFI_STATUS: return CoAllocString(L"", ppwsz);
    default:         return E_INVALIDARG;
    }
}

HRESULT CPinCredential::GetBitmapValue(DWORD, HBITMAP*)                    { return E_NOTIMPL; }
HRESULT CPinCredential::GetCheckboxValue(DWORD, BOOL*, PWSTR*)            { return E_NOTIMPL; }
HRESULT CPinCredential::GetComboBoxValueCount(DWORD, DWORD*, DWORD*)      { return E_NOTIMPL; }
HRESULT CPinCredential::GetComboBoxValueAt(DWORD, DWORD, PWSTR*)          { return E_NOTIMPL; }
HRESULT CPinCredential::SetCheckboxValue(DWORD, BOOL)                     { return E_NOTIMPL; }
HRESULT CPinCredential::SetComboBoxSelectedValue(DWORD, DWORD)            { return E_NOTIMPL; }
HRESULT CPinCredential::CommandLinkClicked(DWORD)                         { return E_NOTIMPL; }

HRESULT CPinCredential::GetSubmitButtonValue(DWORD dwFieldID, DWORD* pdwAdjacentTo) {
    if (dwFieldID != PFI_SUBMIT) return E_INVALIDARG;
    *pdwAdjacentTo = PFI_PIN;
    return S_OK;
}

HRESULT CPinCredential::SetStringValue(DWORD dwFieldID, PCWSTR pwz) {
    if (dwFieldID == PFI_PIN) {
        StringCchCopyW(_wszPin, ARRAYSIZE(_wszPin), pwz);
        return S_OK;
    }
    return E_INVALIDARG;
}

bool CPinCredential::ResolvePinToUsername(
    LPCWSTR pin, WCHAR* username, DWORD cchUsername)
{
    bool ok = false;
    HINTERNET hSession = NULL, hConnect = NULL, hRequest = NULL;

    hSession = WinHttpOpen(L"PinCredentialProvider/1.0",
        WINHTTP_ACCESS_TYPE_NO_PROXY, NULL, NULL, 0);
    if (!hSession) goto done;

    hConnect = WinHttpConnect(hSession, L"localhost", 8080, 0);
    if (!hConnect) goto done;

    hRequest = WinHttpOpenRequest(hConnect, L"POST",
        L"/internal/auth/resolve-pin",
        NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, 0);
    if (!hRequest) goto done;

    {
        char pinUtf8[32] = {};
        WideCharToMultiByte(CP_UTF8, 0, pin, -1,
            pinUtf8, sizeof(pinUtf8), NULL, NULL);

        char jsonBody[128] = {};
        StringCbPrintfA(jsonBody, sizeof(jsonBody),
            "{\"pin\":\"%s\"}", pinUtf8);
        DWORD bodyLen = static_cast<DWORD>(strlen(jsonBody));

        LPCWSTR hdrs = L"Content-Type: application/json\r\n";
        if (!WinHttpSendRequest(hRequest, hdrs, (DWORD)-1L,
                jsonBody, bodyLen, bodyLen, 0))
            goto done;

        if (!WinHttpReceiveResponse(hRequest, NULL))
            goto done;

        DWORD statusCode = 0, statusSize = sizeof(statusCode);
        WinHttpQueryHeaders(hRequest,
            WINHTTP_QUERY_STATUS_CODE | WINHTTP_QUERY_FLAG_NUMBER,
            NULL, &statusCode, &statusSize, NULL);
        if (statusCode != 200) goto done;

        char resp[512] = {};
        DWORD bytesRead = 0;
        if (!WinHttpReadData(hRequest, resp, sizeof(resp) - 1, &bytesRead))
            goto done;
        resp[bytesRead] = '\0';

        // Minimal JSON parse — find "username":"<value>".
        char* p = strstr(resp, "\"username\":\"");
        if (!p) goto done;
        p += 12;
        char* end = strchr(p, '"');
        if (!end) goto done;
        *end = '\0';

        MultiByteToWideChar(CP_UTF8, 0, p, -1, username, cchUsername);
        ok = true;
    }

done:
    if (hRequest) WinHttpCloseHandle(hRequest);
    if (hConnect) WinHttpCloseHandle(hConnect);
    if (hSession) WinHttpCloseHandle(hSession);
    return ok;
}

HRESULT CPinCredential::GetSerialization(
    CREDENTIAL_PROVIDER_GET_SERIALIZATION_RESPONSE* pcpgsr,
    CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION*   pcpcs,
    PWSTR*                                          ppwszOptionalStatusText,
    CREDENTIAL_PROVIDER_STATUS_ICON*                pcpsiOptionalStatusIcon)
{
    *pcpgsr = CPGSR_NO_CREDENTIAL_NOT_FINISHED;

    if (_wszPin[0] == L'\0') {
        CoAllocString(L"Please enter your PIN", ppwszOptionalStatusText);
        *pcpsiOptionalStatusIcon = CPSI_ERROR;
        return S_OK;
    }

    WCHAR username[64] = {};
    if (!ResolvePinToUsername(_wszPin, username, ARRAYSIZE(username))) {
        CoAllocString(L"Invalid PIN", ppwszOptionalStatusText);
        *pcpsiOptionalStatusIcon = CPSI_ERROR;

        if (_pEvents) {
            _pEvents->SetFieldState(this, PFI_STATUS, CPFS_DISPLAY_IN_SELECTED_TILE);
            _pEvents->SetFieldString(this, PFI_STATUS, L"Invalid PIN. Try again.");
        }
        return S_OK;
    }

    WCHAR qualifiedUser[128] = {};
    StringCchPrintfW(qualifiedUser, ARRAYSIZE(qualifiedUser), L".\\%s", username);

    DWORD cbBuf = 0;
    CredPackAuthenticationBufferW(0, qualifiedUser, _wszPin, NULL, &cbBuf);

    BYTE* pbBuf = static_cast<BYTE*>(CoTaskMemAlloc(cbBuf));
    if (!pbBuf) return E_OUTOFMEMORY;

    if (!CredPackAuthenticationBufferW(0, qualifiedUser, _wszPin, pbBuf, &cbBuf)) {
        CoTaskMemFree(pbBuf);
        return HRESULT_FROM_WIN32(GetLastError());
    }

    ULONG authPackage = 0;
    {
        HANDLE hLsa = NULL;
        if (SUCCEEDED(HRESULT_FROM_NT(LsaConnectUntrusted(&hLsa)))) {
            LSA_STRING lsaName;
            lsaName.Buffer        = const_cast<PCHAR>("Negotiate");
            lsaName.Length        = 9;
            lsaName.MaximumLength = 10;
            LsaLookupAuthenticationPackage(hLsa, &lsaName, &authPackage);
            LsaDeregisterLogonProcess(hLsa);
        }
    }

    pcpcs->clsidCredentialProvider = CLSID_PinCredentialProvider;
    pcpcs->rgbSerialization        = pbBuf;
    pcpcs->cbSerialization         = cbBuf;
    pcpcs->ulAuthenticationPackage = authPackage;

    *pcpgsr = CPGSR_RETURN_CREDENTIAL_FINISHED;

    SecureZeroMemory(_wszPin, sizeof(_wszPin));
    return S_OK;
}

HRESULT CPinCredential::ReportResult(
    NTSTATUS ntsStatus, NTSTATUS /*ntsSubstatus*/,
    PWSTR* ppwszOptionalStatusText,
    CREDENTIAL_PROVIDER_STATUS_ICON* pcpsiOptionalStatusIcon)
{
    if (ntsStatus != 0) {
        CoAllocString(L"Invalid PIN. Please try again.", ppwszOptionalStatusText);
        *pcpsiOptionalStatusIcon = CPSI_ERROR;
    }
    return S_OK;
}
'@ | Out-File -Encoding ASCII "$srcDir\credential.cpp"

        # PinCredentialProvider.def
        @'
LIBRARY PinCredentialProvider
EXPORTS
    DllGetClassObject    PRIVATE
    DllCanUnloadNow      PRIVATE
    DllRegisterServer    PRIVATE
    DllUnregisterServer  PRIVATE
'@ | Out-File -Encoding ASCII "$InstallDir\credprov\PinCredentialProvider.def"

        Write-Host "  C++ source files written to $srcDir" -ForegroundColor Green

        # ---- Compile with cl.exe via vcvarsall ----
        # Find vcvarsall.bat to set up the build environment
        $vsPath = Split-Path (Split-Path (Split-Path (Split-Path (Split-Path $clExe))))  # up from bin\Hostx64\x64 to VC root
        $vcvarsall = Join-Path (Split-Path $vsPath) "Auxiliary\Build\vcvarsall.bat"
        if (-not (Test-Path $vcvarsall)) {
            # Try broader search
            $vsInstall = & "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe" -latest -products * -property installationPath 2>$null
            if ($vsInstall) {
                $vcvarsall = Join-Path $vsInstall "VC\Auxiliary\Build\vcvarsall.bat"
            }
        }

        if (Test-Path $vcvarsall) {
            Write-Host "  Compiling native credential provider DLL..." -ForegroundColor Cyan

            $defFile = "$InstallDir\credprov\PinCredentialProvider.def"
            $buildCmd = @"
call "$vcvarsall" x64 >nul 2>&1
cd /d "$srcDir"
cl.exe /nologo /EHsc /W4 /O2 /LD /DUNICODE /D_UNICODE ^
    dllmain.cpp provider.cpp credential.cpp ^
    /Fe:"$credProvDll" ^
    /link /DEF:"$defFile" ^
    ole32.lib credui.lib winhttp.lib secur32.lib advapi32.lib
"@
            $buildCmd | Out-File -Encoding ASCII "$InstallDir\credprov\build_temp.bat"
            $buildResult = & cmd.exe /c "$InstallDir\credprov\build_temp.bat" 2>&1
            $buildExitCode = $LASTEXITCODE
            Remove-Item "$InstallDir\credprov\build_temp.bat" -Force -ErrorAction SilentlyContinue

            if ($buildExitCode -eq 0 -and (Test-Path $credProvDll)) {
                Write-Host "  Compiled PinCredentialProvider.dll successfully" -ForegroundColor Green
                # Save source hash so we can detect changes on next run
                $buildHash = (Get-FileHash -InputStream (
                    [IO.MemoryStream]::new([Text.Encoding]::UTF8.GetBytes($PSCommandPath))
                ) -Algorithm SHA256).Hash
                $buildHash | Out-File -Encoding ASCII $sourceHashFile
            } else {
                Write-Host "  WARNING: Compilation failed (exit code: $buildExitCode):" -ForegroundColor Yellow
                $buildResult | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
            }
        } else {
            Write-Host "  ERROR: vcvarsall.bat not found -- cannot set up build environment" -ForegroundColor Red
        }

        # ---- Register with regsvr32 and add credential provider entry ----
        if (Test-Path $credProvDll) {
            if ($needsRebootForCredProv) {
                # Compiled to .dll.new — schedule replacement on reboot
                Write-Host "  Scheduling DLL replacement on next reboot..." -ForegroundColor Yellow
                Add-Type -TypeDefinition @'
                    using System;
                    using System.Runtime.InteropServices;
                    public class MoveFileUtil {
                        [DllImport("kernel32.dll", SetLastError=true, CharSet=CharSet.Unicode)]
                        public static extern bool MoveFileEx(string lpExistingFileName, string lpNewFileName, int dwFlags);
                    }
'@
                # First schedule old DLL for deletion
                [MoveFileUtil]::MoveFileEx($finalCredProvDll, $null, 0x4) | Out-Null
                # Then schedule new DLL to take its place
                [MoveFileUtil]::MoveFileEx($credProvDll, $finalCredProvDll, 0x5) | Out-Null
                Write-Host "  New native DLL will replace old .NET DLL on next reboot" -ForegroundColor Green
                Write-Host "  ACTION REQUIRED: Reboot the server for the PIN credential provider to take effect" -ForegroundColor Red
            } else {
                # DLL is in final location — register it now
                Write-Host "  Registering DLL with regsvr32..." -ForegroundColor Cyan
                $regResult = & regsvr32.exe /s $credProvDll 2>&1
                if ($LASTEXITCODE -eq 0) {
                    Write-Host "  COM registration successful" -ForegroundColor Green
                } else {
                    Write-Host "  WARNING: regsvr32 returned exit code $LASTEXITCODE" -ForegroundColor Yellow
                }

                # Register as a Windows credential provider
                $cpRegPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Providers\$credProvGuid"
                New-Item -Path $cpRegPath -Force | Out-Null
                Set-ItemProperty -Path $cpRegPath -Name "(Default)" -Value "P0rtal PIN Credential Provider"

                Write-Host "  PIN credential provider registered" -ForegroundColor Green
                Write-Host "  Users will see a 'P0rtal Gateway' PIN tile on the logon screen" -ForegroundColor Green
            }
        }
    }
  } # end if (-not $skipBuild)
}

# ------------------------------------------------------------------
# Step 9: Seed Example Credentials File
# ------------------------------------------------------------------
Write-Host "[12/13] Creating example credentials file..." -ForegroundColor Yellow

$credentialsPath = "$InstallDir\config\credentials.json"
if (-not (Test-Path $credentialsPath)) {
    if ($PSCmdlet.ShouldProcess($credentialsPath, "Create example credentials file")) {
        $exampleCreds = @{
            targets = @(
                @{
                    id       = "example-server"
                    name     = "Example Target Server"
                    host     = "10.1.0.7"
                    port     = 3389
                    username = "Administrator"
                    password = "CHANGE_ME"
                    domain   = ""
                }
            )
        } | ConvertTo-Json -Depth 4
        $exampleCreds | Out-File -Encoding UTF8 $credentialsPath
        Write-Host "  Created: $credentialsPath" -ForegroundColor Green
        Write-Host "  WARNING: Edit credentials.json with real target credentials" -ForegroundColor Yellow
    }
} else {
    Write-Host "  Credentials file exists, skipping" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 10: Install Gateway Agent Service
# ------------------------------------------------------------------
Write-Host "[13/13] Installing Gateway Agent service..." -ForegroundColor Yellow

$agentExe = "$InstallDir\bin\gateway-agent.exe"
$agentConfig = "$InstallDir\config\agent.json"

# Write agent config
if ($PSCmdlet.ShouldProcess($agentConfig, "Write agent configuration")) {
    $config = @{
        listen_addr             = "0.0.0.0:$AgentPort"
        recordings_dir          = $RecordingsDir
        install_dir             = $InstallDir
        credentials_file        = $credentialsPath
        user_pool_file          = "$InstallDir\config\user-pool.json"
        session_script          = $launchScriptPath
        ffmpeg_path             = $ffmpegExe
        max_sessions            = $MaxSessions
        session_timeout_minutes = 60
        reconnect_grace_minutes = 5
        log_file                = "$InstallDir\logs\gateway-agent.log"
        gateway_hostname        = $GatewayHostname
    } | ConvertTo-Json -Depth 3
    $config | Out-File -Encoding UTF8 $agentConfig
    Write-Host "  Agent config written: $agentConfig" -ForegroundColor Green
}

# Register as Windows service (if the binary exists)
if (Test-Path $agentExe) {
    $svc = Get-Service -Name "GatewayAgent" -ErrorAction SilentlyContinue
    if (-not $svc) {
        if ($PSCmdlet.ShouldProcess("GatewayAgent", "Register Windows service")) {
            New-Service -Name "GatewayAgent" `
                -BinaryPathName "`"$agentExe`" --config `"$agentConfig`"" `
                -DisplayName "RDP Bastion Gateway Agent" `
                -Description "Manages RDP bastion sessions, recording, and API" `
                -StartupType Automatic | Out-Null
            Write-Host "  Service registered: GatewayAgent" -ForegroundColor Green
        }
    }
    if ($PSCmdlet.ShouldProcess("GatewayAgent", "Start service")) {
        Start-Service -Name "GatewayAgent" -ErrorAction SilentlyContinue
        Write-Host "  Service started" -ForegroundColor Green
    }
} else {
    Write-Host "  WARNING: gateway-agent.exe not found at $agentExe" -ForegroundColor Yellow
    Write-Host "  Deploy the binary and run: sc start GatewayAgent" -ForegroundColor Yellow
}

# ------------------------------------------------------------------
# Summary
# ------------------------------------------------------------------
Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  Installation Complete" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Install directory:  $InstallDir" -ForegroundColor White
Write-Host "  Recordings:         $RecordingsDir" -ForegroundColor White
Write-Host "  RD Gateway:         https://${GatewayHostname}:443" -ForegroundColor White
Write-Host "  API endpoint:       http://$(hostname):$AgentPort" -ForegroundColor White
Write-Host "  Session users:      ${SessionUserPrefix}001 - ${SessionUserPrefix}$('{0:D3}' -f $SessionUserCount)" -ForegroundColor White
Write-Host "  Credentials file:   $credentialsPath" -ForegroundColor White
Write-Host ""

if ($WhatIfPreference) {
    Write-Host "  [DRY RUN] No changes were applied." -ForegroundColor Yellow
    Write-Host ""
} else {
    # Run post-install validation
    $validationPassed = Test-Installation `
        -InstallDir $InstallDir `
        -SessionUserPrefix $SessionUserPrefix `
        -SessionUserCount $SessionUserCount `
        -AgentPort $AgentPort

    Write-Host ""

    if ($needsReboot) {
        Write-Host "  ACTION REQUIRED: Reboot the server to complete RDS installation" -ForegroundColor Red
        Write-Host "  Run: Restart-Computer" -ForegroundColor Red
    } else {
        Write-Host "  Next steps:" -ForegroundColor Yellow
        Write-Host "  1. Edit $credentialsPath with real target credentials" -ForegroundColor White
        Write-Host "  2. Deploy gateway-agent.exe to $InstallDir\bin\" -ForegroundColor White
        Write-Host "  3. Deploy session-launch.ps1 to $InstallDir\scripts\" -ForegroundColor White
        Write-Host "  4. Start the agent: sc start GatewayAgent" -ForegroundColor White
        Write-Host "  5. Test: curl http://$(hostname):$AgentPort/health" -ForegroundColor White
    }

    # Restart TermService at the very end so all registry/config changes are
    # applied before the service picks them up.
    if ($needsTermServiceRestart) {
        Write-Host ""
        Write-Host "  TermService must be restarted for RemoteApp settings to take effect." -ForegroundColor Yellow
        Read-Host "  Press Enter to restart TermService"
        Write-Host "  Restarting TermService..." -ForegroundColor Gray
        try {
            Restart-Service -Name "TermService" -Force
            Start-Sleep -Seconds 3
            $tsSvc = Get-Service -Name "TermService"
            if ($tsSvc.Status -ne "Running") {
                throw "TermService is not running after restart (status: $($tsSvc.Status))"
            }
            Write-Host "  TermService restarted successfully (status: $($tsSvc.Status))" -ForegroundColor Green
        } catch {
            Write-Host "  WARNING: TermService restart failed: $_" -ForegroundColor Red
            Write-Host "  Try manually: Restart-Service -Name 'TermService' -Force" -ForegroundColor Yellow
        }
    }
}
