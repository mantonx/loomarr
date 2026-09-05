package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerquarantine"
	"github.com/loomarr/loomarr/internal/mediatools"
	"github.com/loomarr/loomarr/internal/testkit"
)

type fakeDeriver struct{ frame, alternateFrame []byte }

func (f fakeDeriver) Measure(context.Context, string, int64, int64) (mediaMeasurement, error) {
	return mediaMeasurement{DurationMS: 30_000, Usable: true, Detail: "duration_ms=30000;no_video=false;no_audio=false;black_percent=0;silence_percent=0"}, nil
}
func (f fakeDeriver) Frames(_ context.Context, path string, _, _ int64) ([][]byte, error) {
	frame := f.frame
	if strings.Contains(path, "case-b") && len(f.alternateFrame) > 0 {
		frame = f.alternateFrame
	}
	return [][]byte{frame, frame, frame, frame}, nil
}
func (f fakeDeriver) Audio(context.Context, string, int64, int64) (audioDerivative, error) {
	data := []byte("bounded audio derivative")
	return audioDerivative{Data: data, SHA256: fillercorpus.InventorySHA256(data), DurationMS: 30_000}, nil
}
func (f fakeDeriver) Video(context.Context, string, int64, int64) (videoDerivative, error) {
	data := []byte("bounded video derivative")
	return videoDerivative{Data: data, SHA256: fillercorpus.InventorySHA256(data), DurationMS: 10_000, Width: 640, Height: 360}, nil
}

func TestPrepareFreezesApprovedMediaIntoLabelBlindPackets(t *testing.T) {
	opts, deriver := preparationFixture(t)
	draft, packets, err := prepare(t.Context(), opts, deriver)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Cases) != 2 || len(packets) != 2 || draft.Cases[0].Truth != "" || len(draft.Cases[0].LabelReviews) != 0 {
		t.Fatalf("draft = %+v, packets = %d", draft, len(packets))
	}
	if draft.Kind != fillereval.CorpusDevelopmentSeed {
		t.Fatalf("draft kind = %q, want development_seed", draft.Kind)
	}
	if draft.Cases[0].EvidenceSHA256 == "" || draft.Cases[0].Provenance.RightsDecision != "approved" || len(packets[0].Facts) != 2 || len(packets[0].Signals) != 8 {
		t.Fatalf("case = %+v packet = %+v", draft.Cases[0], packets[0])
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"itemRef"`, `"evidenceRef"`, `"sourceRef"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("draft omits provenance field %s: %s", want, raw)
		}
	}
	for _, retired := range []string{`"itemUrl"`, `"evidenceUrl"`, `"sourceUrl"`} {
		if bytes.Contains(raw, []byte(retired)) {
			t.Fatalf("draft preserves retired provenance field %s: %s", retired, raw)
		}
	}
	entries, err := os.ReadDir(opts.derivativesRoot)
	if err != nil || len(entries) != 2 {
		t.Fatalf("derivatives = %v, %v", entries, err)
	}
}

func TestPrepareDevelopmentUsesEveryApprovedRowAndLeavesHeldRowsInert(t *testing.T) {
	opts, deriver := preparationFixture(t)
	mutatePreparationFixture(t, opts, func(inv *fillercorpus.Inventory, decisions []fillercorpus.RightsDecision, plan *preparationPlan) {
		inv.Cases[0].Creator, inv.Cases[0].Campaign, inv.Cases[0].SourceFamily = nil, "", ""
		decisions[1].Decision, decisions[1].Redistributable = "held", false
		plan.Cases = plan.Cases[:1]
		plan.SliceGates[0].MinCases = 1
	})
	opts.minItems, opts.maxItems = 1, 1
	draft, packets, err := prepare(t.Context(), opts, deriver)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Kind != fillereval.CorpusDevelopmentSeed || len(draft.Cases) != 1 || len(packets) != 1 || draft.Cases[0].Provenance.Campaign != "" {
		t.Fatalf("development draft = %+v, packets = %d", draft, len(packets))
	}
}

