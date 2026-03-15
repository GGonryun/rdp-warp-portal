using System;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;

namespace Portal;

public class RecordingManager : IDisposable
{
    private readonly string _recordingDir;
    private readonly string _ffmpegPath;
    private Process? _ffmpegProcess;
    private int _segmentCounter;
    private string _encoderName = "";
    private string[] _encoderArgs = Array.Empty<string>();

    public int? ProcessId => _ffmpegProcess?.Id;

    [DllImport("kernel32.dll")]
    private static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);

    [DllImport("kernel32.dll")]
    private static extern bool SetPriorityClass(IntPtr handle, uint priorityClass);

    [DllImport("kernel32.dll")]
    private static extern bool SetProcessAffinityMask(IntPtr handle, UIntPtr mask);

    [DllImport("kernel32.dll")]
    private static extern bool CloseHandle(IntPtr handle);

    private const uint PROCESS_SET_INFORMATION = 0x0200;
    private const uint BELOW_NORMAL_PRIORITY_CLASS = 0x00004000;

    public RecordingManager(string recordingDir, string ffmpegPath)
    {
        _recordingDir = recordingDir;
        _ffmpegPath = ffmpegPath;
    }

    /// <summary>
    /// Detect the best available H.264 encoder by running ffmpeg -encoders.
    /// Priority: h264_nvenc, h264_qsv, h264_amf, libx264 (software fallback).
    /// </summary>
    public void DetectEncoder()
    {
        string encoderOutput = "";
        try
        {
            var psi = new ProcessStartInfo
            {
                FileName = _ffmpegPath,
                Arguments = "-hide_banner -encoders",
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };

            using var proc = Process.Start(psi);
            if (proc == null)
            {
                Logger.Log("Failed to start ffmpeg for encoder detection");
                SetEncoder("libx264", new[] { "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf", "28", "-threads", "2" });
                return;
            }

            encoderOutput = proc.StandardOutput.ReadToEnd();
            proc.WaitForExit(10000);
        }
        catch (Exception ex)
        {
            Logger.Log($"Error running ffmpeg encoder detection: {ex.Message}");
            SetEncoder("libx264", new[] { "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf", "28", "-threads", "2" });
            return;
        }

        if (encoderOutput.Contains("h264_nvenc"))
        {
            SetEncoder("h264_nvenc", new[] { "-c:v", "h264_nvenc", "-preset", "p1", "-rc", "constqp", "-qp", "28" });
        }
        else if (encoderOutput.Contains("h264_qsv"))
        {
            SetEncoder("h264_qsv", new[] { "-c:v", "h264_qsv", "-preset", "veryfast", "-global_quality", "28" });
        }
        else if (encoderOutput.Contains("h264_amf"))
        {
            SetEncoder("h264_amf", new[] { "-c:v", "h264_amf", "-quality", "speed", "-qp_i", "28", "-qp_p", "28" });
        }
        else
        {
            SetEncoder("libx264", new[] { "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-crf", "28", "-threads", "2" });
        }
    }

    private void SetEncoder(string name, string[] args)
    {
        _encoderName = name;
        _encoderArgs = args;
        Logger.Log($"Selected encoder: {_encoderName}");
    }

    /// <summary>
    /// Start ffmpeg recording at the given resolution.
    /// Returns true if ffmpeg launched successfully, false otherwise.
    /// </summary>
    public bool Start(int width, int height)
    {
        // Ensure even dimensions (required by most H.264 encoders)
        width = width % 2 == 0 ? width : width - 1;
        height = height % 2 == 0 ? height : height - 1;

        Directory.CreateDirectory(_recordingDir);

        var segmentPattern = Path.Combine(_recordingDir, "segment_%04d.ts");
        var playlistPath = Path.Combine(_recordingDir, "playlist.m3u8");
        var logPath = Path.Combine(_recordingDir, $"ffmpeg_{_segmentCounter}.log");

        var args = string.Join(" ", new[]
        {
            "-f gdigrab",
            "-framerate 10",
            "-offset_x 0",
            "-offset_y 0",
            $"-video_size {width}x{height}",
            "-i desktop"
        });

        args += " " + string.Join(" ", _encoderArgs);

        args += " " + string.Join(" ", new[]
        {
            "-pix_fmt yuv420p",
            "-f hls",
            "-hls_time 4",
            "-hls_list_size 0",
            "-hls_flags append_list+independent_segments",
            $"-start_number {_segmentCounter}",
            $"-hls_segment_filename \"{segmentPattern}\"",
            $"\"{playlistPath}\""
        });

        Logger.Log($"Starting ffmpeg: {_ffmpegPath} {args}");

        try
        {
            var psi = new ProcessStartInfo
            {
                FileName = _ffmpegPath,
                Arguments = args,
                RedirectStandardError = true,
                RedirectStandardOutput = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };

            _ffmpegProcess = Process.Start(psi);
            if (_ffmpegProcess == null)
            {
                Logger.Log("Failed to start ffmpeg process");
                return false;
            }

            // Redirect stderr to log file asynchronously
            _ = Task.Run(() =>
            {
                try
                {
                    using var logWriter = new StreamWriter(logPath, append: false);
                    while (_ffmpegProcess != null && !_ffmpegProcess.HasExited)
                    {
                        var line = _ffmpegProcess.StandardError.ReadLine();
                        if (line != null)
                            logWriter.WriteLine(line);
                    }
                    // Drain remaining output
                    var remaining = _ffmpegProcess?.StandardError.ReadToEnd();
                    if (!string.IsNullOrEmpty(remaining))
                        logWriter.Write(remaining);
                }
                catch
                {
                    // Ignore errors from log writing
                }
            });

            // Set process priority and affinity
            SetProcessPriorityAndAffinity(_ffmpegProcess.Id);

            // Wait 2 seconds then check if process exited early (indicates failure)
            System.Threading.Thread.Sleep(2000);

            if (_ffmpegProcess.HasExited)
            {
                Logger.Log($"ffmpeg exited early with code {_ffmpegProcess.ExitCode}, recording failed");
                _ffmpegProcess.Dispose();
                _ffmpegProcess = null;
                return false;
            }

            Logger.Log($"ffmpeg started successfully (PID: {_ffmpegProcess.Id}, encoder: {_encoderName})");
            return true;
        }
        catch (Exception ex)
        {
            Logger.Log($"Error starting ffmpeg: {ex.Message}");
            _ffmpegProcess?.Dispose();
            _ffmpegProcess = null;
            return false;
        }
    }

    /// <summary>
    /// Stop the current ffmpeg process.
    /// </summary>
    public void Stop()
    {
        if (_ffmpegProcess == null)
            return;

        try
        {
            if (!_ffmpegProcess.HasExited)
            {
                Logger.Log($"Stopping ffmpeg (PID: {_ffmpegProcess.Id})");
                _ffmpegProcess.Kill();
                _ffmpegProcess.WaitForExit(5000);
            }
        }
        catch (Exception ex)
        {
            Logger.Log($"Error stopping ffmpeg: {ex.Message}");
        }
        finally
        {
            _ffmpegProcess.Dispose();
            _ffmpegProcess = null;
        }
    }

    /// <summary>
    /// Restart recording at a new resolution. Kills current ffmpeg,
    /// counts existing segments, and starts with the correct segment number.
    /// </summary>
    public void Restart(int newWidth, int newHeight)
    {
        Stop();

        // Count existing segment files to determine the next start number
        try
        {
            var segmentFiles = Directory.GetFiles(_recordingDir, "segment_*.ts");
            _segmentCounter = segmentFiles.Length;
            Logger.Log($"Restarting recording at {newWidth}x{newHeight}, segment counter: {_segmentCounter}");
        }
        catch (Exception ex)
        {
            Logger.Log($"Error counting segments: {ex.Message}");
        }

        Start(newWidth, newHeight);
    }

    /// <summary>
    /// Set ffmpeg to BelowNormal priority and pin to cores 0-1 on 4+ core machines.
    /// </summary>
    private void SetProcessPriorityAndAffinity(int pid)
    {
        IntPtr handle = IntPtr.Zero;
        try
        {
            handle = OpenProcess(PROCESS_SET_INFORMATION, false, (uint)pid);
            if (handle == IntPtr.Zero)
            {
                Logger.Log("Failed to open ffmpeg process for priority/affinity adjustment");
                return;
            }

            if (!SetPriorityClass(handle, BELOW_NORMAL_PRIORITY_CLASS))
            {
                Logger.Log("Failed to set ffmpeg to BelowNormal priority");
            }
            else
            {
                Logger.Log("Set ffmpeg to BelowNormal priority");
            }

            int coreCount = Environment.ProcessorCount;
            if (coreCount >= 4)
            {
                // Pin to cores 0 and 1 (affinity mask 0x3)
                if (!SetProcessAffinityMask(handle, new UIntPtr(0x3)))
                {
                    Logger.Log("Failed to set ffmpeg CPU affinity");
                }
                else
                {
                    Logger.Log("Pinned ffmpeg to cores 0-1");
                }
            }
        }
        catch (Exception ex)
        {
            Logger.Log($"Error setting process priority/affinity: {ex.Message}");
        }
        finally
        {
            if (handle != IntPtr.Zero)
                CloseHandle(handle);
        }
    }

    public void Dispose()
    {
        Stop();
    }
}
