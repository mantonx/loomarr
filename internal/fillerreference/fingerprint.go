package fillerreference

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// FingerprintMedia decodes one complete, duration-bounded source into the
// visual and audio sequences understood by DuplicateAlgorithm. Callers own
// source identity and path containment; this function owns the exact ffmpeg
// invocation and output parsing so every duplicate gate uses one algorithm.
func FingerprintMedia(ctx context.Context, ffmpegPath, path string, durationMS int64, hasAudio bool) ([]uint64, []uint32, error) {
	frames, err := visualFingerprint(ctx, ffmpegPath, path, durationMS)
	if err != nil {
		return nil, nil, err
	}
	if !hasAudio {
		return frames, nil, nil
	}
	audio, err := audioEnvelope(ctx, ffmpegPath, path, durationMS)
	if err != nil {
		return nil, nil, err
	}
	return frames, audio, nil
}

func visualFingerprint(ctx context.Context, ffmpegPath, path string, durationMS int64) ([]uint64, error) {
	if durationMS <= 0 {
		return nil, fmt.Errorf("source duration is not bounded")
	}
	var stdout, stderr bytes.Buffer
	filter := "fps=2,crop=trunc(iw*0.90/2)*2:trunc(ih*0.78/2)*2,scale=9:8,format=gray"
	cmd := exec.CommandContext(ctx, ffmpegPath, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", path,
		"-t", strconv.FormatFloat(float64(durationMS)/1000, 'f', 3, 64), "-an", "-vf", filter, "-pix_fmt", "gray", "-f", "rawvideo", "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg fingerprint: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	const frameBytes = 9 * 8
	raw := stdout.Bytes()
	if len(raw)%frameBytes != 0 {
		return nil, fmt.Errorf("ffmpeg returned partial gray frame")
	}
	hashes := make([]uint64, 0, len(raw)/frameBytes)
	for len(raw) >= frameBytes {
		var hash uint64
		bit := uint(0)
		for row := 0; row < 8; row++ {
			for column := 0; column < 8; column++ {
				if raw[row*9+column] > raw[row*9+column+1] {
					hash |= 1 << bit
				}
				bit++
			}
		}
		hashes = append(hashes, hash)
		raw = raw[frameBytes:]
	}
	return hashes, nil
}

func audioEnvelope(ctx context.Context, ffmpegPath, path string, durationMS int64) ([]uint32, error) {
	if durationMS <= 0 {
		return nil, fmt.Errorf("source duration is not bounded")
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpegPath, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", path,
		"-t", strconv.FormatFloat(float64(durationMS)/1000, 'f', 3, 64), "-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg audio fingerprint: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	const samplesPerBin = 800
	raw := stdout.Bytes()
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("ffmpeg returned partial audio sample")
	}
	envelope := make([]uint32, 0, len(raw)/(samplesPerBin*2))
	for len(raw) >= samplesPerBin*2 {
		var squares int64
		for offset := 0; offset < samplesPerBin*2; offset += 2 {
			sample := int64(int16(binary.LittleEndian.Uint16(raw[offset : offset+2])))
			squares += sample * sample
		}
		envelope = append(envelope, uint32(math.Sqrt(float64(squares)/samplesPerBin)))
		raw = raw[samplesPerBin*2:]
	}
	return envelope, nil
}
