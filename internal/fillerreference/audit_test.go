package fillerreference_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/filleradmission"
	"github.com/loomarr/loomarr/internal/fillerbakeoff"
	"github.com/loomarr/loomarr/internal/fillercorpus"
	"github.com/loomarr/loomarr/internal/fillereval"
	"github.com/loomarr/loomarr/internal/fillerreference"
)

func TestBuildAuditDerivesInputIdentitiesFromOwnedRawArtifacts(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	raw := rawAuditInputs(t, manifest, packets, mapping, downloads, review)
	audit, err := fillerreference.BuildAudit(raw, time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if audit.Inputs.ManifestSHA256 != fillerreference.SHA256(raw.Manifest) ||
		audit.Inputs.PacketsSHA256 != fillerreference.SHA256(raw.Packets) ||
		audit.Inputs.MappingSHA256 != fillerreference.SHA256(raw.Mapping) ||
		audit.Inputs.DownloadLedgerSHA256 != fillerreference.SHA256(raw.DownloadLedger) ||
		audit.Inputs.ContentReviewSHA256 != fillerreference.SHA256(raw.ContentReview) {
		t.Fatalf("audit accepted caller-independent identities: %+v", audit.Inputs)
	}
}

func TestBuildAuditAcceptsPurposeBoundDevelopmentLedgerAndRejectsQuarantine(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	downloads.SchemaVersion = 2
	downloads.Profile = fillercorpus.RightsProfileDevelopment
	if _, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("schema-v2 development ledger: %v", err)
	}
	downloads.Profile = fillercorpus.RightsProfileQuarantine
	if _, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("quarantine ledger entered reference audit")
	}
}

func TestBuildAuditKeepsAdmissionEditorialAndAcceptanceSeparate(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	audit, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if audit.Summary.Cases != 300 || audit.Summary.Candidates != 295 || audit.Summary.Holds != 2 || audit.Summary.Excluded != 3 {
		t.Fatalf("summary = %+v", audit.Summary)
	}
	again, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("same bound inputs and generation time produced different audits")
	}
	byID := make(map[string]fillerreference.Case, len(audit.Cases))
	for _, item := range audit.Cases {
		byID[item.CaseID] = item
		if item.ReadinessGrade != "ungraded" || !item.NeedsFullVideoInspection {
			t.Fatalf("%s prematurely assigned editorial truth: %+v", item.CaseID, item)
		}
	}
	kix := byID["archive.org/vhscommercials/y-2mate.is-kix-commercial-on-pbs-kids-tv-funding-sponsor-2001-hd-version-2-3-kjd"]
	if kix.Disposition != fillerreference.DispositionHold || !slices.Equal(kix.ProposedProductionTags, []string{"commercial"}) || !slices.Contains(kix.ReasonCodes, fillerreference.ReasonCommercialProductMissing) {
		t.Fatalf("ungrounded Kix = %+v", kix)
	}
	for _, id := range []string{
		"archive.org/movie_trailers_unsorted/TheInvisibleBoy-Trailer",
		"archive.org/movie_trailers_picfixer/CodeOfTheCactusTrailer",
		"archive.org/classic_tv_commercials/VID20181114WA0037",
	} {
		got := byID[id]
		if got.Disposition != fillerreference.DispositionExclude {
			t.Fatalf("non-filler %s = %+v", id, got)
		}
	}
	if got := byID["archive.org/movie_trailers_picfixer/CodeOfTheCactusTrailer"]; !slices.Contains(got.ReasonCodes, fillerreference.ReasonRoleNotFiller) {
		t.Fatalf("trailer-named programme excerpt = %+v", got)
	}
	if got := byID["archive.org/classic_tv_commercials/VID20181114WA0037"]; !slices.Contains(got.ReasonCodes, fillerreference.ReasonNonBroadcastMaterial) || got.SemanticTruth != fillereval.TruthEligible || got.ContentRole != filleradmission.RoleInterstitial || got.ContentFinding == nil {
		t.Fatalf("content-reviewed non-broadcast material = %+v", got)
	}
	blows := byID["archive.org/classic_tv_commercials/BLOWSFATBELLIE"]
	if blows.Disposition != fillerreference.DispositionHold || !slices.Contains(blows.ReasonCodes, fillerreference.ReasonSourceCoverageIncomplete) {
		t.Fatalf("truncated source coverage = %+v", blows)
	}
}

