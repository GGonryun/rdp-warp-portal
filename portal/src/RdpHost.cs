using System.Windows.Forms;

namespace Portal;

/// <summary>
/// Custom AxHost subclass that wraps the Microsoft RDP ActiveX control.
/// Uses late-binding (dynamic) COM interop so the type library is not needed at build time.
/// The actual COM objects are only available on Windows at runtime.
/// </summary>
public class RdpClientHost : AxHost
{
    // CLSIDs in preference order:
    //   MsRdpClient10NotSafeForScripting — Win10+ / Server 2016+
    //   MsRdpClient9NotSafeForScripting  — Win8.1 / Server 2012 R2
    //   MsTscAx                          — fallback
    private const string ClsidMsRdpClient10 = "7cacbd7b-0d99-468f-ac33-22e495c0afe5";
    private const string ClsidMsRdpClient9 = "8b918b82-7985-4c24-89df-c33ad2bbfbcd";
    private const string ClsidMsTscAx = "1fb464c8-09bb-4017-a2f5-eb742f04392f";

    private const int ConnectionTimeoutSeconds = 15;

    private dynamic? _ocx;
    private bool _connected;
    private readonly System.Windows.Forms.Timer _pollTimer;
    private DateTime _connectStarted;

    /// <summary>Fired when the RDP connection is established.</summary>
    public event Action? OnRdpConnected;

    /// <summary>Fired when the RDP connection is lost. Parameter is the disconnect reason code.</summary>
    public event Action<int>? OnRdpDisconnected;

    /// <summary>Fired when the connection attempt times out. Parameter is the timeout in seconds.</summary>
    public event Action<int>? OnRdpConnectionTimeout;


    public RdpClientHost() : base(ClsidMsRdpClient10)
    {
        _pollTimer = new System.Windows.Forms.Timer { Interval = 500 };
        _pollTimer.Tick += PollConnectionState;
    }

    protected override void AttachInterfaces()
    {
        try
        {
            _ocx = GetOcx();
            Logger.Log("RDP ActiveX control attached (MsRdpClient10)");
        }
        catch (Exception ex)
        {
            Logger.LogError("Failed to attach RDP ActiveX interfaces", ex);
        }
    }

    /// <summary>
    /// Configure the RDP connection using the session config.
    /// Must be called after the control is added to a form (so AttachInterfaces has fired).
    /// </summary>
    public void Configure(SessionConfig config)
    {
        if (_ocx == null)
            throw new InvalidOperationException("RDP ActiveX control is not initialized.");

        try
        {
            _ocx.Server = config.TargetHost;
            _ocx.UserName = config.TargetUser;
            _ocx.Domain = config.TargetDomain ?? "";

            var adv = _ocx.AdvancedSettings9;
            adv.RDPPort = config.TargetPort;
            adv.ClearTextPassword = config.TargetPass;

            // Enable CredSSP/NLA — most targets require Network Level Authentication.
            // AuthenticationLevel 0 = connect even if cert is untrusted (no prompt).
            adv.EnableCredSspSupport = true;
            adv.AuthenticationLevel = 0;

            // Performance / display settings.
            adv.BitmapPeristence = 1; // enable bitmap caching
            _ocx.ColorDepth = 16;
            adv.Compress = 1;

            // Redirect clipboard only.
            adv.RedirectClipboard = true;
            adv.RedirectDrives = false;
            adv.RedirectPrinters = false;
            adv.RedirectSmartCards = false;

            // We handle fullscreen ourselves (borderless maximized form).
            adv.ContainerHandledFullScreen = 1;

            // Set desktop size to primary screen dimensions.
            var bounds = Screen.PrimaryScreen!.Bounds;
            _ocx.DesktopWidth = bounds.Width;
            _ocx.DesktopHeight = bounds.Height;

            Logger.Log($"RDP configured — server={config.TargetHost}:{config.TargetPort} user={config.TargetUser} desktop={bounds.Width}x{bounds.Height}");
        }
        catch (Exception ex)
        {
            Logger.LogError("Failed to configure RDP connection", ex);
            throw;
        }
    }

    /// <summary>
    /// Initiate the RDP connection.
    /// </summary>
    public void Connect()
    {
        if (_ocx == null)
            throw new InvalidOperationException("RDP ActiveX control is not initialized.");

        try
        {
            Logger.Log("Connecting to RDP target...");
            _connectStarted = DateTime.UtcNow;
            _ocx.Connect();
            _pollTimer.Start();
        }
        catch (Exception ex)
        {
            Logger.LogError("Failed to initiate RDP connection", ex);
            throw;
        }
    }

