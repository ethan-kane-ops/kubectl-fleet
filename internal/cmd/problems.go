package cmd

import (
	"strconv"
	"strings"
)

// healthyStatus are STATUS words that mean all is well. SchedulingDisabled
// (a cordoned node) is deliberate, not a fault.
var healthyStatus = map[string]bool{
	"Running": true, "Completed": true, "Complete": true, "Succeeded": true,
	"Ready": true, "Bound": true, "Active": true, "True": true,
	"SchedulingDisabled": true,
}

// problemRow judges one table row from its column names, for any kind:
//   - a READY condition (True/False/Unknown, as cert-manager and Flux
//     print it) decides alone: anything but True is a problem;
//   - otherwise a one-word STATUS decides: any comma-separated part that is
//     not a healthy word is a problem, and so is Running with READY x/y,
//     x < y. A STATUS holding a sentence (a condition message) or nothing
//     says too little to judge and is ignored;
//   - otherwise READY x/y with x < y, or a bare READY below DESIRED.
//
// ok is false when the row has neither STATUS nor READY to judge by.
func problemRow(columns, cells []string) (problem, ok bool) {
	cell := func(name string) (string, bool) {
		for i, c := range columns {
			if c == name && i < len(cells) {
				return strings.TrimSpace(cells[i]), true
			}
		}
		return "", false
	}
	status, hasStatus := cell("STATUS")
	ready, hasReady := cell("READY")
	if !hasStatus && !hasReady {
		return false, false
	}
	switch ready {
	case "True":
		return false, true
	case "False", "Unknown":
		return true, true
	}
	if hasStatus && status != "" && !strings.Contains(status, " ") {
		for _, part := range strings.Split(status, ",") {
			if !healthyStatus[strings.TrimSpace(part)] {
				return true, true
			}
		}
		return status == "Running" && hasReady && short(ready, ""), true
	}
	if hasReady {
		desired, _ := cell("DESIRED")
		return short(ready, desired), true
	}
	return false, true
}

// short reports READY below what is wanted: x/y with x < y, or a bare count
// below desired.
func short(ready, desired string) bool {
	if have, want, found := strings.Cut(ready, "/"); found {
		h, err1 := strconv.Atoi(have)
		w, err2 := strconv.Atoi(want)
		return err1 == nil && err2 == nil && h < w
	}
	h, err1 := strconv.Atoi(ready)
	w, err2 := strconv.Atoi(desired)
	return err1 == nil && err2 == nil && h < w
}
