package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mirza/telesync/internal/auth"
)

// runHashPassword prints an Argon2id hash of the password read from stdin.
//
// It is the documented way to create the first administrator's hash for the
// bootstrap command: the password goes in, the hash comes out, and the plaintext
// is never an argument that would land in a shell history.
func runHashPassword(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	password, err := readPassword(stdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, hash)
	return nil
}

// readPassword reads one line and refuses an empty one.
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the password: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("the password is empty")
	}
	return password, nil
}
