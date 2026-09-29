package tunnel

import (
	"os/exec"
	"strconv"
	"strings"
)

// ListenerPIDs returns the processes listening on a local TCP port. It is
// best-effort and shells out to lsof, which macOS and most Linux distros ship.
func ListenerPIDs(port int) []int {
	out, err := exec.Command("lsof", "-nP", "-t", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// ProcessCommand returns the command line of a pid, or "" when unknown.
func ProcessCommand(pid int) string {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
