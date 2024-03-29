// Copyright (c) 2015 Klaus Post, 2023 Eik Madsen, released under MIT License. See LICENSE file.

package shutdown

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func startTimer(m *Manager, t *testing.T) chan struct{} {
	finished := make(chan struct{})
	m.srM.RLock()
	var to time.Duration
	for i := range m.timeouts {
		to += m.timeouts[i]
	}
	m.srM.RUnlock()
	// Add some extra time.
	toc := time.After((to * 10) / 9)
	go func() {
		select {
		case <-toc:
			pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
			panic("unexpected timeout while running test")
		case <-finished:
			return
		}
	}()
	return finished
}

func TestBasic(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	f := m.First()
	ok := false
	go func() {
		n := <-f.Notify()
		ok = true
		close(n)
	}()
	m.Shutdown()
	if !ok {
		t.Fatal("did not get expected shutdown signal")
	}
	if !m.Started() {
		t.Fatal("shutdown not marked started")
	}
	// Should just return at once.
	m.Shutdown()
	// Should also return at once.
	m.Wait()
}

func TestCancel(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	f := m.First()
	ok := false
	go func() {
		n := <-f.Notify()
		ok = true
		close(n)
	}()
	f.Cancel()
	m.Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestCancel2(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	f2 := m.First()
	f := m.First()
	var ok, ok2 bool

	go func() {
		n := <-f.Notify()
		ok = true
		close(n)
	}()
	go func() {
		n := <-f2.Notify()
		ok2 = true
		close(n)
	}()
	f.Cancel()
	m.Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
	if !ok2 {
		t.Fatal("missing shutdown signal")
	}
}

func TestCancelWait(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 1000))

	defer close(startTimer(m, t))
	f := m.First()
	var ok bool
	go func() {
		n := <-f.Notify()
		ok = true
		close(n)
	}()
	f.CancelWait()
	m.Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestCancelWait2(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 1000))

	defer close(startTimer(m, t))
	f2 := m.First()
	f := m.First()
	var ok, ok2 bool

	go func() {
		n := <-f.Notify()
		ok = true
		close(n)

	}()
	go func() {
		n := <-f2.Notify()
		ok2 = true
		close(n)
	}()
	f.CancelWait()
	m.Shutdown()
	if ok {
		t.Fatal("got unexpected shutdown signal")
	}
	if !ok2 {
		t.Fatal("missing shutdown signal")
	}
}

// TestCancelWait3 assert that we can CancelWait, and that wait will wait until the
// specified stage.
func TestCancelWait3(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 3000))

	// defer close(startTimer(m, t))
	f := m.First()
	var ok, ok2, ok3 bool
	f2 := m.Second()
	cancelled := make(chan struct{})
	reached := make(chan struct{})
	p2started := make(chan struct{})
	_ = m.SecondFn(func() {
		<-p2started
		close(reached)
	})
	var wg sync.WaitGroup
	go func() {
		select {
		case v := <-f2.Notify():
			ok3 = true
			close(v)
		case <-cancelled:
		}
	}()
	wg.Add(1)
	go func() {
		n := <-f.Notify()
		ok = true
		go func() {
			wg.Done()
			close(cancelled)
			f2.CancelWait()
			// We should be at stage 2
			close(p2started)
			<-reached
		}()
		wg.Wait()
		time.Sleep(10 * time.Millisecond)
		close(n)
	}()
	m.Shutdown()
	if !ok {
		t.Fatal("missing shutdown signal")
	}
	if ok2 {
		t.Fatal("got unexpected shutdown signal")
	}
	if ok3 {
		t.Fatal("got unexpected shutdown signal")
	}
}

// TestCancelWait4 assert that we can CancelWait on a previous stage,
// and it doesn't block.
func TestCancelWait4(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 100))
	defer close(startTimer(m, t))
	f := m.Second()
	var ok bool
	f2 := m.First()
	go func() {
		n := <-f.Notify()
		// Should not wait
		f2.CancelWait()
		ok = true
		close(n)
	}()
	m.Shutdown()
	if !ok {
		t.Fatal("missing shutdown signal")
	}
}

type logBuffer struct {
	buf bytes.Buffer
	fn  func(string, ...interface{})
	sync.Mutex
}

func (l *logBuffer) WriteF(format string, a ...interface{}) {
	//fmt.Printf(format, a...)
	l.fn(format, a...)
	l.Lock()
	l.buf.WriteString(fmt.Sprintf(format, a...) + "\n")
	l.Unlock()
}

