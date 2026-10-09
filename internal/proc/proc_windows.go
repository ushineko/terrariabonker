package proc

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

/*
The Windows half: the game is a native process, reached through a handle.

A same-user process can open another with read, write and query access without
elevation, so nothing here needs administrator rights. The handle is opened on
first use and kept for the Mem's life, rather than opened per read the way
/proc/<pid>/mem is, because OpenProcess is a system call per byte range
otherwise.
*/

// sysMem is the process handle, opened on first use.
type sysMem struct {
	once sync.Once
	h    windows.Handle
	err  error
}

// access is what Mem needs of the game: read, write, and enough to query and
// change its pages.
const access = windows.PROCESS_VM_READ | windows.PROCESS_VM_WRITE |
	windows.PROCESS_VM_OPERATION | windows.PROCESS_QUERY_INFORMATION

// memPrivate and memFree are MEM_PRIVATE and MEM_FREE, which x/sys/windows
// does not export.
const (
	memPrivate = 0x20000
	memFree    = 0x10000
)

// flushInstructionCache is kernel32's FlushInstructionCache, which x/sys/windows
// does not wrap.
var flushInstructionCache = windows.NewLazySystemDLL("kernel32.dll").NewProc("FlushInstructionCache")

func (m *Mem) handle() (windows.Handle, error) {
	s := m.sys
	if s == nil {
		s = &sysMem{}
		m.sys = s
	}
	s.once.Do(func() {
		pid := uint32(m.PID) //nolint:gosec // a pid, which Windows keeps in 32 bits
		s.h, s.err = windows.OpenProcess(access, false, pid)
		if s.err != nil {
			s.err = fmt.Errorf("open pid %d: %w", m.PID, s.err)
		}
	})
	return s.h, s.err
}

// Regions is the process's writable, scannable memory: committed pages a write
// is allowed to, which is where the managed heap is.
func (m *Mem) Regions() []Region { return m.query(func(r Region) bool { return r.Writable }) }

/*
ExecRegions is the process's executable memory, which is where JIT'd code is.

The CLR's code heap is private memory; the executable sections of mapped images
(the game's own PE, clr.dll, every system DLL) are not where a JIT'd method can
be, so they are left out. That keeps an anchor search off some hundred megabytes
of code it cannot match.
*/
func (m *Mem) ExecRegions() []Region {
	return m.queryTyped(func(r Region, private bool) bool { return r.Executable && private })
}

/*
AllRegions is every part of the address space that is taken: committed or only
reserved.

It answers "what is already taken", for finding a gap to put an arena in, so
reserved-but-uncommitted space counts as taken. It is exactly as unavailable to
an allocation as committed memory.
*/
func (m *Mem) AllRegions() []Region {
	return m.queryAll(func(windows.MemoryBasicInformation) bool { return true })
}

func (m *Mem) query(keep func(Region) bool) []Region {
	return m.queryTyped(func(r Region, _ bool) bool { return keep(r) })
}

// queryTyped walks committed memory and keeps the regions keep accepts, told
// whether each is private rather than part of a mapped image or file.
func (m *Mem) queryTyped(keep func(Region, bool) bool) []Region {
	var out []Region
	for _, mbi := range m.walk() {
		if mbi.State != windows.MEM_COMMIT || mbi.Protect&windows.PAGE_GUARD != 0 {
			continue
		}
		r := regionOf(mbi)
		if keep(r, mbi.Type == memPrivate) {
			out = append(out, r)
		}
	}
	return out
}

func (m *Mem) queryAll(keep func(windows.MemoryBasicInformation) bool) []Region {
	var out []Region
	for _, mbi := range m.walk() {
		if mbi.State == memFree || !keep(mbi) {
			continue
		}
		out = append(out, regionOf(mbi))
	}
	return out
}

// walk is every region VirtualQueryEx reports below 4 GB.
func (m *Mem) walk() []windows.MemoryBasicInformation {
	h, err := m.handle()
	if err != nil {
		return nil
	}
	var out []windows.MemoryBasicInformation
	var addr uintptr
	for addr < 1<<32 {
		var mbi windows.MemoryBasicInformation
		if windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)) != nil {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		if next > 1<<32 {
			break // a 32-bit process's space ends at 4 GB; see parseRegion
		}
		out = append(out, mbi)
		if next <= addr {
			break
		}
		addr = next
	}
	return out
}

