//go:build windows

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	npmShimRe = regexp.MustCompile(`(?i)"%dp0%\\node\.exe"[^"\n\r]*"%dp0%\\([^"\r\n]+\.(?:cjs|mjs|js))"`)

	jobsMu sync.Mutex
	jobs   = map[*exec.Cmd]windows.Handle{}
)

func isBatch(executable string) bool {
	ext := strings.ToLower(filepath.Ext(executable))
	return ext == ".cmd" || ext == ".bat"
}

// resolveNpmShim checks whether executable is a standard npm-generated .cmd wrapper
// pointing to node.exe + a JS entrypoint next to it, bypassing cmd.exe completely.
func resolveNpmShim(executable string, args []string) (string, []string, bool) {
	if !isBatch(executable) {
		return "", nil, false
	}
	f, err := os.Open(executable)
	if err != nil {
		return "", nil, false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	if n <= 0 {
		return "", nil, false
	}
	m := npmShimRe.FindSubmatch(buf[:n])
	if len(m) < 2 {
		return "", nil, false
	}
	dir := filepath.Dir(executable)
	scriptPath := filepath.Clean(filepath.Join(dir, string(m[1])))
	if _, err := os.Stat(scriptPath); err != nil {
		return "", nil, false
	}
	nodeExe := filepath.Join(dir, "node.exe")
	if _, err := os.Stat(nodeExe); err != nil {
		var lookErr error
		nodeExe, lookErr = exec.LookPath("node.exe")
		if lookErr != nil {
			return "", nil, false
		}
	}
	argv := make([]string, 0, 2+len(args))
	argv = append(argv, nodeExe, scriptPath)
	argv = append(argv, args...)
	return nodeExe, argv, true
}

func resolveLaunch(executable string, args []string) (string, []string) {
	if nodeExe, argv, ok := resolveNpmShim(executable, args); ok {
		return nodeExe, argv
	}
	if isBatch(executable) {
		return "cmd.exe", append([]string{"cmd.exe", "/d", "/s", "/c", executable}, args...)
	}
	return executable, append([]string{executable}, args...)
}

func configure(cmd *exec.Cmd, executable string, args []string) {
	attr := &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if isBatch(executable) && strings.EqualFold(filepath.Base(cmd.Path), "cmd.exe") {
		// Build a safe cmd.exe /d /s /c ""exe" ..." command line where metacharacters
		// (& | < > ^ % ! " ( )) cannot break out of quoting.
		attr.CmdLine = buildCmdExeLine(executable, args)
	}
	cmd.SysProcAttr = attr
}

func buildCmdExeLine(executable string, args []string) string {
	var inner strings.Builder
	inner.WriteString(quoteCmdExeArg(executable))
	for _, a := range args {
		inner.WriteByte(' ')
		inner.WriteString(quoteCmdExeArg(a))
	}
	return `cmd.exe /d /s /c "` + inner.String() + `"`
}

// quoteCmdExeArg quotes an argument for cmd.exe /d /s /c so shell metacharacters,
// newlines, quotes, and %VAR% / !VAR! expansions cannot inject commands.
// Simple tokens without whitespace or metacharacters are emitted verbatim so
// batch %1 parameters do not gain unexpected surrounding quotes.
func quoteCmdExeArg(s string) string {
	s = strings.NewReplacer("\x00", "", "\r", " ", "\n", " ").Replace(s)
	if s != "" && !strings.ContainsAny(s, " \t\"%&|<>^!();,=") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			// Close quotes, pass ^" to cmd.exe, reopen quotes.
			b.WriteString(`"^""`)
		case '%', '!':
			// Close quotes, escape % or ! with ^ outside quotes, reopen quotes.
			b.WriteString(`"^` + string(s[i]) + `"`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

func attachJob(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	procHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	assignErr := windows.AssignProcessToJobObject(job, procHandle)
	_ = windows.CloseHandle(procHandle)
	if assignErr != nil {
		_ = windows.CloseHandle(job)
		return
	}
	jobsMu.Lock()
	jobs[cmd] = job
	jobsMu.Unlock()
}

func releaseJob(cmd *exec.Cmd) {
	jobsMu.Lock()
	job, ok := jobs[cmd]
	if ok {
		delete(jobs, cmd)
	}
	jobsMu.Unlock()
	if ok && job != 0 {
		_ = windows.CloseHandle(job)
	}
}

func killTree(cmd *exec.Cmd) {
	jobsMu.Lock()
	job, ok := jobs[cmd]
	jobsMu.Unlock()
	if ok && job != 0 {
		_ = windows.TerminateJobObject(job, 1)
	}
	k := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = k.Run()
}
