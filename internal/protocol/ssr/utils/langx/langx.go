package langx

import (
	"reflect"
	"slices"
)

func FirstResult(function any, args ...any) any {
	inputs := make([]reflect.Value, len(args))
	for i := range args {
		inputs[i] = reflect.ValueOf(args[i])
	}
	if v := reflect.ValueOf(function); v.Kind() == reflect.Func {
		results := v.Call(inputs)
		if len(results) > 0 {
			return results[0].Interface()
		}

		return nil
	}

	return nil
}

func IntIn(src int, to []int) bool {
	return slices.Contains(to, src)
}
