package names

import (
	"context"
	"fmt"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheepname"
)

// ManagerName is the special name for the shepherd manager.
const ManagerName = "shepherd"

// DefaultNames is the initial pool of sheep names (used for seeding DB).
var DefaultNames = []string{
	"양동이", "양말이", "양철이", "양순이",
	"메에롱", "깜순이", "흰둥이", "복실이",
	"숀", "뭉치", "구름이", "몽실이",
}

var client *ent.Client

// SetClient sets the DB client for names package.
func SetClient(c *ent.Client) {
	client = c
}

// IsManager checks if the given name is the manager name.
func IsManager(name string) bool {
	return name == ManagerName
}

// InitializeDefaults seeds the default names into the database if empty.
func InitializeDefaults() error {
	if client == nil {
		return fmt.Errorf("DB client not initialized")
	}

	ctx := context.Background()

	count, err := client.SheepName.Query().Count(ctx)
	if err != nil {
		return fmt.Errorf("failed to query sheep names: %w", err)
	}

	if count > 0 {
		return nil
	}

	for i, name := range DefaultNames {
		_, err := client.SheepName.Create().
			SetName(name).
			SetPriority(i).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to add default name '%s': %w", name, err)
		}
	}

	return nil
}

// GetAll returns all sheep names from the database.
func GetAll() []string {
	if client == nil {
		return DefaultNames
	}

	ctx := context.Background()
	names, err := client.SheepName.Query().
		Order(ent.Asc(sheepname.FieldPriority)).
		All(ctx)
	if err != nil || len(names) == 0 {
		return DefaultNames
	}

	result := make([]string, len(names))
	for i, n := range names {
		result[i] = n.Name
	}
	return result
}

// GetAvailable returns names that are not in the usedNames list.
func GetAvailable(usedNames []string) []string {
	used := make(map[string]bool)
	for _, name := range usedNames {
		used[name] = true
	}

	var available []string
	for _, name := range GetAll() {
		if !used[name] {
			available = append(available, name)
		}
	}
	return available
}

// GetNext returns the first available name not in usedNames.
// If all names are used, generates a numbered name like "양13", "양14", etc.
func GetNext(usedNames []string) string {
	available := GetAvailable(usedNames)
	if len(available) > 0 {
		return available[0]
	}

	// All names used - generate numbered name
	used := make(map[string]bool)
	for _, name := range usedNames {
		used[name] = true
	}

	allNames := GetAll()
	for i := len(allNames) + 1; ; i++ {
		name := fmt.Sprintf("양%d", i)
		if !used[name] {
			return name
		}
	}
}

// IsValid checks if the given name is valid.
func IsValid(name string) bool {
	for _, n := range GetAll() {
		if n == name {
			return true
		}
	}

	// Check numbered names (양13, 양14, ...)
	if len(name) >= 2 && name[:len("양")] == "양" {
		for _, c := range name[len("양"):] {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}

	return false
}

// Count returns the total number of names in the pool.
func Count() int {
	return len(GetAll())
}

// Add adds a new name to the pool.
func Add(name string) error {
	if client == nil {
		return fmt.Errorf("DB client not initialized")
	}

	ctx := context.Background()

	exists, err := client.SheepName.Query().
		Where(sheepname.Name(name)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("failed to query name: %w", err)
	}
	if exists {
		return fmt.Errorf("'%s' already exists", name)
	}

	maxPriority := 0
	lastName, err := client.SheepName.Query().
		Order(ent.Desc(sheepname.FieldPriority)).
		First(ctx)
	if err == nil {
		maxPriority = lastName.Priority + 1
	}

	_, err = client.SheepName.Create().
		SetName(name).
		SetPriority(maxPriority).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to add name: %w", err)
	}

	return nil
}

// Remove removes a name from the pool.
func Remove(name string) error {
	if client == nil {
		return fmt.Errorf("DB client not initialized")
	}

	ctx := context.Background()

	count, err := client.SheepName.Delete().
		Where(sheepname.Name(name)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete name: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("'%s' not found", name)
	}

	return nil
}

// List returns all names with their priorities.
func List() ([]NameInfo, error) {
	if client == nil {
		result := make([]NameInfo, len(DefaultNames))
		for i, name := range DefaultNames {
			result[i] = NameInfo{Name: name, Priority: i}
		}
		return result, nil
	}

	ctx := context.Background()
	names, err := client.SheepName.Query().
		Order(ent.Asc(sheepname.FieldPriority)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list names: %w", err)
	}

	result := make([]NameInfo, len(names))
	for i, n := range names {
		result[i] = NameInfo{
			Name:     n.Name,
			Priority: n.Priority,
		}
	}
	return result, nil
}

// NameInfo holds name and priority information.
type NameInfo struct {
	Name     string
	Priority int
}
