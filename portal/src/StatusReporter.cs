using System;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Threading.Tasks;

namespace Portal;

public class StatusReporter
{
    private readonly HttpClient _client;
    private readonly string _baseUrl;

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase
    };

    public StatusReporter(string callbackUrl, string sessionId)
    {
        _baseUrl = $"{callbackUrl.TrimEnd('/')}/internal/sessions/{sessionId}/status";
        _client = new HttpClient
        {
            Timeout = TimeSpan.FromSeconds(5)
        };
    }

    /// <summary>
    /// Fire-and-forget POST to the gateway agent status endpoint.
    /// Logs warnings on failure, never throws.
    /// </summary>
    public async Task ReportAsync(string status, int? ffmpegPid = null, string? recordingPath = null)
    {
        try
        {
            var payload = new StatusPayload
            {
                Status = status,
                FfmpegPid = ffmpegPid,
                RecordingPath = recordingPath
            };

            var json = JsonSerializer.Serialize(payload, JsonOptions);
            var content = new StringContent(json, Encoding.UTF8, "application/json");

            Logger.Log($"Reporting status: {status} to {_baseUrl}");

            var response = await _client.PostAsync(_baseUrl, content);

            if (!response.IsSuccessStatusCode)
            {
                Logger.Log($"Status callback returned {(int)response.StatusCode}: {response.ReasonPhrase}");
            }
        }
        catch (TaskCanceledException)
        {
            Logger.Log($"Status callback timed out for status: {status}");
        }
        catch (Exception ex)
        {
            Logger.Log($"Status callback failed for status: {status} - {ex.Message}");
        }
    }

    /// <summary>
    /// Fire-and-forget wrapper for use in non-async contexts.
    /// Never throws or blocks.
    /// </summary>
    public void Report(string status, int? ffmpegPid = null, string? recordingPath = null)
    {
        _ = Task.Run(() => ReportAsync(status, ffmpegPid, recordingPath));
    }

    private class StatusPayload
    {
        [JsonPropertyName("status")]
        public string Status { get; set; } = "";

        [JsonPropertyName("ffmpeg_pid")]
        public int? FfmpegPid { get; set; }

        [JsonPropertyName("recording_path")]
        public string? RecordingPath { get; set; }
    }
}