func TestBuildAuditFailsClosedOnUnboundPacket(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	delete(packets, manifest.Cases[0].ID)
	_, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now())
	if err == nil {
		t.Fatal("missing packet accepted")
	}
}

func TestBuildAuditRejectsAdversarialArtifactBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fillereval.Manifest, map[string]fillerbakeoff.Packet, *fillerreference.MappingArtifact, *fillerreference.DownloadLedger, *fillerreference.ContentReviewArtifact)
	}{
		{"invalid full development manifest", func(m *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			m.SliceGates = nil
		}},
		{"extra packet", func(_ *fillereval.Manifest, p map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			p["extra"] = fillerbakeoff.Packet{}
		}},
		{"schema-invalid packet", func(m *fillereval.Manifest, p map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			packet := p[m.Cases[0].ID]
			packet.SchemaVersion++
			p[m.Cases[0].ID] = packet
			rebindPacket(m, packet)
		}},
		{"packet digest mismatch", func(m *fillereval.Manifest, p map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			packet := p[m.Cases[0].ID]
			packet.Facts[0].Value = filleradmission.UsabilityUnusable
			p[m.Cases[0].ID] = packet
		}},
		{"duplicate media fact", func(m *fillereval.Manifest, p map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			packet := p[m.Cases[0].ID]
			duplicate := packet.Facts[0]
			duplicate.ID = "media-duplicate"
			packet.Facts = append(packet.Facts, duplicate)
			p[m.Cases[0].ID] = packet
			rebindPacket(m, packet)
		}},
		{"extra mapping", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, mapping *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			mapping.Mappings = append(mapping.Mappings, fillerreference.ProductMapping{ReviewerLabel: "extra", Occurrences: 1, ProductionCategories: []string{"cereal"}, Basis: "not in manifest"})
		}},
		{"extra download", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, downloads *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			downloads.Cases = append(downloads.Cases, downloads.Cases[0])
		}},
		{"rights approval mismatch", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, downloads *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			downloads.Cases[0].Approval.Decision = "held"
		}},
		{"acquired source byte mismatch", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, downloads *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			downloads.Cases[0].Representation.Bytes++
		}},
		{"duplicate acquired content identity", func(m *fillereval.Manifest, p map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, downloads *fillerreference.DownloadLedger, _ *fillerreference.ContentReviewArtifact) {
			first, second := 5, 6
			m.Cases[second].ContentSHA256 = m.Cases[first].ContentSHA256
			relockLabels(&m.Cases[second], m.LockedAt)
			packet := p[m.Cases[second].ID]
			packet.ContentSHA256 = m.Cases[first].ContentSHA256
			p[packet.CaseID] = packet
			rebindPacket(m, packet)
			downloads.Cases[second].ContentSHA256 = m.Cases[first].ContentSHA256
		}},
		{"wrong content review manifest", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, review *fillerreference.ContentReviewArtifact) {
			review.SourceManifestSHA256 = strings.Repeat("f", 64)
		}},
		{"duplicate content review finding", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, review *fillerreference.ContentReviewArtifact) {
			review.Findings = append(review.Findings, review.Findings[0])
		}},
		{"unknown content review identity", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].ContentSHA256 = strings.Repeat("f", 64)
		}},
		{"content review cites decoder", func(_ *fillereval.Manifest, _ map[string]fillerbakeoff.Packet, _ *fillerreference.MappingArtifact, _ *fillerreference.DownloadLedger, review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].EvidenceRefs = []string{"decoder", "frame-a"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, packets, mapping, downloads, review := fixture()
			test.mutate(&manifest, packets, &mapping, &downloads, &review)
			if _, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now()); err == nil {
				t.Fatal("invalid artifact binding accepted")
			}
		})
	}
}

