package schedule

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestWindowsVerifyUsesRegisteredTask(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"command", "<Command>", "<Command>changed-"},
		{"trigger", "2000-01-01T01:05:00", "2000-01-01T02:05:00"},
		{"disabled", "<Enabled>true</Enabled>", "<Enabled>false</Enabled>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			executor := &fakeExecutor{}
			manager := NewManager(WithExecutor(executor), WithPlatform("windows", 0), WithClock(time.Now))
			state, err := manager.InstallSpec(context.Background(), Spec{Name: "example", Action: ActionBackup, Backend: BackendWindows, Executable: "resticctl.exe", ConfigDir: directory, Expressions: []string{"5 1 * * *"}, Enabled: true, Start: true})
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(state.JobFile)
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Verify(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			name := `\resticctl\` + nativeID(state)
			executor.windowsTasks[name] = []byte(strings.Replace(string(executor.windowsTasks[name]), test.old, test.replacement, 1))
			if err := manager.Verify(context.Background(), state); !errors.Is(err, ErrDrift) {
				t.Fatalf("Verify = %v, want drift", err)
			}
			unchanged, err := os.ReadFile(state.JobFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, unchanged) {
				t.Fatal("fixture changed saved XML instead of registered task")
			}
		})
	}
}

func TestCanonicalTaskXMLFormattingAndEncoding(t *testing.T) {
	original := `<Task version="1.4"><Settings><Enabled>true</Enabled></Settings><Actions><Exec><Arguments>--path &amp; data</Arguments></Exec></Actions></Task>`
	expected, err := canonicalTaskXML([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	formatted := "<?xml version=\"1.0\" encoding=\"UTF-16\"?>\n" + strings.ReplaceAll(original, "><", ">\n  <")
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		units := append([]uint16{0xfeff}, utf16.Encode([]rune(formatted))...)
		data := make([]byte, len(units)*2)
		for i, unit := range units {
			order.PutUint16(data[2*i:], unit)
		}
		actual, err := canonicalTaskXML(data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, expected) {
			t.Fatalf("encoding or formatting changed hash:\n%s\n%s", actual, expected)
		}
	}
}

func TestCanonicalTaskXMLRejectsInvalidDefinitions(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(`<Task>`), []byte(`<Other/>`), []byte(`<Task/><Task/>`), {0xff, 0xfe, 0x3c}} {
		if _, err := canonicalTaskXML(data); err == nil {
			t.Fatalf("accepted malformed XML %q", data)
		}
	}
}

func TestWindowsVerifyLegacyStateAllowsRegistrationDefaults(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := NewManager(WithExecutor(executor), WithPlatform("windows", 0), WithClock(time.Now))
	state, err := manager.InstallSpec(context.Background(), Spec{Name: "example", Action: ActionBackup, Backend: BackendWindows, Executable: "resticctl.exe", ConfigDir: directory, Expressions: []string{"5 1 * * *"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	state.RegisteredHash = ""
	name := `\resticctl\` + nativeID(state)
	executor.windowsTasks[name] = []byte(strings.Replace(string(executor.windowsTasks[name]), "<Settings>", "<Settings><Hidden>false</Hidden>", 1))
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatalf("unchanged legacy task: %v", err)
	}
	executor.windowsTasks[name] = []byte(strings.Replace(string(executor.windowsTasks[name]), "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1))
	if err := manager.Verify(context.Background(), state); !errors.Is(err, ErrDrift) {
		t.Fatalf("changed legacy task: %v", err)
	}
}

func TestWindowsInstallHashesRegistrationDefaults(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	executor.callback = func(call execution) {
		if call.name == "schtasks" && len(call.arguments) == 4 && call.arguments[3] == "/XML" {
			name := call.arguments[2]
			content := string(executor.windowsTasks[name])
			if !strings.Contains(content, "<Hidden>") {
				executor.windowsTasks[name] = []byte(strings.Replace(content, "<Settings>", "<Settings><Hidden>false</Hidden>", 1))
			}
		}
	}
	manager := NewManager(WithExecutor(executor), WithPlatform("windows", 0), WithClock(time.Now))
	state, err := manager.InstallSpec(context.Background(), Spec{Name: "example", Action: ActionBackup, Backend: BackendWindows, Executable: "resticctl.exe", ConfigDir: directory, Expressions: []string{"5 1 * * *"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if state.RegisteredHash == "" {
		t.Fatal("registered hash missing")
	}
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RegisteredHash != state.RegisteredHash {
		t.Fatal("registered hash not persisted")
	}
}

func canonicalTaskXML(data []byte) ([]byte, error) {
	definition, err := decodeTaskXML(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(definition.tokens)
}

func TestTaskHashIgnoresRegistrationMetadata(t *testing.T) {
	original := `<Task><RegistrationInfo><Date>2026-01-01</Date></RegistrationInfo><Settings><Enabled>true</Enabled></Settings></Task>`
	expected, err := canonicalTaskXML([]byte(original))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := canonicalTaskXML([]byte(strings.Replace(original, "2026-01-01", "2026-10-05", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatal("registration timestamp changed task hash")
	}
}
