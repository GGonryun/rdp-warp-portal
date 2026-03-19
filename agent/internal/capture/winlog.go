//go:build windows

package capture

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// Windows Event Log IDs we care about:
//   Security:
//     4624  - Logon success
//     4625  - Logon failure
//     4634  - Logoff
//     4647  - User-initiated logoff
//     4648  - Explicit credential logon (RunAs)
//     4672  - Special privileges assigned (admin/elevated)
//     4688  - Process creation (with command line if audit policy set)
//     4689  - Process termination
//     4698  - Scheduled task created
//     4699  - Scheduled task deleted
//     4702  - Scheduled task updated
//     4720  - User account created
//     4722  - User account enabled
//     4725  - User account disabled
//     4726  - User account deleted
//     4732  - Member added to local group
//     4733  - Member removed from local group
//     4740  - Account lockout
//     4756  - Member added to universal group
//     4946  - Firewall rule added
//     4947  - Firewall rule modified
//     4948  - Firewall rule deleted
//   System:
//     7036  - Service entered running/stopped state
//     7040  - Service start type changed
//     7045  - New service installed
//   Microsoft-Windows-PowerShell/Operational:
//     4104  - PowerShell script block logging

const winlogScript = `
$lastCheck = (Get-Date).AddSeconds(-5)
# Security events — high-value only. Process create/terminate (4688/4689) excluded
# since they are captured separately by WMI and generate too much noise here.
$eventIds = @(4624,4625,4634,4647,4648,4672,4698,4699,4702,4720,4722,4725,4726,4732,4733,4740,4756,4946,4947,4948)
$sysEventIds = @(7045)
$psEventIds = @(4104)

while ($true) {
    Start-Sleep 3
    $now = Get-Date
    try {
        # Security events
        $secEvents = Get-WinEvent -FilterHashtable @{LogName='Security';ID=$eventIds;StartTime=$lastCheck} -ErrorAction SilentlyContinue
        foreach ($e in $secEvents) {
            # For 4688/4689 events, extract the process name from Properties
            $procName = ''
            if ($e.Id -eq 4688 -and $e.Properties.Count -ge 6) { $procName = $e.Properties[5].Value }
            if ($e.Id -eq 4689 -and $e.Properties.Count -ge 6) { $procName = $e.Properties[5].Value }
            $msg = $e.Message.Split([Environment]::NewLine)[0]
            if ($procName) { $msg = "[$procName] $msg" }
            @{
                ts = $e.TimeCreated.ToString('o')
                type = 'winlog'
                event_id = $e.Id
                log = 'Security'
                source = $e.ProviderName
                message = $msg
                user = if ($e.Properties.Count -ge 6) { $e.Properties[5].Value } else { '' }
                level = $e.LevelDisplayName
            } | ConvertTo-Json -Compress
        }

        # System events (service changes)
        $sysEvents = Get-WinEvent -FilterHashtable @{LogName='System';ID=$sysEventIds;StartTime=$lastCheck} -ErrorAction SilentlyContinue
        foreach ($e in $sysEvents) {
            @{
                ts = $e.TimeCreated.ToString('o')
                type = 'winlog'
                event_id = $e.Id
                log = 'System'
                source = $e.ProviderName
                message = $e.Message.Split([Environment]::NewLine)[0]
                level = $e.LevelDisplayName
            } | ConvertTo-Json -Compress
        }

        # PowerShell script block logging
        $psEvents = Get-WinEvent -FilterHashtable @{LogName='Microsoft-Windows-PowerShell/Operational';ID=$psEventIds;StartTime=$lastCheck} -ErrorAction SilentlyContinue
        foreach ($e in $psEvents) {
            $scriptBlock = if ($e.Properties.Count -ge 3) { $e.Properties[2].Value } else { '' }
            if ($scriptBlock.Length -gt 500) { $scriptBlock = $scriptBlock.Substring(0,500) + '...' }
            @{
                ts = $e.TimeCreated.ToString('o')
                type = 'winlog'
                event_id = $e.Id
                log = 'PowerShell'
                source = $e.ProviderName
                message = 'Script block executed'
                script_block = $scriptBlock
                level = $e.LevelDisplayName
            } | ConvertTo-Json -Compress
        }
    } catch {}
    $lastCheck = $now
}
`

// WinLogEvent represents a captured Windows Event Log entry.
type WinLogEvent struct {
	Timestamp   time.Time
	EventID     int
	Log         string
	Source      string
	Message     string
	User        string
	Level       string
	ScriptBlock string
}

// winlogEventJSON is the JSON structure output by the PowerShell script.
type winlogEventJSON struct {
	Timestamp   string `json:"ts"`
	Type        string `json:"type"`
	EventID     int    `json:"event_id"`
	Log         string `json:"log"`
	Source      string `json:"source"`
	Message     string `json:"message"`
	User        string `json:"user"`
	Level       string `json:"level"`
	ScriptBlock string `json:"script_block"`
}

// WinLogCapture captures Windows Event Log entries via PowerShell.
type WinLogCapture struct {
	onEvent func(WinLogEvent)
	cmd     *exec.Cmd
	cancel  context.CancelFunc
}

// NewWinLogCapture creates a new Windows Event Log capture.
func NewWinLogCapture(onEvent func(WinLogEvent)) *WinLogCapture {
	return &WinLogCapture{onEvent: onEvent}
}

// Start spawns a PowerShell process that polls Windows Event Logs.
func (w *WinLogCapture) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)

	w.cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", winlogScript)

	stdout, err := w.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("get stdout pipe: %w", err)
	}

	if err := w.cmd.Start(); err != nil {
		return fmt.Errorf("start powershell: %w", err)
	}

	slog.Info("windows event log capture started", "pid", w.cmd.Process.Pid)

	go func() {
		scanner := bufio.NewScanner(stdout)
		// Increase buffer for potentially large script block entries.
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var evt winlogEventJSON
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				slog.Debug("failed to parse winlog event", "line", line, "error", err)
				continue
			}

			ts, err := time.Parse(time.RFC3339, evt.Timestamp)
			if err != nil {
				ts = time.Now()
			}

			we := WinLogEvent{
				Timestamp:   ts,
				EventID:     evt.EventID,
				Log:         evt.Log,
				Source:      evt.Source,
				Message:     evt.Message,
				User:        evt.User,
				Level:       evt.Level,
				ScriptBlock: evt.ScriptBlock,
			}

			if w.onEvent != nil {
				w.onEvent(we)
			}
		}

		if err := scanner.Err(); err != nil {
			slog.Debug("winlog event scanner error", "error", err)
		}
	}()

	return nil
}

// Stop terminates the PowerShell process.
func (w *WinLogCapture) Stop() error {
	slog.Info("stopping windows event log capture")

	if w.cancel != nil {
		w.cancel()
	}

	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Wait()
	}

	return nil
}