// TestContextLog assert that context is logged as expected.
func TestContextLog(t *testing.T) {
	var buf = &logBuffer{fn: t.Logf}
	m := New(WithLogPrinter(buf.WriteF), WithTimeout(10*time.Millisecond))
	defer close(startTimer(m, t))

	txt1 := "arbitrary text"
	txt2 := "something else"
	txt3 := 456778
	txt4 := time.Now()
	txtL := "politically correct text"
	_ = m.Lock(txtL)
	_ = m.First(txt1)
	_ = m.Second(txt2, txt3)
	_ = m.ThirdFn(func() { select {} }, txt4)
	m.Shutdown()
	logged := buf.buf.String()
	if !strings.Contains(logged, txt1) {
		t.Errorf("Log should contain %s", txt1)
	}
	if !strings.Contains(logged, txt2) {
		t.Errorf("Log should contain %s", txt2)
	}
	if !strings.Contains(logged, fmt.Sprintf("%v", txt3)) {
		t.Errorf("Log should contain %v", txt3)
	}
	if !strings.Contains(logged, fmt.Sprintf("%v", txt4)) {
		t.Errorf("Log should contain %v", txt4)
	}
	if !strings.Contains(logged, fmt.Sprintf("%v", txtL)) {
		t.Errorf("Log should contain %v", txtL)
	}
}

