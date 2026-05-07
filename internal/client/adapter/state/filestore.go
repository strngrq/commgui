package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/strngrq/commgui/internal/client/port"
)

type FileStore struct {
	baseDir string
}

func NewFileStore() *FileStore {
	base := os.Getenv("COMMCLIENT_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config", "commclient")
	}
	return &FileStore{baseDir: base}
}

func NewFileStoreWithDir(dir string) *FileStore {
	return &FileStore{baseDir: dir}
}

func (f *FileStore) ProfileDir(profile string) string {
	return filepath.Join(f.baseDir, "profiles", profile)
}

func (f *FileStore) Load(profile string) (*port.State, error) {
	raw, err := os.ReadFile(filepath.Join(f.ProfileDir(profile), "state.json"))
	if err != nil {
		return nil, err
	}
	var st port.State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (f *FileStore) Delete(profile string) error {
	return os.RemoveAll(f.ProfileDir(profile))
}

func (f *FileStore) Save(profile string, st *port.State) error {
	dir := f.ProfileDir(profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o600)
}
