package fillerreference

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestCompareDuplicateSequencesToleratesLeaderAndTrailer(t *testing.T) {
	unit := []uint64{
		0x0f0f0f0f0f0f0f0f, 0x1f0f0f0f0f0f0f0f, 0x2f0f0f0f0f0f0f0f,
		0x3f0f0f0f0f0f0f0f, 0x4f0f0f0f0f0f0f0f, 0x5f0f0f0f0f0f0f0f,
		0x6f0f0f0f0f0f0f0f, 0x7f0f0f0f0f0f0f0f, 0x8f0f0f0f0f0f0f0f,
		0x9f0f0f0f0f0f0f0f, 0xaf0f0f0f0f0f0f0f, 0xbf0f0f0f0f0f0f0f,
		0xcf0f0f0f0f0f0f0f, 0xdf0f0f0f0f0f0f0f, 0xef0f0f0f0f0f0f0f,
		0xff0f0f0f0f0f0f0f,
	}
	a := append([]uint64{0x3333333333333333, 0x5555555555555555}, unit...)
	b := append(append([]uint64{}, unit...), 0x7777777777777777, 0xaaaaaaaaaaaaaaaa)

	got := CompareDuplicateSequences(a, b)
	if !got.Related || got.MatchedFrames < len(unit) || got.Coverage < 0.85 {
		t.Fatalf("comparison = %+v, want related sustained sequence", got)
	}
}

func TestCompareDuplicateSequencesRejectsSparseOrFlatAgreement(t *testing.T) {
	flat := make([]uint64, 30)
	if got := CompareDuplicateSequences(flat, flat); got.Related {
		t.Fatalf("flat comparison = %+v, want unrelated", got)
	}

	a := make([]uint64, 30)
	b := make([]uint64, 30)
	for i := range a {
		a[i] = uint64(i+8) * 0x0101010101010101
		b[i] = ^a[i]
	}
	copy(b[:5], a[:5])
	if got := CompareDuplicateSequences(a, b); got.Related {
		t.Fatalf("sparse comparison = %+v, want unrelated", got)
	}
}

func TestCompareDuplicateSequencesRequiresEnoughEvidence(t *testing.T) {
	a := []uint64{0x1111111111111111, 0x2222222222222222, 0x3333333333333333}
	if got := CompareDuplicateSequences(a, a); got.Related {
		t.Fatalf("short comparison = %+v, want unrelated", got)
	}
}

func TestDuplicateComparisonsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	frames := make([]uint64, 100)
	audio := make([]uint32, 100)
	for index := range frames {
		frames[index] = 0x0f0f0f0f0f0f0f0f ^ uint64(index)
		audio[index] = uint32(index + 1)
	}
	if _, err := CompareDuplicateSequencesContext(ctx, frames, frames); err == nil {
		t.Fatal("visual comparison ignored cancellation")
	}
	if _, err := CompareAudioEnvelopesContext(ctx, audio, audio); err == nil {
		t.Fatal("audio comparison ignored cancellation")
	}
}

func TestFingerprintComparabilityMatchesEvidenceRequirements(t *testing.T) {
	frames := make([]uint64, duplicateMinFrames)
	for index := range frames {
		frames[index] = 0x0f0f0f0f0f0f0f0f ^ uint64(index)
	}
	if !VisualFingerprintComparable(frames) || VisualFingerprintComparable(frames[:duplicateMinFrames-1]) {
		t.Fatal("visual comparability did not enforce informative-frame minimum")
	}
	audio := make([]uint32, duplicateAudioMinBins)
	for index := range audio {
		audio[index] = uint32(index)
	}
	if !AudioFingerprintComparable(audio) || AudioFingerprintComparable(audio[:duplicateAudioMinBins-1]) {
		t.Fatal("audio comparability did not enforce bin minimum")
	}
	for index := range audio {
		audio[index] = 1
	}
	if AudioFingerprintComparable(audio) {
		t.Fatal("constant audio accepted as comparable")
	}
}

func TestBuildFamilyAuditReportsTransitiveNonCliqueWithoutChoosingRendition(t *testing.T) {
	base := make([]uint64, 20)
	for i := range base {
		base[i] = uint64(i+5) * 0x0102040810204081
	}
	a := append([]uint64{}, base...)
	b := append([]uint64{}, base...)
	c := append([]uint64{}, base...)
	for i := 0; i < 5; i++ {
		a[i] ^= 0xffffffffffffffff
		c[len(c)-1-i] ^= 0xffffffffffffffff
	}
	when := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	fingerprints := []FamilyFingerprint{
		{CaseID: "c", ContentSHA256: repeatedSHA('c'), LocalFile: "c.mp4", FrameHashes: c, AudioRMS: []uint32{1}},
		{CaseID: "a", ContentSHA256: repeatedSHA('a'), LocalFile: "a.mp4", FrameHashes: a, AudioRMS: []uint32{1}},
		{CaseID: "b", ContentSHA256: repeatedSHA('b'), LocalFile: "b.mp4", FrameHashes: b, AudioRMS: []uint32{1}},
	}
	got, err := BuildFamilyAudit(familySourceAudit(t, fingerprints, when.Add(-time.Hour)), fingerprints, when)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.DuplicateFamilies != 1 || got.Summary.NonCliqueFamilies != 1 || len(got.Families) != 1 {
		t.Fatalf("summary=%+v families=%+v", got.Summary, got.Families)
	}
	if got.Families[0].CompleteClique || got.Families[0].PreferredCase != "" {
		t.Fatalf("family=%+v, want unresolved non-clique", got.Families[0])
	}
}

