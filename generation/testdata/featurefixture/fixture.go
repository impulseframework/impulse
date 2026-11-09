// Package featurefixture holds the structs and constants the @feature tests resolve: the
// flags a resources package declares as resource.Feature constants, the resources,
// fields and methods gated behind them in every accepted shape, and the declarations
// each rule refuses.
package featurefixture

import (
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

const (
	// Debriefs lets a crew write and read mission debriefs.
	Debriefs resource.Feature = "debriefs"
	// CargoManifest shows a ship's cargo bays.
	CargoManifest resource.Feature = "cargo_manifest"
)

// NotAFlag is a constant of another type: it declares nothing.
const NotAFlag string = "not_a_flag"

type (
	// Debrief is gated whole.
	// @feature(Debriefs)
	Debrief struct {
		ID    impulse.UUID `spanner:"Id"`
		Title string       `spanner:"Title"`
	}

	// Ship carries one gated field.
	Ship struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
		// @feature(CargoManifest)
		CargoBays *int64 `spanner:"CargoBays"`
	}

	// GatedKey gates its key, which is refused.
	GatedKey struct {
		// @feature(Debriefs)
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}

	// GatedRequired gates a NOT NULL column with no default, which a creatable
	// resource refuses.
	GatedRequired struct {
		ID impulse.UUID `spanner:"Id"`
		// @feature(Debriefs)
		Name string `spanner:"Name"`
	}

	// UnknownFlag names a constant the package does not declare.
	// @feature(Hyperdrive)
	UnknownFlag struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// QuotedFlag names the flag by its value, not its constant.
	// @feature("debriefs")
	QuotedFlag struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// FeatureFlag would take the library's resource name (FeatureFlags).
	FeatureFlag struct {
		ID impulse.UUID `spanner:"Id"`
	}

	// Manifest is a computed resource with one gated field and a gated key.
	Manifest struct {
		// @primarykey
		ID impulse.UUID
		// @feature(CargoManifest)
		Bays int64
	}

	// GatedManifestKey gates a computed resource's key, which is refused.
	GatedManifestKey struct {
		// @primarykey
		// @feature(CargoManifest)
		ID impulse.UUID
	}
)

// SetFeature would take the library's method name.
type SetFeature struct {
	Input string
}

// GatedMethod is gated whole.
// @feature(Debriefs)
type GatedMethod struct {
	Input string
}

// FieldGatedMethod gates one of its fields, which a method refuses.
type FieldGatedMethod struct {
	// @feature(Debriefs)
	Input string
}
