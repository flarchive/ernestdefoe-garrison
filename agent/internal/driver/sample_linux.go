//go:build linux

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 🚨 This file is the answer to "there is no `docker stats` for a bare
// process". Two strategies, in order of truthfulness:
//
//  1. cgroup v2, when the process has a cgroup of its own. It accounts for the
//     whole tree including anything the game forked, it is what the kernel
//     itself charges, and it carries a memory limit when one is set.
//  2. /proc, walked over the entire descendant tree. Correct but more work,
//     and it is what most bare-metal installs will actually use, because
//     nobody puts a hand-started Valheim in its own slice.
//
// Sampling only the PID we launched is the tempting third option and it is
// always wrong: a Minecraft server's real memory is the JVM plus whatever it
// forked, so the top-level process alone reports a number far too small.

type sample struct {
	CPUPercent  float64
	MemoryBytes uint64
	MemoryLimit uint64
	Processes   int
	Source      string
}

// cpuPrev remembers the previous CPU reading per PID so a percentage can be
// derived. CPU usage is a rate, and a single reading of a counter is not one —
// the first sample for a server therefore reports 0 and the second is real.
var (
	cpuMu   sync.Mutex
	cpuPrev = map[int]cpuReading{}
)

type cpuReading struct {
	at time.Time
	// CPU time consumed, in SECONDS.
	//
	// 🚨 Seconds, not "ticks" or "usec". Both sources count CPU time in their
	// own unit — cgroup v2 in microseconds, /proc in USER_HZ jiffies — and the
	// first version of this file passed each one's raw counter into a shared
	// rate function with a comment claiming they matched. They did not: usec
	// was multiplied by 10 where it needed dividing by 10,000, and a freshly
	// started process reported **10,705% CPU** on the status page. Converting
	// at the source, into one obvious unit, makes that class of mistake
	// impossible rather than merely unlikely.
	seconds float64
}

func sampleProcessTree(pid int) (sample, error) {
	if s, err := sampleCgroup2(pid); err == nil {
		return s, nil
	}
	return sampleProc(pid)
}

// ---- cgroup v2 -----------------------------------------------------------

func sampleCgroup2(pid int) (sample, error) {
	var out sample

	rel, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return out, err
	}
	// Unified hierarchy lines look like "0::/system.slice/valheim.service".
	var path string
	for _, line := range strings.Split(string(rel), "\n") {
		if strings.HasPrefix(line, "0::") {
			path = strings.TrimPrefix(line, "0::")
			break
		}
	}
	if path == "" || path == "/" {
		// The root cgroup accounts for the entire machine, not this server.
		// Reporting it would be wildly wrong, so decline and let /proc answer.
		return out, fmt.Errorf("no dedicated cgroup")
	}

	base := filepath.Join("/sys/fs/cgroup", path)
	current, err := readUint(filepath.Join(base, "memory.current"))
	if err != nil {
		return out, err
	}
	out.MemoryBytes = current
	out.Source = "cgroup2"

	if lim, err := readUint(filepath.Join(base, "memory.max")); err == nil {
		out.MemoryLimit = lim // absent or "max" leaves it zero, meaning unlimited
	}

	if usec, err := readCPUStat(filepath.Join(base, "cpu.stat")); err == nil {
		out.CPUPercent = cpuRate(pid, float64(usec)/1e6) // microseconds -> seconds
	}
	if n, err := countPIDs(filepath.Join(base, "cgroup.procs")); err == nil {
		out.Processes = n
	}
	return out, nil
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}

func readCPUStat(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "usage_usec "); ok {
			return strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
		}
	}
	return 0, fmt.Errorf("usage_usec not found")
}

func countPIDs(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// ---- /proc ---------------------------------------------------------------

func sampleProc(root int) (sample, error) {
	out := sample{Source: "proc"}

	kids, err := descendants(root)
	if err != nil {
		return out, err
	}
	out.Processes = len(kids)

	var ticks uint64
	for _, pid := range kids {
		st, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue // it exited between listing and reading; normal
		}
		utime, stime, ok := parseStatTimes(string(st))
		if ok {
			ticks += utime + stime
		}
		if rss, err := readRSS(pid); err == nil {
			out.MemoryBytes += rss
		}
	}
	// USER_HZ is 100 on every Linux Go builds for, so a jiffy is 10ms.
	const userHz = 100
	out.CPUPercent = cpuRate(root, float64(ticks)/userHz)
	return out, nil
}

// parseStatTimes pulls utime and stime out of /proc/<pid>/stat.
//
// 🚨 Field 2 is the executable name IN PARENTHESES, and it may itself contain
// spaces and parentheses — a server started from a script called
// "run (new) server.sh" breaks any parser that splits the line on whitespace.
// Seek the LAST ')' and count from there.
func parseStatTimes(line string) (utime, stime uint64, ok bool) {
	i := strings.LastIndex(line, ")")
	if i < 0 || i+2 >= len(line) {
		return 0, 0, false
	}
	fields := strings.Fields(line[i+2:])
	// After the comm field, index 0 is state; utime is field 14 and stime 15
	// of the whole line, which is 11 and 12 here.
	if len(fields) < 13 {
		return 0, 0, false
	}
	u, err1 := strconv.ParseUint(fields[11], 10, 64)
	s, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return u, s, true
}

func readRSS(pid int) (uint64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, fmt.Errorf("short statm")
	}
	pages, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * uint64(os.Getpagesize()), nil
}

// descendants returns root and every process below it.
func descendants(root int) ([]int, error) {
	children := map[int][]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		i := strings.LastIndex(string(b), ")")
		if i < 0 {
			continue
		}
		f := strings.Fields(string(b)[i+2:])
		if len(f) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}

	var out []int
	seen := map[int]bool{}
	var walk func(int)
	walk = func(p int) {
		if seen[p] {
			return // a PID cannot really be its own ancestor, but do not hang if it is
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

// cpuRate turns a monotonic counter of CPU SECONDS into a percentage, using
// the previous reading for the same PID.
func cpuRate(pid int, seconds float64) float64 {
	return rateFrom(&cpuMu, cpuPrev, pid, seconds, time.Now())
}

// rateFrom is the arithmetic on its own, so it can be tested without a real
// process and without waiting a second between samples.
func rateFrom(mu *sync.Mutex, prevs map[int]cpuReading, pid int, seconds float64, now time.Time) float64 {
	mu.Lock()
	defer mu.Unlock()

	prev, ok := prevs[pid]
	prevs[pid] = cpuReading{at: now, seconds: seconds}

	if !ok {
		return 0 // first reading: a counter is not a rate
	}

	elapsed := now.Sub(prev.at).Seconds()
	if elapsed <= 0 || seconds < prev.seconds {
		return 0 // clock went backwards, or the process restarted and reset
	}

	return (seconds - prev.seconds) / elapsed * 100
}

// Forget drops remembered CPU readings for a PID that has gone away, so the
// map does not grow for the life of the agent.
func Forget(pid int) {
	cpuMu.Lock()
	delete(cpuPrev, pid)
	cpuMu.Unlock()
}
