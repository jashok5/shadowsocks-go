package atperr

import (
	"errors"
	"fmt"
)

type Code uint16

const (
	CodeBadRequest   Code = 1000
	CodeBadVersion   Code = 1001
	CodeAuthFailed   Code = 1002
	CodeInvalidFrame Code = 1003
	CodeFlowControl  Code = 1004
	CodeTimeout      Code = 1005
	CodeInternal     Code = 1006
)

type Error struct {
	Code   Code
	Reason string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Reason == "" {
		return fmt.Sprintf("atp error code=%d", e.Code)
	}
	return fmt.Sprintf("atp error code=%d reason=%s", e.Code, e.Reason)
}

func IsCode(err error, code Code) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}
