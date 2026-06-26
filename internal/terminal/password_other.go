//go:build !linux

package terminal

import (
	"fmt"
	"io"
)

func (Reader) ReadPassword(prompt string, stdin io.Reader, stdout io.Writer) (string, error) {
	if _, err := fmt.Fprint(stdout, prompt); err != nil {
		return "", err
	}
	return readPasswordFallback(stdin)
}
