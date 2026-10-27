package settings

import (
	"os"
	"testing"
)

func cleanup(t *testing.T) {
	t.Helper()
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(p) })
}

func TestSaveLoad(t *testing.T) {
	cleanup(t)
	in := Settings{Site: "hcfc", Time: "2026-01-01 10:00:00", Sessions: 3, Driver: "auto"}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Errorf("读取结果应与保存一致: %+v != %+v", got, in)
	}
}

func TestLoadCorruptJSONFails(t *testing.T) {
	cleanup(t)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("损坏的 settings.json 应当报错")
	}
}
