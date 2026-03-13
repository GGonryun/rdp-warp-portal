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
                    # Ignore errors from query/logoff — user may not be logged in
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

    # When initial program (session-launch.ps1) exits, terminate the session immediately
    Set-ItemProperty -Path $tsRegPath -Name "fResetBroken" -Value 1
    Write-Host "  Configured session termination on shell exit" -ForegroundColor Green

    # Disable wallpaper in sessions to reduce recording size
    Set-ItemProperty -Path $tsRegPath -Name "fNoRemoteDesktopWallpaper" -Value 1 -Type DWord -Force
    Write-Host "  Disabled wallpaper in RDS sessions" -ForegroundColor Green

    # Allow alternate shell / initial program from RDP client
    # fInheritInitialProgram = 1 means "use the program specified by the client or user profile"
    Set-ItemProperty -Path $tsRegPath -Name "fInheritInitialProgram" -Value 1 -Type DWord -Force
    Write-Host "  Enabled initial program inheritance (alternate shell)" -ForegroundColor Green
}

# Enable "Always use client-provided startup program" on the RDP-Tcp listener
if ($PSCmdlet.ShouldProcess("RDP-Tcp WinStation", "Allow alternate shell")) {
    $rdpTcpPath = "HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp"
    Set-ItemProperty -Path $rdpTcpPath -Name "fInheritInitialProgram" -Value 1 -Type DWord -Force
    Write-Host "  RDP-Tcp: allow client alternate shell" -ForegroundColor Green
}

