package fillerstructurewindowcert

import (
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"strings"
)

func ValidateShadowReport(report ShadowReport) error {
	if report.SchemaVersion != ShadowReportSchemaVersion || report.ContractVersion != ShadowReportContractVersion ||
		report.ComparedAt.IsZero() || report.ComparedAt != report.ComparedAt.UTC() ||
		!validShadowDigest(report.WindowSetManifestSHA256) || !validShadowDigest(report.WindowCertificationSHA256) ||
		report.ReducerVersion == "" || report.BoundaryToleranceMS != BoundaryToleranceMS ||
		len(report.ExpectedAliases) != ShadowRequiredCases || !slices.IsSorted(report.ExpectedAliases) || !validShadowAliases(report.ExpectedAliases) ||
		len(slices.Compact(slices.Clone(report.ExpectedAliases))) != len(report.ExpectedAliases) ||
		report.TrainingAllowed || report.AutomaticMaterializationAllowed || !validShadowDigest(report.SHA256) ||
		report.SHA256 != ShadowReportSHA256(report) {
		return errors.New("short-long shadow report identity or disposition is invalid")
	}
	inputs := make([]ShadowCase, 0, len(report.Cases))
	seen := make(map[string]struct{}, len(report.Cases))
	for _, result := range report.Cases {
		if _, duplicate := seen[result.Alias]; duplicate || !slices.Contains(report.ExpectedAliases, result.Alias) {
			return errors.New("short-long shadow report case set is invalid")
		}
		seen[result.Alias] = struct{}{}
		inputs = append(inputs, ShadowCase{Alias: result.Alias, CompleteVideo: result.CompleteVideo, WindowMediaSet: result.WindowMediaSet})
	}
	replayed, err := compareShortLong(report.WindowSetManifestSHA256, report.WindowCertificationSHA256, report.ExpectedAliases, inputs, report.ComparedAt)
	if err != nil {
		return err
	}
	replayed.SHA256 = report.SHA256
	if !reflect.DeepEqual(replayed, report) {
		return errors.New("short-long shadow report does not replay")
	}
	return nil
}

func validShadowDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
