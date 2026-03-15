namespace Portal;

public static class Logger
{
    private static readonly object _lock = new();
    private static string _logFilePath = string.Empty;
    private static bool _initialized;

    public static void Init()
    {
        if (_initialized) return;

        var logDir = @"C:\Gateway\logs";
        Directory.CreateDirectory(logDir);

        var username = Environment.UserName;
        var timestamp = DateTime.Now.ToString("yyyyMMdd-HHmmss");
        _logFilePath = Path.Combine(logDir, $"portal-{username}-{timestamp}.log");

        _initialized = true;
        Log("Logger initialized");
    }

    public static void Log(string message)
    {
        if (!_initialized) return;

        var line = $"[{DateTime.Now:yyyy-MM-dd HH:mm:ss}] [portal] {message}";
        lock (_lock)
        {
            try
            {
                File.AppendAllText(_logFilePath, line + Environment.NewLine);
            }
            catch
            {
                // Swallow file write errors to avoid cascading failures.
            }
        }
    }

    public static void LogError(string message, Exception? ex)
    {
        Log($"ERROR: {message}");
        if (ex != null)
        {
            Log($"  Exception: {ex.GetType().Name}: {ex.Message}");
            Log($"  StackTrace: {ex.StackTrace}");
            if (ex.InnerException != null)
            {
                Log($"  InnerException: {ex.InnerException.GetType().Name}: {ex.InnerException.Message}");
            }
        }
    }
}
