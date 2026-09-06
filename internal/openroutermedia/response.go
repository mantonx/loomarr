package openroutermedia

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/loomarr/loomarr/internal/fillereval"
)

type structuredResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"message"`
		Error *structuredWireError `json:"error"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64       `json:"prompt_tokens"`
		CompletionTokens int64       `json:"completion_tokens"`
		Cost             json.Number `json:"cost"`
	} `json:"usage"`
	Metadata struct {
		Attempt   int `json:"attempt"`
		Endpoints struct {
			Available []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Selected bool   `json:"selected"`
			} `json:"available"`
		} `json:"endpoints"`
		Attempts []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Status   int    `json:"status"`
		} `json:"attempts"`
	} `json:"openrouter_metadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type structuredWireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// StatusError classifies a non-success provider response without retaining
// provider-controlled detail in public error state.
type StatusError struct {
	StatusCode int
	Detail     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("OpenRouter structured call returned status %d", e.StatusCode)
}

func newStatusError(statusCode int) *StatusError {
	return &StatusError{StatusCode: statusCode, Detail: "provider request failed"}
}

func settleResponse(result Result, raw []byte, config Config) (Result, error) {
	var wire structuredResponse
	if err := decodeProviderJSON(raw, &wire); err != nil {
		return result, err
	}
	result.GenerationID = wire.ID
	result.PromptTokens = wire.Usage.PromptTokens
	result.CompletionTokens = wire.Usage.CompletionTokens
	result.ChargedAmountUSD = wire.Usage.Cost.String()
	if wire.Error != nil {
		return result, fmt.Errorf("OpenRouter structured response included a provider error")
	}
	charged, err := fillereval.USDToNanoCeil(result.ChargedAmountUSD)
	if err != nil || charged < 0 || charged > config.MaxChargeNanoUSD {
		return result, fmt.Errorf("OpenRouter structured call returned missing or out-of-reservation cost")
	}
	result.ChargedNanoUSD, result.ChargeKnown = charged, true
	if len(wire.Choices) == 1 && wire.Choices[0].Error != nil {
		wireError := wire.Choices[0].Error
		status := wireError.Code
		if status < 100 || status > 599 {
			status = http.StatusBadGateway
		}
		return result, newStatusError(status)
	}
	if wire.ID == "" || wire.Model != config.Model || len(wire.Choices) != 1 || wire.Metadata.Attempt != 1 || !validAttemptLedger(wire, config) || !selectedEndpoint(wire, config) {
		return result, fmt.Errorf("OpenRouter structured response does not bind the requested one-attempt route")
	}
	result.StructuredOutput = wire.Choices[0].Message.Content
	result.ReasoningBytes = len(wire.Choices[0].Message.Reasoning)
	return result, nil
}

func decodeProviderJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing provider JSON value")
	}
	return nil
}

func validAttemptLedger(wire structuredResponse, config Config) bool {
	if len(wire.Metadata.Attempts) == 0 {
		return true
	}
	if len(wire.Metadata.Attempts) != 1 {
		return false
	}
	attempt := wire.Metadata.Attempts[0]
	return attempt.Provider == config.UpstreamProvider && attempt.Model == config.ResolvedModel && attempt.Status >= 200 && attempt.Status < 300
}

func selectedEndpoint(wire structuredResponse, config Config) bool {
	selected := 0
	for _, endpoint := range wire.Metadata.Endpoints.Available {
		if !endpoint.Selected {
			continue
		}
		selected++
		if endpoint.Provider != config.UpstreamProvider || endpoint.Model != config.ResolvedModel {
			return false
		}
	}
	return selected == 1
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