    /// <summary>
    /// Disconnect the current RDP session.
    /// </summary>
    public void Disconnect()
    {
        _pollTimer.Stop();
        if (_ocx == null) return;

        try
        {
            if (IsConnected)
            {
                Logger.Log("Disconnecting RDP session");
                _ocx.Disconnect();
            }
        }
        catch (Exception ex)
        {
            Logger.LogError("Error during RDP disconnect", ex);
        }
    }

    /// <summary>
    /// Attempt a seamless reconnect at a new resolution (IMsRdpClient8::Reconnect).
    /// Falls back to a full disconnect/reconnect cycle if Reconnect() is not available.
    /// </summary>
    public void Reconnect(uint width, uint height)
    {
        if (_ocx == null || !IsConnected) return;

        try
        {
            Logger.Log($"Attempting seamless reconnect at {width}x{height}");
            _ocx!.Reconnect(width, height);
        }
        catch (Exception ex)
        {
            Logger.LogError("Seamless reconnect failed, performing full reconnect cycle", ex);
            try
            {
                _ocx!.Disconnect();
                _ocx!.DesktopWidth = (int)width;
                _ocx!.DesktopHeight = (int)height;
                _ocx!.Connect();
            }
            catch (Exception innerEx)
            {
                Logger.LogError("Full reconnect cycle also failed", innerEx);
            }
        }
    }

    /// <summary>
    /// Whether the RDP session is currently connected.
    /// </summary>
    public bool IsConnected
    {
        get
        {
            try
            {
                // Connected property: 0 = not connected, 1 = connected, 2 = connecting
                return _ocx != null && (short)_ocx!.Connected == 1;
            }
            catch
            {
                return false;
            }
        }
    }

    /// <summary>
    /// Poll the COM object's Connected property to detect state transitions.
    /// This avoids the complexity of COM event sinking without type libraries.
    /// </summary>
    private short _lastLoggedState = -1;

    private void PollConnectionState(object? sender, EventArgs e)
    {
        try
        {
            if (_ocx == null) return;

            short state = (short)_ocx.Connected;

            // Log state changes for diagnostics (0=disconnected, 1=connected, 2=connecting)
            if (state != _lastLoggedState)
            {
                Logger.Log($"RDP connection state: {state} (0=disconnected, 1=connected, 2=connecting)");
                _lastLoggedState = state;
            }

            if (state == 1 && !_connected)
            {
                // Transition: not connected -> connected
                _connected = true;
                Logger.Log("RDP connection established");
                OnRdpConnected?.Invoke();
            }
            else if (state == 0 && _connected)
            {
                // Transition: connected -> disconnected
                _connected = false;
                _pollTimer.Stop();
                LogDisconnectReason("RDP disconnected");
                OnRdpDisconnected?.Invoke(GetDisconnectReason());
            }
            else if (state == 0 && !_connected && _lastLoggedState == 0 &&
                     (DateTime.UtcNow - _connectStarted).TotalSeconds > 2)
            {
                // Connection failed immediately (never reached state 2)
                _pollTimer.Stop();
                LogDisconnectReason("RDP connection failed immediately");
                OnRdpDisconnected?.Invoke(GetDisconnectReason());
            }
            else if (!_connected && (DateTime.UtcNow - _connectStarted).TotalSeconds > ConnectionTimeoutSeconds)
            {
                // Connection attempt timed out (stuck in connecting state)
                _pollTimer.Stop();
                Logger.Log($"RDP connection timed out after {ConnectionTimeoutSeconds}s (state={state})");
                OnRdpConnectionTimeout?.Invoke(ConnectionTimeoutSeconds);
            }
        }
        catch (Exception ex)
        {
            Logger.LogError("Error polling RDP connection state", ex);
        }
    }

    private int GetDisconnectReason()
    {
        try { return (int)_ocx!.ExtendedDisconnectReason; }
        catch { return -1; }
    }

    private void LogDisconnectReason(string prefix)
    {
        try
        {
            int extended = GetDisconnectReason();
            Logger.Log($"{prefix} — ExtendedDisconnectReason={extended}");
        }
        catch
        {
            Logger.Log($"{prefix} — could not read disconnect reason");
        }
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            _pollTimer.Stop();
            _pollTimer.Dispose();
            try
            {
                Disconnect();
            }
            catch
            {
                // Best effort.
            }
        }
        base.Dispose(disposing);
    }
}
