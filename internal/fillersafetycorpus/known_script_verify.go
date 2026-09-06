package fillersafetycorpus

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

type verifiedKnownScriptMember struct {
	scriptRaw         []byte
	processorSchedule KnownScriptProcessorSchedule
}

func verifyKnownScriptInputs(
	ctx context.Context,
	loaded loadedKnownScript,
	config PrepareKnownScriptConfig,
) (map[string]verifiedKnownScriptMember, int64, error) {
	result := make(map[string]verifiedKnownScriptMember, len(loaded.authority.Members))
	total := int64(len(loaded.authorityRaw) + len(loaded.seed))
	if total > config.MaximumInputBytes {
		return nil, 0, fmt.Errorf("known-script inputs exceed byte ceiling")
	}
	type verifiedFile struct {
		authority FileAuthority
		path      string
	}
	verified := map[string]verifiedFile{}
	verify := func(authority FileAuthority, maximum int64) (string, error) {
		if authority.Bytes > maximum {
			return "", fmt.Errorf("known-script input exceeds per-file ceiling")
		}
		if previous, ok := verified[authority.Path]; ok {
			if previous.authority != authority {
				return "", fmt.Errorf("known-script input path has conflicting authorities")
			}
			return previous.path, nil
		}
		if authority.Bytes > config.MaximumInputBytes-total {
			return "", fmt.Errorf("known-script inputs exceed byte ceiling")
		}
		path, err := verifiedMemberPath(loaded.root, authority, maximum)
		if err != nil {
			return "", fmt.Errorf("known-script input bytes do not match authority")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("known-script input is not private")
		}
		total += authority.Bytes
		verified[authority.Path] = verifiedFile{authority: authority, path: path}
		return path, nil
	}
	for index, member := range loaded.authority.Members {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		for _, document := range []FileAuthority{
			member.Consent.Document,
			member.Consent.SignerAuthorityEvidence,
			member.Consent.ProcessorSchedule,
			member.Consent.WithdrawalInstructions,
		} {
			if _, err := verify(document, maximumReleaseAuthorityBytes); err != nil {
				return nil, 0, fmt.Errorf("known-script member %d consent evidence is invalid: %w", index+1, err)
			}
		}
		scheduleRaw, err := readVerifiedMember(loaded.root, member.Consent.ProcessorSchedule, maximumReleaseAuthorityBytes)
		if err != nil {
			return nil, 0, fmt.Errorf("known-script member %d processor schedule is invalid", index+1)
		}
		schedule, err := decodeKnownScriptJSON[KnownScriptProcessorSchedule](scheduleRaw)
		if err != nil || validateKnownScriptProcessorSchedule(schedule) != nil {
			return nil, 0, fmt.Errorf("known-script member %d processor schedule is invalid", index+1)
		}
		if _, err := verify(member.MasterAudio, config.MaximumInputBytes); err != nil {
			return nil, 0, fmt.Errorf("known-script member %d master audio is invalid: %w", index+1, err)
		}
		_, err = verify(member.SelectedAudio, config.MaximumInputBytes)
		if err != nil {
			return nil, 0, fmt.Errorf("known-script member %d selected audio is invalid: %w", index+1, err)
		}
		_, err = verify(member.Script, maximumTranscriptBytes)
		if err != nil {
			return nil, 0, fmt.Errorf("known-script member %d script is invalid: %w", index+1, err)
		}
		scriptRaw, err := readVerifiedMember(loaded.root, member.Script, maximumTranscriptBytes)
		if err != nil || !utf8.Valid(scriptRaw) || strings.TrimSpace(string(scriptRaw)) == "" || strings.ContainsRune(string(scriptRaw), 0) {
			return nil, 0, fmt.Errorf("known-script member %d script is not bounded non-empty UTF-8", index+1)
		}
		if _, err := verify(member.PolicyMapping, maximumTranscriptBytes); err != nil {
			return nil, 0, fmt.Errorf("known-script member %d policy mapping is invalid: %w", index+1, err)
		}
		mappingRaw, err := readVerifiedMember(loaded.root, member.PolicyMapping, maximumTranscriptBytes)
		if err != nil {
			return nil, 0, fmt.Errorf("known-script member %d policy mapping is invalid", index+1)
		}
		mapping, err := decodeKnownScriptJSON[KnownScriptPolicyMapping](mappingRaw)
		if err != nil || validateKnownScriptMapping(mapping, member, loaded.authority.PolicySHA256) != nil {
			return nil, 0, fmt.Errorf("known-script member %d policy mapping does not bind authority", index+1)
		}
		for assetIndex, asset := range member.Transformation.Assets {
			if _, err := verify(asset.Media, config.MaximumInputBytes); err != nil {
				return nil, 0, fmt.Errorf("known-script member %d asset %d media is invalid", index+1, assetIndex+1)
			}
			if _, err := verify(asset.RightsEvidence, maximumReleaseAuthorityBytes); err != nil {
				return nil, 0, fmt.Errorf("known-script member %d asset %d rights are invalid", index+1, assetIndex+1)
			}
		}
		result[member.ParticipantID] = verifiedKnownScriptMember{
			scriptRaw: scriptRaw, processorSchedule: schedule,
		}
	}
	return result, total, nil
}

func verifyKnownScriptStability(
	ctx context.Context,
	expected loadedKnownScript,
	config PrepareKnownScriptConfig,
	expectedBytes int64,
) error {
	current, err := loadKnownScript(config)
	if err != nil || current.root != expected.root || !bytes.Equal(current.authorityRaw, expected.authorityRaw) ||
		!bytes.Equal(current.seed, expected.seed) {
		return fmt.Errorf("known-script authority, root, or alias seed changed during preparation")
	}
	if _, currentBytes, err := verifyKnownScriptInputs(ctx, current, config); err != nil || currentBytes != expectedBytes {
		return fmt.Errorf("known-script evidence changed during preparation")
	}
	return nil
}