// regionOf is one VirtualQueryEx answer as a Region.
func regionOf(mbi windows.MemoryBasicInformation) Region {
	p := mbi.Protect &^ (windows.PAGE_GUARD | windows.PAGE_NOCACHE | windows.PAGE_WRITECOMBINE)
	writable := p == windows.PAGE_READWRITE || p == windows.PAGE_WRITECOPY ||
		p == windows.PAGE_EXECUTE_READWRITE || p == windows.PAGE_EXECUTE_WRITECOPY
	executable := p == windows.PAGE_EXECUTE || p == windows.PAGE_EXECUTE_READ ||
		p == windows.PAGE_EXECUTE_READWRITE || p == windows.PAGE_EXECUTE_WRITECOPY
	readable := mbi.State == windows.MEM_COMMIT && p != windows.PAGE_NOACCESS && p != windows.PAGE_EXECUTE
	return Region{
		Start:    uint32(mbi.BaseAddress),                  //nolint:gosec // below 4 GB; walk stops there
		End:      uint32(mbi.BaseAddress + mbi.RegionSize), //nolint:gosec // below 4 GB; walk stops there
		Writable: writable, Executable: executable, Readable: readable,
	}
}

/*
Read is size bytes at addr, or nothing.

A read that runs off the end of a region comes back short, as it does through
/proc/<pid>/mem. ReadProcessMemory itself does not do that -- it fails the whole
read when any page in the range is unreadable -- so a failed read is retried once,
clamped to the end of the region addr is in.
*/
func (m *Mem) Read(addr uint32, size int) []byte {
	h, err := m.handle()
	if err != nil || size <= 0 {
		return nil
	}
	buf := make([]byte, size)
	var got uintptr
	if err := windows.ReadProcessMemory(h, uintptr(addr), &buf[0], uintptr(size), &got); err == nil {
		return buf[:got]
	}
	var mbi windows.MemoryBasicInformation
	if windows.VirtualQueryEx(h, uintptr(addr), &mbi, unsafe.Sizeof(mbi)) != nil || !regionOf(mbi).Readable {
		return nil
	}
	room := mbi.BaseAddress + mbi.RegionSize - uintptr(addr)
	if room >= uintptr(size) {
		return nil
	}
	if err := windows.ReadProcessMemory(h, uintptr(addr), &buf[0], room, &got); err != nil {
		return nil
	}
	return buf[:got]
}

/*
Write puts data at addr and reports whether all of it landed.

The instruction cache is flushed after every write. A write into data makes
that a no-op; a write into code (a patch site, a stub) needs it, and telling
the two apart would cost a query per write.
*/
func (m *Mem) Write(addr uint32, data []byte) bool {
	h, err := m.handle()
	if err != nil || len(data) == 0 {
		return false
	}
	var put uintptr
	err = windows.WriteProcessMemory(h, uintptr(addr), &data[0], uintptr(len(data)), &put)
	if err != nil || put != uintptr(len(data)) {
		return false
	}
	_, _, _ = flushInstructionCache.Call(uintptr(h), uintptr(addr), uintptr(len(data)))
	return true
}

// ExePath is the full path of the process's executable, or "" when it cannot be
// asked.
func (m *Mem) ExePath() string {
	h, err := m.handle()
	if err != nil {
		return ""
	}
	var n uint32 = windows.MAX_LONG_PATH
	buf := make([]uint16, n)
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// ErrNoGame is returned when the game is not running.
var ErrNoGame = errors.New("no running " + GameExe + " found. Is Terraria launched?")

// FindPID is the pid of the running game, found by its executable's name.
func FindPID() (int, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("list processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var found []int
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), GameExe) {
			found = append(found, int(pe.ProcessID))
		}
	}
	switch len(found) {
	case 0:
		return 0, ErrNoGame
	case 1:
		return found[0], nil
	}
	return 0, fmt.Errorf("multiple %s processes found: %v", GameExe, found)
}

/*
ModulePaths is the path of every module the process has loaded.

What a runtime detector reads on Windows: the CLR is clr.dll, and the module
list says which one and from where, as /proc/<pid>/maps does under Proton.
*/
func ModulePaths(pid int) []string {
	p := uint32(pid) //nolint:gosec // a pid, which Windows keeps in 32 bits
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, p)
	if err != nil {
		return nil
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var out []string
	var me windows.ModuleEntry32
	me.Size = uint32(unsafe.Sizeof(me))
	for err = windows.Module32First(snap, &me); err == nil; err = windows.Module32Next(snap, &me) {
		out = append(out, windows.UTF16ToString(me.ExePath[:]))
	}
	return out
}

// stillActive is STILL_ACTIVE, the exit code GetExitCodeProcess reports for a
// process that has not exited.
const stillActive = 259

/*
Alive reports whether a pid is still a running process.

Opening it is not enough: Windows keeps a process object, and its pid, for as
long as anyone holds a handle to it, exited or not. The exit code is what says
whether it is still running.
*/
func Alive(pid int) bool {
	p := uint32(pid) //nolint:gosec // a pid, which Windows keeps in 32 bits
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
