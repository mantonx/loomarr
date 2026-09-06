package fillerstructuremedia

import "github.com/loomarr/loomarr/internal/mediatools"

const (
	inputToken    = "{input}"
	startToken    = "{start}"
	durationToken = "{duration}"
	outputToken   = "{output}"
	listToken     = "{list}"
)

// PartArguments renders one complete source interval into the canonical
// join-compatible assessment profile.
func PartArguments(source string, startMS, durationMS int64, output string) []string {
	return replaceTokens(partArgumentTemplate(), map[string]string{
		inputToken: source, startToken: mediatools.MsToFFmpegTime(startMS),
		durationToken: mediatools.MsToFFmpegTime(durationMS), outputToken: output,
	})
}

// ConcatArguments joins canonical parts without another lossy encode.
func ConcatArguments(list, output string) []string {
	return replaceTokens(concatArgumentTemplate(), map[string]string{listToken: list, outputToken: output})
}

func partArgumentTemplate() []string {
	// -t is deliberately an output option after -i. As an input option it capped audio while
	// the fps filter emitted video through the source's full timeline on real MP4 inputs.
	// start_time=0 prevents a positive source start timestamp from dropping the first output
	// frame and turning an otherwise constant 30 fps stream into a non-canonical average rate.
	return []string{
		"-nostdin", "-hide_banner", "-v", "error", "-y",
		"-threads", "1", "-ss", startToken, "-i", inputToken, "-t", durationToken,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1",
		"-vf", "fps=30:start_time=0,scale=w=960:h=720:force_original_aspect_ratio=decrease,pad=960:720:(ow-iw)/2:(oh-ih)/2,setsar=1",
		"-c:v", "libx264", "-preset", "medium", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2",
		"-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1",
		"-fflags", "+bitexact", "-flags:v", "+bitexact", "-flags:a", "+bitexact", "-video_track_timescale", "90000",
		"-metadata", "creation_time=", "-metadata", "encoder=", "-metadata:s:v", "encoder=", "-metadata:s:a", "encoder=",
		"-movflags", "+faststart", outputToken,
	}
}

func concatArgumentTemplate() []string {
	return []string{
		"-nostdin", "-hide_banner", "-v", "error", "-y",
		"-f", "concat", "-safe", "1", "-i", listToken,
		"-map", "0:v:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1",
		"-c", "copy", "-fflags", "+bitexact",
		"-metadata", "creation_time=", "-metadata", "encoder=", "-metadata:s:v", "encoder=", "-metadata:s:a", "encoder=",
		"-movflags", "+faststart", outputToken,
	}
}

func replaceTokens(template []string, values map[string]string) []string {
	arguments := make([]string, len(template))
	for index, value := range template {
		if replacement, ok := values[value]; ok {
			value = replacement
		}
		arguments[index] = value
	}
	return arguments
}
