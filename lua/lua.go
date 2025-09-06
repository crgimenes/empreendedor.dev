package lua

import (
	"errors"
	"fmt"
	"log"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type Lua struct {
	ls *lua.LState
}

var (
	ErrorFunctionNotFound = errors.New("function not found")
	ErrorNotAllowedType   = errors.New("not allowed return type")
)

func (l *Lua) fromGoToLua(v any) lua.LValue {
	switch v := v.(type) { // adjusted for binding
	case int:
		return lua.LNumber(v)
	case uint:
		return lua.LNumber(v)
	case int8:
		return lua.LNumber(v)
	case uint8:
		return lua.LNumber(v)
	case int16:
		return lua.LNumber(v)
	case uint16:
		return lua.LNumber(v)
	case int32:
		return lua.LNumber(v)
	case uint32:
		return lua.LNumber(v)
	case int64:
		return lua.LNumber(v)
	case uint64:
		return lua.LNumber(v)
	case float64:
		return lua.LNumber(v)
	case string:
		return lua.LString(v)
	case []string:
		table := l.ls.CreateTable(len(v), 0)
		for i, s := range v {
			l.ls.RawSet(table, lua.LNumber(i+1), lua.LString(s))
		}
		return table
	case bool:
		return lua.LBool(v)
	case []byte:
		return lua.LString(v)
	case time.Duration:
		return lua.LNumber(v.Nanoseconds())
	case time.Time:
		return lua.LNumber(v.Unix())
	case map[string]any:
		return l.mapToLuaTable(v)
	case map[string]string:
		table := l.ls.CreateTable(len(v), 0)
		for k, s := range v {
			l.ls.RawSet(table, lua.LString(k), lua.LString(s))
		}
		return table
	default:
		log.Println("not allowed type")
		return lua.LNil
	}
}

func fromLuaToGo(v lua.LValue) (any, error) {
	switch v.Type() {
	case lua.LTNil:
		return nil, nil
	case lua.LTNumber:
		return float64(v.(lua.LNumber)), nil
	case lua.LTString:
		return string(v.(lua.LString)), nil
	case lua.LTBool:
		return bool(v.(lua.LBool)), nil
	case lua.LTTable:
		m := make(map[string]any)
		t := v.(*lua.LTable)
		t.ForEach(func(k, v lua.LValue) {
			m[string(k.(lua.LString))] = v
		})
		return m, nil
	default:
		return nil, ErrorNotAllowedType
	}
}

func (l *Lua) mapToLuaTable(m map[string]any) *lua.LTable {
	table := l.ls.NewTable()
	for k, v := range m {
		switch v := v.(type) { // adjusted for binding
		case int:
			table.RawSetString(k, lua.LNumber(v))
		case float64:
			table.RawSetString(k, lua.LNumber(v))
		case string:
			table.RawSetString(k, lua.LString(v))
		case bool:
			table.RawSetString(k, lua.LBool(v))
		case []byte:
			table.RawSetString(k, lua.LString(v))
		case time.Duration:
			table.RawSetString(k, lua.LNumber(v.Nanoseconds()))
		case time.Time:
			table.RawSetString(k, lua.LNumber(v.Unix()))
		case map[string]any:
			table.RawSetString(k, l.mapToLuaTable(v))
		default:
			table.RawSetString(k, lua.LNil)
		}
	}
	return table
}

func New() *Lua {
	ls := lua.NewState()
	ls.PreloadModule("math", lua.OpenMath)
	return &Lua{ls: ls}
}

func (l *Lua) Close() {
	l.ls.Close()
}

func (l *Lua) SetFunction(name string, f func(*lua.LState) int) {
	l.ls.SetGlobal(name, l.ls.NewFunction(f))
}

func (l *Lua) DoString(luaScript string) error {
	return l.ls.DoString(luaScript)
}

func (l *Lua) SetGlobal(name string, value any) {
	var luaValue lua.LValue
	switch v := value.(type) {
	case string:
		luaValue = lua.LString(v)
	case int:
		luaValue = lua.LNumber(v)
	case int64:
		luaValue = lua.LNumber(v)
	case float32:
		luaValue = lua.LNumber(v)
	case float64:
		luaValue = lua.LNumber(v)
	case bool:
		luaValue = lua.LBool(v)
	case []string:
		luaTable := l.ls.CreateTable(len(v), 0)
		for i, s := range v {
			l.ls.RawSet(luaTable, lua.LNumber(i+1), lua.LString(s))
		}
		l.ls.SetGlobal(name, luaTable)
		return
	case map[string]string:
		luaValue = l.fromGoToLua(v)
	default:
		luaValue = lua.LString(fmt.Sprintf("%v", v))
	}
	l.ls.SetGlobal(name, luaValue)
}

func (l *Lua) MustGetInt(vGlobal string) int {
	v, ok := l.ls.GetGlobal(vGlobal).(lua.LNumber)
	if !ok {
		log.Fatalf("Error converting %q to int", vGlobal)
	}
	return int(v)
}

func (l *Lua) MustGetString(vGlobal string) string {
	v, ok := l.ls.GetGlobal(vGlobal).(lua.LString)
	if !ok {
		log.Fatalf("Error converting %q to string", vGlobal)
	}
	return string(v)
}

func (l *Lua) MustGetTable(vGlobal string) []string {
	v, ok := l.ls.GetGlobal(vGlobal).(*lua.LTable)
	if !ok {
		log.Fatalf("Error converting %q to []string", vGlobal)
	}
	var ret []string
	v.ForEach(func(k lua.LValue, v lua.LValue) {
		ret = append(ret, v.String())
	})
	return ret
}

func (l *Lua) MustGetMap(vGlobal string) map[string]string {
	m, err := fromLuaToGo(l.ls.GetGlobal(vGlobal))
	if err != nil {
		log.Fatalf("Error loading %q into map[string]string", vGlobal)
	}
	mi, ok := m.(map[string]any)
	if !ok {
		log.Fatalf("Error converting %q into map[string]string", vGlobal)
	}
	ret := make(map[string]string)
	for k, v := range mi {
		ret[k] = v.(lua.LString).String()
	}
	return ret
}

func (l *Lua) MustGetBool(vGlobal string) bool {
	v, ok := l.ls.GetGlobal(vGlobal).(lua.LBool)
	if !ok {
		log.Fatalf("Erro ao converter %q para bool", vGlobal)
	}
	return bool(v)
}

func (l *Lua) NewTable() *lua.LTable {
	return l.ls.NewTable()
}

// GetGlobalTable returns the global variable as a *lua.LTable.
func (l *Lua) GetGlobalTable(name string) *lua.LTable {
	t := l.ls.GetGlobal(name)
	if tbl, ok := t.(*lua.LTable); ok {
		return tbl
	}
	return nil
}

// SetGlobalTable sets a global variable with a given *lua.LTable.
func (l *Lua) SetGlobalTable(name string, tbl *lua.LTable) {
	l.ls.SetGlobal(name, tbl)
}

func (l *Lua) GetState() *lua.LState {
	return l.ls
}
