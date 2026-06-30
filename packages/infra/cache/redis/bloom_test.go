package redis

import "testing"

func TestNewBloomConfig(t *testing.T) {
	cfg, err := newBloomConfig(1_000_000, 0.02)
	if err != nil {
		t.Fatalf("newBloomConfig() error = %v", err)
	}
	if cfg.bits == 0 || cfg.hashes == 0 {
		t.Fatalf("newBloomConfig() = %+v", cfg)
	}
}

func TestBloomOffsets(t *testing.T) {
	cfg, err := newBloomConfig(1000, 0.01)
	if err != nil {
		t.Fatalf("newBloomConfig() error = %v", err)
	}

	first, err := bloomOffsets(cfg, []byte("device-1"))
	if err != nil {
		t.Fatalf("bloomOffsets() error = %v", err)
	}
	second, err := bloomOffsets(cfg, []byte("device-1"))
	if err != nil {
		t.Fatalf("bloomOffsets() second error = %v", err)
	}

	if len(first) != int(cfg.hashes) {
		t.Fatalf("bloomOffsets() len = %d, want %d", len(first), cfg.hashes)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("bloomOffsets() not stable: %v != %v", first, second)
		}
	}
}
