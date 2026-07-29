package configitem

// EventBus configures the process-local RingMPSC-backed event resource.
type EventBus struct {
	Name     string `mapstructure:"Name"`
	Capacity int    `mapstructure:"Capacity" validate:"omitempty,min=1"`
}

// Flags returns process-local EventBus configuration flags.
func (EventBus) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "Name"), Default: "eventbus", Usage: "event bus component name"},
		{Name: flagName(prefix, "Capacity"), Default: 8192, Usage: "event bus queue capacity"},
	}
}
