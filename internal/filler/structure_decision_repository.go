package filler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/loomarr/loomarr/internal/fillerstructure"
)

const structureDecisionArtifactMaxBytes = 2 << 20

// PutStructureDecisionArtifact publishes the complete reducer result only after every assessment
// record is durable. Its content identity makes retries idempotent and conflicting bytes fatal.
func (r *FileStructureAssessmentEvidenceRepository) PutStructureDecisionArtifact(ctx context.Context, artifact fillerstructure.Artifact) error {
	if r == nil || r.root == "" {
		return fmt.Errorf("structure decision repository is unavailable")
	}
	if err := fillerstructure.ValidateArtifact(artifact); err != nil {
		return err
	}
	raw, err := json.Marshal(artifact)
	if err != nil || len(raw) > structureDecisionArtifactMaxBytes {
		return fmt.Errorf("marshal structure decision artifact")
	}
	if err := r.putImmutable(ctx, r.blobPath("decisions", artifact.SHA256), raw, structureDecisionArtifactMaxBytes); err != nil {
		return fmt.Errorf("persist structure decision artifact: %w", err)
	}
	return nil
}

func (r *FileStructureAssessmentEvidenceRepository) GetStructureDecisionArtifact(ctx context.Context, artifactSHA256 string) (fillerstructure.Artifact, error) {
	if r == nil || r.root == "" || !structureEvidenceDigest(artifactSHA256) {
		return fillerstructure.Artifact{}, fmt.Errorf("structure decision identity is invalid")
	}
	raw, err := r.readImmutable(ctx, r.blobPath("decisions", artifactSHA256), structureDecisionArtifactMaxBytes)
	if err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("read structure decision artifact: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var artifact fillerstructure.Artifact
	if err := decoder.Decode(&artifact); err != nil {
		return fillerstructure.Artifact{}, fmt.Errorf("decode structure decision artifact: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fillerstructure.Artifact{}, fmt.Errorf("decode structure decision artifact: trailing JSON")
	}
	if artifact.SHA256 != artifactSHA256 {
		return fillerstructure.Artifact{}, fmt.Errorf("structure decision path does not match its identity")
	}
	if err := fillerstructure.ValidateArtifact(artifact); err != nil {
		return fillerstructure.Artifact{}, err
	}
	return artifact, nil
}
