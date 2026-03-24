package api

import (
	"testing"
	"time"

	"github.com/p0-security/rdp-broker/internal/recording"
)

func TestRecordingMatchesTargetFilter(t *testing.T) {
	t.Parallel()

	base := func() *recording.Recording {
		return &recording.Recording{
			ID:            "rec-1",
			TargetID:      "",
			TargetName:    "win-host",
			AgentHostname: "WIN-HOST",
			StartedAt:     time.Now(),
		}
	}

	tests := []struct {
		name       string
		rec        *recording.Recording
		targetName string
		targetID   string
		want       bool
	}{
		{
			name:       "legacy hostname matches target param",
			rec:        base(),
			targetName: "win-host",
			targetID:   "dest-1",
			want:       true,
		},
		{
			name:       "legacy hostname mismatch with target_id set",
			rec:        base(),
			targetName: "other",
			targetID:   "dest-1",
			want:       false,
		},
		{
			name: "stored target_id match",
			rec: func() *recording.Recording {
				r := base()
				r.TargetID = "dest-1"
				return r
			}(),
			targetName: "different-label",
			targetID:   "dest-1",
			want:       true,
		},
		{
			name: "stored target_id mismatch",
			rec: func() *recording.Recording {
				r := base()
				r.TargetID = "dest-2"
				return r
			}(),
			targetName: "win-host",
			targetID:   "dest-1",
			want:       false,
		},
		{
			name:       "hostname only filter",
			rec:        base(),
			targetName: "WIN-HOST",
			targetID:   "",
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := recordingMatchesTargetFilter(tt.rec, tt.targetName, tt.targetID)
			if got != tt.want {
				t.Fatalf("recordingMatchesTargetFilter(...) = %v, want %v", got, tt.want)
			}
		})
	}
}
