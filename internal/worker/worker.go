package worker

import (
	"context"
	"fmt"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
)

// CreateOptions contains options for sheep creation
type CreateOptions struct {
	Name     string
	Provider string // "claude", "opencode", "auto"
}

// Create creates a new sheep with the given name.
// If name is empty, assigns the next available name from the pool.
func Create(name string) (*ent.Sheep, error) {
	return CreateWithOptions(CreateOptions{Name: name, Provider: "claude"})
}

// CreateWithOptions creates a new sheep with options.
func CreateWithOptions(opts CreateOptions) (*ent.Sheep, error) {
	ctx := context.Background()
	client := db.Client()

	name := opts.Name

	// Auto-assign if name is empty
	if name == "" {
		usedNames, err := getUsedNames(ctx, client)
		if err != nil {
			return nil, err
		}
		name = names.GetNext(usedNames)
		if name == "" {
			return nil, fmt.Errorf("no available names (max %d sheep)", names.Count())
		}
	} else {
		// Validate name
		if !names.IsValid(name) {
			return nil, fmt.Errorf("'%s' is not a valid sheep name", name)
		}
	}

	// Check if already exists
	exists, err := client.Sheep.Query().
		Where(sheep.Name(name)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query sheep: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("'%s' already exists", name)
	}

	// Validate provider
	provider := opts.Provider
	if provider == "" {
		provider = "claude"
	}
	if provider != "claude" && provider != "opencode" && provider != "auto" {
		return nil, fmt.Errorf("'%s' is not a valid provider (claude, opencode, auto)", provider)
	}

	// Create sheep
	s, err := client.Sheep.Create().
		SetName(name).
		SetStatus(sheep.StatusIdle).
		SetProvider(sheep.Provider(provider)).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create sheep: %w", err)
	}

	return s, nil
}

// Delete deletes a sheep by name.
func Delete(name string) error {
	ctx := context.Background()
	client := db.Client()

	// Look up the sheep
	s, err := client.Sheep.Query().
		Where(sheep.Name(name)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("'%s' not found", name)
		}
		return fmt.Errorf("failed to query sheep: %w", err)
	}

	// Delete the sheep
	if err := client.Sheep.DeleteOne(s).Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete sheep: %w", err)
	}

	return nil
}

// DeleteAll deletes all sheep.
func DeleteAll() (int, error) {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Sheep.Delete().Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to delete all sheep: %w", err)
	}

	return count, nil
}

// List returns all sheep (excluding the manager).
func List() ([]*ent.Sheep, error) {
	ctx := context.Background()
	client := db.Client()

	sheepList, err := client.Sheep.Query().
		Where(sheep.NameNEQ(names.ManagerName)).
		WithProject().
		Order(ent.Asc(sheep.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list sheep: %w", err)
	}

	return sheepList, nil
}

// Get returns a sheep by name.
func Get(name string) (*ent.Sheep, error) {
	ctx := context.Background()
	client := db.Client()

	s, err := client.Sheep.Query().
		Where(sheep.Name(name)).
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("'%s' not found", name)
		}
		return nil, fmt.Errorf("failed to query sheep: %w", err)
	}

	return s, nil
}

// UpdateStatus updates the status of a sheep.
func UpdateStatus(name string, status sheep.Status) error {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Sheep.Update().
		Where(sheep.Name(name)).
		SetStatus(status).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to update sheep status: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("'%s' not found", name)
	}

	return nil
}

// Count returns the number of sheep.
func Count() (int, error) {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Sheep.Query().Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count sheep: %w", err)
	}

	return count, nil
}

// getUsedNames returns all used sheep names.
func getUsedNames(ctx context.Context, client *ent.Client) ([]string, error) {
	sheepList, err := client.Sheep.Query().All(ctx)
	if err != nil {
		return nil, err
	}

	var usedNames []string
	for _, s := range sheepList {
		usedNames = append(usedNames, s.Name)
	}
	return usedNames, nil
}

