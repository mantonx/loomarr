package fillersafetycorpus

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

type selectedVCTK struct {
	member       VCTKMember
	caseID       string
	sourceFamily string
}

func selectVCTK(seed []byte, members []VCTKMember, count int) []selectedVCTK {
	bySpeaker := make(map[string][]VCTKMember)
	for _, member := range members {
		bySpeaker[member.SpeakerID] = append(bySpeaker[member.SpeakerID], member)
	}
	chosen := make([]VCTKMember, 0, len(bySpeaker))
	for speaker, candidates := range bySpeaker {
		slices.SortFunc(candidates, func(a, b VCTKMember) int {
			comparison := bytes.Compare(keyedRank(seed, "utterance", speaker+"\x00"+a.UtteranceID+"\x00"+a.Microphone),
				keyedRank(seed, "utterance", speaker+"\x00"+b.UtteranceID+"\x00"+b.Microphone))
			if comparison != 0 {
				return comparison
			}
			return bytes.Compare([]byte(a.UtteranceID+"\x00"+a.Microphone), []byte(b.UtteranceID+"\x00"+b.Microphone))
		})
		chosen = append(chosen, candidates[0])
	}
	slices.SortFunc(chosen, func(a, b VCTKMember) int {
		comparison := bytes.Compare(keyedRank(seed, "speaker", a.SpeakerID), keyedRank(seed, "speaker", b.SpeakerID))
		if comparison != 0 {
			return comparison
		}
		return bytes.Compare([]byte(a.SpeakerID), []byte(b.SpeakerID))
	})
	chosen = chosen[:count]
	result := make([]selectedVCTK, 0, count)
	for _, member := range chosen {
		result = append(result, selectedVCTK{
			member:       member,
			caseID:       "vctk-case-" + hex.EncodeToString(keyedRank(seed, "case", member.SpeakerID+"\x00"+member.UtteranceID+"\x00"+member.Microphone))[:24],
			sourceFamily: "vctk-family-" + hex.EncodeToString(keyedRank(seed, "family", member.SpeakerID))[:24],
		})
	}
	slices.SortFunc(result, func(a, b selectedVCTK) int { return bytes.Compare([]byte(a.caseID), []byte(b.caseID)) })
	return result
}

func keyedRank(seed []byte, domain, value string) []byte {
	hash := hmac.New(sha256.New, seed)
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(value))
	return hash.Sum(nil)
}
