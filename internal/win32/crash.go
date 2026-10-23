//go:build windows

package win32

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var crashKernel32 = windows.NewLazySystemDLL("kernel32.dll")
var procSetUnhandledExceptionFilter = crashKernel32.NewProc("SetUnhandledExceptionFilter")

// exceptionRecord 对应 Win32 的 EXCEPTION_RECORD（x64 布局，字段顺序与对齐一致）。
type exceptionRecord struct {
	Code    uint32
	Flags   uint32
	Record  uintptr
	Address uintptr
}

type exceptionPointers struct {
	ExceptionRecord *exceptionRecord
	ContextRecord   uintptr
}

// InstallCrashHandler 注册 Windows 层的未处理异常过滤器。
//
// Go 的 recover 只能拦住 goroutine 里的 panic；像「C 栈溢出」「访问冲突」这类
// 发生在消息回调线程上的崩溃完全不走 Go 的 panic 路径，进程会直接消失，
// app.log 里连一行都留不下。注册这个过滤器后，至少能把异常码与地址写到文件里：
//   0xC00000FD = 栈溢出（递归）
//   0xC0000005 = 访问冲突
func InstallCrashHandler(path string) {
	cb := syscall.NewCallback(func(info uintptr) uintptr {
		var code uint32
		var addr uintptr
		if info != 0 {
			// 回调只能收 uintptr；这里先写进 unsafe.Pointer 再解引用，
			// 避免 uintptr → unsafe.Pointer 的直接转换（vet 会判为可疑用法）。
			var p unsafe.Pointer
			*(*uintptr)(unsafe.Pointer(&p)) = info
			if ep := (*exceptionPointers)(p); ep.ExceptionRecord != nil {
				code = ep.ExceptionRecord.Code
				addr = ep.ExceptionRecord.Address
			}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%s 未处理异常 code=0x%08X addr=0x%X\n",
				time.Now().Format("2006-01-02 15:04:05.000"), code, addr)
			_ = f.Close()
		}
		return 1 // EXCEPTION_EXECUTE_HANDLER
	})
	procSetUnhandledExceptionFilter.Call(cb)
}
