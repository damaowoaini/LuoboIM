package dbconn

import (
	"context"
	"gorm.io/gorm/logger"
)

// GORM's ParamsFilter extension prevents password verifiers, codes and personal
// data from being substituted into operational SQL logs.
type parameterizedLogger struct{ logger.Interface }

func (l parameterizedLogger) LogMode(level logger.LogLevel) logger.Interface {
	return parameterizedLogger{Interface: l.Interface.LogMode(level)}
}
func (parameterizedLogger) ParamsFilter(_ context.Context, sql string, _ ...interface{}) (string, []interface{}) {
	return sql, nil
}
