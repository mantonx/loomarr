package filler

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/mediatools"
)

type transcodeStore struct {
	oldHash       string
	clip          StoreClip
	err           error
	clips         map[string]StoreClip
	beforeReplace func(string, StoreClip)
}

type cleanupSyncSource struct{ dir string }

func (s cleanupSyncSource) EnsureLocalSource(context.Context, string) error { return nil }
func (s cleanupSyncSource) ListLocalClips(ctx context.Context) ([]RawClip, error) {
	clips, _, err := ScanDir(ctx, s.dir, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	})
	return clips, err
}

type cleanupSyncStore struct{ clips map[string]StoreClip }

func (s *cleanupSyncStore) UpsertClip(_ context.Context, clip StoreClip) error {
	s.clips[clip.Hash] = clip
	return nil
}
func (s *cleanupSyncStore) GetClip(_ context.Context, id string) (StoreClip, bool, error) {
	clip, ok := s.clips[id]
	return clip, ok, nil
}
func (s *cleanupSyncStore) DeleteClipsNotIn(context.Context, []string) (int, error) { return 0, nil }

func (s *transcodeStore) ReplaceClipIdentity(_ context.Context, oldHash string, c StoreClip) error {
	if s.beforeReplace != nil {
		s.beforeReplace(oldHash, c)
	}
	s.oldHash, s.clip = oldHash, c
	err := s.err
	s.err = nil
	return err
}

func (s *transcodeStore) CommitConditioningPublication(_ context.Context, publication ConditioningPublication, c StoreClip) error {
	return s.ReplaceClipIdentity(context.Background(), publication.SourceHash, c)
}

func TestTranscodeStage_ConcurrentSyncHoldsPublishedConditionedTargetUntilCatalogRekey(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("retained composite"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	sourceHash := writeContentAddressedClip(t, dir, []byte("reviewed child source"), ".mkv")
	sourceRel := filepath.ToSlash(ClipRelPath(sourceHash, ".mkv"))
	sourceFull := filepath.Join(dir, filepath.FromSlash(sourceRel))
	if err := WriteSidecarTags(sourceFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: sourceHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}

	before := completeConditioningMeasurement(-28.4)
	after := completeConditioningMeasurement(-23.1)
	for i := range after.Cuts[0].Streams {
		after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
		after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
	}
	published := make(chan StoreClip, 1)
	resumeRekey := make(chan struct{})
	stageStore := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stageStore.beforeReplace = func(_ string, target StoreClip) {
		published <- target
		<-resumeRekey
	}
	stage := NewTranscodeStage(stageStore, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return after.Quality, os.WriteFile(req.Out, []byte("conditioned target bytes"), 0o600)
	}
	measurements := 0
	stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		measurements++
		if measurements == 1 {
			return before, nil
		}
		return after, nil
	})

	type runResult struct {
		result StageResult
		err    error
	}
	runDone := make(chan runResult, 1)
	go func() {
		result, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
			Hash: sourceHash, Path: sourceRel, Name: "Reviewed child", Kind: Commercial, ParentHash: parentHash,
		}})
		runDone <- runResult{result: result, err: err}
	}()
	target := <-published

	syncStore := &cleanupSyncStore{clips: map[string]StoreClip{
		sourceHash: {Clip: Clip{Hash: sourceHash, Path: sourceRel, Name: "Reviewed child", Kind: Commercial, ParentHash: parentHash}},
		parentHash: {Clip: Clip{Hash: parentHash, Path: parentRel, Name: "Composite", IsComposite: true}},
	}}
	layout, err := NewLayout(dir, "")
	if err != nil {
		close(resumeRekey)
		<-runDone
		t.Fatal(err)
	}
	_, syncErr := NewSyncer(cleanupSyncSource{dir}, syncStore, layout, time.Now, nil).Sync(context.Background())
	observed, found, getErr := syncStore.GetClip(context.Background(), target.Hash)
	close(resumeRekey)
	run := <-runDone
	if syncErr != nil || getErr != nil || !found {
		t.Fatalf("concurrent Sync target = %+v, found=%v, syncErr=%v, getErr=%v", observed, found, syncErr, getErr)
	}
	if !observed.Held || observed.ParentHash != parentHash {
		t.Fatalf("concurrent Sync published target = %+v; want owner-bound conditioning hold", observed)
	}
	if run.err != nil || run.result.Verdict != VerdictContinue {
		t.Fatalf("transcode result = %+v, err=%v", run.result, run.err)
	}
}

func (s *transcodeStore) GetClip(_ context.Context, hash string) (StoreClip, bool, error) {
	c, ok := s.clips[hash]
	return c, ok, nil
}

func completeConditioningMeasurement(lufs float64) mediatools.ConditioningMeasurement {
	available := func(ms int64) mediatools.OptionalMilliseconds {
		return mediatools.OptionalMilliseconds{Milliseconds: ms, Available: true}
	}
	return mediatools.ConditioningMeasurement{
		ContainerDurationMs: 30_000,
		Streams: []mediatools.ConditioningStream{
			{Kind: mediatools.StreamVideo, Index: 0, Start: available(0), Duration: available(30_000),
				Cadence: &mediatools.Rational{Numerator: 30_000, Denominator: 1001}},
			{Kind: mediatools.StreamAudio, Index: 1, Start: available(120), Duration: available(29_880)},
		},
		AVSkew: mediatools.ConditioningSkew{Start: available(120), End: available(0)},
		Loudness: mediatools.ConditioningLoudness{
			IntegratedLUFS: lufs, Available: true,
			TruePeak: mediatools.ConditioningTruePeak{State: mediatools.TruePeakFinite, DBTP: -2.1},
		},
		Quality: MediaQuality{
			EvidenceVersion: mediatools.MediaQualityEvidenceV1,
			Provenance:      mediatools.MediaQualityProvenanceFFmpegDetectors,
			DurationMs:      30_000,
		},
		Cuts: []mediatools.ConditioningCutMeasurement{{Intended: mediatools.Interval{StartMs: 1_000, EndMs: 31_000}, Streams: []mediatools.ConditioningCutStream{
			{Kind: mediatools.StreamVideo, Index: 0, StartError: available(3), EndError: available(-4)},
			{Kind: mediatools.StreamAudio, Index: 1, StartError: available(5), EndError: available(-2)},
		}}},
	}
}

