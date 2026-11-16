package main

// Fork addition (plan 45): the e-mail and the notification sent when apps are
// installed are src/appsignin's; it hooks itself into the creation route.

import "github.com/azukaar/cosmos-server/src/appsignin"

func init() { appsignin.Start() }
