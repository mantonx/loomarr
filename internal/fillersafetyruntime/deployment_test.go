package fillersafetyruntime

import (
	"strings"
	"testing"
)

func TestDeploymentIsContentAddressedAndEvidenceOnly(t *testing.T) {
	deployment, err := SealDeployment(Deployment{
		AuthoritySHA256:    strings.Repeat("a", 64),
		MaximumSourceBytes: 1_000_000, MaximumSourceDurationMS: 120_000,
		AudioMaximumInputTokens: 4_000, VideoMaximumInputTokens: 8_000,
		AudioReservationNanoUSD: 1_000_000, VideoReservationNanoUSD: 2_000_000,
		PerClipBudgetNanoUSD: 4_000_000, PerRunBudgetNanoUSD: 4_000_000,
		PerDayBudgetNanoUSD: 40_000_000, CertifiedEvidenceExecution: true,
	})
	if err != nil || validateDeploymentShape(deployment) != nil || deployment.SHA256 == "" {
		t.Fatalf("deployment=%+v err=%v", deployment, err)
	}
	changed := deployment
	changed.VideoReservationNanoUSD++
	if validateDeploymentShape(changed) == nil {
		t.Fatal("deployment accepted changed execution authority under the old digest")
	}
	changed = deployment
	changed.CertifiedEvidenceExecution = false
	changed.SHA256 = deploymentSHA256(changed)
	if validateDeploymentShape(changed) == nil {
		t.Fatal("deployment accepted disabled certified evidence execution")
	}
}
