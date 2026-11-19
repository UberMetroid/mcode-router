package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ExportUsage captures exact provider-reported tokens and USD cost.
type ExportUsage struct {
	Tokens  *int
	CostUSD *float64
	Failed  bool `json:"-"`
}

// AtomicWriteJSON serializes v to JSON atomically via a sibling temp file.
func AtomicWriteJSON(path string, v any, compact bool, priv bool) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if priv {
		_ = os.Chmod(parent, 0o700)
	}
	var data []byte
	var err error
	if compact {
		data, err = json.Marshal(v)
	} else {
		data, err = json.MarshalIndent(v, "", "  ")
		if err == nil {
			data = append(data, '\n')
		}
	}
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if priv {
		mode = 0o600
	}
	tmp, err := os.CreateTemp(parent, fmt.Sprintf(".%s.tmp.%d.*", filepath.Base(path), os.Getpid()))
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// CyclePatch specifies updates for ConfigureCycle.
type CyclePatch struct {
	Provider, QuotaPath      string
	Hours, CostBudget        *float64
	TokenBudget, MaxParallel *int
	ResetNow                 bool
	Effort, Model            *string
}
