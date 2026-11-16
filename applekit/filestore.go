package applekit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type fileDeviceStore string

func (path fileDeviceStore) Load() (DeviceRecord, error) {
	raw, err := os.ReadFile(string(path))
	if err != nil {
		return DeviceRecord{}, err
	}
	defer clear(raw)
	return ReadDeviceRecord(raw)
}

func (p fileDeviceStore) Save(record DeviceRecord) error {
	state, err := installationFromRecord(record)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	defer clear(raw)
	return p.saveRaw(raw)
}

func (p fileDeviceStore) saveRaw(raw []byte) error {
	path := string(p)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".apple-device-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func defaultStatePath(configDir string) (string, error) {
	path := filepath.Join(configDir, "applekit", "apple-device.json")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	legacy := filepath.Join(configDir, "ipa_offline", "apple-device.json")
	raw, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	defer clear(raw)
	if err := ValidateDeviceState(raw); err != nil {
		return "", err
	}
	if err := fileDeviceStore(path).saveRaw(raw); err != nil {
		return "", err
	}
	return path, os.Remove(legacy)
}
