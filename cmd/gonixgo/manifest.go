package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// loadManifest decodes the manifest of the derivation being built into v
// and returns the derivation's output path. Derivations with
// __structuredAttrs get both from the attrs file; others pass the manifest
// and $out in the environment.
func loadManifest(v any) (out string, err error) {
	if file := os.Getenv("NIX_ATTRS_JSON_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		var attrs struct {
			Manifest json.RawMessage `json:"manifest"`
			Outputs  struct {
				Out string `json:"out"`
			} `json:"outputs"`
		}
		if err := json.Unmarshal(data, &attrs); err != nil {
			return "", fmt.Errorf("parsing %s: %w", file, err)
		}
		if err := json.Unmarshal(attrs.Manifest, v); err != nil {
			return "", fmt.Errorf("parsing manifest in %s: %w", file, err)
		}
		return attrs.Outputs.Out, nil
	}

	manifest, out := os.Getenv("manifest"), os.Getenv("out")
	if manifest == "" || out == "" {
		return "", errors.New("no manifest: expected NIX_ATTRS_JSON_FILE, or the manifest and out environment variables")
	}
	if err := json.Unmarshal([]byte(manifest), v); err != nil {
		return "", fmt.Errorf("parsing manifest: %w", err)
	}
	return out, nil
}
