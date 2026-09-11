// Package equipment defines immutable, protocol-independent equipment data and
// deterministic stat resolution. Callers load catalogs outside the Room tick.
package equipment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
)

type ID uint32
type Slot string
type Stat string
type Operation string

const (
	Weapon Slot = "weapon"
	Relic  Slot = "relic"
	Potion Slot = "potion"

	Attack      Stat = "attack"
	Defense     Stat = "defense"
	MaxHealth   Stat = "max_health"
	MoveSpeed   Stat = "move_speed"
	AttackSpeed Stat = "attack_speed"

	Add      Operation = "add"
	Multiply Operation = "multiply"
)

var ErrUnknownEquipment = errors.New("unknown equipment")

type Modifier struct {
	Stat      Stat      `json:"stat"`
	Operation Operation `json:"operation"`
	Value     float64   `json:"value"`
}

type Definition struct {
	ID          ID         `json:"id"`
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Slot        Slot       `json:"slot"`
	Modifiers   []Modifier `json:"modifiers,omitempty"`
	Heal        float64    `json:"heal,omitempty"`
}

type catalogFile struct {
	Version uint32       `json:"version"`
	Items   []Definition `json:"items"`
}

// Catalog owns detached definitions and exposes copies so live game state
// cannot be changed by a config slice retained by the loader or a caller.
type Catalog struct {
	version uint32
	items   map[ID]Definition
	ids     []ID
}

func Parse(r io.Reader) (Catalog, error) {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	var file catalogFile
	if err := decoder.Decode(&file); err != nil {
		return Catalog{}, fmt.Errorf("decode equipment catalog: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Catalog{}, err
	}
	return NewCatalog(file.Version, file.Items)
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("equipment catalog contains multiple JSON values")
		}
		return fmt.Errorf("decode equipment catalog suffix: %w", err)
	}
	return nil
}

func NewCatalog(version uint32, definitions []Definition) (Catalog, error) {
	if version == 0 || len(definitions) == 0 {
		return Catalog{}, errors.New("equipment catalog needs a version and at least one item")
	}
	catalog := Catalog{version: version, items: make(map[ID]Definition, len(definitions)), ids: make([]ID, 0, len(definitions))}
	keys := make(map[string]bool, len(definitions))
	for _, source := range definitions {
		definition := source
		definition.Key = strings.TrimSpace(definition.Key)
		definition.Name = strings.TrimSpace(definition.Name)
		definition.Description = strings.TrimSpace(definition.Description)
		definition.Modifiers = slices.Clone(definition.Modifiers)
		if err := definition.validate(); err != nil {
			return Catalog{}, fmt.Errorf("equipment %d: %w", definition.ID, err)
		}
		if _, exists := catalog.items[definition.ID]; exists {
			return Catalog{}, fmt.Errorf("duplicate equipment id %d", definition.ID)
		}
		if keys[definition.Key] {
			return Catalog{}, fmt.Errorf("duplicate equipment key %q", definition.Key)
		}
		catalog.items[definition.ID] = definition
		catalog.ids = append(catalog.ids, definition.ID)
		keys[definition.Key] = true
	}
	slices.Sort(catalog.ids)
	return catalog, nil
}

func (d Definition) validate() error {
	if d.ID == 0 || d.Key == "" || d.Name == "" {
		return errors.New("id, key and name are required")
	}
	switch d.Slot {
	case Weapon, Relic:
		if len(d.Modifiers) == 0 || d.Heal != 0 {
			return errors.New("persistent equipment needs modifiers and cannot heal")
		}
	case Potion:
		if len(d.Modifiers) != 0 || !finitePositive(d.Heal) {
			return errors.New("potion needs a positive heal and cannot have modifiers")
		}
	default:
		return fmt.Errorf("invalid slot %q", d.Slot)
	}
	for _, modifier := range d.Modifiers {
		if err := modifier.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (m Modifier) validate() error {
	switch m.Stat {
	case Attack, Defense, MaxHealth, MoveSpeed, AttackSpeed:
	default:
		return fmt.Errorf("invalid stat %q", m.Stat)
	}
	if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) || math.Abs(m.Value) > 1e6 {
		return errors.New("modifier value is not finite or bounded")
	}
	switch m.Operation {
	case Add:
	case Multiply:
		if m.Value <= 0 {
			return errors.New("multiply modifier must be positive")
		}
	default:
		return fmt.Errorf("invalid operation %q", m.Operation)
	}
	return nil
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 && value <= 1e6
}

func (c Catalog) Version() uint32 { return c.version }

func (c Catalog) IDs() []ID { return slices.Clone(c.ids) }

func (c Catalog) Lookup(id ID) (Definition, bool) {
	definition, ok := c.items[id]
	definition.Modifiers = slices.Clone(definition.Modifiers)
	return definition, ok
}

func (c Catalog) Require(id ID) (Definition, error) {
	definition, ok := c.Lookup(id)
	if !ok {
		return Definition{}, fmt.Errorf("%w: %d", ErrUnknownEquipment, id)
	}
	return definition, nil
}
