package grpc

import (
	"github.com/asynkron/protoactor-go/log"
)

var plog = log.New(log.DebugLevel, "[REMOTE]")

// SetLogLevel sets the log level for the logger.
//
// SetLogLevel is safe to call concurrently
func SetLogLevel(level log.Level) {
	plog.SetLevel(level)
}
