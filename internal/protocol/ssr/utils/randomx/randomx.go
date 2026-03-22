package randomx

import (
	"crypto/rand"
	"math"
	mrand "math/rand"
	"time"
)

var (
	r = mrand.New(mrand.NewSource(time.Now().UnixNano()))
)

func RandomBytes(size int) []byte {
	bytes := make([]byte, size)
	rand.Read(bytes)
	return bytes
}

func RandomStringsChoice(data []string) string {
	return data[r.Intn(len(data))]
}

func RandomIntChoice(data []int) int {
	return data[r.Intn(len(data))]
}

func RandIntRange(min, max int) int {
	if min == max {
		return min
	}
	return r.Intn((max+1)-min) + min
}

func RandFloat64Range(min, max float64) float64 {
	if min == max {
		return min
	}
	return r.Float64()*(max-min) + min
}

func Uint16() uint16 {
	return uint16(RandIntRange(0, math.MaxUint16))
}

func Float64() float64 {
	return RandFloat64Range(math.SmallestNonzeroFloat64, math.MaxFloat64)
}

func Float64Range(min, max float64) float64 {
	return RandFloat64Range(min, max)
}
