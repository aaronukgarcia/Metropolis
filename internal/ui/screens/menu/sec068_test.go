package menu

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

func TestSEC068_SaveKeymapFile_Race(t *testing.T) {
	s := New("corr-sec068")
	km := &keys.Keymap{
		Version: 1,
		Bindings: map[string]string{
			"Ctrl-S": "save",
		},
	}
	s.selectedKeymap = km

	path := filepath.Join(t.TempDir(), "keymap.json")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			s.mu.Lock()
			s.selectedKeymap.Bindings["Ctrl-S"] = strconv.Itoa(i)
			s.mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = s.SaveKeymapFile(path)
		}
	}()

	wg.Wait()
}
