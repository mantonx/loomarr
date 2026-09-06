package filler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillerstructure"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type capturedWindowPreparer struct {
	prepared StructureAssessmentWindowMediaSet
	err      error
}

func (p *capturedWindowPreparer) PrepareWindows(_ context.Context, _ StructureAssessmentSource, _ fillerstructurewindow.Plan) (StructureAssessmentWindowMediaSet, error) {
	return p.prepared, p.err
}

type capturedWindowAssessor struct {
	profile        fillerstructure.AssessorProfile
	timeline       []fillerstructure.Segment
	events         *[]string
	failureOrdinal int
	driftProfile   bool
}

func (a *capturedWindowAssessor) Profile() fillerstructure.AssessorProfile { return a.profile }

func (a *capturedWindowAssessor) AssessWindow(_ context.Context, set fillerstructurewindow.MediaSet, media StructureAssessmentWindowMedia) (fillerstructurewindow.RecordedAssessment, error) {
	*a.events = append(*a.events, "call:"+a.profile.ID+":"+string(rune('0'+media.Window.Ordinal)))
	profile := a.profile
	if a.driftProfile {
		profile.ID += "-drift"
	}
	duration := media.Window.MediaEndMS - media.Window.MediaStartMS
	input := fillerstructurewindow.CallRecordInput{
		MediaSet: set, WindowOrdinal: media.Window.Ordinal, Assessor: profile,
		MetadataSnapshotSHA256: strings.Repeat("f", 64),
		PromptSHA256:           fillerstructurewindow.DirectVideoPromptSHA256(duration),
		SchemaSHA256:           fillerstructurewindow.DirectVideoSchemaSHA256(duration),
		RequestSHA256:          windowRequestDigest(profile.ID, media.Window.Ordinal),
		RawResponse:            []byte("provider response"), StructuredOutput: windowStructuredOutput(timelineWithinWindow(a.timeline, media.Window), media.Window),
		ResolvedProvider: profile.Provider, ResolvedModel: "resolved-model", UpstreamProvider: "Provider",
		UpstreamProviderSlug: "provider", GenerationID: "generation-1",
		Tokens:           fillerstructure.AssessmentTokenUsage{Prompt: 100, Completion: 20, Video: 80},
		RequestedNanoUSD: 2_000, ReservedNanoUSD: 2_000, ChargedAmountUSD: "0.000001",
		ChargedNanoUSD: 1_000, AccountedNanoUSD: 1_000, ChargeKnown: true,
		State:      fillerstructure.AssessmentRecordAccepted,
		AssessedAt: time.Date(2026, time.September, 11, 10, 0, media.Window.Ordinal, 0, time.UTC),
	}
	if media.Window.Ordinal == a.failureOrdinal {
		input.State, input.Failure = fillerstructure.AssessmentRecordFailed, fillerstructure.AssessmentFailureProvider
		input.ResolvedProvider, input.ResolvedModel = "", ""
	}
	return fillerstructurewindow.NewRecordedAssessment(input)
}

type capturedWindowEvidence struct {
	events      *[]string
	failEvent   string
	assessments []fillerstructurewindow.RecordedAssessment
	byRecord    map[string]fillerstructurewindow.RecordedAssessment
	byOperation map[string]string
	stitches    []fillerstructurewindow.StitchResult
	decisions   []fillerstructure.Artifact
}

func (e *capturedWindowEvidence) PutStructureWindowAssessmentEvidence(_ context.Context, recorded fillerstructurewindow.RecordedAssessment) error {
	assessment := recorded.Assessment
	event := "put:" + assessment.Assessor.ID + ":" + string(rune('0'+assessment.WindowOrdinal))
	*e.events = append(*e.events, event)
	if event == e.failEvent {
		return errors.New("persistence failed")
	}
	e.assessments = append(e.assessments, recorded)
	if e.byRecord == nil {
		e.byRecord = make(map[string]fillerstructurewindow.RecordedAssessment)
	}
	e.byRecord[recorded.Record.SHA256] = recorded
	if e.byOperation == nil {
		e.byOperation = make(map[string]string)
	}
	operation := fillerstructurewindow.CallOperationSHA256(recorded.Record.MediaSet, recorded.Record.WindowOrdinal, recorded.Record.Assessor)
	if existing := e.byOperation[operation]; existing != "" && existing != recorded.Record.SHA256 {
		return errors.New("operation conflict")
	}
	e.byOperation[operation] = recorded.Record.SHA256
	return nil
}