func TestTranscodeStage_UnavailableOrMalformedConditioningHoldsChildWithoutTranscoding(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("parent"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	childBytes := []byte("child source bytes")
	childHash := writeContentAddressedClip(t, dir, childBytes, ".mkv")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
	childFull := filepath.Join(dir, filepath.FromSlash(childRel))
	if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error) {
		t.Fatal("unavailable evidence must stop before transcode")
		return MediaQuality{}, nil
	}
	cases := []struct {
		name   string
		mutate func(*mediatools.ConditioningMeasurement)
	}{
		{name: "unavailable loudness", mutate: func(m *mediatools.ConditioningMeasurement) {
			m.Loudness.Available = false
		}},
		{name: "non-finite loudness", mutate: func(m *mediatools.ConditioningMeasurement) {
			m.Loudness.IntegratedLUFS = math.NaN()
		}},
		{name: "unknown stream kind", mutate: func(m *mediatools.ConditioningMeasurement) {
			m.Streams[0].Kind = mediatools.StreamKind("subtitle")
		}},
		{name: "duplicate stream", mutate: func(m *mediatools.ConditioningMeasurement) {
			m.Streams = append(m.Streams, m.Streams[0])
		}},
		{name: "unavailable cut edge", mutate: func(m *mediatools.ConditioningMeasurement) {
			m.Cuts[0].Streams[0].EndError.Available = false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			measurement := completeConditioningMeasurement(-23)
			tc.mutate(&measurement)
			stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
				return measurement, nil
			})
			out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
				Hash: childHash, Path: childRel, Name: "Child", Kind: Commercial, ParentHash: parentHash,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if out.Verdict != VerdictReview || out.Clip.Hash != childHash || stored.oldHash != "" {
				t.Fatalf("invalid evidence result = %+v, stored replacement = %q", out, stored.oldHash)
			}
			got, err := os.ReadFile(childFull)
			if err != nil || string(got) != string(childBytes) {
				t.Fatalf("source bytes changed: %q, %v", got, err)
			}
		})
	}
}

func TestTranscodeStage_HoldsWhenConditioningSourceOrParentBytesDoNotMatchCatalogIdentity(t *testing.T) {
	for _, target := range []string{"child", "parent"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			parentHash := writeContentAddressedClip(t, dir, []byte("retained parent"), ".mp4")
			parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
			parentFull := filepath.Join(dir, filepath.FromSlash(parentRel))
			childHash := writeContentAddressedClip(t, dir, []byte("reviewed child"), ".mkv")
			childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
			childFull := filepath.Join(dir, filepath.FromSlash(childRel))
			if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
				ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
			}}, false); err != nil {
				t.Fatal(err)
			}
			if target == "child" {
				if err := os.WriteFile(childFull, []byte("replaced child bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(parentFull, []byte("replaced parent bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
				Hash: parentHash, Path: parentRel, IsComposite: true,
			}}}}
			stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
				return Probed{DurationMs: 30_000, Height: 480}, nil
			}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
			stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
				t.Fatal("identity mismatch must stop before conditioning measurement")
				return mediatools.ConditioningMeasurement{}, nil
			})
			stage.transcode = func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error) {
				t.Fatal("identity mismatch must stop before transcode")
				return MediaQuality{}, nil
			}

			out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
				Hash: childHash, Path: childRel, ParentHash: parentHash,
			}})
			if err != nil || out.Verdict != VerdictReview || stored.oldHash != "" {
				t.Fatalf("identity mismatch result = %+v, stored=%q, err=%v", out, stored.oldHash, err)
			}
		})
	}
}

func TestTranscodeStage_PathReplacementDuringConditioningPublishesNothing(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("retained parent"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	childHash := writeContentAddressedClip(t, dir, []byte("reviewed child"), ".mkv")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
	childFull := filepath.Join(dir, filepath.FromSlash(childRel))
	if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	measurements := 0
	stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		measurements++
		if measurements == 1 {
			if err := os.WriteFile(childFull, []byte("replacement during measurement"), 0o600); err != nil {
				return mediatools.ConditioningMeasurement{}, err
			}
		}
		measurement := completeConditioningMeasurement(-23)
		if measurements > 1 {
			for i := range measurement.Cuts[0].Streams {
				measurement.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
				measurement.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
			}
		}
		return measurement, nil
	})
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		measurement := completeConditioningMeasurement(-23)
		return measurement.Quality, os.WriteFile(req.Out, []byte("would-be output"), 0o600)
	}

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, ParentHash: parentHash,
	}})
	if err != nil || out.Verdict != VerdictReview || stored.oldHash != "" {
		t.Fatalf("path replacement result = %+v, stored=%q, err=%v", out, stored.oldHash, err)
	}
}

func TestTranscodeStage_SparseIdentityCollisionCannotHideSourceOrParentReplacement(t *testing.T) {
	for _, target := range []string{"child", "parent"} {
		t.Run(target, func(t *testing.T) {
			childOriginal, childReplacement := sparseIdentityCollision()
			parentOriginal, parentReplacement := sparseIdentityCollision()
			parentOriginal[0], parentReplacement[0] = 'p', 'p'
			dir := t.TempDir()
			parentHash := writeContentAddressedClip(t, dir, parentOriginal, ".mp4")
			parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
			parentFull := filepath.Join(dir, filepath.FromSlash(parentRel))
			childHash := writeContentAddressedClip(t, dir, childOriginal, ".mkv")
			childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
			childFull := filepath.Join(dir, filepath.FromSlash(childRel))
			if parentHash == childHash {
				t.Fatal("parent and child fixtures must have distinct catalog identities")
			}
			if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
				ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
			}}, false); err != nil {
				t.Fatal(err)
			}
			stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
				Hash: parentHash, Path: parentRel, IsComposite: true,
			}}}}
			stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
				return Probed{DurationMs: 30_000, Height: 480}, nil
			}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
			measurements := 0
			stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
				measurements++
				if measurements == 1 {
					path, replacement := childFull, childReplacement
					if target == "parent" {
						path = parentFull
						replacement = parentReplacement
					}
					if err := os.WriteFile(path, replacement, 0o600); err != nil {
						return mediatools.ConditioningMeasurement{}, err
					}
				}
				measurement := completeConditioningMeasurement(-23)
				if measurements > 1 {
					for i := range measurement.Cuts[0].Streams {
						measurement.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
						measurement.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
					}
				}
				return measurement, nil
			})
			stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
				measurement := completeConditioningMeasurement(-23)
				return measurement.Quality, os.WriteFile(req.Out, []byte("would-be output"), 0o600)
			}

			out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
				Hash: childHash, Path: childRel, ParentHash: parentHash,
			}})
			if err != nil || out.Verdict != VerdictReview || stored.oldHash != "" {
				t.Fatalf("sparse %s replacement result = %+v, stored=%q, err=%v", target, out, stored.oldHash, err)
			}
			if _, err := os.Stat(childFull); err != nil {
				t.Fatalf("source was not preserved: %v", err)
			}
		})
	}
}

