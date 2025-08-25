package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ScanSummary represents the final JSON summary from TruffleHog
type ScanSummary struct {
	VerifiedSecrets   int `json:"verified_secrets"`
	UnverifiedSecrets int `json:"unverified_secrets"`
}

// ValidateTruffleHogOutput parses TruffleHog's output and checks for secrets
func ValidateTruffleHogOutput(output []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var lastJSONLine string

	// TruffleHog outputs multiple lines, final JSON summary is last
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			lastJSONLine = line
		}
	}

	if lastJSONLine == "" {
		return fmt.Errorf("no summary JSON found in TruffleHog output")
	}

	var summary ScanSummary
	if err := json.Unmarshal([]byte(lastJSONLine), &summary); err != nil {
		return fmt.Errorf("failed to parse TruffleHog summary: %w", err)
	}

	if summary.VerifiedSecrets > 0 || summary.UnverifiedSecrets > 0 {
		return fmt.Errorf(
			"TruffleHog detected %d verified and %d unverified secrets",
			summary.VerifiedSecrets,
			summary.UnverifiedSecrets,
		)
	}

	return nil
}
