package metadata

import "sort"

// MapCarrier adapts map-based pub/sub headers to Carrier.
type MapCarrier map[string]string

func (m MapCarrier) Get(key string) string {
	if value, ok := m[key]; ok {
		return value
	}
	for candidate, value := range m {
		if normalizeKey(candidate) == normalizeKey(key) {
			return value
		}
	}
	return ""
}

func (m MapCarrier) Set(key, value string) { m[normalizeKey(key)] = value }

func (m MapCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