func TestTranscodeStage_RestartReusesCompleteConditioningWithoutRewrite(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("parent"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	childHash := writeContentAddressedClip(t, dir, []byte("completed child mezzanine"), ".mp4")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mp4"))
	measurement := completeConditioningMeasurement(-23)
	if err := WriteSidecarTags(filepath.Join(dir, filepath.FromSlash(childRel)), SidecarTags{
		OriginalName: "Completed child", Mezzanine: mediatools.DefaultMezzanine().ID(),
		MediaQuality: &measurement.Quality,
		ConditioningLineage: &ConditioningLineage{
			ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
		},
		Conditioning: &ConditioningEvidence{
			BeforeRewriteHash: childHash, AfterRewriteHash: childHash,
			BeforeRewrite: measurement, AfterRewrite: measurement,
			DerivedParentEdgesAfterRewrite: measurement.Cuts[0],
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		t.Fatal("complete restart evidence must avoid another probe")
		return Probed{}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error) {
		t.Fatal("complete restart evidence must avoid another transcode")
		return MediaQuality{}, nil
	}
	stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		t.Fatal("complete restart evidence must avoid another measurement")
		return mediatools.ConditioningMeasurement{}, nil
	})

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, Name: "Completed child", Kind: Commercial, ParentHash: parentHash,
	}})
	if err != nil || out.Verdict != VerdictContinue || out.Clip.Hash != childHash {
		t.Fatalf("restart result = %+v, %v", out, err)
	}
}

func TestTranscodeStage_RestartHoldsMalformedOrUnboundConditioningEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ConditioningLineage, *ConditioningEvidence, *MediaQuality, *StoreClip)
	}{
		{name: "empty intended interval", mutate: func(lineage *ConditioningLineage, _ *ConditioningEvidence, _ *MediaQuality, _ *StoreClip) {
			lineage.IntendedEndMs = lineage.IntendedStartMs
		}},
		{name: "invalid structure decision identity", mutate: func(lineage *ConditioningLineage, _ *ConditioningEvidence, _ *MediaQuality, _ *StoreClip) {
			lineage.StructureDecisionSHA256 = "not-a-digest"
		}},
		{name: "parent is airable", mutate: func(_ *ConditioningLineage, _ *ConditioningEvidence, _ *MediaQuality, parent *StoreClip) {
			parent.IsComposite = false
		}},
		{name: "media quality provenance missing", mutate: func(_ *ConditioningLineage, _ *ConditioningEvidence, quality *MediaQuality, _ *StoreClip) {
			quality.Provenance = ""
		}},
		{name: "detector intervals are not normalized", mutate: func(_ *ConditioningLineage, evidence *ConditioningEvidence, _ *MediaQuality, _ *StoreClip) {
			evidence.BeforeRewrite.Quality.Black = []Interval{{StartMs: 2_000, EndMs: 3_000}, {StartMs: 1_000, EndMs: 1_500}}
		}},
		{name: "stream index is reused across kinds", mutate: func(_ *ConditioningLineage, evidence *ConditioningEvidence, _ *MediaQuality, _ *StoreClip) {
			evidence.BeforeRewrite.Streams[1].Index = 0
			evidence.BeforeRewrite.Cuts[0].Streams[1].Index = 0
		}},
		{name: "before after identities differ", mutate: func(_ *ConditioningLineage, evidence *ConditioningEvidence, _ *MediaQuality, _ *StoreClip) {
			evidence.AfterRewrite.Streams[1].Index = 2
			evidence.AfterRewrite.Cuts[0].Streams[1].Index = 2
		}},
		{name: "before and after agree on interval different from lineage", mutate: func(lineage *ConditioningLineage, evidence *ConditioningEvidence, _ *MediaQuality, _ *StoreClip) {
			lineage.IntendedStartMs, lineage.IntendedEndMs = 2_000, 32_000
			evidence.BeforeRewrite.Cuts[0].Intended = Interval{StartMs: 1_000, EndMs: 31_000}
			evidence.AfterRewrite.Cuts[0].Intended = Interval{StartMs: 1_000, EndMs: 31_000}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			parentHash := writeContentAddressedClip(t, dir, []byte("retained parent"), ".mp4")
			parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
			childHash := writeContentAddressedClip(t, dir, []byte("completed child"), ".mp4")
			childRel := filepath.ToSlash(ClipRelPath(childHash, ".mp4"))
			lineage := ConditioningLineage{ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000}
			before := completeConditioningMeasurement(-28.4)
			after := completeConditioningMeasurement(-23.1)
			for i := range after.Cuts[0].Streams {
				after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
				after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
			}
			evidence := ConditioningEvidence{
				BeforeRewriteHash: childHash, AfterRewriteHash: childHash,
				BeforeRewrite: before, AfterRewrite: after,
				DerivedParentEdgesAfterRewrite: before.Cuts[0],
			}
			quality := after.Quality
			parent := StoreClip{Clip: Clip{Hash: parentHash, Path: parentRel, IsComposite: true}}
			tc.mutate(&lineage, &evidence, &quality, &parent)
			if err := WriteSidecarTags(filepath.Join(dir, filepath.FromSlash(childRel)), SidecarTags{
				Mezzanine: mediatools.DefaultMezzanine().ID(), NormalizedLUFS: -23,
				MediaQuality: &quality, ConditioningLineage: &lineage, Conditioning: &evidence,
			}, false); err != nil {
				t.Fatal(err)
			}
			stored := &transcodeStore{clips: map[string]StoreClip{parentHash: parent}}
			stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
				t.Fatal("invalid restart evidence must hold before probing")
				return Probed{}, nil
			}, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now)
			stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
				t.Fatal("invalid restart evidence must hold before measuring")
				return mediatools.ConditioningMeasurement{}, nil
			})
			out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
				Hash: childHash, Path: childRel, ParentHash: parentHash,
			}})
			if err != nil || out.Verdict != VerdictReview || stored.oldHash != "" {
				t.Fatalf("invalid restart result = %+v, stored=%q, err=%v", out, stored.oldHash, err)
			}
		})
	}
}

