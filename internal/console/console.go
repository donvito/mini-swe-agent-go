// Package console contains minimal terminal helpers: colors, rules, a spinner and an interruptible line reader.
package console

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

var colorEnabled = os.Getenv("NO_COLOR") == "" && IsTerminal(os.Stdout)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f any) bool {
	file, ok := f.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func style(code, s string) string {
	if !colorEnabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Bold(s string) string       { return style("1", s) }
func Dim(s string) string        { return style("2", s) }
func Red(s string) string        { return style("31", s) }
func BoldRed(s string) string    { return style("1;31", s) }
func Green(s string) string      { return style("32", s) }
func BoldGreen(s string) string  { return style("1;32", s) }
func BoldYellow(s string) string { return style("1;33", s) }
func BoldBlue(s string) string   { return style("1;34", s) }

// Rule prints a horizontal line across the terminal.
func Rule(out io.Writer) {
	width := 80
	if f, ok := out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			width = w
		}
	}
	fmt.Fprintln(out, Dim(strings.Repeat("─", width)))
}

// Spinner shows an animated status message until the returned stop function is called.
// It only animates if out is a terminal.
func Spinner(out io.Writer, message string) (stop func()) {
	if !IsTerminal(out) {
		return func() {}
	}
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		for i := 0; ; i++ {
			fmt.Fprintf(out, "\r%s %s", Green(string(frames[i%len(frames)])), message)
			select {
			case <-done:
				fmt.Fprint(out, "\r\033[K")
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); <-finished }) }
}

type lineResult struct {
	line string
	err  error
}

// LineReader reads lines from an input in the background so that reads can be cancelled with a context.
type LineReader struct {
	in    io.Reader
	once  sync.Once
	lines chan lineResult
}

func NewLineReader(in io.Reader) *LineReader {
	return &LineReader{in: in, lines: make(chan lineResult)}
}

// Stdin is the shared reader for standard input (there must only be one reader of os.Stdin).
var Stdin = NewLineReader(os.Stdin)

func (r *LineReader) start() {
	r.once.Do(func() {
		go func() {
			reader := bufio.NewReader(r.in)
			for {
				line, err := reader.ReadString('\n')
				if line != "" {
					r.lines <- lineResult{line: strings.TrimRight(line, "\r\n")}
				}
				if err != nil {
					r.lines <- lineResult{err: err}
				}
			}
		}()
	})
}

// Line reads a single line (without the trailing newline).
func (r *LineReader) Line(ctx context.Context) (string, error) {
	r.start()
	select {
	case result := <-r.lines:
		return result.line, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Multiline reads lines until EOF (Ctrl-D in a terminal).
func (r *LineReader) Multiline(ctx context.Context) (string, error) {
	var lines []string
	for {
		line, err := r.Line(ctx)
		if err == io.EOF {
			if len(lines) == 0 {
				return "", io.EOF
			}
			return strings.Join(lines, "\n"), nil
		}
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
}
