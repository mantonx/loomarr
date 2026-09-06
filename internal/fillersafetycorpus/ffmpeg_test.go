//go:build !windows

package fillersafetycorpus

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/execfixture"
)

func TestFFmpegWrapperBindsRecipeToolsArgumentsAndCompleteDuration(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "ffmpeg-arguments")
	t.Setenv("LOOMARR_VCTK_FFMPEG_ARGUMENTS", arguments)
	ffmpeg := execfixture.POSIX(t, "ffmpeg", `
if [ "$1" = "-version" ]; then
  printf 'ffmpeg version 7.1.2\n'
  exit 0
fi
for destination do :; done
if [ "$destination" = "-" ]; then
  exit 0
fi
printf '%s\n' "$@" > "$LOOMARR_VCTK_FFMPEG_ARGUMENTS"
printf 'wrapped audiovisual fixture' > "$destination"
`)
	ffprobe := execfixture.POSIX(t, "ffprobe", `
if [ "$1" = "-version" ]; then
  printf 'ffprobe version 7.1.2\n'
  exit 0
fi
printf '%s\n' '{"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"format":{"duration":"2.3455"}}'
`)
	input := filepath.Join(t.TempDir(), "input.flac")
	if err := os.WriteFile(input, []byte("real speech"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "source.mp4")
	wrapper := &ffmpegWrapper{ffmpegPath: ffmpeg, ffprobePath: ffprobe}
	ffmpegIdentity, ffprobeIdentity, recipeSHA, err := wrapper.Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	media, err := wrapper.Wrap(t.Context(), input, output)
	if err != nil {
		t.Fatal(err)
	}
	if ffmpegIdentity.Version != "ffmpeg version 7.1.2" || ffprobeIdentity.Version != "ffprobe version 7.1.2" ||
		!validSHA256(ffmpegIdentity.BinarySHA256) || !validSHA256(ffprobeIdentity.BinarySHA256) ||
		recipeSHA != hashBytes([]byte(VCTKNeutralVideoRecipe)) || media.DurationMS != 2_346 ||
		media.SHA256 != bytesSHA([]byte("wrapped audiovisual fixture")) {
		t.Fatalf("ffmpeg=%+v ffprobe=%+v recipe=%s media=%+v", ffmpegIdentity, ffprobeIdentity, recipeSHA, media)
	}
	knownScriptWrapper := &ffmpegWrapper{
		ffmpegPath: ffmpeg, ffprobePath: ffprobe, recipe: KnownScriptPackagingRecipe,
	}
	_, _, knownScriptRecipeSHA, err := knownScriptWrapper.Identity(t.Context())
	if err != nil || knownScriptRecipeSHA != hashBytes([]byte(KnownScriptPackagingRecipe)) {
		t.Fatalf("known-script recipe=%s err=%v", knownScriptRecipeSHA, err)
	}
	raw, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(raw))
	for _, pair := range [][2]string{
		{"-f", "lavfi"}, {"-map", "0:v:0"}, {"-map", "1:a:0"}, {"-shortest", "-map_metadata"},
		{"-map_metadata", "-1"}, {"-threads", "1"}, {"-fflags", "+bitexact"}, {"-movflags", "+faststart"},
	} {
		if !adjacentArguments(args, pair[0], pair[1]) {
			t.Fatalf("missing adjacent ffmpeg arguments %q %q in %v", pair[0], pair[1], args)
		}
	}
	if !slices.Contains(args, "color=c=0x202830:s=640x360:r=30") || args[len(args)-1] != output {
		t.Fatalf("fixed carrier or destination missing from %v", args)
	}
}

func TestFFmpegWrapperRejectsDecodeFailure(t *testing.T) {
	ffmpeg := execfixture.POSIX(t, "ffmpeg", `
if [ "$1" = "-version" ]; then
  printf 'ffmpeg version 7.1.2\n'
  exit 0
fi
for destination do :; done
if [ "$destination" = "-" ]; then
  exit 9
fi
printf 'header-valid corrupt-tail fixture' > "$destination"
`)
	ffprobe := execfixture.POSIX(t, "ffprobe", `
if [ "$1" = "-version" ]; then
  printf 'ffprobe version 7.1.2\n'
  exit 0
fi
printf '%s\n' '{"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"format":{"duration":"2.0"}}'
`)
	input := filepath.Join(t.TempDir(), "input.flac")
	if err := os.WriteFile(input, []byte("real speech"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := &ffmpegWrapper{ffmpegPath: ffmpeg, ffprobePath: ffprobe}
	if _, _, _, err := wrapper.Identity(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapper.Wrap(t.Context(), input, filepath.Join(t.TempDir(), "source.mp4")); err == nil ||
		!strings.Contains(err.Error(), "fully decode") {
		t.Fatalf("err=%v", err)
	}
}

func TestProbeCompleteAVRejectsMissingModalityAndUnknownFields(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{name: "audio only", payload: `{"streams":[{"codec_type":"audio"}],"format":{"duration":"2.0"}}`},
		{name: "unknown field", payload: `{"streams":[{"codec_type":"video"},{"codec_type":"audio"}],"format":{"duration":"2.0"},"invented":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := execfixture.POSIX(t, "ffprobe", "printf '%s\\n' '"+test.payload+"'")
			if _, err := probeCompleteAV(t.Context(), probe, "ignored.mp4"); err == nil {
				t.Fatal("expected complete-AV probe rejection")
			}
		})
	}
}

func TestPrepareVCTKRejectsSymlinkedMember(t *testing.T) {
	fixture := newVCTKFixture(t)
	path := filepath.Join(fixture.root, filepath.FromSlash(fixture.authority.Members[0].Audio.Path))
	realPath := path + ".real"
	if err := os.Rename(path, realPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(realPath), path); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareVCTK(t.Context(), fixture.config, fixture.wrapper); err == nil || !strings.Contains(err.Error(), "audio bytes") {
		t.Fatalf("err=%v", err)
	}
	if fixture.media.wraps.Calls() != 0 {
		t.Fatalf("wrapper calls=%d", fixture.media.wraps.Calls())
	}
}

func adjacentArguments(arguments []string, first, second string) bool {
	for index := 1; index < len(arguments); index++ {
		if arguments[index-1] == first && arguments[index] == second {
			return true
		}
	}
	return false
}
