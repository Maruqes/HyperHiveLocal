//go:build linux

package terminal

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

func (Reader) ReadPassword(prompt string, stdin io.Reader, stdout io.Writer) (string, error) {
	if _, err := fmt.Fprint(stdout, prompt); err != nil {
		return "", err
	}

	file, ok := stdin.(*os.File)
	if !ok {
		return readPasswordFallback(stdin)
	}

	fd := int(file.Fd())
	var oldState syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&oldState)), 0, 0, 0); errno != 0 {
		return readPasswordFallback(stdin)
	}

	newState := oldState
	newState.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&newState)), 0, 0, 0); errno != 0 {
		return "", errno
	}
	defer func() {
		_, _, _ = syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&oldState)), 0, 0, 0)
		_, _ = fmt.Fprintln(stdout)
	}()

	return readPasswordFallback(stdin)
}
