package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

// promptSet is where one command's prompts read and write: the CLI's stdin and
// stdout, or a test's buffers.
type promptSet struct {
	in  io.Reader
	out io.Writer
	// tty reports whether in is an interactive terminal, in which case a secret
	// is read without echo.
	tty func() bool

	once sync.Once
	buf  *bufio.Reader
}

func (a *App) prompts() *promptSet {
	in := a.deps.Stdin
	if in == nil {
		in = os.Stdin
	}
	tty := a.tty
	if tty == nil {
		tty = func() bool {
			f, ok := in.(*os.File)
			return ok && term.IsTerminal(int(f.Fd()))
		}
	}
	return &promptSet{in: in, out: a.deps.Stdout, tty: tty}
}

// ask prints a prompt and reads one line.
//
// The answer is trimmed. A secret is read without echo when the input is a
// terminal; piped input cannot be hidden, and the caller is told so.
func (p *promptSet) ask(text string, secret bool) (string, error) {
	fmt.Fprint(p.out, text)
	if secret && p.tty() {
		f, ok := p.in.(*os.File)
		if ok {
			data, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(p.out)
			if err != nil {
				return "", fmt.Errorf("read the answer: %w", err)
			}
			return strings.TrimSpace(string(data)), nil
		}
	}
	line, err := p.reader().ReadString('\n')
	if err != nil && line == "" && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func (p *promptSet) reader() *bufio.Reader {
	p.once.Do(func() { p.buf = bufio.NewReader(p.in) })
	return p.buf
}
