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
    [string]$RecordingsDir = "D:\recordings",
    [string]$FFmpegVersion = "7.1",
    [int]$AgentPort = 8080,
    [int]$MaxSessions = 20,
    [string]$SessionUserPrefix = "gwsession",
    [int]$SessionUserCount = 20,
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
    foreach ($ruleName in @("Gateway-RDP", "Gateway-API")) {
        $rule = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
        if (-not $rule) {
            $missingRules += $ruleName
        }
    }
    if ($missingRules.Count -eq 0) {
        $checks += @{ Name = $checkName; Status = "PASS"; Detail = "Gateway-RDP and Gateway-API rules present" }
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
    Write-Host "[1/5] Removing GatewayAgent service..." -ForegroundColor Yellow
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
    Write-Host "[2/5] Removing session user accounts..." -ForegroundColor Yellow
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

    # --- Remove firewall rules ---
    Write-Host "[3/5] Removing firewall rules..." -ForegroundColor Yellow
    foreach ($ruleName in @("Gateway-RDP", "Gateway-API")) {
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
    Write-Host "[4/5] Removing install directory..." -ForegroundColor Yellow
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

    # --- Revert NLA setting ---
    Write-Host "[5/5] Reverting RDS NLA setting..." -ForegroundColor Yellow
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
    Write-Host "  RDS Session Host role was NOT removed (manual action if desired)." -ForegroundColor Yellow
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
Write-Host "[1/10] Validating prerequisites..." -ForegroundColor Yellow

$osInfo = Get-CimInstance Win32_OperatingSystem
if ($osInfo.Caption -notmatch "Server") {
    throw "This script requires Windows Server. Detected: $($osInfo.Caption)"
}
Write-Host "  OS: $($osInfo.Caption)" -ForegroundColor Green

# ------------------------------------------------------------------
# Step 2: Create Directory Structure
# ------------------------------------------------------------------
Write-Host "[2/10] Creating directory structure..." -ForegroundColor Yellow

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
Write-Host "[3/10] Installing RDS Session Host role..." -ForegroundColor Yellow

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
# Step 4: Download and Install ffmpeg
# ------------------------------------------------------------------
Write-Host "[4/10] Installing ffmpeg..." -ForegroundColor Yellow

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
}

# ------------------------------------------------------------------
# Step 5: Create Session User Accounts
# ------------------------------------------------------------------
Write-Host "[5/10] Creating session user accounts..." -ForegroundColor Yellow

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
# Step 6: Configure RDS Policies
# ------------------------------------------------------------------
Write-Host "[6/10] Configuring RDS policies..." -ForegroundColor Yellow

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

    # When session limit reached: disconnect (don't terminate -- allows reconnect)
    Set-ItemProperty -Path $tsRegPath -Name "fResetBroken" -Value 0
    Write-Host "  Configured disconnect on limit (allows reconnect)" -ForegroundColor Green

    # Disable wallpaper in sessions to reduce recording size
    Set-ItemProperty -Path $tsRegPath -Name "fNoRemoteDesktopWallpaper" -Value 1 -Type DWord -Force
    Write-Host "  Disabled wallpaper in RDS sessions" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 7: Configure Firewall
# ------------------------------------------------------------------
Write-Host "[7/10] Configuring firewall rules..." -ForegroundColor Yellow

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

# ------------------------------------------------------------------
# Step 8: Deploy Session Launch Script
# ------------------------------------------------------------------
Write-Host "[8/10] Deploying session launch script..." -ForegroundColor Yellow

# The session-launch.ps1 is deployed by the installer.
# See Section 6 of the spec for the full script.
# The Go agent will also manage/update this script.

$launchScriptPath = "$InstallDir\scripts\session-launch.ps1"
if (-not (Test-Path $launchScriptPath)) {
    if ($PSCmdlet.ShouldProcess($launchScriptPath, "Create placeholder launch script")) {
        "# Session launch script -- deployed by Gateway Agent" | Out-File $launchScriptPath
        Write-Host "  Placeholder created: $launchScriptPath" -ForegroundColor Yellow
        Write-Host "  NOTE: Deploy the real session-launch.ps1 from the build artifacts" -ForegroundColor Yellow
    }
} else {
    Write-Host "  Script exists: $launchScriptPath" -ForegroundColor Green
}

# ------------------------------------------------------------------
# Step 9: Seed Example Credentials File
# ------------------------------------------------------------------
Write-Host "[9/10] Creating example credentials file..." -ForegroundColor Yellow

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
                    tags     = @("example")
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
Write-Host "[10/10] Installing Gateway Agent service..." -ForegroundColor Yellow

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
