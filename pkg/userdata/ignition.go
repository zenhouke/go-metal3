package userdata

import (
	"encoding/json"
	"fmt"
	"regexp"
)

var ignitionVersionPattern = regexp.MustCompile(`^3\.[0-9]+\.[0-9]+$`)

// RenderIgnition validates and returns a compact copy of an Ignition v3 JSON
// document. Full schema validation remains the responsibility of Ignition's
// version-specific tooling.
func RenderIgnition(raw []byte) ([]byte, error) {
	var document struct {
		Ignition struct {
			Version string `json:"version"`
		} `json:"ignition"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("parse Ignition JSON: %w", err)
	}
	if !ignitionVersionPattern.MatchString(document.Ignition.Version) {
		return nil, fmt.Errorf("Ignition version must be a v3 semantic version")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parse Ignition JSON: %w", err)
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("render Ignition JSON: %w", err)
	}
	return result, nil
}
