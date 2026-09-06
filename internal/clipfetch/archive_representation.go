package clipfetch

import (
	"path/filepath"
	"strconv"
	"strings"
)

// archiveRepresentationRank is an observed ordering, not an authenticity claim. Archive's
// "original" label identifies which uploaded representation a derivative came from; it says
// nothing about whether the upload itself is an original broadcast recording or may be reused.
type archiveRepresentationRank struct {
	sourceClass int
	measured    int
	pixels      int64
	height      int64
	width       int64
	bitrate     int64
	bytes       int64
	name        string
}

// pickVideoFile chooses the strongest declared source representation for retained evidence.
// Ranking is deliberately pure and total: API order can change without changing the selected file.
func pickVideoFile(files []archiveFile) (archiveFile, bool) {
	var best archiveFile
	var bestRank archiveRepresentationRank
	found := false
	for _, file := range files {
		rank, ok := rankArchiveRepresentation(file)
		if !ok {
			continue
		}
		if !found || archiveRepresentationBetter(rank, bestRank) {
			best, bestRank, found = file, rank, true
		}
	}
	return best, found
}

func rankArchiveRepresentation(file archiveFile) (archiveRepresentationRank, bool) {
	if strings.TrimSpace(file.Name) == "" || !isVideoFormat(file.Format) {
		return archiveRepresentationRank{}, false
	}
	width := positiveArchiveInt(file.Width)
	height := positiveArchiveInt(file.Height)
	bytes := positiveArchiveInt(file.Size)
	durationMS := int64(parseLengthMS(file.Length))
	measured := 0
	if durationMS > 0 && (width > 0 || height > 0) {
		measured = 1
	}
	pixels := int64(0)
	if width > 0 && height > 0 && width <= (1<<62)/height {
		pixels = width * height
	}
	bitrate := plausibleArchiveBitrate(bytes, durationMS)
	return archiveRepresentationRank{
		sourceClass: archiveSourceClass(file.Source),
		measured:    measured, pixels: pixels, height: height, width: width,
		bitrate: bitrate, bytes: bytes,
		name: strings.ToLower(filepath.Base(file.Name)),
	}, true
}

func archiveRepresentationBetter(candidate, current archiveRepresentationRank) bool {
	for _, pair := range [][2]int64{
		{int64(candidate.sourceClass), int64(current.sourceClass)},
		{int64(candidate.measured), int64(current.measured)},
		{candidate.pixels, current.pixels},
		{candidate.height, current.height},
		{candidate.width, current.width},
		{candidate.bitrate, current.bitrate},
		{candidate.bytes, current.bytes},
	} {
		if pair[0] != pair[1] {
			return pair[0] > pair[1]
		}
	}
	return candidate.name < current.name
}

func archiveSourceClass(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "original":
		return 2
	case "derivative":
		return 1
	default:
		return 0
	}
}

func positiveArchiveInt(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// plausibleArchiveBitrate returns bits/second only inside a deliberately broad compressed-video
// envelope. A corrupt byte or duration claim must not win selection by manufacturing infinity.
func plausibleArchiveBitrate(bytes, durationMS int64) int64 {
	if bytes <= 0 || durationMS <= 0 || bytes > (1<<62)/8000 {
		return 0
	}
	bitsPerSecond := bytes * 8000 / durationMS
	if bitsPerSecond < 32_000 || bitsPerSecond > 500_000_000 {
		return 0
	}
	return bitsPerSecond
}

func archiveRepresentationEvidence(file archiveFile) map[string]string {
	return map[string]string{
		"name": file.Name, "format": file.Format, "source": file.Source,
		"size": file.Size, "length": file.Length, "width": file.Width, "height": file.Height,
	}
}

// isVideoFormat reports whether an Archive `format` is a video ffmpeg can reasonably be expected
// to decode. Format is the API's codec/container declaration; filename extensions are not trusted.
func isVideoFormat(format string) bool {
	value := strings.ToLower(format)
	for _, video := range []string{
		"mpeg4", "mpeg2", "mpeg-2", "h.264", "matroska", "quicktime", "ogg video",
		"webm", "512kb", "hi-mp4", "avi", "dv video", "prores",
	} {
		if strings.Contains(value, video) {
			return true
		}
	}
	return false
}
