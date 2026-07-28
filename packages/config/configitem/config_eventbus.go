package configitem

import "github.com/wplbyx/modular/packages/config"

// EventBus configures the process-local RingMPSC-backed event resource.
type EventBus struct {
	Name     string `mapstructure:"Name"`
	Capacity int    `mapstructure:"Capacity" validate:"omitempty,min=1"`
}

// Flags returns process-local EventBus configuration flags.
func (EventBus) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".name", Default: "eventbus", Usage: "event bus component name"},
		{Name: prefix + ".capacity", Default: 8192, Usage: "event bus queue capacity"},
	}
}
