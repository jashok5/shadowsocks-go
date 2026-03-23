package stringx

import (
	"strings"
)

func IsDigit(data string) bool {
	if len(data) != 1 {
		return false
	}
	if strings.ContainsAny(data, "1234567890") {
		return true
	}
	return false
}