func TestFnCancelWait(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	f := m.First()
	var ok, ok2 bool
	f2 := m.SecondFn(func() {
		ok2 = true
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		n := <-f.Notify()
		ok = true
		go func() {
			wg.Done()
			f2.CancelWait()
		}()
		wg.Wait()
		time.Sleep(10 * time.Millisecond)
		close(n)
	}()
	m.Shutdown()
	if !ok {
		t.Fatal("missing shutdown signal")
	}
	if ok2 {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestNilNotifier(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	var reached = make(chan struct{})
	var finished = make(chan struct{})
	var testDone = make(chan struct{})
	_ = m.ThirdFn(func() { close(reached); <-finished })
	go func() { m.Shutdown(); close(testDone) }()

	// Wait for stage 3
	<-reached

	tests := []Notifier{m.PreShutdown(), m.First(), m.Second(), m.Third(),
		m.PreShutdownFn(func() {}), m.FirstFn(func() {}), m.SecondFn(func() {}), m.ThirdFn(func() {})}

	for i := range tests {
		if tests[i].Valid() {
			t.Errorf("Expected test %d to be invalid, was %#v", i, tests[i].Valid())
		}
	}
	close(finished)
	<-testDone
}

func TestNilNotifierCancel(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	var reached = make(chan struct{})
	var finished = make(chan struct{})
	var testDone = make(chan struct{})
	_ = m.ThirdFn(func() { close(reached); <-finished })
	go func() { m.Shutdown(); close(testDone) }()

	// Wait for stage 3
	<-reached

	tests := []Notifier{m.PreShutdown(), m.First(), m.Second(), m.Third(),
		m.PreShutdownFn(func() {}), m.FirstFn(func() {}), m.SecondFn(func() {}), m.ThirdFn(func() {})}

	for i := range tests {
		// All cancels should return at once.
		tests[i].Cancel()
	}
	close(finished)
	<-testDone
}

func TestNilNotifierCancelWait(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	var reached = make(chan struct{})
	var finished = make(chan struct{})
	var testDone = make(chan struct{})
	_ = m.ThirdFn(func() { close(reached); <-finished })
	go func() { m.Shutdown(); close(testDone) }()

	// Wait for stage 3
	<-reached

	tests := []Notifier{m.PreShutdown(), m.First(), m.Second(), m.Third(),
		m.PreShutdownFn(func() {}), m.FirstFn(func() {}), m.SecondFn(func() {}), m.ThirdFn(func() {})}

	for i := range tests {
		// All cancel-waits should return at once.
		tests[i].CancelWait()
	}
	close(finished)
	<-testDone
}

func TestNilNotifierFollowing(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	var reached = make(chan struct{})
	var finished = make(chan struct{})
	var testDone = make(chan struct{})
	_ = m.PreShutdownFn(func() { close(reached); <-finished })
	go func() { m.Shutdown(); close(testDone) }()

	// Wait for stage 3
	<-reached

	tests := []Notifier{m.First(), m.Second(), m.Third(),
		m.FirstFn(func() {}), m.SecondFn(func() {}), m.ThirdFn(func() {})}

	for i := range tests {
		if !tests[i].Valid() {
			t.Errorf("Expected test %d to NOT be nil.", i)
			continue
		}
		tests[i].Cancel()
	}
	close(finished)
	<-testDone
}

func TestWait(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	ok := make(chan bool)
	go func() {
		m.Wait()
		close(ok)
	}()
	// Wait a little - enough to fail very often.
	time.Sleep(time.Millisecond * 10)

	select {
	case <-ok:
		t.Fatal("Wait returned before shutdown finished")
	default:
	}

	m.Shutdown()

	// ok should return, otherwise we wait for timeout, which will fail the test
	<-ok
}

func TestTimeout(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 400))

	defer close(startTimer(m, t))
	f := m.First()
	go func() {
		<-f.Notify()
	}()
	tn := time.Now()
	m.Shutdown()
	dur := time.Since(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Since(tn))
	}
	if !m.Started() {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestTimeoutN(t *testing.T) {
	m := New(WithTimeout(time.Millisecond*2000), WithTimeoutN(Stage1, time.Millisecond*100))

	defer close(startTimer(m, t))
	f := m.First()
	go func() {
		<-f.Notify()
	}()
	tn := time.Now()
	m.Shutdown()
	dur := time.Since(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Since(tn))
	}
	if !m.Started() {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestTimeoutCallback(t *testing.T) {
	var gotStage Stage
	var gotCtx string
	m := New(WithOnTimeout(func(s Stage, ctx string) {
		gotStage = s
		gotCtx = ctx
	}), WithTimeout(time.Millisecond*2000), WithTimeoutN(Stage1, time.Millisecond*100))

	defer close(startTimer(m, t))

	const testctx = "lock context"
	f := m.First(testctx)
	go func() {
		<-f.Notify()
	}()
	tn := time.Now()
	m.Shutdown()
	dur := time.Since(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Errorf("timeout time was unexpected:%v (%v->%v)", dur, tn, time.Now())
	}
	if !m.Started() {
		t.Fatal("got unexpected shutdown signal")
	}
	if gotStage != Stage1 {
		t.Errorf("want stage 1, got %+v", gotStage)
	}
	if !strings.Contains(gotCtx, testctx) {
		t.Errorf("want context to contain %q, got %q", testctx, gotCtx)
	}
}

func TestTimeoutN2(t *testing.T) {
	m := New(WithTimeout(time.Millisecond*100), WithTimeoutN(Stage2, time.Second*2))

	defer close(startTimer(m, t))
	f := m.First()
	go func() {
		<-f.Notify()
	}()
	tn := time.Now()
	m.Shutdown()
	dur := time.Since(tn)
	if dur > time.Second || dur < time.Millisecond*50 {
		t.Fatalf("timeout time was unexpected:%v", time.Since(tn))
	}
	if !m.Started() {
		t.Fatal("got unexpected shutdown signal")
	}
}

func TestOrder(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))

	t3 := m.Third()
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t2 := m.Second()
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t1 := m.First()
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t0 := m.PreShutdown()
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	sdOrder := make(chan int, 4)
	go func() {
		for {
			select {
			//t0 must be first
			case n := <-t0.Notify():
				sdOrder <- 0
				close(n)
			case n := <-t1.Notify():
				sdOrder <- 1
				close(n)
			case n := <-t2.Notify():
				sdOrder <- 2
				close(n)
			case n := <-t3.Notify():
				sdOrder <- 3
				close(n)
			}
			if len(sdOrder) == 4 {
				close(sdOrder)
				return
			}
		}
	}()
	if len(sdOrder) > 0 {
		t.Fatal("shutdown has already happened")
	}
	m.Shutdown()

	if len(sdOrder) != 4 {
		t.Fatalf("expected 4, got:%d", len(sdOrder))
	}

	var res []int
	for i := range sdOrder {
		res = append(res, i)
	}
	last := -1
	for _, v := range res {
		if v <= last {
			t.Fatalf("did not get expected shutdown signals %v", res)
		}
		last = v
	}
}

func TestRecursive(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))

	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	t1 := m.First()
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	var ok1, ok2, ok3 bool
	go func() {
		n1 := <-t1.Notify()
		ok1 = true
		t2 := m.Second()
		close(n1)

		n2 := <-t2.Notify()
		ok2 = true
		t3 := m.Third()
		close(n2)
		n3 := <-t3.Notify()
		ok3 = true
		close(n3)
	}()
	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	m.Shutdown()
	if !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