func TestPrepareCertificationRejectsHeldInventoryRows(t *testing.T) {
	opts, deriver := preparationFixture(t)
	mutatePreparationFixture(t, opts, func(_ *fillercorpus.Inventory, decisions []fillercorpus.RightsDecision, plan *preparationPlan) {
		addHoldoutContracts(decisions)
		decisions[1].Decision, decisions[1].Redistributable = "held", false
		plan.Kind = fillereval.CorpusCertification
		plan.Cases[1].Split = fillereval.SplitHoldout
		plan.SliceGates[0].MinAccuracyLower = .5
	})
	opts.kind = fillereval.CorpusCertification
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "requires every inventory case to be approved") {
		t.Fatalf("certification held-row error = %v", err)
	}
}

func TestPrepareCertificationRejectsSchemaThreeRightsApprovals(t *testing.T) {
	opts, deriver := preparationFixture(t)
	mutatePreparationFixture(t, opts, func(_ *fillercorpus.Inventory, _ []fillercorpus.RightsDecision, plan *preparationPlan) {
		plan.Kind = fillereval.CorpusCertification
		plan.Cases[1].Split = fillereval.SplitHoldout
		plan.SliceGates[0].MinAccuracyLower = .5
	})
	opts.kind = fillereval.CorpusCertification
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "legacy or mismatched certification rights decision") {
		t.Fatalf("schema-v3 certification error = %v", err)
	}
}

func TestPrepareRejectsQuarantineAuthorityForEveryCorpusProfile(t *testing.T) {
	for _, kind := range []fillereval.CorpusKind{fillereval.CorpusDevelopmentSeed, fillereval.CorpusCertification} {
		t.Run(string(kind), func(t *testing.T) {
			opts, deriver := preparationFixture(t)
			mutatePreparationFixture(t, opts, func(_ *fillercorpus.Inventory, decisions []fillercorpus.RightsDecision, plan *preparationPlan) {
				for index := range decisions {
					decisions[index].Redistributable = false
					decisions[index].QuarantineContract = &fillercorpus.QuarantineAcquisitionContract{
						SchemaVersion:  fillercorpus.QuarantineAcquisitionContractSchemaVersion,
						Purpose:        fillercorpus.QuarantinePurposeLocalInspection,
						CopyAndStorage: true, LocalTechnicalInspection: true,
					}
				}
				if kind == fillereval.CorpusCertification {
					plan.Kind = kind
					plan.Cases[1].Split = fillereval.SplitHoldout
					plan.SliceGates[0].MinAccuracyLower = .5
				}
			})
			opts.kind = kind
			if _, _, err := prepare(t.Context(), opts, deriver); err == nil {
				t.Fatal("quarantine authority entered corpus preparation")
			}
		})
	}
}

func TestPrepareRejectsRetiredSchemaThreePlan(t *testing.T) {
	opts, deriver := preparationFixture(t)
	mutatePreparationFixture(t, opts, func(_ *fillercorpus.Inventory, _ []fillercorpus.RightsDecision, plan *preparationPlan) {
		plan.SchemaVersion = 3
	})
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "plan schema") {
		t.Fatalf("retired schema error = %v", err)
	}
}

func TestCorpusKindForProfileRequiresExplicitCurrentProfile(t *testing.T) {
	for _, test := range []struct {
		profile string
		want    fillereval.CorpusKind
	}{
		{profileDevelopment, fillereval.CorpusDevelopmentSeed},
		{profileCertification, fillereval.CorpusCertification},
	} {
		got, err := corpusKindForProfile(test.profile)
		if err != nil || got != test.want {
			t.Fatalf("profile %q = %q, %v", test.profile, got, err)
		}
	}
	for _, profile := range []string{"", "development_seed", "legacy"} {
		if _, err := corpusKindForProfile(profile); err == nil {
			t.Fatalf("profile %q was accepted", profile)
		}
	}
}

