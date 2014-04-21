package lua

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

func functionName(l *State, d *Debug) string {
	switch {
	case d.NameKind != "":
		return fmt.Sprintf("function '%s'", d.Name)
	case d.What == "main":
		return "main chunk"
	case d.What == "Go":
		if pushGlobalFunctionName(l, d) {
			s, _ := ToString(l, -1)
			Pop(l, 1)
			return fmt.Sprintf("function '%s'", s)
		} else {
			return "?"
		}
	}
	return fmt.Sprintf("function <%s:%d>", d.ShortSource, d.LineDefined)
}

func countLevels(l *State) int {
	var d Debug
	li, le := 1, 1
	for Stack(l, le, &d) {
		li = le
		le *= 2
	}
	for li < le {
		m := (li + le) / 2
		if Stack(l, m, &d) {
			li = m + 1
		} else {
			le = m
		}
	}
	return le - 1
}

func Traceback(l, l1 *State, message string, level int) {
	const levels1, levels2 = 12, 10
	levels := countLevels(l1)
	mark := 0
	if levels > levels1+levels2 {
		mark = levels1
	}
	buf := message
	if buf != "" {
		buf += "\n"
	}
	buf += "stack traceback:"
	var d Debug
	for Stack(l1, level, &d) {
		if level++; level == mark {
			buf += "\n\t..."
			level = levels - levels2
		} else {
			Info(l1, "Slnt", &d)
			buf += "\n\t" + d.ShortSource + ":"
			if d.CurrentLine > 0 {
				buf += fmt.Sprintf("%d:", d.CurrentLine)
			}
			buf += " in " + functionName(l, &d)
			if d.IsTailCall {
				buf += "\n\t(...tail calls...)"
			}
		}
	}
	PushString(l, buf)
}

func MetaField(l *State, index int, event string) bool {
	if !MetaTable(l, index) {
		return false
	}
	PushString(l, event)
	RawGet(l, -2)
	if IsNil(l, -1) {
		Pop(l, 2) // remove metatable and metafield
		return false
	}
	Remove(l, -2) // remove only metatable
	return true
}

func CallMeta(l *State, index int, event string) bool {
	index = AbsIndex(l, index)
	if !MetaField(l, index, event) {
		return false
	}
	PushValue(l, index)
	Call(l, 1, 1)
	return true
}

func ArgumentError(l *State, argCount int, extraMessage string) {
	var activationRecord Debug
	if !Stack(l, 0, &activationRecord) { // no stack frame?
		Errorf(l, "bad argument #%d (%s)", argCount, extraMessage)
		return
	}
	Info(l, "n", &activationRecord)
	if activationRecord.NameKind == "method" {
		argCount--         // do not count 'self'
		if argCount == 0 { // error is in the self argument itself?
			Errorf(l, "calling '%s' on bad self (%s)", activationRecord.Name, extraMessage)
			return
		}
	}
	if activationRecord.Name == "" {
		if pushGlobalFunctionName(l, &activationRecord) {
			activationRecord.Name, _ = ToString(l, -1)
		} else {
			activationRecord.Name = "?"
		}
	}
	Errorf(l, "bad argument #%d to '%s' (%s)", argCount, activationRecord.Name, extraMessage)
}

func findField(l *State, objectIndex, level int) bool {
	if level == 0 || !IsTable(l, -1) {
		return false
	}
	for PushNil(l); Next(l, -2); Pop(l, 1) { // for each pair in table
		if IsString(l, -2) { // ignore non-string keys
			if RawEqual(l, objectIndex, -1) { // found object?
				Pop(l, 1) // remove value (but keep name)
				return true
			} else if findField(l, objectIndex, level-1) { // try recursively
				Remove(l, -2) // remove table (but keep name)
				PushString(l, ".")
				Insert(l, -2) // place "." between the two names
				Concat(l, 3)
				return true
			}
		}
	}
	return false
}

func pushGlobalFunctionName(l *State, activationRecord *Debug) bool {
	top := Top(l)
	Info(l, "f", activationRecord) // push function
	PushGlobalTable(l)
	if findField(l, top+1, 2) {
		Copy(l, -1, top+1) // move name to proper place
		Pop(l, 2)          // remove pushed values
		return true
	}
	SetTop(l, top) // remove function and global table
	return false
}

func typeError(l *State, argCount int, typeName string) {
	ArgumentError(l, argCount, PushString(l, typeName+" expected, got "+TypeNameOf(l, argCount)))
}

func tagError(l *State, argCount int, tag Type) { typeError(l, argCount, TypeName(l, tag)) }

