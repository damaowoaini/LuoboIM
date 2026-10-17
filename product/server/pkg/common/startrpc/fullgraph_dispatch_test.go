package startrpc

import "testing"

// Linux integration adapter only. A platform without the actual Linux process
// graph fails the required fixture rather than replacing it with v1/Skip.
// The original ack and redis-unknown unit branches remain independent.
var fullgraphRequiredTokenRunner func(*testing.T)