func TestBasicFn(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	gotcall := false

	// Register a function
	_ = m.FirstFn(func() {
		gotcall = true
	})

	// Start shutdown
	m.Shutdown()
	if !gotcall {
		t.Fatal("did not get expected shutdown signal")
	}
}

func setBool(i *bool) func() {
	return func() {
		*i = true
	}
}

func TestFnOrder(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))

	var ok1, ok2, ok3 bool
	_ = m.ThirdFn(setBool(&ok3))
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = m.SecondFn(setBool(&ok2))
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = m.FirstFn(setBool(&ok1))
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	m.Shutdown()

	if !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

func TestFnRecursive(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))

	var ok1, ok2, ok3 bool

	_ = m.FirstFn(func() {
		ok1 = true
		_ = m.SecondFn(func() {
			ok2 = true
			_ = m.ThirdFn(func() {
				ok3 = true
			})
		})
	})

	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	m.Shutdown()

	if !ok1 || !ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

// When setting First or Second inside stage three they should be ignored.
func TestFnRecursiveRev(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))

	var ok1, ok2, ok3 bool

	_ = m.ThirdFn(func() {
		ok3 = true
		_ = m.SecondFn(func() {
			ok2 = true
		})
		_ = m.FirstFn(func() {
			ok1 = true
		})
	})

	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3)
	}

	m.Shutdown()

	if ok1 || ok2 || !ok3 {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3)
	}
}

func TestFnCancel(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	var g0, g1, g2, g3 bool

	// Register a function
	notp := m.PreShutdownFn(func() {
		g0 = true
	})
	not1 := m.FirstFn(func() {
		g1 = true
	})
	not2 := m.SecondFn(func() {
		g2 = true
	})
	not3 := m.ThirdFn(func() {
		g3 = true
	})

	notp.Cancel()
	not1.Cancel()
	not2.Cancel()
	not3.Cancel()

	// Start shutdown
	m.Shutdown()
	if g1 || g2 || g3 || g0 {
		t.Fatal("got unexpected shutdown signal", g0, g1, g2, g3)
	}
}

func TestFnCancelWait2(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	var g0, g1, g2, g3 bool

	// Register a function
	notp := m.PreShutdownFn(func() {
		g0 = true
	})
	not1 := m.FirstFn(func() {
		g1 = true
	})
	not2 := m.SecondFn(func() {
		g2 = true
	})
	not3 := m.ThirdFn(func() {
		g3 = true
	})

	notp.CancelWait()
	not1.CancelWait()
	not2.CancelWait()
	not3.CancelWait()

	// Start shutdown
	m.Shutdown()
	if g1 || g2 || g3 || g0 {
		t.Fatal("got unexpected shutdown signal", g0, g1, g2, g3)
	}
}

func TestFnPanic(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	gotcall := false

	// Register a function
	_ = m.FirstFn(func() {
		gotcall = true
		panic("This is expected")
	})

	// Start shutdown
	m.Shutdown()
	if !gotcall {
		t.Fatal("did not get expected shutdown signal")
	}
}

func TestFnNotify(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))
	gotcall := false

	// Register a function
	fn := m.FirstFn(func() {
		gotcall = true
	})

	// Start shutdown
	m.Shutdown()

	// This must have a notification
	_, ok := <-fn.Notify()
	if !ok {
		t.Fatal("Notifier was closed before a notification")
	}
	// After this the channel must be closed
	_, ok = <-fn.Notify()
	if ok {
		t.Fatal("Notifier was not closed after initial notification")
	}
	if !gotcall {
		t.Fatal("did not get expected shutdown signal")
	}
}

func TestStatusTimerFn(t *testing.T) {
	version := strings.Split(runtime.Version(), ".")
	if len(version) >= 2 {
		if minor, err := strconv.Atoi(version[1]); err == nil {
			if minor < 9 {
				t.Skip("Skipping test due to caller changes")
				return
			}
		}
	}
	var b bytes.Buffer
	m := New(WithStatusTimer(time.Millisecond), WithLogPrinter(func(f string, val ...interface{}) {
		b.WriteString(fmt.Sprintf(f+"\n", val...))
	}))
	m.FirstFn(func() {
		time.Sleep(time.Millisecond * 100)
	})
	_, file, line, _ := runtime.Caller(0)
	want := fmt.Sprintf("%s:%d", file, line-3)

	m.Shutdown()

	if !strings.Contains(b.String(), want) {
		t.Errorf("Expected logger to contain trace to %s, got: %v", want, b.String())
	}
	lines := strings.Split(b.String(), "\n")
	for _, l := range lines {
		if strings.Contains(l, want) {
			t.Log("Got:", l)
			break
		}
	}
}

