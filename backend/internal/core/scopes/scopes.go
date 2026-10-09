// Package scopes defines the permission vocabulary shared by scoped API keys and
// OAuth grants. A scope has the form "resource:action". The wildcard Full grants
// every scope, but never the credential-management surface (see the API key
// route table in app/api), which no non-session credential may reach.
package scopes

import (
	"errors"
	"fmt"
	"slices"
	"sort"
)

// Scope is a single named permission.
type Scope = string

const (
	// Full grants every scope. It exists so that API keys created before scopes
	// were introduced keep working unchanged.
	Full Scope = "*"

	ItemsRead   Scope = "items:read"
	ItemsWrite  Scope = "items:write"
	ItemsDelete Scope = "items:delete"

	AttachmentsRead  Scope = "attachments:read"
	AttachmentsWrite Scope = "attachments:write"

	MaintenanceRead  Scope = "maintenance:read"
	MaintenanceWrite Scope = "maintenance:write"

	// CollectionRead covers collection metadata, statistics and member listing.
	CollectionRead Scope = "collection:read"
)

// Preset names offered by the UI and accepted by the API.
const (
	PresetReadOnly  = "read-only"
	PresetReadWrite = "read-write"
	PresetFull      = "full"
)

// ErrInvalid is returned when a scope list contains an unknown scope.
var ErrInvalid = errors.New("invalid scope")

var descriptions = map[Scope]string{
	Full:             "Everything the user can do, except managing credentials",
	ItemsRead:        "Read items, locations, tags, entity types and templates",
	ItemsWrite:       "Create and update items, locations, tags, entity types and templates",
	ItemsDelete:      "Delete items, locations, tags, entity types and templates",
	AttachmentsRead:  "Read attachment metadata and download attachments",
	AttachmentsWrite: "Add, update and remove attachments",
	MaintenanceRead:  "Read maintenance logs and schedules",
	MaintenanceWrite: "Create, update and delete maintenance entries",
	CollectionRead:   "Read collection details, members and statistics",
}

// All returns every grantable scope except Full, in a stable order.
func All() []Scope {
	out := make([]Scope, 0, len(descriptions))
	for s := range descriptions {
		if s != Full {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Description returns a human readable description, or "" for unknown scopes.
func Description(s Scope) string { return descriptions[s] }

// Valid reports whether s is a known scope (including Full).
func Valid(s Scope) bool {
	_, ok := descriptions[s]
	return ok
}

// Preset expands a preset name into its scopes.
func Preset(name string) ([]Scope, error) {
	switch name {
	case PresetReadOnly:
		return []Scope{AttachmentsRead, CollectionRead, ItemsRead, MaintenanceRead}, nil
	case PresetReadWrite:
		return []Scope{
			AttachmentsRead, AttachmentsWrite, CollectionRead,
			ItemsRead, ItemsWrite, MaintenanceRead, MaintenanceWrite,
		}, nil
	case PresetFull:
		return []Scope{Full}, nil
	default:
		return nil, fmt.Errorf("%w: unknown preset %q", ErrInvalid, name)
	}
}

// Normalize validates, de-duplicates and sorts a scope list. An empty input
// yields the read-only preset: the safe default for anything newly issued.
// Full may not be combined with other scopes, as the combination is redundant
// and usually signals a client bug.
func Normalize(in []Scope) ([]Scope, error) {
	if len(in) == 0 {
		return Preset(PresetReadOnly)
	}
	out := slices.Clone(in)
	sort.Strings(out)
	out = slices.Compact(out)
	for _, s := range out {
		if !Valid(s) {
			return nil, fmt.Errorf("%w: %q", ErrInvalid, s)
		}
	}
	if slices.Contains(out, Full) && len(out) > 1 {
		return nil, fmt.Errorf("%w: %q cannot be combined with other scopes", ErrInvalid, Full)
	}
	return out, nil
}

// Has reports whether granted satisfies required. Full satisfies everything.
func Has(granted []Scope, required Scope) bool {
	return slices.Contains(granted, Full) || slices.Contains(granted, required)
}

// Subset reports whether every scope in want is satisfied by granted. It is
// used to cap what a collection owner or an OAuth consent may hand out.
func Subset(want, granted []Scope) bool {
	for _, s := range want {
		if !Has(granted, s) {
			return false
		}
	}
	return true
}

// ReadOnly reports whether the list grants no mutating scope.
func ReadOnly(granted []Scope) bool {
	for _, s := range granted {
		switch s {
		case Full, ItemsWrite, ItemsDelete, AttachmentsWrite, MaintenanceWrite:
			return false
		}
	}
	return true
}

// Collection MCP access levels, set by the collection owner. The level is a
// ceiling: a credential's effective scopes are its own scopes intersected with
// the ceiling of the collection it acts in, so lowering it takes effect on the
// very next request without touching any key or grant.
const (
	AccessOff   = "off"
	AccessRead  = "read"
	AccessWrite = "write"
	AccessFull  = "full"
)

// ValidAccess reports whether level is a known access level.
func ValidAccess(level string) bool {
	switch level {
	case AccessOff, AccessRead, AccessWrite, AccessFull:
		return true
	}
	return false
}

// Ceiling returns the scopes an access level permits. "off" permits none.
// Unknown levels permit none, so a bad value fails closed.
func Ceiling(level string) []Scope {
	switch level {
	case AccessRead:
		p, _ := Preset(PresetReadOnly)
		return p
	case AccessWrite:
		p, _ := Preset(PresetReadWrite)
		return p
	case AccessFull:
		p, _ := Preset(PresetReadWrite)
		return append(p, ItemsDelete)
	default:
		return nil
	}
}

// Intersect returns the scopes in granted that the ceiling also permits. Full in
// granted expands to the whole ceiling, so a full-access key is narrowed to
// exactly what the collection allows.
func Intersect(granted, ceiling []Scope) []Scope {
	if slices.Contains(granted, Full) {
		return slices.Clone(ceiling)
	}
	var out []Scope
	for _, s := range granted {
		if Has(ceiling, s) {
			out = append(out, s)
		}
	}
	return out
}

// Without returns list with the given scopes removed. Full is expanded to the
// non-Full scopes it stands for first, so removing a scope from a wildcard grant
// works as expected.
func Without(list []Scope, remove ...Scope) []Scope {
	if slices.Contains(list, Full) {
		list = All()
	}
	var out []Scope
	for _, s := range list {
		if !slices.Contains(remove, s) {
			out = append(out, s)
		}
	}
	return out
}

// Mutating lists every scope that permits changing data.
func Mutating() []Scope {
	return []Scope{ItemsWrite, ItemsDelete, AttachmentsWrite, MaintenanceWrite}
}
