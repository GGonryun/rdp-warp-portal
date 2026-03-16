using System.Windows.Forms;

namespace Portal;

public class PortalForm : Form
{
    private const int WM_DISPLAYCHANGE = 0x007E;
    private const int WM_SIZE = 0x0005;
    private const int ResizeDebounceMs = 300;

    private const int ResizeSuppressionMs = 5000; // ignore resizes for 5s after connect

    private readonly SessionConfig _config;
    private readonly RdpClientHost _rdpHost;
    private readonly System.Windows.Forms.Timer _resizeDebounceTimer;
    private readonly RecordingManager _recorder;
    private readonly StatusReporter _status;
    private bool _recordingStarted;
    private DateTime _connectedAt;
    private int _lastAppliedWidth;
    private int _lastAppliedHeight;
    private const int MinResizeThresholdPx = 50;

    public PortalForm(SessionConfig config)
    {
        _config = config;
        _recorder = new RecordingManager(config.RecordingDir, config.FFmpegPath);
        _status = new StatusReporter(config.CallbackURL, config.SessionId);

        // Borderless, maximized, black background.
        FormBorderStyle = FormBorderStyle.None;
        WindowState = FormWindowState.Maximized;
        BackColor = Color.Black;
        Text = "Portal";

        // Create the RDP ActiveX host control.
        _rdpHost = new RdpClientHost
        {
            Dock = DockStyle.Fill
        };

        // Wire up RDP events.
        _rdpHost.OnRdpConnected += HandleRdpConnected;
        _rdpHost.OnRdpDisconnected += HandleRdpDisconnected;
        _rdpHost.OnRdpConnectionTimeout += HandleRdpTimeout;

        // Add the control to the form. This triggers AxHost creation and AttachInterfaces.
        Controls.Add(_rdpHost);

        // Resize debounce timer.
        _resizeDebounceTimer = new System.Windows.Forms.Timer { Interval = ResizeDebounceMs };
        _resizeDebounceTimer.Tick += OnResizeDebounced;

        // Wire up Shown event to initiate connection.
        Shown += OnFormShown;
    }

    private void OnFormShown(object? sender, EventArgs e)
    {
        try
        {
            // Detect encoder and report launching status before connecting.
            _recorder.DetectEncoder();
            Logger.Log("DetectEncoder complete, reporting status...");
            _status.Report("launching");
            Logger.Log("Status reported, configuring RDP...");

            _rdpHost.Configure(_config);
            Logger.Log("RDP configured, connecting...");
            _rdpHost.Connect();
            Logger.Log("RDP Connect() called");
        }
        catch (Exception ex)
        {
            Logger.LogError("Failed to connect on form shown", ex);
            _status.Report("failed");
            Close();
        }
    }

    private void HandleRdpConnected()
    {
        _connectedAt = DateTime.UtcNow;
        Logger.Log("RDP connected — portal is active");

        // Record initial resolution for resize threshold checks.
        var screen = Screen.PrimaryScreen;
        if (screen != null)
        {
            _lastAppliedWidth = screen.Bounds.Width;
            _lastAppliedHeight = screen.Bounds.Height;
        }

        // Report active status immediately.
        _status.Report("active");

        // Delay recording start by 3s to let the remote desktop render and
        // the display settle after the initial WM_DISPLAYCHANGE flurry.
        var recordingDelayTimer = new System.Windows.Forms.Timer { Interval = 3000 };
        recordingDelayTimer.Tick += (s, _) =>
        {
            recordingDelayTimer.Stop();
            recordingDelayTimer.Dispose();
            StartRecording();
        };
        recordingDelayTimer.Start();
    }

    private void StartRecording()
    {
        if (_recordingStarted) return;

        var screen = Screen.PrimaryScreen;
        if (screen == null) return;

        Logger.Log($"Starting recording at {screen.Bounds.Width}x{screen.Bounds.Height}");
        _recordingStarted = _recorder.Start(screen.Bounds.Width, screen.Bounds.Height);

        if (!_recordingStarted)
        {
            // Retry once after 3 more seconds
            Logger.Log("Recording failed on first attempt, retrying in 3s...");
            var retryTimer = new System.Windows.Forms.Timer { Interval = 3000 };
            retryTimer.Tick += (s, _) =>
            {
                retryTimer.Stop();
                retryTimer.Dispose();
                var retryScreen = Screen.PrimaryScreen;
                if (retryScreen != null)
                {
                    _recordingStarted = _recorder.Start(retryScreen.Bounds.Width, retryScreen.Bounds.Height);
                    if (_recordingStarted)
                        Logger.Log("Recording started on retry");
                    else
                        Logger.Log("Recording failed on retry — giving up");
                }
            };
            retryTimer.Start();
        }

        // Update status with ffmpeg PID and recording dir if recording started.
        if (_recordingStarted)
            _status.Report("active", _recorder.ProcessId, _recorder.ActiveRecordingDir);
    }

