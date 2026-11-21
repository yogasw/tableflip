package tableflip

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

var initialWD, _ = os.Getwd()

type process interface {
	fmt.Stringer
	Signal(sig os.Signal) error
	Wait() error
}

type osProcess struct {
	*os.Process
	finished bool
}

// resolveExecutable avoids os/exec.LookPath. Go 1.27 implements the latter
// through faccessat2, which Android's seccomp filter kills with SIGSYS on
// older Termux kernels.
func resolveExecutable(executable string) (string, error) {
	if strings.ContainsRune(executable, os.PathSeparator) {
		return executable, nil
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, executable)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable file not found in $PATH: %s", executable)
}

func newOSProcess(executable string, args []string, files []*os.File, env []string) (process, error) {
	executable, err := resolveExecutable(executable)
	if err != nil {
		return nil, err
	}
	fds := make([]uintptr, 0, len(files))
	for _, file := range files {
		fd, err := sysConnFd(file)
		if err != nil {
			return nil, err
		}
		fds = append(fds, fd)
	}

	attr := &syscall.ProcAttr{
		Dir:   initialWD,
		Env:   env,
		Files: fds,
	}

	args = append([]string{executable}, args...)
	pid, _, err := syscall.StartProcess(executable, args, attr)
	if err != nil {
		return nil, fmt.Errorf("fork/exec: %s", err)
	}

	// Ensure that fds stay valid until after StartProcess finishes.
	runtime.KeepAlive(files)

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("find pid %d: %s", pid, err)
	}

	return &osProcess{Process: proc}, nil
}

func (osp *osProcess) Wait() error {
	if osp.finished {
		return fmt.Errorf("already waited")
	}
	osp.finished = true

	state, err := osp.Process.Wait()
	if err != nil {
		return err
	}

	if !state.Success() {
		return &exec.ExitError{ProcessState: state}
	}

	return nil
}

func (osp *osProcess) String() string {
	return fmt.Sprintf("pid=%d", osp.Pid)
}