func TestBuildAuditRejectsUnsupportedContentReviewAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fillerreference.ContentReviewArtifact)
	}{
		{"schema", func(review *fillerreference.ContentReviewArtifact) { review.SchemaVersion++ }},
		{"kind", func(review *fillerreference.ContentReviewArtifact) { review.Kind = "filler_reference_admission_review" }},
		{"contract", func(review *fillerreference.ContentReviewArtifact) { review.ContractVersion = "different-contract" }},
		{"reviewer", func(review *fillerreference.ContentReviewArtifact) { review.ReviewerID = " " }},
		{"admission disposition", func(review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].Disposition = string(fillerreference.DispositionCandidate)
		}},
		{"open reason", func(review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].ReasonCode = fillerreference.ReasonSemanticInvalid
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, packets, mapping, downloads, review := fixture()
			test.mutate(&review)
			if _, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now()); err == nil {
				t.Fatal("unsupported content-review authority accepted")
			}
		})
	}
}

func TestBuildAuditRejectsInvalidContentReviewEvidenceReferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fillereval.Manifest, *fillerreference.ContentReviewArtifact)
	}{
		{"one row", func(_ *fillereval.Manifest, review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].EvidenceRefs = []string{"frame-a"}
		}},
		{"duplicate row", func(_ *fillereval.Manifest, review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].EvidenceRefs = []string{"frame-a", "frame-a"}
		}},
		{"noncanonical order", func(_ *fillereval.Manifest, review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].EvidenceRefs = []string{"transcript-a", "frame-a"}
		}},
		{"unknown row", func(_ *fillereval.Manifest, review *fillerreference.ContentReviewArtifact) {
			review.Findings[0].EvidenceRefs = []string{"frame-a", "missing"}
		}},
		{"unsupported row kind", func(manifest *fillereval.Manifest, _ *fillerreference.ContentReviewArtifact) {
			manifest.Cases[4].Evidence[0].Kind = string(filleradmission.KindDecoder)
			relockLabels(&manifest.Cases[4], manifest.LockedAt)
		}},
		{"ambiguous row id", func(manifest *fillereval.Manifest, _ *fillerreference.ContentReviewArtifact) {
			manifest.Cases[4].Evidence = append(manifest.Cases[4].Evidence, manifest.Cases[4].Evidence[0])
			relockLabels(&manifest.Cases[4], manifest.LockedAt)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, packets, mapping, downloads, review := fixture()
			test.mutate(&manifest, &review)
			if _, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now()); err == nil {
				t.Fatal("invalid content-review evidence references accepted")
			}
		})
	}
}

func TestBuildAuditRejectsFutureContentReview(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	generatedAt := time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)
	review.ReviewedAt = generatedAt.Add(time.Second)
	if _, err := buildAudit(t, manifest, packets, mapping, downloads, review, generatedAt); err == nil {
		t.Fatal("future content review accepted")
	}
}

func TestBuildAuditExcludesZeroDurationMedia(t *testing.T) {
	tests := []struct {
		name     string
		location string
	}{
		{"segment", "source_duration_ms=11500;segment_start_ms=0;segment_duration_ms=0;no_video=false;no_audio=false;black_percent=0;silence_percent=0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, packets, mapping, downloads, review := fixture()
			id := manifest.Cases[4].ID
			packet := packets[id]
			packet.Facts[0].Location = test.location
			packets[id] = packet
			rebindPacket(&manifest, packet)
			audit, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			got := caseByID(t, audit, id)
			if got.Disposition != fillerreference.DispositionExclude || !slices.Contains(got.ReasonCodes, fillerreference.ReasonMediaUnusable) {
				t.Fatalf("zero %s duration = %+v", test.name, got)
			}
		})
	}
}