    private void HandleRdpTimeout(int timeoutSeconds)
    {
        Logger.Log($"Connection timeout after {timeoutSeconds}s — target may be unreachable");
        _status.Report("failed");

        void ShowError()
        {
            MessageBox.Show(
                this,
                $"Could not connect to {_config.TargetHost}:{_config.TargetPort} within {timeoutSeconds} seconds.\n\n" +
                "The target machine may be offline or unreachable from this bastion.",
                "Connection Timed Out",
                MessageBoxButtons.OK,
                MessageBoxIcon.Warning);
            Close();
        }

        if (InvokeRequired)
            BeginInvoke(ShowError);
        else
            ShowError();
    }

    private void HandleRdpDisconnected(int reason)
    {
        Logger.Log($"RDP disconnected (reason={reason}), stopping recording and closing");

        // Stop recording and report disconnected (not completed — the Go side
        // handles the transition to completed after the reconnect grace period).
        _recorder.Stop();
        _status.Report("disconnected", null, _recorder.ActiveRecordingDir);

        // Marshal to UI thread if needed.
        if (InvokeRequired)
        {
            BeginInvoke(() => Close());
        }
        else
        {
            Close();
        }
    }

    protected override void WndProc(ref Message m)
    {
        switch (m.Msg)
        {
            case WM_DISPLAYCHANGE:
                Logger.Log("WM_DISPLAYCHANGE received — display resolution changed");
                StartResizeDebounce();
                break;

            case WM_SIZE:
                StartResizeDebounce();
                break;
        }

        base.WndProc(ref m);
    }

    private void StartResizeDebounce()
    {
        // Reset the debounce timer on each resize event.
        _resizeDebounceTimer.Stop();
        _resizeDebounceTimer.Start();
    }

    private void OnResizeDebounced(object? sender, EventArgs e)
    {
        _resizeDebounceTimer.Stop();

        // Suppress resizes shortly after initial connection — the display
        // change when the RDP session first establishes is expected.
        if (_connectedAt != default && (DateTime.UtcNow - _connectedAt).TotalMilliseconds < ResizeSuppressionMs)
        {
            Logger.Log("Resize suppressed — within post-connect suppression window");
            return;
        }

        if (!_rdpHost.IsConnected)
        {
            Logger.Log("Resize ignored — not connected");
            return;
        }

        var screen = Screen.PrimaryScreen;
        if (screen == null) return;

        int newWidth = screen.Bounds.Width;
        int newHeight = screen.Bounds.Height;

        // Skip if change is too small.
        if (Math.Abs(newWidth - _lastAppliedWidth) < MinResizeThresholdPx &&
            Math.Abs(newHeight - _lastAppliedHeight) < MinResizeThresholdPx)
        {
            Logger.Log($"Resize below threshold — {newWidth}x{newHeight} vs {_lastAppliedWidth}x{_lastAppliedHeight}");
            return;
        }

        Logger.Log($"Applying display resize — {_lastAppliedWidth}x{_lastAppliedHeight} -> {newWidth}x{newHeight}");

        // Enable SmartSizing for immediate visual feedback while resolution updates.
        _rdpHost.SetSmartSizing(true);

        // Request actual resolution change via UpdateSessionDisplaySettings.
        if (_rdpHost.UpdateSessionDisplay(newWidth, newHeight))
        {
            _lastAppliedWidth = newWidth;
            _lastAppliedHeight = newHeight;

            // Disable SmartSizing after a short delay to let the server process
            // the resolution change — image becomes crisp at the new resolution.
            var smartSizingTimer = new System.Windows.Forms.Timer { Interval = 500 };
            smartSizingTimer.Tick += (s, _) =>
            {
                smartSizingTimer.Stop();
                smartSizingTimer.Dispose();
                _rdpHost.SetSmartSizing(false);
            };
            smartSizingTimer.Start();

            // Restart recording at the new resolution.
            if (_recordingStarted)
            {
                _recorder.Restart(newWidth, newHeight);
            }
        }
        else
        {
            // UpdateSessionDisplaySettings failed — keep SmartSizing as fallback.
            Logger.Log("Falling back to SmartSizing only (scaled view)");
            _lastAppliedWidth = newWidth;
            _lastAppliedHeight = newHeight;
        }
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            _resizeDebounceTimer.Stop();
            _resizeDebounceTimer.Dispose();
            _rdpHost.OnRdpConnected -= HandleRdpConnected;
            _rdpHost.OnRdpDisconnected -= HandleRdpDisconnected;
            _rdpHost.OnRdpConnectionTimeout -= HandleRdpTimeout;
            _recorder.Dispose();
            _rdpHost.Dispose();
        }
        base.Dispose(disposing);
    }
}