func TestStatusTimer(t *testing.T) {
	var b bytes.Buffer
	m := New(WithStatusTimer(time.Millisecond), WithLogPrinter(func(f string, val ...interface{}) {
		b.WriteString(fmt.Sprintf(f+"\n", val...))
	}))
	fn := m.First()
	_, file, line, _ := runtime.Caller(0)
	want := fmt.Sprintf("%s:%d", file, line-1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		wg.Done()
		v := <-fn.Notify()
		time.Sleep(2 * time.Millisecond)
		close(v)
	}()

	wg.Wait()
	m.Shutdown()
	if !strings.Contains(b.String(), want) {
		t.Errorf("Expected logger to contain trace to %s, got: %v", want, b.String())
	}
	lines := strings.Split(b.String(), "\n")
	for _, l := range lines {
		if strings.Contains(l, want) {
			t.Log("Got:", l)
			break
		}
	}
}

func TestFnSingleCancel(t *testing.T) {
	m := newTestTimer()
	defer close(startTimer(m, t))

	var ok1, ok2, ok3, okcancel bool
	_ = m.ThirdFn(func() {
		ok3 = true
	})
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = m.SecondFn(func() {
		ok2 = true
	})
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	cancel := m.SecondFn(func() {
		okcancel = true
	})
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	_ = m.FirstFn(func() {
		ok1 = true
	})
	if m.Started() {
		t.Fatal("shutdown started unexpectedly")
	}

	if ok1 || ok2 || ok3 || okcancel {
		t.Fatal("shutdown has already happened", ok1, ok2, ok3, okcancel)
	}

	cancel.Cancel()

	m.Shutdown()

	if !ok1 || !ok2 || !ok3 || okcancel {
		t.Fatal("did not get expected shutdown signal", ok1, ok2, ok3, okcancel)
	}
}

func TestCancelMulti(t *testing.T) {
	m := New(WithTimeout(time.Second))

	defer close(startTimer(m, t))
	rand.Seed(0xC0CAC01A)
	for i := 0; i < 1000; i++ {
		var n Notifier
		switch rand.Int31n(10) {
		case 0:
			n = m.PreShutdown()
		case 1:
			n = m.First()
		case 2:
			n = m.Second()
		case 3:
			n = m.Third()
		case 4:
			n = m.PreShutdownFn(func() {})
		case 5:
			n = m.FirstFn(func() {})
		case 6:
			n = m.SecondFn(func() {})
		case 7:
			n = m.ThirdFn(func() {})
		}
		go func(n Notifier, t time.Duration) {
			time.Sleep(t)
			n.Cancel()
		}(n, time.Millisecond*time.Duration(rand.Intn(100)))
		time.Sleep(time.Millisecond)
	}
	// Start shutdown
	m.Shutdown()
}

func TestCancelMulti2(t *testing.T) {
	m := New(WithTimeout(time.Second))

	defer close(startTimer(m, t))
	rand.Seed(0xC0CAC01A)
	var wg sync.WaitGroup
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		var n Notifier
		switch rand.Int31n(10) {
		case 0:
			n = m.PreShutdown()
		case 1:
			n = m.First()
		case 2:
			n = m.Second()
		case 3:
			n = m.Third()
		case 4:
			n = m.PreShutdownFn(func() {})
		case 5:
			n = m.FirstFn(func() {})
		case 6:
			n = m.SecondFn(func() {})
		case 7:
			n = m.ThirdFn(func() {})
		}
		go func(n Notifier, r int) {
			if r&1 == 0 {
				n.Cancel()
				wg.Done()
				v, ok := <-n.Notify()
				t.Errorf("Got notifier on %+v", n)
				if ok {
					close(v)
				}
			} else {
				wg.Done()
				v, ok := <-n.Notify()
				if ok {
					close(v)
				}
			}
		}(n, rand.Intn(100))
	}
	wg.Wait()
	// Start shutdown
	m.Shutdown()
}