func Where(l *State, level int) {
	var activationRecord Debug
	if Stack(l, level, &activationRecord) { // check function at level
		Info(l, "Sl", &activationRecord)      // get info about it
		if activationRecord.CurrentLine > 0 { // is there info?
			PushString(l, fmt.Sprintf("%s:%d: ", activationRecord.ShortSource, activationRecord.CurrentLine))
			return
		}
	}
	PushString(l, "") // else, no information available...
}

func Errorf(l *State, format string, a ...interface{}) {
	Where(l, 1)
	PushFString(l, format, a...)
	Concat(l, 2)
	Error(l)
}

func ToStringMeta(l *State, index int) (string, bool) {
	if !CallMeta(l, index, "__tostring") {
		switch TypeOf(l, index) {
		case TypeNumber, TypeString:
			PushValue(l, index)
		case TypeBoolean:
			if ToBoolean(l, index) {
				PushString(l, "true")
			} else {
				PushString(l, "false")
			}
		case TypeNil:
			PushString(l, "nil")
		default:
			PushFString(l, "%s: %p", TypeNameOf(l, index), ToValue(l, index))
		}
	}
	return ToString(l, -1)
}

func NewMetaTable(l *State, name string) bool {
	if MetaTableNamed(l, name); !IsNil(l, -1) {
		return false
	}
	Pop(l, 1)
	NewTable(l)
	PushValue(l, -1)
	SetField(l, RegistryIndex, name)
	return true
}

func MetaTableNamed(l *State, name string) {
	Field(l, RegistryIndex, name)
}

func SetMetaTableNamed(l *State, name string) {
	MetaTableNamed(l, name)
	SetMetaTable(l, -2)
}

func TestUserData(l *State, index int, name string) interface{} {
	if d := ToUserData(l, index); d != nil {
		if MetaTable(l, index) {
			if MetaTableNamed(l, name); !RawEqual(l, -1, -2) {
				d = nil
			}
			Pop(l, 2)
			return d
		}
	}
	return nil
}

func CheckUserData(l *State, index int, name string) interface{} {
	if d := TestUserData(l, index, name); d != nil {
		return d
	}
	typeError(l, index, name)
	panic("unreachable")
}

func CheckType(l *State, index int, t Type) {
	if TypeOf(l, index) != t {
		tagError(l, index, t)
	}
}

func CheckAny(l *State, index int) {
	if TypeOf(l, index) == TypeNone {
		ArgumentError(l, index, "value expected")
	}
}

func ArgumentCheck(l *State, cond bool, index int, extraMessage string) {
	if !cond {
		ArgumentError(l, index, extraMessage)
	}
}

func CheckString(l *State, index int) string {
	if s, ok := ToString(l, index); ok {
		return s
	}
	tagError(l, index, TypeString)
	panic("unreachable")
}

func OptString(l *State, index int, def string) string {
	if IsNoneOrNil(l, index) {
		return def
	}
	return CheckString(l, index)
}

func CheckNumber(l *State, index int) float64 {
	n, ok := ToNumber(l, index)
	if !ok {
		tagError(l, index, TypeNumber)
	}
	return n
}

func OptNumber(l *State, index int, def float64) float64 {
	if IsNoneOrNil(l, index) {
		return def
	}
	return CheckNumber(l, index)
}

func CheckInteger(l *State, index int) int {
	i, ok := ToInteger(l, index)
	if !ok {
		tagError(l, index, TypeNumber)
	}
	return i
}

func OptInteger(l *State, index, def int) int {
	if IsNoneOrNil(l, index) {
		return def
	}
	return CheckInteger(l, index)
}

func CheckUnsigned(l *State, index int) uint {
	i, ok := ToUnsigned(l, index)
	if !ok {
		tagError(l, index, TypeNumber)
	}
	return i
}

func OptUnsigned(l *State, index int, def uint) uint {
	if IsNoneOrNil(l, index) {
		return def
	}
	return CheckUnsigned(l, index)
}

func TypeNameOf(l *State, index int) string {
	return TypeName(l, TypeOf(l, index))
}

func SetFunctions(l *State, functions []RegistryFunction, upValueCount uint8) {
	uvCount := int(upValueCount)
	CheckStackWithMessage(l, uvCount, "too many upvalues")
	for _, r := range functions { // fill the table with given functions
		for i := 0; i < uvCount; i++ { // copy upvalues to the top
			PushValue(l, -uvCount)
		}
		PushGoClosure(l, r.Function, upValueCount) // closure with those upvalues
		SetField(l, -(uvCount + 2), r.Name)
	}
	Pop(l, uvCount) // remove upvalues
}

