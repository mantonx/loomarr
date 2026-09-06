package fillerbakeoff

import (
	"strings"
	"testing"
	"time"
)

func TestOpenRouterAssessorIdentityIsStableAcrossCaptureFacts(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	modelDigest, capabilityDigest, err := OpenRouterAssessorIdentity(snapshot, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", "disabled")
	if err != nil {
		t.Fatal(err)
	}
	changed := snapshot
	changed.Models = append([]OpenRouterModelSnapshot(nil), snapshot.Models...)
	changed.Models[0].Endpoints = append([]OpenRouterEndpointSnapshot(nil), snapshot.Models[0].Endpoints...)
	changed.RetrievedAt = changed.RetrievedAt.Add(12 * time.Hour)
	changed.ResponseBytes++
	changed.Models[0].Name = "Renamed display label"
	changed.Models[0].Endpoints[0].Name = "Renamed endpoint label"
	changed.Models[0].Endpoints[0].Status = -2
	changed.Models[0].Endpoints[0].Pricing = map[string]string{"completion": "0.000004", "prompt": "0.000003"}
	gotModel, gotCapability, err := OpenRouterAssessorIdentity(changed, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", "disabled")
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != modelDigest || gotCapability != capabilityDigest || len(gotModel) != 64 || len(gotCapability) != 64 {
		t.Fatalf("stable identity drifted: model %s/%s capability %s/%s", modelDigest, gotModel, capabilityDigest, gotCapability)
	}
}

func TestOpenRouterAssessorIdentityChangesWithModelOrRouteCapability(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	wantModel, wantCapability, err := OpenRouterAssessorIdentity(snapshot, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", "disabled")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*OpenRouterSnapshot){
		"canonical revision": func(value *OpenRouterSnapshot) { value.Models[0].CanonicalSlug += "-new" },
		"model creation":     func(value *OpenRouterSnapshot) { value.Models[0].Created++ },
		"modality":           func(value *OpenRouterSnapshot) { value.Models[0].InputModalities = []string{"text", "video"} },
		"quantization":       func(value *OpenRouterSnapshot) { value.Models[0].Endpoints[0].Quantization = "int8" },
		"context":            func(value *OpenRouterSnapshot) { value.Models[0].Endpoints[0].ContextLength++ },
		"parameters": func(value *OpenRouterSnapshot) {
			value.Models[0].Endpoints[0].SupportedParameters = []string{"response_format"}
		},
		"privacy":        func(value *OpenRouterSnapshot) { value.Models[0].Endpoints[0].ZDR = false },
		"implicit cache": func(value *OpenRouterSnapshot) { value.Models[0].Endpoints[0].SupportsImplicitCache = true },
	} {
		t.Run(name, func(t *testing.T) {
			changed := snapshot
			changed.Models = append([]OpenRouterModelSnapshot(nil), snapshot.Models...)
			changed.Models[0].InputModalities = append([]string(nil), snapshot.Models[0].InputModalities...)
			changed.Models[0].Endpoints = append([]OpenRouterEndpointSnapshot(nil), snapshot.Models[0].Endpoints...)
			changed.Models[0].Endpoints[0].SupportedParameters = append([]string(nil), snapshot.Models[0].Endpoints[0].SupportedParameters...)
			mutate(&changed)
			gotModel, gotCapability, err := OpenRouterAssessorIdentity(changed, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", "disabled")
			if err != nil {
				t.Fatal(err)
			}
			if name == "canonical revision" || name == "model creation" {
				if gotModel == wantModel {
					t.Fatal("model identity did not change")
				}
			} else if gotModel != wantModel {
				t.Fatal("route-only capability changed model identity")
			}
			if gotCapability == wantCapability {
				t.Fatal("capability identity did not change")
			}
		})
	}
}

func TestOpenRouterAssessorIdentityRequiresExactSelectedRoute(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	_, _, err := OpenRouterAssessorIdentity(snapshot, "vendor/model-1", "Other Provider", "other", "disabled")
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing route error = %v", err)
	}
}

func TestOpenRouterAssessorIdentityBindsInferenceMode(t *testing.T) {
	t.Parallel()
	snapshot := validOpenRouterSnapshot()
	modelDigest, disabled, err := OpenRouterAssessorIdentity(snapshot, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", "disabled")
	if err != nil {
		t.Fatal(err)
	}
	otherModelDigest, required, err := OpenRouterAssessorIdentity(snapshot, "vendor/model-1", "Pinned Provider", "pinned-provider/variant", "provider_required")
	if err != nil {
		t.Fatal(err)
	}
	if modelDigest != otherModelDigest || disabled == required {
		t.Fatal("inference mode did not change only the capability identity")
	}
}
