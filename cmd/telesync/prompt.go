package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gotd/td/tg"
	"golang.org/x/term"
)

// terminalPrompter reads login input from the terminal.
//
// The 2FA password is read without echo; everything else is echoed normally.
type terminalPrompter struct {
	in  io.Reader
	out io.Writer
	buf *bufio.Reader
}

// newTerminalPrompter builds a prompter over stdin/stdout.
func newTerminalPrompter() *terminalPrompter {
	return &terminalPrompter{in: os.Stdin, out: os.Stdout}
}

func (t *terminalPrompter) reader() *bufio.Reader {
	if t.buf == nil {
		t.buf = bufio.NewReader(t.in)
	}
	return t.buf
}

// Phone asks for the account's phone number.
func (t *terminalPrompter) Phone(_ context.Context, key string) (string, error) {
	fmt.Fprintf(t.out, "\nAuthorizing the %q client.\n", key)
	return t.read("Phone number (international format, e.g. +989121234567): ")
}

// Code asks for the login code that Telegram just sent.
func (t *terminalPrompter) Code(_ context.Context, sent *tg.AuthSentCode) (string, error) {
	fmt.Fprint(t.out, "\nA login code was sent.\n")
	return t.read("Login code" + sentCodeHint(sent) + ": ")
}

// Password asks for the two-step verification password without echoing it.
func (t *terminalPrompter) Password(context.Context) (string, error) {
	fmt.Fprint(t.out, "\nThis account has two-step verification enabled.\n")
	const prompt = "Password (input hidden): "

	// If stdin has already been buffered (piped input), read from the buffer so
	// bytes are not lost. Otherwise use a no-echo terminal read.
	if t.buf != nil && t.buf.Buffered() > 0 {
		fmt.Fprint(t.out, prompt+"(echoed: input is piped)\n")
		return t.read("")
	}
	fd := stdinFD(t.in)
	if fd < 0 || !term.IsTerminal(fd) {
		fmt.Fprint(t.out, prompt+"(echoed: input is not a terminal)\n")
		return t.read("")
	}

	fmt.Fprint(t.out, prompt)
	data, err := term.ReadPassword(fd)
	fmt.Fprintln(t.out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(data), nil
}

// read prints prompt and returns one trimmed line.
func (t *terminalPrompter) read(prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	line, err := t.reader().ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return "", fmt.Errorf("no input: end of input reached")
		}
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// stdinFD returns the file descriptor behind r, or -1.
func stdinFD(r io.Reader) int {
	if f, ok := r.(*os.File); ok {
		return int(f.Fd())
	}
	return -1
}

// sentCodeHint describes how the code was delivered, without echoing the code.
func sentCodeHint(sent *tg.AuthSentCode) string {
	switch v := sent.Type.(type) {
	case *tg.AuthSentCodeTypeSMS:
		return fmt.Sprintf(" (sent by SMS, %d digits)", v.Length)
	case *tg.AuthSentCodeTypeApp:
		return fmt.Sprintf(" (sent in the Telegram app, %d digits)", v.Length)
	case *tg.AuthSentCodeTypeCall:
		return fmt.Sprintf(" (spoken by an automated call, %d digits)", v.Length)
	case *tg.AuthSentCodeTypeFlashCall:
		return " (flash call)"
	case *tg.AuthSentCodeTypeEmailCode:
		return fmt.Sprintf(" (sent by email to %s)", v.EmailPattern)
	case *tg.AuthSentCodeTypeFragmentSMS:
		return " (sent by SMS via Fragment)"
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return " (sent by SMS)"
	case *tg.AuthSentCodeTypeSMSWord:
		return " (SMS contains a word beginning with " + v.Beginning + ")"
	case *tg.AuthSentCodeTypeSMSPhrase:
		if v.Beginning == "" {
			return " (SMS contains a phrase)"
		}
		return " (SMS contains a phrase beginning with " + v.Beginning + ")"
	default:
		return ""
	}
}

// promptReader reads sensitive input from the terminal without echo.
type promptReader struct {
	in  *os.File
	out io.Writer
}

func newPromptReader() *promptReader {
	return &promptReader{in: os.Stdin, out: os.Stdout}
}

// readPassword prints prompt and reads a line without echoing it.
func (p *promptReader) readPassword(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	fd := int(p.in.Fd())
	if !term.IsTerminal(fd) {
		// Piped input cannot hide; the caller is responsible for not logging it.
		line, err := bufio.NewReader(p.in).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	data, err := term.ReadPassword(fd)
	fmt.Fprintln(p.out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(data), nil
}
