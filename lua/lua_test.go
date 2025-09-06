package lua

import (
	"log"
	"testing"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Llongfile)
}

// TestDoString executes a simple Lua script that assigns a global variable
// and then verifies that the variable was correctly set.
func TestDoString(t *testing.T) {
	l := New()
	defer l.Close()

	// Execute Lua script setting global variable 'x' to 42.
	if err := l.DoString("x = 42"); err != nil {
		t.Fatalf("DoString error: %v", err)
	}

	// Retrieve 'x' and verify its value.
	x := l.MustGetInt("x")
	if x != 42 {
		t.Fatalf("Expected x = 42, got %d", x)
	}
}

// TestSetGlobalInt verifies that an integer is correctly set as a global variable.
func TestSetGlobalInt(t *testing.T) {
	l := New()
	defer l.Close()

	l.SetGlobal("num", 100)
	num := l.MustGetInt("num")
	if num != 100 {
		t.Fatalf("Expected num = 100, got %d", num)
	}
}

// TestSetGlobalString verifies that a string is correctly set as a global variable.
func TestSetGlobalString(t *testing.T) {
	l := New()
	defer l.Close()

	l.SetGlobal("greeting", "hello")
	greeting := l.MustGetString("greeting")
	if greeting != "hello" {
		t.Fatalf("Expected greeting = 'hello', got %s", greeting)
	}
}

// TestSetGlobalTable verifies that a []string is correctly set as a Lua table global.
func TestSetGlobalTable(t *testing.T) {
	l := New()
	defer l.Close()

	expected := []string{"one", "two", "three"}
	l.SetGlobal("list", expected)

	list := l.MustGetTable("list")
	if len(list) != len(expected) {
		t.Fatalf("Expected list length %d, got %d", len(expected), len(list))
	}
	for i, v := range expected {
		if list[i] != v {
			t.Fatalf("Expected list[%d] = %s, got %s", i, v, list[i])
		}
	}

	m := make(map[string]string)
	m["one"] = "uno"
	m["two"] = "dos"
	m["three"] = "tres"
	l.SetGlobal("map", m)

	mapTable := l.MustGetMap("map")
	if len(mapTable) != len(m) {
		t.Fatalf("Expected map length %d, got %d", len(m), len(mapTable))
	}
}
