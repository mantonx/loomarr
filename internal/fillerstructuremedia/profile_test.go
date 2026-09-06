package fillerstructuremedia

import (
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalProfilePinsCompleteAssessmentRecipe(t *testing.T) {
	profile := CanonicalProfile()
	if profile.SHA256 != "58b09cd2b54d235ac126675f381b8898462fb3461b3d95bdb0af42dd248ae73c" {
		t.Fatalf("canonical profile digest = %s", profile.SHA256)
	}
	if profile.SHA256 != ProfileSHA256(profile) || profile.SHA256 == "" ||
		profile.PartRecipeSHA256 == "" || profile.ConcatRecipeSHA256 == "" ||
		profile.MaximumVideoBytes != 64<<20 || profile.MaximumTimelineDriftMS != 1_000 {
		t.Fatalf("invalid canonical profile: %+v", profile)
	}
	if second := CanonicalProfile(); !reflect.DeepEqual(profile, second) {
		t.Fatalf("canonical profile drifted: first=%+v second=%+v", profile, second)
	}
	profile.Width++
	if profile.SHA256 == ProfileSHA256(profile) {
		t.Fatal("profile mutation retained its content identity")
	}
}

func TestPartArgumentsPinJoinCompatibleProfile(t *testing.T) {
	arguments := strings.Join(PartArguments("source.mp4", 1_000, 2_000, "part.mp4"), " ")
	for _, required := range []string{
		"-ss 1.000 -i source.mp4 -t 2.000", "fps=30:start_time=0", "scale=w=960:h=720:force_original_aspect_ratio=decrease",
		"pad=960:720", "-pix_fmt yuv420p", "-ar 48000", "-ac 2", "-video_track_timescale 90000",
		"-threads 1", "-fflags +bitexact", "creation_time=", "encoder=",
	} {
		if !strings.Contains(arguments, required) {
			t.Fatalf("structure part arguments omit %q: %s", required, arguments)
		}
	}
}

func TestConcatArgumentsPinMetadataFreeCopy(t *testing.T) {
	arguments := strings.Join(ConcatArguments("concat.txt", "result.mp4"), " ")
	for _, required := range []string{"-safe 1", "-map_metadata -1", "-map_chapters -1", "-c copy", "-fflags +bitexact", "creation_time=", "encoder="} {
		if !strings.Contains(arguments, required) {
			t.Fatalf("concat arguments omit %q: %s", required, arguments)
		}
	}
}

func TestProfileDigestChangesWithRecipe(t *testing.T) {
	profile := CanonicalProfile()
	if profile.PartRecipeSHA256 == recipeSHA256(append(partArgumentTemplate(), "-an")) {
		t.Fatal("recipe mutation retained its content identity")
	}
}
