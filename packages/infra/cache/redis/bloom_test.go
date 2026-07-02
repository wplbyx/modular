package redis

import "testing"

func TestNewBloomParams(t *testing.T) {
	p, err := NewBloomParams(1_000_000, 0.02)
	if err != nil {
		t.Fatalf("NewBloomParams() error = %v", err)
	}
	if p.Bits == 0 || p.Hashes == 0 {
		t.Fatalf("NewBloomParams() = %+v", p)
	}
}

func TestBloomOffsets(t *testing.T) {
	p, err := NewBloomParams(1000, 0.01)
	if err != nil {
		t.Fatalf("NewBloomParams() error = %v", err)
	}

	first, err := bloomOffsets(p, []byte("device-1"))
	if err != nil {
		t.Fatalf("bloomOffsets() error = %v", err)
	}
	second, err := bloomOffsets(p, []byte("device-1"))
	if err != nil {
		t.Fatalf("bloomOffsets() second error = %v", err)
	}

	if len(first) != int(p.Hashes) {
		t.Fatalf("bloomOffsets() len = %d, want %d", len(first), p.Hashes)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("bloomOffsets() not stable: %v != %v", first, second)
		}
	}
}
