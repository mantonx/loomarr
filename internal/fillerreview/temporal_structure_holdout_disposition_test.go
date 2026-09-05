package fillerreview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTemporalStructureHoldoutRequiresExplicitNegativeDispositionsBeforeMedia(t *testing.T) {
	fixture := newTemporalStructureHoldoutFixture(t)
	planRoot := filepath.Join(t.TempDir(), "plan")
	if _, err := BuildTemporalStructureHoldoutPlan(fixture.config(planRoot)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(planRoot, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"blindHumanAuditRequired", "trainingAllowed", "productionAdmissionAllowed"} {
		if string(fields[field]) != "false" {
			t.Fatalf("generated receipt %s = %s, want explicit false", field, fields[field])
		}
		for _, value := range []string{"missing", "null", "true"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				mutated := make(map[string]json.RawMessage, len(fields))
				for key, content := range fields {
					mutated[key] = content
				}
				if value == "missing" {
					delete(mutated, field)
				} else {
					mutated[field] = json.RawMessage(value)
				}
				receiptPath := writeTemporalHumanJSON(t, t.TempDir(), "receipt.json", mutated)
				media := &fakeTemporalStructureMedia{durationByPath: map[string]int64{}}
				output := filepath.Join(t.TempDir(), "challenge")
				_, err := BuildTemporalStructureChallenge(context.Background(), TemporalStructureChallengeConfig{
					AuthoringPath: filepath.Join(planRoot, "authoring.json"), PlanReceiptPath: receiptPath,
					SourceRoot: fixture.root, OutputDir: output, ChallengeID: "disposition-tamper",
					Seed: "blinding-seed", GeneratedAt: fixture.plannedAt.Add(time.Hour), Media: media,
				})
				if err == nil || !strings.Contains(err.Error(), "disposition") {
					t.Fatalf("receipt disposition error = %v", err)
				}
				if media.probeCalls != 0 {
					t.Fatalf("invalid disposition reached media: probes=%d", media.probeCalls)
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatalf("invalid disposition published output: %v", err)
				}
			})
		}
	}
}
