package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// BUG-1224 round r2: a module that implements the rule-versioned interface
// but is not registered with the version state would silently judge replayed
// history by the current rules. Two structural checks hold that closed.

// TestBug1224R2_EveryVersionedModuleInTheStateIsRegistered walks every field
// of the wired simulation state; any field whose type implements the
// rule-versioned interface must be among the modules registered with the
// version state.
func TestBug1224R2_EveryVersionedModuleInTheStateIsRegistered(t *testing.T) {
	rc := newRuleCity(t, nil, -1)
	st := rc.c.state
	registered := map[any]bool{}
	for _, v := range st.rv.modules {
		registered[v.mod] = true
	}
	iface := reflect.TypeOf((*ruleVersioned)(nil)).Elem()
	sv := reflect.ValueOf(st).Elem()
	found := 0
	for i := 0; i < sv.NumField(); i++ {
		f := sv.Field(i)
		ft := f.Type()
		if !ft.Implements(iface) && !reflect.PointerTo(ft).Implements(iface) {
			continue
		}
		if (ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Interface) && f.IsNil() {
			continue
		}
		val := reflect.NewAt(ft, unsafe.Pointer(f.UnsafeAddr())).Elem().Interface()
		found++
		if !registered[val] {
			t.Errorf("simState field %s (%s) is rule-versioned but is not registered with the version state; wire it with st.rv.wire", sv.Type().Field(i).Name, ft)
		}
	}
	if found == 0 {
		t.Fatal("found no rule-versioned module in the state: the walk proves nothing")
	}
}

// TestBug1224R2_VersionedModuleSetIsPinned scans the production source for
// every type that declares SetRuleVersionSource. The set is pinned: adding a
// versioned module fails here until it is named below AND wired (the test
// above then proves the wiring).
func TestBug1224R2_VersionedModuleSetIsPinned(t *testing.T) {
	root := filepath.Join("..", "..") // internal/
	var got []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "func (") && strings.Contains(line, ") SetRuleVersionSource(") {
				got = append(got, filepath.ToSlash(p))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"../../engine/build/build.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("production rule-versioned modules = %v, want %v: wire any new one with st.rv.wire in compose.go and name it here", got, want)
	}
}
