package util

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/afjoseph/commongo/print"
)

func SafeDelete(parentPath, targetPath string) error {
	if !IsDirectory(parentPath) {
		return print.Errorf("No parentPath")
	}
	if !IsDirectory(targetPath) {
		// targetPath is already not there. Just ignore
		return nil
	}

	// Evaluate symlinks
	parentPath, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return err
	}
	targetPath, err = filepath.EvalSymlinks(targetPath)
	if err != nil {
		return err
	}
	// Are they already the same?
	if parentPath == targetPath {
		return print.Errorf("trying to delete targetPath that has an identical parentPath: %s", targetPath)
	}

	// Get the relative path between them and make sure it does not have any
	// '..' traversals
	newPath, err := filepath.Rel(parentPath, targetPath)
	if err != nil {
		return err
	}
	if strings.Contains(newPath, "..") {
		return print.Errorf("Can't delete %s since it is not inside expected parent dir %s",
			targetPath, parentPath)
	}
	err = os.RemoveAll(targetPath)
	if err != nil {
		return err
	}
	return nil
}

func IsDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fileInfo.IsDir()
}

func IsFile(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fileInfo.IsDir()
}

func AskForSudo() error {
	print.Debugf("Please give me sudo...\n")
	_, _, _, err := Exec("", "sudo -v")
	if err != nil {
		return err
	}
	return nil
}

func GetProjectBranch(gitDirectory string) (string, error) {
	stdout, _, _, err := Exec("",
		"git --git-dir %s rev-parse --abbrev-ref HEAD", gitDirectory)
	if err != nil {
		return "", err
	}
	return stdout, nil
}

func GetProjectShortHash(gitDirectory string) (string, error) {
	stdout, _, _, err := Exec("",
		"git --git-dir %s rev-parse --short HEAD", gitDirectory)
	if err != nil {
		return "", err
	}
	return stdout, nil
}

func ShallowCloneRepo(projectPath, gitUrl, gitBranch string) error {
	print.Debugf("Cloning [%s] from branch [%s] to [%s]\n", gitUrl, gitBranch, projectPath)
	_, _, _, err := Exec("", "git clone -q --depth=1 --branch %s %s %s", gitBranch, gitUrl, projectPath)
	if err != nil {
		return err
	}
	return nil
}

const defaultFailedCode = 1

// Exec runs a command "sprintf(cmdFmtStr, args)" and return stdout, stderr and
// exitcode of the command. If exitCode != 0, err will return with a
// nicely-formatted map of the command's outputs
//
// XXX This command is a simple wrapper around exec.Command(). The following
// are cases this command does **NOT** accommodate:
// * Using environments in a command
// * Having a field in the command that includes a space as part of the parameter
//    i.e., ./bin -param iPhone 12 # where the input of -param is "iPhone 12", not just "iPhone"
func Exec(currentWorkingDir string,
	cmdFmtStr string,
	args ...interface{}) (stdout string, stderr string, exitCode int, err error) {
	var outbuf, errbuf bytes.Buffer
	cmdStr := fmt.Sprintf(cmdFmtStr, args...)
	arr := strings.Split(cmdStr, " ")
	cmd := exec.Command(arr[0], arr[1:]...)
	if len(currentWorkingDir) != 0 {
		cmd.Dir = currentWorkingDir
	}
	cmd.Stdout = &outbuf
	cmd.Stderr = &errbuf
	print.Debugf("Executing command: %s\n", cmd.String())

	err = cmd.Run()
	stdout = outbuf.String()
	stderr = errbuf.String()

	if err != nil {
		// try to get the exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			ws := exitError.Sys().(syscall.WaitStatus)
			exitCode = ws.ExitStatus()
		} else {
			// This will happen (in OSX) if `name` is not available in $PATH,
			// in this situation, exit code could not be get, and stderr will be
			// empty string very likely, so we use the default fail code, and format err
			// to string and set to stderr
			print.Errorf("Could not get exit code for failed program: %v\n",
				cmd.String())
			exitCode = defaultFailedCode
			if stderr == "" {
				stderr = err.Error()
			}
		}
	} else {
		// success, exitCode should be 0 if go is ok
		ws := cmd.ProcessState.Sys().(syscall.WaitStatus)
		exitCode = ws.ExitStatus()
	}
	if exitCode != 0 {
		err = fmt.Errorf("Command failed: %+v",
			map[string]string{
				"stdout":   stdout,
				"stderr":   stderr,
				"exitCode": strconv.Itoa(exitCode),
			},
		)
	}
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	return
}

// Expand expands the path to include the home directory if the path
// is prefixed with `~`. If it isn't prefixed with `~`, the path is
// returned as-is.
func ExpandPath(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] != '~' {
		return path
	}
	if len(path) > 1 && path[1] != '/' && path[1] != '\\' {
		return path
	}
	return filepath.Join(os.Getenv("HOME"), path[1:])
}
