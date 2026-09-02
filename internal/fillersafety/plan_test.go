package fillersafety

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

func TestPlanCompleteMediaBindsPathIndependentAuthorityToFullSpans(t *testing.T) {
	t.Parallel()
	authority := validSourceAuthority()
	firstPath := filepath.Join(t.TempDir(), "first.mp4")
	secondPath := filepath.Join(t.TempDir(), "second.mp4")
	first, err := PlanCompleteMedia(SourceRequest{Authority: authority, Path: firstPath})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanCompleteMedia(SourceRequest{Authority: authority, Path: secondPath})
	if err != nil {
		t.Fatal(err)
	}
	wantSpan := Span{StartMS: 0, EndMS: authority.DurationMS}
	if first.AuthoritySHA256 == "" || first.AuthoritySHA256 != second.AuthoritySHA256 || first.SourcePath != firstPath || first.SourceSHA256 != authority.SourceSHA256 || first.SourceBytes != authority.SourceBytes || first.Audio != wantSpan || first.Video != wantSpan {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	public, err := json.Marshal(first)
	if err != nil || strings.Contains(string(public), firstPath) || strings.Contains(string(public), authority.SourceID) {
		t.Fatalf("public plan leaked private authority: %s err=%v", public, err)
	}
}

func TestPlanCompleteMediaRejectsInvalidAuthorityWithoutLeakingValues(t *testing.T) {
	t.Parallel()
	secret := "private-source-name"
	base := validSourceAuthority()
	base.SourceID = secret
	validPath := filepath.Join(t.TempDir(), secret+".mp4")
	tests := []struct {
		name   string
		mutate func(*SourceAuthority, *string)
		code   AuthorityCode
	}{
		{name: "schema", mutate: func(authority *SourceAuthority, _ *string) { authority.SchemaVersion++ }, code: AuthoritySchemaInvalid},
		{name: "certification", mutate: func(authority *SourceAuthority, _ *string) { authority.CertificationSHA256 = "invalid" }, code: AuthorityIdentityInvalid},
		{name: "policy", mutate: func(authority *SourceAuthority, _ *string) { authority.PolicySHA256 = "invalid" }, code: AuthorityIdentityInvalid},
		{name: "implementation", mutate: func(authority *SourceAuthority, _ *string) { authority.Implementation = "" }, code: AuthorityIdentityInvalid},
		{name: "source id", mutate: func(authority *SourceAuthority, _ *string) { authority.SourceID = "" }, code: AuthorityIdentityInvalid},
		{name: "source digest", mutate: func(authority *SourceAuthority, _ *string) { authority.SourceSHA256 = "invalid" }, code: AuthoritySourceInvalid},
		{name: "source bytes", mutate: func(authority *SourceAuthority, _ *string) { authority.SourceBytes = 0 }, code: AuthoritySourceInvalid},
		{name: "source ceiling", mutate: func(authority *SourceAuthority, _ *string) {
			authority.SourceBytes = mediatools.ConditioningMaxSnapshotBytes + 1
		}, code: AuthoritySourceInvalid},
		{name: "duration", mutate: func(authority *SourceAuthority, _ *string) { authority.DurationMS = 0 }, code: AuthoritySourceInvalid},
		{name: "measurement time", mutate: func(authority *SourceAuthority, _ *string) { authority.MeasuredAt = time.Time{} }, code: AuthoritySourceInvalid},
		{name: "audio coverage", mutate: func(authority *SourceAuthority, _ *string) { authority.HasAudio = false }, code: AuthorityCoverageMissing},
		{name: "video coverage", mutate: func(authority *SourceAuthority, _ *string) { authority.HasVideo = false }, code: AuthorityCoverageMissing},
		{name: "ffmpeg", mutate: func(authority *SourceAuthority, _ *string) { authority.FFmpeg.BinarySHA256 = "invalid" }, code: AuthorityIdentityInvalid},
		{name: "ffprobe", mutate: func(authority *SourceAuthority, _ *string) { authority.FFprobe.Version = "" }, code: AuthorityIdentityInvalid},
		{name: "relative path", mutate: func(_ *SourceAuthority, path *string) { *path = secret + ".mp4" }, code: AuthoritySourceInvalid},
		{name: "unclean path", mutate: func(_ *SourceAuthority, path *string) {
			*path = filepath.Dir(*path) + string(filepath.Separator) + "child" + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(*path)
		}, code: AuthoritySourceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authority, path := base, validPath
			test.mutate(&authority, &path)
			_, err := PlanCompleteMedia(SourceRequest{Authority: authority, Path: path})
			var authorityErr *AuthorityError
			if !errors.As(err, &authorityErr) || authorityErr.Code != test.code || strings.Contains(err.Error(), secret) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func validSourceAuthority() SourceAuthority {
	return SourceAuthority{
		SchemaVersion:       SourceAuthoritySchemaVersion,
		CertificationSHA256: strings.Repeat("a", 64),
		PolicySHA256:        strings.Repeat("b", 64),
		Implementation:      "filler-spoken-safety-v1",
		SourceID:            "source-one",
		SourceSHA256:        strings.Repeat("c", 64),
		SourceBytes:         1024,
		DurationMS:          30_000,
		HasAudio:            true,
		HasVideo:            true,
		MeasuredAt:          time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
		FFmpeg:              ToolIdentity{Version: "ffmpeg-8", BinarySHA256: strings.Repeat("d", 64)},
		FFprobe:             ToolIdentity{Version: "ffprobe-8", BinarySHA256: strings.Repeat("e", 64)},
	}
}
