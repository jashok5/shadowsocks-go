package log

import "github.com/sirupsen/logrus"

func Debug(message string, params ...any) {
	if params != nil {
		logrus.Debugf(message, params...)
	} else {
		logrus.Debug(message)
	}
}

func Info(message string, params ...any) {
	if params != nil {
		logrus.Infof(message, params...)
	} else {
		logrus.Info(message)
	}
}

func Warn(message string, params ...any) {
	if params != nil {
		logrus.Warnf(message, params...)
	} else {
		logrus.Warn(message)
	}
}

func Error(message string, params ...any) {
	if params != nil {
		logrus.Errorf(message, params...)
	} else {
		logrus.Error(message)
	}
}