# --- RDS Security: Use RDP security layer instead of NLA ---
# NLA shows a separate CredSSP dialog before the session starts.
# RDP security layer shows the standard Windows login screen inside the session.
# This is required for the .rdp file username pre-fill to work smoothly.
if ($PSCmdlet.ShouldProcess("RDS security layer", "Configure for minimal prompts")) {
    $rdpTcpPath = "HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp"
    Set-ItemProperty -Path $rdpTcpPath -Name "SecurityLayer" -Value 0 -Type DWord
    Set-ItemProperty -Path $rdpTcpPath -Name "UserAuthentication" -Value 0 -Type DWord
    Write-Host "  Set RDP security layer (NLA disabled)" -ForegroundColor Green

    # Also set via Group Policy path (takes precedence over WinStation settings)
    $tsPolPath = "HKLM:\SOFTWARE\Policies\Microsoft\Windows NT\Terminal Services"
    New-Item -Path $tsPolPath -Force -ErrorAction SilentlyContinue | Out-Null
    Set-ItemProperty -Path $tsPolPath -Name "SecurityLayer" -Value 0 -Type DWord
    Set-ItemProperty -Path $tsPolPath -Name "fPromptForPassword" -Value 0 -Type DWord
    Set-ItemProperty -Path $tsPolPath -Name "AuthenticationLevel" -Value 0 -Type DWord
    Write-Host "  Set Group Policy: no password prompt, no auth verification" -ForegroundColor Green

    # Suppress legal notice banners that appear before login
    $systemPolPath = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System"
    Set-ItemProperty -Path $systemPolPath -Name "legalnoticecaption" -Value "" -Type String
    Set-ItemProperty -Path $systemPolPath -Name "legalnoticetext" -Value "" -Type String
    Write-Host "  Cleared legal notice banners" -ForegroundColor Green

    # Restart terminal services to apply all changes
    Restart-Service -Name "TermService" -Force
    Write-Host "  Restarted Terminal Services" -ForegroundColor Green
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

$ErrorActionPreference = "Stop"

# ======================================================================
# Logging
# ======================================================================
function Write-Log {
    param([string]$Message)
    Write-Host "[$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')] [session-launch] $Message"
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
# State variables — declared up front so the finally block can reference them
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
$watchdogJob    = $null

# ======================================================================
# Register engine-exit handler for graceful shutdown on logoff signal
# ======================================================================
Register-EngineEvent -SourceIdentifier PowerShell.Exiting -Action {
    Write-Host "[$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')] [session-launch] PowerShell.Exiting event received — cleaning up"

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
# Main logic — wrapped in try/finally so credential & temp cleanup is
# guaranteed even on forced termination or unhandled exceptions.
# ======================================================================
try {

    # ------------------------------------------------------------------
    # Load session configuration
    # ------------------------------------------------------------------
    Write-Log "Loading configuration from $ConfigPath"

    if (-not (Test-Path $ConfigPath)) {
        Write-Log "ERROR: Config file not found: $ConfigPath"
        # Cannot send callback without config — just exit
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

    Write-Log "Session $sessionID — target ${targetHost}:${targetPort} as ${targetUser}"

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
            Write-Log "WARNING: ffmpeg exited early (code: $($ffmpegProcess.ExitCode)) — session will continue without recording"
            $ffmpegStarted = $false
        }
    } catch {
        Write-Log "WARNING: Failed to start ffmpeg: $_ — session will continue without recording"
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
    # Launch mstsc.exe — this blocks until the user closes it
    # ------------------------------------------------------------------
    Write-Log "Launching mstsc to $targetHost"
    $mstscStart = Get-Date

    $mstscProcess = Start-Process -FilePath "mstsc.exe" `
        -ArgumentList $rdpFile, "/f" `
        -PassThru

    # Layer 3: Watchdog — if mstsc dies and the main script hasn't cleaned up
    # within 5 seconds, force logoff the RDS session as a safety net.
    $watchdogJob = Start-Job -ScriptBlock {
        param($MstscPid)
        try {
            $proc = Get-Process -Id $MstscPid -ErrorAction Stop
            $proc.WaitForExit()
        } catch {}
        Start-Sleep -Seconds 5
        logoff.exe
    } -ArgumentList $mstscProcess.Id

    Write-Log "Watchdog started for mstsc PID $($mstscProcess.Id)"

    # Block until mstsc exits
    $mstscProcess.WaitForExit()

    $mstscDuration = (Get-Date) - $mstscStart
    Write-Log "mstsc exited (code: $($mstscProcess.ExitCode), duration: $([math]::Round($mstscDuration.TotalSeconds, 1))s)"

    # Check for early exit indicating connection failure
    if ($mstscDuration.TotalSeconds -lt 10 -and $mstscProcess.ExitCode -ne 0) {
        Write-Log "ERROR: mstsc exited within 10 seconds with code $($mstscProcess.ExitCode) — likely failed to connect to target"
        Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID `
            -Body @{ status = "failed"; error = "mstsc connection failed (exit code $($mstscProcess.ExitCode))" }
        # Don't exit 1 here — fall through to finally for cleanup
        # But set a flag so we skip the "completed" callback
        $sessionFailed = $true
    }

    # ------------------------------------------------------------------
    # Stop ffmpeg
    # ------------------------------------------------------------------
    if ($ffmpegProcess -and -not $ffmpegProcess.HasExited) {
        Write-Log "Stopping ffmpeg (PID: $($ffmpegProcess.Id))"
        Stop-Process -Id $ffmpegProcess.Id -Force -ErrorAction SilentlyContinue
        # Give ffmpeg a moment to flush buffers
        Start-Sleep -Seconds 2
    }

    if ($ffmpegWatcher) {
        Stop-Job -Job $ffmpegWatcher -ErrorAction SilentlyContinue
        Remove-Job -Job $ffmpegWatcher -Force -ErrorAction SilentlyContinue
    }

    # Check if ffmpeg died mid-session
    $ffmpegCrashLog = Join-Path $recordingDir "ffmpeg_crash.log"
    if (Test-Path $ffmpegCrashLog) {
        $crashInfo = Get-Content $ffmpegCrashLog -Raw
        Write-Log "WARNING: ffmpeg crashed during recording: $crashInfo"
        Remove-Item $ffmpegCrashLog -Force -ErrorAction SilentlyContinue
    }

    Write-Log "ffmpeg stopped"

    # ------------------------------------------------------------------
    # Concatenate HLS segments to final MP4
    # ------------------------------------------------------------------
    $playlistPath = Join-Path $recordingDir "playlist.m3u8"
    $finalMp4     = Join-Path $recordingDir "recording.mp4"

    if (Test-Path $playlistPath) {
        Write-Log "Concatenating HLS segments to MP4..."
        try {
            $concatArgs = @(
                "-y",
                "-i", $playlistPath,
                "-c", "copy",
                $finalMp4
            )
            $concatProcess = Start-Process -FilePath $ffmpegPath `
                -ArgumentList $concatArgs `
                -Wait -NoNewWindow -PassThru `
                -RedirectStandardError (Join-Path $recordingDir "ffmpeg_concat.log")

            if (Test-Path $finalMp4) {
                $fileSize = (Get-Item $finalMp4).Length
                Write-Log "Recording saved: $finalMp4 ($([math]::Round($fileSize / 1MB, 2)) MB)"
                # Clean up HLS segments
                Remove-Item (Join-Path $recordingDir "segment_*.ts") -Force -ErrorAction SilentlyContinue
                Remove-Item $playlistPath -Force -ErrorAction SilentlyContinue
            } else {
                Write-Log "WARNING: MP4 concatenation produced no output file"
            }
        } catch {
            Write-Log "WARNING: MP4 concatenation failed: $_"
        }
    } else {
        Write-Log "No HLS playlist found — skipping concatenation"
    }

    # ------------------------------------------------------------------
    # Notify agent: session completed (unless we already reported failure)
    # ------------------------------------------------------------------
    if (-not $sessionFailed) {
        $completedBody = @{ status = "completed" }
        if ($finalMp4 -and (Test-Path $finalMp4)) {
            $completedBody.recording_path = $finalMp4
        }
        Send-StatusCallback -CallbackUrl $callbackUrl -SessionID $sessionID -Body $completedBody
    }

    Write-Log "Session ended"

} catch {
    # ------------------------------------------------------------------
    # Unhandled exception — report failure and re-throw details to log
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
    # GUARANTEED CLEANUP — runs even on forced termination
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

    # 5. Stop watchdog job (cleanup completed normally, no need for forced logoff)
    if ($watchdogJob) {
        Stop-Job -Job $watchdogJob -ErrorAction SilentlyContinue
        Remove-Job -Job $watchdogJob -Force -ErrorAction SilentlyContinue
        Write-Log "Watchdog job stopped"
    }

    # 6. Clean up concat log
    if ($recordingDir) {
        $concatLog = Join-Path $recordingDir "ffmpeg_concat.log"
        if (Test-Path $concatLog) {
            Remove-Item $concatLog -Force -ErrorAction SilentlyContinue
        }
    }

    Write-Log "Cleanup complete"
}

# Exit — this terminates the RDS session
if ($sessionFailed -or $LASTEXITCODE -eq 1) {
    exit 1
}
exit 0
'@

    $launchScriptContent | Out-File -Encoding UTF8 $launchScriptPath -Force
    Write-Host "  Deployed session-launch.ps1 ($($launchScriptContent.Length) bytes)" -ForegroundColor Green
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
}
