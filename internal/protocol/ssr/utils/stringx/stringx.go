package stringx

import (
	"strings"
)

func IsDigit(data string) bool {
	if len(data) != 1 {
		return false
	}
	if strings.IndexAny(data, "1234567890") != -1 {
		return true
	}
	return false
}
