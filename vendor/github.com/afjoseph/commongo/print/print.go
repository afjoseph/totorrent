package print

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fatih/color"
)

const (
	LOG_SILENCE = iota
	LOG_INFO    = iota
	LOG_DEBUG   = iota
)

var logLevel uint = LOG_INFO
var logChan = make(chan ColoredLogMsg, 50)
var doneChan = make(chan bool)

const (
	COLOR_DEBUG_NOCOLOR = iota
	COLOR_DEBUG         = iota
	COLOR_INFO          = iota
	COLOR_WARN          = iota
)

type ColoredLogMsg struct {
	color  int
	prefix string
	msg    string
}

// Infinite loop to ensure synchronised setting of terminal colors in stdout
func init() {
	go func() {
		for {
			msgAndColor, ok := <-logChan
			if !ok {
				doneChan <- true
				return
			}
			switch msgAndColor.color {
			case COLOR_DEBUG_NOCOLOR:
				fmt.Print(msgAndColor.prefix)
				fmt.Print(msgAndColor.msg)
			case COLOR_DEBUG:
				color.Set(color.FgYellow)
				fmt.Print(msgAndColor.prefix)
				fmt.Print(msgAndColor.msg)
			case COLOR_INFO:
				color.Set(color.FgHiBlue)
				fmt.Print(msgAndColor.prefix)
				fmt.Print(msgAndColor.msg)
			case COLOR_WARN:
				color.Set(color.FgRed)
				fmt.Print(msgAndColor.prefix)
				fmt.Print(msgAndColor.msg)
			default:
				panic(fmt.Sprintf("Unsupported color: %d", msgAndColor.color))
			}
			color.Unset()
		}
	}()
}

func SetLevel(level uint) {
	logLevel = level
}

func absToRelFilepath(absFilepath string) (string, bool) {
	if len(absFilepath) == 0 {
		return "", false
	}
	arr := strings.Split(absFilepath, "/")
	if len(arr) < 2 {
		return "", false
	}
	relFilepathArr := arr[len(arr)-2:]
	relFilepath := strings.Join(relFilepathArr, "/")
	return relFilepath, true
}

func SetLogFile(fileNamePrefix string) {
	var fileName string
	if fileNamePrefix == "" {
		fileName = fmt.Sprintf("%d.log", time.Now().Unix())
	} else {
		fileName = fmt.Sprintf("%s_%d.log", fileNamePrefix, time.Now().Unix())
	}
	logFile, err := os.OpenFile(fileName, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		// Ignore errors
		return
	}
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)
	log.SetFlags(5)
}

// absToSimpleFilePath takes an absolute path and returns only the last two nodes.
// This is used exclusively for printing debug statements
// Example:
//     Input -> /tmp/aaa/bbb/ccc
//     Output -> bbb/ccc
func absToSimpleFilePath(absFilepath string) (string, bool) {
	if len(absFilepath) == 0 {
		return "", false
	}
	arr := strings.Split(absFilepath, "/")
	if len(arr) < 3 {
		return "", false
	}
	relFilepathArr := arr[len(arr)-2:]
	relFilepath := strings.Join(relFilepathArr, "/")
	return relFilepath, true
}

func InfoFunc() {
	if logLevel < LOG_INFO {
		return
	}
	pc, absFilepath, lineNum, ok := runtime.Caller(1)
	if !ok {
		return
	}
	relFilepath, ok := absToSimpleFilePath(absFilepath)
	if !ok {
		return
	}
	details := runtime.FuncForPC(pc)
	logChan <- ColoredLogMsg{
		COLOR_INFO,
		fmt.Sprintf("[+%s:%d] %s()", relFilepath, lineNum, details.Name()),
		"\n"}
}

func Infoln(text string) {
	if logLevel < LOG_INFO {
		return
	}
	logChan <- ColoredLogMsg{COLOR_INFO, "[+] ", text + "\n"}
}

func Infof(format string, v ...interface{}) {
	if logLevel < LOG_INFO {
		return
	}
	logChan <- ColoredLogMsg{COLOR_INFO, "[+] ", fmt.Sprintf(format, v...)}
}

func Warnln(text string) {
	logChan <- ColoredLogMsg{COLOR_WARN, "[!] ", text + "\n"}
}

func Warnf(format string, v ...interface{}) {
	logChan <- ColoredLogMsg{COLOR_WARN, "[!] ",
		fmt.Sprintf(format, v...)}
}

func Debugln(text string) {
	if logLevel < LOG_DEBUG {
		return
	}
	_, absFilepath, lineNum, _ := runtime.Caller(1)
	relFilepath, _ := absToSimpleFilePath(absFilepath)
	logChan <- ColoredLogMsg{
		COLOR_DEBUG_NOCOLOR,
		fmt.Sprintf("[D:%s:%d] ", relFilepath, lineNum),
		text + "\n"}
}

func Debugf(format string, v ...interface{}) {
	if logLevel < LOG_DEBUG {
		return
	}
	pc, absFilepath, lineNum, _ := runtime.Caller(1)
	relFilepath, _ := absToSimpleFilePath(absFilepath)
	details := runtime.FuncForPC(pc)
	arr := strings.Split(details.Name(), ".")
	funcName := arr[len(arr)-1]
	logChan <- ColoredLogMsg{
		COLOR_DEBUG_NOCOLOR,
		fmt.Sprintf("[D:%s:%d#%s] ", relFilepath, lineNum, funcName),
		fmt.Sprintf(format, v...)}
}

func DebugFunc() {
	if logLevel < LOG_DEBUG {
		return
	}
	pc, absFilepath, lineNum, _ := runtime.Caller(1)
	relFilepath, _ := absToSimpleFilePath(absFilepath)
	details := runtime.FuncForPC(pc)
	logChan <- ColoredLogMsg{
		COLOR_DEBUG,
		fmt.Sprintf("[+%s:%d] %s()", relFilepath, lineNum, details.Name()),
		"\n"}
}

// ErrorWrapf creates an error out of a formatted string using 'format' with
// arguments from 'v' and wraps it with 'wrappedError', along with the file and
// line number this function was called from.
// XXX One caveat here: you can only propagate one error
func ErrorWrapf(wrappedError error, format string, v ...interface{}) error {
	content := fmt.Sprintf(format, v...)
	_, absFilepath, lineNum, _ := runtime.Caller(1)
	relFilepath, ok := absToRelFilepath(absFilepath)
	var prefix string
	if ok {
		prefix = fmt.Sprintf("[!%s:%d]", relFilepath, lineNum)
	}
	return fmt.Errorf("%w:%s: %s", wrappedError, prefix, content)
}

// ErrorWrapf creates an error out of a formatted string using 'format' with
// arguments from 'v', along with the file and line number this function was
// called from.
// XXX One caveat here: you can't use error propagation using %w. If you want
// that, use ErrorWrapf
func Errorf(format string, v ...interface{}) error {
	content := fmt.Sprintf(format, v...)
	_, absFilepath, lineNum, _ := runtime.Caller(1)
	relFilepath, ok := absToRelFilepath(absFilepath)
	var prefix string
	if ok {
		prefix = fmt.Sprintf("[!%s:%d]", relFilepath, lineNum)
	}
	return fmt.Errorf("%s: %s", prefix, content)
}

func Flush() chan bool {
	close(logChan)
	return doneChan
}
