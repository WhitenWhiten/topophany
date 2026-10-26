//go:build linux

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Raw PTY fixtures exercise the actual Linux terminal backend without CGO.
func ptyFixture(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, e := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { master.Close() })
	var unlock int32
	if e = ioctl(master.Fd(), syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); e != nil {
		t.Fatal(e)
	}
	var n uint32
	if e = ioctl(master.Fd(), syscall.TIOCGPTN, unsafe.Pointer(&n)); e != nil {
		t.Fatal(e)
	}
	slave, e := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { slave.Close() })
	var size = struct{ Rows, Cols, X, Y uint16 }{24, 100, 0, 0}
	if e = ioctl(slave.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&size)); e != nil {
		t.Fatal(e)
	}
	return master, slave
}

type capture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capture) text() string { c.mu.Lock(); defer c.mu.Unlock(); return c.buf.String() }
func consumePTY(master *os.File) (*capture, func()) {
	c := &capture{}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b := make([]byte, 65536)
		for ctx.Err() == nil {
			n, e := syscall.Read(int(master.Fd()), b)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(b[:n])
				c.mu.Unlock()
			}
			if e != nil && e != syscall.EAGAIN && e != syscall.EINTR {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	return c, func() { cancel(); wg.Wait() }
}
func TestActualLinuxTerminalReadSizeAndRestore(t *testing.T) {
	master, slave := ptyFixture(t)
	var old syscall.Termios
	if e := ioctl(slave.Fd(), syscall.TCGETS, unsafe.Pointer(&old)); e != nil {
		t.Fatal(e)
	}
	term, e := openTerminal(slave, slave)
	if e != nil {
		t.Fatal(e)
	}
	if w, h := term.Size(); w != 100 || h != 24 {
		t.Fatal(w, h)
	}
	if _, e = syscall.Write(int(master.Fd()), []byte("j")); e != nil {
		t.Fatal(e)
	}
	testkit.Wait(t, time.Second, func() bool {
		b, e := term.Read()
		if e != nil {
			t.Fatal(e)
		}
		return string(b) == "j"
	})
	term.Close()
	var after syscall.Termios
	ioctl(slave.Fd(), syscall.TCGETS, unsafe.Pointer(&after))
	if after != old {
		t.Fatal("terminal flags not restored")
	}
	file, e := os.CreateTemp(t.TempDir(), "redirected")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if _, e = openTerminal(slave, file); e == nil {
		t.Fatal("nonterminal output accepted")
	}
}
func TestTerminalContinuesWithoutSessionCache(t *testing.T) {
	blockSessionCache(t)
	t.Setenv("TERM", "xterm-256color")
	master, slave := ptyFixture(t)
	_, stop := consumePTY(master)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fetched := make(chan struct{}, 1)
	fetch := func(context.Context, string) (model.Snapshot, string, error) {
		select {
		case fetched <- struct{}{}:
		default:
		}
		return model.Snapshot{GeneratedAt: time.Now()}, "local", nil
	}
	done := make(chan error, 1)
	go func() { done <- RunWithFetch(ctx, fetch, "", true, slave, slave) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-fetched:
	case <-time.After(2 * time.Second):
		t.Fatal("diagnostic cache prevented terminal fetch")
	}
}

func TestRunWithFetchSourceSwitchFailureRecoveryAndQuit(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	master, slave := ptyFixture(t)
	cap, stop := consumePTY(master)
	defer stop()
	var mu sync.Mutex
	failNext := false
	var views []string
	m := fixtureModel()
	fetch := func(ctx context.Context, view string) (model.Snapshot, string, error) {
		mu.Lock()
		views = append(views, view)
		fail := failNext
		failNext = false
		mu.Unlock()
		if fail {
			return model.Snapshot{}, "", errors.New("simulated API outage")
		}
		s := m.Snapshot
		s.ViewObserver = view
		s.GeneratedAt = time.Now()
		return s, "https://fixture.invalid", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWithFetch(ctx, fetch, "", true, slave, slave) }()
	testkit.Wait(t, 2*time.Second, func() bool { return strings.Contains(cap.text(), "fixture.invalid") })
	syscall.Write(int(master.Fd()), []byte("v"))
	testkit.Wait(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, v := range views {
			if v == "home" {
				return true
			}
		}
		return false
	})
	mu.Lock()
	failNext = true
	mu.Unlock()
	syscall.Write(int(master.Fd()), []byte("r"))
	testkit.Wait(t, 2*time.Second, func() bool { return strings.Contains(cap.text(), "simulated API outage") })
	syscall.Write(int(master.Fd()), []byte("r"))
	syscall.Write(int(master.Fd()), []byte("5\rj\r"))
	syscall.Write(int(master.Fd()), []byte("q"))
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TUI did not quit")
	}
	testkit.Wait(t, time.Second, func() bool { return strings.Contains(cap.text(), "\x1b[?1049l") })
	// The window was named after the verdict, and its own title restored.
	if out := cap.text(); !strings.Contains(out, "\x1b[22;0t") || !strings.Contains(out, " · Topophany\x07") || !strings.Contains(out, "\x1b[23;0t") {
		t.Fatal("window title not set and restored")
	}
}
func TestReadSecretActualNoEchoPasteBackspaceCancel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  string
		want   string
		cancel bool
	}{{"typed", "abc\x7fd\r", "abd", false}, {"paste", "\x1b[200~private-token\x1b[201~\r", "private-token", false}, {"cancel", "\x03", "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := ptyFixture(t)
			cap, stop := consumePTY(master)
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			type result struct {
				s string
				e error
			}
			done := make(chan result, 1)
			go func() { s, e := ReadSecret(ctx, slave, slave); done <- result{s, e} }()
			testkit.Wait(t, time.Second, func() bool {
				var current syscall.Termios
				ioctl(slave.Fd(), syscall.TCGETS, unsafe.Pointer(&current))
				return current.Lflag&syscall.ECHO == 0
			})
			syscall.Write(int(master.Fd()), []byte(tc.input))
			r := <-done
			if tc.cancel {
				if !errors.Is(r.e, context.Canceled) {
					t.Fatal(r.e)
				}
			} else if r.e != nil || r.s != tc.want {
				t.Fatal(r)
			}
			if tc.want != "" && strings.Contains(cap.text(), tc.want) {
				t.Fatal("secret echoed to terminal")
			}
		})
	}

}
