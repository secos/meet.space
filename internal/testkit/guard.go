package testkit

import (
	"meet.space/internal/testkit/nonet"
)

// ErrNetworkInTest is returned when a test tries to reach the network.
// Importing testkit installs the guard (see package nonet).
var ErrNetworkInTest = nonet.ErrNetworkInTest