func (e *capturedWindowEvidence) GetStructureWindowAssessmentEvidence(_ context.Context, _ fillerstructurewindow.MediaSet, recordSHA256 string) (fillerstructurewindow.RecordedAssessment, error) {
	recorded, ok := e.byRecord[recordSHA256]
	if !ok {
		return fillerstructurewindow.RecordedAssessment{}, errors.New("missing evidence")
	}
	event := "get:" + recorded.Assessment.Assessor.ID + ":" + string(rune('0'+recorded.Assessment.WindowOrdinal))
	*e.events = append(*e.events, event)
	if event == e.failEvent {
		return fillerstructurewindow.RecordedAssessment{}, errors.New("replay failed")
	}
	return recorded, nil
}

func (e *capturedWindowEvidence) FindStructureWindowAssessmentEvidence(_ context.Context, set fillerstructurewindow.MediaSet, ordinal int, profile fillerstructure.AssessorProfile) (fillerstructurewindow.RecordedAssessment, bool, error) {
	operation := fillerstructurewindow.CallOperationSHA256(set, ordinal, profile)
	recordSHA256 := e.byOperation[operation]
	if recordSHA256 == "" {
		return fillerstructurewindow.RecordedAssessment{}, false, nil
	}
	recorded := e.byRecord[recordSHA256]
	event := "reuse:" + profile.ID + ":" + string(rune('0'+ordinal))
	*e.events = append(*e.events, event)
	if event == e.failEvent {
		return fillerstructurewindow.RecordedAssessment{}, false, errors.New("resume failed")
	}
	return recorded, true, nil
}

func (e *capturedWindowEvidence) PutStructureWindowStitch(_ context.Context, stitch fillerstructurewindow.StitchResult) error {
	event := "stitch:" + stitch.Assessor.ID
	*e.events = append(*e.events, event)
	if event == e.failEvent {
		return errors.New("persistence failed")
	}
	e.stitches = append(e.stitches, stitch)
	return nil
}

func (e *capturedWindowEvidence) PutStructureDecisionArtifact(_ context.Context, artifact fillerstructure.Artifact) error {
	*e.events = append(*e.events, "decision")
	if e.failEvent == "decision" {
		return errors.New("persistence failed")
	}
	e.decisions = append(e.decisions, artifact)
	return nil
}