func TestPrepareFailsClosedWithoutExactApprovalAndPublishesNoDerivatives(t *testing.T) {
	opts, deriver := preparationFixture(t)
	raw, err := os.ReadFile(opts.approvalsPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"inventorySha256":"`), []byte(`"inventorySha256":"bad`), 1)
	if err := os.WriteFile(opts.approvalsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "not bound to this inventory") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(opts.derivativesRoot); !os.IsNotExist(err) {
		t.Fatalf("partial derivatives were published: %v", err)
	}
}

func TestPrepareRemoteMediaRequiresExactInspectionBinding(t *testing.T) {
	opts, deriver, _ := remotePreparationFixture(t)
	decisions, err := readJSONL[fillercorpus.RightsDecision](opts.approvalsPath)
	if err != nil {
		t.Fatal(err)
	}
	decisions[0].QuarantineInspection = nil
	if err := writeJSONL(opts.approvalsPath, decisions); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "does not bind the exact quarantine inspection") {
		t.Fatalf("unbound remote decision error = %v", err)
	}
	if _, err := os.Stat(opts.derivativesRoot); !os.IsNotExist(err) {
		t.Fatalf("unbound authority published derivatives: %v", err)
	}
}

func TestPrepareRejectsPostInspectionSourceMutationBeforeCreatingOutput(t *testing.T) {
	opts, deriver, remotePath := remotePreparationFixture(t)
	media, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range media {
		media[index] ^= 0xff
	}
	if err := os.WriteFile(remotePath, media, 0o600); err != nil {
		t.Fatal(err)
	}
	outputParent := filepath.Join(filepath.Dir(opts.derivativesRoot), "authority-preflight-output")
	opts.derivativesRoot = filepath.Join(outputParent, "derivatives")
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "source bytes differ from quarantine inspection") {
		t.Fatalf("mutated remote source error = %v", err)
	}
	if _, err := os.Stat(outputParent); !os.IsNotExist(err) {
		t.Fatalf("authority failure created output directories: %v", err)
	}
}

func TestPrepareRejectsPerceptualFamilySplitAcrossClusters(t *testing.T) {
	opts, deriver := preparationFixture(t)
	deriver.alternateFrame = deriver.frame
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "perceptually related") {
		t.Fatalf("perceptual family leakage error = %v", err)
	}
}

func TestPrepareRejectsCampaignAuthoredAfterAcquisition(t *testing.T) {
	opts, deriver := preparationFixture(t)
	mutatePreparationFixture(t, opts, func(inv *fillercorpus.Inventory, decisions []fillercorpus.RightsDecision, plan *preparationPlan) {
		addHoldoutContracts(decisions)
		inv.Cases[0].Campaign = ""
		plan.Kind = fillereval.CorpusCertification
		plan.Cases[1].Split = fillereval.SplitHoldout
		plan.SliceGates[0].MinAccuracyLower = .5
	})
	opts.kind = fillereval.CorpusCertification
	if _, _, err := prepare(t.Context(), opts, deriver); err == nil || !strings.Contains(err.Error(), "incomplete acquisition provenance") {
		t.Fatalf("acquisition provenance error = %v", err)
	}
}

func addHoldoutContracts(decisions []fillercorpus.RightsDecision) {
	for index := range decisions {
		decisions[index].WorksheetSchemaVersion = fillercorpus.HoldoutRightsWorksheetSchemaVersion
		decisions[index].HoldoutContract = validHoldoutContract(decisions[index].CaseID)
	}
}

func validHoldoutContract(id string) *fillercorpus.HoldoutRightsContract {
	return &fillercorpus.HoldoutRightsContract{
		SchemaVersion: fillercorpus.HoldoutRightsContractSchemaVersion,
		AgreementID:   "agreement-v1", AgreementSHA256: strings.Repeat("a", 64), ScheduleID: id, ScheduleSHA256: strings.Repeat("b", 64),
		SignerAuthorityStatus: fillercorpus.RightsStatusCleared, SignerAuthorityEvidenceSHA256: strings.Repeat("c", 64), ProcessorID: "processor-v1", ProcessorTermsSHA256: strings.Repeat("d", 64),
		Grants:                       fillercorpus.HoldoutRightsGrants{CommercialEvaluation: true, CopyAndStorage: true, TechnicalModification: true, EvidenceExtraction: true, ProviderTransfer: true},
		EmbeddedRights:               fillercorpus.EmbeddedRightsStatus{Music: fillercorpus.RightsStatusNotPresent, PerformersAndVoices: fillercorpus.RightsStatusCleared, StockAndArtwork: fillercorpus.RightsStatusNotPresent, Trademarks: fillercorpus.RightsStatusCleared, PrivacyAndPublicity: fillercorpus.RightsStatusCleared, Locations: fillercorpus.RightsStatusNotPresent},
		EmbeddedRightsEvidenceSHA256: strings.Repeat("e", 64), RedistributionScope: fillercorpus.RedistributionMasterAndDerivatives, Territory: fillercorpus.RightsTerritoryWorldwide, Term: fillercorpus.RightsTermPerpetualIrrevocable, Withdrawal: fillercorpus.RightsWithdrawalDefectRetirement,
	}
}

func TestRealDeriverMeasuresAndBoundsShippingMediaDerivatives(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	media := filepath.Join(t.TempDir(), "clip.mp4")
	output, err := exec.Command(ffmpeg,
		"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=10:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", media).CombinedOutput()
	if err != nil {
		t.Fatalf("create fixture: %v: %s", err, output)
	}
	deriver := realDeriver{ffmpeg: ffmpeg, tools: mediatools.NewFFmpegTools(ffmpeg, ffprobe, "", "", "")}
	measurement, err := deriver.Measure(t.Context(), media, 0, 2_000)
	if err != nil || !measurement.Usable || measurement.DurationMS < 2_000 {
		t.Fatalf("measurement = %+v, %v", measurement, err)
	}
	frames, err := deriver.Frames(t.Context(), media, 0, 2_000)
	if err != nil || len(frames) != 4 {
		t.Fatalf("frames = %d, %v", len(frames), err)
	}
	video, err := deriver.Video(t.Context(), media, 0, 2_000)
	if err != nil || video.Width != 640 || video.Height != 360 || video.DurationMS <= 0 || video.DurationMS > 3_000 || len(video.Data) > mediatools.HostedVideoMaxBytes {
		t.Fatalf("video = %dx%d duration=%d bytes=%d, %v", video.Width, video.Height, video.DurationMS, len(video.Data), err)
	}
	audio, err := deriver.Audio(t.Context(), media, 0, 2_000)
	if err != nil || audio.DurationMS <= 0 || audio.DurationMS > 3_000 || len(audio.Data) <= 44 {
		t.Fatalf("audio duration=%d bytes=%d, %v", audio.DurationMS, len(audio.Data), err)
	}
}

func preparationFixture(t *testing.T) (options, fakeDeriver) {
	t.Helper()
	dir := t.TempDir()
	localRoot, remoteRoot := filepath.Join(dir, "local"), filepath.Join(dir, "remote")
	if err := os.MkdirAll(localRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(remoteRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	snapshot := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	authority, collection, role := "direct-license", "fixture", "commercial"
	captureID := fillercorpus.NewCaptureID(authority, collection, role)
	inv := fillercorpus.Inventory{SchemaVersion: fillercorpus.InventorySchemaVersion, SnapshotAt: snapshot, Captures: []fillercorpus.Capture{{CaptureID: captureID, Transport: fillercorpus.TransportLocal, Authority: authority, Collection: collection, RoleHint: role, SnapshotAt: snapshot, MaxPredictedMediaBytes: 1024, MaxWallTimeMS: 1000}}}
	for index, id := range []string{"case-a", "case-b"} {
		rel := id + ".mp4"
		media := []byte("unique media " + id)
		if err := os.WriteFile(filepath.Join(localRoot, rel), media, 0o600); err != nil {
			t.Fatal(err)
		}
		inv.Captures[0].PredictedMediaBytes += int64(len(media))
		inv.Cases = append(inv.Cases, fillercorpus.InventoryCase{CaseID: fillercorpus.CaseID(authority, id), CaptureIDs: []string{captureID}, Authority: authority, ItemID: id, Title: "Case " + id, RoleHints: []string{role}, Creator: []string{"creator-" + id}, Campaign: "campaign-" + id, SourceFamily: "family-" + id, RightsAssertions: []string{"signed redistribution grant"}, MetadataRetrievedAt: snapshot, MetadataSHA256: strings.Repeat(string(rune('a'+index)), 64), Evidence: []fillercorpus.InventoryEvidence{{Kind: "rights", Path: "rights.txt", Bytes: 1, SHA256: strings.Repeat("c", 64)}, {Kind: "provenance", Path: "provenance.txt", Bytes: 1, SHA256: strings.Repeat("d", 64)}}, Representation: fillercorpus.InventoryRepresentation{Transport: fillercorpus.TransportLocal, Name: rel, Path: rel, MIMEType: "video/mp4", Bytes: int64(len(media)), SHA256: fillercorpus.InventorySHA256(media)}})
	}
	inventoryRaw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	inventoryPath := filepath.Join(dir, "inventory.json")
	if err := os.WriteFile(inventoryPath, inventoryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fillercorpus.InventorySHA256(inventoryRaw)
	approvalsPath := filepath.Join(dir, "approvals.jsonl")
	var approvals bytes.Buffer
	encoder := json.NewEncoder(&approvals)
	for _, item := range inv.Cases {
		if err := encoder.Encode(fillercorpus.RightsDecision{WorksheetSchemaVersion: fillercorpus.RightsWorksheetSchemaVersion, InventorySHA256: digest, CaseID: item.CaseID, CaptureIDs: item.CaptureIDs, Authority: item.Authority, ItemID: item.ItemID, MetadataSHA256: item.MetadataSHA256, ReviewerID: "rights-reviewer", ReviewedAt: snapshot.Add(time.Hour), Decision: "approved", Basis: "signed grant inspected", Redistributable: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(approvalsPath, approvals.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := preparationPlan{SchemaVersion: preparationSchemaVersion, Kind: fillereval.CorpusDevelopmentSeed, CorpusVersion: "fixture-v1", EvidenceVersion: "evidence-v1", SliceGates: []fillereval.SliceGate{{Slice: "all", MinCases: 2, MinAccuracy: .9}}, Cases: []plannedCase{{CaseID: inv.Cases[0].CaseID, Split: fillereval.SplitDevelopment, Cluster: "cluster-a", SegmentDurationMS: 30_000, VideoDurationMS: 10_000}, {CaseID: inv.Cases[1].CaseID, Split: fillereval.SplitDevelopment, Cluster: "cluster-b", SegmentDurationMS: 30_000, VideoDurationMS: 10_000}}}
	planPath := filepath.Join(dir, "plan.json")
	planRaw, _ := json.Marshal(plan)
	if err := os.WriteFile(planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, img, nil); err != nil {
		t.Fatal(err)
	}
	alternate := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 64; x++ {
			alternate.Set(x, y, color.RGBA{R: uint8(255 - x), G: uint8(255 - y), B: uint8((x + y) % 255), A: 255})
		}
	}
	var alternateJPEG bytes.Buffer
	if err := jpeg.Encode(&alternateJPEG, alternate, nil); err != nil {
		t.Fatal(err)
	}
	return options{inventoryPath: inventoryPath, approvalsPath: approvalsPath, planPath: planPath, localRoot: localRoot, remoteRoot: remoteRoot, derivativesRoot: filepath.Join(dir, "derivatives"), kind: fillereval.CorpusDevelopmentSeed, preparedAt: snapshot.Add(2 * time.Hour), minItems: 2, maxItems: 2, maxInputBytes: 1024, maxOutputBytes: 1 << 20, maxWallTime: time.Minute}, fakeDeriver{frame: jpegBytes.Bytes(), alternateFrame: alternateJPEG.Bytes()}
}

func mutatePreparationFixture(t *testing.T, opts options, mutate func(*fillercorpus.Inventory, []fillercorpus.RightsDecision, *preparationPlan)) {
	t.Helper()
	inventoryRaw, err := os.ReadFile(opts.inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var inv fillercorpus.Inventory
	if err := json.Unmarshal(inventoryRaw, &inv); err != nil {
		t.Fatal(err)
	}
	decisions, err := readJSONL[fillercorpus.RightsDecision](opts.approvalsPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan preparationPlan
	if err := readStrictJSON(opts.planPath, &plan); err != nil {
		t.Fatal(err)
	}
	mutate(&inv, decisions, &plan)
	inventoryRaw, err = json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.inventoryPath, inventoryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fillercorpus.InventorySHA256(inventoryRaw)
	for i := range decisions {
		decisions[i].InventorySHA256 = digest
	}
	if err := writeJSONL(opts.approvalsPath, decisions); err != nil {
		t.Fatal(err)
	}
	planRaw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func remotePreparationFixture(t *testing.T) (options, fakeDeriver, string) {
	t.Helper()
	opts, deriver := preparationFixture(t)
	inventoryRaw, err := os.ReadFile(opts.inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var inv fillercorpus.Inventory
	if err := json.Unmarshal(inventoryRaw, &inv); err != nil {
		t.Fatal(err)
	}
	decisions, err := readJSONL[fillercorpus.RightsDecision](opts.approvalsPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan preparationPlan
	if err := readStrictJSON(opts.planPath, &plan); err != nil {
		t.Fatal(err)
	}

	item := &inv.Cases[0]
	oldPath := filepath.Join(opts.localRoot, item.Representation.Path)
	media, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	directCapture := &inv.Captures[0]
	directCapture.PredictedMediaBytes -= int64(len(media))
	remoteAuthority, collection, role := "archive.org/prelinger", "prelinger", "commercial"
	remoteCaptureID := fillercorpus.NewCaptureID(remoteAuthority, collection, role)
	inv.Captures = append(inv.Captures, fillercorpus.Capture{
		CaptureID: remoteCaptureID, Transport: fillercorpus.TransportHTTPS, Authority: remoteAuthority, Collection: collection, RoleHint: role, SnapshotAt: inv.SnapshotAt,
		MaxRequests: 10, RequestsUsed: 1, MaxResponseBytes: 10_000, ResponseBytes: 100, MaxPredictedMediaBytes: 10_000, PredictedMediaBytes: int64(len(media)), MaxWallTimeMS: 1_000, WallTimeMS: 10,
	})
	item.CaseID = fillercorpus.CaseID(remoteAuthority, item.ItemID)
	item.CaptureIDs = []string{remoteCaptureID}
	item.Authority = remoteAuthority
	item.Collection = []string{collection}
	item.ItemURL = "https://archive.org/details/" + item.ItemID
	item.MetadataURL = "https://archive.org/metadata/" + item.ItemID
	item.AllowedMediaHosts = []string{"archive.org", ".archive.org"}
	item.Representation.Transport = fillercorpus.TransportHTTPS
	item.Representation.URL = "https://archive.org/download/" + item.ItemID + "/" + item.Representation.Name
	item.Representation.Path = ""
	item.Representation.SHA256 = ""
	remoteName := fillercorpus.InventorySHA256([]byte(item.CaseID))[:16] + filepath.Ext(item.Representation.Name)
	remotePath := filepath.Join(opts.remoteRoot, remoteName)
	if err := os.WriteFile(remotePath, media, 0o600); err != nil {
		t.Fatal(err)
	}
	decisions[0].CaseID = item.CaseID
	decisions[0].CaptureIDs = append([]string(nil), item.CaptureIDs...)
	decisions[0].Authority = item.Authority
	plan.Cases[0].CaseID = item.CaseID

	inventoryRaw, err = json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if failures := fillercorpus.ValidateInventory(inv); len(failures) != 0 {
		t.Fatalf("remote inventory: %v", failures)
	}
	if err := os.WriteFile(opts.inventoryPath, inventoryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fillercorpus.InventorySHA256(inventoryRaw)
	reportRaw := testkit.FillerQuarantineReport(t, inventoryRaw, map[string]string{item.CaseID: fillerquarantine.DispositionEligibleForRightsReview}, map[string]string{item.CaseID: fillercorpus.InventorySHA256(media)})
	reportPath := filepath.Join(filepath.Dir(opts.inventoryPath), "inspection.json")
	if err := os.WriteFile(reportPath, reportRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	authority, err := fillerquarantine.OpenRightsEligibility(inventoryRaw, reportRaw)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := authority.Selected(1, len(inv.Cases))
	if err != nil {
		t.Fatal(err)
	}
	bindingByCase := make(map[string]*fillercorpus.QuarantineInspectionCaseBinding, len(selection.Cases))
	for _, candidate := range selection.Cases {
		bindingByCase[candidate.Inventory.CaseID] = candidate.QuarantineInspection
	}
	for index := range decisions {
		decisions[index].InventorySHA256 = digest
		decisions[index].QuarantineInspection = bindingByCase[decisions[index].CaseID]
	}
	if err := writeJSONL(opts.approvalsPath, decisions); err != nil {
		t.Fatal(err)
	}
	planRaw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	opts.quarantineInspectionPath = reportPath
	return opts, deriver, remotePath
}