func TestReadConditioningSidecar_ReturnsRetryableErrorWhenPathBecomesDirectory(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "child.mp4")
	if err := os.WriteFile(media, []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteSidecarTags(media, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: "child", ParentHash: "parent", IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	if _, present, err := readConditioningSidecar(media); err != nil || !present {
		t.Fatalf("valid conditioning sidecar = present %v, err %v", present, err)
	}
	path := sidecarPathFor(media)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, present, err := readConditioningSidecar(media); err == nil || present {
		t.Fatalf("directory conditioning sidecar = present %v, err %v; want retryable error", present, err)
	}
}

func TestTranscodeStage_NormalizedMarkerAloneCannotCertifyAChildRestart(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("retained parent"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	childHash := writeContentAddressedClip(t, dir, []byte("marker-only child"), ".mp4")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mp4"))
	if err := WriteSidecarTags(filepath.Join(dir, filepath.FromSlash(childRel)), SidecarTags{
		Mezzanine: mediatools.DefaultMezzanine().ID(), NormalizedLUFS: -23,
		MediaQuality: &MediaQuality{
			EvidenceVersion: mediatools.MediaQualityEvidenceV1,
			Provenance:      mediatools.MediaQualityProvenanceFFmpegDetectors,
			DurationMs:      30_000,
		},
		ConditioningLineage: &ConditioningLineage{
			ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	stage := NewTranscodeStage(&transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}, nil, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now)
	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, ParentHash: parentHash,
	}})
	if err != nil || out.Verdict != VerdictReview {
		t.Fatalf("marker-only restart = %+v, %v; want review", out, err)
	}
}

func TestTranscodeStage_PostMeasurementFailurePublishesNothing(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("parent"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	childBytes := []byte("immutable child")
	childHash := writeContentAddressedClip(t, dir, childBytes, ".mkv")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
	childFull := filepath.Join(dir, filepath.FromSlash(childRel))
	if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{DurationMs: 30_000}, os.WriteFile(req.Out, []byte("unpublished mezzanine"), 0o644)
	}
	measurements := 0
	stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		measurements++
		if measurements == 2 {
			return mediatools.ConditioningMeasurement{}, errors.New("post measurement failed")
		}
		return completeConditioningMeasurement(-27), nil
	})

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, Name: "Child", Kind: Commercial, ParentHash: parentHash,
	}})
	if err != nil || out.Verdict != VerdictReview || stored.oldHash != "" {
		t.Fatalf("post-measurement result = %+v, stored=%q, err=%v", out, stored.oldHash, err)
	}
	got, readErr := os.ReadFile(childFull)
	if readErr != nil || string(got) != string(childBytes) {
		t.Fatalf("source bytes changed: %q, %v", got, readErr)
	}
	entries, readDirErr := os.ReadDir(filepath.Join(dir, transcodeStagingDir))
	if readDirErr != nil || len(entries) != 0 {
		t.Fatalf("staging after failed measurement = %+v, %v", entries, readDirErr)
	}
}

func TestTranscodeStage_MissingParentHoldsBeforeMeasurement(t *testing.T) {
	dir := t.TempDir()
	childHash := writeContentAddressedClip(t, dir, []byte("child"), ".mkv")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
	if err := WriteSidecarTags(filepath.Join(dir, filepath.FromSlash(childRel)), SidecarTags{
		ConditioningLineage: &ConditioningLineage{
			ChildHash: childHash, ParentHash: "missing-parent", IntendedStartMs: 1_000, IntendedEndMs: 31_000,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	stage := NewTranscodeStage(&transcodeStore{}, nil, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		t.Fatal("missing parent must stop before measurement")
		return mediatools.ConditioningMeasurement{}, nil
	})
	stage.transcode = func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error) {
		t.Fatal("missing parent must stop before transcode")
		return MediaQuality{}, nil
	}
	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, Name: "Child", Kind: Commercial, ParentHash: "missing-parent",
	}})
	if err != nil || out.Verdict != VerdictReview || out.Clip.Hash != childHash {
		t.Fatalf("missing parent result = %+v, %v", out, err)
	}
}

func TestTranscodeStage_CancellationHoldsConditionedChildBeforePublish(t *testing.T) {
	for _, phase := range []string{"conditioning", "transcode"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			parentHash := writeContentAddressedClip(t, dir, []byte("parent"), ".mp4")
			parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
			childHash := writeContentAddressedClip(t, dir, []byte("child"), ".mkv")
			childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
			childFull := filepath.Join(dir, filepath.FromSlash(childRel))
			if err := WriteSidecarTags(childFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
				ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
			}}, false); err != nil {
				t.Fatal(err)
			}
			beforeMedia, err := os.ReadFile(childFull)
			if err != nil {
				t.Fatal(err)
			}
			beforeSidecar, err := os.ReadFile(sidecarPathFor(childFull))
			if err != nil {
				t.Fatal(err)
			}
			stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
				Hash: parentHash, Path: parentRel, IsComposite: true,
			}}}}
			stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
				return Probed{DurationMs: 30_000, Height: 480}, nil
			}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
			started := make(chan struct{})
			observedCancellation := make(chan error, 1)
			measurements := 0
			stage.WithConditioning(func(ctx context.Context, _ mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
				measurements++
				measurement := completeConditioningMeasurement(-23)
				if measurements > 1 {
					for i := range measurement.Cuts[0].Streams {
						measurement.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
						measurement.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
					}
				}
				if phase == "conditioning" && measurements == 1 {
					close(started)
					<-ctx.Done()
					observedCancellation <- ctx.Err()
				}
				return measurement, nil
			})
			stage.transcode = func(ctx context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
				if phase == "transcode" {
					close(started)
					<-ctx.Done()
					observedCancellation <- ctx.Err()
				}
				measurement := completeConditioningMeasurement(-23)
				return measurement.Quality, os.WriteFile(req.Out, []byte("hidden staged output"), 0o600)
			}
			removed := false
			stage.removeSource = func(path string) error {
				removed = true
				return os.Remove(path)
			}

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan struct {
				out StageResult
				err error
			}, 1)
			go func() {
				out, runErr := stage.Run(ctx, StoreClip{Clip: Clip{
					Hash: childHash, Path: childRel, Name: "Child", Kind: Commercial, ParentHash: parentHash,
				}})
				result <- struct {
					out StageResult
					err error
				}{out: out, err: runErr}
			}()
			<-started
			cancel()
			got := <-result
			if observed := <-observedCancellation; !errors.Is(observed, context.Canceled) {
				t.Fatalf("boundary observed %v, want context cancellation identity", observed)
			}
			if got.err != nil || got.out.Verdict != VerdictReview || stored.oldHash != "" || removed {
				t.Fatalf("%s cancellation result = %+v, stored=%q removed=%v err=%v", phase, got.out, stored.oldHash, removed, got.err)
			}
			afterMedia, mediaErr := os.ReadFile(childFull)
			afterSidecar, sidecarErr := os.ReadFile(sidecarPathFor(childFull))
			if mediaErr != nil || sidecarErr != nil || !bytes.Equal(afterMedia, beforeMedia) || !bytes.Equal(afterSidecar, beforeSidecar) {
				t.Fatalf("%s cancellation mutated source media or sidecar", phase)
			}
		})
	}
}

