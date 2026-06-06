package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// pi (pi-coding-agent) stores custom providers/models in ~/.pi/agent/models.json.
// The schema mirrors OpenCode's native config but uses a different shape, so
// Shepherd reads it directly to populate the Pi model selector — the same way
// ListOpenCodeModels reads OpenCode's config.json. See https://pi.dev/docs/latest
// (models.md) for the file format.

// PiModelsConfigPath returns the path to pi's custom models file.
func PiModelsConfigPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".pi", "agent", "models.json")
}

// PiModelOption describes a single selectable pi model.
type PiModelOption struct {
	ID    string `json:"id"`    // "<provider>/<model_id>" — passed to `pi --model`
	Label string `json:"label"` // human-readable display name
}

// ListPiModels returns the custom providers/models defined in pi's models.json.
// IDs are "<provider>/<model_id>" so they can be passed straight to `pi --model`.
func ListPiModels() []PiModelOption {
	configPath := PiModelsConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[pi] ListPiModels: read failed for %s: %v", configPath, err)
		}
		return nil
	}
	data = stripUTF8BOM(data)

	var raw struct {
		Providers map[string]struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("[pi] ListPiModels: JSON parse failed for %s: %v", configPath, err)
		return nil
	}

	seen := map[string]bool{}
	out := make([]PiModelOption, 0)
	for provKey, prov := range raw.Providers {
		for _, model := range prov.Models {
			if model.ID == "" {
				continue
			}
			id := provKey + "/" + model.ID
			if seen[id] {
				continue
			}
			seen[id] = true
			label := id
			if model.Name != "" {
				label = model.Name + " (" + id + ")"
			}
			out = append(out, PiModelOption{ID: id, Label: label})
		}
	}

	return out
}