func TestStructureWindowRuntimePersistsFamilyMajorSerialEvidenceBeforeReduction(t *testing.T) {
	input, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	assessors := []CompleteWindowStructureAssessor{
		windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events),
		windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events),
	}
	evidence := &capturedWindowEvidence{events: &events}
	runtime, err := NewStructureWindowAssessmentRuntime(assessors, &capturedWindowPreparer{prepared: prepared}, evidence, 2_000, func() time.Time {
		return time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := runtime.Assess(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []string{
		"call:assessor-a:0", "put:assessor-a:0", "get:assessor-a:0", "call:assessor-a:1", "put:assessor-a:1", "get:assessor-a:1", "call:assessor-a:2", "put:assessor-a:2", "get:assessor-a:2", "stitch:assessor-a",
		"call:assessor-b:0", "put:assessor-b:0", "get:assessor-b:0", "call:assessor-b:1", "put:assessor-b:1", "get:assessor-b:1", "call:assessor-b:2", "put:assessor-b:2", "get:assessor-b:2", "stitch:assessor-b", "decision",
	}
	if !reflect.DeepEqual(events, wantEvents) || len(evidence.assessments) != 6 || len(evidence.stitches) != 2 || len(evidence.decisions) != 1 ||
		artifact.Decision.Status != fillerstructure.StatusConfirmed || artifact.Decision.Unit != fillerstructure.UnitCompilation ||
		artifact.Decision.Input.Kind != fillerstructure.AssessmentInputWindowMediaSet {
		t.Fatalf("events=%v evidence=%+v artifact=%+v", events, evidence, artifact)
	}
}

func TestStructureWindowRuntimeAssessesPreflightedMediaWithoutPreparingAgain(t *testing.T) {
	input, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	runtime, err := NewStructureWindowAssessmentRuntime([]CompleteWindowStructureAssessor{
		windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events),
		windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events),
	}, &capturedWindowPreparer{err: errors.New("preparer must not run")}, &capturedWindowEvidence{events: &events}, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := runtime.AssessPrepared(t.Context(), input, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Decision.Status != fillerstructure.StatusConfirmed || len(events) == 0 {
		t.Fatalf("prepared assessment = %+v, events=%v", artifact, events)
	}
}

func TestStructureWindowFamilyRuntimePersistsAndReturnsOneReplayableStitch(t *testing.T) {
	_, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	evidence := &capturedWindowEvidence{events: &events}
	runtime, err := NewStructureWindowFamilyRuntime(
		windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events), evidence, 2_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	familyEvidence, err := runtime.AssessWithEvidence(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	stitch := familyEvidence.Stitch
	wantEvents := []string{
		"call:assessor-a:0", "put:assessor-a:0", "get:assessor-a:0",
		"call:assessor-a:1", "put:assessor-a:1", "get:assessor-a:1",
		"call:assessor-a:2", "put:assessor-a:2", "get:assessor-a:2",
		"stitch:assessor-a",
	}
	if !reflect.DeepEqual(events, wantEvents) || len(evidence.stitches) != 1 ||
		!reflect.DeepEqual(stitch, evidence.stitches[0]) || fillerstructurewindow.ValidateStitchResult(stitch) != nil ||
		stitch.Status != fillerstructurewindow.StitchComplete || ValidateStructureWindowFamilyEvidence(familyEvidence) != nil ||
		len(familyEvidence.CallRecords) != len(prepared.Windows) || len(familyEvidence.Publications) != len(prepared.Windows) {
		t.Fatalf("events=%v family=%+v evidence=%+v", events, familyEvidence, evidence)
	}
	drifted := familyEvidence
	drifted.Publications = append([]fillerstructurewindow.CallPublication(nil), familyEvidence.Publications...)
	drifted.Publications[0].RecordSHA256 = strings.Repeat("f", 64)
	drifted.Publications[0].SHA256 = fillerstructurewindow.CallPublicationSHA256(drifted.Publications[0])
	drifted.SHA256 = StructureWindowFamilyEvidenceSHA256(drifted)
	if err := ValidateStructureWindowFamilyEvidence(drifted); err == nil {
		t.Fatal("family evidence accepted a publication detached from its call record")
	}
}

func TestStructureWindowRuntimeRetainsOperationalFailureAndCompletesEveryWindow(t *testing.T) {
	input, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	left := windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events)
	left.failureOrdinal = 1
	evidence := &capturedWindowEvidence{events: &events}
	runtime, err := NewStructureWindowAssessmentRuntime([]CompleteWindowStructureAssessor{
		left, windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events),
	}, &capturedWindowPreparer{prepared: prepared}, evidence, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := runtime.Assess(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Decision.Status != fillerstructure.StatusHeld ||
		!reflect.DeepEqual(artifact.Decision.ReasonCodes, []string{fillerstructure.ReasonOperationalFailure}) ||
		len(evidence.assessments) != 6 || len(evidence.stitches) != 2 || evidence.stitches[0].Status != fillerstructurewindow.StitchHeld {
		t.Fatalf("events=%v evidence=%+v artifact=%+v", events, evidence, artifact)
	}
}

func TestStructureWindowRuntimeStopsBeforeNextCallOnDriftOrPersistenceFailure(t *testing.T) {
	for _, test := range []struct {
		name      string
		drift     bool
		failEvent string
	}{
		{name: "profile drift", drift: true},
		{name: "assessment persistence", failEvent: "put:assessor-a:0"},
		{name: "assessment replay", failEvent: "get:assessor-a:0"},
		{name: "stitch persistence", failEvent: "stitch:assessor-a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, prepared := structureWindowRuntimeFixture(t)
			events := []string{}
			timeline := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
			left := windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events)
			left.driftProfile = test.drift
			evidence := &capturedWindowEvidence{events: &events, failEvent: test.failEvent}
			runtime, err := NewStructureWindowAssessmentRuntime([]CompleteWindowStructureAssessor{
				left, windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events),
			}, &capturedWindowPreparer{prepared: prepared}, evidence, 2_000, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Assess(t.Context(), input); err == nil {
				t.Fatal("invalid or unpersisted evidence reached reduction")
			}
			for _, event := range events {
				if strings.HasPrefix(event, "call:assessor-b") {
					t.Fatalf("second family ran after failure: %v", events)
				}
			}
		})
	}
}

func TestStructureWindowRuntimeRejectsInvalidPolicyBeforePreparation(t *testing.T) {
	input, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	assessors := []CompleteWindowStructureAssessor{
		windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events),
		windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events),
	}
	preparer := &capturedWindowPreparer{prepared: prepared}
	if _, err := NewStructureWindowAssessmentRuntime(assessors, preparer, &capturedWindowEvidence{events: &events}, fillerstructurewindow.ContextOverlapMS, time.Now); err == nil {
		t.Fatal("seam tolerance as wide as the context was accepted")
	}
	prepared.Windows[0].FullPath = input.FullPath
	runtime, err := NewStructureWindowAssessmentRuntime(assessors, preparer, &capturedWindowEvidence{events: &events}, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Assess(t.Context(), input); err == nil || len(events) != 0 {
		t.Fatalf("source path reused as normalized window: events=%v error=%v", events, err)
	}
}

