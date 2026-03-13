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
