// Package register exposes the Component Model provider catalog to generated
// Wago runtimes. Importing this package has no registration side effects.
package register

import (
	component "github.com/wago-org/component-model"
	"github.com/wago-org/wago"
)

// Providers returns fresh explicit catalog entries.
func Providers() []wago.PluginProvider {
	return []wago.PluginProvider{component.Provider()}
}
