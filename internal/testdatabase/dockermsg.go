package testdatabase

// This file carries no build tag on purpose. The rest of the package is
// integration-only, but classifying a docker CLI message is pure string work
// with a unit test, and hiding that test behind the integration tag would mean
// a container-backed run were the only thing that could catch a regression in
// it.

import "strings"

// IsMissingObject reports whether a failed docker command failed only because
// the object it names does not exist.
//
// The wording is not stable enough to match literally. The daemon answers
// "Error response from daemon: No such container: x", while the CLI's own
// client-side lookup answers "error: no such object: x" — different subjects
// and different capitalisation for the same condition. Matching one spelling
// exactly turns "this container is not there yet", which is the ordinary
// first-run state, into a hard failure: every integration package then dies
// with `start shared TimescaleDB: inspect container ...: no such object`
// instead of starting the container.
func IsMissingObject(output string) bool {
	lowered := strings.ToLower(output)
	for _, subject := range []string{"container", "object", "image"} {
		if strings.Contains(lowered, "no such "+subject) {
			return true
		}
	}
	return false
}

// IsNameConflict reports whether `docker run --name` failed because another
// process already created a container by that name. For the shared container
// that is not an error: losing the create race means the winner's container
// is the one both processes go on to use.
func IsNameConflict(output string) bool {
	return strings.Contains(strings.ToLower(output), "already in use")
}