func TestBuildAuditFailsClosedWithoutAValidAudibleVideoPresentation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*fillerbakeoff.Packet)
		wantError bool
	}{
		{"no video", func(packet *fillerbakeoff.Packet) {
			packet.Facts[0].Location = strings.Replace(packet.Facts[0].Location, "no_video=false", "no_video=true", 1)
		}, false},
		{"no audio", func(packet *fillerbakeoff.Packet) {
			packet.Facts[0].Location = strings.Replace(packet.Facts[0].Location, "no_audio=false", "no_audio=true", 1)
		}, false},
		{"missing video presentation", func(packet *fillerbakeoff.Packet) { packet.Signals = nil }, false},
		{"invalid video presentation", func(packet *fillerbakeoff.Packet) { packet.Signals[0].DurationMS = 0 }, false},
		{"positive segment with zero source", func(packet *fillerbakeoff.Packet) {
			packet.Facts[0].Location = "source_duration_ms=0;segment_start_ms=0;segment_duration_ms=11000;no_video=false;no_audio=false;black_percent=0;silence_percent=0"
		}, true},
		{"segment starts beyond source", func(packet *fillerbakeoff.Packet) {
			packet.Facts[0].Location = "source_duration_ms=30500;segment_start_ms=31000;segment_duration_ms=10000;no_video=false;no_audio=false;black_percent=0;silence_percent=0"
		}, true},
		{"segment overruns source", func(packet *fillerbakeoff.Packet) {
			packet.Facts[0].Location = "source_duration_ms=30500;segment_start_ms=25000;segment_duration_ms=10000;no_video=false;no_audio=false;black_percent=0;silence_percent=0"
		}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, packets, mapping, downloads, review := fixture()
			id := manifest.Cases[4].ID
			packet := packets[id]
			test.mutate(&packet)
			packets[id] = packet
			rebindPacket(&manifest, packet)
			audit, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now())
			if test.wantError {
				if err == nil {
					t.Fatal("invalid segment bounds accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := caseByID(t, audit, id)
			if got.Disposition != fillerreference.DispositionExclude || !slices.Contains(got.ReasonCodes, fillerreference.ReasonMediaUnusable) {
				t.Fatalf("invalid audible video presentation = %+v", got)
			}
		})
	}
}

func TestBuildAuditRequiresContentReviewDigestIdentity(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	raw := rawAuditInputs(t, manifest, packets, mapping, downloads, review)
	raw.ContentReview = nil
	if _, err := fillerreference.BuildAudit(raw, time.Now()); err == nil {
		t.Fatal("missing raw content-review artifact accepted")
	}
}

func TestBuildAuditExcludesSubTenSecondMediaAtTheProductionScanBoundary(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	id := manifest.Cases[4].ID
	packet := packets[id]
	packet.Facts[0].Location = "source_duration_ms=9500;segment_start_ms=0;segment_duration_ms=9000;no_video=false;no_audio=false;black_percent=0;silence_percent=0"
	packets[id] = packet
	rebindPacket(&manifest, packet)
	audit, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Cases {
		if item.CaseID == id && (item.Disposition != fillerreference.DispositionExclude || !slices.Contains(item.ReasonCodes, fillerreference.ReasonDurationTooShort)) {
			t.Fatalf("short clip = %+v", item)
		}
	}
}

