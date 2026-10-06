package testdatabase

import "testing"

// The two spellings below are verbatim docker output, not paraphrases: the
// first is what the CLI printed on the machine where the case-sensitive match
// this helper replaced broke every integration package, and the second is the
// daemon's own phrasing for the same condition.
func TestIsMissingObjectAcceptsBothDockerSpellings(t *testing.T) {
	cases := map[string]bool{
		"error: no such object: faultline-itest-9508616d5b94":                     true,
		"Error response from daemon: No such container: faultline-itest-9508616d": true,
		"Error: No such object: faultline-itest-9508616d5b94":                     true,
		"Error response from daemon: No such image: timescale/timescaledb:latest": true,
		"": false,
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock":  false,
		"error during connect: Get \"http://docker/v1.51/containers/x/json\"": false,
	}
	for output, want := range cases {
		if got := IsMissingObject(output); got != want {
			t.Errorf("IsMissingObject(%q) = %v, want %v", output, got, want)
		}
	}
}

// A daemon that is unreachable must NOT read as "the object is missing":
// treating it as missing would make every caller try to create the container
// and report the creation failure instead of the real cause.
func TestIsMissingObjectRejectsAnUnreachableDaemon(t *testing.T) {
	if IsMissingObject("Cannot connect to the Docker daemon. Is the docker daemon running?") {
		t.Error("an unreachable daemon must not be reported as a missing object")
	}
}

func TestIsNameConflictMatchesTheCreateRace(t *testing.T) {
	conflict := `docker: Error response from daemon: Conflict. The container name ` +
		`"/faultline-itest-9508616d5b94" is already in use by container "abc123".`
	if !IsNameConflict(conflict) {
		t.Errorf("IsNameConflict(%q) = false, want true", conflict)
	}
	if IsNameConflict("error: no such object: faultline-itest-9508616d5b94") {
		t.Error("a missing container must not read as a name conflict")
	}
}
