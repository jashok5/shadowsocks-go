package internal

import (
	"fmt"
	"os"
	"strconv"
	"sync"
)

const (
	DefaultSFCapacity = 1e6
	DefaultSFFPR      = 1e-6
	DefaultSFSlot     = 10
)

const EnvironmentPrefix = "SHADOWSOCKS_"

var saltfilter *BloomRing

var initSaltfilterOnce sync.Once

func getSaltFilterSingleton() *BloomRing {
	initSaltfilterOnce.Do(func() {
		var (
			finalCapacity = DefaultSFCapacity
			finalFPR      = DefaultSFFPR
			finalSlot     = float64(DefaultSFSlot)
		)
		for _, opt := range []struct {
			ENVName string
			Target  *float64
		}{
			{
				ENVName: "CAPACITY",
				Target:  &finalCapacity,
			},
			{
				ENVName: "FPR",
				Target:  &finalFPR,
			},
			{
				ENVName: "SLOT",
				Target:  &finalSlot,
			},
		} {
			envKey := EnvironmentPrefix + "SF_" + opt.ENVName
			env := os.Getenv(envKey)
			if env != "" {
				p, err := strconv.ParseFloat(env, 64)
				if err != nil {
					panic(fmt.Sprintf("Invalid envrionment `%s` setting in saltfilter: %s", envKey, env))
				}
				*opt.Target = p
			}
		}
		if finalCapacity <= 0 {
			return
		}
		saltfilter = NewBloomRing(int(finalSlot), int(finalCapacity), finalFPR)
	})
	return saltfilter
}

func AddSalt(b []byte) {
	getSaltFilterSingleton().Add(b)
}

func CheckSalt(b []byte) bool {
	return getSaltFilterSingleton().Test(b)
}