func TestBuildAuditHoldsEvaluationVocabularyThatDoesNotResolveInLiveTaxonomy(t *testing.T) {
	manifest, packets, mapping, downloads, review := fixture()
	manifest.Cases[0].Taxonomy["product"] = []string{"cleaner"}
	relockLabels(&manifest.Cases[0], manifest.LockedAt)
	mapping.Mappings = []fillerreference.ProductMapping{{
		ReviewerLabel: "cleaner", Occurrences: 1, ProductionCategories: []string{"detergent"}, Basis: "evaluation mapping",
	}}
	mapping.SourceProductAssignments, mapping.UniqueReviewerLabels, mapping.MappedAssignments = 1, 1, 1
	audit, err := buildAudit(t, manifest, packets, mapping, downloads, review, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var got fillerreference.Case
	for _, item := range audit.Cases {
		if item.CaseID == manifest.Cases[0].ID {
			got = item
			break
		}
	}
	if got.Disposition != fillerreference.DispositionHold || !slices.Equal(got.UnresolvedTaxonomyValues, []string{"detergent"}) || !slices.Contains(got.ReasonCodes, fillerreference.ReasonTaxonomyUnresolved) {
		t.Fatalf("unresolved live taxonomy = %+v", got)
	}
}

func fixture() (fillereval.Manifest, map[string]fillerbakeoff.Packet, fillerreference.MappingArtifact, fillerreference.DownloadLedger, fillerreference.ContentReviewArtifact) {
	const (
		kix       = "archive.org/vhscommercials/y-2mate.is-kix-commercial-on-pbs-kids-tv-funding-sponsor-2001-hd-version-2-3-kjd"
		invisible = "archive.org/movie_trailers_unsorted/TheInvisibleBoy-Trailer"
		cactus    = "archive.org/movie_trailers_picfixer/CodeOfTheCactusTrailer"
		blows     = "archive.org/classic_tv_commercials/BLOWSFATBELLIE"
		reviewed  = "archive.org/classic_tv_commercials/VID20181114WA0037"
	)
	manifest := fillereval.Manifest{
		SchemaVersion: fillereval.SchemaVersion, Kind: fillereval.CorpusDevelopmentSeed,
		CorpusVersion: "fixture", LockedAt: time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC),
		SliceGates: []fillereval.SliceGate{{Slice: "contract", MinCases: 300, MinAccuracy: .99}},
	}
	packets := make(map[string]fillerbakeoff.Packet, 300)
	downloads := fillerreference.DownloadLedger{SchemaVersion: 1, InventorySHA256: strings.Repeat("d", 64), GeneratedAt: manifest.LockedAt, MaxRequests: 1, MaxItems: 300, MaxBytes: 400_000}
	add := func(id string, truth fillereval.Truth, role string, product []string, sourceDuration, segmentDuration int64) {
		hash := fmt.Sprintf("%064x", len(manifest.Cases)+1)
		item := fillereval.Case{
			ID: id, Split: fillereval.SplitDevelopment, Cluster: id, ContentSHA256: hash,
			Source: "fixture/source", License: "CC0-1.0", Truth: truth, ContentRole: role,
			Provenance: fillereval.MediaProvenance{Authority: "archive.org/fixture", ItemID: id, ItemRef: "https://example.invalid/items/" + id, MetadataRetrievedAt: manifest.LockedAt, MetadataSHA256: strings.Repeat("e", 64), EvidenceRef: "https://example.invalid/evidence/" + id, LicenseURL: "https://example.invalid/license/" + id, RightsStatement: "fixture rights", RightsDecision: "approved", RightsReviewerID: "rights", RightsReviewedAt: manifest.LockedAt, Redistributable: true, SourceFilename: fmt.Sprintf("%03d.mp4", len(manifest.Cases)), SourceRef: "https://example.invalid/media/" + id, SourceBytes: 1000, SegmentDurationMS: segmentDuration},
			Taxonomy:   map[string][]string{},
			Slices:     []string{"contract"}, Evidence: []fillereval.Evidence{{ID: "role", Kind: "review", Claim: "content_role", Value: role, Provenance: "fixture"}},
		}
		if id == reviewed {
			item.Evidence = []fillereval.Evidence{
				{ID: "frame-a", Kind: string(filleradmission.KindFrame), Claim: "casual candid activity", Value: "person outdoors", Provenance: "bound/frame-a.jpg"},
				{ID: "transcript-a", Kind: string(filleradmission.KindTranscript), Claim: "casual speech", Value: "see you soon", Provenance: "bound/audio.wav#transcript"},
			}
		}
		if truth == fillereval.TruthAmbiguous {
			item.ReviewQuestion = "fixture ambiguity"
		}
		if truth == fillereval.TruthInvalid {
			item.RejectClass = fillereval.RejectSemantic
		}
		if len(product) > 0 {
			item.Taxonomy["product"] = product
		}
		packet := fillerbakeoff.Packet{
			SchemaVersion: fillerbakeoff.PacketSchemaVersion, CaseID: id, EvidenceVersion: fillerreference.PacketEvidenceVersion, ContentSHA256: hash,
			Facts: []filleradmission.Evidence{
				{ID: "media", Claim: filleradmission.ClaimMediaUsability, Value: filleradmission.UsabilityUsable, Kind: filleradmission.KindDecoder, Source: "decoder", Location: fmt.Sprintf("source_duration_ms=%d;segment_start_ms=0;segment_duration_ms=%d;no_video=false;no_audio=false;black_percent=0;silence_percent=0", sourceDuration, segmentDuration)},
				{ID: "license", Claim: filleradmission.ClaimSourceLicense, Value: filleradmission.EligibilityEligible, Kind: filleradmission.KindSourcePolicy, Source: "rights"},
			},
			Signals: []fillerbakeoff.Signal{{ID: "video", Kind: "video", Path: "video.mp4", SHA256: strings.Repeat("f", 64), Bytes: 1, DurationMS: segmentDuration, Width: 1280, Height: 720, ContentTypes: []string{"video/mp4"}}},
		}
		item.EvidenceSHA256 = fillerbakeoff.PacketSHA256(packet)
		relockLabels(&item, manifest.LockedAt)
		manifest.Cases = append(manifest.Cases, item)
		packets[id] = packet
		p := item.Provenance
		downloads.Cases = append(downloads.Cases, fillerreference.DownloadCase{CaseID: id, Authority: p.Authority, ItemID: p.ItemID, LicenseURL: p.LicenseURL, ItemURL: p.ItemRef, MetadataURL: p.EvidenceRef, MetadataRetrievedAt: p.MetadataRetrievedAt, MetadataSHA256: p.MetadataSHA256, Representation: fillercorpus.InventoryRepresentation{Name: p.SourceFilename, URL: p.SourceRef, Bytes: p.SourceBytes}, LocalFile: p.SourceFilename, ContentSHA256: hash, Approval: fillercorpus.RightsDecision{InventorySHA256: downloads.InventorySHA256, CaseID: id, CaptureIDs: []string{"fixture/capture/commercial"}, Authority: p.Authority, ItemID: p.ItemID, MetadataSHA256: p.MetadataSHA256, ReviewerID: p.RightsReviewerID, ReviewedAt: p.RightsReviewedAt, Decision: "approved", Basis: "fixture", Redistributable: true}, VerifiedAt: manifest.LockedAt})
		downloads.Bytes += p.SourceBytes
	}
	add(kix, fillereval.TruthEligible, filleradmission.RoleCommercial, nil, 14_860, 14_360)
	add(invisible, fillereval.TruthAmbiguous, filleradmission.RoleProgrammeExcerpt, nil, 146_646, 120_000)
	add(cactus, fillereval.TruthEligible, filleradmission.RoleProgrammeExcerpt, nil, 69_938, 69_438)
	add(blows, fillereval.TruthEligible, filleradmission.RolePromo, nil, 378_611, 120_000)
	add(reviewed, fillereval.TruthEligible, filleradmission.RoleInterstitial, nil, 12_213, 11_713)
	for len(manifest.Cases) < 300 {
		id := fmt.Sprintf("fixture/candidate-%03d", len(manifest.Cases))
		add(id, fillereval.TruthEligible, filleradmission.RolePromo, nil, 30_500, 30_000)
	}
	mapping := fillerreference.MappingArtifact{
		SchemaVersion: 1, MappingVersion: "mapping-v1", SourceManifestSHA256: strings.Repeat("a", 64),
	}
	review := fillerreference.ContentReviewArtifact{
		SchemaVersion: 1, Kind: fillerreference.ContentReviewKind, ContractVersion: fillerreference.ContractVersion,
		ReviewerID: "contract-reviewer", ReviewedAt: manifest.LockedAt, SourceManifestSHA256: strings.Repeat("a", 64),
		Findings: []fillerreference.ContentFinding{{
			ContentSHA256: manifest.Cases[4].ContentSHA256, Disposition: string(fillerreference.DispositionExclude),
			ReasonCode: fillerreference.ReasonNonBroadcastMaterial, Detail: "bound content is not broadcast material", EvidenceRefs: []string{"frame-a", "transcript-a"},
		}},
	}
	return manifest, packets, mapping, downloads, review
}

