package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxConcurrentCaptures bounds the simultaneous ffmpeg runs so a subnet
// full of cameras cannot stampede the Pi.
const maxConcurrentCaptures = 8

// captureFromCamera grabs one frame from ip, trying stream 0 and falling
// back to stream 1 exactly like the shell script it replaces. Frames are
// written to a temp file and renamed into place only after passing the
// size check, so readers never observe a partial image. It reports
// saved image.
func captureFromCamera(ip string, cfg Config) (string, bool) {
	dest := filepath.Join(cfg.OutputDir, ip+".jpg")
	if name, ok := cfg.NameMap[ip]; ok {
		dest = filepath.Join(cfg.OutputDir, name)
	}

	for _, stream := range []string{"0", "1"} {
		tmp, size, err := grabStream(ip, stream, cfg)
		if err != nil {
			log.Printf("[%s] stream=%s: capture failed: %v", ip, stream, err)
			continue
		}
		if size < cfg.MinSize {
			log.Printf("[%s] stream=%s: image too small (%d bytes)", ip, stream, size)
			_ = os.Remove(tmp)
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			log.Printf("[%s] cannot move image into place: %v", ip, err)
			_ = os.Remove(tmp)
			return "", false
		}
		log.Printf("[%s] saved %s (stream=%s, %d bytes)", ip, dest, stream, size)
		return dest, true
	}

	log.Printf("[%s] no usable image from either stream", ip)
	return "", false
}

// grabStream runs ffmpeg once against one stream of the camera at ip and
// returns the path and size of the captured frame. The caller owns the
// returned file and must remove or rename it.
func grabStream(ip, stream string, cfg Config) (path string, size int64, err error) {
	tmp, err := os.CreateTemp(cfg.OutputDir, ".camera-*.jpg")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.CaptureTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx, cfg.FFmpegPath,
		"-y",
		"-rtsp_transport", "tcp",
		"-i", buildStreamURL(ip, cfg.RTSPPort, stream),
		"-vframes", "1",
		tmpPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		if ctx.Err() != nil {
			return "", 0, fmt.Errorf("ffmpeg timed out after %s", cfg.CaptureTimeout)
		}
		return "", 0, fmt.Errorf("ffmpeg: %v: %s", err, tailOutput(out))
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	return tmpPath, info.Size(), nil
}

// buildStreamURL keeps the URL scheme these cameras expect, with the
// credentials embedded in the path rather than the authority portion.
func buildStreamURL(ip string, port int, stream string) string {
	return fmt.Sprintf("rtsp://%s:%d/user=admin_password=_channel=0_stream=%s.sdp", ip, port, stream)
}

// tailOutput keeps the last lines of ffmpeg output for error messages.
func tailOutput(out []byte) string {
	const max = 300
	s := strings.TrimSpace(string(out))
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}
