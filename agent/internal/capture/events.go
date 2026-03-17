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

const psScript = `
Register-WmiEvent -Class Win32_ProcessStartTrace -Action {
    $p = $Event.SourceEventArgs.NewEvent
    @{ts=(Get-Date -Format o);type='process_start';pid=$p.ProcessId;name=$p.ProcessName;command_line=$p.CommandLine;parent_pid=$p.ParentProcessId} | ConvertTo-Json -Compress
} | Out-Null
Register-WmiEvent -Class Win32_ProcessStopTrace -Action {
    $p = $Event.SourceEventArgs.NewEvent
    @{ts=(Get-Date -Format o);type='process_end';pid=$p.ProcessId;name=$p.ProcessName;exit_code=$p.ExitStatus} | ConvertTo-Json -Compress
} | Out-Null
while($true){Start-Sleep 1}
`

// ProcessEvent represents a captured process event.
type ProcessEvent struct {
	Timestamp   time.Time
	Type        string // "process_start" or "process_end"
	PID         uint32
	Name        string
	CommandLine string
	User        string
	ParentPID   uint32
	ExitCode    int32
}

// psEventJSON is the JSON structure output by the PowerShell script.
type psEventJSON struct {
	Timestamp   string `json:"ts"`
	Type        string `json:"type"`
	PID         uint32 `json:"pid"`
	Name        string `json:"name"`
	CommandLine string `json:"command_line"`
	ParentPID   uint32 `json:"parent_pid"`
	ExitCode    int32  `json:"exit_code"`
}

// EventCapture captures process events via PowerShell WMI subscriptions.
type EventCapture struct {
	onEvent func(ProcessEvent)
	cmd     *exec.Cmd
	cancel  context.CancelFunc
}

// NewEventCapture creates a new event capture.
func NewEventCapture(onEvent func(ProcessEvent)) *EventCapture {
	return &EventCapture{
		onEvent: onEvent,
	}
}

// Start spawns a PowerShell process that subscribes to WMI process events.
func (e *EventCapture) Start(ctx context.Context) error {
	ctx, e.cancel = context.WithCancel(ctx)

	e.cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", psScript)

	stdout, err := e.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("get stdout pipe: %w", err)
	}

	if err := e.cmd.Start(); err != nil {
		return fmt.Errorf("start powershell: %w", err)
	}

	slog.Info("process event capture started", "pid", e.cmd.Process.Pid)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			var evt psEventJSON
			if err := json.Unmarshal([]byte(line), &evt); err != nil {
				slog.Debug("failed to parse process event", "line", line, "error", err)
				continue
			}

			ts, err := time.Parse(time.RFC3339, evt.Timestamp)
			if err != nil {
				ts = time.Now()
			}

			pe := ProcessEvent{
				Timestamp:   ts,
				Type:        evt.Type,
				PID:         evt.PID,
				Name:        evt.Name,
				CommandLine: evt.CommandLine,
				ParentPID:   evt.ParentPID,
				ExitCode:    evt.ExitCode,
			}

			if e.onEvent != nil {
				e.onEvent(pe)
			}
		}

		if err := scanner.Err(); err != nil {
			slog.Debug("process event scanner error", "error", err)
		}
	}()

	return nil
}

// Stop terminates the PowerShell process.
func (e *EventCapture) Stop() error {
	slog.Info("stopping process event capture")

	if e.cancel != nil {
		e.cancel()
	}

	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Wait()
	}

	return nil
}
