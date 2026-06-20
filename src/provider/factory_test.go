package provider

import (
	"testing"

	"github.com/rishang/seek/config"
)

func TestNewFactorySkipsUnconfigured(t *testing.T) {
	f := NewFactory([]config.ProviderConfig{
		{Name: "tavily"},                               // no key, no host -> skipped
		{Name: "exa", APIKey: "k"},                     // key -> built
		{Name: "lightpanda", Host: "http://localhost"}, // host only -> built
	})
	if f.Get("tavily") != nil {
		t.Error("tavily has no key/host; should not be built")
	}
	if f.Get("exa") == nil {
		t.Error("exa has a key; should be built")
	}
	if f.Get("lightpanda") == nil {
		t.Error("lightpanda has a host; should be built")
	}
}