func relockLabels(item *fillereval.Case, lockedAt time.Time) {
	hash := fillereval.LabelSHA256(*item)
	item.LabelReviews = []fillereval.LabelReview{{ReviewerID: "reviewer-a", BatchID: "batch-a", ReviewedAt: lockedAt, Independent: true, SubmissionSHA256: hash}, {ReviewerID: "reviewer-b", BatchID: "batch-b", ReviewedAt: lockedAt, Independent: true, SubmissionSHA256: hash}}
}

func rebindPacket(manifest *fillereval.Manifest, packet fillerbakeoff.Packet) {
	for index := range manifest.Cases {
		if manifest.Cases[index].ID == packet.CaseID {
			manifest.Cases[index].EvidenceSHA256 = fillerbakeoff.PacketSHA256(packet)
			return
		}
	}
}

func buildAudit(t *testing.T, manifest fillereval.Manifest, packets map[string]fillerbakeoff.Packet, mapping fillerreference.MappingArtifact, downloads fillerreference.DownloadLedger, review fillerreference.ContentReviewArtifact, generatedAt time.Time) (fillerreference.Audit, error) {
	t.Helper()
	return fillerreference.BuildAudit(rawAuditInputs(t, manifest, packets, mapping, downloads, review), generatedAt)
}

func rawAuditInputs(t *testing.T, manifest fillereval.Manifest, packets map[string]fillerbakeoff.Packet, mapping fillerreference.MappingArtifact, downloads fillerreference.DownloadLedger, review fillerreference.ContentReviewArtifact) fillerreference.RawAuditInputs {
	t.Helper()
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := strings.Repeat("a", 64)
	if mapping.SourceManifestSHA256 == placeholder {
		mapping.SourceManifestSHA256 = fillerreference.SHA256(manifestRaw)
	}
	if review.SourceManifestSHA256 == placeholder {
		review.SourceManifestSHA256 = fillerreference.SHA256(manifestRaw)
	}
	ids := make([]string, 0, len(packets))
	for id := range packets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var packetRaw bytes.Buffer
	encoder := json.NewEncoder(&packetRaw)
	for _, id := range ids {
		if err := encoder.Encode(packets[id]); err != nil {
			t.Fatal(err)
		}
	}
	mappingRaw, err := json.Marshal(mapping)
	if err != nil {
		t.Fatal(err)
	}
	downloadRaw, err := json.Marshal(downloads)
	if err != nil {
		t.Fatal(err)
	}
	reviewRaw, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return fillerreference.RawAuditInputs{
		Manifest: manifestRaw, Packets: packetRaw.Bytes(), Mapping: mappingRaw,
		DownloadLedger: downloadRaw, ContentReview: reviewRaw,
	}
}

func caseByID(t *testing.T, audit fillerreference.Audit, id string) fillerreference.Case {
	t.Helper()
	for _, item := range audit.Cases {
		if item.CaseID == id {
			return item
		}
	}
	t.Fatalf("case %q missing from audit", id)
	return fillerreference.Case{}
}
