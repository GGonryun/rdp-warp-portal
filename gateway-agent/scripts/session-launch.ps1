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
public class Win32Process {
    [DllImport("kernel32.dll")] public static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);
    [DllImport("kernel32.dll")] public static extern bool SetPriorityClass(IntPtr hProcess, uint priorityClass);
    [DllImport("kernel32.dll")] public static extern bool SetProcessAffinityMask(IntPtr hProcess, UIntPtr affinityMask);
    [DllImport("kernel32.dll")] public static extern bool CloseHandle(IntPtr handle);
    public const uint PROCESS_SET_INFORMATION = 0x0200;
    public const uint ABOVE_NORMAL_PRIORITY_CLASS = 0x00008000;
    public const uint BELOW_NORMAL_PRIORITY_CLASS = 0x00004000;
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
connection type:i:6
networkautodetect:i:0
bandwidthautodetect:i:0
compression:i:1
videoplaybackmode:i:1
disable wallpaper:i:1
disable full window drag:i:1
disable menu anims:i:1
disable themes:i:0
allow font smoothing:i:1
redirectcomports:i:0
redirectprinters:i:0
redirectsmartcards:i:0
redirectwebauthn:i:0
"@

    $rdpContent | Out-File -Encoding ASCII $rdpFile
    Write-Log "RDP file written: $rdpFile"

    # ------------------------------------------------------------------
    # Start ffmpeg recording (HLS)
    # ------------------------------------------------------------------
    $ffmpegStarted = $false
    Add-Type -AssemblyName System.Windows.Forms

    # Detect best available video encoder (hardware preferred, software fallback).
    # Returns a hashtable with 'name' and 'args' keys.
    function Get-BestEncoder {
        param([string]$FfmpegPath)

        # Check if an encoder is available by querying ffmpeg
        function Test-Encoder {
            param([string]$Encoder)
            try {
                $output = & $FfmpegPath -hide_banner -encoders 2>&1 | Out-String
                return $output -match $Encoder
            } catch { return $false }
        }

        # NVIDIA GPU
        if (Test-Encoder "h264_nvenc") {
            Write-Log "Hardware encoder detected: h264_nvenc"
            return @{
                name = "h264_nvenc"
                args = @("-c:v", "h264_nvenc", "-preset", "p1", "-rc", "constqp", "-qp", "28")
            }
        }

        # Intel QuickSync
        if (Test-Encoder "h264_qsv") {
            Write-Log "Hardware encoder detected: h264_qsv"
            return @{
                name = "h264_qsv"
                args = @("-c:v", "h264_qsv", "-preset", "veryfast", "-global_quality", "28")
            }
        }

        # AMD AMF
        if (Test-Encoder "h264_amf") {
            Write-Log "Hardware encoder detected: h264_amf"
            return @{
                name = "h264_amf"
                args = @("-c:v", "h264_amf", "-quality", "speed", "-qp_i", "28", "-qp_p", "28")
            }
        }

        # Software fallback
        Write-Log "No hardware encoder found, using libx264 software encoding"
        return @{
            name = "libx264"
            args = @("-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf", "28", "-threads", "2")
        }
    }

    $script:encoder = Get-BestEncoder -FfmpegPath $ffmpegPath
    Write-Log "Selected encoder: $($script:encoder.name)"

    # Start-Ffmpeg: launches ffmpeg at the current desktop resolution.
    # Returns the process object or $null on failure.
    # Uses HLS append_list so segments stitch together across restarts.
    function Start-Ffmpeg {
        param([string]$RecDir, [string]$FfmpegPath, [int]$SegCounter)
        try {
            $screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
            $w = $screen.Width
            $h = $screen.Height
            # Ensure even dimensions (x264 requires it)
            if ($w % 2 -ne 0) { $w-- }
            if ($h % 2 -ne 0) { $h-- }
            Write-Log "Starting ffmpeg at ${w}x${h} (segment counter: $SegCounter, encoder: $($script:encoder.name))"

            $args = @(
                "-f", "gdigrab",
                "-framerate", "10",
                "-offset_x", "0",
                "-offset_y", "0",
                "-video_size", "${w}x${h}",
                "-i", "desktop"
            )
            $args += $script:encoder.args
            $args += @(
                "-pix_fmt", "yuv420p",
                "-f", "hls",
                "-hls_time", "4",
                "-hls_list_size", "0",
                "-hls_flags", "append_list+independent_segments",
                "-start_number", "$SegCounter",
                "-hls_segment_filename", (Join-Path $RecDir "segment_%04d.ts"),
                (Join-Path $RecDir "playlist.m3u8")
            )

            $logFile = Join-Path $RecDir "ffmpeg_${SegCounter}.log"
            $proc = Start-Process -FilePath $FfmpegPath `
                -ArgumentList $args `
                -PassThru -NoNewWindow `
                -RedirectStandardError $logFile

            Start-Sleep -Seconds 2
            if ($proc.HasExited) {
                Write-Log "WARNING: ffmpeg exited early (code: $($proc.ExitCode))"
                return $null
            }
            return $proc
        } catch {
            Write-Log "WARNING: Failed to start ffmpeg: $_"
            return $null
        }
    }

    # Get current desktop size for tracking changes
    function Get-DesktopSize {
        $s = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
        return @{ Width = $s.Width; Height = $s.Height }
    }

    # Set-ProcessPriority: adjust the Windows priority class of a process.
    function Set-ProcessPriority {
        param([int]$Pid, [uint32]$PriorityClass)
        $handle = [Win32Process]::OpenProcess([Win32Process]::PROCESS_SET_INFORMATION, $false, [uint32]$Pid)
        if ($handle -ne [IntPtr]::Zero) {
            [Win32Process]::SetPriorityClass($handle, $PriorityClass) | Out-Null
            [Win32Process]::CloseHandle($handle) | Out-Null
        }
    }

    # Set-ProcessAffinity: pin a process to specific CPU cores (bitmask).
    function Set-ProcessAffinity {
        param([int]$Pid, [uint64]$AffinityMask)
        $handle = [Win32Process]::OpenProcess([Win32Process]::PROCESS_SET_INFORMATION, $false, [uint32]$Pid)
        if ($handle -ne [IntPtr]::Zero) {
            [Win32Process]::SetProcessAffinityMask($handle, [UIntPtr]::new($AffinityMask)) | Out-Null
            [Win32Process]::CloseHandle($handle) | Out-Null
        }
    }

    $script:ffmpegSegCounter = 0
    $ffmpegProcess = Start-Ffmpeg -RecDir $recordingDir -FfmpegPath $ffmpegPath -SegCounter $script:ffmpegSegCounter
    if ($ffmpegProcess) {
        $ffmpegStarted = $true
        $lastSize = Get-DesktopSize
        Write-Log "ffmpeg started (PID: $($ffmpegProcess.Id))"

        # Set ffmpeg to BelowNormal priority so it doesn't compete with mstsc
        Set-ProcessPriority -Pid $ffmpegProcess.Id -PriorityClass ([Win32Process]::BELOW_NORMAL_PRIORITY_CLASS)
        Write-Log "  ffmpeg priority set to BelowNormal"

        # Pin ffmpeg to cores 0-1 on machines with 4+ cores
        $numCPU = [Environment]::ProcessorCount
        if ($numCPU -ge 4) {
            Set-ProcessAffinity -Pid $ffmpegProcess.Id -AffinityMask 0x3
            Write-Log "  ffmpeg pinned to cores 0-1 ($numCPU cores available)"
        }
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
    # Launch mstsc.exe
    # ------------------------------------------------------------------
    Write-Log "Launching mstsc to $targetHost"
    $mstscStart = Get-Date

    $mstscProcess = Start-Process -FilePath "mstsc.exe" `
        -ArgumentList $rdpFile, "/f" `
        -PassThru

    # Boost mstsc to AboveNormal priority — user-facing, latency-sensitive
    Set-ProcessPriority -Pid $mstscProcess.Id -PriorityClass ([Win32Process]::ABOVE_NORMAL_PRIORITY_CLASS)
    Write-Log "mstsc launched (PID: $($mstscProcess.Id), priority: AboveNormal) -- waiting for it to exit"

    # ------------------------------------------------------------------
    # Poll loop: wait for mstsc to exit while monitoring for desktop
    # resize events. When the user resizes the RDP window, the gateway
    # desktop resolution changes — restart ffmpeg at the new size so the
    # recording matches. HLS append_list stitches segments seamlessly.
    # ------------------------------------------------------------------
    while (-not $mstscProcess.HasExited) {
        Start-Sleep -Seconds 2

        if ($ffmpegStarted -and $ffmpegProcess -and -not $ffmpegProcess.HasExited) {
            $currentSize = Get-DesktopSize
            if ($currentSize.Width -ne $lastSize.Width -or $currentSize.Height -ne $lastSize.Height) {
                Write-Log "Desktop resized: $($lastSize.Width)x$($lastSize.Height) -> $($currentSize.Width)x$($currentSize.Height) -- restarting ffmpeg"

                # Stop current ffmpeg gracefully
                Stop-Process -Id $ffmpegProcess.Id -Force -ErrorAction SilentlyContinue
                Start-Sleep -Seconds 1

                # Count existing segments to set start_number correctly
                $existingSegs = @(Get-ChildItem -Path $recordingDir -Filter "segment_*.ts" -ErrorAction SilentlyContinue)
                $script:ffmpegSegCounter = $existingSegs.Count

                # Restart at new resolution
                $ffmpegProcess = Start-Ffmpeg -RecDir $recordingDir -FfmpegPath $ffmpegPath -SegCounter $script:ffmpegSegCounter
                if ($ffmpegProcess) {
                    $lastSize = $currentSize
                    Set-ProcessPriority -Pid $ffmpegProcess.Id -PriorityClass ([Win32Process]::BELOW_NORMAL_PRIORITY_CLASS)
                    $numCPU = [Environment]::ProcessorCount
                    if ($numCPU -ge 4) { Set-ProcessAffinity -Pid $ffmpegProcess.Id -AffinityMask 0x3 }
                    Write-Log "ffmpeg restarted (PID: $($ffmpegProcess.Id), priority: BelowNormal)"
                } else {
                    Write-Log "WARNING: ffmpeg failed to restart after resize"
                    $ffmpegStarted = $false
                }
            }
        }
    }

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

    # Logoff immediately so user never sees gateway desktop after mstsc closes.
    Write-Log "Session ended -- logging off"
    logoff

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
