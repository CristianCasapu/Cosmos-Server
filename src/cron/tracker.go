package cron

import "github.com/azukaar/cosmos-server/src/utils"

// InternalProcessTracker is the restart-blocking tracker shared with the rest
// of the server (see utils.InternalProcessTracker); kept under its old name.
var InternalProcessTracker = utils.InternalProcessTracker
