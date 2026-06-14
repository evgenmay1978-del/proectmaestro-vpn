package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

func TestPutGetPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c := &Customer{Login: "a", SubToken: "t-a", Expires: time.Now().Add(time.Hour),
		Hy2: &subgen.Hy2Creds{Server: "h", Port: 8443, User: "a", Pass: "p"}}
	if err := st.Put(c); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// reopen → persisted
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := st2.ByToken("t-a")
	if err != nil {
		t.Fatalf("ByToken: %v", err)
	}
	if got.Login != "a" || got.Hy2 == nil || got.Hy2.User != "a" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if _, err := st2.ByLogin("a"); err != nil {
		t.Fatalf("ByLogin: %v", err)
	}
}

func TestActiveAndExtend(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	_ = st.Put(&Customer{Login: "x", SubToken: "tx", Expires: time.Now().Add(-time.Hour)})
	if c, _ := st.ByLogin("x"); c.Active() {
		t.Fatal("expired customer reported Active")
	}
	c, err := st.Extend("x", 48*time.Hour)
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if !c.Active() {
		t.Fatal("extended customer not Active")
	}
}

func TestExtendStacks(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	future := time.Now().Add(10 * 24 * time.Hour)
	_ = st.Put(&Customer{Login: "y", SubToken: "ty", Expires: future})
	c, _ := st.Extend("y", 30*24*time.Hour)
	// stacks onto the existing future expiry, not from now
	if c.Expires.Before(future.Add(29 * 24 * time.Hour)) {
		t.Fatalf("Extend did not stack onto future expiry: %v", c.Expires)
	}
}
