package mediatools

import "testing"

func TestDerivativeRecipesAreStableAndRoleSeparated(t *testing.T) {
	evidence := EvidenceDerivativeRecipe()
	playback := PlaybackDerivativeRecipe(DefaultMezzanine(), -23)
	evidenceDigest, err := evidence.Digest()
	if err != nil {
		t.Fatal(err)
	}
	playbackDigest, err := playback.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if evidenceDigest == playbackDigest || evidence.Role == playback.Role {
		t.Fatalf("evidence and playback recipes collapsed: %s / %s", evidenceDigest, playbackDigest)
	}
	if evidence.TargetLUFS != 0 || playback.TargetLUFS != -23 {
		t.Fatalf("loudness policy crossed roles: evidence=%v playback=%v", evidence.TargetLUFS, playback.TargetLUFS)
	}
	if evidence.Profile().CRF >= playback.Profile().CRF {
		t.Fatalf("evidence CRF %d should preserve more encoded detail than playback CRF %d", evidence.Profile().CRF, playback.Profile().CRF)
	}

	changed := evidence
	changed.CRF++
	changedDigest, err := changed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == evidenceDigest {
		t.Fatal("semantic recipe change retained the same identity")
	}
}

func TestEvidenceRecipeRefusesPlaybackLoudness(t *testing.T) {
	recipe := EvidenceDerivativeRecipe()
	recipe.TargetLUFS = -23
	if err := recipe.Validate(); err == nil {
		t.Fatal("evidence recipe accepted a loudness rewrite")
	}
}
