package fillerstructure

import "fmt"

const (
	DirectVideoPromptVersion       = "filler-temporal-structure-direct-video-v9"
	DirectVideoMaximumSegments     = 128
	DirectVideoMaximumDecisiveTime = 8
	DirectVideoMaximumReasonRunes  = 512
)

const directVideoContentFormat = "Complete video duration: %d milliseconds. Inspect the complete supplied video and return the closed temporal-structure assessment."

const DirectVideoSystemPrompt = `Segment one complete identity-blind video. Judge the supplied file's actual item boundaries, not whether its topic resembles an advertisement.

Return segments in playback order covering every millisecond from 0 through the supplied duration. Each segment supplies its exclusive endMs: the first starts at 0, every later segment starts at the preceding endMs, and the final endMs equals the supplied duration. Keep one independently bounded, self-contained item with one cohesive communicative purpose as one segment even when it contains many shots, scenes, title cards, or credits. Split whenever separately bounded items have been joined. For programme material with an inserted filler item, return the programme before the insertion, every inserted item, and the resumed programme as separate intervals. A scene change inside one continuing programme is not an item boundary.

Give every segment its own role: commercial, promo, bumper, psa, station_id, trailer, interstitial, programme_fragment, non_filler, ambiguous, or unusable. Use programme_fragment for material whose beginning or ending depends on a larger programme, including an ordinary scene, programme opening, sustained performance, credits/title fragment, or interior cut. Never copy one role across the file merely because the source appears commercial. Use ambiguous rather than guessing. Use unusable only when corruption or degradation prevents reliable temporal assessment; age, poor image quality, or recording overlays alone are insufficient.

Each segment must cite up to eight sorted, unique decisive timestamps inside its own interval and give a concise reason for its boundary and role. Do not infer source identity, mention filenames or aliases, or claim content suitability. Return only the requested JSON.`

func DirectVideoContent(durationMS int64) string {
	return fmt.Sprintf(directVideoContentFormat, durationMS)
}

func DirectVideoSchema(durationMS int64) map[string]any {
	roles := []string{
		string(RoleCommercial), string(RolePromo), string(RoleBumper), string(RolePSA),
		string(RoleStationID), string(RoleTrailer), string(RoleInterstitial),
		string(RoleProgrammeFragment), string(RoleNonFiller), string(RoleAmbiguous), string(RoleUnusable),
	}
	times := map[string]any{
		"type": "array", "maxItems": DirectVideoMaximumDecisiveTime,
		"items": map[string]any{"type": "integer", "minimum": 0, "maximum": durationMS},
	}
	segment := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"endMs", "role", "decisiveAtMs", "reason"},
		"properties": map[string]any{
			"endMs": map[string]any{"type": "integer", "minimum": 1, "maximum": durationMS},
			"role":  map[string]any{"type": "string", "enum": roles}, "decisiveAtMs": times,
			"reason": map[string]any{"type": "string"},
		},
	}
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"segments"},
		"properties": map[string]any{
			// The local validator enforces DirectVideoMaximumSegments. Do not emit that
			// large maxItems bound here: otherwise-compatible strict video routes reject it.
			"segments": map[string]any{
				"type": "array", "minItems": 1, "items": segment,
			},
		},
	}
}
