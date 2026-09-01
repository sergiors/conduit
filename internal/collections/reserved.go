package collections

import "strings"

// reservedCollectionNames are the internal Conduit configuration namespaces
// that must never be shadowed by a user-created CDC collection. They are the
// physical MongoDB collections the Manager itself owns (see NewManager):
// config.collections holds collection settings, config.sinks holds sink
// settings, and config.dlq holds dead-letter entries. Creating a CDC collection
// with one of these names would collide with Conduit's own control-plane
// storage.
var reservedCollectionNames = map[string]struct{}{
	"config.collections": {},
	"config.sinks":       {},
	"config.dlq":         {},
}

// IsReservedCollectionName reports whether name is reserved and therefore must
// not be used as a CDC collection. Reserved names are Conduit's internal
// configuration namespaces (config.collections, config.sinks, config.dlq) and
// MongoDB's system namespaces (system.*), which MongoDB itself reserves for
// internal use. The check is exact and case-sensitive, matching how MongoDB
// treats collection names.
func IsReservedCollectionName(name string) bool {
	if _, ok := reservedCollectionNames[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "system.")
}
