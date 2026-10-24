//go:build !linux

package driver

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Agents ship for Linux; this file exists so the whole thing still builds and
// tests on the machine it is written on. `ps` is not as truthful as cgroups —
// no memory limit, and RSS double-counts shared pages across a process tree —
// so Source says "ps" and the forum can label it as approximate rather than
// quietly presenting a worse number as if it were the same one.

type sample struct {
	CPUPercent  float64
	MemoryBytes uint64
	MemoryLimit uint64
	Processes   int
	Source      string
}

func sampleProcessTree(pid int) (sample, error) {
	out := sample{Source: "ps"}

	pids, err := descendants(pid)
	if err != nil {
		return out, err
	}
	out.Processes = len(pids)

	for _, p := range pids {
		cpu, rssKB, err := psOne(p)
		if err != nil {
			continue // exited between listing and reading; normal
		}
		out.CPUPercent += cpu
		out.MemoryBytes += rssKB * 1024
	}
	return out, nil
}

func psOne(pid int) (cpu float64, rssKB uint64, err error) {
	b, err := exec.Command("ps", "-o", "%cpu=,rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, 0, fmt.Errorf("unexpected ps output %q", string(b))
	}
	cpu, _ = strconv.ParseFloat(f[0], 64)
	rssKB, _ = strconv.ParseUint(f[1], 10, 64)
	return cpu, rssKB, nil
}

func descendants(root int) ([]int, error) {
	b, err := exec.Command("ps", "-Ao", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}

	var out []int
	seen := map[int]bool{}
	var walk func(int)
	walk = func(p int) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
		for _, c := range children[p] {
			walk(c)
		}
	}
	walk(root)
	return out, nil
}

// Forget is a no-op here; the ps path holds no per-PID state.
func Forget(pid int) {}
