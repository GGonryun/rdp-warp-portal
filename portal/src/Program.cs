using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text.Json;

namespace Portal;

internal static class Program
{
    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool ExitWindowsEx(uint uFlags, uint dwReason);

    [STAThread]
    static void Main(string[] args)
    {
        try
        {
            // Parse -ConfigPath argument.
            var configPath = ParseConfigPath(args);
            if (string.IsNullOrEmpty(configPath))
            {
                MessageBox.Show(
                    "Usage: Portal.exe -ConfigPath <path-to-config.json>",
                    "Portal",
                    MessageBoxButtons.OK,
                    MessageBoxIcon.Error);
                return;
            }

            // Read and deserialize the session config.
            if (!File.Exists(configPath))
            {
                MessageBox.Show(
                    $"Config file not found: {configPath}",
                    "Portal",
                    MessageBoxButtons.OK,
                    MessageBoxIcon.Error);
                return;
            }

            var json = File.ReadAllText(configPath);

            // Delete the config file immediately — it contains credentials.
            try
            {
                File.Delete(configPath);
            }
            catch
            {
                // Best-effort deletion.
            }

            var config = JsonSerializer.Deserialize<SessionConfig>(json);
            if (config == null)
            {
                MessageBox.Show(
                    "Failed to parse session config.",
                    "Portal",
                    MessageBoxButtons.OK,
                    MessageBoxIcon.Error);
                return;
            }

            // Initialize logger.
            Logger.Init();
            Logger.Log($"Portal starting — session_id={config.SessionId} target={config.TargetHost}:{config.TargetPort} user={config.TargetUser} pid={Environment.ProcessId}");

            // Run the portal form.
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.Run(new PortalForm(config));

            Logger.Log("Portal form closed, initiating logoff");
        }
        catch (Exception ex)
        {
            try
            {
                Logger.LogError("Fatal error in Portal", ex);
            }
            catch
            {
                // Logger may not be initialized.
            }
        }
        finally
        {
            // Log off the Windows session. When Portal is the alternate shell,
            // closing it should end the RDP session.
            Logoff();
        }
    }

    private static string? ParseConfigPath(string[] args)
    {
        for (int i = 0; i < args.Length - 1; i++)
        {
            if (string.Equals(args[i], "-ConfigPath", StringComparison.OrdinalIgnoreCase))
            {
                return args[i + 1];
            }
        }
        return null;
    }

    private static void Logoff()
    {
        try
        {
            // Try P/Invoke first: EWX_LOGOFF = 0x00
            if (!ExitWindowsEx(0, 0))
            {
                // Fallback: run logoff.exe
                Process.Start(new ProcessStartInfo
                {
                    FileName = "logoff.exe",
                    UseShellExecute = false,
                    CreateNoWindow = true
                });
            }
        }
        catch
        {
            // Last resort fallback.
            try
            {
                Process.Start(new ProcessStartInfo
                {
                    FileName = "logoff.exe",
                    UseShellExecute = false,
                    CreateNoWindow = true
                });
            }
            catch
            {
                // Nothing more we can do.
            }
        }
    }
}