func TestBuildFamilyAuditRejectsMissingExtraDuplicateAndMutatedBindings(t *testing.T) {
	when := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	fingerprints := []FamilyFingerprint{
		{CaseID: "a", ContentSHA256: repeatedSHA('a'), LocalFile: "a.mp4", FrameHashes: make([]uint64, 12), AudioRMS: []uint32{1}},
		{CaseID: "b", ContentSHA256: repeatedSHA('b'), LocalFile: "b.mp4", FrameHashes: make([]uint64, 12), AudioRMS: []uint32{1}},
	}
	raw := familySourceAudit(t, fingerprints, when.Add(-time.Hour))
	tests := map[string][]FamilyFingerprint{
		"missing":   fingerprints[:1],
		"extra":     append(append([]FamilyFingerprint{}, fingerprints...), FamilyFingerprint{CaseID: "extra", ContentSHA256: repeatedSHA('e'), LocalFile: "extra.mp4"}),
		"duplicate": {fingerprints[0], fingerprints[0]},
		"mutated":   {fingerprints[0], {CaseID: "b", ContentSHA256: repeatedSHA('x'), LocalFile: "b.mp4", FrameHashes: []uint64{1}, AudioRMS: []uint32{1}}},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildFamilyAudit(raw, input, when); err == nil {
				t.Fatal("invalid family input accepted")
			}
		})
	}
}

func TestFamilyAuditValidateSplitKeepsNearDuplicatesTogether(t *testing.T) {
	audit := FamilyAudit{
		Fingerprints: []FamilyFingerprint{{CaseID: "a"}, {CaseID: "b"}, {CaseID: "c"}},
		Families:     []DuplicateFamily{{FamilyID: "family", Members: []string{"a", "b"}}},
	}
	if err := audit.ValidateSplit(map[string]string{"a": "development", "b": "development", "c": "holdout"}); err != nil {
		t.Fatal(err)
	}
	if err := audit.ValidateSplit(map[string]string{"a": "development", "b": "holdout", "c": "holdout"}); err == nil {
		t.Fatal("similarity family crossed split boundary")
	}
}

func familySourceAudit(t *testing.T, fingerprints []FamilyFingerprint, generatedAt time.Time) []byte {
	t.Helper()
	cases := make([]Case, 0, 300)
	for _, fingerprint := range fingerprints {
		cases = append(cases, Case{CaseID: fingerprint.CaseID, ContentSHA256: fingerprint.ContentSHA256, SourceLocalFile: fingerprint.LocalFile, Disposition: DispositionHold})
	}
	for len(cases) < 300 {
		index := len(cases)
		cases = append(cases, Case{CaseID: fmt.Sprintf("excluded-%03d", index), ContentSHA256: fmt.Sprintf("%064x", index+1), SourceLocalFile: fmt.Sprintf("excluded-%03d.mp4", index), Disposition: DispositionExclude})
	}
	audit := Audit{
		SchemaVersion: AuditSchemaVersion, Contract: ContractVersion, GeneratedAt: generatedAt,
		Inputs:  InputIdentity{ManifestSHA256: repeatedSHA('1'), PacketsSHA256: repeatedSHA('2'), MappingSHA256: repeatedSHA('3'), DownloadLedgerSHA256: repeatedSHA('4'), ContentReviewSHA256: repeatedSHA('5')},
		Summary: Summary{Cases: 300, Holds: len(fingerprints), Excluded: 300 - len(fingerprints)}, Cases: cases,
	}
	raw, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func repeatedSHA(value byte) string {
	result := make([]byte, 64)
	for i := range result {
		result[i] = value
	}
	return string(result)
}

func TestCompareAudioEnvelopesFindsShiftedLevelScaledCopy(t *testing.T) {
	a := make([]uint32, 120)
	for i := range a {
		a[i] = uint32((i*37)%101 + 20)
	}
	b := make([]uint32, 0, 125)
	b = append(b, 4, 8, 12, 16, 20)
	for _, value := range a {
		b = append(b, value*3+7)
	}
	got := CompareAudioEnvelopes(a, b)
	if !got.Related || got.Correlation < 0.999 || got.OffsetBins != 5 {
		t.Fatalf("audio comparison=%+v, want shifted related track", got)
	}
}

func TestCompareAudioEnvelopesRejectsSilence(t *testing.T) {
	silence := make([]uint32, 100)
	if got := CompareAudioEnvelopes(silence, silence); got.Related {
		t.Fatalf("silence comparison=%+v, want unrelated", got)
	}
}
