package spawn

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnvFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteEnvFile(dir, map[string]string{"ERBRUS_URL": "http://127.0.0.1:7421", "ERBRUS_RUN_ID": "7"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "env"))
	if string(b) != "ERBRUS_RUN_ID=7\nERBRUS_URL=http://127.0.0.1:7421\n" {
		t.Errorf("env = %q", b)
	}
	got, err := ReadEnvFile(filepath.Join(dir, "cmd.json"))
	if err != nil || !reflect.DeepEqual(got, []string{"ERBRUS_RUN_ID=7", "ERBRUS_URL=http://127.0.0.1:7421"}) {
		t.Errorf("read = %q, %v", got, err)
	}
	if got, err := ReadEnvFile(filepath.Join(t.TempDir(), "cmd.sh")); err != nil || got != nil {
		t.Errorf("missing file: %q, %v", got, err)
	}
}
