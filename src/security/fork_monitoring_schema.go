package security

// Fork addition (plan 51): the record's table is part of the monitoring
// store's schema for utils.PrepareMonitoringSchema, which lays out a store
// Cosmos is not using (the copy of the history, src/monitorcopy). The table is
// created by this package's own code, createSchema.

import "github.com/azukaar/cosmos-server/src/utils"

func init() {
	utils.RegisterMonitoringSchema(tableName, createSchema)
}
