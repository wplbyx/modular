package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
	"github.com/spaolacci/murmur3"
)

const (
	scriptSetBloom = `
for _, offset in ipairs(ARGV) do
	redis.call("SETBIT", KEYS[1], offset, 1)
end
return 1
`

	scriptGetBloom = `
for _, offset in ipairs(ARGV) do
	if tonumber(redis.call("GETBIT", KEYS[1], offset)) == 0 then
		return 0
	end
end
return 1
`
)

// BloomParams 是布隆过滤器的位图参数，由 NewBloomParams 根据容量和误判率计算得出。
// 调用方自行持有该值，Add/MightContain 操作共享同一个参数。
type BloomParams struct {
	Bits   uint
	Hashes uint
}

// NewBloomParams 根据预期元素数量和期望误判率，计算最优的位图大小和哈希函数个数。
func NewBloomParams(capacity uint, falsePositiveRate float64) (BloomParams, error) {
	if capacity == 0 {
		return BloomParams{}, errors.New("bloom capacity must be positive")
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return BloomParams{}, errors.New("bloom false positive rate must be between 0 and 1")
	}

	bits := uint(math.Ceil(-float64(capacity) * math.Log(falsePositiveRate) / math.Pow(math.Log(2), 2)))
	hashes := uint(math.Round(float64(bits) / float64(capacity) * math.Log(2)))
	if hashes == 0 {
		hashes = 1
	}
	return BloomParams{Bits: bits, Hashes: hashes}, nil
}

// BloomAdd 向 Redis bitmap 布隆过滤器添加元素。
func BloomAdd(ctx context.Context, client goredis.UniversalClient, key string, p BloomParams, item []byte) error {
	offsets, err := bloomOffsets(p, item)
	if err != nil {
		return err
	}
	if err := goredis.NewScript(scriptSetBloom).Run(ctx, client, []string{key}, offsets...).Err(); err != nil {
		return fmt.Errorf("set bloom bits: %w", err)
	}
	return nil
}

// BloomMightContain 检查元素是否可能存在（可能有误判）。
func BloomMightContain(ctx context.Context, client goredis.UniversalClient, key string, p BloomParams, item []byte) (bool, error) {
	offsets, err := bloomOffsets(p, item)
	if err != nil {
		return false, err
	}
	result, err := goredis.NewScript(scriptGetBloom).Run(ctx, client, []string{key}, offsets...).Bool()
	if err != nil {
		return false, fmt.Errorf("get bloom bits: %w", err)
	}
	return result, nil
}

func bloomOffsets(p BloomParams, item []byte) ([]interface{}, error) {
	if p.Bits == 0 || p.Hashes == 0 {
		return nil, errors.New("invalid bloom config")
	}
	result := make([]interface{}, 0, p.Hashes)
	for i := uint(0); i < p.Hashes; i++ {
		hash := murmur3.New32WithSeed(uint32(i))
		if _, err := hash.Write(item); err != nil {
			return nil, err
		}
		offset := uint(hash.Sum32()) % p.Bits
		result = append(result, strconv.FormatUint(uint64(offset), 10))
	}
	return result, nil
}
