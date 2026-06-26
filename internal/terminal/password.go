package terminal

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

type PasswordReader interface {
	ReadPassword(prompt string, stdin io.Reader, stdout io.Writer) (string, error)
}

type Reader struct{}

func ReadLine(prompt string, stdin io.Reader, stdout io.Writer) (string, error) {
	if _, err := fmt.Fprint(stdout, prompt); err != nil {
		return "", err
	}
	line, err := readString(stdin)
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func readPasswordFallback(stdin io.Reader) (string, error) {
	line, err := readString(stdin)
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func readString(stdin io.Reader) (string, error) {
	if reader, ok := stdin.(interface {
		ReadString(delim byte) (string, error)
	}); ok {
		return reader.ReadString('\n')
	}
	return bufio.NewReader(stdin).ReadString('\n')
}