// StatusToKorean converts sheep status to a display string.
func StatusToKorean(status sheep.Status) string {
	switch status {
	case sheep.StatusIdle:
		return "idle"
	case sheep.StatusWorking:
		return "working"
	case sheep.StatusError:
		return "error"
	default:
		return string(status)
	}
}

// IsWorking returns true if the sheep is currently working.
func IsWorking(name string) (bool, error) {
	ctx := context.Background()
	client := db.Client()

	s, err := client.Sheep.Query().
		Where(sheep.Name(name)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, fmt.Errorf("'%s' not found", name)
		}
		return false, fmt.Errorf("failed to query sheep: %w", err)
	}

	return s.Status == sheep.StatusWorking, nil
}

// UpdateProvider updates the provider of a sheep.
// Also clears the session ID since different providers have different session systems.
func UpdateProvider(name string, provider string) error {
	ctx := context.Background()
	client := db.Client()

	// Validate provider
	if provider != "claude" && provider != "opencode" && provider != "auto" {
		return fmt.Errorf("'%s' is not a valid provider (claude, opencode, auto)", provider)
	}

	// Clear session ID when changing provider (different providers use different session systems)
	count, err := client.Sheep.Update().
		Where(sheep.Name(name)).
		SetProvider(sheep.Provider(provider)).
		SetSessionID("").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to update sheep provider: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("'%s' not found", name)
	}

	return nil
}

// ProviderDisplayName converts provider to a display string.
func ProviderDisplayName(provider sheep.Provider) string {
	switch provider {
	case sheep.ProviderClaude:
		return "Claude"
	case sheep.ProviderOpencode:
		return GetOpenCodeDisplayName()
	case sheep.ProviderAuto:
		return "auto"
	default:
		return string(provider)
	}
}

// ProviderToKorean is an alias for ProviderDisplayName (backward compat).
func ProviderToKorean(provider sheep.Provider) string {
	return ProviderDisplayName(provider)
}

// ProviderEmoji returns emoji for provider.
func ProviderEmoji(provider sheep.Provider) string {
	switch provider {
	case sheep.ProviderClaude:
		return "🟠" // Claude = orange
	case sheep.ProviderOpencode:
		return "🟢" // OpenCode = green
	case sheep.ProviderAuto:
		return "🔵" // Auto = blue
	default:
		return "⚪"
	}
}

// GetOpenCodeDisplayName returns the display name for OpenCode provider
func GetOpenCodeDisplayName() string {
	cfg, err := config.LoadOpenCodeConfig()
	if err != nil || cfg == nil {
		return "opencode"
	}
	return cfg.GetModelDisplayName()
}

// GetLocalModelDisplayName is an alias for GetOpenCodeDisplayName (backward compat).
func GetLocalModelDisplayName() string {
	return GetOpenCodeDisplayName()
}

// GetProvider returns the provider of a sheep by name.
func GetProvider(name string) (sheep.Provider, error) {
	ctx := context.Background()
	client := db.Client()

	s, err := client.Sheep.Query().
		Where(sheep.Name(name)).
		Only(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to query sheep: %w", err)
	}

	return s.Provider, nil
}

// GetOrCreateManager returns the manager sheep, creating it if it doesn't exist.
// The manager is a special sheep that handles shepherd commands.
func GetOrCreateManager() (*ent.Sheep, error) {
	ctx := context.Background()
	client := db.Client()

	// Check if manager already exists
	s, err := client.Sheep.Query().
		Where(sheep.Name(names.ManagerName)).
		Only(ctx)
	if err == nil {
		return s, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("failed to query manager: %w", err)
	}

	// Create manager
	s, err = client.Sheep.Create().
		SetName(names.ManagerName).
		SetStatus(sheep.StatusIdle).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create manager: %w", err)
	}

	return s, nil
}
