package backupguard

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestLineWriter checks how lineWriter cuts the output into the lines it
// hands to each: across writes, at empty lines and at the 64 KiB line limit
// (REQ-BG-115, REQ-BG-118).
func TestLineWriter(t *testing.T) {
	const limit = 64 << 10 // written out: the test pins the limit itself
	long := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name   string
		writes []string
		flush  bool // final flush, as RunEach does after the program exits
		want   []string
	}{
		{"line split across two writes", []string{"[new] re", "fs/heads/a\n"}, false, []string{"[new] refs/heads/a"}},
		{"last line without newline", []string{"a\nb"}, true, []string{"a", "b"}},
		{"empty line at start of a write", []string{"a", "\nb\n\nc\n"}, false, []string{"a", "b", "c"}},
		{"write of a single newline", []string{"a", "\n", "b\n"}, false, []string{"a", "b"}},
		{"remainder of exactly one byte", []string{"a\n\n", "b"}, true, []string{"a", "b"}},
		{"remainder of one byte ends a line", []string{"a", "x\n", "b"}, true, []string{"ax", "b"}},
		{"line of exactly 64 KiB is kept", []string{long(limit) + "\nnext\n"}, false, []string{long(limit), "next"}},
		{"64 KiB line split across writes is kept", []string{long(limit - 1), "x\n"}, false, []string{long(limit)}},
		{"64 KiB plus one is dropped", []string{long(limit+1) + "\nnext\n"}, false, []string{"next"}},
		{"too long across writes is dropped, next line arrives", []string{long(limit), "y", long(10), "\nnext\n"}, false, []string{"next"}},
		{"too long line with an event is dropped", []string{"[new] " + long(limit) + "\n[new] b\n"}, false, []string{"[new] b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			w := &lineWriter{each: func(s string) { got = append(got, s) }}
			for _, s := range tc.writes {
				if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if tc.flush {
				w.flush()
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("lines %s; want %s", short(got), short(tc.want))
			}
		})
	}
}

func short(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) > 20 {
			l = fmt.Sprintf("%.10s…(%d bytes)", l, len(l))
		}
		out[i] = l
	}
	return out
}