func TestTranscodeStage_PersistsConditioningBeforeAndAfterWithRekeyedLineage(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("immutable parent compilation"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	childHash := writeContentAddressedClip(t, dir, []byte("reviewed stream-copy child"), ".mkv")
	childRel := filepath.ToSlash(ClipRelPath(childHash, ".mkv"))
	lineage := &ConditioningLineage{ChildHash: childHash, ParentHash: parentHash, IntendedStartMs: 10_000, IntendedEndMs: 40_000}
	if err := WriteSidecarTags(filepath.Join(dir, filepath.FromSlash(childRel)), SidecarTags{
		OriginalName: "Reviewed advert", ConditioningLineage: lineage,
	}, false); err != nil {
		t.Fatal(err)
	}

	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, Name: "Compilation", IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	before := completeConditioningMeasurement(-28.4)
	after := completeConditioningMeasurement(-23.1)
	before.Cuts[0].Intended = mediatools.Interval{StartMs: 10_000, EndMs: 40_000}
	after.Cuts[0].Intended = before.Cuts[0].Intended
	for i := range after.Cuts[0].Streams {
		after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
		after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
	}
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return after.Quality, os.WriteFile(req.Out, []byte("measured mezzanine"), 0o644)
	}
	measurements := 0
	stage.WithConditioning(func(_ context.Context, req mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		measurements++
		if measurements%2 == 1 {
			return before, nil
		}
		return after, nil
	})

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: childHash, Path: childRel, Name: "Reviewed advert", Kind: Commercial,
		ParentHash: parentHash,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != VerdictContinue || out.Clip.Hash == childHash || out.Clip.ParentHash != parentHash {
		t.Fatalf("transcode result = %+v", out)
	}
	tags, ok := ReadSidecarTags(filepath.Join(dir, filepath.FromSlash(out.Clip.Path)))
	if !ok || tags.Conditioning == nil {
		t.Fatalf("replacement sidecar conditioning = %+v, ok=%v", tags.Conditioning, ok)
	}
	if !reflect.DeepEqual(tags.ConditioningLineage, lineage) ||
		!reflect.DeepEqual(tags.Conditioning.BeforeRewrite, before) ||
		!reflect.DeepEqual(tags.Conditioning.AfterRewrite, after) ||
		!reflect.DeepEqual(tags.Conditioning.DerivedParentEdgesAfterRewrite, before.Cuts[0]) {
		t.Fatalf("replacement evidence = lineage %+v conditioning %+v", tags.ConditioningLineage, tags.Conditioning)
	}
	for _, edge := range tags.Conditioning.AfterRewrite.Cuts[0].Streams {
		if edge.StartError.Available || edge.EndError.Available {
			t.Fatalf("raw post-rewrite packet edges were overwritten by derived evidence: %+v", edge)
		}
	}
	raw, err := os.ReadFile(sidecarPathFor(filepath.Join(dir, filepath.FromSlash(out.Clip.Path))))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"derivedParentEdgesAfterRewrite"`)) ||
		bytes.Contains(raw, []byte(`"parentEdgesAfterRewrite"`)) {
		t.Fatalf("conditioning sidecar must name derived provenance explicitly: %s", raw)
	}
}

func TestTranscodeStage_RecoversAPublishedPairAfterStoreFailure(t *testing.T) {
	dir := t.TempDir()
	oldHash := writeContentAddressedClip(t, dir, []byte("original"), ".mkv")
	oldRel := filepath.ToSlash(ClipRelPath(oldHash, ".mkv"))
	stored := &transcodeStore{err: errors.New("commit failed")}
	probe := func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{}, os.WriteFile(req.Out, []byte("replacement"), 0o644)
	}
	clip := StoreClip{Clip: Clip{Hash: oldHash, Path: oldRel, Name: "Named advert", Kind: Commercial}}

	if _, err := stage.Run(context.Background(), clip); err == nil {
		t.Fatal("first run succeeded despite the simulated store failure")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(oldRel))); err != nil {
		t.Fatalf("store failure removed the original: %v", err)
	}
	// The verified replacement is deliberately retained. The retry recognizes its marker and
	// finishes the database re-key instead of rejecting the clip as a collision forever.
	out, err := stage.Run(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}
	if out.Clip.Hash == oldHash || stored.clip.Hash != out.Clip.Hash {
		t.Errorf("retry did not complete replacement: %+v / %+v", out.Clip, stored.clip)
	}
}

func TestTranscodeStage_ExistingConditionedOutputMustMatchTheCurrentChildEvidence(t *testing.T) {
	cases := []struct {
		name    string
		missing bool
		corrupt bool
		sparse  bool
		mutate  func(string, *SidecarTags) []byte
	}{
		{name: "missing sidecar", missing: true, mutate: func(_ string, _ *SidecarTags) []byte { return nil }},
		{name: "top-level artifact", mutate: func(_ string, tags *SidecarTags) []byte {
			tags.ConditioningLineage = nil
			tags.Conditioning = nil
			return nil
		}},
		{name: "different parent", mutate: func(_ string, tags *SidecarTags) []byte {
			tags.ConditioningLineage.ParentHash = "other-parent"
			return nil
		}},
		{name: "different reviewed child", mutate: func(_ string, tags *SidecarTags) []byte {
			tags.ConditioningLineage.ChildHash = "other-child"
			return nil
		}},
		{name: "different interval", mutate: func(_ string, tags *SidecarTags) []byte { tags.ConditioningLineage.IntendedStartMs++; return nil }},
		{name: "incomplete evidence", mutate: func(_ string, tags *SidecarTags) []byte {
			tags.Conditioning.AfterRewrite.Loudness.Available = false
			return nil
		}},
		{name: "different evidence", mutate: func(_ string, tags *SidecarTags) []byte {
			tags.Conditioning.AfterRewrite.Loudness.IntegratedLUFS = -18
			return nil
		}},
		{name: "corrupt sidecar", mutate: func(_ string, _ *SidecarTags) []byte { return []byte(`{"loomarr":`) }},
		{name: "corrupt output bytes", corrupt: true, mutate: func(_ string, _ *SidecarTags) []byte { return nil }},
		{name: "same sparse identity with different middle bytes", sparse: true, mutate: func(_ string, _ *SidecarTags) []byte { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			parentHash := writeContentAddressedClip(t, dir, []byte("retained composite"), ".mp4")
			parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
			sourceHash := writeContentAddressedClip(t, dir, []byte("reviewed child source"), ".mkv")
			sourceRel := filepath.ToSlash(ClipRelPath(sourceHash, ".mkv"))
			sourceFull := filepath.Join(dir, filepath.FromSlash(sourceRel))
			lineage := &ConditioningLineage{
				ChildHash: sourceHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
			}
			if err := WriteSidecarTags(sourceFull, SidecarTags{ConditioningLineage: lineage}, false); err != nil {
				t.Fatal(err)
			}
			outputBytes := []byte("same transformed bytes")
			var sparseReplacement []byte
			if tc.sparse {
				outputBytes, sparseReplacement = sparseIdentityCollision()
			}
			outputHash := writeContentAddressedClip(t, dir, outputBytes, ".mp4")
			outputRel := filepath.ToSlash(ClipRelPath(outputHash, ".mp4"))
			outputFull := filepath.Join(dir, filepath.FromSlash(outputRel))
			before := completeConditioningMeasurement(-28.4)
			after := completeConditioningMeasurement(-23.1)
			for i := range after.Cuts[0].Streams {
				after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
				after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
			}
			existing := SidecarTags{
				Mezzanine: mediatools.DefaultMezzanine().ID(), MediaQuality: &after.Quality,
				ConditioningLineage: lineage,
				Conditioning: &ConditioningEvidence{
					BeforeRewriteHash: sourceHash, AfterRewriteHash: outputHash,
					BeforeRewrite: before, AfterRewrite: after,
					DerivedParentEdgesAfterRewrite: before.Cuts[0],
				},
			}
			raw := tc.mutate(outputFull, &existing)
			if tc.corrupt {
				if err := os.WriteFile(outputFull, []byte("replacement bytes at the output hash"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if tc.sparse {
				if err := os.WriteFile(outputFull, sparseReplacement, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.missing {
				if err := os.Remove(sidecarPathFor(outputFull)); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			} else if raw != nil {
				if err := os.WriteFile(sidecarPathFor(outputFull), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := WriteSidecarTags(outputFull, existing, false); err != nil {
				t.Fatal(err)
			}

			stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
				Hash: parentHash, Path: parentRel, IsComposite: true,
			}}}}
			stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
				return Probed{DurationMs: 30_000, Height: 480}, nil
			}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
			stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
				return after.Quality, os.WriteFile(req.Out, outputBytes, 0o600)
			}
			measurements := 0
			stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
				measurements++
				if measurements == 1 {
					return before, nil
				}
				return after, nil
			})
			out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
				Hash: sourceHash, Path: sourceRel, ParentHash: parentHash,
			}})
			if err != nil || out.Verdict != VerdictReview || stored.oldHash != "" {
				t.Fatalf("mismatched existing output = %+v, stored=%q, err=%v", out, stored.oldHash, err)
			}
			if got, readErr := os.ReadFile(sourceFull); readErr != nil || string(got) != "reviewed child source" {
				t.Fatalf("source was not preserved: %q, %v", got, readErr)
			}
		})
	}
}

func TestTranscodeStage_ConditionedReplacementFailurePreservesSourceAndEvidenceForRecovery(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("retained composite"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	sourceHash := writeContentAddressedClip(t, dir, []byte("reviewed child source"), ".mkv")
	sourceRel := filepath.ToSlash(ClipRelPath(sourceHash, ".mkv"))
	sourceFull := filepath.Join(dir, filepath.FromSlash(sourceRel))
	lineage := &ConditioningLineage{
		ChildHash: sourceHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}
	if err := WriteSidecarTags(sourceFull, SidecarTags{ConditioningLineage: lineage}, false); err != nil {
		t.Fatal(err)
	}
	before := completeConditioningMeasurement(-28.4)
	after := completeConditioningMeasurement(-23.1)
	for i := range after.Cuts[0].Streams {
		after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
		after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
	}
	outputBytes := []byte("recoverable conditioned output")
	stored := &transcodeStore{err: errors.New("catalog replacement failed"), clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return after.Quality, os.WriteFile(req.Out, outputBytes, 0o600)
	}
	measurements := 0
	stage.WithConditioning(func(_ context.Context, req mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		measurements++
		if measurements%2 == 1 {
			return before, nil
		}
		return after, nil
	})
	clip := StoreClip{Clip: Clip{Hash: sourceHash, Path: sourceRel, ParentHash: parentHash}}

	first, err := stage.Run(context.Background(), clip)
	if err == nil || first.Verdict != VerdictContinue {
		t.Fatalf("failed conditioned replacement = %+v, %v; want retryable error", first, err)
	}
	if _, err := os.Stat(sourceFull); err != nil {
		t.Fatalf("failed replacement removed source: %v", err)
	}
	outputHashPath := filepath.Join(dir, filepath.FromSlash(ClipRelPath(stored.clip.Hash, ".mp4")))
	tags, ok := ReadSidecarTags(outputHashPath)
	if !ok || tags.Conditioning == nil || tags.Conditioning.BeforeRewriteHash != sourceHash ||
		tags.Conditioning.AfterRewriteHash != stored.clip.Hash || tags.NormalizedLUFS != -23 {
		t.Fatalf("recoverable published evidence = %+v, ok=%v", tags, ok)
	}

	second, err := stage.Run(context.Background(), clip)
	if err != nil || second.Verdict != VerdictContinue || second.Clip.Hash != stored.clip.Hash {
		t.Fatalf("conditioned recovery = %+v, %v", second, err)
	}
	if _, err := os.Stat(sourceFull); !os.IsNotExist(err) {
		t.Fatalf("successful recovery retained obsolete source: %v", err)
	}
}

func TestTranscodeStage_RecoveryCanonicalizesPostRewriteDurationBeforeEvidenceComparison(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("retained composite"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	sourceHash := writeContentAddressedClip(t, dir, []byte("duration-shift source"), ".mkv")
	sourceRel := filepath.ToSlash(ClipRelPath(sourceHash, ".mkv"))
	sourceFull := filepath.Join(dir, filepath.FromSlash(sourceRel))
	if err := WriteSidecarTags(sourceFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: sourceHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	before := completeConditioningMeasurement(-28.4)
	after := completeConditioningMeasurement(-23.1)
	after.ContainerDurationMs = 29_967
	after.Streams[0].Duration.Milliseconds = 29_967
	after.Streams[1].Duration.Milliseconds = 29_847
	after.Quality.DurationMs = 29_967
	for i := range after.Cuts[0].Streams {
		after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
		after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
	}
	outputBytes := []byte("duration-shift output")
	stored := &transcodeStore{err: errors.New("first replacement failed"), clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(_ context.Context, path string) (Probed, error) {
		if filepath.Ext(path) == ".mp4" && !strings.Contains(filepath.Base(path), "parent") {
			return Probed{DurationMs: 29_967, Height: 480}, nil
		}
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		quality := after.Quality
		quality.DurationMs = 30_000 // detector result before canonical output probe duration is applied
		return quality, os.WriteFile(req.Out, outputBytes, 0o600)
	}
	stage.WithConditioning(func(_ context.Context, req mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		if req.Path == sourceFull || filepath.Ext(req.Path) == ".mkv" {
			return before, nil
		}
		return after, nil
	})
	clip := StoreClip{Clip: Clip{Hash: sourceHash, Path: sourceRel, ParentHash: parentHash}}
	first, err := stage.Run(context.Background(), clip)
	if err == nil || first.Verdict != VerdictContinue {
		t.Fatalf("first interrupted publication = %+v, %v; want retryable error", first, err)
	}
	second, err := stage.Run(context.Background(), clip)
	if err != nil || second.Verdict != VerdictContinue || second.Clip.Hash == sourceHash {
		t.Fatalf("duration-shift recovery = %+v, %v; want exact existing evidence recovery", second, err)
	}
}

func TestTranscodeStage_SourceCleanupFailureLeavesDurableQuarantineAcrossSync(t *testing.T) {
	dir := t.TempDir()
	parentHash := writeContentAddressedClip(t, dir, []byte("retained composite"), ".mp4")
	parentRel := filepath.ToSlash(ClipRelPath(parentHash, ".mp4"))
	sourceHash := writeContentAddressedClip(t, dir, []byte("reviewed child source"), ".mkv")
	sourceRel := filepath.ToSlash(ClipRelPath(sourceHash, ".mkv"))
	sourceFull := filepath.Join(dir, filepath.FromSlash(sourceRel))
	if err := WriteSidecarTags(sourceFull, SidecarTags{ConditioningLineage: &ConditioningLineage{
		ChildHash: sourceHash, ParentHash: parentHash, IntendedStartMs: 1_000, IntendedEndMs: 31_000,
	}}, false); err != nil {
		t.Fatal(err)
	}
	before := completeConditioningMeasurement(-28.4)
	after := completeConditioningMeasurement(-23.1)
	for i := range after.Cuts[0].Streams {
		after.Cuts[0].Streams[i].StartError = mediatools.OptionalMilliseconds{}
		after.Cuts[0].Streams[i].EndError = mediatools.OptionalMilliseconds{}
	}
	stored := &transcodeStore{clips: map[string]StoreClip{parentHash: {Clip: Clip{
		Hash: parentHash, Path: parentRel, IsComposite: true,
	}}}}
	stage := NewTranscodeStage(stored, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, func() float64 { return -23 }, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return after.Quality, os.WriteFile(req.Out, []byte("conditioned replacement"), 0o600)
	}
	measurements := 0
	stage.WithConditioning(func(context.Context, mediatools.ConditioningRequest) (mediatools.ConditioningMeasurement, error) {
		measurements++
		if measurements == 1 {
			return before, nil
		}
		return after, nil
	})
	stage.removeSource = func(string) error { return errors.New("injected source cleanup failure") }

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: sourceHash, Path: sourceRel, ParentHash: parentHash,
	}})
	if err != nil || out.Verdict != VerdictContinue || out.Clip.Hash == sourceHash {
		t.Fatalf("cleanup failure result = %+v, %v", out, err)
	}
	if _, err := os.Stat(sourceFull); err != nil {
		t.Fatalf("obsolete source was not preserved: %v", err)
	}
	tags, ok := ReadSidecarTags(sourceFull)
	if !ok || tags.SupersededByHash != out.Clip.Hash {
		t.Fatalf("obsolete source quarantine = %+v, ok=%v", tags, ok)
	}

	watch := t.TempDir()
	layout, err := NewLayout(dir, watch)
	if err != nil {
		t.Fatal(err)
	}
	scanStore := &cleanupSyncStore{clips: map[string]StoreClip{}}
	if _, err := NewSyncer(cleanupSyncSource{dir: dir}, scanStore, layout, time.Now, nil).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, found, err := scanStore.GetClip(context.Background(), sourceHash)
	if err != nil || !found || !old.Held {
		t.Fatalf("obsolete source after Sync = %+v, found=%v, err=%v; want held", old, found, err)
	}
}

func TestTranscodeStage_RekeysBytesAndPreservesHumanMetadata(t *testing.T) {
	dir := t.TempDir()
	oldBytes := []byte("the original container bytes")
	newBytes := []byte("the verified mezzanine bytes are different")
	oldHash := writeContentAddressedClip(t, dir, oldBytes, ".mkv")
	oldRel := filepath.ToSlash(ClipRelPath(oldHash, ".mkv"))
	oldFull := filepath.Join(dir, filepath.FromSlash(oldRel))

	stored := &transcodeStore{}
	probe := func(_ context.Context, path string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, nil,
		func() time.Time { return time.Unix(1_900_000_000, 0) })
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{}, os.WriteFile(req.Out, newBytes, 0o644)
	}

	in := StoreClip{Clip: Clip{
		Hash: oldHash, Path: oldRel, Name: "McDonald's 1993", Kind: Commercial,
		Era: 1993, Audience: Kids, Category: "fast_food", Source: "archive:waga",
		Held: true, Confidence: 91,
	}, UpdatedAt: time.Unix(1_800_000_000, 0)}
	out, err := stage.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	newHash, err := ClipID(filepath.Join(dir, filepath.FromSlash(out.Clip.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if out.Clip.Hash != newHash || stored.clip.Hash != newHash {
		t.Fatalf("transformed identity = %q / stored %q, want content hash %q", out.Clip.Hash, stored.clip.Hash, newHash)
	}
	if want := filepath.ToSlash(ClipRelPath(newHash, ".mp4")); out.Clip.Path != want {
		t.Errorf("transformed path = %q, want canonical %q", out.Clip.Path, want)
	}
	if stored.oldHash != oldHash {
		t.Errorf("replaced hash = %q, want %q", stored.oldHash, oldHash)
	}
	if stored.clip.Name != in.Name || stored.clip.Source != in.Source {
		t.Errorf("human metadata did not follow the transform: %+v", stored.clip.Clip)
	}
	if _, err := os.Stat(oldFull); !os.IsNotExist(err) {
		t.Errorf("old media still exists after durable replacement: %v", err)
	}

	tags, ok := ReadSidecarTags(filepath.Join(dir, filepath.FromSlash(out.Clip.Path)))
	if !ok {
		t.Fatal("transformed clip has no sidecar")
	}
	if tags.OriginalName != in.Name {
		t.Errorf("originalName = %q, want display name %q — a split child must not rescan as a hash", tags.OriginalName, in.Name)
	}
	if tags.Mezzanine != mediatools.DefaultMezzanine().ID() || tags.Era != in.Era || tags.Category != in.Category {
		t.Errorf("sidecar did not carry the transform metadata: %+v", tags)
	}
	if tags.MediaAssets == nil || tags.MediaAssets.SourceMaster.ClipHash != oldHash {
		t.Fatalf("playable sidecar lost source-master lineage: %+v", tags.MediaAssets)
	}
	masterPath := filepath.Join(dir, filepath.FromSlash(tags.MediaAssets.SourceMaster.Path))
	masterBytes, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatalf("retained source master is unavailable after replacement: %v", err)
	}
	if !bytes.Equal(masterBytes, oldBytes) {
		t.Fatalf("retained source master = %q, want exact input %q", masterBytes, oldBytes)
	}

	// Drive the real scanner over the transformed layout. This is the lifecycle seam that
	// originally replaced the title with the stale hash on the pass after transcode.
	clips, skipped, err := ScanDir(context.Background(), dir, probe)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(clips) != 1 {
		t.Fatalf("rescan = %d clips / %d skipped, want one clean clip", len(clips), skipped)
	}
	if clips[0].ID != newHash || clips[0].Name != in.Name || clips[0].Path != out.Clip.Path {
		t.Errorf("rescan lost identity/title/path: %+v", clips[0])
	}
}

func TestTranscodeStage_IdenticalOutputAlreadyAtCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	bytes := []byte("already normalized mezzanine bytes")
	hash := writeContentAddressedClip(t, dir, bytes, ".mp4")
	rel := filepath.ToSlash(ClipRelPath(hash, ".mp4"))
	stored := &transcodeStore{}
	probe := func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{}, os.WriteFile(req.Out, bytes, 0o644)
	}

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: hash, Path: rel, Name: "Already normalized", Kind: Commercial,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Clip.Hash != hash || out.Clip.Path != rel || stored.oldHash != hash || stored.clip.Hash != hash {
		t.Fatalf("same-identity result = %+v, stored old=%q clip=%+v", out.Clip, stored.oldHash, stored.clip)
	}
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bytes) {
		t.Fatalf("canonical media changed: %q", got)
	}
	tags, ok := ReadSidecarTags(filepath.Join(dir, filepath.FromSlash(rel)))
	if !ok || tags.Mezzanine != mediatools.DefaultMezzanine().ID() || tags.MediaQuality == nil {
		t.Fatalf("canonical sidecar = %+v, ok=%v", tags, ok)
	}
}

func TestTranscodeStage_IdenticalOutputMovesToCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	bytes := []byte("unchanged bytes in a noncanonical container path")
	hash := writeContentAddressedClip(t, dir, bytes, ".mkv")
	oldRel := filepath.ToSlash(ClipRelPath(hash, ".mkv"))
	stored := &transcodeStore{}
	probe := func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{}, os.WriteFile(req.Out, bytes, 0o644)
	}

	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: hash, Path: oldRel, Name: "Move without re-key", Kind: Commercial,
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantRel := filepath.ToSlash(ClipRelPath(hash, ".mp4"))
	if out.Clip.Hash != hash || out.Clip.Path != wantRel || stored.oldHash != hash || stored.clip.Hash != hash {
		t.Fatalf("same-identity move = %+v, stored old=%q clip=%+v", out.Clip, stored.oldHash, stored.clip)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(oldRel))); !os.IsNotExist(err) {
		t.Fatalf("old path survived canonical move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(wantRel))); err != nil {
		t.Fatalf("canonical path missing: %v", err)
	}
}

func TestScanDir_IgnoresTranscodeStaging(t *testing.T) {
	dir := t.TempDir()
	stageDir := filepath.Join(dir, transcodeStagingDir)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "partial.mp4"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	clips, skipped, err := ScanDir(context.Background(), dir, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 0 || skipped != 0 {
		t.Fatalf("staging leaked into catalog: clips=%d skipped=%d", len(clips), skipped)
	}
}

func TestTranscodeStage_RejectsNearTotalBlackContentAndPersistsEvidence(t *testing.T) {
	dir := t.TempDir()
	oldHash := writeContentAddressedClip(t, dir, []byte("original"), ".mkv")
	oldRel := filepath.ToSlash(ClipRelPath(oldHash, ".mkv"))
	stored := &transcodeStore{}
	probe := func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}
	stage := NewTranscodeStage(stored, probe, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(_ context.Context, req mediatools.TranscodeRequest, _ func(int)) (MediaQuality, error) {
		return MediaQuality{DurationMs: 30_000, Black: []Interval{{StartMs: 0, EndMs: 28_000}}},
			os.WriteFile(req.Out, []byte("replacement"), 0o644)
	}
	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: oldHash, Path: oldRel, Name: "Named advert", Kind: Commercial,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != VerdictReject || out.Reason != ReasonBlackContent {
		t.Fatalf("quality verdict = %v/%q (%s)", out.Verdict, out.Reason, out.Detail)
	}
	tags, ok := ReadSidecarTags(filepath.Join(dir, filepath.FromSlash(out.Clip.Path)))
	if !ok || tags.MediaQuality == nil || len(tags.MediaQuality.Black) != 1 {
		t.Fatalf("sidecar quality evidence = %+v ok=%v", tags.MediaQuality, ok)
	}
}

func TestTranscodeStage_BackfillsQualityWithoutReencodingOldMezzanine(t *testing.T) {
	dir := t.TempDir()
	hash := writeContentAddressedClip(t, dir, []byte("old mezzanine"), ".mp4")
	rel := filepath.ToSlash(ClipRelPath(hash, ".mp4"))
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := WriteSidecarTags(full, SidecarTags{
		OriginalName: "Old advert", Mezzanine: mediatools.DefaultMezzanine().ID(),
	}, false); err != nil {
		t.Fatal(err)
	}
	stage := NewTranscodeStage(&transcodeStore{}, func(context.Context, string) (Probed, error) {
		return Probed{DurationMs: 30_000, Height: 480}, nil
	}, dir, mediatools.DefaultMezzanine(), nil, nil, time.Now)
	stage.transcode = func(context.Context, mediatools.TranscodeRequest, func(int)) (MediaQuality, error) {
		t.Fatal("an existing mezzanine must not be re-encoded to backfill quality")
		return MediaQuality{}, nil
	}
	stage.inspect = func(context.Context, string, string, int64, bool) (MediaQuality, error) {
		return MediaQuality{DurationMs: 30_000, Silence: []Interval{{StartMs: 0, EndMs: 29_000}}}, nil
	}
	out, err := stage.Run(context.Background(), StoreClip{Clip: Clip{
		Hash: hash, Path: rel, Name: "Old advert", Kind: Commercial,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict != VerdictReject || out.Reason != ReasonSilentContent {
		t.Fatalf("backfill verdict = %v/%q", out.Verdict, out.Reason)
	}
	tags, ok := ReadSidecarTags(full)
	if !ok || tags.MediaQuality == nil {
		t.Fatal("quality backfill was not persisted beside the old mezzanine")
	}
	if applies, _ := stage.Applies(context.Background(), out.Clip); !applies {
		t.Fatal("an anomalous report must remain applicable until its airability gate is durable")
	}
	stage.inspect = func(context.Context, string, string, int64, bool) (MediaQuality, error) {
		t.Fatal("retry must use the persisted quality evidence")
		return MediaQuality{}, nil
	}
	retry, err := stage.Run(context.Background(), out.Clip)
	if err != nil || retry.Verdict != VerdictReject || retry.Reason != ReasonSilentContent {
		t.Fatalf("persisted quality retry = %+v, %v", retry, err)
	}
}

func writeContentAddressedClip(t *testing.T, dir string, body []byte, ext string) string {
	t.Helper()
	tmp := filepath.Join(dir, "source"+ext)
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := ClipID(tmp)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, ClipRelPath(hash, ext))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		t.Fatal(err)
	}
	return hash
}

func sparseIdentityCollision() ([]byte, []byte) {
	const window = 64 << 10
	head := bytes.Repeat([]byte{'h'}, window)
	tail := bytes.Repeat([]byte{'t'}, window)
	left := append(append(append(make([]byte, 0, window*3), head...), bytes.Repeat([]byte{'a'}, window)...), tail...)
	right := append(append(append(make([]byte, 0, window*3), head...), bytes.Repeat([]byte{'b'}, window)...), tail...)
	return left, right
}
