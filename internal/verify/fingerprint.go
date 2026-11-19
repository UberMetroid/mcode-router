package verify

import (
	"crypto/sha256"
	"fmt"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func fileFingerprint(full string, rem *int64) string {
	if st, err := os.Lstat(full); err != nil {
		return "missing"
	} else if st.Mode()&os.ModeSymlink != 0 {
		dest, _ := os.Readlink(full)
		return "link:" + dest
	} else if !st.Mode().IsRegular() {
		return fmt.Sprintf("mode:%o", st.Mode())
	} else if fh, err := os.Open(full); err != nil {
		return "unreadable"
	} else {
		defer fh.Close()
		capB, h := max(int64(0), min(*rem, snapMaxFileBytes)), sha256.New()
		if capB > 0 {
			n, cerr := io.Copy(h, io.LimitReader(fh, capB))
			*rem -= n
			if cerr != nil {
				return "unreadable"
			}
		}
		if st.Size() > capB {
			return fmt.Sprintf("trunc:%d:%o:%d:%x", st.Size(), st.Mode().Perm(), st.ModTime().UnixNano(), h.Sum(nil))
		}
		return fmt.Sprintf("%d:%o:%x", st.Size(), st.Mode().Perm(), h.Sum(nil))
	}
}

func runtimeFilter(extra ...string) func(string) bool {
	qp := fleet.QuotaFilePath("")
	cands := []string{fleet.ScratchDirPath(), fleet.SlotsRootPath(), qp, strings.TrimSuffix(qp, filepath.Ext(qp)) + ".lock"}
	for _, p := range extra {
		if strings.TrimSpace(p) != "" {
			cands = append(cands, p, strings.TrimSuffix(p, filepath.Ext(p))+".lock")
		}
	}
	var ex []string
	for _, p := range cands {
		if a, err := filepath.Abs(p); err == nil && a != "" {
			ex = append(ex, a)
		}
	}
	return func(ap string) bool {
		for _, r := range ex {
			if ap == r || strings.HasPrefix(ap, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
}