func TestStructureWindowRuntimeFreezesConfiguredProfiles(t *testing.T) {
	input, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{{StartMS: 0, EndMS: 300_000, Role: fillerstructure.RoleCommercial}}
	left := windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events)
	runtime, err := NewStructureWindowAssessmentRuntime([]CompleteWindowStructureAssessor{
		left, windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events),
	}, &capturedWindowPreparer{prepared: prepared}, &capturedWindowEvidence{events: &events}, 2_000, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	left.profile.ID = "assessor-a-mutated"
	if _, err := runtime.Assess(t.Context(), input); err == nil {
		t.Fatal("profile mutation after construction was accepted")
	}
}

func TestStructureWindowRuntimeResumesPublishedWindowsWithoutDuplicateCalls(t *testing.T) {
	input, prepared := structureWindowRuntimeFixture(t)
	events := []string{}
	timeline := []fillerstructure.Segment{
		{StartMS: 0, EndMS: 120_000, Role: fillerstructure.RoleCommercial},
		{StartMS: 120_000, EndMS: 300_000, Role: fillerstructure.RolePromo},
	}
	left := windowAssessorFixture("assessor-a", "family-a", "a", timeline, &events)
	right := windowAssessorFixture("assessor-b", "family-b", "b", timeline, &events)
	evidence := &capturedWindowEvidence{events: &events}
	for ordinal := 0; ordinal < 2; ordinal++ {
		recorded, err := left.AssessWindow(t.Context(), prepared.Authority, prepared.Windows[ordinal])
		if err != nil {
			t.Fatal(err)
		}
		if err := evidence.PutStructureWindowAssessmentEvidence(t.Context(), recorded); err != nil {
			t.Fatal(err)
		}
	}
	events = nil
	runtime, err := NewStructureWindowAssessmentRuntime(
		[]CompleteWindowStructureAssessor{left, right}, &capturedWindowPreparer{prepared: prepared}, evidence,
		2_000, func() time.Time { return time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC) },
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := runtime.Assess(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{
		"reuse:assessor-a:0", "get:assessor-a:0", "reuse:assessor-a:1", "get:assessor-a:1",
		"call:assessor-a:2", "put:assessor-a:2", "get:assessor-a:2", "stitch:assessor-a",
	}
	if len(events) < len(wantPrefix) || !reflect.DeepEqual(events[:len(wantPrefix)], wantPrefix) ||
		artifact.Decision.Status != fillerstructure.StatusConfirmed {
		t.Fatalf("events=%v artifact=%+v", events, artifact)
	}
	for _, event := range events {
		if event == "call:assessor-a:0" || event == "call:assessor-a:1" || event == "put:assessor-a:0" || event == "put:assessor-a:1" {
			t.Fatalf("published window was called or rewritten: %v", events)
		}
	}
}

func structureWindowRuntimeFixture(t *testing.T) (StructureAssessmentSource, StructureAssessmentWindowMediaSet) {
	t.Helper()
	root := t.TempDir()
	source := SplitSourceAsset{
		Role: SplitSourceLegacyPlayback, SHA256: strings.Repeat("1", 64), Bytes: 2_048,
		ClipHash: strings.Repeat("2", 64), Path: "source.mp4", DurationMs: 300_000,
	}
	input := StructureAssessmentSource{Source: source, FullPath: filepath.Join(root, source.Path)}
	core := fillerstructure.Source{SHA256: source.SHA256, Bytes: source.Bytes, DurationMS: source.DurationMs}
	plan, err := fillerstructurewindow.NewPlan(core)
	if err != nil {
		t.Fatal(err)
	}
	media := make([]fillerstructure.AssessmentMedia, len(plan.Windows))
	for ordinal, window := range plan.Windows {
		media[ordinal] = fillerstructure.AssessmentMedia{
			SHA256: strings.Repeat(string(rune('3'+ordinal)), 64), Bytes: 1_024,
			DurationMS:    window.MediaEndMS - window.MediaStartMS,
			ProfileSHA256: plan.Profile.AssessmentMediaProfileSHA256,
			LineageSHA256: strings.Repeat(string(rune('6'+ordinal)), 64),
		}
	}
	set, err := fillerstructurewindow.NewMediaSet(plan, media)
	if err != nil {
		t.Fatal(err)
	}
	prepared := StructureAssessmentWindowMediaSet{Source: source, Authority: set}
	for ordinal, window := range plan.Windows {
		prepared.Windows = append(prepared.Windows, StructureAssessmentWindowMedia{
			Window: window, Media: set.Windows[ordinal], FullPath: filepath.Join(root, "window-"+string(rune('0'+ordinal))+".mp4"),
		})
	}
	return input, prepared
}

func windowAssessorFixture(id, family, digest string, timeline []fillerstructure.Segment, events *[]string) *capturedWindowAssessor {
	return &capturedWindowAssessor{
		profile: fillerstructure.AssessorProfile{
			ID: id, ModelFamily: family, Provider: "provider", Model: "model",
			ModelDigest: strings.Repeat(digest, 64), CapabilitySHA256: strings.Repeat("f", 64),
			PromptVersion:    fillerstructurewindow.DirectVideoPromptVersion,
			EvidenceContract: fillerstructurewindow.CallRecordContractVersion,
		},
		timeline: timeline, events: events, failureOrdinal: -1,
	}
}

func clipStructureTimeline(timeline []fillerstructure.Segment, window fillerstructurewindow.Window) []fillerstructure.Segment {
	var clipped []fillerstructure.Segment
	for _, segment := range timeline {
		start, end := max(segment.StartMS, window.MediaStartMS), min(segment.EndMS, window.MediaEndMS)
		if start < end {
			clipped = append(clipped, fillerstructure.Segment{StartMS: start, EndMS: end, Role: segment.Role})
		}
	}
	return clipped
}

func timelineWithinWindow(timeline []fillerstructure.Segment, window fillerstructurewindow.Window) []fillerstructure.Segment {
	return clipStructureTimeline(timeline, window)
}

func windowStructuredOutput(segments []fillerstructure.Segment, window fillerstructurewindow.Window) string {
	type responseSegment struct {
		EndMS        int64   `json:"endMs"`
		Role         string  `json:"role"`
		DecisiveAtMS []int64 `json:"decisiveAtMs"`
		Reason       string  `json:"reason"`
	}
	response := struct {
		Segments []responseSegment `json:"segments"`
	}{Segments: make([]responseSegment, 0, len(segments))}
	for _, segment := range segments {
		localEnd := segment.EndMS - window.MediaStartMS
		response.Segments = append(response.Segments, responseSegment{
			EndMS: localEnd, Role: string(segment.Role), DecisiveAtMS: []int64{max(0, localEnd-1)}, Reason: "fixture evidence",
		})
	}
	raw, _ := json.Marshal(response)
	return string(raw)
}

func windowRequestDigest(assessorID string, ordinal int) string {
	digest := sha256.Sum256([]byte(assessorID + ":" + string(rune('0'+ordinal))))
	return hex.EncodeToString(digest[:])
}
