package fillersafetycert

import (
	"encoding/json"
	"fmt"
	"os"
)

// Publish verifies and scores one pre-authored authority against an exhaustive
// label-blind ledger manifest. Passing remains evidence, never permission.
func Publish(config Config) (Report, string, error) {
	loaded, err := loadCertification(config)
	if err != nil {
		return Report{}, "", err
	}
	report := score(loaded, config.ScoredAt)
	if err := validateReport(report); err != nil {
		return Report{}, "", fmt.Errorf("validate cascade certification report: %w", err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return Report{}, "", err
	}
	raw = append(raw, '\n')
	if err := writePrivateNew(config.OutputPath, raw); err != nil {
		return Report{}, "", fmt.Errorf("publish cascade certification: %w", err)
	}
	return report, hashBytes(raw), nil
}

func writePrivateNew(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	written = true
	return nil
}
