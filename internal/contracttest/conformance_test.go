//go:build conformance

package contracttest

import "testing"

func TestConformanceCurrent(t *testing.T) {
	bin := buildServer(t, ".")
	base := startServer(t, bin, generalProfileEnv())
	Run(t, base, smokeScenarios())
}