func CheckStackWithMessage(l *State, space int, message string) {
	// keep some extra space to run error routines, if needed
	if !CheckStack(l, space+MinStack) {
		if message != "" {
			Errorf(l, "stack overflow (%s)", message)
		} else {
			Errorf(l, "stack overflow")
		}
	}
}

func CheckOption(l *State, index int, def string, list []string) int {
	var name string
	if def == "" {
		name = OptString(l, index, def)
	} else {
		name = CheckString(l, index)
	}
	for i, s := range list {
		if name == s {
			return i
		}
	}
	ArgumentError(l, index, PushFString(l, "invalid option '%s'", name))
	panic("unreachable")
}

func SubTable(l *State, index int, name string) bool {
	Field(l, index, name)
	if IsTable(l, -1) {
		return true // table already there
	}
	Pop(l, 1) // remove previous result
	index = AbsIndex(l, index)
	NewTable(l)
	PushValue(l, -1)         // copy to be left at top
	SetField(l, index, name) // assign new table to field
	return false             // did not find table there
}

func Require(l *State, name string, f Function, global bool) {
	PushGoFunction(l, f)
	PushString(l, name) // argument to f
	Call(l, 1, 1)       // open module
	SubTable(l, RegistryIndex, "_LOADED")
	PushValue(l, -2)      // make copy of module (call result)
	SetField(l, -2, name) // _LOADED[name] = module
	Pop(l, 1)             // remove _LOADED table
	if global {
		PushValue(l, -1)   // copy of module
		SetGlobal(l, name) // _G[name] = module
	}
}

func NewLibraryTable(l *State, functions []RegistryFunction) { CreateTable(l, 0, len(functions)) }

func NewLibrary(l *State, functions []RegistryFunction) {
	NewLibraryTable(l, functions)
	SetFunctions(l, functions, 0)
}

func skipComment(r *bufio.Reader) (bool, error) {
	bom := "\xEF\xBB\xBF"
	if ba, err := r.Peek(len(bom)); err != nil {
		return false, err
	} else if string(ba) == bom {
		_, _ = r.Read(ba)
	}
	if c, _, err := r.ReadRune(); err != nil {
		return false, err
	} else if c == '#' {
		_, err = r.ReadBytes('\n')
		return true, err
	}
	return false, r.UnreadRune()
}

func LoadFile(l *State, fileName, mode string) error {
	var f *os.File
	fileNameIndex := Top(l) + 1
	fileError := func(what string) error {
		fileName, _ := ToString(l, fileNameIndex)
		PushFString(l, "cannot %s %s", what, fileName[1:])
		Remove(l, fileNameIndex)
		return FileError
	}
	if fileName == "" {
		PushString(l, "=stdin")
		f = os.Stdin
	} else {
		PushString(l, "@"+fileName)
		var err error
		if f, err = os.Open(fileName); err != nil {
			return fileError("open")
		}
	}
	r := bufio.NewReader(f)
	if skipped, err := skipComment(r); err != nil {
		SetTop(l, fileNameIndex)
		return fileError("read")
	} else if skipped {
		r = bufio.NewReader(io.MultiReader(strings.NewReader("\n"), r))
	}
	s, _ := ToString(l, -1)
	err := Load(l, r, s, mode)
	if f != os.Stdin {
		_ = f.Close()
	}
	if err != nil {
		SetTop(l, fileNameIndex)
		return fileError("read")
	}
	Remove(l, fileNameIndex)
	return err
}

func LoadString(l *State, s string) error { return LoadBuffer(l, s, s, "") }

func LoadBuffer(l *State, b, name, mode string) error {
	return Load(l, strings.NewReader(b), name, mode)
}

func NewStateEx() *State {
	l := NewState()
	if l != nil {
		_ = AtPanic(l, func(l *State) int {
			s, _ := ToString(l, -1)
			fmt.Fprintf(os.Stderr, "PANIC: unprotected error in call to Lua API (%s)\n", s)
			return 0
		})
	}
	return l
}

func LengthEx(l *State, index int) int {
	Length(l, index)
	if length, ok := ToInteger(l, -1); ok {
		Pop(l, 1)
		return length
	}
	Errorf(l, "object length is not a number")
	panic("unreachable")
}

func FileResult(l *State, err error, filename string) int {
	if err == nil {
		PushBoolean(l, true)
		return 1
	}
	PushNil(l)
	if filename != "" {
		PushString(l, filename+": "+err.Error())
	} else {
		PushString(l, err.Error())
	}
	PushInteger(l, 0) // TODO map err to errno
	return 3
}
