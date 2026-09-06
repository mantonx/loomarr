package fillercorpus

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPrepareMetRightsPrescreenReportsConsistencyWithoutAuthority(t *testing.T) {
	fixture := newMetRightsPrescreenFixture(t, `"rightsAndReproduction":"",`)
	report, err := PrepareMetRightsPrescreen(fixture.inventoryBytes(t), fixture.policyBytes(t), fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != MetRightsPrescreenSchemaVersion || report.TotalCases != 1 || report.PassedCases != 1 || report.HeldCases != 0 || !report.CompleteCoverage {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Cases) != 1 || report.Cases[0].Status != metRightsPrescreenPass || len(report.Cases[0].ReasonCodes) != 0 {
		t.Fatalf("cases = %+v", report.Cases)
	}
	if report.RightsApproval || report.DownloadAuthority || report.TruthAuthority || report.TrainingAuthority || report.ProductionAuthority || report.IngestionAuthority || report.SchedulingAuthority || report.BroadcastAuthority {
		t.Fatalf("pre-screen granted authority: %+v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), fixture.options.MetadataRoot) || strings.Contains(string(raw), "Valid work") {
		t.Fatal("path or source prose leaked into path-free report")
	}
	again, err := PrepareMetRightsPrescreen(fixture.inventoryBytes(t), fixture.policyBytes(t), fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Cases, again.Cases) || report.InventorySHA256 != again.InventorySHA256 || report.PolicyEvidenceSHA256 != again.PolicyEvidenceSHA256 {
		t.Fatal("identical evidence did not reproduce")
	}
}

func TestPrepareMetRightsPrescreenHoldsMetadataAnomalies(t *testing.T) {
	tests := []struct {
		name       string
		rightsJSON string
		mutate     func(*testing.T, *metRightsPrescreenFixture)
		reasons    []string
	}{
		{name: "missing rights field", reasons: []string{"rights_and_reproduction_missing"}},
		{name: "nonempty rights field", rightsJSON: `"rightsAndReproduction":"Copyright notice",`, reasons: []string{"rights_and_reproduction_nonempty"}},
		{name: "public domain false", rightsJSON: `"rightsAndReproduction":"",`, mutate: func(t *testing.T, fixture *metRightsPrescreenFixture) {
			fixture.rewriteMetadata(t, strings.Replace(string(fixture.metadata), `"isPublicDomain":true`, `"isPublicDomain":false`, 1))
		}, reasons: []string{"public_domain_not_asserted"}},
		{name: "changed cache bytes", rightsJSON: `"rightsAndReproduction":"",`, mutate: func(t *testing.T, fixture *metRightsPrescreenFixture) {
			if err := os.WriteFile(fixture.metadataPath(), append(slices.Clone(fixture.metadata), ' '), 0o600); err != nil {
				t.Fatal(err)
			}
		}, reasons: []string{"metadata_sha256_mismatch"}},
		{name: "changed inventory projection", rightsJSON: `"rightsAndReproduction":"",`, mutate: func(_ *testing.T, fixture *metRightsPrescreenFixture) {
			fixture.inventory.Cases[0].Title = "Changed title"
		}, reasons: []string{"inventory_projection_mismatch"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMetRightsPrescreenFixture(t, test.rightsJSON)
			if test.mutate != nil {
				test.mutate(t, fixture)
			}
			report, err := PrepareMetRightsPrescreen(fixture.inventoryBytes(t), fixture.policyBytes(t), fixture.options)
			if err != nil {
				t.Fatal(err)
			}
			if report.PassedCases != 0 || report.HeldCases != 1 || report.Cases[0].Status != metRightsPrescreenHold || !slices.Equal(report.Cases[0].ReasonCodes, test.reasons) {
				t.Fatalf("case = %+v", report.Cases[0])
			}
		})
	}
}

func TestPrepareMetRightsPrescreenRejectsPolicyOrCoverageDrift(t *testing.T) {
	fixture := newMetRightsPrescreenFixture(t, `"rightsAndReproduction":"",`)
	policy := fixture.policy
	policy.Limitations = policy.Limitations[:len(policy.Limitations)-1]
	badPolicy, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareMetRightsPrescreen(fixture.inventoryBytes(t), badPolicy, fixture.options); err == nil {
		t.Fatal("policy limitation drift passed")
	}
	policy = fixture.policy
	policy.Sources = slices.Clone(fixture.policy.Sources)
	policy.Sources[0].SHA256 = strings.Repeat("f", 64)
	badPolicy, err = json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareMetRightsPrescreen(fixture.inventoryBytes(t), badPolicy, fixture.options); err == nil {
		t.Fatal("policy source digest drift passed")
	}
	options := fixture.options
	options.MinItems = 2
	if _, err := PrepareMetRightsPrescreen(fixture.inventoryBytes(t), fixture.policyBytes(t), options); err == nil {
		t.Fatal("incomplete declared coverage passed")
	}
}

type metRightsPrescreenFixture struct {
	inventory Inventory
	policy    MetOpenAccessPolicyEvidence
	metadata  []byte
	options   MetRightsPrescreenOptions
}

func newMetRightsPrescreenFixture(t *testing.T, rightsJSON string) *metRightsPrescreenFixture {
	t.Helper()
	snapshot := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	metadata := []byte(`{"objectID":195733,"isPublicDomain":true,` + rightsJSON + `"primaryImage":"https://images.metmuseum.org/valid.jpg","title":"Valid work","artistDisplayName":"Valid Creator","objectDate":"1900","objectURL":"https://www.metmuseum.org/art/collection/search/195733","repository":"Metropolitan Museum of Art, New York, NY","creditLine":"Gift, 1900","tags":[{"term":"Female Nudes"}]}`)
	var object metObject
	if err := json.Unmarshal(metadata, &object); err != nil {
		t.Fatal(err)
	}
	collection := "selection-sha256:" + strings.Repeat("a", 64)
	role := "policy-positive-nomination"
	captureID := NewCaptureID(MetAuthority, collection, role)
	metadataURL := metAPIBase + "/objects/195733"
	item := metInventoryCase(object, []string{"venus"}, metadata, metadataURL, snapshot.Add(-time.Minute), captureID, role, "image/jpeg", 100)
	inventory := Inventory{
		SchemaVersion: InventorySchemaVersion,
		SnapshotAt:    snapshot,
		Captures: []Capture{{
			CaptureID: captureID, Transport: TransportHTTPS, Authority: MetAuthority, Collection: collection, RoleHint: role,
			SnapshotAt: snapshot, MaxRequests: 3, RequestsUsed: 3, MaxResponseBytes: 10_000, ResponseBytes: int64(len(metadata)),
			MaxPredictedMediaBytes: 100, PredictedMediaBytes: 100, MaxWallTimeMS: 1_000, WallTimeMS: 100,
		}},
		Cases: []InventoryCase{item},
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/"+item.MetadataCache, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	sources := slices.Clone(requiredMetPolicySources)
	return &metRightsPrescreenFixture{
		inventory: inventory,
		policy: MetOpenAccessPolicyEvidence{
			SchemaVersion: MetOpenAccessPolicyEvidenceSchemaVersion, EvidenceID: "met-open-access-metadata-prescreen-v1",
			CapturedAt: snapshot, Sources: sources, Limitations: slices.Clone(requiredMetPolicyLimitations),
		},
		metadata: metadata,
		options:  MetRightsPrescreenOptions{MetadataRoot: root, PreparedAt: snapshot.Add(time.Hour), MinItems: 1, MaxItems: 1},
	}
}

func (fixture *metRightsPrescreenFixture) inventoryBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(fixture.inventory)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (fixture *metRightsPrescreenFixture) policyBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (fixture *metRightsPrescreenFixture) metadataPath() string {
	return fixture.options.MetadataRoot + "/" + fixture.inventory.Cases[0].MetadataCache
}

func (fixture *metRightsPrescreenFixture) rewriteMetadata(t *testing.T, raw string) {
	t.Helper()
	fixture.metadata = []byte(raw)
	var object metObject
	if err := json.Unmarshal(fixture.metadata, &object); err != nil {
		t.Fatal(err)
	}
	item := fixture.inventory.Cases[0]
	fixture.inventory.Cases[0] = metInventoryCase(object, []string{"venus"}, fixture.metadata, item.MetadataURL, item.MetadataRetrievedAt, item.CaptureIDs[0], item.RoleHints[0], item.Representation.MIMEType, item.Representation.Bytes)
	if err := os.WriteFile(fixture.metadataPath(), fixture.metadata, 0o600); err != nil {
		t.Fatal(err)
	}
}
