package ffmpeg

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// URL for a static ffmpeg Windows build (gpl essentials, amd64).
	ffmpegURL = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
)

// EnsureInstalled checks whether ffmpeg is available at the given path (or on PATH).
// If not found, it downloads a static Windows build and extracts ffmpeg.exe into
// the same directory as the running executable. It returns the resolved path to ffmpeg.
func EnsureInstalled(configuredPath string) (string, error) {
	// 1. Check if the configured path works.
	if configuredPath != "" {
		if p, err := exec.LookPath(configuredPath); err == nil {
			slog.Info("ffmpeg found", "path", p)
			return p, nil
		}
	}

	// 2. Check if ffmpeg is on PATH.
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		slog.Info("ffmpeg found on PATH", "path", p)
		return p, nil
	}

	// 3. Check if we already downloaded it next to our exe.
	localPath, err := localFfmpegPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localPath); err == nil {
		slog.Info("ffmpeg found (previously downloaded)", "path", localPath)
		return localPath, nil
	}

	// 4. Download and extract.
	slog.Info("ffmpeg not found, downloading...", "url", ffmpegURL)
	if err := downloadAndExtract(localPath); err != nil {
		return "", fmt.Errorf("failed to install ffmpeg: %w", err)
	}

	slog.Info("ffmpeg installed successfully", "path", localPath)
	return localPath, nil
}

// localFfmpegPath returns the path where we'd place a downloaded ffmpeg.exe,
// which is in the same directory as the running agent executable.
func localFfmpegPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "ffmpeg.exe"), nil
}

func downloadAndExtract(destPath string) error {
	// Download zip to a temp file.
	tmpFile, err := os.CreateTemp("", "ffmpeg-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	slog.Info("downloading ffmpeg", "dest", tmpPath)

	resp, err := http.Get(ffmpegURL)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("download ffmpeg: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		return fmt.Errorf("download ffmpeg: HTTP %d", resp.StatusCode)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("save ffmpeg zip: %w", err)
	}
	tmpFile.Close()

	// Open the zip and find ffmpeg.exe inside it.
	slog.Info("extracting ffmpeg.exe from zip")

	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		// The zip contains a folder like ffmpeg-7.1-essentials_build/bin/ffmpeg.exe
		if strings.HasSuffix(f.Name, "bin/ffmpeg.exe") {
			return extractFile(f, destPath)
		}
	}

	return fmt.Errorf("ffmpeg.exe not found in downloaded zip")
}

func extractFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry: %w", err)
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("extract ffmpeg.exe: %w", err)
	}

	return nil
}
