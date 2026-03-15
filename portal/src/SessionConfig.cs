using System.Text.Json.Serialization;

namespace Portal;

public class SessionConfig
{
    [JsonPropertyName("session_id")]
    public string SessionId { get; set; } = string.Empty;

    [JsonPropertyName("target_host")]
    public string TargetHost { get; set; } = string.Empty;

    [JsonPropertyName("target_port")]
    public int TargetPort { get; set; }

    [JsonPropertyName("target_user")]
    public string TargetUser { get; set; } = string.Empty;

    [JsonPropertyName("target_pass")]
    public string TargetPass { get; set; } = string.Empty;

    [JsonPropertyName("target_domain")]
    public string TargetDomain { get; set; } = string.Empty;

    [JsonPropertyName("recording_dir")]
    public string RecordingDir { get; set; } = string.Empty;

    [JsonPropertyName("ffmpeg_path")]
    public string FFmpegPath { get; set; } = string.Empty;

    [JsonPropertyName("callback_url")]
    public string CallbackURL { get; set; } = string.Empty;
}