func TestCancelWaitMulti(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 400))

	defer close(startTimer(m, t))
	rand.Seed(0xC0CAC01A)
	for i := 0; i < 1000; i++ {
		var n Notifier
		switch rand.Int31n(10) {
		case 0:
			n = m.PreShutdown()
		case 1:
			n = m.First()
		case 2:
			n = m.Second()
		case 3:
			n = m.Third()
		case 4:
			n = m.PreShutdownFn(func() {})
		case 5:
			n = m.FirstFn(func() {})
		case 6:
			n = m.SecondFn(func() {})
		case 7:
			n = m.ThirdFn(func() {})
		}
		go func(n Notifier, t time.Duration) {
			time.Sleep(t)
			n.CancelWait()
		}(n, time.Millisecond*time.Duration(rand.Intn(100)))
	}
	// Start shutdown
	m.Shutdown()
}

func TestCancelWaitMulti2(t *testing.T) {
	m := New(WithTimeout(time.Millisecond * 400))

	defer close(startTimer(m, t))
	rand.Seed(0xC0CAC01A)
	var wg sync.WaitGroup
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		var n Notifier
		switch rand.Int31n(10) {
		case 0:
			n = m.PreShutdown()
		case 1:
			n = m.First()
		case 2:
			n = m.Second()
		case 3:
			n = m.Third()
		case 4:
			n = m.PreShutdownFn(func() {})
		case 5:
			n = m.FirstFn(func() {})
		case 6:
			n = m.SecondFn(func() {})
		case 7:
			n = m.ThirdFn(func() {})
		}
		go func(n Notifier, r int) {
			if r%3 == 0 {
				n.CancelWait()
				wg.Done()
				v, ok := <-n.Notify()
				t.Errorf("Got notifier on %+v", n)
				if ok {
					close(v)
				}
			} else if r%2 == 1 {
				wg.Done()
				wg.Wait()
				n.CancelWait()
			} else {
				wg.Done()
				v, ok := <-n.Notify()
				if ok {
					close(v)
				}
			}
		}(n, rand.Intn(50))
	}
	wg.Wait()
	// Start shutdown
	m.Shutdown()
}

/*
// Get a notifier and perform our own code when we shutdown
func ExampleNotifier() {
	shutdown := m.First()
	if shutdown.Valid() {
		n := <-shutdown.Notify()
		// Do shutdown code ...

		// Signal we are done
		close(n)
	}
}

// Get a notifier and perform our own function when we shutdown
func Example_functions() {
	_ = m.FirstFn(func() {
		// This function is called on shutdown
		fmt.Println("First shutdown stage called")
	})

	// Will print the parameter when m.Shutdown() is called
}

// Note that the same effect of this example can also be achieved using the
// WrapHandlerFunc helper.
func ExampleLock() {
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		// Get a lock while we have the lock, the server will not shut down.
		lock := m.Lock()
		if lock != nil {
			defer lock()
		} else {
			// We are currently shutting down, return http.StatusServiceUnavailable
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// ...
	})
	http.ListenAndServe(":8080", nil)
}

// Change timeout for a single stage
func ExampleSetTimeoutN() {
	// Set timout for all stages
	m.SetTimeout(time.Second)

	// But give second stage more time
	m.SetTimeoutN(m.Stage2, time.Second*10)
}

// This is an example, that could be your main function.
//
// We wait for jobs to finish in another goroutine, from
// where we initialize the shutdown.
//
// This is of course not a real-world problem, but there are many
// cases where you would want to initialize shutdown from other places than
// your main function, and where you would still like it to be able to
// do some final cleanup.
func ExampleWait() {
	x := make([]struct{}, 10)
	var wg sync.WaitGroup

	wg.Add(len(x))
	for i := range x {
		go func(i int) {
			time.Sleep(time.Millisecond * time.Duration(i))
			wg.Done()
		}(i)
	}

	// ignore this reset, for test purposes only
	t.Parallel()
m := NewTestTimer()

	// Wait for the jobs above to finish
	go func() {
		wg.Wait()
		fmt.Println("jobs done")
		m.Shutdown()
	}()

	// Since this is main, we wait for a shutdown to occur before
	// exiting.
	m.Wait()
	fmt.Println("exiting main")

	// Note than the output will always be in this order.

	// Output: jobs done
	// exiting main
}
*/
