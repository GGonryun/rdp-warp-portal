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

        // Start recording at current screen resolution.
        var screen = Screen.PrimaryScreen;
        if (screen != null && !_recordingStarted)
        {
            _recordingStarted = _recorder.Start(screen.Bounds.Width, screen.Bounds.Height);
        }

        // Report active status with ffmpeg PID.
        _status.Report("active", _recorder.ProcessId);
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

        // Stop recording and report completion.
        _recorder.Stop();
        _status.Report(reason == 0 ? "completed" : "completed");

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

        var screen = Screen.PrimaryScreen;
        if (screen == null) return;

        int newWidth = screen.Bounds.Width;
        int newHeight = screen.Bounds.Height;

        Logger.Log($"Display resize detected — {newWidth}x{newHeight} (RDP session keeps initial resolution)");
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
