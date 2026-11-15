package configapi

// Fork addition: the log level is one of the known ones. Upstream's
// validation tag on LoggingLevel is malformed, so any text was saved, and an
// unknown level counts as DEBUG (which also turns on the profiling routes).
// A level that is not changed is left alone, so an older file still saves.

import "github.com/azukaar/cosmos-server/src/utils"

// loggingLevelProblem returns why the level of a save is refused, "" when it is fine
func loggingLevelProblem(level utils.LoggingLevel, stored utils.LoggingLevel) string {
	if level == stored {
		return ""
	}
	if _, known := utils.LoggingLevelLabels[level]; !known {
		return "The log level is one of DEBUG, INFO, WARNING and ERROR."
	}
	return ""
}
