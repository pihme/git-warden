package gitx

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// OutputLimit bounds the output the guards keep from a program they start
// (REQ-PG-050, 051; REQ-BG-115 to 117). The guards' own git calls are not
// bounded this way.
const OutputLimit = 1 << 20

// ErrOutputTooLarge is returned for evaluated output above OutputLimit.
var ErrOutputTooLarge = errors.New("output exceeds 1 MiB")

// LimitBuffer keeps output that will be evaluated, up to Max bytes. Beyond
// that it keeps nothing more, remembers the overflow and calls OnOverflow
// once (to stop the program); the output must then not be evaluated.
type LimitBuffer struct {
	Max        int
	OnOverflow func()

	mu       sync.Mutex
	buf      []byte
	overflow bool
}

func (b *LimitBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return len(p), nil
	}
	if len(b.buf)+len(p) > b.Max {
		b.overflow = true
		if b.OnOverflow != nil {
			b.OnOverflow()
		}
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// Bytes returns the output kept.
func (b *LimitBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf
}

// String returns the output kept.
func (b *LimitBuffer) String() string { return string(b.Bytes()) }

// Err is ErrOutputTooLarge, naming what, after an overflow; nil otherwise.
func (b *LimitBuffer) Err(what string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return fmt.Errorf("%s: %w", what, ErrOutputTooLarge)
	}
	return nil
}

// TailBuffer keeps the last Max bytes of output that is only shown or
// logged; everything before is dropped and the program runs on normally.
type TailBuffer struct {
	Max int

	mu  sync.Mutex
	buf []byte
}

func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= b.Max {
		b.buf = append(b.buf[:0], p[len(p)-b.Max:]...)
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	if len(b.buf) > 2*b.Max { // compact now and then, not on every write
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-b.Max:]...)
	}
	return len(p), nil
}

// String returns the last Max bytes written.
func (b *TailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) > b.Max {
		return string(b.buf[len(b.buf)-b.Max:])
	}
	return string(b.buf)
}

// RunBounded runs cmd with stdout and stderr each kept in a LimitBuffer of
// OutputLimit. If either overflows, the program is killed and the error
// wraps ErrOutputTooLarge, so cut output is never evaluated (REQ-PG-050,
// REQ-BG-116). Otherwise err is cmd.Run's error.
func RunBounded(cmd *exec.Cmd, what string) (stdout, stderr []byte, err error) {
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 5 * time.Second
	}
	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	out := &LimitBuffer{Max: OutputLimit, OnOverflow: kill}
	errOut := &LimitBuffer{Max: OutputLimit, OnOverflow: kill}
	cmd.Stdout, cmd.Stderr = out, errOut
	runErr := cmd.Run()
	if err := out.Err(what + " stdout"); err != nil {
		return nil, nil, err
	}
	if err := errOut.Err(what + " stderr"); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), errOut.Bytes(), runErr
}
