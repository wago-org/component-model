package register

import (
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wago"
)

func TestComponentModelPluginIsRegistered(t *testing.T) {
	ext, ok := wago.NewExtension(component.PluginID)
	if !ok {
		t.Fatalf("plugin %q is not registered", component.PluginID)
	}
	if ext.Info().ID != component.PluginID {
		t.Fatalf("registered plugin ID = %q", ext.Info().ID)
	}
}
