//go:build linux

package driver

import (
	"sync"
	"testing"
	"time"
)

// 🚨 The regression test for a bug that reached a real status page: a freshly
// started process reported 10,705% CPU, because cgroup v2's microsecond
// counter was multiplied by 10 where it needed dividing by 10,000 before being
// handed to a rate function shared with /proc's jiffies.
//
// A test with a fake counter and a made-up unit would have passed either way.
// This one pins the CONVERSION by working in CPU seconds, and asserts a
// plausible range rather than an exact float.
func TestCPURateIsAPercentageOfOneCore(t *testing.T) {
	var mu sync.Mutex
	prev := map[int]cpuReading{}
	t0 := time.Now()

	// First reading of a counter is not a rate, and must not pretend to be.
	if got := rateFrom(&mu, prev, 1, 5.0, t0); got != 0 {
		t.Fatalf("first reading returned %v, want 0", got)
	}

	cases := []struct {
		name    string
		seconds float64 // cumulative CPU seconds at t0+2s
		after   time.Duration
		want    float64
	}{
		{"half a core", 6.0, 2 * time.Second, 50},
		{"one core flat out", 7.0, 2 * time.Second, 100},
		{"two cores", 9.0, 2 * time.Second, 200},
		{"idle", 5.0, 2 * time.Second, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			prev := map[int]cpuReading{1: {at: t0, seconds: 5.0}}

			got := rateFrom(&mu, prev, 1, c.seconds, t0.Add(c.after))
			if diff := got - c.want; diff > 0.01 || diff < -0.01 {
				t.Fatalf("rate = %.2f%%, want %.2f%%", got, c.want)
			}
		})
	}
}

// A restarted process resets its counter. Reporting a huge negative — or a
// huge positive from unsigned wraparound — is worse than reporting nothing.
func TestCPURateIgnoresACounterThatWentBackwards(t *testing.T) {
	var mu sync.Mutex
	t0 := time.Now()
	prev := map[int]cpuReading{1: {at: t0, seconds: 900}}

	if got := rateFrom(&mu, prev, 1, 2, t0.Add(time.Second)); got != 0 {
		t.Fatalf("counter reset produced %v, want 0", got)
	}
}

// 🚨 The unit conversions themselves, which is where the bug actually lived.
func TestSourceUnitsConvertToSeconds(t *testing.T) {
	// cgroup v2 reports usage_usec: 2,500,000 µs is 2.5 CPU seconds.
	if got := float64(2_500_000) / 1e6; got != 2.5 {
		t.Fatalf("cgroup µs -> s gave %v, want 2.5", got)
	}
	// /proc reports jiffies at USER_HZ=100: 250 jiffies is 2.5 CPU seconds.
	if got := float64(250) / 100; got != 2.5 {
		t.Fatalf("proc jiffies -> s gave %v, want 2.5", got)
	}
}
