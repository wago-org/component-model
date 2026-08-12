package register

import (
	"testing"

	component "github.com/wago-org/component-model"
)

func TestProvidersReturnsExplicitComponentCatalog(t *testing.T) {
	providers := Providers()
	if len(providers) != 1 {
		t.Fatalf("Providers length = %d, want 1", len(providers))
	}
	if got := providers[0].Definition.ID; got != component.PluginID {
		t.Fatalf("provider ID = %q, want %q", got, component.PluginID)
	}
	if providers[0].New == nil || providers[0].ValidateConfig == nil {
		t.Fatal("provider is missing its factory or config validator")
	}

	providers[0].Definition.Name = "mutated"
	if got := Providers()[0].Definition.Name; got == "mutated" {
		t.Fatal("Providers returned shared mutable metadata")
	}
}
